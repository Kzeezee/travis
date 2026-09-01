# travis — Build Plan

**Status:** v0.2 · **Last updated:** 2026-09-01

**What this document is.** The order we build things in, what can run in parallel at each
stage, and what gets cut first when time runs short.

**What it is not.** It holds no design decisions. Those live in
[`DESIGN.md`](./DESIGN.md) — §0.5 for the decision register (D-01…D-34), §0.6 for
D-01…D-10 explained in plain words, §12 for open questions, §13 for risks. Concepts that
are easy to get wrong on first read are in [`DESIGN-overview.md`](./DESIGN-overview.md)
§13. Getting a working environment is [`DEV-SETUP.md`](./DEV-SETUP.md). If this document
and `DESIGN.md` disagree about *what* the system is, `DESIGN.md` wins; if they disagree
about *sequencing*, this one does.

| Section | Read it when |
| --- | --- |
| §1 Where we are | Starting a session, or picking up work |
| §2 The plan at a glance | Any time you need the shape of the whole thing |
| §3 Phases | At every phase boundary |
| §4 Priority stack | When time runs short and something has to go |
| §5 OS-specific work | Before writing any build-tagged file |
| §6 Work tracking | Weekly — what is agreed but not yet built |

---

## 1. Where we are

**Phase 0 is underway.** Already landed:

| Done | Detail |
| --- | --- |
| Module path | `github.com/Kzeezee/travis` — closes `OQ-25` |
| Go toolchain | `go 1.25.0` directive (Bubble Tea v2 requires ≥ 1.25) |
| Dependency pins | `charm.land/bubbletea/v2 v2.0.9`, `lipgloss/v2 v2.0.6`, `bubbles/v2 v2.2.1`, held by `internal/cli/deps.go` under a `tools` build tag |
| `travis version` | plus a test; `go build`, `go test`, `go vet`, `go mod verify` all pass |
| Repo hygiene | `.editorconfig`, `.gitattributes`, `.gitignore` |
| `AGENTS.md`, `CLAUDE.md` | exist, but minimal — the hard rules from §3 Phase 0 F are not in them yet |

**Not yet done, and blocking the fork into parallel work:**

- `pkg/core` — types, interfaces, `Bus` + event list, `Clock`
- The broker wire protocol (D-19)
- `deps.yaml` + `make lint-deps` · `make lint-portability`
- CI: **native** matrix on linux/macOS/windows, with `-race`
- The full package skeleton (all 13 `internal/` packages, `profiles/`, `testdata/`)
- The test strategy: contract suites, the fake set, the cassette seam

⚠️ **Note for whoever writes CI:** `internal/cli/deps.go` is `//go:build tools`-tagged, so
`go build ./...` **skips it** and never compiles the Charm dependencies. CI needs
`go build -tags tools ./...` (or `go mod download all`) or a broken pin will not be caught.

**Also changed:** `CODEOWNERS` is no longer a per-package required file. Ownership is by
convention.

---

## 2. The plan at a glance

