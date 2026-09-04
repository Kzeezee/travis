# Presentation Layer

Status: Phase 0 architecture note.

`docs/DESIGN.md` is still normative. This page defines the frontend layers inside
`internal/cli` and what each frontend file is responsible for.

Doc index: [README.md](README.md). Tour section:
[7. Pattern: Presentation Layer](codebase_tour.md#7-pattern-presentation-layer).

Travis is a Bubble Tea CLI. The archived React notes in
[archive/frontend](archive/frontend) are source material only; use the ideas about
orchestration, components, and data translation, but use the Travis file names
below.

## 1. Frontend Layers

| Layer | Responsibility | Main Files | Not Responsible For |
| --- | --- | --- | --- |
| App / screen orchestration | Coordinates the UI flow: global UI state, focus, event routing, and child UI parts. | `app.go`, later `screens/*.go` | Provider calls, tool execution, policy decisions, broker IPC, MCP protocol handling. |
| UI ports and plumbing | Connects the UI to Travis through commands and events. | `commands.go`, `events.go` | Importing `internal/brain`, `internal/ai`, `internal/mcp`, or broker internals directly. |
| Presentation mapping | Converts Travis events and domain values into UI-shaped view models. | `presenters.go`, later `presentation/*.go` | Effects, backend mutation, provider retry, policy decisions. |
| UI components | Render view models and manage small local UI behavior. | `prompt_box.go`, `tool_card.go`, `status_bar.go`, `palette.go`, `approval_modal.go` | Business logic, tool execution, external protocols, filesystem effects. |
| Theme and layout helpers | Keep terminal styling, dimensions, and layout behavior consistent. | `theme.go`, `layout.go` | App workflow, backend state, business behavior. |

The important direction is:

| Direction | Meaning |
| --- | --- |
| User intent goes inward | UI sends commands through a command sink. |
| App facts come outward | UI receives events and turns them into view models. |
| Inner modules stay UI-free | Brain, provider, MCP, security, and broker code must not import `internal/cli`. |

## 2. Frontend Folder Architecture

This is the grouped shape the frontend can grow into. Use `screens/` for the
page-like layer, `components/` for reusable UI parts, and `presentation/` for the
hook-like data translation layer.

```text
internal/cli/
  app.go                    # root Bubble Tea model; wires screens, focus, events, commands
  focus.go                  # focus and modal-stack state; decides which UI part receives keys

  ports/
    commands.go             # command sink; sends user intent inward without importing brain
    events.go               # event subscription; receives Travis events as UI messages

  screens/                  # page-like layer; one file per major UI workflow
    chat.go                 # main chat screen orchestration
    trace.go                # trace viewer screen orchestration
    undo.go                 # undo/journal screen orchestration
    settings.go             # settings screen orchestration

  components/               # reusable UI parts; rendering plus small local UI behavior
    prompt_box.go           # prompt text, cursor state, submit key, inline validation
    tool_card.go            # tool call/result rendering and expanded/collapsed display state
    status_bar.go           # provider, sandbox tier, voice state, warnings
    palette.go              # slash command picker: filtering, selection, keyboard navigation
    approval_modal.go       # approval UI; captures allow/deny but does not decide policy

  presentation/             # hook-like layer; prepares data for components
    presenters.go           # converts events/domain values into UI view models
    tool_card_view.go       # view model for tool-card display
    transcript_view.go      # view model for chat transcript display

  style/
    theme.go                # terminal styles, colors, spacing, and theme variants
    layout.go               # dimensions, wrapping, pane sizing, reusable layout calculations

  testkit/
    fake_commands.go        # fake command sink for frontend tests
    event_fixtures.go       # synthetic events for screen/component tests
```

English version:

| Path | Layer | In Charge Of |
| --- | --- | --- |
| `app.go` | App / screen orchestration | The root UI module. It wires screens, command/event ports, focus, and top-level Bubble Tea update/view flow. |
| `focus.go` | App / screen orchestration | Cross-component coordination. It owns focus and modal-stack state so key handling is explicit. |
| `ports/` | UI ports and plumbing | UI seams for commands going inward and events coming outward. |
| `screens/` | App / screen orchestration | Page-like workflow orchestration. Screens compose components and presentation mappers. |
| `components/` | UI components | Reusable UI parts. Components render view models and own only small local UI behavior. |
| `presentation/` | Presentation mapping | Hook-like data preparation. It converts app events/domain values into view models. |
| `style/` | Theme and layout helpers | Terminal styling, layout calculations, wrapping, sizing, and theme variants. |
| `testkit/` | Test helpers | Fakes and fixtures for frontend tests. |

## 3. Split Later Only When Needed

In Go, every folder is also a package. Do not create all folders on day one if the
UI is still small. Start flat, then split when ownership becomes hard to see.

| Folder | Move Into It | When It Is Worth Creating |
| --- | --- | --- |
| `internal/cli/components/` | `prompt_box.go`, `tool_card.go`, `status_bar.go`, `palette.go`, `approval_modal.go` | There are enough UI components that the root folder is noisy. |
| `internal/cli/presentation/` | view models, presenter functions, transcript/tool-card display mapping | Presentation mapping has enough types or tests to need its own place. |
| `internal/cli/screens/` | `chat.go`, `undo.go`, `trace.go`, `settings.go` | The root `App` no longer clearly owns all screen-level orchestration. |
| `internal/cli/testkit/` | reusable fake command sinks, event fixtures, render helpers | Multiple frontend tests need the same helpers. |

Do not create a folder level just because the pattern has a name. Add a folder when
it makes ownership easier to see.
