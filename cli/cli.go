package main

import (
	"context"
	"encoding/json"
	"log"
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
			"path": {
				Type:        "string",
				Description: "Path to the file to read",
			},
		},
		Required: []string{"path"},
	}
}
func (r readFile) Annotations() mcp.Annotations {
	return mcp.Annotations{
		Title:         "Read file",
		ReadOnlyHint:  mcp.HintTrue(),
		OpenWorldHint: mcp.HintFalse(), // reads the local vault, no internet access
	}
}
func (r readFile) Execute(ctx context.Context, args json.RawMessage) ([]mcp.Content, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return []mcp.Content{}, err
	}
	bytes, err := os.ReadFile(p.Path)
	if err != nil {
		return []mcp.Content{}, err
	}
	return []mcp.Content{mcp.Text(string(bytes))}, nil
}

func main() {
	srv := mcp.NewServer(
		"test-server",
		"0.1.0",
		mcp.WithMaxConcurrency(12),
	)
	ctx := context.Background()
	srv.Register(readFile{})
	err := srv.Run(ctx)
	if err != nil {
		log.Fatal(err)
	}
}