| Phase | The goal | What actually gets built | Demo at the end |
| --- | --- | --- | --- |
| **0** | Lay out the **entire** architecture — every folder, every package, including ones nobody implements for months — decide how the code gets tested, and prove the build works on all three OSes. **You write code, but no *features*** | • **`pkg/core`** — the shared vocabulary: the types, interfaces and events every other package uses. Everything imports it; it imports nothing<br>• **The broker message format** — how travis and the broker will talk. Frozen now because two teams build against it<br>• **The full folder skeleton** — all 13 packages, even unwritten ones, so the import rules bite from the first commit instead of being negotiated later<br>• **Two lints + CI on 3 OSes** — machines enforce the architecture, not reviewers<br>• **Test strategy** — one agreed testing style, the fake objects, the contract suites<br>• **`AGENTS.md`** — the project's hard rules, so everyone's AI assistant follows them | `travis version`. Nothing a user would notice — that's the point |
| **1** | Ship a working assistant end to end: it talks to a model, reads your files, edits them, remembers things. **No security yet** | • **Provider transport** — the code that sends your conversation to a model and streams the reply back word by word. One implementation covers Ollama, OpenAI, Anthropic, anything speaking the OpenAI format<br>• **The agent loop** — build the prompt → ask the model → run whatever tools it asked for → feed the results back → repeat until done or out of budget<br>• **The 8 tools + registry** — what the model can actually *do*: read a file, write one, edit part of one, search, run a command, fetch a URL, ask you a question, save a note. The registry is the menu the model sees, plus validating its arguments<br>• **The terminal UI** — text streaming in, cards showing which tool ran and what it returned, a status bar with context usage and cost<br>• **Config + per-OS paths** — settings from TOML, and where files live on each OS (`~/.config` vs `%AppData%`)<br>• **Cassettes + traces** — recorded model replies so tests run in seconds and give the same answer every time, plus a log of every event so you can see what the agent did | "Read my notes folder and tell me what I decided about X" — a real 3-tool-call task |
| **2** | Ship the **enforcement**: the AI physically cannot act outside what you granted. And add undo, so it can act *without stopping to ask* | • **Capabilities + the policy engine** — permission slips as strings (`fs.write:/proj/**`), and the code that answers allow / ask / deny<br>• **The broker process** — a second program that performs every file, network and command operation. The main program *loses* that ability entirely, so even a total compromise of it can't touch your disk<br>• **The undo journal** — save a copy of every file before changing it, so `/undo` puts it back. This is what lets the assistant act without asking<br>• **Approval UX** — the prompt when something *can't* be undone, plus "always allow" rules and `/auto 20m`<br>• **The deny-list** — paths it may never read (`~/.ssh`, `~/.aws`) that override every other permission<br>• **The red-team suite** — 200 attacks that must all fail. This is the gate: one success blocks the phase | Tell it to write to `/etc/hosts`. Watch it get refused. Then `/undo` a real edit |
| **3** | Plug in the outside world — MCP servers and skills. **This is the phase that makes it an assistant** rather than a file editor | • **The MCP client** — one piece of code that talks to *any* MCP server. Adding GitHub or Gmail becomes ~8 lines of config, never a new Go package<br>• **Server supervision** — starting servers on demand, restarting crashed ones, removing their tools from the menu when unhealthy<br>• **Skills loader** — reads `SKILL.md` folders and gives the model instructions *on demand*, so 60 skills cost ~2k tokens instead of 200k<br>• **Tool pruning** — 12 MCP servers is ~300 tools and 40k tokens of descriptions sent on *every* request. This picks the relevant handful. Without it the app is unusable on a small local model<br>• **Reading the user's `AGENTS.md`** — project instructions, treated as untrusted text<br>• **Palette + inspectors** — `/` commands, and screens showing which tools and what context are active | Connect a real MCP server and have it fetch something from a live service |
| **4** | Cage the third-party code, then make travis **talk and listen** | • **The OS sandbox, three implementations** — asking the kernel to fence in MCP servers, so a compromised one can't read your SSH keys. Landlock + seccomp (Linux), `sandbox-exec` (macOS), restricted token + job object (Windows)<br>• **Speech selection** — picking the one sentence worth hearing out of a long answer. The actual innovation, and plain portable Go<br>• **TTS playback** — handing that sentence to the OS's own speak command (`say` / `espeak` / SAPI)<br>• **`cmd/travis-audio`** — a third binary owning microphone access via `malgo`, keeping CGO out of the main program<br>• **Capture + VAD + local Whisper** — record, detect when you stopped talking, transcribe locally, drop the text in the input box<br>• **Per-OS mic permissions** — macOS entitlement + dialog, Windows privacy setting + device changes, Linux no-device degradation | A compromised server can't read `~/.ssh`; and you hold a key, talk, and it answers aloud |
| **5** | Find out whether it's any good — with numbers instead of opinions | • **Scenario suites** — realistic assistant tasks with a fixed starting workspace and pass/fail assertions<br>• **Rubric grading** — a model judging the fuzzy qualities, nightly rather than per-PR because it's slow and noisy<br>• **The scorecard** — the three numbers that decide whether the autonomy bet paid off: how often it interrupts you, how often you undo it, how many tasks finish untouched<br>• **Trace redaction** — scrubbing secrets so a trace can be attached to a bug report | A scorecard: prompts per task, undo rate, tasks completed unattended |

---

## 3. Phases

Ordered by **dependency**, not by skill. Within a phase, `‖` marks parallel work.

