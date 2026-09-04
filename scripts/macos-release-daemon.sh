#!/usr/bin/env bash
# Sign and notarize the darwin daemon binaries already built under dist/, then tar them
# the way the release and the Homebrew formulas expect (discodrive-daemon-<dir>.tar.gz).
#
# Usage: scripts/macos-release-daemon.sh
#
# The four binaries are notarized in one zip (Apple accepts an archive of bare Mach-O
# files); nothing can be stapled to a bare executable, Gatekeeper checks the ticket online.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"

dirs=()
for d in darwin-amd64 darwin-arm64 darwin-amd64-tray darwin-arm64-tray; do
  [ -x "$DIST/$d/discodrive" ] && dirs+=("$d")
done
[ ${#dirs[@]} -gt 0 ] || { echo "macos-release-daemon: nothing built under $DIST/darwin-*" >&2; exit 1; }

bins=()
for d in "${dirs[@]}"; do bins+=("$DIST/$d/discodrive"); done
"$ROOT/scripts/macos-sign.sh" "${bins[@]}"

ZIP="$DIST/discodrive-daemon-darwin-notarize.zip"
rm -f "$ZIP"
(cd "$DIST" && zip -q -r "$ZIP" "${dirs[@]}")
"$ROOT/scripts/macos-notarize.sh" "$ZIP"
rm -f "$ZIP"

for d in "${dirs[@]}"; do
  tar czf "$DIST/discodrive-daemon-$d.tar.gz" -C "$DIST/$d" .
  echo "packed → dist/discodrive-daemon-$d.tar.gz"
done
