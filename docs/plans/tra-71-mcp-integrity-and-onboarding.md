# Implementation plan: TRA-71 MCP install integrity and full §7.1 onboarding

Status: **phases 1–3 implemented; 4–5 in progress**, against `main` at `9c40e28` (after sessions resume/fork and
retention landed). Sources: Linear TRA-71, `DESIGN.md` §5.7, §7.1, §9.2, D-22,
D-27, D-32, D-33, `Code-Conventions.md`.

Goal: finish what TRA-70 left out. A registry MCP server cannot drift from what the user
approved, the security profile becomes a real user choice, and `travis init` covers §7.1.

## 0. Scope: what is cut, and why

The ticket lists DESIGN §5.7 and §7.1 item by item. Several items add machinery that nothing
uses today. They are cut here; each one comes back when its trigger happens.

| Item | Decision | Bring back when |
|---|---|---|
| Config `ref` / `pin` / `integrity` fields | **Cut.** `command`/`args` already carry the pinned ref, and `ResolvePackageRef` reads it back. Integrity goes in `mcp.lock`, not in the config file. | Never; the duplicate would only drift |
| Config `transport` | **Cut.** stdio is the only transport implemented | an HTTP transport lands |
| Config `sandbox` tier per server | **Cut.** There is only one tier, strict | a second MCP tier exists |
| Lazy start, `eager = true` | **Cut.** `BuildManager` already starts servers asynchronously and never blocks. A lazy start would also need cached tool schemas, because the registry needs tools before first use | someone runs enough servers that idle RAM matters |
| Curated/verified list, "inspect tools" before install | **Cut.** Inspecting tools means running the server before the user consents, and a curated list is data someone has to maintain | there is a maintainer for the list |
| `yolo` profile + `--i-understand` | **Cut, needs an RFC.** "No restrictions" contradicts D-27 (exec always asks) and D-22 (the deny-list is a policy primitive). `dev` is already the loose profile | an RFC amends D-22/D-27 |
| PyPI integrity enforcement | **Shown, not enforced.** `uvx` has no lock and no hash checking (verified with `uvx --help`, uv 0.12.13) | uv gains hash checking for `tool run` |

## 1. Design decisions

### 1.1 What "verify integrity" checks

A server's code comes from npx, which installs into `<scratch>/npm-cache/_npx/<hash>/`. That
directory holds npm's own `package-lock.json`, with an `integrity` for **every transitive
package** (checked against a real `~/.npm/_npx` lock).

| Option | Catches | Cost |
|---|---|---|
| A. Recheck the registry on start | the registry changing a pinned version's hash (npm forbids republishing, so this almost never happens) | needs the network on every start; weak |
| **B. Pin what npx resolved** | top-level hash ≠ the one approved at `add`; any transitive package drifting on a reinstall (cache wiped, new machine) | one file + tests; parses npm's `_npx` layout |
| C. travis-owned install (`npm ci` from a lock generated at add, code mounted read-only) | all of B, plus a running server rewriting its own code | changes the launch command, sandbox paths and trust matching; a PyPI equivalent is still unsolved |

**Choose B.** It meets §5.7's requirement ("`mcp.lock` records exactly what is installed,
re-checked on every start") with the smallest diff. It guards the supply chain. It does not
guard against tampering at runtime: the sandbox does that, and the server can write its own
scratch folder. Mark that in code:
`// ponytail: lock checks supply chain, not in-place tampering; upgrade path is option C`.

Data shape (`mcp.lock`, TOML, in the state dir beside `mcp-trust.toml`, which is user-scoped like
`[mcp.servers]`; the manager already receives that dir, so it needs no new parameter):

```toml
[server.github]
ref       = "npm:@modelcontextprotocol/server-github@0.6.2"
integrity = "sha512-…"                 # from the registry at `travis mcp add`
[server.github.resolved]               # recorded at first launch (trust on first use)
"node_modules/zod" = "sha512-…"
```

Invariants:
- A registry launch with no lock entry, or with a `ref` that differs from the current args, is
  refused, with the hint "re-run `travis mcp add <name> …`". Local commands need no entry.
- Before every launch, including restarts (`connect`): the resolved npx lock must match
  `resolved`, and its root package must match `integrity`. An empty `resolved` means first
  launch: launch, then record it.
