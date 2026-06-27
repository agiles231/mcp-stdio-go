# mcp-stdio-go

A small Go framework for building [Model Context Protocol](https://modelcontextprotocol.io)
servers that speak JSON-RPC 2.0 over stdio.

# Install

go get github.com/agiles231/mcp-stdio-go

# Usage

Implement the `Tool` interface, register it, and run:

```go
srv := mcp.NewServer("my-server", "1.0.0")
srv.Register(myTool{})
if err := srv.Run(context.Background()); err != nil {
    log.Fatal(err)
}
```

See example_test.go for a complete tool.

# Design

- Transport owns stdio. Only the transport laywer writes to stdout; it carries new-delimited JSON-RPC. Route all logging elsewher (the framework uses slog, defaulting to slog.Default()).
- Bounded concurrent dispatch - Tool calls run on goroutines capped by `WithMaxConcurrency` (default 8). A panicking tool is recovered, never crashing the server.
- Graceful shutdown. Cancel the context passed to `Run` to stop serving; in-flight tool calls are awaited.

## Concurrency Contract

`Execute` may be called concurrently - the same tool can be run multiple times at once. Implementations are responsible for their own thread-safety. Tools that mutate external resources should serialize access to the _same_ resources (e.g. a per-path mutex).

# License

MIT