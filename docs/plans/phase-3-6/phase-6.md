# Phase 6: evaluation

[Back to overview](overview.md)

Two jobs that are often conflated. Regression asks "did we break it?" and needs
determinism. Capability and safety ask "is it good, and can it be turned?" and need live
models, repeated trials and statistics. Cassette replay answers only the first: replaying
recorded responses gives the same result every time, so it measures our code, not the
agent.

**Input state.** `internal/eval/trace` writes redacted JSONL traces with `TurnCompleted.Usage`
and timestamps. `internal/providers/cassette` replays HTTP in tests.
`cmd/travis/e2e_test.go` runs the notes and write tasks end to end, once from a cassette.
`testdata/scenarios/phase2-redteam.toml` drives the policy-level red-team gate, which runs
no model. travis has one provider adapter, `openaicompat`.

**Tooling split.** Regression (6A) stays in Go and runs in-process on every PR. Live
evaluation (6B–6F) uses [Inspect AI](https://inspect.aisi.org.uk/), the UK AI Security
Institute's eval framework, with travis run as a black box through a headless mode. Inspect
supplies repeated trials, scorers, model judges and a transcript viewer, and its logs are a
format other evaluators already read. 6-spike decides whether that route works; if it does
not, 6B–6F fall back to a Go runner with the same results schema.

**Model.** DeepSeek V4.1 Flash through OpenRouter's OpenAI-compatible API, served by the
existing `openaicompat` adapter. One model fits the budget. Local models are out: they lack
the context for tool schemas, skills and `AGENTS.md`. Adding Claude or Gemini, possibly on a
subscription instead of per-token billing, is a later decision; the runner takes the model
as a parameter, so adding one needs no code change.

**Budget.** SGD 10 in total for live runs (about USD 7). One OpenRouter key with a credit
limit set to the budget is the hard stop; the runner's own cap is the soft stop.

| Use | Share | Rule |
|---|---|---|
| Spike and development | ~20% | One trial, smallest task subset |
| Published run (`docs/EVAL.md`) | ~80% | Sized from the spike's measured cost per run |

Sizing the published run: runs = tasks × trials. Start at 30 tasks × 3 trials (90 runs).
If that exceeds the share, cut trials to 2 before cutting tasks. Never go below 30 tasks;
below that the 95% interval is too wide to report anything.

### 6A Regression suite (new)

| | |
|---|---|
| Changes | `internal/eval/scenario`: a scenario is a fixture directory, scripted turns, a cassette, and assertions on final file state and on tools called or not called. Convert the existing e2e notes and write tasks into scenario files. Known-bad scenarios must fail. |
| Proof | `go test ./internal/eval/...` in CI on every PR; a deliberately broken assertion fails. |
| Depends on | Nothing |

### 6-spike Headless run and Inspect (new, one day)

| | |
|---|---|
| Changes | Minimal `travis run --prompt <text> --answers <file>`: answers approvals and `ask_user` from a file, writes the trace, exits with the turn outcome. One Inspect task for the notes scenario that runs it in a sandbox, routes its model calls through Inspect's agent bridge, and scores the resulting files. |
| Output | One graded transcript in `inspect view` with token usage. Measured cost per run on DeepSeek V4.1 Flash. A note in this folder: Inspect route accepted or rejected, with the reason. |
| Depends on | 6A |

### 6B Live runner (new)

| | |
|---|---|
| Changes | Inspect route: `travis run` hardened (exit codes, answers file validated at the boundary), a shared solver for all tasks, results exported as JSONL. Fallback route: a Go runner through `assembleChatSession` with a scripted approver and simulated user. Either route: k trials per task, budget cap, `make eval-live`, and a GitHub Actions workflow started by hand (`workflow_dispatch`) with the OpenRouter key as a repo secret. No scheduled runs. |
| Data | Result row: `{task, model, trial, passed, steps, tokens, cost_usd, latency_ms, transcript}`. |
| Proof | One task × DeepSeek × k=3 produces three rows and three transcripts, locally and from the manual workflow. |
| Depends on | 6-spike |

### 6C Task set and graders (new, grows continuously)

| | |
|---|---|
| Changes | 30–50 tasks across notes retrieval, file edits, multi-step work, clarification via `ask_user`, refusal or out of scope, MCP, skills, `fetch`. Code graders first; a model judge only for open-ended answers. About 20% held out and not used during development. Every Phase 3–5 ticket adds its own tasks. |
| Proof | Each grader has a passing and a failing fixture. |
| Depends on | 6B |

### 6D Judge calibration (new)

| | |
|---|---|
| Changes | Hand-label about 50 transcripts. Report judge–human agreement per criterion. Only criteria above the agreed threshold count in the scorecard. |
| Proof | Agreement table committed; known-bad transcripts fail the judge. |
| Depends on | 6C |

### 6E Injection eval (new)

| | |
|---|---|
| Changes | AgentDojo-style suite with the model in the loop. Attacks planted in fetched pages, MCP results, AGENTS.md, skills and workspace files; starts from the 3F and 3E red-team cases. Reports utility without attack, utility under attack, and attack success rate with defences on and off. Canary-secret check: the canary never appears in traces, the session log or memory. |
| Proof | With policy and untrusted marking off, attack success is non-zero (the suite can detect attacks); with them on, it is reported with intervals. |
| Depends on | 6B, 3E, 3F |

### 6F Report and ablations (new)

| | |
|---|---|
| Changes | A script turns results into `docs/EVAL.md`: method, models and dates, pass@1 with Wilson 95% intervals, pass^k (succeeds in all k trials), cost and latency, an error-analysis summary with linked sample transcripts, limitations. Ablations: policy off/on, untrusted marking off/on, `fs_write` only vs with `fs_edit` (D-08), pruning off/on, skills off/on, supervised vs collaborative autonomy (D-09). Ablations run at one trial each to fit the budget. |
| Proof | `make eval-report` regenerates `docs/EVAL.md` from committed results with no diff. |
| Depends on | 6C, 6D, 6E |

## Risks

- At 30 tasks the 95% interval is roughly ±17 points. The report must not claim smaller
  differences.
- The budget allows about one published run. Development uses replay where possible.
- One model means results describe travis on DeepSeek V4.1 Flash, not travis in general.
  The report says so.
- If the budget runs short, keep 6A, 6B, 6C and 6E; cut calibration depth and ablation count.
