package mcp

import (
	"context"
	"encoding/json"
)

// Tool is the contract a tool author implements.
//
// Execute MAY be called concurrently. The server dispatches tool calls on
// separate goroutines (bounded by WithMaxConcurrency), so a single Tool's
// Execute can be running multiple times at once. Implementations are
// responsible for their own thread-safety:
//
//   - Guard any shared mutable state the tool holds.
//   - For tools that mutate external resources (e.g. files), serialize
//     access to the SAME resource yourself — typically per-resource
//     locking (a per-path mutex), not a single global lock.
//
// Name, Description, and Schema are treated as constant metadata; they may
// be queried at any time and must not depend on mutable state.
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