### Phase 0 — Frozen contracts · **fully serial, nobody forks**

> **In plain words:** three things. **(1)** Decide the shared vocabulary — the types,
> interfaces and events every other package will use. **(2)** Lay out the *whole* folder
> structure, every package included, even ones you won't touch for months, so the
> architecture is enforced from the first commit rather than negotiated later by whoever
> happens to start that package. **(3)** Decide how the code gets tested, once, before five
> people invent five different styles.
>
> **"No features" does not mean "no code."** You write shared plumbing: types, interfaces,
> the event bus, the clock, ID generation. A *feature* is something a user can do.
>
> **So what do the tests test?** The pieces that have behaviour — the `Bus` (does a
> subscriber receive events? does a slow one drop the oldest and emit `EventsDropped`
> rather than stalling the publisher?), the `Clock` fake, ULID ordering — plus the lints
> themselves: **plant an illegal import and confirm CI fails.** Interfaces have no
> behaviour to test; they are agreements, not running code.
>
> **The real deliverable is that the machinery works**, not coverage. Broken now is a day's
> fix; broken in Phase 3 blocks five people.

**A · Decisions that block everything else**

1. ~~Module path~~ — ✅ `github.com/Kzeezee/travis`
2. `OQ-02` — bus ordering guarantees (changes the `Bus` contract, so it cannot come later)

**B · The shared code (`pkg/core`)**

3. Types: `ID` (ULID), `Capability`, `Message`/`Part`, `Cost`, `StopReason`, the error taxonomy (§6.2)
4. Interfaces: `Tool`, `ToolIO`, `Result`, `Provider`, `Stream`, `Chunk`, `Policy`, `Broker`, `Credential`
5. `Bus` + **the full event list** (§4.5) — the TUI cannot be built without it
6. `Clock`
7. **The broker wire protocol** (D-19)

**C · The complete skeleton — every package, including ones nobody implements for months**

8. **All 13 `internal/` packages** from §8's layout: `brain`, `cli` (with `components/`,
   `screens/`, `theme/`), `ai`, `tools`, `coretools`, `mcp`, `skills`, `security`,
   `voicein`, `voiceout`, `session`, `eval`, `swarm`. Each gets `doc.go` with the package
   clause and a one-paragraph purpose — that alone compiles, so CI stays green
9. **All three `cmd/` directories**: `travis` ✅, `travis-broker`, `travis-audio`
10. `pkg/core`, `pkg/config`, `pkg/log` · `profiles/` (`personas/`, `mcp/`, `security/`) ·
    `testdata/` (`cassettes/`, `fixtures/`, `scenarios/`, `redteam/`)
11. **Per-package required files** (§8): `README.md` (≤30 lines), `DECISIONS.md`,
    `doc.go`, `<name>test/`

**D · Enforcing the architecture mechanically**

12. **`deps.yaml` declaring legal import edges for *all* packages** — including
    unimplemented ones. See the note below
13. `make lint-deps`, blocking
14. `make lint-portability`, blocking (`runtime.GOOS`, path separators, `os.Getenv("HOME")`)
15. **Native CI on all three OSes**, blocking, with `-race` — and `-tags tools` so the
    dependency pins are compiled

**E · How the code gets tested** — decided once, before five people invent five styles

16. **The contract-suite pattern.** Every package ships `<name>test/` exporting
    `Run(t, impl)`. Create the skeletons now: `tooltest`, `providertest`, `brokertest`,
    `policytest`, `storetest`
17. **The fake set:** `FakeProvider` (script-driven), `FakeBroker`, `fakeTool`, the `Clock`
    fake, a null TTS player that records timings
18. **The cassette interface** (D-16) — Phase 0 defines the `http.RoundTripper` seam,
    Phase 1 implements record/replay
19. **Testing rules, into CI and `AGENTS.md`:** table-driven tests as the default ·
    **zero network in any `brain` or `ai` unit test** · golden files normalise line endings
    before comparing (or they fail only on Windows, only for one person) · `teatest` golden
    frames at 60/80/120/200 · `time.Now()` outside `core` is a lint error
20. **Adopt the red-team assertion convention now (D-29)** — `assert_event = "PolicyDenied"`
    is mandatory. It must exist *before* cases are authored; retrofitting after 200 exist
    means rewriting all 200

