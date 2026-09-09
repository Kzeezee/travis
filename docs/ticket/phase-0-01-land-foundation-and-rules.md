# Phase 0 / 01: Land Minimal Foundation And Rules

**What to build:** Review and land the Phase 0 foundation slice that gives Travis its first shared app seams, config-owned path behavior, logging facade, and agent-facing project rules. The landed branch should leave Travis with the same user-visible version command, but with enough internal vocabulary for provider, tool, agent-loop, terminal, state, and eval work to begin without inventing conflicting local DTOs.

**Blocked by:** None (can start immediately).

**Status:** done

**Landed in:** PR #2, after PR #1 merged first.

- [x] The active docs are in sync with the agreed Phase 0 baseline, except for the intentional agent-rule additions.
- [x] Agent instructions list the read-first docs and hard rules for one module, no public package tree, internal shared seams, OS ownership, path handling, import direction, ports/adapters, validation, events, formatting, and commit prefixes.
- [x] Shared app seams include provider-neutral messages, provider streaming DTOs, tool DTOs, event facts, capability DTOs, clocks, and generic error behavior without freezing large future interfaces.
- [x] Config owns platform reporting and per-OS path defaults, including override-root behavior and portable logical path resolution.
- [x] The version command continues to report the platform through the config-owned seam.
- [x] Logging dependencies can cross module seams through a small project facade.
- [x] Focused tests cover behavior in errors, clocks, path resolution, logging, and the version command.
- [x] The local Go test suite passes.
- [x] The config package compile-checks for Linux and Windows build tags.
- [x] No CI/lint track work or broad future package skeleton is included in this ticket.

## Implementation Notes

This ticket landed the smallest useful Phase 0B/0D foundation. It is not the whole
Phase 0. It creates only the packages needed by the current PR: `internal/core`,
`internal/config`, and `internal/log`, then updates the existing `travis version`
command to respect the new OS-ownership rule.

### Non-Code Alignment

The active docs were restored to match `feat/phase-0-frozen-contracts`, then
`AGENTS.md` was intentionally extended with the project rules this branch needs.
That means `docs/` and `CLAUDE.md` should match the branch baseline, while
`AGENTS.md` should differ by the added `Read first` and `Hard rules` sections.

The important rule changes captured in `AGENTS.md` are:

- exactly one Go module;
- no `pkg/`;
- shared cross-module DTOs and seams go in `internal/core`;
- do not create the whole future folder tree;
- keep OS checks in OS-owning modules;
- use `filepath` and `config.BuildCurrentPathSet()` rather than hard-coded OS paths;
- inner modules must not import outward modules;
- use ports/adapters for external effects;
- validate data at entrypoints and trusted boundaries;
- events describe facts that already happened;
- do not mix broad formatting with content edits;
- use conventional commit prefixes.

### `internal/core`: Shared App Vocabulary

`internal/core` is the shared app seam package. It does not implement features. It
defines the small DTOs and interfaces that multiple future modules need to agree on
before provider, tool, agent-loop, terminal, state, and eval work begins.

Files added:

- `doc.go`
- `errors.go`
- `id.go`
- `capability.go`
- `clock.go`
- `message.go`
- `provider.go`
- `tool.go`
- `events.go`
- focused tests for error and clock behavior

#### Error Kinds And Provider Error

`errors.go` adds four generic error kinds:

- `ErrInvalid`
- `ErrUnsupported`
- `ErrTransient`
- `ErrExhausted`

These are deliberately broad. They are meant to be useful across modules without
forcing every caller to understand a concrete provider, tool, broker, or OS error.

`ProviderError` wraps:

- the provider name;
- the generic kind;
- the original cause.

The key behavior is that callers can use normal Go error matching against the
generic kind while logs and debuggers can still reach the original cause. For
example, an OpenAI 429 can become a provider error whose kind is exhausted, while
still carrying the raw HTTP/client error as the cause.

Review questions:

- Are the generic kinds still small enough to be shared vocabulary rather than a
  full policy system?
- Does `ProviderError` keep decision-making out of `core`? It should classify, not
  decide retry policy.
- Does the error test prove matching, unwrapping, and stable text without testing
  implementation details?

#### IDs

`id.go` adds `type ID string` and a `String()` method.

This intentionally does not generate IDs. Generation can later live in another
package that may use a ULID dependency. Keeping generation out of `core` prevents
`core` from taking on a dependency before there is a concrete caller.

Review questions:

- Is `ID` enough for DTOs to reference turns, tool calls, events, and results?
- Is ID generation correctly deferred?

#### Capabilities

`capability.go` adds `CapabilityKind` constants and the `Capability` DTO:

- `fs.read`
- `fs.write`
- `exec`
- `net.http`
- `audio.capture`
- `audio.play`
- `secret`

The DTO has a kind and a scope. It does not match globs, resolve paths, decide
policy, inspect commands, validate raw input, or resolve secrets.

That split is intentional:

- `core` says what authority is being requested;
- future `security` modules decide whether the authority allows an operation.

Review questions:

- Are these capability kinds shared enough to belong in `core`?
- Is matching logic absent from `core`?
- Is the scope still an opaque string so policy can own interpretation later?

