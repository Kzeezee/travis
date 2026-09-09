# AGENTS.md
## Project Overview
A Bubbletea v2 (Go) CLI personal assistant app with modular feature sets, support for MCP servers for added custom functionality, and isolated and strong security execution focus.

## Code standards
### Readability
- Focus on self-documenting code

### Comments
- Comments are max a single line describing complex large sections or files
- `docs/Code-Conventions.md` is the dense checklist of patterns to follow (ports and
  adapters, events vs operations, error handling, testing, frontend layering); read
  it before writing non-trivial code

### Naming
- Prefer names that explain role and intent at both the call site and the definition or
  initialization site, with signatures and types carrying the precise details
- Prefer noun/result names for pure value-producing helpers, such as `configFromFile` or
  `commandFromArgs`, when the returned value is the main idea
- Use a light verb for value-producing helpers when it makes the definition easier to understand or
  when construction, normalization, or aggregation is part of the stable intent, such as
  `buildPathSetFromInputs`
- For exported functions that compute values from process state, OS state, environment variables,
  multiple inputs, validation, normalization, or aggregation, prefer an explicit verb such as
  `Build`, `Normalize`, `Validate`, or `Resolve`; for example, prefer
  `config.BuildCurrentPathSet()` over `config.CurrentPathSet()`
- Use verb names for operations, side effects, validation, or required work, such as `ensureDir`,
  `startServer`, or `writeFile`
- Avoid names tied to replaceable implementation details, such as a storage mechanism, OS source,
  transport, or parsing technology, unless that detail is the stable domain concept; prefer
  `buildPathSetFromInputs` over `pathsFromEnv` when the helper constructs and validates paths
  from more than environment lookup
- Name from the reader's point of view across the call site and definition; prefer stable intent
  over either hidden implementation mechanics or overly terse names

### Architecture
- Separate decisions from effects: decision code depends on ports, while adapters own real
  provider, filesystem, broker, MCP, terminal, or audio effects
- Validate raw external data at entrypoints and adapter boundaries before passing typed values inward
- Keep dependency direction inward; inner decision modules must not import outward infrastructure
  modules
- Put shared cross-module DTOs and seams in `internal/core` only when they truly cross module
  boundaries
- Use events for facts that already happened; use operations for required work that needs ordering,
  errors, approval, or return values
- Test behavior where it is owned, using fakes across ports instead of real providers, filesystems,
  broker processes, terminals, or MCP servers
- See `docs/code_design/codebase_tour.md` for the full codebase map and examples

### Presentation Layer
- UI is an outer adapter: it renders app state, collects user intent, and sends commands inward
- Screens orchestrate flow, focus, commands, events, and child components
- Components render view models and own only small local UI behavior
- Presentation mappers translate core/domain values into UI-facing view models
- Frontend validation is for immediate feedback; backend entrypoints still validate
- UI must not import agent loop, provider, tool, broker, policy, or MCP internals
- See `docs/code_design/presentation_layer.md` before changing terminal UI structure

## Where the design lives
- `docs/DESIGN.md` is normative. §0.5 is the decision register (D-01..D-34), §0.6 explains
  D-01..D-10 in plain words, §12 is open questions, §13 is risks
- `docs/DESIGN-overview.md` is the short version; §13 covers concepts that are easy to
  invert on first read
- `docs/PLAN.md` is build order and parallelism only — it holds no decisions
- `docs/DEV-SETUP.md` is clone-to-working-editor
- Do not relitigate a `D-nn` decision in a PR; reversing one needs an RFC
- Do not read or reference anything under `docs/code_design/` unless explicitly
  asked to; it is verbose background material, not day-to-day guidance

## Read first
- `docs/PLAN.md`
- `docs/code_design/codebase_tour.md`
- `docs/code_design/design_rationales.md`
- `docs/code_design/implementation_example/ports_and_adapters.md`
- `docs/code_design/implementation_example/error_handling.md`
- `docs/code_design/presentation_layer.md`
- `AGENTS.md`

## Hard rules
- Use exactly one Go module
- Do not create or use `pkg/`; all shared app code belongs under `internal/`
- Put shared cross-module DTOs and seams in `internal/core`
- Do not create the whole future folder tree; add only packages needed by the current PR
- Keep `runtime.GOOS` out of packages that do not own OS behavior; OS-owning packages are `internal/config`, `internal/security/...`, `internal/frontends/voicein`, and `internal/frontends/voiceout`
- Use `filepath` and `config.BuildCurrentPathSet()` instead of hard-coded OS paths, `~`, `$HOME`, or path separators
- Inner modules must not import outward modules
- Keep external effects behind ports and adapters
- Validate data at entrypoints and trusted boundaries
- Events are facts that already happened; required work should be represented as operations

## Git
- Never mix a formatter pass over a whole file with content edits in the same commit.
  The reformatted lines conflict with everyone else's edits and real content gets lost in
  the resolution. Format in its own commit, or not at all
- Conventional commit prefixes: `feat:`, `fix:`, `docs:`, `style:`, `chore:`, `test:`
