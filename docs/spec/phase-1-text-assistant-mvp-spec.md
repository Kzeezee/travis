# Phase 1 Text Assistant MVP Spec

## Problem Statement

Phase 0 gave Travis a small internal foundation: shared app DTOs in `internal/core`,
config-owned platform/path behavior, and a logging facade. The next risk is that
Phase 1 work gets split into provider, terminal, tools, and agent-loop branches
before anyone can run the assistant path end to end.

That horizontal split would make each branch look locally reasonable while delaying
the important feedback: can a user start Travis, submit a prompt, and see an
assistant response? Phase 1 should therefore start with one vertical text-assistant
slice. After that slice lands, deeper provider, tool, terminal, and config work can
split safely because everyone will be extending a working program.

## Solution

Build Phase 1 in two movements:

1. First land a minimal prompt-to-response slice through the terminal, agent loop,
   `core.Provider`, one provider implementation, and `cmd/travis` wiring.
2. Then split follow-up work into provider hardening, built-in tools, terminal
   interaction polish, config loading, and the first end-to-end tool task.

The first slice may choose the smallest provider path that proves the program works.
That can be a narrow real OpenAI-compatible adapter, a local/static provider, or
another documented development provider. The PR must keep the agent loop dependent
on `core.Provider`, with concrete provider wiring owned by `cmd/travis`.

Cassette/replay support is not part of the first slice. It remains Phase 1 work, but
belongs in provider hardening after the team has a running baseline and has made the
record/replay format and secret-handling decisions deliberately.

## User Stories

1. As a CLI user, I want to run Travis and submit one prompt, so that the program is
   visibly more than a version command.
2. As a CLI user, I want to see one assistant response in the terminal, so that the
   basic text-assistant path is understandable without reading logs.
3. As a maintainer, I want the first Phase 1 PR to cross the terminal, agent loop,
   provider seam, and composition root, so that integration assumptions are tested
   early.
4. As a provider implementer, I want the agent loop to depend only on `core.Provider`,
   so that provider implementations can change without leaking transport details
   inward.
5. As a terminal implementer, I want UI code to send user intent inward rather than
   importing provider internals, so that the UI stays an outer adapter.
6. As a test author, I want the agent loop tested with a fake provider, so that the
   first slice can run deterministically without live network calls.
7. As a reviewer, I want tools, config loading, replay cassettes, security, broker,
   MCP, voice, persistence, and terminal polish kept out of the first slice, so that
   the PR proves the main path without becoming the whole product.
8. As a future contributor, I want follow-up Phase 1 tickets to start from a working
   program, so that provider/tool/terminal changes can be reviewed against real app
   behavior instead of guessed interfaces.

## Implementation Decisions

- Phase 1 starts with `1A - Minimal text assistant slice`, not separate provider,
  tool, terminal, and agent-loop PRs.
- The first slice is allowed to be thin. It only has to prove one prompt and one
  assistant response.
- The agent loop depends on `core.Provider`; it must not import a concrete provider
  adapter.
- `cmd/travis` owns concrete wiring for the first slice.
- The terminal frontend is an outer adapter. It may collect prompt text and render
  response text, but it must not own provider calls, tool execution, policy, broker,
  or MCP behavior.
- The first provider path must be documented in the PR. The implementation can choose
  the smallest path that proves the slice, but should not pretend a temporary/static
  provider is a complete provider adapter.
- Unit tests should use fake providers and fake streams by default.
- Cassette/replay support is deferred to provider hardening. The replay format,
  request matching rules, recording mode, and secret handling need a focused design
  pass.
- Built-in tools are deferred until after the prompt-to-response path works.
- Config file loading is deferred. The first slice may use constructor values,
  flags, environment variables, or a small temporary wiring choice if documented.
- Security policy and brokered effects are deferred to Phase 2.

## Testing Decisions

- The first slice should run `go test ./...`.
- Agent-loop behavior should be testable with a fake `core.Provider`.
- Stream handling should be tested with deterministic fake chunks.
- Command-level tests should keep `travis version` working.
- If the first slice adds a `travis chat` or default assistant command, test the
  non-interactive parts without needing a live model.
- Live provider calls are optional for manual testing only and must not be required
  for the default test suite.

## Out Of Scope

- No built-in tools.
- No tool-call execution.
- No cassette/replay implementation.
- No TOML config loading or hot reload.
- No security policy, approvals, broker process, sandbox, or undo journal.
- No MCP or skills loader.
- No voice input or output.
- No session persistence.
- No full terminal polish.
- No broad package skeleton beyond the packages needed by the active ticket.

## Follow-Up Work

After the minimal slice lands, the team can split into at most three parallel Phase 1
follow-up PRs from the plan:

- provider adapter hardening and replay/cassette decision;
- built-in tool slice;
- terminal interaction polish;
- config file loading.

Those branches should merge back through the first end-to-end tool task.
