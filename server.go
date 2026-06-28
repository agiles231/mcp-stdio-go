package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/agiles231/mcp-stdio-go/protocol"
	"github.com/agiles231/mcp-stdio-go/transport"
)

// protocolVersion is the MCP version this server speaks. Match this to the clients you target
const protocolVersion = "2024-11-05"

type Server struct {
	name         string
	version      string
	tools        map[string]Tool
	toolContexts map[string]context.CancelFunc
	tp           *transport.Stdio
	log          *slog.Logger
	initialized  bool

	sem chan struct{}  // bounds in-flight tool executions
	wg  sync.WaitGroup // tracks them for graceful shutdown
}

const defaultMaxConcurrency = 8

type Option func(*Server)

func WithMaxConcurrency(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.sem = make(chan struct{}, n)
		}
	}
}

func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.log = l }
}

func WithIO(r io.Reader, w io.Writer) Option {
	return func(s *Server) { s.tp = transport.New(r, w) }
}

func NewServer(name, version string, opts ...Option) *Server {
	s := &Server{
		name:         name,
		version:      version,
		tools:        make(map[string]Tool),
		toolContexts: make(map[string]context.CancelFunc),
		tp:           transport.New(os.Stdin, os.Stdout),
		log:          slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.sem == nil {
		s.sem = make(chan struct{}, defaultMaxConcurrency)
	}
	return s
}

func (s *Server) Register(tools ...Tool) {
	for _, t := range tools {
		if t == nil {
			panic("mcp: Register called with nil Tool")
		}
		name := t.Name()
		if name == "" {
			panic("mcp: Register called with a Tool thas has an empty Name()")
		}
		if _, exists := s.tools[name]; exists {
			panic(fmt.Sprintf("mcp: duplicate tool name %q", name))
		}
		s.tools[t.Name()] = t
	}
}

func (s *Server) Run(ctx context.Context) error {
	defer s.wg.Wait() // let in-flight tool calls finish writing before we return
	type readResult struct {
		raw json.RawMessage
		err error
	}
	reads := make(chan readResult)

	// Reader goroutine: the only thing that ever blocks on Read. It
	// offers each result to the loop, but abandons the send if the
	// context is cancelled first, so it can never block forever on a
	// receiver that has already gone away
	go func() {
		for {
			raw, err := s.tp.Read()
			select {
			case reads <- readResult{raw, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			// Unblock the reader's in-flight Read so the goroutine exits.
			_ = s.tp.Close()
			return ctx.Err()
		case r := <-reads:
			if r.err != nil {
				if errors.Is(r.err, io.EOF) {
					return nil // clean shutdown
				}
			}
			if err := s.handleRaw(ctx, r.raw); err != nil {
				s.log.Error("handling message", "err", err)
				return err
			}
		}
	}
}

func (s *Server) capabilities() map[string]any {
	caps := map[string]any{}
	if len(s.tools) > 0 {
		caps["tools"] = map[string]any{}
	}
	return caps
}

type UnmarshaledId string

func (id *UnmarshaledId) UnmarshalJSON(b []byte) error {
	// Trim quotes if the incoming value is already a JSON string
	str := string(b)
	if strings.HasPrefix(str, `"`) && strings.HasSuffix(str, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*id = UnmarshaledId(s)
		return nil
	}

	// Keep the raw JSON text representation for numbers, bools, objects, and arrays
	*id = UnmarshaledId(str)
	return nil
}

// handleRaw decodes one message, dispatches it, and writes the response
// (if any). A nil return from dispatch means a notification - no reply
func (s *Server) handleRaw(ctx context.Context, raw json.RawMessage) error {
	var req protocol.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.tp.Write((parseError()))
	}

	// tools/call is the only slow handler - run it concurrently. All
	// other methods are fast and lifecycle-sensitive, so they stay on the
	// main goroutine where shared state (initialized) is safe.
	if req.Method == "tools/call" {
		var id UnmarshaledId
		err := json.Unmarshal(req.ID, &id)
		if err != nil {
			fmt.Printf("req.ID: %v\n", req.ID)
			fmt.Printf("err: %v\n", err)
			return s.tp.Write(fail(req.ID, protocol.CodeInvalidParams, "invalid tool call params"))
		}
		toolCtx, cancel := context.WithCancel(ctx)
		s.registerRequestContext(string(id), cancel)
		return s.dispatchToolCall(toolCtx, &req)
	}
	resp := s.dispatch(ctx, &req)
	if resp == nil {
		return nil
	}
	return s.tp.Write(resp)
}

func (s *Server) dispatch(ctx context.Context, req *protocol.Request) *protocol.Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil // notification: acknowledge by doing nothing
	case "notifications/cancelled":
		s.handleToolCancel(req)
		return nil
	case "ping":
		return result(req.ID, struct{}{})
	case "tools/list":
		if !s.initialized {
			return fail(req.ID, protocol.CodeServerNotInitialized, "server not initialized")
		}
		return s.handleToolsList(req)
	default:
		if req.IsNotification() {
			return nil // unknown notifications are ignored, never errored
		}
		return fail(req.ID, protocol.CodeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) handleToolCancel(req *protocol.Request) {
	var params struct {
		RequestId UnmarshaledId `json:"requestId"`
	}
	err := json.Unmarshal(req.Params, &params)
	if err != nil {
		s.log.Warn("Cancel was requested but unmarshaling parms failed", "err", err.Error())
	}
	_, err = s.cancelToolCall(string(params.RequestId))
	if err != nil {
		s.log.Warn("Cancel was requested cancel failed", "err", err.Error())
	}
}

func (s *Server) registerRequestContext(id string, cancel context.CancelFunc) {
	if _, ok := s.toolContexts[id]; ok {
		// TODO return error
		return
	}
	s.toolContexts[id] = cancel
}

func (s *Server) cancelToolCall(id string) (bool, error) {
	cancel, ok := s.toolContexts[id]
	if !ok {
		// TODO handle failure
		return false, nil
	}
	cancel()
	return true, nil
}
func (s *Server) dispatchToolCall(ctx context.Context, req *protocol.Request) error {
	// Validation happens on main goroutine: it reads shared state
	// (initialized, the tools map) and is cheap. Only Execute is deferred.
	if !s.initialized {
		return s.tp.Write(fail(req.ID, protocol.CodeServerNotInitialized, "server not initialized"))
	}
	var p protocol.ToolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.tp.Write(fail(req.ID, protocol.CodeInvalidParams, "invalid tool call params"))
	}
	tool, ok := s.tools[p.Name]
	if !ok {
		return s.tp.Write(fail(req.ID, protocol.CodeInvalidParams, "unknown tool: "+p.Name))
	}

	// Acquire a slot. If we're at capacity this blocks the read loop -
	// deliberate backpressure that bounds memory and goroutine count.
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { <-s.sem }()

		contents, err := s.safeExecute(ctx, tool, p.Arguments)
		var res protocol.ToolCallResult
		if err != nil {
			res = protocol.ToolCallResult{
				Content: []protocol.Content{{Type: "text", Text: err.Error()}},
				IsError: true,
			}
		} else {
			res = protocol.ToolCallResult{Content: toWireContent(contents)}
		}
		resp := result(req.ID, res)
		// Write is mutex-guarded in the transport - this is eactly the
		// concurrent-writer case that mutex was built for
		if werr := s.tp.Write(resp); werr != nil {
			s.log.Error("writing tool response", "tool", p.Name, "err", werr)
		}
	}()
	return nil
}

