#!/usr/bin/env bash
# Runs a local OpenAI-compatible llama-server in the foreground. Ctrl-C to stop.
set -euo pipefail

MODEL_PATH="${TRAVIS_MODEL_PATH:-$HOME/ai/Ling-3.0-tiny-Q4_K_M.gguf}"
LLAMA_BIN="${TRAVIS_LLAMA_BIN:-llama-server}"
HOST="${TRAVIS_LLAMA_HOST:-127.0.0.1}"
PORT="${TRAVIS_LLAMA_PORT:-8080}"
CONTEXT_SIZE="${TRAVIS_CONTEXT_SIZE:-65536}"
GPU_LAYERS="${TRAVIS_GPU_LAYERS:-999}"
FLASH_ATTENTION="${TRAVIS_FLASH_ATTENTION:-on}"

MODEL_NAME="$(basename "${MODEL_PATH%.gguf}")"

command -v "$LLAMA_BIN" >/dev/null || { echo "llama-server: not found: $LLAMA_BIN" >&2; exit 1; }
[ -f "$MODEL_PATH" ] || { echo "llama-server: no model at $MODEL_PATH" >&2; exit 1; }

# A GGUF whose architecture the binary lacks fails deep inside model loading, so name the
# mismatch up front. Some architectures only exist in vendor forks of llama.cpp.
report_model_architecture() {
  python3 - "$MODEL_PATH" <<'PARSE' 2>/dev/null || true
import struct, sys

with open(sys.argv[1], "rb") as model:
    if model.read(4) != b"GGUF":
        sys.exit(1)
    model.read(12)
    key_count, = struct.unpack("<Q", model.read(8))
    if key_count:
        length, = struct.unpack("<Q", model.read(8))
        key = model.read(length).decode("utf-8", "replace")
        value_type, = struct.unpack("<I", model.read(4))
        if key == "general.architecture" and value_type == 8:
            length, = struct.unpack("<Q", model.read(8))
            print(model.read(length).decode("utf-8", "replace"))
PARSE
}

check_architecture_support() {
  local architecture="$1" resolved
  resolved="$(command -v "$LLAMA_BIN")"

  local searched=("$resolved")
  while read -r library; do
    searched+=("$library")
  done < <(ldd "$resolved" 2>/dev/null | grep -o '/[^ ]*libllama[^ ]*' || true)

  # grep -q would exit early and SIGPIPE strings, which pipefail reports as failure.
  if strings -a "${searched[@]}" 2>/dev/null | grep -xF "$architecture" >/dev/null; then
    return 0
  fi

  cat >&2 <<EOF
llama-server: no loader for architecture '$architecture'
  model:  $MODEL_PATH
  binary: $resolved
  Some architectures only exist in a vendor fork until support is merged
  upstream. Point at that build, or at a model this build supports:
      TRAVIS_LLAMA_BIN=/path/to/fork/build/bin/llama-server $0
      TRAVIS_MODEL_PATH=~/ai/<supported>.gguf $0
EOF
  exit 1
}

if command -v python3 >/dev/null && command -v strings >/dev/null; then
  architecture="$(report_model_architecture)"
  [ -n "$architecture" ] && check_architecture_support "$architecture"
fi

echo "llama-server: $MODEL_NAME on http://${HOST}:${PORT}/v1 (ctx $CONTEXT_SIZE)"

exec "$LLAMA_BIN" \
  --model "$MODEL_PATH" \
  --alias "$MODEL_NAME" \
  --host "$HOST" \
  --port "$PORT" \
  --ctx-size "$CONTEXT_SIZE" \
  --gpu-layers "$GPU_LAYERS" \
  --flash-attn "$FLASH_ATTENTION" \
  "$@"
