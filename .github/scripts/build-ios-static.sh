#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/build/ios-static"
FRAMEWORK_DIR="$OUT_DIR/Mobile.framework"

mkdir -p "$OUT_DIR"

gomobile init

if [[ -n "${GO_TAGS:-}" ]]; then
  gomobile bind -target=ios -tags "${GO_TAGS}" -o "$FRAMEWORK_DIR" ./mobile
else
  gomobile bind -target=ios -o "$FRAMEWORK_DIR" ./mobile
fi

cp "$FRAMEWORK_DIR/Mobile" "$OUT_DIR/libmihomo.a"

HEADER_FILE="$(find "$FRAMEWORK_DIR/Headers" -maxdepth 1 -name "*.h" | head -n 1)"
cp "$HEADER_FILE" "$OUT_DIR/libmihomo.h"
