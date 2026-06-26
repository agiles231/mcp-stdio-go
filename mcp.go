package mcp

import (
	"context"
	"encoding/json"
)

// Tool is the contract a tool author implements. The framework calls
// these to advertise the tool (Name/Description/Schema) and to run it
type Tool interface {
	Name() string
	Description() string
	Schema() InputSchema
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// InputSchema is a JSON Schema description of a tool's arguments. It
// serializes directly into the "inputSchema" field of an MCP tool
// descriptor.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property describes a single argument within an InputSchema.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	// TODO: Support full protocol
}
