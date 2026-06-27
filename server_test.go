package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agiles231/mcp-stdio-go"
)

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "echoes its message back" }
func (echoTool) Schema() mcp.InputSchema {
	return mcp.InputSchema{
		Type: "object",
		Properties: map[string]mcp.Property{
			"message": {Type: "string", Description: " text to echo"},
		},
		Required: []string{"message"},
	}
}
func (echoTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	return p.Message, nil
}

// rpcResponse is a black-box view of a JSON-RPC response - defined here
// rather than importing the internal protocol package, so the test sees
// only what a real client would see on the wire
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestServerSession(t *testing.T) {
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{
"name":"test","version":"0.0.1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}`,
	}
	input := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var output strings.Builder
	srv := mcp.NewServer("test-server", "0.1.0", mcp.WithIO(input, &output))
	srv.Register(echoTool{})

	// Input is fully buffered and finite, so Run consumes every request
	// and returns cleanly at EOF - no goroutine needed.
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Decode the newline-delimited responses off the captured output.
	got := decodeAll(t, output.String())

	// We sent 4 messages but one was a notification - it must produce no
	// response. This single assertion proves notification handling works.
	if len(got) != 3 {
		t.Fatalf("got %d responses, want 3 (notification must yield none)", len(got))
	}

	// initialize: id echoed, serverInfo reflects our constructor args.
	if string(got[0].ID) != "1" {
		t.Errorf("initialize id = %s, want 1", got[0].ID)
	}

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}

	mustUnmarshal(t, got[0].Result, &initResult)
	if initResult.ServerInfo.Name != "test-server" {
		t.Errorf("serverInfo.name = %q, want test-server", initResult.ServerInfo.Name)
	}
	if initResult.ProtocolVersion == "" {
		t.Errorf("initialize result missing protocolVersion")
	}

	// tools/list: our one registered tool shows up with its name.
	var listResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}

	mustUnmarshal(t, got[1].Result, &listResult)
	if len(listResult.Tools) != 1 || listResult.Tools[0].Name != "echo" {
		t.Errorf("tools/list returned %+v, want one tool named echo", listResult.Tools)
	}

	var callResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, got[2].Result, &callResult)
	if callResult.IsError {
		t.Error("tools/call reported isError, want success")
	}
	if len(callResult.Content) != 1 || callResult.Content[0].Text != "hello" {
		t.Errorf("tools/call content = %+v, want one text block 'hello'", callResult.Content)
	}
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
}

type failTool struct{}

func (failTool) Name() string            { return "fail" }
func (failTool) Description() string     { return "always returns an error" }
func (failTool) Schema() mcp.InputSchema { return mcp.InputSchema{Type: "object"} }
func (failTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("boom")
}

type panicTool struct{}

func (panicTool) Name() string            { return "panic" }
func (panicTool) Description() string     { return "always panics" }
func (panicTool) Schema() mcp.InputSchema { return mcp.InputSchema{Type: "object"} }
func (panicTool) Execute(context.Context, json.RawMessage) (string, error) {
	panic("kaboom")
}

func TestServerFailurePaths(t *testing.T) {
	requests := []string{
		`{"jsonrpc":"2.0","id":10,"method":"bogus/method"}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"fail","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"panic","arguments":{}}}`,
	}
	input := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var output strings.Builder

	// Discard logs so the recovered panic's stack trace doesn't clutter
	// test output - this is also a nice demo of WithLogger
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := mcp.NewServer("test", "0.1.0", mcp.WithIO(input, &output), mcp.WithLogger(quiet))
	srv.Register(failTool{}, panicTool{})

	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	got := decodeAll(t, output.String())
	if len(got) != 4 {
		t.Fatalf("got %d responses, want 4", len(got))
	}

	// Unknown method: a PROTOCOL error (MethodNotFound).
	if got[0].Error == nil || got[0].Error.Code != -32601 {
		t.Errorf("unknown method: got %+v, want error code -32601", got[0])
	}
	// Unknown tool: the message parse fine, so it's InvalidParams, not
	// MethodNotFound - a deliberate distinction.
	if got[1].Error == nil || got[1].Error.Code != -32602 {
		t.Errorf("unknown tool: got %+v, want error code -32602", got[1])
	}

	// Tool returns an error: NOT a protocol error - a successful call
	// reporting isError:true.
	assertToolError(t, got[2], "boom")
	// Tool panics: recovered and surfaced the same way as a returned error
	assertToolError(t, got[3], "panicked")
}

func decodeAll(t *testing.T, stream string) []rpcResponse {
	t.Helper()
	var out []rpcResponse
	dec := json.NewDecoder(strings.NewReader(stream))
	for {
		var r rpcResponse
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decoding response stream: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func assertToolError(t *testing.T, resp rpcResponse, wantSubstr string) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("got protocol error %+v, want a tool result with isError:true", resp.Error)
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, resp.Result, &res)
	if !res.IsError {
		t.Error("isError = false, want true")
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, wantSubstr) {
		t.Errorf("content = %+v, want text containing %q", res.Content, wantSubstr)
	}
}

func TestRunReturnsOnCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	var output strings.Builder
	srv := mcp.NewServer("test", "0.1.0", mcp.WithIO(pr, &output))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return within 2s of cancellation")
	}
}