- Writers are `mcp add`/`remove` (CLI) and first launch (chat). Each one does a
  read-modify-write under an exclusive file lock (D-32 rule 4). Reuse `openTrustFile` (the
  no-symlink open) and `lockExclusive` from `internal/tools/mcp`. Don't import broker's
  `openPrivateFileNoFollow`, which does the same thing: `tools/mcp` must not depend on the
  broker. Rename `openTrustFile` to `openPrivateFileNoFollow` once it has two callers.

Seam: one pure function plus file IO. No new interface.

```go
// internal/tools/mcp/lock.go
func compareResolved(locked lockedServer, resolved map[string]string) []string // mismatching package paths
func readResolvedNPXLock(scratchDir string, ref PackageRef) (map[string]string, bool, error)
```

### 1.2 Where the security profile lives

`Config.Profile` exists, but nothing reads it. `cmd/travis/main.go` pins `"default"` instead,
because the workspace layer can set it: the comment at :173, `buildMCPPolicy` at :314, and
`startSessionBroker` at :436.

| Option | |
|---|---|
| **A. Move it to `[security] profile`** | The workspace layer already refuses `[security]` with a hard error, so this reuses an existing guard. It sits beside `exec_allow`. The old top-level field has no readers, so deleting it costs nothing |
| B. Keep it top-level and remove it from `workspaceLayerCfg` | Adds a second user-only rule in another place |

**Choose A.** Unknown names are already rejected by `capabilities.BuildProfileSet`
(`ErrInvalid`) when the session starts, so no second list of valid profile names is needed.

### 1.3 Health signal

`/mcp` already shows the state and the reason, and the status bar already shows the broker's
failures. What's missing: (1) the reason does not include the server's last stderr line;
(2) a server that fails at first start publishes no event, so the status bar can't know.
Fix: put the last non-empty line of `stderr.log` into the reason, and publish the existing
`core.MCPServerUnavailable` from `connectAndRegister` on a start failure too. No new event type.

## 2. Phases and subtasks

Each phase merges on its own, in this order. Phase 1 builds on TRA-85. Phases 2 and 3 are
independent of phase 1 and can run in parallel with it.

### Phase 1: `mcp.lock` and `travis mcp verify` (§5.7, D-32)

- [x] 1.1 `lock.go`: `lockFile`/`lockedServer` types, load (missing file = empty), save under
      an exclusive lock, `compareResolved`, `readResolvedNPXLock` (find the `_npx/*` directory
      whose `package.json` depends on `ref.Name`)
- [x] 1.2 `mcp add`: after "y", write the entry (ref + registry integrity). Refuse an npm
      registry ref whose registry reported no integrity. PyPI records its sha256 and the card
      says "not enforced (uvx has no hash check)"
- [x] 1.3 `mcp remove`: delete the entry
- [x] 1.4 `buildLaunchCommand`: check before launch (no entry or ref mismatch → `ErrDenied`
      with a hint; resolved mismatch → `ErrDenied` naming the packages). `connectAndRegister`
      records `resolved` after the first successful handshake
- [x] 1.5 `travis mcp verify`: one line per server: `ok`, `not launched yet`, `mismatch: <pkgs>`,
      `unverified (pypi)`, `local`. Non-zero exit on any mismatch. Add it to `mcpUsage` and to
      the `unknown command` hint in `main.go`
- [x] 1.6 Tests (fakes, temp dirs): tampered transitive integrity refuses the launch;
      root ≠ add-time integrity refuses; first launch records; a missing entry refuses
      a registry launch and allows a local one. Red-team case (D-29): assert the denial fired
- [x] 1.7 Docs: §5.7 paragraph on what the lock does and doesn't guard, plus the user docs

**Proof:** install a real pinned npm server, chat once, edit one integrity in the npx lock,
`travis mcp verify` reports it, and the next chat refuses that server.
**Done:** `@modelcontextprotocol/server-memory@2.1.1` added, first `travis run` recorded 4
packages, a swapped `zod` hash made `mcp verify` exit 1 and the next launch refused before
asking for approval.
**Rollback:** revert the PR; `mcp.lock` is inert without the code.

