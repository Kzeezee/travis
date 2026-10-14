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
- Functions and methods should normally use verb phrases that describe what they do
  or what answer they report, even when they only return a value
- Avoid function or method names that are only the returned type, receiver type, field
  name, or bare noun; prefer `ReportEventType()` over `EventType()` and
  `ReportMessageContentType()` over `contentType()`
- Use a light verb for value-producing helpers when construction, normalization,
  aggregation, selection, conversion, or reporting is part of the stable intent, such
  as `buildPathSetFromInputs`, `normalizeConfigPath`, `selectProvider`, or
  `reportEventType`
- For exported functions that compute values from process state, OS state, environment variables,
  multiple inputs, validation, normalization, or aggregation, prefer an explicit verb such as
  `Build`, `Normalize`, `Validate`, or `Resolve`; for example, prefer
  `config.BuildCurrentPathSet()` over `config.CurrentPathSet()`
- Use verb names for operations, side effects, validation, or required work, such as `ensureDir`,
  `startServer`, or `writeFile`
- Noun/result names are fine for types, constants, variables, fields, and packages,
  but avoid them for functions and methods unless they are standard Go convention
  methods; when naming a function or method, choose the verb phrase
- Keep standard Go convention method names when they are required or strongly
  expected by standard interfaces, such as `String()`, `Error()`, `Unwrap()`,
  `MarshalJSON()`, and `UnmarshalJSON()`
- Name boolean variables and fields as predicates that include the subject when context
  is not obvious, such as `isEventChannelOpen`, `hasPendingEvents`, `canWrite`, or
  `shouldRetry`; avoid bare adjectives or nouns like `open` when the value is a truth
  condition
- Avoid names tied to replaceable implementation details, such as a storage mechanism, OS source,
  transport, or parsing technology, unless that detail is the stable domain concept; prefer
  `buildPathSetFromInputs` over `pathsFromEnv` when the helper constructs and validates paths
  from more than environment lookup
- Name from the reader's point of view across the call site and definition; prefer stable intent
  over either hidden implementation mechanics or overly terse names

### Architecture
- Separate decisions from effects: decision code depends on ports, while adapters own real
  provider, filesystem, broker, MCP, terminal, or audio effects
- Dependency-inject ports and runtime choices, not implementation mechanics; inject
  dependencies when they vary by runtime environment, user/config choice, or test
  scenario, especially external effects and nondeterministic behavior that need fakes
  in tests
- Keep stable library setup, pure helpers, and deterministic implementation details
  inside their owning module; callers may choose output, level, provider, clock, or
  sink, but owning modules keep concrete setup such as `slog`, provider HTTP clients,
  OS path logic, or sandbox calls private
- Validate raw external data at entrypoints and adapter boundaries before passing typed values inward
- Keep dependency direction inward; inner decision modules must not import outward infrastructure
  modules
- Put shared cross-module DTOs and seams in `internal/core` only when they truly cross module
  boundaries
- Use events for facts that already happened; use operations for required work that needs ordering,
  errors, approval, or return values
- Test behavior where it is owned, using fakes across ports instead of real providers, filesystems,
  broker processes, terminals, or MCP servers

### Design Quality
- Evaluate design changes by whether they reduce cognitive load, change amplification, and
  unknown unknowns
- Prefer deep modules: small interfaces with useful behavior hidden behind them. Avoid
  shallow pass-through wrappers that only rename or delegate
- Keep design decisions in one owning module. If callers must know implementation details
  to use a module correctly, the interface is leaking
- Pull complexity downward when one module has the context to handle it. Do not push edge
  cases, ordering rules, or implementation choices onto every caller
- Split code only when it reduces real dependency or separates concepts that vary
  independently. Keep code together when pieces share knowledge or must always be used
  together
- Abstract conceptual duplication, not coincidental similarity
- For non-trivial seams or module interfaces, compare at least two designs before
  implementing and document the tradeoff briefly
- Comments should explain intent, invariants, contracts, or non-obvious design choices;
  do not comment mechanics already clear from code
- Before adding an error path, ask whether the operation can be redefined so the case is
  not an error
- Exposed behavior should be obvious from names, types, and comments

### Presentation Layer
- UI is an outer adapter: it renders app state, collects user intent, and sends commands inward
- Screens orchestrate flow, focus, commands, events, and child components
- Components render view models and own only small local UI behavior
- Presentation mappers translate core/domain values into UI-facing view models
- Frontend validation is for immediate feedback; backend entrypoints still validate
- UI must not import agent loop, provider, tool, broker, policy, or MCP internals
- See `docs/Code-Conventions.md` before changing terminal UI structure

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
- `AGENTS.md`
- `docs/Code-Conventions.md`
- `docs/DESIGN-overview.md`
- `docs/DESIGN.md`
- `docs/PLAN.md`
- `docs/DEV-SETUP.md`

## Hard rules
- Use exactly one Go module
- Do not create or use `pkg/`; all shared app code belongs under `internal/`
- Put shared cross-module DTOs and seams in `internal/core`
- Do not create the whole future folder tree; add only packages needed by the current PR
- Keep `runtime.GOOS` out of packages that do not own OS behavior; OS-owning packages are `internal/config`, `internal/security/...`, `internal/frontends/voicein`, `internal/frontends/voiceout`, and `internal/tools/mcp` (resolves `npx` vs `npx.cmd` for its own subprocess launches)
- Use `filepath` and `config.BuildCurrentPathSet()` instead of hard-coded OS paths, `~`, `$HOME`, or path separators
- Inner modules must not import outward modules
- Keep external effects behind ports and adapters
- Inject choices and ports, not internals; implementation details stay inside their owning module
- Validate data at entrypoints and trusted boundaries
- Events are facts that already happened; required work should be represented as operations

## Git
- Never mix a formatter pass over a whole file with content edits in the same commit.
  The reformatted lines conflict with everyone else's edits and real content gets lost in
  the resolution. Format in its own commit, or not at all
- Conventional commit prefixes: `feat:`, `fix:`, `docs:`, `style:`, `chore:`, `test:`
