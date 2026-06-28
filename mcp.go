package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// Annotations are advisory, client-facing hints about a tool's behavior.
// They are used to reason about risk (i.e. auto-allow read-only tools,
// confirm destructive ones). They are HINTs, not a true security
// boundary.
//
// Hints are pointers so that JSON serialization will omit when not
// present, so the client can its own defaults.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

func HintTrue() *bool {
	b := true
	return &b
}
func HintFalse() *bool {
	b := false
	return &b
}

// Annotated is optional interface a Tool may implement to advertise
// behavioral hints. The server detects it by type assertion; tools
// that don't implement advertise no annotations
type Annotated interface {
	Annotations() Annotations
}

type Content struct {
	Type     string `json:"type"`               // "text", "image", "audio"
	Text     string `json:"text,omitempty"`     // for "text"
	Data     string `json:"data,omitempty"`     // base64 for "image" / "audio"
	MimeType string `json:"mimeType,omitempty"` // for "image" / "audio"
}

func Text(s string) Content { return Content{Type: "text", Text: s} }
func Image(data []byte, mimeType string) Content {
	return Content{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}
}
func Audio(data []byte, mimeType string) Content {
	return Content{
		Type:     "audio",
		Data:     base64.StdEncoding.EncodeToString(data),
		MimeType: mimeType,
	}
}

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
	Execute(ctx context.Context, args json.RawMessage) ([]Content, error)
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