**F · Documentation and conventions**

21. **Fill in `AGENTS.md` with the hard rules** — it exists but is minimal. The rules an
    assistant will otherwise violate by default: exactly one `go.mod` · no `runtime.GOOS`
    outside `config`/`security`/`voicein`/`voiceout` · no path literals, always
    `filepath.Join` and `config.Paths()` · no `time.Now()` outside `core`, use the `Clock` ·
    interfaces live in the *consumer* package · cross-package types live in `pkg/core` ·
    `security` imports no tool package · `Update` must never block > 1 ms · **tool names are
    namespaced** (`fs.read`, not `read`)
22. **Rename tool references to the namespaced form (D-31)** — find-and-replace while the
    repo is empty; a migration once config files and red-team cases exist

> **Why `deps.yaml` must cover unimplemented packages.** The lint walks real imports against
> an allowlist, so declaring edges for an empty package is a harmless forward declaration —
> and it is the difference between an architecture that is enforced and one that is
> aspirational. If `swarm`'s legal edges are only added the day someone starts `swarm`, then
> that person decides the architecture six months after the reasoning was written. C1 is
> blunt about the stakes: *"if that lint is ever made non-blocking, C1 is back"* — and a
> lint with gaps is non-blocking in exactly those gaps.
>
> **Why empty packages, not just directories.** A directory with no `.go` files is ignored
> by `go build ./...`, so it enforces nothing. A `doc.go` with `package swarm` and a
> paragraph of purpose compiles, appears in the import graph, is covered by both lints, and
> captures *why the package exists* while `DESIGN.md` is fresh.
>
> **Technique for freezing interfaces you don't yet understand: write the fakes first.**
> Don't design `Provider` then implement it — write `FakeProvider` and a test that uses it.
> If the fake compiles and the test reads naturally, the interface is probably right.
>
> **Parallel-safe ramp tasks** (no risk to the design): `pkg/log`, the `Clock` fake, ULID
> generation, one CI job each, and **authoring red-team cases** — TOML *data*, no Go needed,
> and it teaches the threat model. Best onboarding task in the project.

**Exit (M0):**

1. `travis version` builds and tests pass **natively** on linux/amd64, darwin/arm64,
   windows/amd64 — with `-race`
2. **Every package in §8's layout exists and compiles**, with `doc.go`, `README.md`,
   `DECISIONS.md`, `<name>test/` — including ones with no implementation yet
3. **`deps.yaml` covers every package**, and `make lint-deps` **provably fails** on a
   deliberately illegal import (test the test)
4. `make lint-portability` provably fails on a planted `runtime.GOOS` and a planted path literal
5. The fake set compiles and at least one contract suite runs against it
6. `AGENTS.md` lists the hard rules

⚠️ **This is a bigger Phase 0 than `DESIGN.md`'s original M0**, which asked only for
`travis version` plus green lints. The skeleton, the complete `deps.yaml`, and the test
strategy are additions — re-estimate accordingly. The argument for paying it here is that
all three are *cheap now and expensive-to-impossible to retrofit*.

> ⚠️ **Phase 0 is being built by one person, which moves two risks.**
>
> **The other four arrive cold.** They will write code against interfaces they did not help
> shape, in a language none of them knows. Budget **an hour walking them through `pkg/core`,
> the `Bus`, and the `Broker` interface** before this phase forks — otherwise the five tracks
> start with four people guessing.
>
> **There is no code review on the foundation**, since nobody else reads Go yet. The
> substitute is **writing the fakes first**: if `FakeProvider` and a test using it read
> naturally, the interface is probably right; if the fake is awkward to write, the interface
> is wrong. That is the only signal available, so do not skip it.
>
> **And the whole timeline is one person's availability.** If Phase 0 stalls, all five stall.
> Worth saying out loud to the team rather than discovering it.
>
> **Meanwhile, work for the other four that touches none of Phase 0's files:** authoring
> red-team cases (TOML data, no Go — and the best way to learn the threat model) · reading
> `DESIGN.md` and raising gaps · **verifying `DEV-SETUP.md` on Linux and Windows**, which is
> unconfirmed · researching the per-OS deny-list paths (`OQ-26`) · learning Go, specifically
> `go test` and table-driven tests.

