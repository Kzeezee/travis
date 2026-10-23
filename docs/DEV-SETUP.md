# Development setup

From a fresh clone to a working editor. ~15 minutes.

⚠️ **Only the macOS path below has been verified end to end.** The Linux and Windows
commands are the standard ones but are unconfirmed — if you hit a difference, fix this file
in the same PR.

---

## 1. Go

**Required: Go ≥ 1.25.0.** `go.mod` sets that directive because `charm.land/bubbletea/v2`
requires it. Anything older will not build.

| OS | Install |
| --- | --- |
| **macOS** | `brew install go` (currently gives 1.27.0) |
| **Linux** | your distro's package may be too old — check `go version`. Otherwise download from <https://go.dev/dl/> and unpack to `/usr/local/go` |
| **Windows** | the MSI from <https://go.dev/dl/>, or `winget install GoLang.Go` |

Verify:

```
go version        # must be >= go1.25.0
```

## 2. Language server and debugger

`gopls` is what gives go-to-definition, hover documentation, and autocomplete. `dlv`
(Delve) is the debugger. Both install as ordinary Go programs:

```
go install golang.org/x/tools/gopls@latest
go install github.com/go-delve/delve/cmd/dlv@latest
```

They land in `$(go env GOPATH)/bin`, which is **not** on your PATH by default.

| Shell | Add to |
| --- | --- |
| zsh (macOS default) | `~/.zshrc`: `export PATH="$(go env GOPATH)/bin:$PATH"` |
| bash | `~/.bashrc`, same line |
| Windows PowerShell | add `%USERPROFILE%\go\bin` to your user PATH via System Settings |

Verify — **in a new terminal**:

```
gopls version
dlv version
```

> Note: `.zshrc` is only read by *interactive* shells. If `zsh -l -c 'gopls version'`
> fails but a normal terminal works, that is expected, not a broken setup.

## 3. Editor

**VS Code** — install the extensions:

```
code --install-extension golang.go
code --install-extension tamasfe.even-better-toml
code --install-extension redhat.vscode-yaml
code --install-extension ms-vscode.makefile-tools
```

If `code` is not on your PATH, open VS Code and run *Shell Command: Install 'code' command
in PATH* from the command palette.

Why these four: `golang.go` drives gopls and the test runner · travis's config is TOML
throughout · `deps.yaml`, `.golangci.yml` and the CI workflows are YAML · the repo has a
`Makefile` (§6).

**Suggested workspace settings.** `.vscode/` is gitignored, so this is per-person. Create
`.vscode/settings.json`:

```jsonc
{
  "gopls": {
    "ui.documentation.hoverKind": "FullDocumentation",
    "ui.diagnostic.staticcheck": true,
    "ui.inlayhint.hints": {
      "assignVariableTypes": true, "compositeLiteralFields": true,
      "constantValues": true, "parameterNames": true, "rangeVariableTypes": true
    }
  },
  "[go]": {
    "editor.defaultFormatter": "golang.go",
    "editor.formatOnSave": true,
    "editor.codeActionsOnSave": { "source.organizeImports": "explicit" }
  },
  "go.testFlags": ["-race", "-v"],
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "workspace"
}
```

`-race` on tests matches what CI runs (§8.1).

`lintOnSave` is worth setting: gopls runs staticcheck but not `golangci-lint`, so without
it the project's own mechanical rules (`depguard`'s import directions, `forbidigo`'s
`time.Now`/`runtime.GOOS` bans, `exhaustruct`'s required dependencies) are first seen at
`make lint` or in CI. `"workspace"` rather than `"file"`, because those rules are about
call sites across a package and per-file linting misses the ones in other files.

**JetBrains GoLand** works too; it bundles its own analysis rather than using gopls. No
project config needed beyond pointing it at the Go SDK.

**Restart the editor** after installing Go, or it will not find `gopls`.

## 4. Verify the checkout

```
go build ./...
go test ./...
go vet ./...
go mod verify
```

All four should pass on a clean clone.

## 5. Model provider, for Phase 1 onward

Not needed to build or test — agent loop and provider unit tests must never touch the
network (cassettes, D-16). You will want a local model for manual work:

```
brew install ollama          # or https://ollama.com/download
ollama pull qwen3:14b
```

Then create a `config.toml` in your **user config directory** (not the workspace
`.travis/` one — see the next paragraph for why). `travis config path` prints where that
is on your machine, and `travis config init` writes a commented starter file there
(it refuses to overwrite an existing one):

