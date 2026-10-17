# Phase 4 spike: sandbox feasibility (TRA-45)

[Back to Phase 4](phase-4.md)

Run 2026-10-17 on macOS (Darwin 25.3, Apple Silicon, Homebrew node 26.5 / uv), after 4B–4D
merged, so it measures the shipped backends rather than prototypes. The harness is
`cmd/sandboxspike` on this branch: it launches a real server through
`sandbox.BuildCommand` and completes `initialize` + `tools/list`. Linux and Windows rows
are **not run yet**; rerun the harness there before 4E closes.

## What ran (seatbelt)

Every sandboxed run had one writable path, a per-server scratch dir. `HOME`, `TMPDIR`,
`npm_config_cache`, `XDG_CACHE_HOME` and `UV_CACHE_DIR` pointed into it.

| Case | Read paths | Net | Result |
|---|---|---|---|
| `npx -y @modelcontextprotocol/server-everything`, unsandboxed | — | yes | 14 tools, 2.7 s (baseline) |
| same, sandboxed | none | no / yes | `execvp … npx: Operation not permitted` |
| same | `/opt/homebrew` | no, cold cache | npm `network` error, fails closed |
| same | `/opt/homebrew` | yes, cold cache | **14 tools, 2.2 s** |
| same | `/opt/homebrew` | no, warm cache | 14 tools, **70 s** (npm waits for the registry to time out) |
| same, pinned + `npx --offline` | `/opt/homebrew` | no, warm cache | **14 tools, 0.35 s** |
| same | `/opt/homebrew/bin` + `Cellar/node/26.5.0` | yes | `execvp` denied |
| `uvx mcp-server-time` | `/opt/homebrew` | yes, cold cache | 2 tools, 3.6 s |
| probe: read `~/.zshrc`, read workspace `go.mod`, write real `$HOME` | `/opt/homebrew` | yes | all `EPERM`; scratch write allowed |

No server process outlived the client.

## Findings

1. **npm cache writes were the expected failure. Redirecting them fixes it.** With the
   cache env vars pointing into scratch, neither npx nor uvx needed a write outside it.
   Nothing under the real `$HOME` had to be readable.
2. **The runner's whole install prefix must be readable.** A narrow node-only grant fails:
   Homebrew node links dylibs from `/opt/homebrew/opt/*`, and exec goes through the
   `/opt/homebrew/bin` symlink. Derivation rule for 4E: `LookPath(command)` → the parent
   of its directory (`/opt/homebrew/bin/npx` → `/opt/homebrew`; nvm's
   `~/.nvm/versions/node/vX/bin/npx` → `~/.nvm/versions/node/vX`). The prefix holds
   binaries, not user data, so read access to it is acceptable.
3. **The first launch of a registry server needs the network, because that launch is the
   install.** Once the cache is warm, a pinned ref with `--offline` runs with no network
   and starts faster than the cold run. That splits cleanly in two: install (needs net,
   human-approved) and run (net only if the server's own job needs it).
4. **Containment holds against the threats that matter for R-01.** The sandboxed server
   could not read dotfiles or the workspace, and could not write to the real home.

## Decisions

- **`allow_net` is a boolean for v1**, per server in `[mcp.servers.<name>]`, default
  `false`. Host-level allowlisting is not possible on any of the three backends (Seatbelt
  and Landlock only see IP sockets), so a host list would promise something nothing
  enforces.
- **The read set is the derived runner prefix plus the OS base set in each backend.** No
  per-runner profiles in Go (D-02).
- **The write set is only the per-server scratch dir**, `StateDir/mcp/<name>/`, with the
  cache env vars pointing into it.
- **OQ-16 closed.** Without Landlock the launcher reports `core.ErrUnsupported`. 4E's
  `require_sandbox = true` then refuses to start the server, and `false` runs it at tier
  `none` with a sticky status-bar warning. Landlock below ABI 9 runs as
  `landlock-no-net` and is shown, not refused (the 4B tier logic).
- **OQ-24 closed in 4D**: restricted token + job object, not AppContainer. The tier string
  `restricted-token` already says the network is not contained.

## Risks for 4E

- **Windows grants are ACL edits.** `applyToSelf` grants each `ReadPaths` entry
  low-integrity access, so the derived prefix (for example
  `C:\Program Files\nodejs`) gets its ACL changed persistently. Measure this on Windows
  before 4E derives a read set there. 4E may need to skip the derived prefix on Windows,
  where low integrity can already read it.
- **Not run on Linux.** The derived-prefix rule assumes `/usr`-installed runners are
  covered by `systemReadPaths`. nvm and `~/.local` installs are not, which the derivation
  rule is meant to fix.
- **`server-everything` is a toy.** Servers that need credentials (GitHub token via env)
  or a config file under `$HOME` will need `read_paths` or env. That is the escape hatch,
  not a default.
