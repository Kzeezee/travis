# Phase 3: tools, MCP and skills follow-up

[Back to overview](overview.md)

**Input state.** 3A merged: `internal/tools/mcp` starts trusted local servers from user
config, asks once at launch (D-27) and remembers "always" in `mcp-trust.toml`. 3B merged:
`internal/tools/skills` builds a skill index, adds `skill_open`, and loads `AGENTS.md`.
Both wrap untrusted text with `core.WrapUntrustedContent`. D-08's `exec` and `fetch` tools
do not exist.

## Wave 0 (do first)

### 3F Phase 3 hardening (new, priority high)

Goal: close the two security gaps 3A and 3B left.

| | |
|---|---|
| Changes | `internal/agent/loop/loop.go`: after any untrusted tool result (MCP or workspace `skill_open`), mark the next step with `core.MarkUntrustedStep`, so the broker's existing policy asks. `internal/tools/mcp/supervise.go`: `buildServerEnv` starts from `config.BuildEnvironmentWithoutSecrets()`. `internal/tools/skills`: workspace skills and workspace `AGENTS.md` load after one approval through the existing modal, keyed on a content hash; delete `skills.load_workspace`. `[security] exec_allow` (user layer only) feeds `capabilities.Roots.ExecAllow`, and the MCP policy in `cmd/travis/main.go` moves from the temporary `dev` profile (`exec: "*"`) to `default`. |
| Data | `core.ToolOutput` gains `IsUntrusted bool`; the loop reads it instead of knowing tool sources. AGENTS.md is in every prompt, so it is gated by approval, not by step marking. |
| Proof | Red-team cases: an injected MCP result requests a write and must reach the approver; the fake MCP server must not see a canary provider key; a workspace skill and `AGENTS.md` are absent until approved and again after their content changes; `main.go` builds no `dev` policy. Manual: fake MCP server in `bin/travis chat`, approval modal appears. |
| Rollback | Revert the PR. No data migration. |
| Depends on | Nothing |

3F implementation: workspace approval belongs to the skills catalog and uses the existing
keyboard approval modal through `core.Approver`, as MCP launch trust does. It approves
prompt intake rather than a filesystem grant; broker write approvals still use the
existing broker approval operation. Compared with background startup loading, preparing
instructions before each model request avoids a startup approval deadlock and detects
content changes. Approval covers the exact content snapshot for the current session;
denied content stays absent, and changed content asks again. Workspace instructions stay
delimited after approval. No durable workspace trust file is added.

Escalation covers the remaining tool calls in the current batch and the next model
step. A step without fresh untrusted output clears it, and a new user turn starts
without it. Denial stops the attempted effect; it does not create a lasting deny rule
for that path. This preserves D-26's step-scoped design rather than propagating trust
marks through the whole conversation.

### TRA-39 Gemini tool calls (existing)

Goal: tool calls work through `openaicompat` against Gemini, a widely used model.

| | |
|---|---|
| Changes | `internal/providers/openaicompat`. Root cause first: record a failing cassette, then fix. |
| Proof | Cassette replay of a Gemini tool-call turn passes; one live run in `bin/travis chat`. |
| Depends on | Nothing. Does not block Phase 6, which evaluates DeepSeek only. |

### TRA-37 `make chat` / `make install` (existing)

As written in the ticket. Every later ticket's runtime check uses `make chat`.

### D-0 docs and lint drift (new)

| | |
|---|---|
| Changes | `.golangci.yml`: voice exemption path to `internal/frontends/(voicein\|voiceout)`. `docs/PLAN.md` §1 and the Phase 3–6 rows link here. `docs/DESIGN.md`: §5.5 MCP names to `mcp__<server>__<tool>`; §5.7 launch-time trust and `mcp-trust.toml`; §5.8 `skill_open`; stale §5.12 `Broker` and §5.15 `Store`/`Journal` interfaces; milestone numbering. |
| Proof | `make check`; a test file under `internal/frontends/voicein` that uses `runtime.GOOS` passes lint. |
| Depends on | Nothing |

## Wave 1

### 3C Pruning and inspector (new)

| | |
|---|---|
| Changes | `internal/tools/registry`: built-ins always exposed, MCP tools ranked by keyword overlap with the prompt, capped at `max_exposed`; add `Unregister`, called when a server exhausts restarts. Terminal: `/tools` queries the registry live (delete the startup `ToolNames` field), add `/skills` and `/memory`. |
| Data | `Prune(ctx, budget, hint) []core.Tool` keeps its signature; ranking is a pure function. |
| Proof | Test: tool-schema tokens stay under budget with three fake servers of 20 tools each. Golden frame for `/tools`. Manual: a server that connects after startup appears in `/tools`. |
| Depends on | 3F. TRA-38 (model suggestions) links here as a sibling palette ticket. |

### 3D `exec` tool (new)

| | |
|---|---|
| Changes | Broker `exec` op (protocol version bump): argv only, no shell, workspace cwd, timeout, output cap, secret-free environment. Always asks (D-27). Grants come from 3F's `exec_allow`. Tool in `internal/tools/builtin`. |
| Proof | `tooltest.Run`; red-team case: an `always` rule cannot be granted for `exec`; manual approval round-trip. |
| Depends on | 3F |

### 3E `fetch` tool (new)

| | |
|---|---|
| Changes | Broker `fetch` op with §9.3 hardening: HTTPS only, private and link-local IPs blocked after DNS resolution, size and redirect caps. `HostAllow` from user config. Output untrusted. |
| Proof | `tooltest.Run` against an `httptest` server; red-team cases: redirect to `127.0.0.1` denied, injected page content forces an ask on the next write. |
| Depends on | 3F |

## Phase 3 exit check

Scenario suite and red-team suite green. Manual demo: one MCP server, one skill, `exec`
and `fetch` in one `make chat` session.
