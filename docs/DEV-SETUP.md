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
throughout · `deps.yaml` and the CI workflows are YAML · Phase 0 adds a `Makefile`.

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
  "go.buildTags": "tools"
}
```

`go.buildTags: "tools"` matters — see §5. `-race` on tests matches what CI runs (§8.1).

**JetBrains GoLand** works too; it bundles its own analysis rather than using gopls. No
project config needed beyond pointing it at the Go SDK.

**Restart the editor** after installing Go, or it will not find `gopls`.

## 4. Verify the checkout

```
go build ./...
go build -tags tools ./...      # see §5 — different, and necessary
go test ./...
go vet ./...
go mod verify
```

All five should pass on a clean clone. If `go build` passes but `-tags tools` fails, the
dependency pins are broken — say so before doing anything else.

## 5. The `tools` build tag, and why it trips people up

`internal/cli/deps.go` carries `//go:build tools`. It exists only to pin the Charm v2
dependencies (D-06) until the real TUI imports them in Phase 1.

**Consequence:** `go build ./...` **skips that file**, so it never compiles the Charm
dependencies, and a broken pin would go unnoticed. Anything that needs to check the pins —
CI, or you before a release — must use `go build -tags tools ./...`.

Set `go.buildTags: "tools"` in your editor so gopls analyses the file too; otherwise it
shows the imports as unresolved.

## 6. Model provider, for Phase 1 onward

Not needed to build or test — `brain` and `ai` unit tests must never touch the network
(cassettes, D-16). You will want a local model for manual work:

```
brew install ollama          # or https://ollama.com/download
ollama pull qwen3:14b
```

Then point `provider.profiles.local.base_url` at `http://127.0.0.1:11434/v1`.

Use local models for day-to-day development; save a paid provider for quality
benchmarking near the end.

## 7. Common problems

| Symptom | Cause |
| --- | --- |
| `command not found: go` in a new terminal | PATH not picked up — restart the terminal, or check the install location |
| `gopls` not found, but it is installed | `$(go env GOPATH)/bin` missing from PATH (§2) |
| Editor shows Charm imports as unresolved | missing `go.buildTags: "tools"` (§5) |
| `go build` fine, `-tags tools` fails | a dependency pin is broken — do not paper over it |
| Golden-file tests fail only on Windows | line endings. Goldens normalise before comparing (§8.1) — the test is wrong, not your machine |
| Tests pass locally, fail in CI | CI runs `-race` natively on three OSes. Run `go test -race ./...` before pushing |
