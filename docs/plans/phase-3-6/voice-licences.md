# Voice dependency licences (D-34 item 3)

[Back to phase 5](phase-5.md)

Audit for 5B, checked 2026-10-20 against each project's own LICENSE file or README.

| Dependency | Used for | Licence | Ships in | Obligation |
|---|---|---|---|---|
| `github.com/gen2brain/malgo` v0.11.26 | Go bindings for miniaudio, in `cmd/travis-audio` | Unlicense (public domain) | `travis-audio` binary | None |
| miniaudio (vendored inside malgo) | Microphone capture on Linux, macOS and Windows | Public domain (Unlicense) or MIT No Attribution, at our choice | `travis-audio` binary | None |
| whisper.cpp | Local speech-to-text server (5C) | MIT | Nothing: the user installs and runs it | None for travis; the user's own install |
| OpenAI Whisper model weights | The model whisper.cpp loads (5C) | MIT (the openai/whisper README covers code and weights) | Nothing: downloaded by the user | None for travis |
| Silero VAD | Optional voice activity detection, if 5C uses it | MIT | Not yet used | Keep the MIT notice if bundled |

Result: nothing here blocks shipping `travis-audio`, commercial use included. The main `travis`
binary takes none of these dependencies; only the optional helper does.

Not cleared, so not to be bundled without a new audit: Fish Speech / OpenAudio weights
(non-commercial terms, as far as was checked) and Piper's maintained fork (GPL-3.0).
