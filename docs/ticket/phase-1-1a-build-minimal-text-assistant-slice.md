# Phase 1 / 1A: Build Minimal Text Assistant Slice

**What to build:** Create the smallest runnable Phase 1 text-assistant path. A user
should be able to start Travis, submit one prompt, and see one assistant response in
the terminal. This ticket is intentionally a vertical slice across terminal input,
agent-loop orchestration, `core.Provider`, one provider implementation, and
`cmd/travis` wiring.

**Blocked by:** Phase 0C CI/architecture-check branch landing.

**Status:** ready-after-0c

- [x] A user-facing command path exists for one prompt and one assistant response.
- [x] `internal/agent/loop` or the smallest equivalent package owns the prompt-to-response
      orchestration.
- [x] The agent loop depends on `core.Provider`, not on a concrete provider package.
- [x] One provider implementation exists only as far as needed to prove the slice.
- [x] The provider choice is documented in the PR: real OpenAI-compatible, local/static,
      Ollama, or another small provider path.
- [x] `cmd/travis` wires concrete terminal, runner, and provider pieces.
- [x] Terminal code collects user intent and renders assistant output without importing
      provider, tool, broker, policy, or MCP internals.
- [ ] Provider and stream failures are shown or returned cleanly enough for a developer
      to understand what failed.
- [ ] Agent-loop tests use fake providers and deterministic fake streams.
- [ ] `travis version` still works.
- [ ] `go test ./...` passes.
- [ ] No tools, cassette/replay, TOML loading, security policy, broker, MCP, voice,
      persistence, or broad terminal polish is included.

## Implementation Notes

This ticket should optimize for a working program, not perfect module depth. Keep the
first path deliberately narrow and document any temporary choices.

The likely package areas are:

```text
cmd/travis
  wires the concrete terminal, runner, and provider

internal/agent/loop
  builds a core.ProviderRequest, calls core.Provider, reads core.Stream chunks,
  and returns or publishes assistant text

internal/frontends/terminal
  collects one prompt, sends user intent inward, and renders assistant text/errors

internal/providers/<chosen>
  implements core.Provider only as far as needed for one response path
```

The first provider path is intentionally left to the implementer. Acceptable choices:

- a narrow real OpenAI-compatible adapter if credentials/config are straightforward;
- a local/static development provider if that is the fastest way to prove wiring;
- a minimal Ollama/local endpoint adapter if the team wants a no-cloud manual path.

Whatever choice is made, the agent loop must still depend on `core.Provider`, and the
composition root must own concrete wiring.

## Suggested Shape

A minimal runner can be as small as:

```text
Run(ctx, prompt)
  -> build []core.Message with one user text part
  -> call provider.Chat(ctx, request)
  -> read chunks until finish or EOF
  -> accumulate/render text
  -> return final text or error
```

The terminal path can be thin. It does not need final Bubble Tea layout, tool cards,
status bars, slash commands, or event replay. It only needs enough UI behavior for a
developer to submit one prompt and see the response.

## Review Questions

- Can a developer run the program and see one assistant response?
- Is the first slice vertical rather than a disconnected provider-only or UI-only
  branch?
- Does the agent loop depend on `core.Provider` instead of provider internals?
- Does `cmd/travis` own concrete wiring?
- Are temporary provider/config choices documented clearly?
- Are tests deterministic by default?
- Did the PR avoid pulling in tools, replay, security, MCP, voice, or persistence?

## Out Of Scope

- Built-in tools such as `fs.read`, `grep`, `fs.write`, `fs.edit`, `fetch`,
  `ask_user`, or `memory`.
- Tool-call parsing, repair, execution, or continuation.
- Cassette record/replay.
- TOML config loading, precedence, or hot reload.
- Security policy, approvals, broker process, sandbox, redaction, or undo.
- MCP, skills, subagents, or tool pruning.
- Voice input/output.
- Session persistence or journal storage.
- Full terminal polish, tool cards, trace view, settings screen, or slash palette.

## Handoff Note

This ticket is suitable for one owner or a small pair. Do not split it into separate
provider, terminal, and agent-loop branches before there is a working prompt-to-response
path. After this lands, follow-up Phase 1 work can split by provider hardening, tools,
terminal polish, and config loading.