#### Clocks

`clock.go` adds a minimal `Clock` interface with `Now()`, plus:

- `SystemClock` for production;
- `FixedClock` for deterministic tests.

This is the smallest time seam that helps Phase 1 tests. It avoids spreading direct
wall-clock calls through later modules.

Review questions:

- Is `Now()` enough for the current foundation?
- Is timer/scheduler behavior correctly deferred until a caller needs it?
- Does the fixed-clock test cover real behavior?

#### Messages And Parts

`message.go` adds provider-neutral conversation DTOs:

- roles: system, user, assistant, tool;
- part kinds: text, tool call, tool result;
- `Message`;
- `Part`.

This gives future provider, agent-loop, tool, session, and trace modules one shared
shape for conversation data without binding the app to a vendor wire format.

Review questions:

- Are the message DTOs provider-neutral?
- Are they small enough for Phase 1?
- Is vendor-specific translation left for provider adapters?

#### Tool DTOs

`tool.go` adds:

- `ToolSpec`;
- `ToolCall`;
- `ToolResult`.

These are shared DTOs, not a full tool execution interface. They let providers,
the agent loop, registries, built-in tools, MCP adapters, frontends, state, and eval
talk about tool descriptions, requested calls, and returned results.

The full `Tool` interface, `ToolIO`, registry behavior, schema validation, and
cancellation rules are deliberately not implemented here. Those should be shaped by
the tool registry and built-in tool tickets.

Review questions:

- Are these DTOs enough to describe tool exposure and lifecycle at a high level?
- Is the actual tool execution seam correctly deferred?
- Does `ToolSpec` carry capabilities as data without making `core` enforce them?

#### Provider Stream DTOs

`provider.go` adds the minimal provider seam:

- `Provider.Chat(context.Context, ProviderRequest) (Stream, error)`;
- `ProviderRequest`;
- `Stream`;
- `Chunk`;
- `ToolCallFragment`;
- `Usage`;
- `StopReason`.

The design keeps this interface intentionally smaller than the long-term provider
shape. It does not include counting, budgeting, embeddings, provider capability
reporting, or provider naming yet. Those should be added only when concrete Phase 1
callers need them.

The stream distinction matters:

- provider adapters return chunks from a provider stream;
- the agent loop will later interpret those chunks;
- the agent loop can then publish event DTOs to the internal event stream.

Review questions:

- Is `Chat` the one operation Phase 1 needs first?
- Are chunk fields neutral enough for both local and remote OpenAI-compatible
  providers?
- Are the omitted provider methods genuinely deferred rather than accidentally
  forgotten?

#### Event DTOs

`events.go` adds:

- `EventType`;
- `Event`;
- `EventSink`;
- `EventHeader`;
- `UserMessageSubmitted`;
- `AssistantTextStreamed`;
- `ToolCallStarted`;
- `ToolCallFinished`;
- `TurnCompleted`.

Each event is a DTO describing one fact that already happened. Many event DTOs can
be delivered through a future bus or channel, but an event is not itself the stream.

This gives the terminal frontend an observation language:

- user message submitted;
- assistant text chunk streamed;
- tool call started;
- tool call finished;
- turn completed.

The frontend can render those facts without importing the agent loop. The same facts
can later be observed by session storage, eval traces, voice output, and logs.

The real bus is intentionally not implemented here. There is no subscription model,
buffering, ordering guarantee, async worker, or dropped-event behavior yet.

Review questions:

- Are these events facts rather than commands?
- Is required work still represented as operations rather than events?
- Is the terminal frontend able to render the first assistant loop from these facts?
- Is full bus behavior correctly deferred?

### `internal/config`: OS Ownership And Path Resolution

`internal/config` is the first OS-owning module. It centralizes platform reporting
and config path calculation so other modules do not branch on OS behavior or embed
home-directory/path assumptions.

Files added:

- `doc.go`
- `platform.go`
- `paths.go`
- `paths_linux.go`
- `paths_darwin.go`
- `paths_windows.go`
- `paths_other.go`
- focused tests for path behavior

#### Platform Reporting

`platform.go` exposes `Platform()`, which returns the Go OS/architecture string.
This keeps direct runtime platform access in `internal/config`.

The existing `travis version` command now calls this helper instead of constructing
the platform string in the command package.

Review questions:

- Is `internal/config` the right owner for platform reporting?
- Does this avoid spreading `runtime.GOOS` and `runtime.GOARCH` into command or
  business logic packages?

#### PathSet

`paths.go` defines `PathSet` with:

- config directory;
- state directory;
- cache directory;
- system config file;
- workspace directory;
- workspace config file.

`BuildCurrentPathSet()` gathers the current user's home directory and current working directory,
then delegates to testable path resolution logic. This keeps OS and environment
behavior behind the config module.

Review questions:

- Are these the right path roles for the minimal foundation?
- Is full TOML loading correctly out of scope?

#### `TRAVIS_HOME`

If `TRAVIS_HOME` is set, config derives the major directories from that absolute
root:

- config uses the root itself;
- state uses a state child;
- cache uses a cache child;
- system config file uses a config file under the root.

