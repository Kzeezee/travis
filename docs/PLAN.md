# travis - Build Plan

**Status:** v0.3
**Last updated:** 2026-09-05

**What this document is.** The order we build things in, which PRs can run in
parallel, and what gets cut first when time is short.

**What this document is not.** It does not create new design decisions.
`docs/DESIGN.md` is still normative. The simplified code-design guides explain
how we want to apply those decisions in code:

- [code_design/codebase_tour.md](code_design/codebase_tour.md) - architecture map
- [code_design/design_rationales.md](code_design/design_rationales.md) - why the rules exist
- [code_design/presentation_layer.md](code_design/presentation_layer.md) - frontend shape
- [code_design/implementation_example/ports_and_adapters.md](code_design/implementation_example/ports_and_adapters.md) - ports/adapters example
- [code_design/implementation_example/error_handling.md](code_design/implementation_example/error_handling.md) - error handling example

Ignore everything under `docs/code_design/archive/`; those files are old source
material, not active guidance.

---

## 1. Where We Are

Already landed:

| Done             | Detail                                                                           |
| ---------------- | -------------------------------------------------------------------------------- |
| Module path      | `github.com/Kzeezee/travis`                                                      |
| Go toolchain     | `go 1.25.0` directive                                                            |
| Dependency pins  | Bubble Tea v2 dependencies are pinned through a temporary `tools` build-tag file |
| `travis version` | Command and test exist                                                           |
| Repo hygiene     | `.editorconfig`, `.gitattributes`, `.gitignore`, `AGENTS.md`, `CLAUDE.md`        |

Current Phase 0 work:

| PR                                  | Status                        | Notes                                                                                                        |
| ----------------------------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------ |
| **0A - Docs and plan alignment**    | Covered by this branch        | Simplifies the plan, aligns code-design docs, and records the smaller foundation strategy                    |
| **0B - Minimal core/config/log**    | Covered for Phase 0 scope     | Adds only the shared DTOs, path/platform seam, and logging facade needed before Phase 1                      |
| **0C - CI and architecture checks** | Landed                        | Native Linux/macOS/Windows CI, `Makefile`, pinned `golangci-lint`, `go vet` for all three `GOOS`, `go mod verify`/`tidy -diff`, `govulncheck`; see `docs/plans/ci-testing-pipeline.md` |
| **0D - Team rules and setup check** | Partly covered by this branch | Updates team rules; another dev with non mac-OS still needs to verify `docs/DEV-SETUP.md`                    |

Before feature work forks, finish the other-OS setup check from 0D. 0C has landed;
`make check` runs the full gate set locally.

Deferred `0B` work is assigned to the later PR that first needs it. Phase 1
starts with one vertical assistant slice before splitting provider, tool,
terminal, and config work into deeper follow-up PRs. Security policy stays in
2A, broker execution stays in 2B, and session/journal storage stays in 2C.

---

## 2. How To Slice PRs

Rules for PR planning:

```text
one PR should own one module or one workflow
parallel PRs should depend on merged interfaces, not on each other's branches
prefer 1-3 parallel PRs, not 5
do the first PR yourself so the team has a concrete example
```

Good PR shape:

```text
small interface
one real implementation or one fake implementation
focused tests
clear package ownership
```

Avoid this PR shape:

```text
new package tree for future work
many unrelated interfaces
docs, formatting, and feature code mixed together
multiple modules changing for one small behavior
```

---

## 3. Plan At A Glance

| Phase | Goal                                         | End state                                                                                |
| ----- | -------------------------------------------- | ---------------------------------------------------------------------------------------- |
| 0     | Align docs and build the smallest foundation | The team can work from the same package map and CI catches obvious architecture mistakes |
| 1     | Text assistant MVP                           | Travis can ask a model, run built-in tools, and show progress in the terminal            |
| 2     | Security enforcement                         | File/process/network effects go through policy and brokered execution                    |
| 3     | MCP and skills                               | Travis can load skills and talk to trusted/local MCP servers                             |
| 4     | Sandbox for third-party code                 | Public MCP servers can run under OS sandboxing                                           |
| 5     | Voice                                        | Travis can speak first; voice input comes after                                          |
| 6     | Evaluation                                   | Scenario suites and scorecards measure whether the assistant is useful                   |

---

## 4. Phases And PRs

### Phase 0 - Foundation

Goal: agree on the working structure and create only the interfaces needed for
the next phase. This is not a full skeleton phase.

| PR                                  | Owner    | Work                                                                                                | Depends on |
| ----------------------------------- | -------- | --------------------------------------------------------------------------------------------------- | ---------- |
| **0A - Docs and plan alignment**    | You      | Finish the simplified code-design docs, update this plan, and make the folder names consistent      | none       |
| **0B - Minimal core/config/log**    | 1 person | Add `internal/core`, `internal/config`, `internal/log`; include only types needed by Phase 1        | 0A         |
| **0C - CI and architecture checks** | Friend   | Add Linux/macOS/Windows CI, `go test`, `go vet`, `go mod verify`, import checks, portability checks | 0A         |
| **0D - Team rules and setup check** | 1 person | Update `AGENTS.md` with hard rules; verify `DEV-SETUP.md` on another OS                             | 0A         |

