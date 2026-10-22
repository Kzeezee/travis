# Phase 6 spike: headless run and Inspect (TRA-55)

[Back to Phase 6](phase-6.md)

Run 2026-10-22 on macOS (Darwin 25.3, Apple Silicon), Docker Desktop 29.7 (linuxkit 7.0,
aarch64), inspect-ai 0.3.283. Harness: `travis run` (`cmd/travis/run.go`) and
`eval/inspect/` on branch `feat/tra-55-phase-6-spike-headless-run-and-inspect`.

**Decision: Inspect route accepted.** 6B–6F build on it; the Go-runner fallback is not needed.

## What ran

`eval/inspect/notes.py` builds travis from source into a Debian image with the `notes`
fixture at `/workspace`, starts `sandbox_agent_bridge`, and runs
`travis run --base-url http://localhost:13131/v1 --model inspect --answers … --prompt …`
inside the container. The container has `network_mode: none`; model traffic goes through
Inspect's exec channel to the bridge, so travis cannot reach anything else. The scorer reads
the fixture files back.

| Run | Prompt | Tools | Result | Tokens (in / cache read / out) | Time |
|---|---|---|---|---|---|
| 1 | "Move my dentist appointment to 10:15 and write a summary." | grep ×2, fs_read ×4 | **fail**: model declined, see below | 2,560 / 6,016 / 1,116 | 24 s |
| 2 | "The clinic moved my 13 October dentist appointment to 10:15 …" | grep ×5, fs_read, fs_edit, fs_write | **pass** | 1,427 / 12,032 / 922 | 27 s |

Both transcripts open in `inspect view --log-dir eval/inspect/logs`, with token usage.

## Findings

- **The bridge accepts `openaicompat` unchanged.** Streaming and tool calls worked over the
  OpenAI Chat Completions endpoint with no adapter change. Inspect records every model call,
  so token usage and cost come from Inspect, not from travis's trace.
- **The broker and policy run inside the container.** Writes inside the workspace were
  allowed without approval under the default profile, so run 2 used no answers. The
  answers file is still unexercised; 6C needs a task that asks.
- **Fixture dates go stale.** The `notes` fixture's appointment is 13 October 2026, and
  travis puts today's date in the system prompt. On 22 October the model rightly refused to
  move a past appointment (run 1). Live tasks need a pinned clock for travis (`core.Clock`
  is already injected) or dates far in the future. Run 2 avoids this by naming the date.
- **The MCP sandbox is not exercised.** This task uses no MCP server, so whether Landlock
  works under Docker Desktop's kernel is untested. The first MCP task in 6C will show it.
- **Model.** DeepSeek's own API was used instead of OpenRouter. `deepseek-flash` is served
  by DeepSeek-V4.1-Flash-1017, the planned model. The model name is a parameter, so
  switching back to `openrouter/deepseek/deepseek-v4.1-flash-1017` needs no code change.
  DeepSeek has no per-key credit limit, so the hard budget stop is the account balance.

## Cost

At the V4.1-Flash-1017 list price (USD 0.15 / M input, 0.60 / M output, cache reads
counted at the full input price as an upper bound), run 2 cost about USD 0.0026 and run 1
about USD 0.0020. Call it **USD 0.003 per run** for a short file task.

The published run at 30 tasks × 3 trials is about USD 0.30 at that rate. Multi-step, MCP
and injection tasks will run longer; even at 10× the cost per run the published run stays
inside its ~USD 5.60 share, so 6B can keep k=3.

## Left for 6B

- `runHeadless` duplicates `runChat`'s setup; share one assembly.
- Exit codes per stop reason, and the answers file validated at the boundary (shape only so
  far). `--answers` lets a file answer approvals, which D-10 forbids for real use; keep the
  command eval-only.
- A shared solver for all tasks, and results exported as JSONL rows.
- The Python project sits at the repo root from `uv init`; move it under `eval/inspect/`.
