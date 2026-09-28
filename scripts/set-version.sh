#!/usr/bin/env bash
# Stamp the Wails app version into wails.json's info.productVersion so it flows
# into every built artifact: the macOS Info.plist (CFBundleShortVersionString),
# the Windows version resource, and the NSIS installer's product version.
#
# Usage: scripts/set-version.sh [version]
# Empty/absent version is a no-op — leaves wails.json as committed (local builds).
set -euo pipefail

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "set-version: no version given — leaving wails.json as-is"
  exit 0
fi

# The version comes from a tag name and ends up in build steps; accept only x.y.z with
# an optional pre-release suffix (1.2.3-rc.1), nothing a shell could read as code.
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
  echo "set-version: '$VERSION' is not an x.y.z[-pre] version" >&2
  exit 1
fi

command -v jq >/dev/null 2>&1 || { echo "set-version: jq is required" >&2; exit 1; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WJ="$ROOT/daemon/cmd/discodrive-wails/wails.json"

tmp="$(mktemp)"
jq --arg v "$VERSION" '.info.productVersion = $v' "$WJ" > "$tmp"
mv "$tmp" "$WJ"
echo "set-version: wails.json info.productVersion = $VERSION"