### Phase 1 — First fork · **5 tracks**

> **In plain words:** ship a working assistant end to end — your message goes to a model,
> the model asks for tools, the tools read and edit real files, the result renders in the
> terminal. **Security is deliberately absent**: tools touch files directly for now. Also
> set up cassettes, so every test afterwards runs in seconds and gives the same answer.
>
> **Why five tracks run at once:** each depends only on Phase 0's shared code. The trick is
> `LocalBroker` (D-18) — tools call a `Broker` *interface*, and this phase supplies a dumb
> version that just does the file operation. Phase 2 swaps in the real one without touching
> the tools.

| Track | Work | Depends on |
| --- | --- | --- |
| **A · `ai`** | OpenAI-compatible chat, SSE streaming, tool-call fragment assembly across chunks, error taxonomy, `Count()`/`Budget()`, `ToolCallRepair` (R-05), **cassette record/replay** (D-16), **raw trace recorder** (a `Bus` subscriber writing JSONL — redaction comes in Phase 2) | core only |
| **B · `tools` + `coretools`** | Registry (namespacing, schema validation); `fs.read`, `grep`, `fs.write`, `fs.edit` (+ anchor fuzz test), `ask_user`, `memory` (**with the size cap**, D-28); the `tooltest` contract suite | core + frozen protocol, against `LocalBroker` |
| **C · `brain`** | Step loop (assemble → call → dispatch → absorb → decide), context assembly, **compaction at 60% with `context_length` from config** (D-33), stop conditions, loop detection, `FakeProvider` tests | core + interfaces. **Zero network** |
| **D · `cli`** | Bubble Tea v2 root model, explicit focus state, viewport/textarea, **30 fps token coalescer** (R-07), status bar, tool cards, `teatest` goldens at 60/80/120/200. **Delete `internal/cli/deps.go`** once the real TUI imports Charm — it exists only to pin the dependencies until then, and CI drops the `-tags tools` flag with it | **core `Bus` + events only.** `cli` never imports `brain` (§4.2), so it is fully parallel, driven by a synthetic event stream |
| **E · `config`** | Layered TOML precedence, **`Paths()` per-OS**, secret refs (`env://`, `cmd://`, `keyring://`), `Validate()` with the security-keys-are-hard-errors asymmetry | core |

**Exit (M1):** completes a **3+ tool-call assistant task over a fixture notes/files tree**,
on all three OSes. Retain a failing-Go-test fixture as an *internal* loop test — cheap and
objectively verifiable — but the demo is assistant-shaped from M1 onward. Credential-free:
`fs.read` + `grep` + `memory` + `fetch` over a folder of markdown notes is already an
assistant.

**`Paths()` is the rehearsal for the OS-sharding pattern** (§5). Smallest possible instance;
getting the rhythm wrong costs a day instead of a week.

### Phase 2 — Enforcement

> **In plain words:** the phase the project exists for. Move every file, network and process
> operation out of the main program into a **separate broker process** that checks
> permissions first — so if a capability wasn't granted, the operation *physically does not
> happen*. Then add the **undo journal**, which is what lets the assistant act without
> asking: a change it can take back doesn't need permission, it needs an undo.
>
> **Order matters.** The permission checker (2a) must exist before anything uses it, so that
> part is serial. Then four tracks run in parallel. The phase ends with the red-team suite:
> 200 attack cases, zero successes, on all three OSes.

**2a · serial** — everything in 2b depends on these:

1. `Capability` + **the glob matcher with a per-OS case flag** (R-22)
2. `Policy.Evaluate` + `Explain`, **including deny-beats-allow ordering** (D-22) — pure
   functions, highly testable, good beginner work

**2b · 4 parallel tracks:**

