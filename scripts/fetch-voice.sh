#!/usr/bin/env bash
# Fetches the voice module's downloads: the needle speech engine into BIN_DIR and the Whistle
# model into MODELS_DIR, both checked against pinned hashes, then links the model beside travis.
# Safe to rerun: a file that already matches its hash is not downloaded again.
set -euo pipefail

BIN_DIR="${1:?usage: fetch-voice.sh <bin-dir> [models-dir]}"
MODELS_DIR="${2:-$HOME/ai}"

# Pinned Hugging Face revisions (both Apache-2.0); bump a revision and its hashes together.
ENGINE_REVISION=d6c7d6a2e9655505e0f8e23339356dd1705900eb # Cactus-Compute/needle3
MODEL_REVISION=741118687e1f008f06d1351a402a731274789790  # Cactus-Compute/whistle
MODEL_SHA256=b6e02f048568ac5d01a2042556c658061e699acbc0aa2a1439f52f3d461dffeb

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) platform=linux-x86_64 engine_sha256=77cd35ee75c53df8532b397e11ed7cf94a45d89a9f14f50881fd9c68ba54a2d0 ;;
  Linux-aarch64 | Linux-arm64) platform=linux-arm64 engine_sha256=a7dd07f740763507edbca760dc7e7065b6da6b6bd6c3a05288ca974f14ce0fcb ;;
  Darwin-arm64) platform=macos-arm64 engine_sha256=0a480f7e01d1fcf9df2e6dbc29af29411406720a5c5cb73024fcd6d16aa25b60 ;;
  *) echo "fetch-voice: no Whistle engine for $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac

hash_file() { (sha256sum "$1" 2>/dev/null || shasum -a 256 "$1") | cut -d' ' -f1; }

# fetch URL DEST SHA256 never leaves a partial or mismatched file at DEST.
fetch() {
  local url=$1 dest=$2 want=$3 got
  if [ -f "$dest" ] && [ "$(hash_file "$dest")" = "$want" ]; then return; fi
  echo "fetch-voice: downloading $(basename "$dest")"
  curl --fail --location --silent --show-error -o "$dest.part" "$url"
  got=$(hash_file "$dest.part")
  if [ "$got" != "$want" ]; then
    rm -f "$dest.part"
    echo "fetch-voice: $url has sha256 $got, want $want" >&2
    exit 1
  fi
  mv "$dest.part" "$dest"
}

mkdir -p "$BIN_DIR" "$MODELS_DIR"
MODELS_DIR="$(cd "$MODELS_DIR" && pwd)" # the link below must not depend on the caller's directory

fetch "https://huggingface.co/Cactus-Compute/needle3/resolve/$ENGINE_REVISION/$platform/needle" "$BIN_DIR/needle" "$engine_sha256"
chmod +x "$BIN_DIR/needle"
fetch "https://huggingface.co/Cactus-Compute/whistle/resolve/$MODEL_REVISION/whistle.cact" "$MODELS_DIR/whistle.cact" "$MODEL_SHA256"
ln -sfn "$MODELS_DIR/whistle.cact" "$BIN_DIR/whistle.cact"
