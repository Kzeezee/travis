# Design Rationales

Status: Phase 0 explanation note.

`docs/DESIGN.md` is still normative. This note explains why Travis uses a few
recurring code patterns. It is not a full implementation spec.

Doc index: [README.md](README.md). Main map: [codebase_tour.md](codebase_tour.md).

## 1. Why Separate Decision Code From Effect Code

Tour section: [4. Pattern: Runtime Validation](codebase_tour.md#4-pattern-runtime-validation).

Travis has code that decides what should happen:

```text
policy allow / ask / deny
budget continue / stop
context keep / drop
approval accepted / rejected
```

Travis also has code that touches the outside world:

```text
write a file
run a command
call a model provider
call an MCP server
save session state
play speech
```

The rule is:

```text
decision code should be pure when possible
effect code should sit behind a small interface
```

Without this rule, one function can end up parsing raw model JSON, deciding policy,
and writing the filesystem:

```go
func (t writeTool) Run(ctx context.Context, raw json.RawMessage) error {
    var args writeArgs
    if err := json.Unmarshal(raw, &args); err != nil {
        return err
    }

    // Policy decision mixed into tool execution.
    if strings.HasPrefix(args.Path, "/private") {
        return errors.New("denied")
    }

    // Real filesystem effect mixed into business logic.
    return os.WriteFile(args.Path, []byte(args.Content), 0644)
}
```

That looks simple, but it creates bad coupling:

```text
policy tests risk touching the filesystem
every tool may invent its own policy check
filesystem behavior leaks into business logic
invalid input, denied policy, and failed writes blur together
```

The improved shape is:

```text
parse raw input
  -> prepare typed facts
  -> policy decides allow / ask / deny
  -> approved effect runs through a port
```

This makes the code easier to test and safer to change.

## 2. Why Use Ports And Adapters

Tour section: [5. Pattern: Ports And Adapters](codebase_tour.md#5-pattern-ports-and-adapters).

A port is the small interface the business logic needs. An adapter is the concrete
implementation passed into that interface.

For `fs.write`, the business logic needs to write a file. It should not know whether
the write is done by the broker, a fake test adapter, or a future remote workspace
adapter.

Without a port, the business logic calls the concrete effect directly:

```go
func (p PreparedWrite) Execute(ctx context.Context) (WriteFileResult, error) {
    // Concrete effect: this directly touches the OS filesystem.
    if err := os.WriteFile(p.Path, p.Content, 0644); err != nil {
        return WriteFileResult{}, err
    }
    return WriteFileResult{BytesWritten: len(p.Content)}, nil
}
```

What is wrong with this:

```text
PreparedWrite now depends on OS filesystem details
unit tests need real files or heavy setup
broker rules can be bypassed by direct os.WriteFile
changing the broker or filesystem strategy changes business logic
```

With a port, the business logic depends only on the ability it needs:

```go
// DTO: data passed into the file-write operation.
type WriteFileRequest struct {
    Path    string
    Content []byte
}

// DTO: data returned from the file-write operation.
type WriteFileResult struct {
    BytesWritten int
}

// Port: the small file-writing interface PreparedWrite needs.
type FileWriter interface {
    WriteFile(context.Context, WriteFileRequest) (WriteFileResult, error)
}

// Business logic: calls whatever adapter satisfies FileWriter.
func (p PreparedWrite) Execute(ctx context.Context, writer FileWriter) (WriteFileResult, error) {
    // Adapter call: production passes BrokerFileWriter; tests pass fakeFileWriter.
    return writer.WriteFile(ctx, WriteFileRequest{
        Path:    p.Path,
        Content: p.Content,
    })
}
```

The production adapter performs the real effect:

```go
// Production adapter: satisfies FileWriter and talks to the broker.
type BrokerFileWriter struct {
    broker BrokerClient
}

func (w BrokerFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    return w.broker.WriteFile(ctx, req)
}
```

The fake adapter is used in unit tests:

```go
// Fake adapter: satisfies FileWriter without touching the real filesystem.
type fakeFileWriter struct {
    got WriteFileRequest
}

func (f *fakeFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    f.got = req
    return WriteFileResult{BytesWritten: len(req.Content)}, nil
}
```

What improves:

```text
business logic can be tested without real filesystem effects
broker IPC changes stay inside the broker adapter
security policy is harder to bypass
production and tests cross the same interface
new adapters can be added without rewriting PreparedWrite
```

The same pattern applies to AI providers:

```text
internal/core
  defines the Provider port and provider-neutral request/stream/error DTOs

internal/agent/loop
  depends on the Provider port while running the assistant turn

internal/providers/openaicompat
  contains the OpenAI-compatible HTTP/SSE adapter

internal/providers/ollama or internal/providers/cassette
  can be added as other Provider adapters
```

If OpenAI-compatible HTTP changes, `internal/providers/openaicompat` changes. If the
local model adapter changes, `internal/providers/ollama` changes. The agent loop
should not change unless the `Provider` port itself was wrong.

The same rule applies to Linux, macOS, and Windows details. OS-specific
implementations should live in outward modules such as `internal/security/sandbox`,
`internal/security/broker`, `internal/frontends/voicein`,
`internal/frontends/voiceout`, or `internal/config`. Inner modules should depend
on interfaces and DTOs, so an OS implementation change does not force agent or
policy code to change.

For the full teaching example, see
[ports_and_adapters.md](implementation_example/ports_and_adapters.md).

## 3. Why Write Event-Based Code This Way

Tour section: [8. Pattern: Events And Operations](codebase_tour.md#8-pattern-events-and-operations).

Travis uses events for observation. An event says something already happened.

```text
ToolCallFinished
  fact: the tool call already finished

FileWriter.WriteFile(...)
  operation: required work that must succeed or fail
```

The rule is:

```text
use an operation for required work
use an event for a fact other modules may observe
```

Without event observation, the tool runner can become coupled to every module that
wants to react:

```go
func (r ToolRunner) RunWrite(ctx context.Context, prepared PreparedWrite) error {
    result, err := prepared.Execute(ctx, r.writer)
    if err != nil {
        return err
    }

    // Tool execution now knows too many concrete reactions.
    r.terminal.ShowToolFinished("fs.write", result.BytesWritten)
    r.session.AppendToolResult("fs.write", result.BytesWritten)
    r.eval.RecordToolResult("fs.write", result.BytesWritten)

    return nil
}
```

What is wrong with this: `ToolRunner` now knows the method names and dependencies
of modules that should only observe the result.

```go
// ToolRunner before a terminal UI refactor.
r.terminal.ShowToolFinished("fs.write", result.BytesWritten)

// ToolRunner after a terminal UI refactor.
r.terminal.AddToolCard("fs.write", result.BytesWritten)
```

The tool still writes the same file. Only the terminal display changed, but
`ToolRunner` still had to change because it knows terminal UI details, even though
a UI refactor should not cause non-terminal code to change.

The same coupling shows up in other ways:

| Problem                                                         | Simple Example                                                                                            |
| --------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| Adding a new observer changes `ToolRunner`.                     | Adding telemetry means adding `r.telemetry.RecordToolResult(...)` inside `RunWrite`.                      |
| Tests need many unrelated dependencies.                         | A unit test for `fs.write` now needs fake terminal, fake session, and fake eval dependencies.             |

With event observation, the required work happens first. Then business logic
publishes one fact:

```go
// Event DTO: a fact that already happened.
type ToolCallFinished struct {
    ToolName     string
    BytesWritten int
}

type Event interface{}

// Event port: business logic publishes facts here.
type EventSink interface {
    Publish(context.Context, Event)
}

func (r ToolRunner) RunWrite(ctx context.Context, prepared PreparedWrite) error {
    result, err := prepared.Execute(ctx, r.writer)
    if err != nil {
        return err
    }

    // Event: this fact is published after the required operation succeeds.
    r.events.Publish(ctx, ToolCallFinished{
        ToolName:     "fs.write",
        BytesWritten: result.BytesWritten,
    })

    return nil
}
```

Observers live with the module that owns the reaction:

```text
internal/frontends/terminal
  observes ToolCallFinished to update the terminal UI

internal/state/session
  observes ToolCallFinished to record session history

internal/frontends/voiceout
  observes ToolCallFinished to speak important updates

internal/eval/traces or internal/eval/scorecard
  observes ToolCallFinished to record evaluation traces
```

What improves:

```text
tool execution only owns the required tool flow
UI, session, voice, and eval can change independently
new observers can be added without editing ToolRunner
observer failures do not change whether the original tool succeeded
tests can check tool execution without starting UI, voice, or eval code
```