This is useful for tests, local experiments, and users who want a single Travis
home location.

Relative `TRAVIS_HOME` is rejected. That prevents a relative environment value from
silently depending on the process working directory.

Review questions:

- Should `TRAVIS_HOME` override all platform defaults?
- Is rejecting relative roots the right validation behavior?

#### Platform Defaults

The platform-specific defaults are split into build-tagged files:

- Linux uses XDG-style roots when provided, otherwise home-relative fallbacks.
- macOS uses Library/Application Support and Library/Caches style locations.
- Windows uses AppData, LocalAppData, and ProgramData style locations.
- Unknown platforms compile with conservative home-relative defaults.

The Linux and macOS system config path avoids hard-coded slash strings by deriving
the root from the OS path separator before joining path components.

Review questions:

- Are the platform defaults consistent with the design docs?
- Are the build tags shaped so only one platform implementation is compiled?
- Is the unknown-platform fallback acceptable for now?

#### Logical Path Resolution

`PathSet.Resolve` accepts portable config path values and resolves them into
concrete paths. Supported prefixes are:

- `config:`
- `state:`
- `cache:`
- `workspace:`

Absolute paths are cleaned and returned. Relative paths resolve under the workspace.
Unknown prefixes are rejected as invalid input. Slash styles are normalized before
resolution so config can be portable across OSes.

Review questions:

- Is resolving relative paths under the workspace the right default?
- Should unknown prefixes fail now instead of being treated as relative paths?
- Is it correct that secret references are not accepted as path prefixes here?

### `internal/log`: Logging Facade

`internal/log` introduces the project logging facade.

Files added:

- `doc.go`
- `log.go`
- focused test for writing through the facade

The facade exposes:

- `Debug`;
- `Info`;
- `Warn`;
- `Error`.

The implementation wraps Go's structured logger. `New(nil)` falls back to the
default structured logger. `Nop()` returns a logger backed by a discard handler, so
tests can satisfy logging dependencies without producing output.

Review questions:

- Is the facade small enough?
- Do modules get the logging seam they need without taking a direct dependency on a
  concrete logging setup?
- Is `Nop()` useful for tests without becoming a global logger pattern?

### `cmd/travis`: Version Command Change

The version command keeps the same user-facing behavior: it prints the Travis
version, optional VCS stamp, Go runtime version, and platform.

The internal change is that platform formatting moved from the command package to
the config package. The command still imports `runtime` for the Go runtime version
and `runtime/debug` for build information, but no longer owns OS/architecture
formatting.

The test was updated to compare against `config.Platform()`.

Review questions:

- Does the command remain a composition root with only wiring/flags/simple command
  handling?
- Is it acceptable that `cmd/travis` still imports `runtime` for `runtime.Version()`?
- Does the test verify behavior without duplicating the OS formatting rule?

## Verification Notes

Commands run during this ticket:

```sh
go test ./...
```

This passed for:

- `cmd/travis`;
- `internal/config`;
- `internal/core`;
- `internal/log`.

Cross-compile checks for config path build tags:

```sh
GOOS=linux go test -c ./internal/config -o /private/tmp/travis-config-linux.test
GOOS=windows go test -c ./internal/config -o /private/tmp/travis-config-windows.test
```

Both passed. These were compile checks, not full native OS test runs.

Repo-shape checks:

```sh
find . -path './.git' -prune -o -type d -name pkg -print
```

This produced no `pkg` directory.

```sh
rg -n "runtime\.GOOS|runtime\.GOARCH" --glob '*.go'
```

This showed platform access only in `internal/config/platform.go`.

```sh
rg -n "pkg/|pkg/core|pkg/config|pkg/log|<module>/pkg" docs AGENTS.md CLAUDE.md -g '!docs/code_design/archive/**'
```

After syncing docs and restoring the hard-rule block, the only intended active hit
is the negative rule in `AGENTS.md` that says not to create or use `pkg/`.

## Reviewer Checklist

- [x] Confirm this ticket does not create the future package tree.
- [x] Confirm every new package exists because this PR currently needs it.
- [x] Confirm `internal/core` contains shared DTOs/seams, not provider, tool,
  security, or terminal implementation logic.
- [x] Confirm `internal/config` owns the only OS/architecture formatting currently
  used by code.
- [x] Confirm config path behavior uses `filepath` and validates absolute roots.
- [x] Confirm `internal/log` is a facade, not a global logging policy.
- [x] Confirm tests cover behavior and do not test interface declarations for their
  own sake.
- [x] Confirm no CI/lint/Phase 0C work is included.

## Known Follow-Ups

- Implement the real event bus only when the terminal, session, eval, or agent loop
  needs subscription behavior.
- Add provider counting, budgeting, capability reporting, and embeddings only when
  concrete provider or agent-loop work needs them.
- Add the full tool execution interface through the registry and built-in tool
  tickets.
- Add policy, broker, credential, and capability matching through security/tool
  tickets rather than `core`.
- Implement full config loading, TOML precedence, profile overlays, secret refs, and
  validation in later config work.
- Leave CI, lint-deps, and lint-portability to Phase 0C ownership unless explicitly
  reassigned.
