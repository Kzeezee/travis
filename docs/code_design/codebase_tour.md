# Travis Codebase Tour

Status: Phase 0 onboarding note.

`docs/DESIGN.md` is still normative. This document is the short code-facing map:
where files should go, which seams matter, and how to keep the code testable.

Doc index: [README.md](README.md).

The active code-design docs are grouped by purpose:

| Group | Files |
| --- | --- |
| Map | [codebase_tour.md](codebase_tour.md) |
| Rationale | [design_rationales.md](design_rationales.md) |
| Implementation Example | [ports_and_adapters.md](implementation_example/ports_and_adapters.md), [error_handling.md](implementation_example/error_handling.md) |
| Presentation layer | [presentation_layer.md](presentation_layer.md) |

Some packages below describe the intended architecture and may not exist yet. The
current repo is still early; use this tour as the target shape for new code.

## Before You Read

This document is easier to understand if you know these ideas:

- ports and adapters, also called hexagonal architecture;
- dependency direction, where decision code should not depend on infrastructure;
- basic Go structure: packages, `cmd/`, `internal/`, structs, interfaces, and errors;
- runtime validation, where raw external data becomes typed internal data;
- events versus direct work, where events observe facts and direct calls perform required work;
- presentation adapters, where UI code renders state and sends user intent inward.

## 1. Philosophy

Travis is an assistant that asks a model what to do and can also touch the real
machine. The codebase is shaped around one main idea: separate decisions from
effects.

Decision code answers questions:

```text
Should this tool call be allowed?
Should this turn continue?
Which context should be kept?
What should be redacted before logging?
```

Effect code touches the outside world:

```text
read files, write files, run commands, open network connections,
store sessions, talk to model providers, capture audio, play speech
```

The practical rules are:

- validate data when it enters the system;
- keep real effects behind adapters;
- test business logic without real providers or real effects;
- keep changes local to the module that owns the behavior.

For the rationale behind separating decisions from effects, see
[design_rationales.md](design_rationales.md).

## 2. User Flow

For the user-facing flow this architecture supports, see
[simple_userflow.md](../simple_userflow.md).

```text
User input
  -> terminal input
  -> agent loop
  -> provider port
  -> provider adapter
  -> model text or tool call
  -> tool registry
  -> tool preparation
  -> policy decision
  -> brokered effect
  -> result back to agent loop
  -> UI, session, voice, and eval observe progress
```

Events are observations. Direct calls perform required work. Do not use events to
trigger privileged effects.

## 3. Folder Placement

The repository is one Go module with private internal modules. There is no `pkg/`
directory yet because no external Go code imports Travis types. MCP servers talk to
Travis over stdio/HTTP, not Go imports. _Note that this structure is not set in stone
and should be changed whenever necessary_

```text
cmd/
  travis/           composition root: load config, wire modules, start CLI
  travis-broker/    broker process
  travis-audio/     audio helper process

internal/
  core/               shared seams: Provider, messages, ToolSpec, events, IDs, errors
  config/             TOML/env config, validation; owns per-OS paths
  log/                logging facade

  agent/
    loop/             assistant turn loop: ask model, run tools, continue or stop
    budget/           token, cost, and step decisions
    context/          context building and compaction planning
    prompt/           system prompt, persona, skills, and memory assembly
    repair/           malformed tool-call repair and provider quirks

  providers/
    openai/           OpenAI-compatible provider adapter
    ollama/           local model provider adapter
    cassette/         replay/test provider adapter
    auth/             provider login and token refresh flows

  tools/
    registry/         tool lookup, pruning, schemas, catalog
    builtin/          fs.write, grep, exec, fetch, ask_user, memory
    mcp/              generic MCP client and MCP tool adapter
    skills/           SKILL.md loader
    subagent/         nested agents exposed as tools

  security/
    policy/           pure allow, ask, deny decisions
    capabilities/     capability strings, glob matching, deny-list
    approvals/        approval decisions and durable rules
    broker/           broker client/server protocol
    sandbox/          OS sandbox implementations
    redaction/        secret and PII masking before logs, traces, export
    credentials/      secret storage helpers, if not provider-specific

  frontends/
    terminal/         Bubble Tea terminal UI
    voicein/          mic capture, PTT/VAD, STT; owns OS mic/device adapters
    voiceout/         speech selection, TTS, playback; owns OS TTS adapters

  state/
    session/          conversation persistence and resume
    journal/          undo snapshots
    memory/           storage support for memory if it outgrows the tool
    migrations/       state/schema migrations if needed

  eval/
    cassettes/        recorded provider responses
    traces/           event traces
    redteam/          attack cases
    scorecard/        cost, tokens, undo rate, other eval metrics

  testkit/
    tooltest/         tool contract tests
    providertest/     provider contract tests
    brokertest/       broker contract tests
    policytest/       policy fixtures and helpers

tests/
  integration/      cross-module/process tests
  e2e/              compiled CLI flows
  architecture/     import graph checks
```

