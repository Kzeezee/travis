# Phase 4: OS sandbox

[Back to overview](overview.md)

**Input state.** MCP servers start from `internal/tools/mcp/supervise.go` via
`exec.CommandContext`, unsandboxed. `travis-broker` is hash-checked at launch
(`internal/security/broker/launch.go`). Per-OS code follows the build-tag pattern in
`internal/security/broker/transport_*.go`. OQ-16 (Linux without Landlock) and OQ-24
(Windows token vs AppContainer) are open.

Scope per D-14: the `strict` tier, applied to MCP servers, on all three OSes.

### 4-spike Sandbox feasibility (new)

| | |
|---|---|
| Changes | Throwaway branch. Run a real `npx` MCP server under Landlock, `sandbox-exec`, and a low-integrity restricted token. Record what breaks (npm cache writes are the expected failure). |
| Output | A short note in this folder closing OQ-16 and OQ-24, and fixing `allow_net` as a boolean for v1. |
| Depends on | Nothing. Informs 4B–4D. |

### 4A Interface and launcher (new)

| | |
|---|---|
| Changes | `internal/security/sandbox`: `BuildCommand(Spec) (*exec.Cmd, Tier, error)`. Stubs for every OS fail closed with `core.ErrUnsupported`. A hidden launcher mode in `travis-broker` applies the sandbox then execs the target, because Go cannot run code between fork and exec. One shared escape test. |
| Data | `Spec{Argv, Env, Dir, ReadPaths, WritePaths, AllowNetwork}`; `Tier` enum `none \| landlock \| seatbelt \| restricted-token`. |
| Proof | Escape test runs and reports `ErrUnsupported` on every OS. |
| Depends on | Nothing |

The escape test re-runs the test binary inside the sandbox and asserts four failures:
write outside scratch, read of a denied directory, network connect with
`AllowNetwork=false`, and survival after cancellation (the whole process tree must die).

### 4B Linux (new)

Landlock via `go-landlock`, applied by the launcher. Kernels without Landlock network
rules report a lower tier. **Proof:** escape test passes on the Ubuntu CI runner.
**Depends on:** 4A.

### 4C macOS (new)

`sandbox-exec -p` with a generated deny-by-default profile. **Proof:** escape test on
macOS CI; PR carries `ci:macos`. **Depends on:** 4A.

### 4D Windows (new)

Job object (kill-on-close, process and memory caps) plus a low-integrity restricted token
and a low-integrity scratch directory. No network containment; the tier string says so.
**Proof:** escape test on Windows CI with the network assertion skipped and the skip
reported. **Depends on:** 4A.

### 4E MCP inside the sandbox (new)

| | |
|---|---|
| Changes | `supervise.go` launches through `sandbox.BuildCommand`. `[security] require_sandbox` (user layer only) refuses to start a server below the required tier. Status bar shows the tier. Tier added to the `mcp-trust.toml` key, so a tier change re-prompts. |
| Proof | Fake MCP server works sandboxed on all three CI runners; with `require_sandbox` on an unsupported tier, startup reports the refusal. Manual: tier visible in `make chat`. |
| Rollback | `require_sandbox = false` restores unsandboxed launch for trusted servers. |
| Depends on | 3F, 4B, 4C, 4D |

## Phase 4 exit check

Escape test green natively on three OSes. Red-team suite re-run.
