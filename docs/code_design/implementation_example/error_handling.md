# Error Handling

Status: Phase 0 working note.

`docs/DESIGN.md` is still normative.

This page answers one question:

```text
When something goes wrong, should Travis return an error, return a value,
translate the error, or record it later?
```

Doc index: [README.md](../README.md). This belongs with
[ports_and_adapters.md](ports_and_adapters.md) because ports/adapters decide where
outside effects happen, and this page decides how failures from those effects move
through the code.

Tour sections:
[ports and adapters](../codebase_tour.md#5-pattern-ports-and-adapters),
[events and operations](../codebase_tour.md#8-pattern-events-and-operations).

The doc has two parts:

```text
1. Error scenarios
   what kind of situation happened?

2. Error translation functions
   how do adapters turn raw errors into Travis business errors?
```

The main rule:

```text
errors describe what happened
callers decide what to do
```

## 1. Error Scenarios

Start by identifying the kind of outcome. Do not begin with "should this be an
`error`?" Begin with "what happened?"

### Normal Business Result

```text
normal business result
  -> return a typed value
  Example: policy returns Deny because the path is not allowed.
  This is a normal answer, not a system failure.
```

Use typed values for normal answers:

```text
policy allowed
policy denied
approval accepted
approval rejected
budget exhausted
```

These should not be Go errors because they are part of the module's normal job.

### Synchronous Required Work Failed

```text
synchronous required work failed
  -> return a Go error now
  Example: fs.write was approved, but FileWriter.WriteFile failed.
  The caller is waiting, so it can receive the error immediately.
```

Use a Go error when Travis tried to do required work and that work failed while the
caller was waiting.

### Asynchronous Required Work Failed

```text
asynchronous required work failed
  -> return job accept/start errors now
  -> record worker failure later
  Example: if Travis cannot start the background job, return an error now.
  If the accepted job fails later, record the failure in job/session/trace state.
```

The split is:

```text
caller is waiting      -> return error now
caller is not waiting  -> record failure later
```

After async work has been accepted, the original call has already returned. There is
no caller left to receive the later worker error, so the worker must record it.

### External Dependency Failed

```text
external dependency failed
  -> adapter translates external error into Travis error
  Example: OpenAI rate limit becomes core.ProviderError{Kind: core.ErrExhausted}.
  Note: inner code should not know OpenAI/MCP/broker error shapes, or provider changes will leak everywhere.
```

Adapters are where Travis talks to outside systems:

```text
internal/ai              -> provider HTTP / SDK errors
internal/mcp             -> MCP protocol errors
internal/security/broker -> broker IPC / filesystem errors
```

Inner code should receive Travis error kinds, not SDK-specific error types.

### Event Observer Failure

```text
event observer failed
  -> log, record, retry, drop, or dead-letter
  -> do not fail the original operation
  Example: fs.write succeeded, but the trace observer failed while recording ToolCallFinished.
  Note: the original operation already succeeded, so observer failure should not rewrite history.
```

Ask:

```text
Did this failure affect whether the original user action succeeded?
```

If yes, it is required work. If no, it is event observation.

### Unexpected Programming Bug

```text
unexpected programming bug
  -> fail loudly in tests/dev
  -> log/alert and fail safely in production
  Example: code receives an impossible enum value after validation.
  Note: this means our code is wrong, so developers need visibility. In production, send it to the configured logging or error-reporting system instead of silently continuing.
```

Unexpected bugs are not business outcomes. They mean Travis reached a state the code
claimed should not be possible.

## 2. Writing Error Translation Functions

Start with where the code lives. The exact packages may change, but the relationship
should stay the same:

```text
internal/
  core/
    errors.go             # generic Travis error kinds: ErrInvalid, ErrTransient, ErrExhausted
    provider.go           # Provider port used by brain
    provider_errors.go    # provider-neutral business error: ProviderError

  ai/
    provider.go           # OpenAI-compatible adapter; calls the real provider client
    errors.go             # translates raw provider/client errors

  brain/
    turn_runner.go        # calls Provider and handles generic Travis error kinds
```

The runtime flow is:

```text
internal/brain/turn_runner.go
  calls provider.Chat(...)

internal/ai/provider.go
  calls the OpenAI-compatible client

provider/client returns raw error
  example: HTTP 429 rate limit

internal/ai/errors.go
  translateProviderError(...) maps the raw error to:
  core.ProviderError{Kind: core.ErrExhausted, Cause: rawError}

internal/brain/turn_runner.go
  checks errors.Is(err, core.ErrExhausted)
  decides whether to stop, wait, retry, or ask the user
```

Philosophy in plain English:

```text
The rest of Travis should not need to understand every error shape from every
provider, MCP server, broker call, OS call, or SDK.

Adapters are translators. They receive external errors, decide what those errors
mean in Travis language, and return errors that the rest of Travis can understand.

The translated error should carry two things:
  a generic kind, so callers can decide what to do
  business context, so logs and messages can explain what failed

The original external error should still be kept as the cause for debugging.
```

Go code does not catch exceptions. A function receives an `err`, classifies it, and
returns a better error to its caller.

### Step 1: Define Generic Travis Error Kinds

Generic kinds are the shared language callers use.

```go
// internal/core/errors.go
package core

import "errors"

var (
    ErrInvalid   = errors.New("invalid")   // Bad input or request shape.
    ErrTransient = errors.New("transient") // Temporary failure; retry may work.
    ErrExhausted = errors.New("exhausted") // Quota, budget, or capacity limit hit.
)
```

Keep this list small. A generic kind should mean something useful to multiple
callers.

```text
core.ErrInvalid
  caller may show the user what input is wrong

core.ErrTransient
  caller may retry

core.ErrExhausted
  caller may stop, wait, or ask the user to change provider/model
```

### Step 2: Compose A Business Error Around The Generic Kind

The provider port is owned by `internal/core`, so a provider-neutral business error
also belongs there. `internal/brain` may import `internal/core`; it should not import
the concrete `internal/ai` adapter just to understand an error.

```go
// internal/core/provider_errors.go
package core

type ProviderError struct {
    Provider string
    Kind     error
    Cause    error
}

func (e *ProviderError) Error() string {
    return e.Provider + ": " + e.Kind.Error()
}

func (e *ProviderError) Is(target error) bool {
    return target == e.Kind
}

func (e *ProviderError) Unwrap() error {
    return e.Cause
}
```

Read the fields like this:

```text
Provider
  business context: which provider failed?

Kind
  generic Travis meaning: invalid, transient, exhausted

Cause
  original provider/SDK error for logs and debugging
```

Other adapters can use the same shape with their own business names:

```text
internal/core            -> ProviderError
internal/mcp             -> MCPError
internal/security/broker -> BrokerError
```

If a business error is part of a port contract, put it with the port. If it is only
useful inside one adapter, keep it beside that adapter.

### Step 3: Translate Inside The Adapter

The adapter receives the raw error and returns a Travis-shaped error.

```go
// internal/ai/errors.go
package ai

// Uses generic errors and ProviderError from internal/core.
func translateProviderError(provider string, err error) error {
    if err == nil {
        return nil
    }

    // Default: unknown provider errors are treated as temporary.
    kind := core.ErrTransient
    switch {
    case isRateLimit(err):
        // Rate limits, quota, or capacity errors mean the request is exhausted.
        kind = core.ErrExhausted
    case isBadRequest(err):
        // Bad provider requests usually mean Travis sent invalid data.
        kind = core.ErrInvalid
    }

    return &core.ProviderError{
        Provider: provider,
        Kind:     kind,
        Cause:    err,
    }
}
```

This is where the actual translation happens:

```text
raw OpenAI-compatible error
  -> core.ErrExhausted / core.ErrInvalid / core.ErrTransient
  -> core.ProviderError with provider name and original cause
```

### Step 4: Use The Translator At The Adapter Edge

The provider adapter uses it at the edge:

```go
// internal/ai/provider.go
package ai

func (p *Provider) Chat(ctx context.Context, req core.ChatRequest) (core.Stream, error) {
    stream, err := p.client.Chat(ctx, toOpenAIRequest(req, p.model))
    if err != nil {
        return nil, translateProviderError(p.name, err)
    }
    return newStream(stream), nil
}
```

This keeps OpenAI-compatible HTTP or SDK details inside `internal/ai`.

### Step 5: Callers Handle The Generic Kind

Callers should make workflow decisions from generic Travis kinds. The brain handles
`core.ErrExhausted`; it does not need to know the provider's raw HTTP status or
SDK error type.

```go
// internal/brain/turn_runner.go
package brain

func (r TurnRunner) RunTurn(ctx context.Context, req core.ChatRequest) (TurnResult, error) {
    stream, err := r.provider.Chat(ctx, req)
    if errors.Is(err, core.ErrExhausted) {
        return TurnResult{Stopped: true, Reason: "provider exhausted"}, nil
    }
    if err != nil {
        return TurnResult{}, err
    }

    return r.consumeStream(ctx, stream)
}
```

If the caller needs provider context, it can inspect the provider-neutral business
error from `internal/core`:

```go
func logProviderFailure(log Logger, err error) {
    var providerErr *core.ProviderError
    if errors.As(err, &providerErr) {
        log.Warn("provider failed", "provider", providerErr.Provider, "err", providerErr.Cause)
    }
}
```

### Step 6: Keep Policy Out Of Error Types

Do not put workflow decisions inside the error type.

```go
// Avoid: retry policy does not belong inside core.ProviderError.
func (e *ProviderError) ShouldRetry() bool
```

The error should describe the failure. The caller decides whether to retry, stop,
ask the user, or display a message.