When adding or sharing code, start by asking who owns the behavior:

```text
used by one module
  keep it in that module

shared by multiple modules, but one module owns the behavior
  keep it with the owner and expose the smallest needed type or function

shared by multiple modules, no clear owner, and crosses module seams
  consider internal/core

shared only to remove a small duplication
  usually keep the duplication until a real owner appears
```

Examples:

- Voice transcript types belong in `frontends/voicein` or `frontends/voiceout`.
- Tool registry types belong in `tools/registry`.
- Provider-neutral request/stream types belong in `core` because both
  `agent/loop` and `providers/*` cross that seam.
- Capability matching belongs in `security/capabilities`, not `core`.

Three-OS rule:

```text
Linux, macOS, and Windows are all MVP targets.

portable code
  should use shared interfaces, config.Paths(), filepath, and typed capabilities

OS-specific code
  should live only in the module that owns the OS detail
  examples: config paths, security transport/sandbox, voice input, voice output

build-tagged files
  add the interface first
  add same-shaped linux/darwin/windows files or stubs together
  document any degraded behavior instead of silently skipping an OS
```

Code shape:

```text
good
  business logic calls config.Paths(), security broker ports, or voice ports
  the owning adapter handles Linux, macOS, and Windows differences

bad
  business logic checks runtime.GOOS
  business logic hard-codes /tmp, ~, $HOME, or C:\ paths
```

Do not spread `runtime.GOOS`, hard-coded `/tmp`, `~`, `$HOME`, or `C:\` paths
through business logic. If OS behavior changes, the owning adapter should change,
not the modules that call it.

## 4. Pattern: Runtime Validation

Runtime validation means raw external data becomes typed internal data at the edge
of the system. After that edge, business logic should receive values whose basic
shape and invariants have already been checked.

This is the trust boundary:

```text
outside Travis, not trusted yet
  raw CLI args, env, TOML, model JSON, provider chunks, MCP payloads, files
    |
    v
entrypoint or adapter validates
    |
    v
inside Travis, trusted shape
  typed config, typed tool args, typed provider chunks, typed policy facts
```

Trusted shape does not mean the content is safe or user-approved. A fetched webpage,
workspace `AGENTS.md`, or MCP result can be valid data and still be untrusted prompt
content. Validation only means the code can rely on the value's structure and local
rules.

Business context:

```text
model asks to call fs.write with raw JSON
  -> tools/builtin parses JSON into WriteArgs
  -> tools/builtin validates required fields and size limits
  -> tools/builtin resolves the path into a policy resource
  -> tools/builtin returns PreparedWrite
  -> policy and FileWriter receive PreparedWrite facts, not raw JSON
