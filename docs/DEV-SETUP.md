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
  "go.testFlags": ["-race", "-v"]
}
```

`-race` on tests matches what CI runs (§8.1).

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

Then point `provider.profiles.local.base_url` at `http://127.0.0.1:11434/v1`.

Use local models for day-to-day development; save a paid provider for quality
benchmarking near the end.

## 6. The Makefile, and what CI will run

`make help` lists the targets. `make check` is everything CI runs, on your machine, in
CI's order: `build vet lint test-race tidy vuln`. Run it before pushing.

| Target | What it is |
| --- | --- |
| `make build` | `go build ./...` |
| `make test` / `make test-race` | unit tests; `-race` is what CI runs |
| `make lint` | `golangci-lint run` at the version pinned in `.golangci-lint-version` |
| `make fmt` | `gofmt` + `goimports` — the same formatters gopls applies on save |
| `make vet` | `go vet` for this OS **and** `GOOS=windows` / `GOOS=darwin` |
| `make tidy` | `go mod verify` and `go mod tidy -diff` |
| `make vuln` | `govulncheck ./...` |

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

| Symptom | Cause |
| --- | --- |
| `command not found: go` in a new terminal | PATH not picked up — restart the terminal, or check the install location |
| `gopls` not found, but it is installed | `$(go env GOPATH)/bin` missing from PATH (§2) |
| Golden-file tests fail only on Windows | line endings. Goldens normalise before comparing (§8.1) — the test is wrong, not your machine |
| Tests pass locally, fail in CI | CI runs `-race` natively on three OSes. Run `go test -race ./...` before pushing |
