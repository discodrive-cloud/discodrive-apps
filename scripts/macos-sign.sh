#!/usr/bin/env bash
# Sign macOS artefacts (an .app, a bare executable, a .dmg) with the Developer ID
# certificate, the hardened runtime and a secure timestamp — what notarization requires.
#
# Usage: scripts/macos-sign.sh <path>...
#
# Identity: $MACOS_SIGN_IDENTITY, else the first "Developer ID Application" in the
# keychain. Without one the artefact is ad-hoc signed and a notice is printed, so a
# contributor without a certificate can still build; set SIGN_REQUIRED=1 (CI does) to
# fail instead.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: $0 <path>..." >&2; exit 2; }

identity="${MACOS_SIGN_IDENTITY:-}"
if [ -z "$identity" ]; then
  identity="$(security find-identity -v -p codesigning 2>/dev/null \
    | grep -o '"Developer ID Application: [^"]*"' | head -1 | tr -d '"' || true)"
fi

if [ -z "$identity" ]; then
  if [ "${SIGN_REQUIRED:-0}" = "1" ]; then
    echo "macos-sign: no Developer ID Application identity in the keychain and SIGN_REQUIRED=1" >&2
    exit 1
  fi
  echo "macos-sign: no Developer ID identity — ad-hoc signing (Gatekeeper will warn)"
  for p in "$@"; do codesign --force --sign - "$p"; done
  exit 0
fi

echo "macos-sign: signing as \"$identity\""
for p in "$@"; do
  case "$p" in
    *.dmg) codesign --force --timestamp --sign "$identity" "$p" ;;
    *)     codesign --force --options runtime --timestamp --sign "$identity" "$p" ;;
  esac
  codesign --verify --strict --verbose=1 "$p" 2>&1 | sed 's/^/  /'
  echo "  signed: $p"
done