### Phase 2: real security profile (§5.12.2, §7.1 step 5)

- [x] 2.1 Move `Profile` into `SecurityCfg` (`toml:"profile"`, default `"default"`); delete
      the top-level field and `workspaceLayerCfg.Profile`
- [x] 2.2 Replace the `"default"` pins in `main.go` (`buildMCPPolicy`, `startSessionBroker`)
      with `cfg.Security.Profile`; delete the two "Not cfg.Profile" comments. A `--resume`d or
      `--fork`ed session runs under the profile configured now, not the one it started with.
      The broker re-checks every effect, so nothing needs storing; say so in `--resume`'s help
- [x] 2.3 `sandbox.ReportAvailability() (Tier, error)` per OS (interface + all OS files
      together, D-25) for the security screen
- [x] 2.4 Setup security step becomes a choice (paranoid · default · dev) saved through a new
      `config.SaveSecurityProfile`; `describeSecurity` per profile, plus the sandbox line from 2.3
- [x] 2.5 Tests: a workspace `[security] profile` is still a hard error; `paranoid` from the
      user config reaches the broker (existing `enforced_test` covers grants); setup saves the choice

**Proof:** set `profile = "paranoid"`, start a chat, ask for a file write, and it is denied by
the broker.
**Done (partly):** with `profile = "paranoid"` the broker process runs `-profile paranoid` and an
`npx` MCP launch is denied despite `exec_allow`; a workspace `[security] profile` is a hard error.
The broker's own paranoid grants are covered by `enforced_test.go`.

### Phase 3: `tools.allow` / `tools.deny` per server (§5.7)

- [x] 3.1 `MCPServerCfg.Tools struct{ Allow, Deny []string }` (globs, deny wins, empty allow = all)
- [x] 3.2 Filter in `connectAndRegister` before `reg.Register`; `/mcp` shows "N tools (M hidden)"
- [x] 3.3 Test: a denied tool never reaches the registry

**Proof:** deny one tool of a real server; `/tools` doesn't list it.

### Phase 4: health signal (§5.7 lifecycle)

- [ ] 4.1 `reportUnavailable` and the start failure path append the last stderr line to the reason
- [ ] 4.2 Publish `MCPServerUnavailable` on a first-start failure
- [ ] 4.3 Status bar: red `● mcp <name>` while any server is unavailable; `/mcp` shows the line
- [ ] 4.4 Tests: a crashing fake server's stderr line appears in the status and the event

**Proof:** configure a server that exits with an error; the status bar shows the red dot and
`/mcp` shows the error line.

### Phase 5: onboarding completeness (§7.1)

- [ ] 5.1 Resume: save the last finished step in the state dir; `travis init` opens at the
      first unfinished step
- [ ] 5.2 Local model detection: probe Ollama (`127.0.0.1:11434/api/tags`) and llama.cpp
      (`127.0.0.1:8080/v1/models`) with a short timeout, and offer each as an
      OpenAI-compatible profile
- [ ] 5.3 Pre-fill `context_length` from Ollama `/api/show` or llama.cpp `/props` (D-33: config
      stays the source of truth)
- [ ] 5.4 Connection test on keypress only (it costs a model call): reachable · streaming · tool call
- [ ] ~~5.5~~ **Skipped:** `[tools] approval_default` is validated but nothing reads it, so a wizard
      step for it would change nothing. Wire it into policy first (separate ticket, §5.5/§6.4). Was: "tools" step: `approval_default` (supervised · collaborative · autonomous, §6.4),
      making it six steps
- [ ] 5.6 Tests: fake HTTP servers for detection and pre-fill; resume starts at the saved step

**Proof:** with Ollama running, a fresh `travis init` detects it, pre-fills the context length,
the test passes, and quitting at step 3 then re-running opens at step 3.

## 3. Open questions

- 1.4 refuses existing registry servers until they are re-added. That's acceptable for the
  current users (the team). Otherwise `mcp verify --pin` would record existing servers on
  first use.
- Should `travis sessions` show which profile each session ran under? Skipped: the session log
  doesn't record it, so add it when someone asks.
- Where the 5.2 probe lives: `cmd/travis` (wiring) or `providers/openaicompat`. Decide when
  writing it, by who owns the HTTP client.
