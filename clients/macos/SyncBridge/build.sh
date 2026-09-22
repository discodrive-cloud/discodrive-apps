#!/bin/bash
set -euo pipefail
export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export GOCACHE="${DERIVED_FILE_DIR}/go-cache"
cd "${SRCROOT}/../../daemon"
archives=()
for arch in $ARCHS; do
  case "$arch" in arm64) goarch=arm64 ;; x86_64) goarch=amd64 ;; *) exit 1 ;; esac
  output="${DERIVED_FILE_DIR}/sync-${arch}.a"
  CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" \
    CGO_CFLAGS="-arch $arch -mmacosx-version-min=14.0" \
    CGO_LDFLAGS="-arch $arch -mmacosx-version-min=14.0" \
    go build -buildmode=c-archive -trimpath -o "$output" ./native
  archives+=("$output")
done
/usr/bin/lipo -create "${archives[@]}" -output "${DERIVED_FILE_DIR}/libDiscoSync.a"
