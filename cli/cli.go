package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/agiles231/mcp-stdio-go"
)

type readFile struct {
}

func (r readFile) Name() string        { return "Read file" }
func (r readFile) Description() string { return "Reads a file" }
func (r readFile) Schema() mcp.InputSchema {
	return mcp.InputSchema{
		Type: "object",
		Properties: map[string]mcp.Property{
			"path": mcp.Property{
				Type:        "string",
				Description: "Path to the file to read",
			},
		},
		Required: []string{"path"},
	}
}
func (r readFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	bytes, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func main() {
	srv := mcp.NewServer(
		"test-server",
		"0.1.0",
		mcp.WithMaxConcurrency(12),
	)
	ctx := context.Background()
	srv.Register(readFile{})
	srv.Run(ctx)
}
