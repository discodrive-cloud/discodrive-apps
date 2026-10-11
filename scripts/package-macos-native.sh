#!/usr/bin/env bash
# Sign the unsigned native macOS app (make app-macos-unsigned) for Developer ID
# distribution, pack it into a drag-to-Applications .dmg, sign and notarize the dmg.
#
# Usage: scripts/package-macos-native.sh <version>
#
# Xcode cannot sign this app outside its own build (App Group + keychain group need a
# provisioning profile, and ad-hoc signing with them is refused), so the build runs with
# CODE_SIGNING_ALLOWED=NO and this script signs inside-out with codesign: each bundle gets
# its entitlements (the source .entitlements with the build-setting variables expanded, plus
# the application identifier a profile-backed signature carries) and, where it uses the App
# Group or keychain group, its Developer ID profile from clients/macos/Signing/. Those
# profiles are public (every shipped app embeds them) and valid until 2044, but they list the
# Developer ID certificates they accept: after renewing the certificate, regenerate them
# (see dev/ops/release-secrets.md).
#
# Needs a "Developer ID Application" identity ($MACOS_SIGN_IDENTITY or the first one in the
# keychain) — there is no ad-hoc fallback: an ad-hoc signature cannot carry these
# entitlements. Notarization credentials as in macos-notarize.sh; SIGN_REQUIRED=1 (CI)
# makes their absence an error.
set -euo pipefail

VERSION="${1:?usage: $0 <version>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/dist/macos-unsigned/DiscoDrive.app"
SRC="$ROOT/clients/macos"
DMG="$ROOT/dist/DiscoDrive-$VERSION-macos.dmg"
TEAM="TQD2R45V4P"
GROUP_SUFFIX="org.discodrive"

[ -d "$APP" ] || { echo "ERROR: build first (make app-macos-unsigned) — $APP missing" >&2; exit 1; }

identity="${MACOS_SIGN_IDENTITY:-}"
if [ -z "$identity" ]; then
  identity="$(security find-identity -v -p codesigning 2>/dev/null \
    | grep -o '"Developer ID Application: [^"]*"' | head -1 | tr -d '"' || true)"
fi
[ -n "$identity" ] || { echo "ERROR: no Developer ID Application identity in the keychain" >&2; exit 1; }
echo "package-macos-native: signing as \"$identity\""

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# entitlements <source.entitlements> <bundle id or empty> <out.plist>
entitlements() {
  sed -e "s/\$(TeamIdentifierPrefix)/$TEAM./g" \
      -e "s/\$(AppIdentifierPrefix)/$TEAM./g" \
      -e "s/\$(DISCODRIVE_GROUP_SUFFIX)/$GROUP_SUFFIX/g" "$1" > "$3"
  if grep -q '\$(' "$3"; then
    echo "ERROR: unexpanded build setting in $1" >&2
    exit 1
  fi
  if [ -n "$2" ]; then
    # PlistBuddy, not plutil: plutil reads the dots in these keys as a key path.
    /usr/libexec/PlistBuddy -c "Add :com.apple.application-identifier string $TEAM.$2" \
      -c "Add :com.apple.developer.team-identifier string $TEAM" "$3"
  fi
  plutil -lint -s "$3"
}

# sign <bundle> <entitlements plist> [profile]
sign() {
  [ -z "${3:-}" ] || cp "$3" "$1/Contents/embedded.provisionprofile"
  codesign --force --options runtime --timestamp --entitlements "$2" --sign "$identity" "$1"
}

PLUGINS="$APP/Contents/PlugIns"
entitlements "$SRC/FileProviderUI/FileProviderUI.entitlements" "" "$WORK/ui.plist"
entitlements "$SRC/FileProviderExt/FileProviderExt.entitlements" org.discodrive.app.fileprovider "$WORK/ext.plist"
entitlements "$SRC/Resources/DiscoDrive.entitlements" org.discodrive.app "$WORK/app.plist"

# Inside-out: a bundle's signature seals its nested code, so the extensions come first.
sign "$PLUGINS/FileProviderUIExt.appex" "$WORK/ui.plist"
sign "$PLUGINS/FileProviderExt.appex" "$WORK/ext.plist" "$SRC/Signing/FileProviderExt.provisionprofile"
sign "$APP" "$WORK/app.plist" "$SRC/Signing/DiscoDrive.provisionprofile"
codesign --verify --deep --strict --verbose=1 "$APP"

rm -f "$DMG"
STAGE="$WORK/stage"
mkdir "$STAGE"
ditto "$APP" "$STAGE/DiscoDrive.app"
ln -s /Applications "$STAGE/Applications"
hdiutil create -volname "DiscoDrive" -srcfolder "$STAGE" -ov -format UDZO "$DMG"

MACOS_SIGN_IDENTITY="$identity" "$ROOT/scripts/macos-sign.sh" "$DMG"
"$ROOT/scripts/macos-notarize.sh" "$DMG"

# The app as a user gets it: release identity, Developer ID everywhere, no development
# entitlements, and — once notarized — accepted by Gatekeeper.
MNT="$WORK/mnt"
mkdir "$MNT"
hdiutil attach -readonly -nobrowse -mountpoint "$MNT" "$DMG" >/dev/null
status=0
"$ROOT/scripts/verify-native-macos.sh" "$MNT/DiscoDrive.app" || status=$?
hdiutil detach "$MNT" >/dev/null
if [ "$status" -ne 0 ]; then
  if [ "${SIGN_REQUIRED:-0}" = "1" ]; then exit "$status"; fi
  echo "package-macos-native: verification failed (expected without notarization)" >&2
fi
echo "built → $DMG"
