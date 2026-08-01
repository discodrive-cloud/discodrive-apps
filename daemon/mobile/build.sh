#!/usr/bin/env bash
# Build the mobile bindings. Run from the daemon/ module root: mobile/build.sh
# Requires the toolchain from BUILD.md (gomobile + Xcode for iOS, Android SDK/NDK for Android).
set -euo pipefail
cd "$(dirname "$0")/.."   # → daemon/
mkdir -p mobile/build

echo "== iOS (.xcframework) =="
gomobile bind -target=ios -o mobile/build/Kfmobile.xcframework ./mobile

echo "== Android (.aar) =="
gomobile bind -target=android -androidapi 21 \
  -ldflags="-extldflags=-Wl,-z,max-page-size=16384" \
  -o mobile/build/kfmobile.aar ./mobile

echo "Done: mobile/build/"
