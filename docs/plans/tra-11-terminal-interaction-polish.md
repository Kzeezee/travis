# Implementation plan — TRA-11 Phase 1 / 1D: Terminal Interaction Polish

Status: **implemented.** Sources: Linear TRA-11, `DESIGN.md` §5.9/§5.9.1/§6.3,
`Code-Conventions.md` (Frontend), `PLAN.md` Phase 1.

Goal: replace the blocking `bufio` terminal and `internal/app/chat` from 1A with one Bubble Tea v2
`tea.Model` (D-06) — prompt input, coalesced streaming, tool cards, status bar, and Esc/Ctrl-C
cancellation.

## 1. Design decisions

**Seam: a func value, not `core.Command`.** The UI must not import `agent/loop`, so it takes the
turn operation as a function:

```go
func RunChat(ctx context.Context, events <-chan core.Event,
    runTurn func(ctx context.Context, prompt string) error, opts Options) error
```

`cmd/travis` passes `assistantConversation.RunAssistantTurn`; tests pass a closure. Running a turn
stays an operation (ordered, returns an error). `core.Command` + `CommandSink` arrive with the
second command (tool approvals, Phase 2); the migration is swapping this parameter.
`internal/app/chat` is deleted — its read → run → render loop is what `Update` now does.

**Coalescer: batch inside the event-listener Cmd.** The Cmd blocks for the first event, then
collects more until the 33 ms window closes, a `TurnCompleted` arrives, or the channel closes, and
returns one `eventBatchMsg`. This matters because Bubble Tea v2 calls `View` after **every**
`Update` (`tea.go:888`), so the number of `Update`s has to drop, not just the number of flushes.
No idle ticking.

**Turn routing.** Submitting creates turn record `n` (so a fast `turnFinishedMsg{n, err}` always
has a record to land on). Streamed events go to the turn matching the count of
`UserMessageSubmitted` facts seen so far. The bus is FIFO, so a late chunk can never land in the
next turn, whichever message arrives first.

**Interrupted vs error.** The model sets `isCancelRequested` when it cancels; it does not inspect
the error (the openai adapter wraps context errors in `ProviderError{Transient}`).

**Screen.** Always altscreen, no option. Inline mode waits for OQ-12 and 1E config.

## 2. File changes

**Delete**
- `internal/app/chat/` (both files)
- `internal/frontends/terminal/terminal.go`, `terminal_test.go`, `deps.go`
- `.github/workflows/ci.yml` `build-tags-tools` job, and its entries in `ci.needs` and the result loop
- `Makefile` `go build -tags tools` line
- `.golangci.yml` `build-tags: [tools]` block and its comment
- `docs/DEV-SETUP.md` paragraph about `deps.go`

**Rewrite**
- `cmd/travis/main.go`: `runChat` calls
  `terminal.RunChat(ctx, events, assistantConversation.RunAssistantTurn, terminal.Options{ModelName, Input, Output})`.

**Add** (`internal/frontends/terminal/`)

| File | Owns |
|---|---|
| `terminal.go` | `RunChat`, `Options`, EOF-aware input, the listener Cmd, the root model (keys, turn lifecycle, per-turn `context.CancelFunc`) |
| `transcript.go` | Transcript stored as source (turns → user text, assistant text, tool cards, outcome); pure event → transcript mapping |
| `view.go` | lipgloss styles, transcript/tool-card rendering, status bar, layout |
| `terminal_test.go` | Listener batching + model behaviour |
| `transcript_test.go` | Event mapping and tool-card correlation |
| `golden_test.go` + `testdata/*.golden` | teatest frames |

## 3. Behaviour

Layout: transcript viewport, single-line `textinput` (Enter is not bound to newline, and it uses the
real terminal cursor, so frames stay deterministic), one-line status bar. The viewport stays at the bottom unless
the user has scrolled up (`AtBottom()` before `SetContent`, then `GotoBottom()`).

| Key | Idle | Turn running |
|---|---|---|
| Enter | Submit (blank ignored; `/quit` quits) | Ignored (typing still allowed) |
| Esc / Ctrl-C | Ctrl-C quits | Cancel turn (§6.3) |
| Ctrl-D | Quit if input is empty | Ignored |
| PgUp / PgDn / mouse wheel | Scroll | Scroll |

- **Only `pgup`/`pgdown` keys are routed to the viewport**; every other key goes to the input. The
  bubbles v2 viewport defaults bind `space f b u d j k h l ctrl+d`, which would scroll while typing.
- **Mouse wheel:** `View` sets `MouseMode = tea.MouseModeCellMotion`, and mouse messages go to the
  viewport, which already handles the wheel (3 lines per notch). Cost: selecting text natively
  needs Shift-drag (Option-drag in iTerm2/Terminal.app).
