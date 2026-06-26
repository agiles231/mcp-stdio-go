package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/agiles231/mcp-stdio-go/protocol"
	"github.com/agiles231/mcp-stdio-go/transport"
)

// protocolVersion is the MCP version this server speaks. Match this to the clients you target
const protocolVersion = "2024-11-05"

type Server struct {
	name    string
	version string
	tools   map[string]Tool
	tp      *transport.Stdio
	log     *slog.Logger
}

type Option func(*Server)

func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.log = l }
}

func WithIO(r io.Reader, w io.Writer) Option {
	return func(s *Server) { s.tp = transport.New(r, w) }
}

func NewServer(name, version string, opts ...Option) *Server {
	s := &Server{
		name:    name,
		version: version,
		tools:   make(map[string]Tool),
		tp:      transport.New(os.Stdin, os.Stdout),
		log:     slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Server) Register(tools ...Tool) {
	for _, t := range tools {
		s.tools[t.Name()] = t
	}
}

func (s *Server) Run(ctx context.Context) error {
	for {
		raw, err := s.tp.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // client close the pipe: clean shutdown
			}
			return err
		}

		var req protocol.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			// We couldn't parse the envelope, so we don't know the id.
			_ = s.tp.Write(parseError())
			continue
		}

		resp := s.dispatch(ctx, &req)
		if resp == nil {
			continue // notification: protocol forbids a response
		}
		if err := s.tp.Write(resp); err != nil {
			s.log.Error("write response failed", "err", err)
			return err
		}
	}
}

func (s *Server) Dispatch(ctx context.Context, req *protocol.Request) *protocol.Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil // notification: acknowledge by doing nothing
	case "ping":
		return result(req.ID, struct{}{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		if req.IsNotification() {
			return nil // unknown notifications are ignored, never errored
		}
		return fail(req.ID, protocol.CodeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleInitialize(req *protocol.Request) *protocol.Response {
	return result(req.ID, protocol.InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      protocol.Implementation{Name: s.name, Version: s.version},
	})
}

func result(id json.RawMessage, payload any) *protocol.Response {
	return &protocol.Response{
		JSONRPC: protocolVersion,
		ID:      id,
		Result:  payload,
	}
}
func fail(id json.RawMessage, code int, msg string) *protocol.Response {
	return &protocol.Response{
		JSONRPC: protocolVersion,
		ID:      id,
		Error:   &protocol.Error{Code: code, Message: msg},
	}
}
