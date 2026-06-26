package mcp

import (
	"io"
	"log/slog"
	"os"

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
