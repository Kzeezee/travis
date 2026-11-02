# Voice dependency licences (D-34 item 3)

[Back to phase 5](phase-5.md)

Audit for 5B, checked 2026-10-20 against each project's own LICENSE file or README. The
speech-to-text rows were redone on 2026-11-02 when Whistle replaced whisper.cpp.

| Dependency | Used for | Licence | Ships in | Obligation |
|---|---|---|---|---|
| `github.com/gen2brain/malgo` v0.11.26 | Go bindings for miniaudio, in `cmd/travis-audio` | Unlicense (public domain) | `travis-audio` binary | None |
| miniaudio (vendored inside malgo) | Microphone capture on Linux, macOS and Windows | Public domain (Unlicense) or MIT No Attribution, at our choice | `travis-audio` binary | None |
| `needle` engine (Cactus-Compute/needle3, revision `d6c7d6a`) | Runs the speech-to-text model, one process per recording | Apache-2.0 | Nothing: `make voice` downloads it, hash-pinned | Keep the licence and NOTICE if a release ever bundles it |
| Whistle model weights (Cactus-Compute/whistle, revision `7411186`) | The speech-to-text model `needle` loads | Apache-2.0 (model card and LICENSE) | Nothing: `make voice` downloads it, hash-pinned | Keep the licence and NOTICE if a release ever bundles it |
| Silero VAD | Optional voice activity detection, if 5C uses it | MIT | Not yet used | Keep the MIT notice if bundled |

Result: nothing here blocks shipping `travis-audio`, commercial use included. The main `travis`
binary takes none of these dependencies; only the optional helper does.

Not cleared, so not to be bundled without a new audit: Fish Speech / OpenAudio weights
(non-commercial terms, as far as was checked) and Piper's maintained fork (GPL-3.0).