Phase 0 parallelism:

```text
Do 0A first.
After 0A merges, 0B, 0C, and 0D can run in parallel.
```

Phase 0 exit:

- `travis version` still builds and tests.
- CI runs on Linux, macOS, and Windows.
- `internal/core`, `internal/config`, and `internal/log` exist only as needed.
- External Go interfaces are out of scope for the current product.

### Phase 1 - Text Assistant MVP

Goal: ship the first real assistant loop without security enforcement. Effects
can still use simple local implementations, but the code must already call them
through ports.

Spec: [phase-1-text-assistant-mvp-spec.md](spec/phase-1-text-assistant-mvp-spec.md).
First ticket:
[phase-1-1a-build-minimal-text-assistant-slice.md](ticket/phase-1-1a-build-minimal-text-assistant-slice.md).

| PR                                      | Work                                                                                                                               | Depends on |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| **1A - Minimal text assistant slice**   | Smallest runnable prompt-to-response path through terminal, agent loop, a `core.Provider` implementation, and `cmd/travis` wiring  | 0B, 0C     |
| **1B - Provider adapter hardening**     | Deepen the provider path: OpenAI-compatible streaming, tool-call fragments, error translation, and replay/cassette decision        | 1A         |
| **1C - Built-in tool slice**            | `internal/tools/registry` and first built-in tools needed for a real file task, starting with the smallest useful read/search path | 1A         |
| **1D - Terminal interaction polish**    | Bubble Tea prompt, status, streaming display, tool-call rendering, event-driven rendering, and focused terminal tests              | 1A         |
| **1E - Config file loading**            | TOML config loading, precedence, validation, and any MVP hot-reload decision                                                       | 1A         |
| **1F - First end-to-end tool task**     | Fixture notes/files task using provider, tools, terminal path, config, and basic trace output                                      | 1B, 1C, 1D, 1E |

Phase 1 parallelism:

```text
Do 1A first as one vertical slice so the program works before layer-specific
branches diverge. The implementation may choose the smallest provider path that
proves the slice, but the PR must document that choice and keep the agent loop
depending on core.Provider.

After 1A merges, assign at most three parallel PRs from 1B, 1C, 1D, and 1E.
Then merge them through 1F.
```

Phase 1 exit:

- Travis completes a 3+ tool-call task over a fixture notes/files tree.
- Unit tests for the agent loop use fake providers and fake tools.
- Provider tests do not require a live model by default once the provider
  hardening/replay path exists.
- Terminal tests can run from synthetic events.

### Phase 2 - Security Enforcement

Goal: move required effects behind policy and brokered execution. The assistant
should not directly touch files, commands, or network effects when enforcement is
on.

| PR                                       | Work                                                                                        | Depends on |
| ---------------------------------------- | ------------------------------------------------------------------------------------------- | ---------- |
| **2A - Capabilities and policy**         | `internal/security/capabilities`, `internal/security/policy`; pure allow/ask/deny decisions | 1F         |
| **2A-seam - Broker port and wire**       | `core.Broker` port, `LocalBroker`, frozen msgpack wire protocol; tools stop doing IO (D-18, D-19) | 2A         |
| **2B - Broker process**                  | `cmd/travis-broker`, remote broker adapter speaking the 2A-seam wire, policy in the broker  | 2A-seam    |
| **2C - Journal and undo**                | `internal/state/session`, `internal/state/journal`, snapshot-before-write, `/undo`          | 2A-seam    |
| **2D - Approvals, deny-list, redaction** | Approval modal, durable approval rules, deny-list, secret/PII redaction                     | 2A         |
| **2E - Red-team gate**                   | Attack cases and assertions that policy denies the right thing for the right reason         | 2B, 2C, 2D |

Phase 2 parallelism:

```text
Do 2A first. After 2A merges, 2A-seam and 2D can run in parallel.
After 2A-seam merges, 2B and 2C can run in parallel.
Then 2E integrates them.
```

Phase 2 exit:

- Writes outside granted capability are denied.
- Irreversible actions ask for approval.
- Undo works for supported file writes/edits.
- Red-team cases run in CI.

### Phase 3 - MCP And Skills

Goal: make Travis useful outside local files while still treating third-party
code carefully. Public MCP should not be considered safe until Phase 4 sandboxing
exists.

| PR                                   | Work                                                                                                                | Depends on |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------- | ---------- |
| **3A - MCP client**                  | `internal/tools/mcp`; stdio client, tool adapter, supervision for trusted/local servers                             | 2A         |
| **3B - Skills loader**               | `internal/tools/skills`; load `SKILL.md`, select relevant skills, treat user/project instructions as untrusted text | 1F         |
| **3C - Tool pruning and inspectors** | Registry pruning, `/` palette, active tools/context inspector                                                       | 3A, 3B     |

Phase 3 exit:

- A trusted/local MCP server can expose tools through the normal tool registry.
- Skills can be loaded without sending every skill into every model request.
- Tool schemas stay within the target token budget.

### Phase 4 - Sandbox

