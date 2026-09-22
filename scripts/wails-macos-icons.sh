#!/usr/bin/env bash
# Wails post-build hook: use the native app's Icon Composer and tray assets.
# Wails invokes hooks from build/bin and passes the packaged executable as $1.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
case "${1:-}" in
  *.app/Contents/MacOS/*) APP="$(dirname "$(dirname "$(dirname "$1")")")" ;;
  *) exit 0 ;; # Unpackaged development binary.
esac
RESOURCES="$APP/Contents/Resources"
SOURCE="$ROOT/clients/macos/Resources"
STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT
xcrun actool "$SOURCE/Assets.xcassets" "$SOURCE/DiscoDrive.icon" \
  --compile "$STAGING" --platform macosx --target-device mac \
  --minimum-deployment-target 14.0 --app-icon DiscoDrive \
  --enable-on-demand-resources NO --development-region en \
  --output-partial-info-plist "$STAGING/icons.plist" \
  --output-format human-readable-text --warnings
cp "$STAGING/Assets.car" "$RESOURCES/Assets.car"
cp "$STAGING/DiscoDrive.icns" "$RESOURCES/DiscoDrive.icns"
plutil -replace CFBundleIconFile -string DiscoDrive "$APP/Contents/Info.plist"
plutil -replace CFBundleIconName -string DiscoDrive "$APP/Contents/Info.plist"
# Packaging was already ad-hoc signed by Wails; adding resources invalidates that
# signature. Distribution signing with Developer ID remains a separate build step.
codesign --force --sign - "$APP"

# Finder shows the bundle directory timestamp, not its executable timestamp.
touch -r "$1" "$APP"
