# Ports And Adapters Example

Status: Phase 0 architecture example.

This page shows the pattern Travis should use when business logic needs outside
effects. It is a teaching example, not a full implementation spec.

Doc index: [README.md](../README.md).

This is the only standalone worked example in `docs/code_design`. It expands
[codebase_tour.md section 5](../codebase_tour.md#5-pattern-ports-and-adapters); the
rationale is in
[design_rationales.md section 2](../design_rationales.md#2-why-use-ports-and-adapters).
Error rules for adapters are in [error_handling.md](error_handling.md).

Some packages and names below describe the intended architecture and may not exist
yet. Use the shape for new code; check the current tree before assuming a package is
already implemented.

## 1. The Pattern

The rule is:

```text
business logic
  -> port interface
  -> adapter
  -> filesystem, network, model provider, MCP process, subprocess, audio device
```

In Go, an adapter does not sit inside the port. The port is just an interface. A
concrete type becomes an adapter of that port when it has the method required by the
interface.

The composition root chooses which adapter to pass in:

```text
production
  pass BrokerFileWriter

unit test
  pass fakeFileWriter
```

## 2. User Flow: fs.write

Use `fs.write` as the main example because the effect is easy to understand:

```text
model asks to call fs.write
  -> brain asks the tool registry for fs.write
  -> coretools parses the raw JSON args
  -> coretools prepares typed facts: path, content, policy resource
  -> security/policy decides allow, ask, or deny
  -> approved write goes through a FileWriter port
  -> broker adapter performs the real filesystem write
  -> result goes back to the brain
```

The important split is this:

```text
business logic decides what should happen
adapter performs the outside-world effect
```

## 3. Roles Before Code

Read the code by stakeholder:

```text
Business logic
  coretools owns fs.write and calls a file-writing adapter through the port

Port
  FileWriter is the small interface coretools needs

Production adapter
  BrokerFileWriter satisfies FileWriter and talks to the broker

Test adapter
  fakeFileWriter also satisfies FileWriter and records the requested write
```

The value is decoupling: the business logic can be tested without importing the
broker, starting a broker process, or writing a real file.

## 4. The Port

The port is the small interface the business logic needs.

```go
type WriteFileRequest struct {
    Path    string
    Content []byte
}

type WriteFileResult struct {
    BytesWritten int
}

// Port: FileWriter is the only file-write ability fs.write business logic needs.
type FileWriter interface {
    WriteFile(context.Context, WriteFileRequest) (WriteFileResult, error)
}
```

`FileWriter` does not know about `BrokerFileWriter` or `fakeFileWriter`. That is the
point. The port describes the need; adapters provide concrete behavior.

## 5. The Business Logic

`PreparedWrite` is business logic. It knows the prepared path and content. It does
not know how the write happens.

```go
type PreparedWrite struct {
    Path     string
    Content  []byte
    Resource policy.Resource
}

// Business logic: Execute calls whatever adapter satisfies the FileWriter port.
func (p PreparedWrite) Execute(ctx context.Context, writer FileWriter) (WriteFileResult, error) {
    req := WriteFileRequest{
        Path:    p.Path,
        Content: p.Content,
    }

    // Adapter call: production passes BrokerFileWriter; unit tests pass fakeFileWriter.
    return writer.WriteFile(ctx, req)
}
```

This is dependency injection in plain Go: the function accepts the port, and the
caller passes the adapter.

## 6. The Production Adapter

`BrokerFileWriter` is the production adapter. It satisfies `FileWriter` because it
has the same `WriteFile(context.Context, WriteFileRequest)` method required by the
port.

```go
// BrokerClient is infrastructure used by the adapter; it is not the FileWriter port.
type BrokerClient struct {
    conn *rpc.Client
}

func (b *BrokerClient) SendWriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    return WriteFileResult{}, nil
}

// Production adapter of FileWriter: it satisfies the port by defining WriteFile.
type BrokerFileWriter struct {
    broker *BrokerClient
}

func (w BrokerFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    // Adapter implementation: translate the request into the broker protocol.
    return w.broker.SendWriteFile(ctx, req)
}
```

If broker IPC changes, the change should stay here. `PreparedWrite.Execute` should
not change because it only knows the `FileWriter` port.

## 7. The Fake Adapter

`fakeFileWriter` is the test adapter. It also satisfies `FileWriter` because it has
the same `WriteFile(context.Context, WriteFileRequest)` method.

```go
// Fake adapter of FileWriter: tests use it because it also defines WriteFile.
type fakeFileWriter struct {
    got WriteFileRequest
}

func (f *fakeFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    f.got = req
    return WriteFileResult{BytesWritten: len(req.Content)}, nil
}
```

The fake does not write a file. It records what the business logic tried to do.

## 8. The Test Value

The test crosses the same port as production code, but uses the fake adapter.

```go
func TestPreparedWriteExecute(t *testing.T) {
    writer := &fakeFileWriter{}
    prepared := PreparedWrite{Path: "notes.txt", Content: []byte("hello")}

    got, err := prepared.Execute(context.Background(), writer)

    if err != nil {
        t.Fatal(err)
    }
    if writer.got.Path != "notes.txt" || got.BytesWritten != 5 {
        t.Fatal("write was not routed through the FileWriter port")
    }
}
```

This proves the business logic without a real filesystem or broker process. The
broker adapter still needs focused integration tests, but the business rule does not.

## 9. Where Policy Fits

Policy should decide from prepared facts, not raw model JSON.

```go
// Business logic: PolicyRequest returns the facts policy needs before any effect runs.
func (p PreparedWrite) PolicyRequest() policy.Request {
    return policy.Request{
        Action:   "fs.write",
        Resource: p.Resource,
    }
}
```

The order should be:

```text
raw model JSON
  -> typed args
  -> prepared policy facts
  -> allow, ask, or deny
  -> brokered effect through FileWriter
```

The policy package should not import the broker or filesystem. The broker should not
decide whether a write is allowed.

## 10. Same Pattern: AI Provider

The AI provider uses the same pattern, but it is a different port.

```text
brain
  -> Provider port
  -> OpenAICompatibleProvider adapter
  -> model provider HTTP/SSE

coretools/fs.write
  -> FileWriter port
  -> BrokerFileWriter adapter
  -> broker/filesystem
```

`Provider` is not an adapter of `FileWriter`. They are separate ports for separate
business needs.

```go
// Port: Provider is the only model-calling ability the brain needs.
type Provider interface {
    Chat(context.Context, ChatRequest) (Stream, error)
}

type TurnRunner struct {
    provider Provider
}

// Business logic: RunTurn calls the model adapter through the Provider port.
func (r *TurnRunner) RunTurn(ctx context.Context, req ChatRequest) (Stream, error) {
    // Adapter call: production passes OpenAICompatibleProvider; tests pass fakeProvider.
    return r.provider.Chat(ctx, req)
}

// Production adapter of Provider: it satisfies the port by defining Chat.
type OpenAICompatibleProvider struct {
    client *Client
}

func (p *OpenAICompatibleProvider) Chat(ctx context.Context, req ChatRequest) (Stream, error) {
    stream, err := p.client.Chat(ctx, toOpenAIRequest(req))
    if err != nil {
        return nil, translateProviderError(err)
    }

    return newStream(stream), nil
}

// Fake adapter of Provider: tests use it because it also defines Chat.
type fakeProvider struct {
    stream Stream
}

func (f fakeProvider) Chat(ctx context.Context, req ChatRequest) (Stream, error) {
    return f.stream, nil
}
```

More models on the same OpenAI-compatible wire protocol usually mean more config
profiles, not more packages. Add another provider adapter only when the wire
protocol really differs.

## 11. Same Pattern: MCP

MCP also fits the same idea:

```text
MCP server process
  -> generic MCP client adapter
  -> Travis tool registry
  -> brain sees normal ToolSpec and ToolCall
```

The brain should not know which MCP server supplied a tool. Server-specific details
belong in config and the MCP adapter.

## 12. Current Code Versus Intended Shape

Current early code includes `cmd/travis` and a placeholder `internal/cli` dependency
pin. Most modules in this example are intended package boundaries for upcoming code.

That is why examples use compact names like `policy.Request`, `Provider`,
`BrokerFileWriter`, and `fakeFileWriter`: they show the architecture rule before all
packages exist.
