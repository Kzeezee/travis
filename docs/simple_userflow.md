```
                        ┌────────────────────────────────────────┐
 L5  Composition        │  cmd/travis  (wiring, DI, flags only)   │
                        └───────────────────┬────────────────────┘
                                            │
        ┌───────────────────┬───────────────┼───────────────┬──────────────────┐
        │                   │               │               │                  │
 L4  ┌──▼───────┐   ┌───────▼──────┐  ┌─────▼─────┐  ┌──────▼──────┐  ┌────────▼───────┐
 UX  │ terminal │   │  voicein     │  │ voiceout  │  │    eval     │  │    session     │
     │(tea v2)  │   │  (STT)       │  │  (TTS)    │  │             │  │  (persistence) │
     └──┬───────┘   └───────┬──────┘  └─────┬─────┘  └──────┬──────┘  └────────┬───────┘
        │                   │               │               │                  │
        └───────────────────┴───────────────┼───────────────┴──────────────────┘
                                            │  (all UX talks to agent loop via Bus)
 L3  Orchestration              ┌───────────▼───────────┐      ┌─────────────┐
                                │      agent/loop       │◄────►│  subagents  │
                                │  (turn loop, state)   │      │             │
                                └───┬───────────────┬───┘      └─────────────┘
                                    │               │
 L2  Capability            ┌────────▼─────┐   ┌─────▼──────┐
                           │  providers   │   │   tools    │  ← single registry
                           │              │   │ (Registry) │
                           └──────────────┘   └─────┬──────┘
                                                    │  providers of Tool:
                                     ┌──────────────┼──────────────┬──────────┐
                                ┌────▼─────┐  ┌─────▼────┐  ┌──────▼───┐  ┌───▼────┐
                                │ builtin  │  │   mcp    │  │  skills  │  │subagent│
                                └────┬─────┘  └─────┬────┘  └──────┬───┘  └───┬────┘
                                     │              │              │          │
 L1  Enforcement                ┌────▼──────────────▼──────────────▼──────────▼────┐
                                │            security (policy + broker)            │
                                │   ALL privileged effects funnel through here     │
                                └──────────────────────┬───────────────────────────┘
                                                       │
 L0  Foundation      ┌─────────────────────────────────▼─────────────────────────┐
                     │  core (types, ids, errors, Bus, clock)  ·  config  ·  log │
                     └───────────────────────────────────────────────────────────┘
```

## Stages

- User gives input through the CLI or voice.
- `agent/loop` owns the turn and decides what should happen next.
- `providers/*` sends the conversation to the selected model provider.
- The model returns either final text or tool calls.
- `tools` registry finds the requested tool.
- The tool prepares raw JSON into typed, meaningful arguments.
- `policy` decides whether the prepared action is allowed, denied, or needs approval.
- `broker` performs approved file, network, command, and secret operations.
- The result returns to `agent/loop`, which may continue the loop or finish the turn.
- UI, session, and voice observe events; they do not drive the security path.

## Story Version

**Stage 1: input.** You start by talking to travis through the terminal, or later
through voice. At this point travis is only collecting what you want: read a folder,
edit a file, remember a note, search something, or answer a question.

**Stage 2: agent loop.** The input goes to `agent/loop`, which manages the conversation. It
knows the current messages, budget, tools, task state, and stop conditions. It does
not directly read files, write files, run commands, or open network connections.

**Stage 3: provider.** The agent loop sends the conversation to `providers/*`, which talks to the
selected provider: OpenAI, Ollama, an Anthropic-compatible endpoint, or another
backend. The agent loop should not care which provider is active; it only cares about the
provider interface.

**Stage 4: model response.** The model can return normal text or ask to use a tool.
If it returns text, the agent loop streams it to the UI, records it in the session, may
send selected text to voice output, and ends the turn.

**Stage 5: tool lookup.** If the model asks for a tool, the agent loop asks the `tools`
registry whether that tool exists and what shape its arguments should have. This
keeps built-in tools, MCP tools, skills, and subagents behind one registry instead of
special-casing each source.

**Stage 6: preparation and validation.** The tool prepares the call before anything
runs. Raw model JSON like `{"path":"docs/DESIGN.md"}` becomes typed Go data plus a
resolved effect, such as "read this resolved file path." Malformed JSON, missing
fields, and schema mismatches fail here.

**Stage 7: policy.** The prepared call goes to `policy`. Policy receives facts: tool
name, resolved path or host, side-effect type, reversibility, untrusted-content state,
capabilities, deny rules, and budget. It returns exactly one answer: allow, ask, or
deny.

**Stage 8: approval or denial.** If policy denies the action, the tool never runs and
the agent loop feeds a tool error back to the model. If policy asks, the UI opens a
keyboard-only approval prompt. Voice can alert you that approval is needed, but it
cannot approve.

**Stage 9: brokered effect.** If the action is allowed or approved, it goes to the
`broker`. The broker is the separate process that actually touches the world. It
re-checks the resolved effect, then performs file, network, command, or secret work.
For writes, it snapshots the old file first so undo can work.

**Stage 10: result and loop.** The broker result returns through the tool to the
agent loop. The agent loop adds it to the conversation and may ask the model to continue. This
loop repeats until the model gives a final answer or travis hits a stop condition.

**Stage 11: events.** While all this happens, modules publish events such as
`turn.started`, `policy.decided`, `tool.completed`, and `turn.completed`. The UI uses
them to render state, `session` records them, and `voiceout` may speak selected
alerts. Events are observations, not the mechanism that performs privileged work.

## Where Validation Matters

Validation matters when data crosses from outside into travis:

- config files, env vars, and flags entering `config`;
- model tool-call JSON entering `tools`;
- MCP tool schemas entering `mcp` and `tools`;
- provider stream chunks entering `providers/*`;
- broker requests entering `security`.

Use JSON Schema validation for tool arguments and MCP schemas. Use explicit Go checks
for config, policy, broker enforcement, provider normalization, and session/journal
integrity.