```

Validate wherever data first crosses from outside Travis into Travis:

```text
process entrypoint: CLI flags, env, TOML config -> internal/config
tool boundary: model tool-call JSON             -> internal/tools/builtin
provider boundary: raw provider chunks         -> internal/providers/*
MCP boundary: tool specs and tool-call results  -> internal/tools/mcp
storage boundary: session files and cassettes   -> internal/state/session or internal/eval/cassettes
security boundary: paths and capabilities       -> internal/security/capabilities
```

Do not push raw external data deeper into the system:

```text
avoid:
  agent/loop passes json.RawMessage around
  policy reparses raw tool args
  broker receives unchecked paths

better:
  tools/builtin validates json.RawMessage once
  policy receives policy.Request
  broker receives WriteFileRequest with a resolved path
```

The code should make the trust boundary visible:

```go
// Raw external DTO: this mirrors model-provided JSON and is not trusted yet.
type WriteArgs struct {
    Path    string `json:"path"`
    Content string `json:"content"`
}

// Port for validation helpers: PrepareWrite needs these checks but not real effects.
type PrepareEnv interface {
    ResolvePath(string) (string, error)
    MaxWriteBytes() int
}

// Trusted internal DTO: after PrepareWrite returns, callers use this instead of raw JSON.
type PreparedWrite struct {
    Path     string
    Content  []byte
    Resource policy.Resource
}

func PrepareWrite(raw json.RawMessage, env PrepareEnv) (PreparedWrite, error) {
    var args WriteArgs

    // Boundary validation: raw model JSON becomes a typed WriteArgs DTO.
    if err := json.Unmarshal(raw, &args); err != nil {
        return PreparedWrite{}, fmt.Errorf("invalid fs.write args: %w", err)
    }

    // Boundary check: reject invalid shape before policy or filesystem effects.
    if args.Path == "" {
        return PreparedWrite{}, fmt.Errorf("path is required")
    }
    if len(args.Content) > env.MaxWriteBytes() {
        return PreparedWrite{}, fmt.Errorf("content exceeds write limit")
    }

    // Boundary check: turn user input into the canonical path policy will see.
    path, err := env.ResolvePath(args.Path)
    if err != nil {
        return PreparedWrite{}, err
    }

    // Trust handoff: from this point inward, use PreparedWrite instead of raw JSON.
    return PreparedWrite{
        Path:     path,
        Content:  []byte(args.Content),
        Resource: policy.FileResource(path),
    }, nil
}
```

After this point, `policy`, `agent/loop`, and `FileWriter` should use
`PreparedWrite` or smaller typed values derived from it. They should not reparse
`json.RawMessage` or guess whether `Path` was already checked.

If a later module needs another guarantee, encode that guarantee in the value it
receives. For example, if policy requires a resolved path, pass a `policy.Resource`,
not a raw string path.

## 5. Pattern: Ports And Adapters

A port is the small interface that decision code depends on. An adapter is concrete
infrastructure behind that interface.

```text
decision code
  -> port interface
  -> adapter
  -> provider, filesystem, broker, MCP process, terminal, audio device
```

The simplest Travis example is a file-writing tool. The business flow is:

```text
model asks to call fs.write
  -> agent/loop asks the tool registry for fs.write
  -> tools/builtin parses the raw JSON args
  -> tools/builtin prepares typed facts: path, content, policy resource
  -> security/policy decides allow, ask, or deny
  -> approved write goes through a FileWriter port
  -> broker adapter performs the real filesystem write
  -> result goes back to the agent loop
```

Read the code in three roles:

- Business logic: `tools/builtin` owns `fs.write`; it prepares the write and calls an
  adapter through a port.
- Port: `FileWriter` is the small interface the business logic needs.
- Adapter: the broker-backed writer performs the real filesystem effect.

The port is owned by the caller because the caller decides what effect it needs:

```go
type FileWriter interface {
    WriteFile(context.Context, WriteFileRequest) (WriteFileResult, error)
}
```

The business logic calls the adapter through the port. It does not import the broker
or call `os.WriteFile`:

```go
func (p PreparedWrite) Execute(ctx context.Context, writer FileWriter) (WriteFileResult, error) {
    req := WriteFileRequest{
        Path:    p.Path,
        Content: p.Content,
    }

    // Adapter call: production passes BrokerFileWriter; unit tests can pass a fake.
    return writer.WriteFile(ctx, req)
}
```

Any adapter that satisfies `FileWriter` can be passed in. Production passes the real
adapter; unit tests pass a fake.

The production adapter calls the broker:

```go
// Adapter of FileWriter: it has the WriteFile method required by the port.
type BrokerFileWriter struct {
    broker *BrokerClient
}

func (w BrokerFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    return w.broker.WriteFile(ctx, req)
}
```

The fake adapter records what business logic tried to do:

```go
// Adapter of FileWriter: tests use it because it has the same WriteFile method.
type fakeFileWriter struct {
    got WriteFileRequest
}

func (f *fakeFileWriter) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResult, error) {
    f.got = req
    return WriteFileResult{BytesWritten: len(req.Content)}, nil
}
```

That lets the test verify behavior without a broker process or real filesystem:

```go
func TestPreparedWriteExecute(t *testing.T) {
    writer := &fakeFileWriter{}
    prepared := PreparedWrite{Path: "notes.txt", Content: []byte("hello")}

    got, err := prepared.Execute(context.Background(), writer)

    if err != nil {
        t.Fatal(err)
    }
    if writer.got.Path != "notes.txt" || got.BytesWritten != 5 {
        t.Fatal("write was not routed through the port")
    }
}
```

The tool logic is decoupled because it knows only the `FileWriter` port, not the
broker implementation, `os.WriteFile`, or test fakes.

Our code for the model providers' adapters follow the same pattern:

```go
type Provider interface {
    Chat(context.Context, ChatRequest) (Stream, error)
}
```

`Provider` is the role the agent loop needs. `OpenAICompatibleProvider` is one
adapter. More OpenAI-compatible models usually mean more config profiles, not more
packages.

For the full compact example, read
[ports_and_adapters.md](implementation_example/ports_and_adapters.md).

## 6. Coupling Guardrails

The important rule is dependency direction:

```text
Outer details may depend inward.
Inner decision code must not depend on outer details.
```

Use this as the interaction map. The arrows show which parts call or use each other
while Travis runs; they do not mean every package imports every package it points at.

```text
cmd/travis/main.go
  wires concrete adapters and starts the process:
    internal/providers/openai
    internal/providers/ollama
    internal/tools/registry
    internal/tools/builtin
    internal/tools/mcp
    internal/security/broker
    internal/security/sandbox
    internal/frontends/terminal
    internal/frontends/voicein
    internal/frontends/voiceout
    internal/state/session
    internal/eval/traces
    internal/eval/scorecard
    |
    v
internal/frontends/terminal, internal/frontends/voicein, internal/frontends/voiceout
  collect user input and display observed progress
    |
    v
internal/agent/loop
  owns one assistant turn
    |
    +-> internal/core.Provider
    |     provider port implemented by internal/providers/*
    |
    +-> internal/tools/registry
    |     finds built-in, MCP, skill, and subagent tools
    |
    +-> internal/security/policy
    |     decides allow, ask, or deny from prepared facts
    |
    +-> tool-owned effect ports
    |     implemented by internal/security/broker adapters
    |
    +-> internal/state/session
    |     records conversation state and undo snapshots
    |
    +-> internal/core events
          observed by UI, session, voice, traces, and eval
```

In Go terms:

```go
// internal/agent/loop: owns one assistant turn and depends on the Provider port.
type TurnRunner struct {
    provider core.Provider
}
```

The agent loop (`internal/agent/loop`) should hold the provider port. It should
not hold an OpenAI HTTP client from `internal/providers/openai`.

One-way imports mean outward modules depend on inward modules:

```text
outward detail
  imports inward port or decision code

inward port or decision code
  does not import outward detail
```

Do it this way so changes in outward modules do not force inward modules to change.
Examples:

```text
internal/providers/openai
  may import internal/core.Provider and adapt an OpenAI-compatible HTTP client to it

internal/agent/loop
  should depend on internal/core.Provider,
  not internal/providers/openai or the OpenAI HTTP client

If provider HTTP changes:
  update internal/providers/openai
  do not change internal/agent/loop unless the provider port itself was wrong
-------------------------------------------------------------------------
internal/frontends/terminal
  may observe internal/core events

internal/security/policy
  should not import internal/frontends/terminal

If the terminal UI changes:
  update internal/frontends/terminal
  do not change internal/security/policy
```

Apply the same rule across the codebase:

```text
cmd/travis
  may import concrete adapters because it wires the program

internal/agent/loop
  should not import providers, Bubble Tea, MCP clients, or broker internals

internal/tools/builtin
  should not perform privileged filesystem, network, or command effects directly

internal/security/policy
  should not import provider, UI, MCP, broker, or filesystem implementations

internal/tools/mcp
  should adapt MCP servers into normal Travis tools

internal/frontends/terminal, internal/state/session, internal/frontends/voicein,
internal/frontends/voiceout, internal/eval/traces, internal/eval/scorecard
  may observe events; should not trigger privileged effects through events
```

This keeps change local:

```text
OpenAI-compatible stream format changes -> internal/providers/openai
Policy rules change                     -> internal/security/policy
Broker IPC changes                      -> internal/security/broker
Terminal UI changes                     -> internal/frontends/terminal
```

Folder names help readers, but they do not enforce architecture. `deps.yaml` is the
mechanical check that rejects imports in the wrong direction.

## 7. Pattern: Presentation Layer

The UI is an outer adapter. It should render app state, collect user input, and send
user intent inward. It should not own provider, tool, broker, MCP, or policy logic.

Business context:

```text
user types prompt in the terminal
  -> UI validates enough to give immediate feedback
  -> UI sends SubmitPrompt command through a command sink
  -> inner modules run the turn and publish events
  -> UI receives events and maps them into view models
  -> UI components render the view models
```

Use these presentation roles:

```text
Root model / screen
  orchestrates input, focus, commands, event subscription, and child components

Component
  owns rendering and small UI behavior such as cursor, focus, expanded state, and selection

Presentation mapper
  translates core events or domain values into UI-facing view models

Command sink
  port used by the UI to send user intent inward

Event subscription
  port used by the UI to observe facts from inner modules
```

The archived frontend notes in [archive/frontend](archive/frontend) came from React
skills. Keep the useful idea, not the React terms:

```text
React page      -> Travis root model or screen
React component -> Travis UI component
React hook      -> Travis presentation mapper or event projector
```

Rules:

```text
screens orchestrate
components render and manage local UI behavior
presentation mappers translate app data into display data
frontend validation is for feedback; backend entrypoints still validate
UI sends commands inward and observes events outward
UI must not import agent loop, provider, tool, broker, or MCP internals
```

For the fuller explanation and code example, read
[presentation_layer.md](presentation_layer.md).

## 8. Pattern: Events And Operations

Use an event when something already happened and other modules only need to observe
it. Use an operation when something must happen for the main flow to be correct.

Business context:

```text
model asks to call fs.write
  -> operation: policy decides allow, ask, or deny
  -> operation: approved FileWriter performs the write
  -> event: ToolCallFinished tells UI, session, voice, and eval what happened
```

Stakeholders in this pattern:

```text
Publisher / business logic
  internal/agent/loop or internal/tools/builtin publishes a fact after required work finishes

Event DTO
  plain typed data such as ToolCallFinished

Event sink / pub-sub adapter
  receives published events and delivers or queues them

Observer / subscriber / consumer
  internal/frontends/terminal, internal/state/session, internal/frontends/voiceout,
  internal/eval/traces, and internal/eval/scorecard react to events

Background worker
  only for async delivery; drains the event queue and calls observers

Logger
  records dropped events or observer failures; it is not an event consumer
```

The snippets keep these roles together for teaching. Real code should split them by
ownership: event DTOs with the shared event types, concrete observers in the module
that owns the reaction, and logging behind the logging facade.

Classify this code with two questions:

```text
Question 1: is this required work or an observed fact?
Question 2: must it finish now, or can it finish later?
```

That gives four cases:

```text
                         DIRECT OPERATION                  EVENT OBSERVATION

SYNCHRONOUS              call and wait for result/error     publish and call observers now
                         policy.Decide                     cheap UI/session updates
                         FileWriter.WriteFile

ASYNCHRONOUS             enqueue command/job                publish fact to queue
                         worker performs required work      worker calls observers later
                         returns job ID or status           traces, eval, voice
```

Use direct operations when the main flow needs ordering, approval, success, failure,
or a returned value. Use event observation when the work is a fact announcement and
observers can be added or removed without changing the main result.

What each case is for:

```text
Synchronous direct operation
  required work that must finish before the caller continues
  example: policy.Decide, FileWriter.WriteFile

Asynchronous direct operation
  required work that may finish later, but is still an explicit command/job
  example: enqueue a background compaction job and return a job ID

Synchronous event observation
  cheap observers that can run immediately after a fact is published
  example: update an in-memory UI status

Asynchronous event observation
  non-critical observers that can run later
  example: trace records, eval records, voice announcement
```

The code should make that difference visible:

```go
type Event interface{}

// Stakeholder: event DTO. This fact already happened; it is not a write instruction.
type ToolCallFinished struct {
    TurnID   string
    ToolName string
    Summary  string
}

// Stakeholder: event sink port. Business logic publishes facts here.
type EventSink interface {
    Publish(context.Context, Event)
}

type ToolRunner struct {
    policy PolicyDecider
    writer FileWriter
    events EventSink
}

func (r ToolRunner) RunWrite(ctx context.Context, prepared PreparedWrite) error {
    // Stakeholder: publisher/business logic. This required operation must finish first.
    decision, err := r.policy.Decide(ctx, prepared.PolicyRequest())
    if err != nil {
        return err
    }
    if !decision.Allowed {
        return decision.Err()
    }

    // Stakeholder: publisher/business logic. This required effect returns success or failure.
    result, err := prepared.Execute(ctx, r.writer)
    if err != nil {
        return err
    }

    // Stakeholder: publisher/business logic. It publishes one fact to the sink.
    r.events.Publish(ctx, ToolCallFinished{
        ToolName: "fs.write",
        Summary:  fmt.Sprintf("%d bytes written", result.BytesWritten),
    })
    return nil
}
```

Some events can be delivered asynchronously. That means the main flow publishes the
fact and moves on; a background worker calls observers later.

```go
// Stakeholder: observer interface. Event consumers implement Handle.
type Observer interface {
    Handle(context.Context, Event) error
}

// Stakeholder: logging dependency. This is not an observer or event DTO.
type Logger interface {
    Warn(string, ...any)
}

// Stakeholder: async pub-sub adapter. It owns the queue and observer list.
type AsyncEventSink struct {
    // queue is a Go channel: an in-memory queue of events waiting for the worker.
    queue chan Event

    // observers are subscribers that receive each event from the worker.
    observers []Observer

    // log records dropped events or observer failures.
    log Logger
}

// Publish is called by business logic after something happened.
func (s *AsyncEventSink) Publish(ctx context.Context, event Event) {
    // Async event: enqueue the fact. Do not perform required work here.
    select {
    case s.queue <- event:
    default:
        s.log.Warn("event dropped", "type", fmt.Sprintf("%T", event))
    }
}

func (s *AsyncEventSink) Run(ctx context.Context) {
    // Background worker: cmd/travis starts this in a goroutine.
    for {
        select {
        case <-ctx.Done():
            return
        case event := <-s.queue:
            for _, observer := range s.observers {
                // Observer call: failure is logged, not returned to the original tool call.
                if err := observer.Handle(ctx, event); err != nil {
                    s.log.Warn("event observer failed", "err", err)
                }
            }
        }
    }
}
```

`AsyncEventSink.Run` is the background worker. The composition root starts it:

```go
// cmd/travis/main.go
go events.Run(ctx)
```

Synchronous delivery is just normal function calls through the event sink:

```text
ToolRunner publishes ToolCallFinished
  -> EventSink calls TerminalObserver.Handle
  -> EventSink calls TraceObserver.Handle
```

They are still observers because `ToolRunner` does not call concrete observers
directly. It only publishes one fact to `EventSink`.

Use async events for non-critical observers such as UI status, traces, voice
announcements, or eval records. Do not use async events for policy decisions,
approval prompts, file writes, command execution, or provider calls.

Do not hide required work inside an event handler. If the caller needs a result,
ordering, approval, or error handling, write it as an operation. Events are for
observation and follow-up work that should not change the meaning of the main flow.

For why this pattern exists, read
[design_rationales.md section 3](design_rationales.md#3-why-write-event-based-code-this-way).
For event failure rules, read
[error_handling.md](implementation_example/error_handling.md#event-observer-failure).

## 9. Testing Architecture

The target is: unit test almost all business logic without starting providers,
MCP servers, broker processes, terminals, audio devices, or real filesystems.

That is possible when the code follows these shapes:

```text
parse raw external data into typed values
prepare decisions as returned values
accept dependencies as interfaces
return results and errors
keep real effects inside adapters
```

Example fake:

```go
type fakeProvider struct {
    stream core.Stream
}

func (f fakeProvider) Chat(context.Context, core.ChatRequest) (core.Stream, error) {
    return f.stream, nil
}
```

The fake crosses the same port as the real provider adapter, so the agent loop can
be tested without HTTP, API keys, or a live model.

Test the branch where the logic lives:

```text
internal/agent/loop          fake providers, fake registries, fake clocks
internal/providers/openai    request conversion, stream parsing, error translation
internal/tools/registry      lookup, pruning, schema/catalog behavior
internal/tools/builtin       JSON parsing, preparation, policy facts, fake broker ports
internal/security/policy     pure allow, ask, deny tests
internal/security/broker     focused IPC, filesystem, sandbox integration tests
internal/tools/mcp           mock protocol tests; pinned real-server tests only when needed
internal/frontends/terminal  synthetic messages and event snapshots
tests/e2e                    compiled CLI user flows
tests/architecture           import graph and portability checks
```

This does not mean every line is a unit test. Infrastructure adapters still need
integration tests. The point is that business rules, parsing, preparation, routing,
budgeting, policy, and provider-neutral turn behavior should not require real
infrastructure.

## 10. Where To Go Next

Read in this order:

1. [simple_userflow.md](../simple_userflow.md) for the user-facing story.
2. [codebase_tour.md](codebase_tour.md) for this short code map.
3. [Section 4](#4-pattern-runtime-validation) of this tour for runtime validation at system edges.
4. [Section 8](#8-pattern-events-and-operations) of this tour for events versus operations.
5. [ports_and_adapters.md](implementation_example/ports_and_adapters.md) for the compact pattern example.
6. [presentation_layer.md](presentation_layer.md) for presentation-layer rules.
7. [design_rationales.md](design_rationales.md) for the main separation principle.
8. [error_handling.md](implementation_example/error_handling.md) for cross-cutting error rules.
9. [DESIGN.md](../DESIGN.md) for the normative architecture record.
