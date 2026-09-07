# AGENTS.md
## Project Overview
A Bubbletea v2 (Go) CLI personal assistant app with modular feature sets, support for MCP servers for added custom functionality, and isolated and strong security execution focus.

## Code standards
- Focus on self-documenting code
- Comments are max a single line describing complex large sections or files
- `docs/Code-Conventions.md` is the dense checklist of patterns to follow (ports and
  adapters, events vs operations, error handling, testing, frontend layering); read
  it before writing non-trivial code

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

## Git
- Never mix a formatter pass over a whole file with content edits in the same commit.
  The reformatted lines conflict with everyone else's edits and real content gets lost in
  the resolution. Format in its own commit, or not at all
- Conventional commit prefixes: `feat:`, `fix:`, `docs:`, `style:`, `chore:`, `test:`
