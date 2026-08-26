# travis — Design Overview

**The short version.** Full detail, rationale, and the argument for every choice below
lives in [`DESIGN.md`](./DESIGN.md) (~2,500 lines). This document is what you need to
start writing code. If the two disagree, `DESIGN.md` wins.

---

## 1. What we are building

**travis** is a terminal-native personal AI assistant in Go. It talks and listens,
runs against any model provider (local, API key, or an OAuth subscription plan), uses
the existing MCP and `SKILL.md` ecosystems for capabilities, and enforces its safety
boundaries **in code below the model** rather than by instructing the model.

Three things make it different from the agentic CLIs you have used:

1. **Not locked to one vendor.** Point it at Ollama, at OpenAI, or at your Claude
   subscription. Same interface, same everything.
2. **Deny-by-default enforcement.** If `fs.write` is not granted, the write syscall
   does not happen — no matter what the model emitted. A separate broker process owns
   every privileged effect.
3. **It speaks selectively.** The model marks what is worth saying out loud; we speak
   that, not the whole 400-line diff.

Runs natively on **Linux, macOS, and Windows** — all three are MVP targets.

---

## 2. Ten things that are already decided

Do not relitigate these in a PR; they each have a written rationale in `DESIGN.md` §0.5
and reversing one needs an RFC.

