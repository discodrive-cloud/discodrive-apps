#!/usr/bin/env bash
# Notarize a signed macOS artefact with Apple and staple the ticket to it.
#
# Usage: scripts/macos-notarize.sh <file.dmg|file.zip|file.pkg>
#
# Credentials, one of:
#   NOTARY_PROFILE   keychain profile saved once with
#                    `xcrun notarytool store-credentials <name>` (default: discodrive)
#   NOTARY_KEY_PATH + NOTARY_KEY_ID + NOTARY_ISSUER_ID
#                    an App Store Connect API key (.p8) — what CI uses
# Without credentials the step is skipped with a notice, unless SIGN_REQUIRED=1.
#
# A .zip cannot be stapled (there is nothing to attach the ticket to); Gatekeeper then
# checks the ticket online, which is how bare command-line binaries are notarized.
set -euo pipefail

f="${1:?usage: $0 <file.dmg|file.zip|file.pkg>}"
[ -e "$f" ] || { echo "macos-notarize: $f does not exist" >&2; exit 1; }

auth=()
if [ -n "${NOTARY_KEY_PATH:-}" ]; then
  : "${NOTARY_KEY_ID:?NOTARY_KEY_ID is required with NOTARY_KEY_PATH}"
  : "${NOTARY_ISSUER_ID:?NOTARY_ISSUER_ID is required with NOTARY_KEY_PATH}"
  auth=(--key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER_ID")
else
  profile="${NOTARY_PROFILE:-discodrive}"
  if xcrun notarytool history --keychain-profile "$profile" >/dev/null 2>&1; then
    auth=(--keychain-profile "$profile")
  fi
fi

if [ ${#auth[@]} -eq 0 ]; then
  if [ "${SIGN_REQUIRED:-0}" = "1" ]; then
    echo "macos-notarize: no notarization credentials and SIGN_REQUIRED=1" >&2
    exit 1
  fi
  echo "macos-notarize: no credentials (keychain profile '${NOTARY_PROFILE:-discodrive}' or NOTARY_KEY_*) — skipping"
  exit 0
fi

echo "macos-notarize: submitting $(basename "$f") …"
out="$(xcrun notarytool submit "$f" "${auth[@]}" --wait --timeout 45m 2>&1)" || true
echo "$out" | sed 's/^/  /'

if ! echo "$out" | grep -q 'status: Accepted'; then
  id="$(echo "$out" | grep -m1 -E '^\s*id: ' | awk '{print $2}')"
  if [ -n "$id" ]; then
    echo "macos-notarize: not accepted — log for $id:" >&2
    xcrun notarytool log "$id" "${auth[@]}" >&2 || true
  fi
  exit 1
fi

case "$f" in
  *.dmg|*.pkg|*.app)
    xcrun stapler staple "$f"
    echo "macos-notarize: stapled $f"
    ;;
  *)
    echo "macos-notarize: accepted (no staple for $(basename "$f"))"
    ;;
esac
