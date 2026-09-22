#!/bin/bash
set -euo pipefail
export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export GOCACHE="${DERIVED_FILE_DIR}/go-cache"
cd "${SRCROOT}/../../daemon"
archives=()
for arch in $ARCHS; do
  case "$arch" in arm64) goarch=arm64 ;; x86_64) goarch=amd64 ;; *) exit 1 ;; esac
  target="$arch-apple-ios${IPHONEOS_DEPLOYMENT_TARGET}"
  if [[ "$PLATFORM_NAME" == "iphonesimulator" ]]; then target="$target-simulator"; fi
  output="${DERIVED_FILE_DIR}/sync-${arch}.a"
  CGO_ENABLED=1 GOOS=ios GOARCH="$goarch" CC="$(xcrun --find clang)" \
    CGO_CFLAGS="-target $target -isysroot $SDKROOT" \
    CGO_LDFLAGS="-target $target -isysroot $SDKROOT" \
    go build -buildmode=c-archive -trimpath -o "$output" ./native
  archives+=("$output")
done
/usr/bin/lipo -create "${archives[@]}" -output "${DERIVED_FILE_DIR}/libDiscoSync.a"
