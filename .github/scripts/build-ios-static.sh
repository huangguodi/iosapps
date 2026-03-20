#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/build/ios-static"
XCFRAMEWORK_DIR="$OUT_DIR/Mobile.xcframework"
ARM64_FRAMEWORK_DIR="$XCFRAMEWORK_DIR/ios-arm64/Mobile.framework"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

gomobile init

if [[ -n "${GO_TAGS:-}" ]]; then
  gomobile bind -target=ios -tags "${GO_TAGS}" -o "$XCFRAMEWORK_DIR" ./mobile
else
  gomobile bind -target=ios -o "$XCFRAMEWORK_DIR" ./mobile
fi

if [[ ! -f "$ARM64_FRAMEWORK_DIR/Mobile" ]]; then
  echo "arm64 framework not found in $XCFRAMEWORK_DIR"
  exit 1
fi
cp "$ARM64_FRAMEWORK_DIR/Mobile" "$OUT_DIR/libmihomo.a"

HEADER_FILE="$(find "$ARM64_FRAMEWORK_DIR/Headers" -maxdepth 1 -name "*.h" | head -n 1)"
if [[ -z "$HEADER_FILE" ]]; then
  echo "header file not found in $ARM64_FRAMEWORK_DIR/Headers"
  exit 1
fi
cp "$HEADER_FILE" "$OUT_DIR/libmihomo.h"
