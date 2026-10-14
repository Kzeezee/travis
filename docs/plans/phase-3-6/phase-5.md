# Phase 5: voice

[Back to overview](overview.md)

**Input state.** No voice code. `core.CapabilityAudioCapture` and `CapabilityAudioPlay`
exist but are unused. The bus carries `AssistantTextStreamed` and `TurnCompleted`, already
redacted. Approvals and `ask_user` travel on channels from `terminal.BuildApprover` and
`terminal.BuildQuestioner`, not the bus. The terminal handles key presses only, not
releases. D-15: voice in and out, `malgo` in a `cmd/travis-audio` helper, local whisper
server for speech-to-text. D-10: approvals are keyboard-only.

### 5A Voice output (new)

| | |
|---|---|
| Changes | Pure streaming `<speak>` parser publishing a speech event; untagged text stays silent. Prompt instruction in `internal/agent/loop/prompt.go`. `internal/frontends/voiceout`: `Speaker` port, adapters for `say`, `espeak` and SAPI, a null player. Wrap the approver and questioner to speak "approval needed"; the answer stays on the keyboard. `[voice.output]` config, off by default. |
| Data | `SpeechRequested{Text}` event; `Speaker.Speak(ctx, text) error`. |
| Boundary rule | Model text reaches TTS on stdin only, never as arguments or script text. |
| Proof | Parser tests with tags split across chunks; adapter test asserts argv carries no model text; null-player test for alerts. Manual: one spoken update per OS. |
| Rollback | Config off. |
| Depends on | D-0 (lint path) |

### 5B Audio helper (new)

| | |
|---|---|
| Changes | D-34(3) licence audit (miniaudio, whisper.cpp, model weights), recorded in this folder. `cmd/travis-audio` with `//go:build cgo` and stubs so cross-OS vet stays green. Pipe protocol to travis. Capture interface in `internal/frontends/voicein`. |
| Proof | `make check` on all three OSes; helper records two seconds of silence from a device on one dev machine per OS. |
| Depends on | D-0 |

### 5C Voice input (new)

| | |
|---|---|
| Changes | HTTP adapter for an OpenAI-style `/audio/transcriptions` endpoint, localhost by default. Push-to-talk: hold where the terminal reports key releases, toggle elsewhere. Mic mutes while any modal is open. Transcript fills the input box and is never sent automatically. Per-OS microphone denial handled without blocking startup. |
| Proof | Adapter tests against a fake server; terminal test: an open approval modal ignores voice input. Manual: dictate a prompt on each OS. |
| Depends on | 5B |

## Phase 5 exit check

Voice off by default; with it on, one full turn (speak, dictate, spoken reply) on each OS.
