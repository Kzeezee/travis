# travis

A personal AI assistant for your terminal. Bring any model, plug in MCP servers and
skills, and let it do real work on your files without handing it your machine.

## What travis is

travis is a terminal-native assistant written in Go with a
[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) UI. It is a **general
personal assistant**, not a coding agent: notes, journals, admin, research, and the
odd script. It reads and edits files, searches, fetches web pages, runs commands you
allow, remembers things between sessions, asks you when it's unsure, and can talk and
listen.

It runs natively on Linux, macOS, and Windows.

## What makes it different

**Safety is enforced in code, not asked of the model.** Most agents tell the model to
behave. travis doesn't trust the model at all:

- **A separate broker process does every privileged effect.** File writes, network,
  `exec`, and secrets all go through `travis-broker`, which re-checks policy against
  the *resolved* path or host, not what the model said it would do. If the broker is
  unreachable, travis fails closed. The broker binary is SHA-256 verified before every
  session, so a swapped binary can't inherit its permissions.
- **Deny by default.** No grant, no syscall. A hard deny-list (`~/.ssh`, `~/.aws`,
  `**/.env`, OS keychains, persistence paths) is checked first and can't be relaxed.
- **MCP servers run in an OS sandbox**: Landlock on Linux, Seatbelt on macOS, a
  restricted token on Windows. The broker stops the model; the sandbox stops a
  compromised server that calls the OS directly.
- **Untrusted content stays untrusted.** Web pages, MCP results, and workspace
  `SKILL.md`/`AGENTS.md` are delimited, can't change grants, and raise the approval
  level for that step. At compaction they are dropped, never summarised into trusted
  text.
- **Approvals are keyboard-only.** Voice can tell you an approval is waiting; it can
  never answer one.

**Autonomy comes from undo, not from more prompts.**

- Every write is snapshotted first, so `/undo` rewinds to before any earlier turn.
- `[a] always` writes a scoped rule to `.travis/decisions.toml`. Day one is chatty, day
  three is quiet. Only the broker writes that file, so the model can't approve itself.
- Irreversible actions, like `exec`, **always** ask. No rule can relax that.

**No vendor lock-in.** Any OpenAI-compatible endpoint works: OpenAI, DeepSeek, Gemini,
Ollama, llama.cpp, OpenRouter. Switch mid-chat with `/provider`.

**Uses the ecosystems that already exist.** MCP servers and `SKILL.md` skills plug in
with zero Go code. `travis mcp add` shows a provenance card (publisher, downloads,
integrity) and pins an exact version before anything is installed.

**Speaks selectively.** The model marks what's worth saying out loud, so you hear the
summary, not the 400-line diff. Speech-to-text runs on your machine.

**Measured, not vibed.** The [evaluation](docs/EVAL.md) runs 36 tasks × 3 trials in a
network-less container, plus a prompt-injection suite across five attack surfaces.
With policy on, attack success was 0/10.

## Installation

