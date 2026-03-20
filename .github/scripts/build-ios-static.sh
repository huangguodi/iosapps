#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="$ROOT_DIR/build/ios-static"
XCFRAMEWORK_DIR="$OUT_DIR/Mobile.xcframework"
ARM64_FRAMEWORK_DIR="$XCFRAMEWORK_DIR/ios-arm64/Mobile.framework"
IOS_MIN_VERSION="${IOS_MIN_VERSION:-15.0}"
DEFAULT_TAGS="purego"
USER_TAGS="${GO_TAGS:-}"
EXTRA_CFLAGS="-miphoneos-version-min=${IOS_MIN_VERSION} -fembed-bitcode -O2"
EXTRA_LDFLAGS="-miphoneos-version-min=${IOS_MIN_VERSION} -Wl,-dead_strip"

if [[ -n "$USER_TAGS" ]]; then
  BUILD_TAGS="$DEFAULT_TAGS $USER_TAGS"
else
  BUILD_TAGS="$DEFAULT_TAGS"
fi

export CGO_ENABLED=1
SANITIZED_CGO_CFLAGS="${CGO_CFLAGS:-}"
SANITIZED_CGO_CXXFLAGS="${CGO_CXXFLAGS:-}"
SANITIZED_CGO_LDFLAGS="${CGO_LDFLAGS:-}"

SANITIZED_CGO_CFLAGS="${SANITIZED_CGO_CFLAGS//-O3/}"
SANITIZED_CGO_CXXFLAGS="${SANITIZED_CGO_CXXFLAGS//-O3/}"
SANITIZED_CGO_LDFLAGS="${SANITIZED_CGO_LDFLAGS//-O3/}"

export CGO_CFLAGS="${SANITIZED_CGO_CFLAGS} ${EXTRA_CFLAGS}"
export CGO_CXXFLAGS="${SANITIZED_CGO_CXXFLAGS} ${EXTRA_CFLAGS}"
export CGO_LDFLAGS="${SANITIZED_CGO_LDFLAGS} ${EXTRA_LDFLAGS}"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

gomobile init

gomobile bind -target=ios -tags "$BUILD_TAGS" -o "$XCFRAMEWORK_DIR" ./mobile

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

if otool -L "$ARM64_FRAMEWORK_DIR/Mobile" | grep -E "libcrypto\\.(so|dylib)|libssl\\.(so|dylib)" >/dev/null; then
  echo "dynamic crypto library detected in output"
  exit 1
fi
