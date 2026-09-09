# Phase 0 Foundation And Rules Spec

## Problem Statement

Travis is still early in Phase 0. The repository had the first working `travis version`
command and the Bubble Tea dependency pins, but the codebase did not yet have the
small shared foundation that Phase 1 work needs. At the same time, the active docs
and assistant instructions needed to converge on the current architecture decisions:
one Go module, no `pkg/` package tree, shared app seams under `internal/core`, and
only creating packages that the current PR needs.

The risk being addressed is not user-facing behavior yet. The risk is that parallel
Phase 1 work starts from inconsistent assumptions: one person builds against `pkg`,
another creates the whole future skeleton, another branches on OS details in business
logic, and another writes direct filesystem/provider effects without a port. Those
errors are cheap to prevent in Phase 0 and expensive to unwind after provider,
tool, terminal, and security work begin.

This change set is therefore a foundation and alignment slice. It creates the
minimum app-internal vocabulary needed by the next PRs, moves platform reporting
behind the module that owns OS facts, and restores the project instructions that
future agents should read before touching the codebase.

## Solution

The solution is to add a narrow Phase 0B foundation while keeping the package tree
deliberately small:

1. Add `internal/core` as the shared app seam package for provider-neutral messages,
   tool DTOs, event DTOs, capability DTOs, generic Travis error kinds, provider
   error wrapping, IDs, and clocks.
2. Add `internal/config` as the owner of per-OS path behavior and platform reporting.
   Other modules should call config-owned helpers rather than reading OS details
   directly.
3. Add `internal/log` as a small logging facade over Go's structured logging so
   modules depend on the project's logging seam rather than each concrete logger
   choice.
4. Update the `travis version` command to use the config-owned platform helper
   instead of directly assembling `runtime.GOOS` and `runtime.GOARCH` in the command
   package.
5. Sync active docs with `feat/phase-0-frozen-contracts` so the current branch is
   aligned with the agreed Phase 0 documentation baseline.
6. Re-add `AGENTS.md` guidance that was not present on that branch but is required
   for this workstream: what to read first, and the hard rules agents should follow.

The end result is intentionally not a user feature. The user-visible CLI still only
has the version command. The useful behavior is for developers and agents: Phase 1
work can import stable-enough shared DTOs, path logic has one owner, and the written
rules point contributors away from stale `pkg/` and broad skeleton work.

## User Stories

1. As a Travis maintainer, I want the branch to use one Go module, so that package
   boundaries are enforced by imports and tooling rather than multiple module files.
2. As a Travis maintainer, I want no `pkg/` package tree, so that the repository does
   not accidentally promise public Go interfaces before there are external importers.
3. As a Travis maintainer, I want shared app seams under `internal/core`, so that
   provider, agent, tool, terminal, state, and eval work can use the same vocabulary
   without creating sideways dependencies.
4. As a Phase 1 provider implementer, I want a minimal provider request and stream
   vocabulary, so that a provider adapter can start without inventing a private
   request shape that later has to be migrated.
5. As a Phase 1 agent-loop implementer, I want provider-neutral message and chunk
   types, so that the turn loop can be tested against fake providers and later run
   against real adapters.
6. As a Phase 1 tool implementer, I want a shared tool spec, tool call, and tool
   result shape, so that built-in tools and future MCP tools can be registered and
   invoked through the same conceptual seam.
7. As a terminal frontend implementer, I want event DTOs for user messages, assistant
   streaming text, tool-call lifecycle, and turn completion, so that the terminal UI
   can render observations without importing agent-loop internals.
8. As a state or eval implementer, I want event facts to be typed, so that traces and
   sessions can observe the same facts the terminal observes.
9. As a security implementer, I want capability DTOs to exist without capability
   matching logic in `core`, so that future policy code owns matching while shared
   modules can still talk about required authority.
10. As a provider adapter implementer, I want external provider failures translated
   into Travis error kinds, so that inner code can reason about invalid, unsupported,
   transient, and exhausted outcomes without depending on provider-specific errors.
11. As a test author, I want a fixed clock, so that time-dependent code can be tested
   deterministically without real wall-clock time.
12. As a production caller, I want a system clock, so that modules can depend on a
   clock seam without each one calling wall-clock time directly.