| OS | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/travis/config.toml` (→ `~/.config/travis/config.toml`) |
| macOS | `~/Library/Application Support/travis/config.toml` |
| Windows | `%AppData%\travis\config.toml` |

```toml
[provider]
active = "local"

[provider.profiles.local]
kind     = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model    = "qwen3:14b"
```

`config.Load()` picks this up automatically on the next `travis chat` — no env var or
flag needed for a local model. `travis config check` validates every layer (non-zero exit
on errors) and `travis config show` prints the merged result, with MCP `env` values masked. To switch back to a cloud model, add a second
`[provider.profiles.<name>]` block and change `active` to point at it, or set
`TRAVIS_PROVIDER_ACTIVE` for a one-off override without editing the file.

**Why not `.travis/config.toml`?** That file is read from whatever directory you run
`travis` in, with no action beyond `cd` + `travis chat` — so a cloned repo could ship
one that redirects `provider.active`/`base_url` and your API key would follow it
there (DESIGN.md risk R-27). The workspace file can still set `[brain]`/`[tools]`, just
not `[provider]`.

**Windows: watch for a UTF-8 BOM.** `go-toml` rejects a leading byte-order mark with a
confusing `invalid character at start of key` error. PowerShell's `Set-Content
-Encoding utf8` writes one in Windows PowerShell 5.1 (`utf8` there means "UTF-8 with
BOM" — `utf8NoBOM` isn't available until PowerShell 6+), and some editors default to
it too. If you hit that error on a file that looks correct, check for a BOM:

```powershell
[System.IO.File]::ReadAllBytes("$env:AppData\travis\config.toml")[0..2]
# BOM present if this prints 239 187 191 (EF BB BF)
```

Write it BOM-free instead:

```powershell
[System.IO.File]::WriteAllText($path, $content, (New-Object System.Text.UTF8Encoding $false))
```

Use local models for day-to-day development; save a paid provider for quality
benchmarking near the end.

**Known gap:** `auth.ref` (e.g. `env://ANTHROPIC_API_KEY`) is parsed but not yet
consulted — every profile currently shares the same `OPENAI_API_KEY` lookup
(`config.ResolveProviderAPIKey()`). A profile needing a different credential source
doesn't work yet; this lands with the broker in a later phase. There is also no
config hot-reload: editing `.travis/config.toml` only takes effect on the next
`travis chat`, not the running session.

### Session traces

Every `travis chat` session records what happened (your prompts, streamed replies,
each tool call with its arguments and result, and token usage per turn) to one JSONL
file, and prints its path when you quit:

