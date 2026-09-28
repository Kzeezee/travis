#!/usr/bin/env bash
# Runs `travis chat` against a local llama-server. Start one first: scripts/llama-server.sh
set -euo pipefail

MODEL_PATH="${TRAVIS_MODEL_PATH:-$HOME/ai/Ling-3.0-tiny-Q4_K_M.gguf}"
HOST="${TRAVIS_LLAMA_HOST:-127.0.0.1}"
PORT="${TRAVIS_LLAMA_PORT:-8080}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_NAME="${TRAVIS_MODEL_NAME:-$(basename "${MODEL_PATH%.gguf}")}"
BASE_URL="http://${HOST}:${PORT}/v1"

if ! curl --silent --fail --max-time 2 "http://${HOST}:${PORT}/health" >/dev/null 2>&1; then
  echo "dev-chat: no llama-server on ${HOST}:${PORT} — start one with scripts/llama-server.sh" >&2
  exit 1
fi

cd "$REPO_ROOT"
exec go run ./cmd/travis chat -base-url "$BASE_URL" -model "$MODEL_NAME" "$@"