13. As a config implementer, I want config paths to be resolved per OS in one module,
   so that business logic does not branch on Linux, macOS, or Windows.
14. As a Windows user, I want config path handling to avoid Unix-only assumptions, so
   that Travis can run on Windows without hard-coded home or slash behavior.
15. As a macOS user, I want config path handling to use platform-appropriate defaults,
   so that Travis stores user files where macOS users and backup tools expect them.
16. As a Linux user, I want config path handling to respect XDG-style roots when
   present, so that Travis follows the Linux convention without other modules knowing
   about it.
17. As a developer using a portable config file, I want logical prefixes for config,
   state, cache, and workspace paths, so that config values can be resolved through
   the config module instead of embedding OS paths everywhere.
18. As a developer setting `TRAVIS_HOME`, I want all major Travis directories to be
   derived from that root, so that a test or custom installation can keep Travis data
   in one controlled location.
19. As a developer setting environment-based paths, I want relative roots rejected,
   so that a typo does not silently create process-working-directory-dependent
   config, state, or cache paths.
20. As a CLI user, I want `travis version` to keep reporting the platform, so that the
   command remains useful while respecting the rule that OS facts belong to config.
21. As a module author, I want a logging facade, so that module interfaces are not
   coupled directly to a particular logging implementation.
22. As a test author, I want a no-op logger, so that tests can satisfy logging
   dependencies without writing output.
23. As an agent working in this repository, I want `AGENTS.md` to list the first docs
   to read, so that I do not skip the active architecture context.
24. As an agent working in this repository, I want `AGENTS.md` to spell out hard
   rules, so that I do not accidentally relitigate settled decisions or create stale
   structure.
25. As a reviewer, I want the active docs and branch docs synced, so that review
   discussion is about this PR's actual foundation work rather than stale planning
   text.
26. As a reviewer, I want broad formatting avoided, so that the content changes are
   easy to inspect and do not obscure unrelated edits.
27. As a future contributor, I want the current work to avoid creating all future
   packages, so that the repository grows only where there is an immediate module
   owner and testable behavior.
28. As a future contributor, I want the current interfaces to stay small, so that
   Phase 1 work is not trapped by a large speculative contract.
29. As a maintainer, I want tests only where there is behavior, so that the foundation
   tests catch real mistakes without asserting empty interfaces.

## Implementation Decisions

- The branch uses `feat/phase-0-frozen-contracts` as the documentation baseline for
  active docs and companion guidance, then intentionally layers the new `AGENTS.md`
  hard-rule block on top.
- The foundation is scoped to Phase 0B and Phase 0D, while Phase 0C remains out of
  scope. That means no CI, lint, import-graph, or portability-check implementation is
  included here.
- Only three new packages are introduced: core, config, and log. The future package
  tree is not created.
- Shared cross-module DTOs and seams are placed in core because multiple future
  modules will need them and no single later module owns the vocabulary.
- The provider seam is intentionally narrow. It has one model-call operation that
  returns a stream, plus provider-neutral request, chunk, usage, tool-call-fragment,
  and stop-reason DTOs.
- The provider seam does not yet include every method from the long-form design.
  Counting, budgeting, embeddings, and capability reporting are deferred until a
  concrete caller needs them.
- Messages and parts are provider-neutral. They express roles, text, tool calls, and
  tool results without committing to one vendor's wire protocol.
- Tool DTOs are shared because the provider, agent loop, tool registry, built-in
  tools, MCP adapter, frontend, trace, and session modules will all need to talk
  about tool specs, tool calls, and tool results.
- Capability DTOs are shared, but capability input validation, matching, and policy
  evaluation are not implemented in core. Future security modules should own matching
  behavior.
- Event DTOs represent facts that already happened. They include user message
  submission, assistant text streaming, tool call start, tool call finish, and turn
  completion.
- The event sink is a small publishing seam. It is not a full bus implementation yet,
  because the current PR does not need async queue behavior, subscriber filters, or
  drop semantics.
- The clock seam includes a system clock for production and a fixed clock for tests.
  This creates the smallest useful time seam without adding ID generation or a broader
  scheduler.
- Generic Travis error kinds are small and reusable: invalid, unsupported, transient,
  and exhausted.