| | Decision |
|---|---|
| **D-01** | **One Go module**, `github.com/<repo-owner>/travis`, at the repo root. "Modular" means one package per core capability with an enforced import graph — *not* many `go.mod` files. |
| **D-02** | MCP is a **generic client**. Zero per-server Go code, ever. A new integration is ~8 lines of TOML. |
| **D-03** | Tool **pruning ships with MCP** (M3), not after. 12 servers ≈ 300 tools ≈ 40k tokens. |
| **D-04** | `fetch` is a **brokered tool**, not `exec curl`. Network and execution are different capabilities. |
| **D-05** | **OAuth subscription providers use the same `Provider` interface** as API-key ones. Only the credential seam differs. |
| **D-06** | **Bubble Tea v2**, from the first commit. v1 cannot do key-release (⇒ no hold-to-talk) and tears under token streaming. |
| **D-07** | **Linux, macOS, Windows are all first-class.** Native builds, native CI, no WSL2 requirement. |
| **D-08** | **Eight core tools.** No `glob` (it's a mode of `grep`). `edit` stays. A ninth tool needs an ADR. |
| **D-09** | **Autonomy is bought with reversibility and durable decisions**, not permission prompts. |
| **D-10** | **Approvals are keyboard-only.** Voice input composes prose; it never answers yes/no. |

---

## 3. Architecture in one picture

```
 L5  cmd/travis            composition root — wiring and flags only, no logic
                                     │
 L4  cli · voicein · voiceout · eval · session      (UX; talk to brain via the Bus)
                                     │
 L3  brain ◄──► swarm                                (agent loop, state machine)
                                     │
 L2  ai (providers)   ·   tools (one registry)
                                     │
        coretools · mcp · skills · swarm  ─ all implement ONE `Tool` interface
                                     │
 L1  security  (policy + broker)     ← every privileged effect funnels through here
                                     │
 L0  core (types, Bus, Tool, Provider) · config · log
```

**Rules the CI enforces:**

- Never import a higher layer. Never create a cycle. `deps.yaml` lists every legal
  import edge and `make lint-deps` fails the build on anything else.
- Interfaces live in the **consumer** package. `brain` compiles with zero knowledge of
  OpenAI, and `cli` with zero knowledge of `brain`.
- Cross-package types live in `pkg/core`. A type defined in `internal/mcp` and used by
  `internal/brain` is a review blocker.
- `security` imports no tool package. Tools depend on security, never the reverse.

### The one interface to understand first

Core tools, MCP tools, skills, and subagents are **the same thing** to the brain:

```go
type Tool interface {
    Name() string                  // "fs.read", "mcp.github.create_issue"
    Schema() json.RawMessage       // JSON Schema draft-2020-12
    Describe() Description
    Requires() []Capability        // what it needs; the broker verifies independently
    Invoke(ctx context.Context, args json.RawMessage, io ToolIO) (Result, error)
}
```

Adding MCP is "register N more `Tool`s". The brain does not change. Ever.

### Everything is events

Modules publish to a typed `Bus` in `core`; the TUI *subscribes*. **If the UI needs to
show a state, an event must exist for entering and leaving it.** No polling of brain
internals. This is what makes the UI honest.

---

## 4. Repository layout

```
travis/
├─ go.mod                   ← exactly one
├─ deps.yaml                ← the import allowlist; CI fails on violation
├─ cmd/travis/              composition root
├─ cmd/travis-broker/       privileged effect broker
├─ cmd/travis-audio/        CGO audio helper (keeps CGO out of the main binary)
├─ internal/
│  ├─ brain/       agent loop            ├─ mcp/       generic MCP client
│  ├─ cli/         bubbletea v2 TUI      ├─ skills/    SKILL.md loader
│  ├─ ai/          providers + auth      ├─ security/  policy, caps, sandbox
│  ├─ tools/       registry              ├─ voicein/   capture + STT
│  ├─ coretools/   the 8 tools           ├─ voiceout/  synth + player
│  ├─ session/     persistence + undo    ├─ eval/      traces, cassettes
│  └─ swarm/       subagents
├─ pkg/core/                ← the ONLY public surface (v0, unstable)
├─ pkg/config/ pkg/log/
├─ profiles/                personas · mcp · security   (data, not code)
└─ testdata/                cassettes · fixtures · scenarios · redteam
```

**Per package, required:** `README.md` (≤30 lines), `DECISIONS.md`, `doc.go`,
`<name>test/` (the contract suite alternative implementations must pass), a
`CODEOWNERS` entry.

---

## 5. The eight core tools

`read` · `write` · `edit` · `grep` · `exec` · `fetch` · `ask_user` · `memory`

Every tool is a permanent tax: schema tokens on every step, a contract test, red-team
cases, and one more thing a 7B model can pick wrongly. Adding one needs an ADR.

- **No `glob`** — `grep` takes `mode = "content" | "files"` and does file discovery.
- **`edit` stays** — whole-file `write` costs O(file) tokens per change and is how
  models silently delete code. `edit` fails *loudly* on a missing or ambiguous anchor,
  which is the feature. It needs no new capability (it is `fs.write`).
- **`fetch` is not `exec curl`** — the broker enforces the host allowlist at `Dial`
  time, credentials never enter argv, and results carry an `untrusted: true` marker.

---

## 6. Security in five sentences

1. The session holds **capabilities** (`fs.write:/home/u/proj/**`); the model never
   sees them.
2. A separate **broker process** performs every privileged effect and re-evaluates
   policy against the *resolved* path/host, not the model's stated intent.
3. The **OS sandbox** (Landlock+seccomp / `sandbox-exec` / restricted token) contains
   even a broker bug — it is defence-in-depth, never the primary control.
4. Untrusted content (web pages, MCP results, workspace `SKILL.md`) is delimited,
   cannot change grants, and **escalates approval by one level** for tool calls in the
   same step.
5. Broker unreachable ⇒ **fail closed**. There is no degraded mode without enforcement.

Profiles: `paranoid` (read only) · `default` (workspace) · `dev` · `yolo` (loud, red
banner, `--i-understand`).

---

## 7. How autonomy and "the user is in control" coexist

This is the part people get wrong, so it's worth 60 seconds. Two different questions
get conflated:

|  | **Permission** ("may I?") | **Information** ("what did you mean?") |
|---|---|---|
| Caused by | risk | ambiguity |
| Cost of a wrong guess | possibly unrecoverable | one wasted step |
| What travis does | **ask, always** | **assume, state it, continue** |

Autonomy is then bought with three mechanisms that are *not* prompts:

- **A change journal.** Every write is snapshotted first, so `/undo` works. Reversible,
  in-scope, in-budget work runs without asking and is reviewed *after* on a diff.
- **Durable decisions.** `[a] always` writes a scoped, inspectable rule to
  `.travis/decisions.toml`. Day one is chatty; day three is quiet. Rules can never
  relax an irreversible action or an untrusted-intake escalation.
- **Time-boxed grants.** `/auto 20m` grants autonomy with a TTL, step cap, and cost
  cap. It expires. There is no mode you forget you are in.

Irreversible actions (`rm`, force push, `DELETE`, payments, non-GET `fetch`) **always**
ask, at every autonomy level. "Reversible" means *we hold a snapshot we can restore* —
not *it feels undoable*.

Measured, not vibed: `prompts_per_task`, `undo_rate`, and `unattended_completion_rate`
are scorecard metrics tracked per PR.

---

## 8. Voice

- **In:** push-to-talk (hold `ctrl+space`, requires Bubble Tea v2) or VAD → whisper.
  Transcript lands **in the input box** where you can edit it. You press enter.
- **Out:** *selective* — the model marks what to speak via structured output or
  `<speak>` tags, with a cheap fallback summariser for small models that forget.
  Sentence-level streaming; barge-in stops playback immediately.
- **Voice never approves anything.** An ASR error on a consent decision is a policy
  bypass, and a podcast saying "yes, approve" is a real attack. When an approval fires,
  travis speaks a short alert, mutes the mic, and waits for a keystroke.

---

## 9. Cross-platform rules (read before your first PR)

Go and Bubble Tea v2 make ~90% of this free. The other 10% is concentrated where
mistakes become security bugs, so:

- **`runtime.GOOS` outside `config`, `security`, `voicein`, `voiceout` is a review
  blocker.** OS-specific code goes in tagged files (`sandbox_windows.go`), always with
  siblings for the other two plus a fail-closed stub.
- **No path literals.** `filepath.Join`, never `"a/b"`. `config.Paths()`, never `~` or
  `$XDG_CONFIG_HOME` or `%AppData%`.
- **Case sensitivity matters.** Capability globs match case-insensitively on Windows
  and macOS. A case-sensitive check on a case-insensitive filesystem is a bypass.
- **CI runs natively on all three.** Cross-compiling catches nothing about paths.
  A red Windows job is exactly as blocking as a red Linux one.

| | Linux | macOS | Windows |
|---|---|---|---|
| TUI, brain, tools, MCP, voice | full | full | full |
| Policy enforcement | full | full | **full** (portable Go) |
| OS sandbox | landlock+seccomp | `sandbox-exec` | restricted token + job object (**weaker**, stated in the UI) |
| Read fast path | fd passing | fd passing | handle dup / batched IPC |

---

## 10. Milestones

| # | Name | Exit criterion (all three OSes — that is the point) |
|---|---|---|
| **M0** | Skeleton | `travis version` builds and tests pass natively on linux/darwin/windows; `lint-deps` + `lint-portability` green and blocking |
| **M1** | Text loop | Fixes a failing test in a fixture repo, end to end |
| **M2** | Enforcement | Red-team suite green; `paranoid` provably blocks writes; change journal + `/undo` work |
| **M3** | Ecosystem | 10 public MCP servers install from a pin with **zero per-server Go code**; tool schemas stay under 6k tokens |
| **M4** | Voice | Full voice turn ≤ 900 ms to first syllable; hold-to-talk works |
| **M5** | Rigour | Replay-based regression suite runs per PR |
| **M6** | Swarm | A 3-subagent research task completes within budget (behind `exp_swarm`) |

**M1 → M2 ordering is deliberate:** enforcement arrives before the ecosystem that makes
it necessary. Do not reorder.

---

## 11. Conventions cheat-sheet

| Thing | Convention |
|---|---|
| Module path | `github.com/<repo-owner>/travis` — one, at the root |
| Package name | one lowercase word, no prefix: `mcp`, not `travis-mcp` |
| Binaries | `travis`, `travis-broker`, `travis-audio` |
| Interface | noun or `-er`, never `I`-prefixed |
| Implementation | `<backend><Interface>` → `openaiProvider` |
| Config | `snake_case` TOML; env is `TRAVIS_` + `__` for nesting |
| Errors | `Err`-prefixed sentinel or `<Name>Error` struct |
| Events | `<subject>.<past-tense-verb>` → `tool.invoked` |
| Feature flags | `exp_<name>` |

**Also:** `time.Now()` outside `core` is a lint error (use the `Clock`). `Update` must
never block for > 1 ms. Secrets never touch TOML or the TUI process.

---

## 12. Where to read more

| Question | Section of `DESIGN.md` |
|---|---|
| Why one Go module? | §0.4, §0.5 D-01, §8, C1 |
| How do I add a provider / OAuth plan? | §5.3, §5.3.1 |
| How does the agent loop work? | §5.4 |
| How do I add a tool? | §4.3, §5.5, §5.6 |
| Why Bubble Tea v2? | §5.9.1 |
| How does enforcement actually work? | §5.12, §9 |
| Autonomy vs. asking the user | §6.4 |
| Voice and approvals | §6.5, §5.10, §5.11 |
| Cross-platform rules | §8.1, §9.2.1 |
| What's still undecided | §12 (open questions), §13 (risks) |