| Track | Work |
| --- | --- |
| **F · broker process** | `cmd/travis-broker`; msgpack over UDS; **named pipe (Windows)**; peer identity checks; **`os.Executable()` resolution + embedded-hash verification** (D-12, R-23); socket nonce; **`AddDecisionRule`** (D-30); **swap `LocalBroker` → `RemoteBroker`** — the moment enforcement becomes real |
| **G · `session` / Journal** | **Durable snapshot-before-write ordering with `fsync` of blob *and* parent dir** (D-13, R-25); content-addressed blob store; sqlite `Store` with `Append`/`Load`/`Fork`; `kill -9` crash-injection tests; `/undo` |
| **H · approval + autonomy** | Approval gate (reversible ∧ in-scope ∧ in-budget); approval modal; `decisions.toml` + `/decisions`; `/auto`; **`exec` classified irreversible** (D-27); `max_daily_cost_usd` (D-34) |
| **I · deny-list + redaction** | Deny rules and the concrete per-OS list (D-22, `OQ-26`); `.travisignore`; **write-deny `.travis/` and `.git/hooks/`** (D-30); the secret scanner, which also unlocks **trace redaction** |

**Pick the session backend deliberately, not by reflex.** §5.15 lists three behind the
`Store` interface, so this is reversible — but the obvious choice is the wrong one:

| Backend | Trade-off |
| --- | --- |
| `mattn/go-sqlite3` | ❌ **CGO** — breaks `CGO_ENABLED=0` and the single-static-binary row in §8.1's support matrix |
| `modernc.org/sqlite` | pure Go, so no CGO, but a large dependency and slower |
| `jsonl` | stdlib only, greppable, no indexed queries |

⚠️ **UNDECIDED — decide at the start of Phase 2.** The recommendation is `jsonl`:
nothing in Phase 2 needs indexed queries, and it keeps the dependency count at zero. But
this has not been agreed, and `modernc.org/sqlite` is a legitimate choice if you would
rather have real queries and accept the dependency. The only option that is genuinely
ruled out is `mattn/go-sqlite3`, because CGO contradicts §8.1's support matrix.

**2c · serial:** the red-team suite (200 cases, §9.4) — the **M2 gate**. Cases authored from
Phase 0 onward, all asserting `PolicyDenied` (D-29).

**Exit (M2):** red-team suite green on all three OSes; `paranoid` provably blocks writes;
sandbox tier reported as `none`, honestly, in the status bar.

### Phase 3 — Ecosystem · **highly parallel**

> **In plain words:** connect travis to the outside world. Write the MCP client **once**, and
> every server in the public ecosystem becomes a few lines of config — that is how the
> assistant reaches email, calendar and notes without an integration per service. Add the
> skills loader and `AGENTS.md` support.
>
> **The one thing that must not slip:** `Registry.Prune`. Twelve MCP servers is ~300 tools
> and 40k tokens of schemas on *every* request. Without pruning the app is unusable on a
> small local model the day MCP lands — which is why D-03 makes it an exit criterion rather
> than a follow-up.
>
> **Under D-11 this is the identity phase.** Before it, travis is a file editor. After it,
> an assistant.

`mcp` generic client + supervision ‖ `skills` loader ‖ **`Registry.Prune`** ‖ `AGENTS.md`
(D-23) ‖ palette + tools/context inspectors

**Exit (M3):** 10 public MCP servers install from a pin with **zero per-server Go code**, on
all three OSes (`npx.cmd` path included), tool-schema tokens under 6k with all 10 loaded.

### Phase 4 — Sandbox, then voice

> **In plain words, two goals.**
>
> **Sandbox** — Phase 2 stopped *the model* acting outside its permissions. But MCP servers
> are other people's programs that never ask our broker; they call the OS directly. The
> sandbox is us asking the OS to fence them in. See `DESIGN-overview.md` §13 for why the
> broker alone cannot do this.
>
> **Voice, both directions (D-15).** *Output* is mostly plain Go — a parser picking the one
> sentence worth hearing, handed to the OS's speak command. *Input* is the expensive half: a
> separate `travis-audio` binary owning the microphone via `malgo` (keeping CGO out of the
> main program), voice-activity detection, local Whisper, and three permission models.
>
> ⚠️ **These two compete for the same people.** `voicein` needs per-OS work — macOS
> entitlements and dialogs, Windows privacy settings and device-change events, Linux
> no-device degradation (R-19) — drawing on **the same three platform owners the sandbox
> needs.** **Sequence them: sandbox first** (it is the submission requirement and the less
> compressible of the two), `voicein` after. C8 budgets 1–2 weeks for the CGO/audio work
> alone.