- **Status bar:** `model · idle|streaming|interrupted · last turn: ok|error`.
- **Turn contents render in arrival order:** assistant text, tool cards, more text.
- **Tool card:** one line: `⚙ name (running…)` → `✓ name` or `✗ name: <first line of content>`.
  Correlated by `ToolCall.ID` ↔ `ToolResult.CallID`; a result with no matching start renders its
  own card.
- **Turn outcome:** nothing when ok; `✗ error: <message>` or `⊘ interrupted` under the turn.
- **Narrow or tiny terminals:** lines truncate with `…`; heights are clamped so the viewport never
  gets a negative size.
- **Colour:** Bubble Tea v2 detects and downsamples the colour profile; no custom code.
- **Panics:** Bubble Tea v2 already recovers panics in `View`/`Update`/Cmds, restores the terminal,
  and prints the trace. Rule: never pass `tea.WithoutCatchPanics`.

### Mockup (plain text; colour only adds emphasis)

80 columns, streaming, with tool cards:

```
 you › what's in my notes folder?

 travis › Let me look.
   ✓ list_dir
   ✗ read_file: permission denied: ideas.md
 You have groceries.md, cs464.md and ideas.md (which I couldn't open).

 you › summarise cs464.md

 travis › Let me read it.
   ⚙ read_file (running…)

────────────────────────────────────────────────────────────────────────────────
 › and list the deadlines too
────────────────────────────────────────────────────────────────────────────────
 gpt-4o-mini · streaming · last turn: ok
```

80 columns, after Esc on one turn and a provider error on the next:

```
 you › write me a long essay on terminals

 travis › Terminals began as physical teletypes wired to
   ⊘ interrupted

 you › try again

 travis ›
   ✗ error: openai: 429 Too Many Requests: rate limit reached

────────────────────────────────────────────────────────────────────────────────
 › █
────────────────────────────────────────────────────────────────────────────────
 gpt-4o-mini · idle · last turn: error
```

50 columns (below the 60 threshold): text re-wraps, one-line items truncate:

```
 you › summarise cs464.md

 travis › The notes cover Bubble Tea v2, the
 event bus, and the ports-and-adapters rule.
   ✗ read_file: permission denied: /home/kez/…

──────────────────────────────────────────────────
 › █
──────────────────────────────────────────────────
 gpt-4o-mini · idle · last turn: ok
```

## 4. Tests

- `terminal_test`: listener batches within the window, flushes early on `TurnCompleted`, reports a
  closed channel. Model with a fake `runTurn` that blocks on its ctx: submit → finish, error shown
  on its turn, Esc/Ctrl-C cancel before the test deadline, idle Ctrl-C quits, `/quit`, Ctrl-D,
  Enter ignored mid-turn, typing does not scroll the transcript but PgUp and the wheel do.
- `transcript_test`: text chunks merge, events route to the latest turn, tool start/finish
  correlation, unmatched result.
- `golden_test` (teatest v2): widths **50 and 80**, fixed terminal size. The golden is the final
  frame's content with ANSI styling stripped, so it is readable and identical on every OS
  (`*.golden -text` is already in `.gitattributes`). Regenerate with `-update`.
- `cmd/travis/main_test`: the existing chat-wiring test keeps passing with empty stdin.
- The deleted `chat_test` coverage moves to `terminal_test` (UI flow) or already exists in
  `loop_test` (multi-turn history, provider failures).

## 5. Commits

1. `feat:` rebuild the terminal as a Bubble Tea model and wire `main`; delete `app/chat`, the old
   `terminal.go`, and `deps.go`
2. `chore:` drop the tools-tag build from CI, Makefile, and golangci; update DEV-SETUP

## 6. Caveats

- **Bubble Tea does not quit on input EOF** (a probe program hung). Wrap the input so EOF sends
  `inputEndedMsg`, **only when the input is not a TTY**; wrapping a real TTY would break raw mode.
- **SIGINT from outside** makes `Run` return `tea.ErrInterrupted`; treat it as a clean exit.
- **teatest v2 has no tagged release** — pinned by pseudo-version. Verified it builds against
  `bubbletea v2.0.9` without bumping it (CI runs `go mod tidy -diff`).
- **The 200 ms cancel budget** depends on the provider honouring ctx; openai uses
  `NewRequestWithContext`. The UI shows "interrupted" as soon as the key is pressed.
- **Tool cards stay unreachable in real use** until 1C publishes tool events; only tests drive
  them. `CallID` is the provider's tool-call ID, not 1C's event ID, so 1C does not block this.
- **Piped multi-prompt input stops working**: `printf 'a\nb\n' | travis chat` runs only the first
  prompt, because Enter is ignored mid-turn. Scripted input belongs to DESIGN's future `plain`
  renderer.

## 7. Follow-ups (not this ticket)

- **Loop history after cancel/error:** the user message stays in history with no reply, so the
  next request sends two user messages in a row (already true after provider errors). Needs a
  small `agent/loop` change; file as its own issue.
