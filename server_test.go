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
func (echoTool) Execute(_ context.Context, args json.RawMessage) ([]mcp.Content, error) {
	var p struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return []mcp.Content{}, err
	}
	return []mcp.Content{mcp.Text(p.Message)}, nil
}

type annotatedEchoTool struct{}

func (annotatedEchoTool) Name() string        { return "annotated_echo" }
func (annotatedEchoTool) Description() string { return "echoes its message back; is annotated" }
func (annotatedEchoTool) Schema() mcp.InputSchema {
	return mcp.InputSchema{
		Type: "object",
		Properties: map[string]mcp.Property{
			"message": {Type: "string", Description: " text to echo"},
		},
		Required: []string{"message"},
	}
}
func (annotatedEchoTool) Annotations() mcp.Annotations {
	return mcp.Annotations{
		ReadOnlyHint:    mcp.HintTrue(),
		OpenWorldHint:   mcp.HintFalse(),
		DestructiveHint: mcp.HintFalse(),
		IdempotentHint:  mcp.HintTrue(),
	}
}
func (annotatedEchoTool) Execute(_ context.Context, args json.RawMessage) ([]mcp.Content, error) {
	var p struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return []mcp.Content{}, err
	}
	return []mcp.Content{mcp.Text(p.Message)}, nil
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
	srv.Register(annotatedEchoTool{})

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

	resp := byID(got)
	// initialize: id echoed, serverInfo reflects our constructor args.
	if string(resp["1"].ID) != "1" {
		t.Errorf("initialize id = %s, want 1", got[0].ID)
	}

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}

	mustUnmarshal(t, resp["1"].Result, &initResult)
	if initResult.ServerInfo.Name != "test-server" {
		t.Errorf("serverInfo.name = %q, want test-server", initResult.ServerInfo.Name)
	}
	if initResult.ProtocolVersion == "" {
		t.Errorf("initialize result missing protocolVersion")
	}

	// tools/list: our one registered tool shows up with its name.
	type ToolResult struct {
		Name        string           `json:"name"`
		Annotations *mcp.Annotations `json:"annotations,omitempty"`
	}
	var listResult struct {
		Tools []ToolResult `json:"tools"`
	}

	mustUnmarshal(t, resp["2"].Result, &listResult)
	if len(listResult.Tools) != 2 {
		t.Errorf("tools/list returned %+v, want two tools named echo and annotated_echo", listResult.Tools)
	}

	byName := make(map[string]ToolResult)
	for _, tool := range listResult.Tools {
		byName[tool.Name] = tool
	}
	tool, ok := byName["echo"]
	if !ok {
		t.Error("tool 'echo' not found in tools/list")
	}
	if tool.Annotations != nil {
		t.Errorf("tool 'echo' included annotations; it should omit annotations because none were provided; %v", tool)
	}
	tool, ok = byName["annotated_echo"]
	if !ok {
		t.Error("tool 'annotated_echo' not found in tools/list")
	}
	if tool.Annotations == nil {
		t.Errorf("tool 'annotated_echo' did not include annotations; %v", tool)
	}

	var callResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, resp["3"].Result, &callResult)
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
func (failTool) Execute(context.Context, json.RawMessage) ([]mcp.Content, error) {
	return []mcp.Content{}, errors.New("boom")
}

type panicTool struct{}

func (panicTool) Name() string            { return "panic" }
func (panicTool) Description() string     { return "always panics" }
func (panicTool) Schema() mcp.InputSchema { return mcp.InputSchema{Type: "object"} }
func (panicTool) Execute(context.Context, json.RawMessage) ([]mcp.Content, error) {
	panic("kaboom")
}

func byID(got []rpcResponse) map[string]rpcResponse {
	m := make(map[string]rpcResponse, len(got))
	for _, r := range got {
		m[string(r.ID)] = r
	}
	return m
}

func TestServerFailurePaths(t *testing.T) {
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo": { "name": "test","version":"0.0.1"}}}`,
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
	if len(got) != 5 {
		t.Fatalf("got %d responses, want 5", len(got))
	}

	resp := byID(got)
	// Unknown method: a PROTOCOL error (MethodNotFound).
	if r := resp["10"]; r.Error == nil || got[1].Error.Code != -32601 {
		t.Errorf("unknown method: got %+v, want error code -32601", got[1])
	}
	// Unknown tool: the message parse fine, so it's InvalidParams, not
	// MethodNotFound - a deliberate distinction.
	if r := resp["11"]; r.Error == nil || got[2].Error.Code != -32602 {
		t.Errorf("unknown tool: got %+v, want error code -32602", got[2])
	}

	// Tool returns an error: NOT a protocol error - a successful call
	// reporting isError:true.
	assertToolError(t, resp["12"], "boom")
	// Tool panics: recovered and surfaced the same way as a returned error
	assertToolError(t, resp["13"], "panicked")
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
	defer func() { _ = pw.Close() }()

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

func TestRejectsCallBeforeInitialize(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}` + "\n")
	var output strings.Builder
	srv := mcp.NewServer("test", "0.1.0", mcp.WithIO(input, &output))
	srv.Register(echoTool{})
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	got := decodeAll(t, output.String())
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	if got[0].Error == nil || got[0].Error.Code != protocolNotInitialized {
		t.Errorf("got %+v, want error code -32002", got[0])
	}
}

const protocolNotInitialized = -32002