- Provider errors wrap a generic kind, a provider name, and an original cause. This
  lets callers use generic error matching while preserving debugging context.
- The config module owns platform reporting. The command package no longer constructs
  the OS/architecture string directly.
- The config module owns path resolution. It exposes a path set for config, state,
  cache, system config, workspace, and workspace config.
- `TRAVIS_HOME` overrides the normal platform directories and derives config, state,
  cache, and config-file paths from one absolute root.
- Platform-specific path defaults are split behind build-tagged files for Linux,
  macOS, Windows, and unknown platforms.
- Unknown platforms get a conservative fallback that still compiles. This is not a
  complete product stance for unknown platforms; it keeps the package buildable and
  localizes future platform decisions.
- Path resolution accepts logical prefixes for config, state, cache, and workspace.
  Unknown prefixes are rejected as invalid input rather than guessed.
- Relative paths resolve under the workspace directory. Absolute paths are cleaned and
  returned as absolute paths.
- Config path parsing normalizes slash styles before using the standard path APIs, so
  config text can be written in a portable form.
- The logging facade is small: debug, info, warn, and error with structured
  attributes.
- The logging implementation wraps the standard structured logger, but callers depend
  on the project facade.
- `AGENTS.md` now lists the read-first documents, including the build plan, codebase
  tour, design rationales, ports/adapters example, error-handling example, and
  `AGENTS.md` itself.
- `AGENTS.md` now spells out the hard rules: one Go module, no `pkg/`, shared seams
  in core, no broad future tree, OS behavior only in owning modules, use config and
  filepath for paths, no outward imports from inner modules, ports/adapters for
  effects, validation at entrypoints and trusted boundaries, event facts versus
  required operations, no broad formatting mixed with content edits, and conventional
  commits.

## Testing Decisions

- Interface-only declarations do not get tests. Tests were added where the new
  foundation has behavior.
- Provider error behavior is tested through generic error matching, cause unwrapping,
  and stable error text.
- Fixed clock behavior is tested by asserting it returns the configured instant.
- Config path behavior is tested through `TRAVIS_HOME`, workspace config derivation,
  logical prefix resolution, separator normalization, relative workspace resolution,
  and rejection of unknown prefixes.
- Relative `TRAVIS_HOME` is rejected and tested as invalid input.
- The logging facade is tested by writing through a structured logger and asserting
  the message and attribute reach the output.
- The existing `travis version` test is updated to assert platform output through the
  config-owned helper.
- The full local test suite passed after the implementation.
- The config package was also compile-checked for Linux and Windows so build-tagged
  path files were not only tested on the current host OS.
- Phase 0C checks, including import lints, portability lints, native CI, race tests,
  and vet/mod verification gates, are explicitly left for the owner of that track.

## Out Of Scope

- No CI or lint implementation.
- No import allowlist implementation.
- No full package skeleton.
- No broker wire protocol implementation.
- No real event bus implementation.
- No tool registry implementation.
- No built-in tool implementation.
- No model provider adapter.
- No agent loop.
- No Bubble Tea terminal frontend.
- No config file parser, TOML precedence, or full config validation.
- No capability matcher, policy engine, broker process, sandbox, approval flow, or
  undo journal.
- No public Go API or compatibility policy.
## Further Notes

The most important design move is that `internal/core` is private shared app
vocabulary, not a public library surface. That keeps Travis free to refactor while
the application shape is still being discovered. The current DTOs are useful enough
to unblock the next Phase 1 seams, but they intentionally avoid freezing the larger
interfaces described in older planning material.

The second important design move is that `internal/config` owns OS facts. The version
command is tiny, but moving platform formatting there establishes the rule early:
packages that do not own OS behavior should not branch on OS behavior. Path
resolution follows the same rule by giving callers a portable path vocabulary.

The third important design move is procedural. `docs/` was restored to match
`feat/phase-0-frozen-contracts`, then `AGENTS.md` was updated with the rules that
agents need for this branch. That means the docs are aligned to the branch baseline,
while the agent guidance includes the extra guardrails requested in this session.

The current active `pkg/` reference is intentional: it appears only as a negative rule
in `AGENTS.md`. The broader word `pkg` should not appear in active docs except in
unrelated external references.