func (s *Server) handleInitialize(req *protocol.Request) *protocol.Response {
	var params protocol.InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(req.ID, protocol.CodeInvalidParams, "invalid initialize params")
		}
	}

	// We advertize exactly one version. If the client asked for another,
	// we still answer with ours - the client decides whether to proceed.
	if params.ProtocolVersion != "" && params.ProtocolVersion != protocolVersion {
		s.log.Warn("client.requested a different protocol version",
			"client", params.ProtocolVersion, "server", protocolVersion)
	}
	s.initialized = true
	return result(req.ID, protocol.InitializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.capabilities(),
		ServerInfo:      protocol.Implementation{Name: s.name, Version: s.version},
	})
}

func (s *Server) handleToolsList(req *protocol.Request) *protocol.Response {
	descriptors := make([]protocol.ToolDescriptor, 0, len(s.tools))
	for _, t := range s.tools {
		d := protocol.ToolDescriptor{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		}
		if a, ok := t.(Annotated); ok {
			ann := a.Annotations()
			d.Annotations = &ann
		}
		descriptors = append(descriptors, d)
	}
	return result(req.ID, protocol.ToolsListResult{Tools: descriptors})
}

func toWireContent(cs []Content) []protocol.Content {
	out := make([]protocol.Content, len(cs))
	for i, c := range cs {
		out[i] = protocol.Content{
			Type:     c.Type,
			Text:     c.Text,
			Data:     c.Data,
			MimeType: c.MimeType,
		}
	}
	return out
}

// safeExecute runs a tool's Execute, converting any panic into an error
// so a single misbehaving tool can never crash the server.
func (s *Server) safeExecute(ctx context.Context, tool Tool, args json.RawMessage) (content []Content, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("tool panicked",
				"tool", tool.Name(),
				"panic", r,
				"stack", string(debug.Stack()),
			)
			err = fmt.Errorf("tool %q panicked: %v", tool.Name(), r)
		}
	}()
	return tool.Execute(ctx, args)
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

func parseError() *protocol.Response {
	// Spec: when the id can't be determined, it must be null (literally),
	// not omitted. So we set the raw bytes "null" explicitly.
	return &protocol.Response{
		JSONRPC: protocol.VERSION,
		ID:      json.RawMessage("null"),
		Error:   &protocol.Error{Code: protocol.CodeParseError, Message: "parse error"},
	}
}