| OS | Folder |
| --- | --- |
| Linux | `$XDG_STATE_HOME/travis/traces/` (→ `~/.local/state/travis/traces/`) |
| macOS | `~/Library/Application Support/travis/traces/` |
| Windows | `%LocalAppData%\travis\traces\` |

Each file is named after the session's ULID, so they sort by start time. Each line is
`{"type": "<event type>", "event": {...}}`, and every event carries a `turn_id`, so
`jq` can slice a trace by event type or by turn:

```sh
jq -c 'select(.type | startswith("tool_call")) | .event' <trace>.jsonl   # every tool call
jq -c 'select(.event.turn_id == "<turn id>")' <trace>.jsonl              # one turn
```

Traces hold file contents and are **not redacted yet**, so they are created readable
only by you; don't paste one into an issue without reading it first. To turn them off,
set this in your user config file (a workspace `.travis/config.toml` cannot, so a cloned
repo can't hide what the agent did inside it):

```toml
[eval]
traces = false
```

To keep a throwaway session (tests, experiments) out of your real state folder,
point `TRAVIS_HOME` at another directory; config, state and traces all move under it.

### Voice

Voice output needs nothing extra: set `[voice.output] enabled = true` in the user config and
travis speaks through the OS (`say` on macOS, `espeak-ng` or `espeak` on Linux, SAPI on
Windows). Voice input records through a separate helper, `travis-audio`, kept out of the main
binary because it needs cgo. Build it with `make audio` (a C compiler is required: Xcode
command line tools, `gcc`, or MinGW on Windows). To check it reaches a microphone, record two
seconds of 16 kHz mono PCM:

```
bin/travis-audio capture | head -c 64000 > /tmp/two-seconds.pcm
```

macOS asks the terminal app for microphone access the first time; a denied permission can
record silence rather than fail, so check the file is not all zeros.

Speech-to-text runs in a local whisper.cpp server, so audio from the room never leaves the
machine (D-15). On macOS:

```
brew install whisper-cpp
curl -L -o ~/ai/ggml-base.en.bin https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin
whisper-server -m ~/ai/ggml-base.en.bin --port 8178 --inference-path /v1/audio/transcriptions
```

Then turn it on in the user config:

```toml
[voice.input]
enabled = true
```

Hold `ctrl+t` while speaking; the transcript lands in the input box for you to edit and
send. A terminal that cannot report key releases (VS Code's, for one) falls back to
press-to-start, press-to-stop, and the status bar says so. Pick another key with `ptt_key`
if `ctrl+t` is taken; `ctrl+space` is not the default because VS Code's terminal and
macOS input switching both claim it.

## 6. The Makefile, and what CI will run

`make help` lists the targets. `make check` is everything CI runs, on your machine, in
CI's order: `build vet lint test-race tidy vuln`. Run it before pushing.

| Target | What it is |
| --- | --- |
| `make build` | `go build ./...`, then `bin/travis` and `bin/travis-broker` with the broker's hash stamped in |
| `make chat` | `make build`, then runs `bin/travis chat` here; pass flags with `ARGS="--model foo"` |
| `make dev-config` | installs `scripts/dev-config.toml` as your user config if you have none: local llama-server provider, every command allowed (each still asks), the `everything` MCP server. Dev only; release builds keep the deny-by-default config |
| `make install` | installs `travis` and `travis-broker` to `$(go env GOPATH)/bin` with the hash stamped in, so plain `travis chat` works from any directory once that's on your PATH |
| `make test` / `make test-race` | unit tests; `-race` is what CI runs |
| `make lint` | `golangci-lint run` at the version pinned in `.golangci-lint-version` |
| `make fmt` | `gofmt` + `goimports` — the same formatters gopls applies on save |
| `make vet` | `go vet` for this OS **and** `GOOS=windows` / `GOOS=darwin` |
| `make tidy` | `go mod verify` and `go mod tidy -diff` |
| `make vuln` | `govulncheck ./...` |

**Why `go run ./cmd/travis` and a plain `go build` can't start a chat.** `travis` checks
`travis-broker`'s SHA-256 before every session and refuses a mismatch, or a `travis` built
without the hash (R-23). Two separate checks cause this: `ResolveBrokerExecutable` only ever
looks for `travis-broker` sitting right beside the running `travis` binary, never on PATH —
`go run` builds into a throwaway temp directory with nothing beside it, so this fails first.
Even once it's found, `verifyBrokerBinary` refuses to run it unless its SHA-256 matches the
hash `travis` was built with, which only `-ldflags "-X main.brokerSHA256=..."` sets — plain
`go build`/`go install` never pass that flag, so the check fails closed instead of trusting
whatever binary happens to be on disk. Both guard against a swapped broker silently inheriting
every permission the real one has, so neither can be bypassed with an env var or flag.

**Use `make chat` for local dev** (builds into `bin/` and runs it in place) **or `make
install`** to get a `travis` you can run as a bare command from any directory once
`$(go env GOPATH)/bin` is on your PATH. Without make (Windows), do what `make install` does
by hand, in PowerShell:

```powershell
go build -o "$(go env GOPATH)\bin\travis-broker.exe" .\cmd\travis-broker
$hash = (Get-FileHash "$(go env GOPATH)\bin\travis-broker.exe" -Algorithm SHA256).Hash.ToLower()
go build -o "$(go env GOPATH)\bin\travis.exe" -ldflags "-X main.brokerSHA256=$hash" .\cmd\travis
```

`lint` and `vuln` install their pinned tool into `$(go env GOPATH)/bin` on first use.
Bump a pin by editing `.golangci-lint-version` or `GOVULNCHECK_VERSION`, never by
reaching for `@latest`.

**There is no `make` on `windows-latest`,** so CI calls `go` directly and the targets
above are a convenience, not the definition of the gates. If you add a gate, add it to
`.github/workflows/ci.yml` as a `go` command first, then wrap it here.

**Tests run with `-shuffle=on`.** Order-dependent tests fail intermittently instead of
never. On failure Go prints `-test.shuffle <seed>`; replay it exactly with
`go test -shuffle=<seed> ./...` — the seed is in the CI log too.

**One required check, named `ci`.** It is an aggregator over the real jobs, so a matrix
edit cannot silently rename the required check away.

**`ci:macos` label.** macOS runner minutes cost ~10x Linux, so `test (macos-latest)`
only runs on `main`, on a manual dispatch, or when a PR carries the `ci:macos` label —
otherwise it shows as *skipped* and `ci` is still green. Apply the label when reviewing
or opening a PR that touches build tags, `config` paths, `security`, voice, or golden
files. Applying it re-triggers the workflow; no push is needed.

## 7. Common problems

MCP server commands must be granted in your user `config.toml` before they can launch:

```toml
[security]
exec_allow = ["node", "python3"]
```

Use the command spelled in `[mcp.servers.<name>].command`; an absolute command needs
an absolute allowlist entry, written with forward slashes. This grant allows the launch
approval prompt; it does not approve the server. Removing a grant blocks even a server
previously approved with “always”. Workspace config cannot set `[security]`.

MCP servers run inside the OS sandbox. A server can read its runner's install folder
(for example `/opt/homebrew` for npx, minus its `var` folder, where Homebrew keeps
local databases) and write only its own scratch folder,
`<state dir>/mcp/<name>`, where its home, temp and npm/uv caches live. The network is
off unless the server allows it, except under `restricted-token` (Windows) and
`landlock-no-net` (Linux before 7.1), which cannot block it; the approval prompt says
`network not blocked` there:

```toml
[mcp.servers.everything]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-everything"]
allow_net = true             # the first launch downloads the package
read_paths = []              # absolute paths beyond the defaults
write_paths = []
```

The status bar shows the tier (`sandbox: seatbelt`, `landlock`, `landlock-no-net`,
`restricted-token`). Where this OS has no sandbox, `[security] require_sandbox = true`
(the default) refuses to start the server; setting it to `false` runs it unconfined and
shows `⚠ sandbox: none`. Changing the tier, `allow_net` or the paths asks for approval
again.

On Linux the tier depends on the kernel's Landlock version. **Linux 5.19 is the supported
floor.** 5.13 to 5.18 runs sandboxed, but its Landlock refuses renames between folders,
which uv needs, so uvx servers cannot install; npx copies instead and still works.

| Kernel | MCP servers |
| --- | --- |
| below 5.13 | no Landlock: refused, or unconfined with `require_sandbox = false` |
| 5.13 to 5.18 | `landlock-no-net`; uvx servers fail to install |
| 5.19 to 7.0 | `landlock-no-net`; everything works |
| 7.1+ | `landlock`; TCP and Unix sockets blocked, UDP too from 7.2 |

`[skills] load_workspace` has been removed. Configure workspace skill roots in your
user `[skills] roots` instead. Workspace skills and `AGENTS.md` require one keyboard
approval per content snapshot per chat session. Content changes ask again; declining
keeps the workspace instructions out of the prompt and `skill_open`.

| Symptom | Cause |
| --- | --- |
| `command not found: go` in a new terminal | PATH not picked up — restart the terminal, or check the install location |
| Every `exec` call fails with `exec_denied` / `no exec grant covers` | The command is not in `[security] exec_allow`; `make dev-config` (or `exec_allow = ["**"]`) allows all, each call still asks |
| A newly approved MCP server's tools are absent from the first reply | Discovery runs asynchronously; wait for the server's tools to appear in `/tools` before sending the request |
| An MCP server did not start | Its own errors are in `<state dir>/mcp/<name>/stderr.log`, which the startup line names |
| That log shows an npm `network`, `ENOTFOUND` or `EAI_AGAIN` error | Its first launch downloads the package but `allow_net` is off; set `allow_net = true` for that server |
| That log shows `Invalid cross-device link (os error 18)` from uv on Linux | The kernel is older than 5.19 and its sandbox cannot allow the rename uv needs; use 5.19+ (for example Ubuntu's HWE kernel) |
| `gopls` not found, but it is installed | `$(go env GOPATH)/bin` missing from PATH (§2) |
| Golden-file tests fail only on Windows | line endings. Goldens normalise before comparing (§8.1) — the test is wrong, not your machine |
| Tests pass locally, fail in CI | CI runs `-race` natively on three OSes. Run `go test -race ./...` before pushing |
