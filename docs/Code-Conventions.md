# Code Conventions

Status: dense checklist of patterns to follow. `docs/DESIGN.md` stays normative
for architecture decisions (`D-nn`).

## Naming caveat
Some older material uses different names (`brain`, `coretools`, `ai`, `internal/cli`)
than the current package layout. Use `agent/loop`, `tools/builtin`, `providers/*`,
and `frontends/terminal` as the actual package names.

## Structure
- No `pkg/` — nothing outside the module imports Travis Go types; MCP servers talk
  over stdio/HTTP.
- Broker (`cmd/travis-broker`) and voice (`cmd/travis-audio`) are separate OS
  processes, not packages. Broker re-validates every effect itself; never trusts the
  caller.
- Ownership rule for shared code: keep it with the single owner; only promote to
  `internal/core` if genuinely cross-module with no owner. Don't dedupe small overlaps
  preemptively.
- Three-OS rule: no `runtime.GOOS`, no hardcoded `/tmp`/`~`/`$HOME`/`C:\` in business
  logic — OS specifics live only in the owning adapter (config, sandbox, broker,
  voice). Build-tagged files: add the interface + all OS variants/stubs together, not
  incrementally.
- `deps.yaml` mechanically enforces import direction — folder names alone don't.

## Core patterns (must follow)
- **Runtime validation**: raw external data (CLI/env/TOML, model JSON, provider
  chunks, MCP payloads, files) becomes typed data exactly at the boundary that
  receives it. Never pass `json.RawMessage` deeper into the system.
- **Ports and adapters**: business logic depends only on a small interface (port) it
  defines; adapters (broker, provider HTTP, fakes) implement it. Inner code never
  imports concrete adapters (no `os.WriteFile` in business logic, no OpenAI client in
  agent loop). Dependency-inject ports and runtime choices, not implementation
  mechanics: inject dependencies when they vary by runtime environment, user/config
  choice, or test scenario, especially external effects and nondeterministic behavior
  that need fakes in tests. Pass `core.Provider`, `core.Clock`, `log.Logger`, output
  writers, levels, or event sinks where they vary, but keep stable library setup,
  pure helpers, and deterministic details such as `slog`, provider HTTP clients, OS
  path logic, and sandbox calls inside their owning modules.
- **Dependency direction**: outer/detail code may import inward; inward
  decision/policy code must never import outer detail (providers, UI, broker, MCP).
  `cmd/travis` is the only place allowed to wire concrete adapters.
- **Events vs operations**: event = fact already happened, observer-only, must never
  trigger privileged effects or be required for correctness. Operation = required
  work; use direct sync/async call if caller needs ordering, approval, result, or
  error. Test: "does failure change whether the user's action succeeded?" — yes ⇒
  operation, no ⇒ event.
- **Policy** returns exactly one of `allow / ask / deny`, from prepared typed facts
  only. Policy package must never import broker, filesystem, provider, UI, or MCP.
- **Approval is keyboard-only.** Voice may alert that approval is needed but cannot
  approve.
- **Async event delivery** drops events under backpressure (non-blocking
  send/default) and only logs the drop — no implicit retry/queueing guarantee.

## Error handling — classify by scenario, not by "is this an error"
| Scenario | Treatment |
|---|---|
| Normal business result (allow/deny/rejected/exhausted) | typed value, **not** a Go `error` |
| Sync required work failed, caller waiting | return Go `error` now |
| Async required work: job failed to start | return `error` now |
| Async required work: accepted job fails later | record in job/session/trace state (no caller left to receive it) |
| External dependency failed (SDK/provider/MCP/broker) | adapter translates to Travis generic kind (`ErrInvalid`/`ErrTransient`/`ErrExhausted`) + business error wrapper (e.g. `ProviderError{Kind, Cause}`); inner code never sees raw SDK error types |
| Event observer failed | log/record/drop/dead-letter only; must never fail or rewrite the already-succeeded original operation |
| Unexpected programming bug (impossible state) | loud failure in dev/test; log+alert and fail safe in prod |

- Don't put workflow policy (e.g. `ShouldRetry()`) inside error types — caller
  decides retry/stop/ask, not the error.
- Error translation happens only in adapters (`providers/*`, `tools/mcp`,
  `security/broker`); keep the original error as `Cause` for debugging.

## Testing target
- Business logic (agent loop, tool prep, policy, provider request/response
  conversion) must be unit-testable with fakes only — no real providers, MCP
  servers, broker process, terminal, audio device, or filesystem.
- Adapters (broker, provider HTTP, MCP client) still need real integration tests;
  the exemption above applies only to decision/business code.

## Frontend (`internal/frontends/terminal`, Bubble Tea)
- Layers: screen/app orchestration → components (render + local UI state only) →
  presentation mappers (event/domain → view model) → command sink (intent inward) /
  event subscription (facts outward).
- Hard rule: brain/provider/tool/broker/MCP packages must never import the terminal
  UI package.
- Don't pre-create `screens/`, `components/`, `presentation/` folders — start flat,
  split only when a single file/folder is demonstrably noisy or shared by multiple
  tests.
