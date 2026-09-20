#!/usr/bin/env bash
# Builds dist/mac/CrossScreen.app as a universal (arm64 + x86_64) binary with
# an explicit macOS deployment target, so the app also runs on older Intel
# Macs and older macOS releases.
#
# Floor: macOS 12 Monterey. This is the minimum enforced by the Go 1.26
# runtime; lowering it further requires building with an older Go toolchain.
set -euo pipefail

cd "$(dirname "$0")/.."

MIN_MACOS="${MACOS_MIN:-12.0}"
APP="dist/mac/CrossScreen.app"
BIN="$APP/Contents/MacOS/CrossScreen"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET="$MIN_MACOS"
export CGO_CFLAGS="-mmacosx-version-min=$MIN_MACOS"
export CGO_LDFLAGS="-mmacosx-version-min=$MIN_MACOS"

echo "==> building arm64 slice (min macOS $MIN_MACOS)"
GOARCH=arm64 go build -trimpath -o "$TMP/CrossScreen-arm64" ./cmd/crossscreen

echo "==> building x86_64 slice (min macOS $MIN_MACOS)"
GOARCH=amd64 go build -trimpath -o "$TMP/CrossScreen-x86_64" ./cmd/crossscreen

echo "==> creating universal binary"
mkdir -p "$(dirname "$BIN")"
lipo -create -output "$BIN" "$TMP/CrossScreen-arm64" "$TMP/CrossScreen-x86_64"

echo "==> ad-hoc signing"
codesign --force --sign - --timestamp=none "$APP"

echo "==> result:"
lipo -info "$BIN"
otool -l "$BIN" | grep -A4 LC_BUILD_VERSION | grep -E "minos|sdk" | sort -u