You need **Go 1.25+** and `make`. Windows users without `make`, see
[DEV-SETUP §6](docs/DEV-SETUP.md#6-the-makefile-and-what-ci-will-run).

```sh
git clone https://github.com/Kzeezee/travis.git
cd travis
make install          # installs travis + travis-broker to $(go env GOPATH)/bin
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`.

> `go install` and `go run` won't give you a working chat. travis refuses any broker
> whose hash wasn't stamped in at build time, and `make install` is what stamps it.

Optional voice input (needs a C compiler; Linux x86_64/arm64 and Apple-silicon macOS):

```sh
make install-voice    # mic helper + local Whistle speech model
```

## Quick start

```sh
cd ~/notes            # travis works in the directory you start it from
travis chat
```

The first run opens a setup wizard: pick a provider, paste a key (saved to your OS
keychain, never to a file), toggle voice, and review the security summary. Re-run it any
time with `travis init`.

For a local model, point a profile at Ollama:

```toml
# travis config path   → prints where this file lives
[provider]
active = "local"

[provider.profiles.local]
kind     = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model    = "qwen3:14b"
```

## Guide

### Commands

| Command | What it does |
| --- | --- |
| `travis chat [--model M] [--base-url U]` | Start a chat in the current directory |
| `travis init` | Run the setup wizard |
| `travis config path\|init\|check\|show` | Find, create, validate, or print the merged config |
| `travis provider list` | Profiles, which one is active, and whether each has a key |
| `travis models` | Models offered by the active endpoint |
| `travis mcp list\|add\|remove` | Manage MCP servers |
| `travis version` | Print the version |

### In chat

| Slash command | What it does |
| --- | --- |
| `/help` | List commands |
| `/new` | Start a new conversation |
| `/model [name]` | Show or switch the model |
| `/provider [name]` | List or switch provider profiles (asks for a key if needed) |
| `/tools` · `/mcp` · `/skills` | What's loaded, and each MCP server's state |
| `/memory` | Show what travis remembers |
| `/cost` | Tokens used this session |
| `/undo` | Rewind files to before an earlier turn |
| `/quit` | Exit |

Approval prompts: `y` once, `a` always (writes a durable rule), `n` deny. Irreversible
actions only offer `y` or `n`.

Hold `ctrl+t` to talk (if voice input is on). The transcript lands in the input box for
you to edit, and you press enter. Terminals that can't report key releases fall back to
press-to-start, press-to-stop.

### Tools

Eight built-ins: `fs_read`, `fs_write`, `fs_edit`, `grep`, `exec`, `fetch`, `ask_user`,
`memory`. MCP tools appear as `mcp__<server>__<tool>`.

### Adding MCP servers

```sh
travis mcp add fs --allow-net npm:@modelcontextprotocol/server-filesystem ~/notes
travis mcp add time --allow-net pypi:mcp-server-time
travis mcp add mine -- npx -y some-server@1.2.3     # explicit command, exact version
```

travis shows the package's provenance and saves it only when you type `y`. The runner
(`npx`, `uvx`, …) must also be granted in your user config:

```toml
[security]
exec_allow = ["npx", "uvx"]
```

`allow_net` lets the first launch download the package. Without it the sandbox blocks
the network (where the OS can).

### Skills

Drop `SKILL.md` folders into the `skills/` folder next to your user config, or add more
roots under `[skills] roots`. A workspace `AGENTS.md` or workspace skill needs one
keyboard approval per content version per session.

### Configuration

Config is layered: compiled defaults → system → user `config.toml` → workspace
`.travis/config.toml` → `TRAVIS_*` env vars → flags. The workspace file **can't** set
`[provider]` or `[security]`, so a cloned repo can't redirect your API key or grant
itself permissions.

Keys are referenced, never stored: `auth.ref = "keyring://NAME"` or `"env://NAME"`.

Voice output: `[voice.output] enabled = true` speaks through the OS (`say`, `espeak-ng`,
or SAPI). Voice input: `[voice.input] enabled = true` after `make install-voice`.

Each session writes a JSONL trace (prompts, tool calls, results, token use) to your
state folder and prints its path on exit. Turn traces off with `[eval] traces = false`
in your user config. Set `TRAVIS_HOME` to keep a throwaway session out of your real
state.

Full reference and troubleshooting: [docs/DEV-SETUP.md](docs/DEV-SETUP.md).

## Contributing

Read these first, in order:

1. [`AGENTS.md`](AGENTS.md): code standards, naming, and hard rules
2. [`docs/Code-Conventions.md`](docs/Code-Conventions.md): the pattern checklist
3. [`docs/DESIGN-overview.md`](docs/DESIGN-overview.md): the short design
4. [`docs/DEV-SETUP.md`](docs/DEV-SETUP.md): clone to working editor

Ground rules:

- **One Go module, everything under `internal/`.** No `pkg/`.
- **Inner layers never import outer ones.** `core` → `security` → `tools`/`providers`
  → `agent/loop` → `frontends`/`state` → `cmd`. Lint enforces this.
- **Effects stay behind ports.** Tests use fakes and recorded cassettes, never real
  providers, networks, or MCP servers.
- **`runtime.GOOS` only in OS-owning packages**, and no hard-coded paths. Linux, macOS,
  and Windows are all first-class.
- **Decisions D-01 to D-34 in [`docs/DESIGN.md`](docs/DESIGN.md) §0.5 are settled.**
  Reversing one needs an RFC, not a PR comment.
- Conventional commits (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `style:`). Never
  mix a whole-file format pass with content edits.

Before pushing:

```sh
make check            # build, vet (3 OSes), lint, test -race, tidy, govulncheck
```

CI runs the same natively on Ubuntu and Windows; add the `ci:macos` label to a PR that
touches build tags, paths, security, voice, or golden files.

Live evals (`make eval-live`) spend model credit and run only on your machine. See
[DEV-SETUP §6](docs/DEV-SETUP.md#6-the-makefile-and-what-ci-will-run).

## Misc

### Repository layout

```
cmd/travis/          composition root: wiring and flags only
cmd/travis-broker/   the privileged-effect broker
cmd/travis-audio/    cgo microphone helper (kept out of the main binary)
internal/
  core/              shared types, Bus, Tool and Provider interfaces
  config/            layered config and OS paths
  agent/loop/        the turn loop, budget, prompt, compaction
  providers/         OpenAI-compatible adapter and replay cassettes
  tools/             registry, built-ins, MCP client, skills
  security/          policy, broker, capabilities, redaction, OS sandbox
  state/             sessions, memory, snapshot blobs, spend ledger
  frontends/         terminal UI, voice in, voice out
  eval/              traces and scenarios
eval/                Inspect-based live eval, judge, published results
docs/                design, plan, setup, eval report
```

### Docs

| Doc | For |
| --- | --- |
| [`docs/DESIGN.md`](docs/DESIGN.md) | The normative design and decision register |
| [`docs/DESIGN-overview.md`](docs/DESIGN-overview.md) | The short version |
| [`docs/PLAN.md`](docs/PLAN.md) | Build order and parallelism |
| [`docs/EVAL.md`](docs/EVAL.md) | Evaluation results, generated by `make eval-report` |

### Known limits

- Windows' restricted-token sandbox and Linux Landlock before kernel 7.1 can't block an
  MCP server's network; the approval prompt says so. Linux 5.19 is the supported floor.
- Traces aren't redacted yet. They're created readable only by you, but read one before
  sharing it.
- Published eval numbers are for one model (DeepSeek V4.1 Flash), not travis in general.

### License

[Apache License 2.0](LICENSE).

### Credits

Built for CS464 Full-Stack Development by the travis team. See the
[contributors](https://github.com/Kzeezee/travis/graphs/contributors).