| Workstream | Who | Work |
| --- | --- | --- |
| **Sandbox** (D-14) — *first* | the 3 platform owners — 1 Linux, 1 macOS, 1 Windows | Shared `Sandbox` interface + 3 stubs merged first (D-25, ~1 day), then Linux user-ns + Landlock + seccomp ‖ macOS `sandbox-exec` deny-all ‖ Windows restricted token + job object + firewall rule. **`strict` tier, for MCP servers** |
| **Voice** (D-15) — *after* | output can start early (portable Go); input needs the platform owners | Speech selection (`<speak>` parser + tier-3 fallback summariser) ‖ `system` TTS backend ‖ then `cmd/travis-audio`, `malgo` capture, VAD, local `whisper_server`, per-OS mic permissions |

**Open at phase start:** `OQ-24` (Windows `strict`: restricted token vs AppContainer — the
hardest sub-question) · the **licensing audit** (D-34 item 3: `malgo`, whisper.cpp, and the
Whisper *model weights*, which are licensed separately from the code).

### Phase 5 — Rigour

> **In plain words:** find out whether travis is good, with numbers instead of opinions.
> Write realistic assistant tasks, run them, score the results. The three numbers that
> matter measure whether D-09's bet paid off: how often it interrupts you
> (`prompts_per_task`), how often you undo it (`undo_rate`), and how many tasks finish with
> no interruption (`unattended_completion_rate`).
>
> **Why last:** you cannot measure quality before there is something to measure. Note the
> *test infrastructure* (cassettes) landed in Phase 1 — this phase is the *measurement
> product*, a different thing. See `DESIGN-overview.md` §13.

`eval` scenarios (assistant-shaped, home-grown per `OQ-17`) · scorecard:
`prompts_per_task`, `undo_rate`, `unattended_completion_rate`, `edit_retry_rate`,
`tokens_of_tool_schemas`.

---

## 4. Priority stack

**Base — must ship, in this order:**

1. Phase 0 — frozen contracts
2. Phase 1 — text loop (M1)
3. Phase 2 — policy + broker + journal + approvals + deny-list (M2)
4. Phase 3 — MCP + skills + `Prune` + `AGENTS.md` (M3) · *identity-critical under D-11*
5. Phase 4 — sandbox (`strict`, for MCP) **then** voice

**Good to have, ranked by value ÷ effort:**

1. **eval scorecard depth** (Phase 5) — what makes D-09 measurable
2. **PII Level 3** — outbound scanning. Promoted under D-11 (D-22)
3. **Plan mode** as a feature — ~3 days. The §6.4.1 *rationale* is already written (D-24)
4. **`piper` TTS backend** — better voice than the OS `system` command

**Rejected for v1 — each has a written rationale in `DESIGN.md`:**

- **User-supplied hooks** (D-20) — advisory in the brain, unsafe in the broker
- **Swarm** (C11) — lowest value-per-unit-risk item in the project; keep `exp_swarm` off
- **Tree-sitter repo map** (D-21) — a coding-agent feature, and it reintroduces CGO

---

## 5. OS-specific work

**Applies to four packages only.** §8.1 permits OS-specific code in `config` (paths),
`security` (sandbox, IPC transport, ACLs), and `voicein`/`voiceout` (device access).
Everywhere else `runtime.GOOS` is a **review blocker**, enforced by `make lint-portability`.
Over-applying this pattern manufactures platform divergence in packages that should be
portable — **trust the lint over judgment.**

**The pattern (D-25):**

1. The shared developer merges the **interface + stubs for all three OSes** to `main` — same
   signature everywhere, `ErrUnsupported` bodies, plus a `_stub.go` that fails closed on an
   unknown GOOS. One day.
2. Three platform owners fill in their file, in parallel, on their own machine.
3. The shared developer fills in the portable logic **concurrently**, not ahead.

```
security/transport.go          ← interface + portable logic
security/transport_linux.go    ← same signature
security/transport_darwin.go   ← same signature
security/transport_windows.go  ← same signature
security/transport_stub.go     ← unknown GOOS, fails closed
```

Nothing gets "sent" to anyone; everything goes through `main`. Why not "one developer works
slightly ahead": that is a pipeline stall — three people idle if they stop — and it
re-creates the churn problem, because the shared code *is* the interface.

**Machines:** 2 macOS · 2 Windows · 1 Linux, all native, no VMs.

