#!/usr/bin/env bash
# Package the built Wails macOS app into a drag-to-Applications .dmg, signed and
# notarized when the machine has a Developer ID certificate and notarization
# credentials (see macos-sign.sh / macos-notarize.sh — both degrade to a notice without).
# Usage: scripts/package-macos-dmg.sh [version]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/daemon/cmd/discodrive-wails/build/bin/discodrive-wails.app"
VERSION="${1:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")}"
DIST="$ROOT/dist"
DMG="$DIST/DiscoDrive-$VERSION-macos.dmg"

[ -d "$APP" ] || { echo "ERROR: build first — $APP missing"; exit 1; }
mkdir -p "$DIST"
rm -f "$DMG"

# Sign the bundle in place: the signature does not depend on the bundle's file name, so
# the copy below is still valid as DiscoDrive.app.
"$ROOT/scripts/macos-sign.sh" "$APP"

STAGE="$(mktemp -d)"
cp -R "$APP" "$STAGE/DiscoDrive.app"
ln -s /Applications "$STAGE/Applications"

hdiutil create -volname "DiscoDrive" -srcfolder "$STAGE" -ov -format UDZO "$DMG"
rm -rf "$STAGE"

"$ROOT/scripts/macos-sign.sh" "$DMG"
"$ROOT/scripts/macos-notarize.sh" "$DMG"

# What Gatekeeper will say about it (informational: "accepted" only once notarized).
spctl -a -t open --context context:primary-signature -v "$DMG" 2>&1 | sed 's/^/  gatekeeper: /' || true
echo "built → $DMG"
