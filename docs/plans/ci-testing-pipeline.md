# Implementation plan — GitHub Actions CI (testing)

Status: **Stage 1 implemented** (§7 deliverables are in the tree). Stages 2–3 ship with
their subjects; the §7 manual steps (§1 plan, `ci:macos` label, required `ci` check) are
still outstanding. Sources: `DESIGN.md` §8/§8.1/§9.4, `PLAN.md`
Phase 0 items 13–19, `Code-Conventions.md`.

## 1. Blocker to resolve first

Protected branches are free on **public** repos only; **private** repos need
Pro/Team/Enterprise. The repo is private, so on GitHub Free there are **no required
status checks** and every §8 gate is advisory — C1's *"if that lint is ever made
non-blocking, C1 is back"* comes true silently. Cheapest fix: **GitHub Pro via the
Student Developer Pack** (free, and raises 2,000 → 3,000 Actions minutes/month). The
workflow below is unaffected either way; only the final enforcement switch is.

Cost, since minutes are metered: Linux $0.006/min, Windows $0.010, **macOS $0.062
(~10×)**. A PR run costs ~8 min of allowance without macOS, ~38 with — ~350 vs ~100
runs/month. Hence §4.

## 2. Gate staging

§8 lists eleven blocking gates; five have anything to run against today (3 Go files).
Each remaining gate ships **in the same PR as the thing it checks** — a job that passes
vacuously is worse than an absent one (§9.4's "green while enforcement rots").

| Gate | Stage |
| --- | --- |
| `go vet` · `-race` tests on 3 OSes · `-tags tools` build · `govulncheck` · golangci-lint (bundles staticcheck) | **1** (this PR) |
| `make lint-deps` · `make lint-portability`, each with a negative test (`PLAN.md` exit 3–4) | 2, with the lints |
| `teatest` goldens · cassette replay (Phase 1) · red-team suite, 3-OS (Phase 2) | 3, as subjects land |
| release matrix, `CGO_ENABLED=0` | out of scope — separate `release.yml` |

## 3. Workflow

```yaml
on:
  pull_request: { branches: [main], types: [opened, synchronize, reopened, labeled] }
  push:         { branches: [main] }
  workflow_dispatch:
```

No `merge_group` (R4). Accepted consequence: two green PRs can still break `main`
together; `push: main` is the detector — same trade as §4.

| Job | Runs on | Command |
| --- | --- | --- |
| `test` | ubuntu + windows, `fail-fast: false` | `go test -race -timeout 5m -shuffle=on ./...` (ubuntu also `-coverprofile` → `go tool cover -func` into the step summary) |
| `test-macos` | macos-latest, conditional (§4) | same |
| `lint` | ubuntu | `golangci-lint run`, then `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...` |
| `build-tags-tools` | ubuntu | `go build -tags tools ./...` |
| `tidy-and-verify` | ubuntu | `go mod verify` · `go mod tidy -diff` |
| `vulncheck` | ubuntu | `govulncheck ./...` |
| `ci` | ubuntu | aggregator: asserts every `needs.*.result` |

Settings: `permissions: contents: read` · `concurrency` cancel-in-progress for PRs
only · `defaults.run.shell: bash` · `timeout-minutes` everywhere · `GOTOOLCHAIN: local`
· actions pinned to **full commit SHA** with Dependabot (a mutable third-party tag in
this project's pipeline is the bug class the project exists to prevent).

Non-obvious, each one an afternoon saved:

- **`windows-latest` has no GNU `make`** — CI calls `go` directly; `make` targets are
  ubuntu-only. **It does have gcc**, so `-race` (which needs cgo) works natively.
- **`build-tags-tools`**: plain `go build ./...` skips `internal/cli/deps.go`, so a
  broken Charm pin (D-06) is invisible. Dropped with that file in Phase 1.
- **Cross-`GOOS` vet**: once `sandbox_windows.go` exists, ubuntu-only analysis never
  compiles it. One minute on the cheap runner beats eight into the Windows job.
- **`ci` aggregator** exists so branch protection needs one check name; matrix edits
  otherwise rename required checks and weaken protection silently. It must assert
  `needs.*.result` explicitly — skipped ≠ failed.

## 4. macOS is opt-in

Runs on `workflow_dispatch`, on the **`ci:macos`** PR label, or on `push: main`:

```yaml
if: github.event_name != 'pull_request' ||
    contains(github.event.pull_request.labels.*.name, 'ci:macos')
```

- `labeled` **must** be in `types:` (§3) or applying the label does nothing until the
  next push.
- The aggregator must accept `skipped` **for this job only**: uniform `success` makes
  every unlabelled PR red; uniform `!= failure` lets a skipped required job pass.
- Apply the label for PRs touching build tags, `config` paths, `security`, voice, or
  goldens. Review rule, in DEV-SETUP.

**Accepted cost:** an unlabelled PR can merge never having run on macOS. That weakens
G8's three-OS rule for money. **Revisit by Phase 4 (voice)**, where the honest answer
is probably to make it unconditional.

## 5. `.golangci.yml`

Value here is mechanical enforcement of rules currently only in prose. `default:
standard` (errcheck, govet, ineffassign, staticcheck, unused) plus:

| Linter | Enforces |
| --- | --- |
| `forbidigo` | `time.Now()` outside `core` · `runtime.GOOS` · `os.Getenv("HOME")` · stray `fmt.Print*` (§5.1, §8.1) |
| `depguard` | Charm only in `frontends/terminal`; no `net/http`/`os/exec` in loop and policy packages |
| `errorlint` | `%w` and `errors.Is/As` — §6.2's taxonomy needs wrapping to actually happen |
| `exhaustive` | switches over `allow/ask/deny`, `StopReason` — "exactly one of three" is only true if the switch says so |
| `bodyclose`, `noctx` | adapter leaks; every call carries `ctx` or §6.3's 200 ms budget is unreachable |
| `nolintlint` | `//nolint` must be specific and explained, or the config decays into decoration |
| `gosec` | it is a security product |

Formatters: **`gofmt` + `goimports` only** — `gofumpt` is rejected because DEV-SETUP's
format-on-save uses plain `gofmt` via gopls and would fight CI on every save. This
replaces a standalone `gofmt` job. Also: `max-issues-per-linter: 0` (never truncate),
`exclusions.warn-unused: true`, `run.build-tags: [tools]`.

Rejected: `wrapcheck` (fires on every cross-package error), `revive`/`lll`/`funlen`
(style arguments, not bugs), `only-new-issues` (would permanently hide today's
violations in a three-file repo).

Pin **v2.13.2** in `.golangci-lint-version`, read by both the action (`version-file:`)
and the Makefile — unpinned `latest` turns unrelated PRs red on a Tuesday.

⚠️ **`depguard` is not `deps.yaml`.** It matches import paths against globs; it cannot
express the package graph or acyclicity. Stage 2 is still required. Same for
`forbidigo` vs `lint-portability` (identifiers, not path-separator literals).

## 6. Decisions

| # | Answer |
| --- | --- |
| R1 | Private repo; macOS opt-in (§4); plan constraint in §1 |
| R2 | **`Code-Conventions.md` wins on layout** — no `pkg/`; `agent/loop`, `tools/builtin`, `providers/*`, `frontends/terminal`. §8's *gate list* is unaffected. CI is layout-agnostic (`./...`), so nothing in Stage 1 blocks; `.golangci.yml` paths and Stage 2's `deps.yaml` use the conventions layout. Add a one-line pointer in `DESIGN.md` §8 (naming supersession, not a `D-nn` reversal — docs edit, not an RFC) |
| R3 | PRs targeting `main` only, plus `push: main` and `workflow_dispatch` |
| R4 | No merge queue |
| R5 | `.golangci.yml` in Stage 1 (§5) |
| R6 | `CODEOWNERS` **yes, but with branch protection** — it is inert without "require review from Code Owners" (§1). Scope to the enforcement layer only: `.github/**`, `Makefile`, `.golangci.yml`, `deps.yaml`, `docs/DESIGN.md`, `docs/Code-Conventions.md` |

## 7. Deliverables (Stage 1)

```
.github/workflows/ci.yml · .github/dependabot.yml · .golangci.yml
.golangci-lint-version   · Makefile (test, test-race, lint, fmt, tidy, vuln)
docs/DEV-SETUP.md  edit — ci:macos label; shuffle-seed and no-make-on-Windows notes
docs/DESIGN.md     edit — §8 naming pointer (R2)
```

Manual, not in version control: resolve §1 · create the `ci:macos` label · require the
single `ci` check on `main`.

**Acceptance — prove the machinery, don't assume it:** a broken test turns `ci` red · an
unformatted file turns `lint` red · an unlabelled PR shows `test-macos` skipped with
`ci` still green · a labelled PR runs it.

Commits: `chore: add Makefile` → `chore: pin and configure golangci-lint v2` →
`feat: add per-PR CI workflow on a native three-OS matrix` → `docs: …`.