Goal: make third-party MCP servers safe enough to run by default.

| PR                                   | Work                                                                         | Depends on |
| ------------------------------------ | ---------------------------------------------------------------------------- | ---------- |
| **4A - Sandbox interface and stubs** | Shared `Sandbox` interface plus Linux/macOS/Windows stubs                    | 3A         |
| **4B - Linux sandbox**               | Landlock/seccomp/user namespace implementation and tests                     | 4A         |
| **4C - macOS sandbox**               | macOS sandbox implementation and tests                                       | 4A         |
| **4D - Windows sandbox**             | Restricted token/job object/firewall approach and tests                      | 4A         |
| **4E - Public MCP enablement**       | Run public MCP servers through sandbox by default; report sandbox tier in UI | 4B, 4C, 4D |

Phase 4 parallelism:

```text
Do 4A first.
Then Linux, macOS, and Windows sandbox work can run in parallel.
```

Phase 4 exit:

- Public MCP servers run inside an OS sandbox where supported.
- Unsupported sandbox tiers fail closed or clearly report degraded behavior.
- Native CI runs same-shaped tests on all three OSes.

### Phase 5 - Voice

Goal: add voice without blocking the text assistant path.

| PR                          | Work                                                                                    | Depends on |
| --------------------------- | --------------------------------------------------------------------------------------- | ---------- |
| **5A - Voice output**       | `internal/frontends/voiceout`; speech selection and system TTS adapter                  | 1F         |
| **5B - Audio helper shape** | `cmd/travis-audio` plus voice input interfaces and OS stubs                             | 5A         |
| **5C - Voice input**        | `internal/frontends/voicein`; mic capture, VAD, local transcription, per-OS permissions | 5B         |

Phase 5 exit:

- Travis can speak short useful updates.
- Voice input is optional and degrades clearly when unsupported.

### Phase 6 - Evaluation

Goal: measure whether Travis is useful, not just whether it works.

| PR                              | Work                                                                        | Depends on |
| ------------------------------- | --------------------------------------------------------------------------- | ---------- |
| **6A - Scenario harness**       | Fixture workspaces, scripted tasks, pass/fail assertions                    | 1F         |
| **6B - Scorecard**              | prompts per task, undo rate, unattended completion rate, tool-schema tokens | 6A         |
| **6C - Trace redaction checks** | Ensure traces attached to failures do not leak secrets                      | 2D, 6A     |

Phase 6 exit:

- The project has a repeatable scorecard instead of relying on manual demos.

---

## 5. OS-Specific Work

Linux, macOS, and Windows are all MVP targets.

OS-specific code should live only in the module that owns the OS detail:

```text
internal/config                 paths and platform defaults
internal/security/broker         IPC transport and peer checks
internal/security/sandbox        OS sandbox implementations
internal/frontends/voicein       mic/device permissions
internal/frontends/voiceout      OS TTS adapters
```

Portable code should use shared interfaces, `filepath`, `config.BuildCurrentPathSet()`, and
typed capabilities. It should not branch on `runtime.GOOS` or hard-code `/tmp`,
`~`, `$HOME`, or `C:\`.

When OS-specific work is needed:

1. Merge the shared interface and all three OS stubs first.
2. Then let Linux, macOS, and Windows owners work in parallel.
3. Each OS file gets a same-shaped test that runs on that OS in CI.

Example shape:

```text
internal/security/sandbox/sandbox.go          shared interface
internal/security/sandbox/sandbox_linux.go    Linux implementation
internal/security/sandbox/sandbox_darwin.go   macOS implementation
internal/security/sandbox/sandbox_windows.go  Windows implementation
internal/security/sandbox/sandbox_stub.go     unknown OS, fails closed
```

---

## 6. Priority Stack

Must ship, in order:

1. Phase 0 - foundation and CI
2. Phase 1 - text assistant MVP
3. Phase 2 - security enforcement
4. Phase 3 - MCP and skills for trusted/local use
5. Phase 4 - sandbox for public MCP

Cut first if time is short:

1. Voice input
2. Advanced voice output backends
3. Full eval scorecard
4. Public MCP server enablement before sandbox
5. Any future package skeleton not needed by an active PR

Keep out of v1 unless explicitly re-planned:

- user-supplied hooks
- swarm/multi-agent orchestration beyond simple subagent-as-tool behavior
- tree-sitter/code-intelligence layer

---

## 7. Open Planning Items

Track these before starting the affected phase:

| Needed by | Question                                                                                   |
| --------- | ------------------------------------------------------------------------------------------ |
| Phase 1   | Decide the first minimal event sink: synchronous only, async queue, or both                |
| Phase 1   | Decide the first provider path: OpenAI-compatible remote, local/static, Ollama, or another |
| Phase 1   | Decide how much config hot-reload is needed for MVP                                        |
| Phase 2   | Define the first deny-list for Linux, macOS, and Windows                                   |
| Phase 2   | Confirm session storage format when implementing journal/session code                      |
| Phase 4   | Decide Windows sandbox approach before assigning implementation                            |
| Phase 5   | Confirm voice dependency licensing before adding audio/transcription dependencies          |
