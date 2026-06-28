package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/agiles231/mcp-stdio-go"
)

type greetTool struct{}

func (greetTool) Name() string        { return "greet" }
func (greetTool) Description() string { return "greets a person by name" }
func (greetTool) Schema() mcp.InputSchema {
	return mcp.InputSchema{
		Type: "object",
		Properties: map[string]mcp.Property{
			"name": {Type: "string", Description: "who to greet"},
		},
		Required: []string{"name"},
	}
}
func (greetTool) Execute(_ context.Context, args json.RawMessage) ([]mcp.Content, error) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	return []mcp.Content{
		mcp.Text(fmt.Sprintf("Hello, %s", p.Name))}, nil
}

func ExampleServer() {
	srv := mcp.NewServer("greeter", "1.0.0")
	srv.Register(greetTool{})
	if err := srv.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