**Every OS-specific file needs a same-shaped test that runs on that OS in CI.** Nobody can
review or run two-thirds of this code, so **CI is the only cross-check** — which is what
makes the blocking three-OS matrix do double duty.

⚠️ **Two known exposures:**

- **Linux has one owner, and Linux is where Landlock lives** — the strongest sandbox tier
  and the most security-critical platform work. Bus factor of 1. Free mitigation: make
  Landlock's CI suite the gate rather than one person's laptop, since `ubuntu-latest` lets
  anyone verify it.
- **Windows carries the heaviest load** — 11 distinct items: `Paths()` entries, named-pipe
  IPC + peer check, `DuplicateHandle`/batched read fast path (R-20), `PATHEXT` +
  `.bat`/`.cmd` + job objects, case-insensitive glob matching (R-22), the Windows red-team
  family (8.3 names, `\\?\`, ADS, reparse points), restricted-token sandbox, `conhost`
  degradation, WASAPI handling, Credential Manager keyring, ACLs instead of `0600`. Two
  owners is right.

**Best Windows ramp task:** authoring the Windows red-team cases. Only someone on Windows
can write them, they are *data not Go*, and they teach the threat model.

---

## 6. Work tracking

Thirteen defects were found in `DESIGN.md` during review. **All now have agreed fixes,
written into `DESIGN.md` v0.4. None are built.** This table tracks implementation only —
the fixes themselves live in the sections named.

| # | What | Fix in `DESIGN.md` | Phase | Built |
| --- | --- | --- | --- | --- |
| 1 | 🔴 Broker binary unverified | §4.4, D-12, R-23 | 2 · F | ☐ |
| 2 | 🔴 Journal write-ordering not durable | §5.15.1, D-13, R-25 | 2 · G | ☐ |
| 3 | 🔴 Compaction launders untrusted content | §5.12.4, §5.4.2, D-26, R-24 | 2–3 | ☐ |
| 4 | 🔴 Model can write its own `decisions.toml` | §6.4, D-30, R-26 | 2 · F+I | ☐ |
| 5 | 🟠 `exec` irreversibility unstated | §6.4, D-27 | 2 · H | ☐ |
| 6 | 🟠 No deny-list; "home minus secrets" undefined | §5.12.2a, D-22 | 2 · I | ☐ |
| 7 | 🟠 `memory` unbounded | §5.6, D-28 | 1 · B | ☐ |
| 8 | 🟠 Red-team cases can pass for the wrong reason | §9.4, D-29 | **0** (convention) | ☐ |
| 9 | 🟡 Tool naming inconsistent | §3, D-08, D-31 | **0** | ☐ |
| 10 | 🟡 Concurrent sessions undefined | D-32 | 2 | ☐ |
| 11 | 🟡 Tokenizer mismatch | §5.4.2, D-33, `OQ-27` | 1 · C | ☐ |
| 12 | 🟡 Plan mode never engaged with | §6.4.1, D-24 | — | ✅ written |
| 13 | ⚪ Smaller items (daily cost cap, privacy statement, licensing, accessibility) | D-34 | 2 / any | ☐ |

**Items 8 and 9 belong in Phase 0** despite low severity, purely because they get more
expensive with every week: the red-team convention must exist before 200 cases are authored,
and the tool rename is trivial in an empty repo but a migration once config files reference
the old names.

**Open questions that gate a phase** — full register in `DESIGN.md` §12:

| Needed by | Question |
| --- | --- |
| **Phase 0** | `OQ-02` bus ordering guarantees |
| Phase 1 | `OQ-03` config hot-reload · `OQ-23` which OAuth vendors at launch · `OQ-27` is the token-count heuristic good enough |
| **Phase 2 start** | `OQ-26` — the deny-list's four sub-questions (override? legitimate need? writes too? who owns the per-OS list?) |
| **Phase 2 start** | Session backend: `jsonl` or `modernc.org/sqlite`. Not agreed — see track G |
| Phase 3 | `OQ-09` MCP sampling · `OQ-10` MCP roots · `OQ-11` skill capability grants |
| **Phase 4 start** | `OQ-24` Windows `strict`: restricted token vs AppContainer · the licensing audit |
| Phase 4 end | `OQ-28` does `workspace` tier for `exec` get picked up |
