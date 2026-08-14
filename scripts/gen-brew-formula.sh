#!/bin/sh
# Emit a Homebrew formula for the released daemon tarballs of a given version.
# usage: gen-brew-formula.sh <version> [tray]   (e.g. 0.0.5) → formula on stdout
#
# Two flavours ship from the same release: the headless daemon, and the desktop one with a
# menu bar / tray icon. They are separate formulas because they install the same binary —
# hence the conflicts_with in each, without which Homebrew would let both in and leave
# whichever was linked last in place.
#
# Downloads the four published tarballs to hash them, so the release assets must already
# exist on GitHub.
set -eu

VER="${1:?usage: gen-brew-formula.sh <version> [tray]}"
FLAVOR="${2:-}"
BASE="https://github.com/discodrive-cloud/discodrive-apps/releases/download/v${VER}"

case "$FLAVOR" in
  "")
    SUFFIX=""
    CLASS="DiscodriveDaemon"
    OTHER="discodrive-daemon-tray"
    DESC="Headless sync daemon for the DiscoDrive personal cloud"
    TRAY_NOTE=""
    ;;
  tray)
    SUFFIX="-tray"
    CLASS="DiscodriveDaemonTray"
    OTHER="discodrive-daemon"
    DESC="Sync daemon for the DiscoDrive personal cloud, with a menu bar icon"
    TRAY_NOTE="
      Run it with the menu bar icon:
        discodrive tray
"
    ;;
  *)
    echo "gen-brew-formula: unknown flavour '$FLAVOR' (want '' or 'tray')" >&2
    exit 2
    ;;
esac

sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    curl -fsSL "$BASE/discodrive-daemon-$1${SUFFIX}.tar.gz" | sha256sum | cut -d' ' -f1
  else
    curl -fsSL "$BASE/discodrive-daemon-$1${SUFFIX}.tar.gz" | shasum -a 256 | cut -d' ' -f1
  fi
}

SHA_DARWIN_ARM64=$(sha darwin-arm64)
SHA_DARWIN_AMD64=$(sha darwin-amd64)
SHA_LINUX_ARM64=$(sha linux-arm64)
SHA_LINUX_AMD64=$(sha linux-amd64)

cat <<EOF
class ${CLASS} < Formula
  desc "${DESC}"
  homepage "https://github.com/discodrive-cloud/discodrive-apps"
  version "${VER}"
  license "PolyForm-Noncommercial-1.0.0"

  conflicts_with "${OTHER}", because: "both install the discodrive binary"

  on_macos do
    on_arm do
      url "${BASE}/discodrive-daemon-darwin-arm64${SUFFIX}.tar.gz"
      sha256 "${SHA_DARWIN_ARM64}"
    end
    on_intel do
      url "${BASE}/discodrive-daemon-darwin-amd64${SUFFIX}.tar.gz"
      sha256 "${SHA_DARWIN_AMD64}"
    end
  end

  on_linux do
    on_arm do
      url "${BASE}/discodrive-daemon-linux-arm64${SUFFIX}.tar.gz"
      sha256 "${SHA_LINUX_ARM64}"
    end
    on_intel do
      url "${BASE}/discodrive-daemon-linux-amd64${SUFFIX}.tar.gz"
      sha256 "${SHA_LINUX_AMD64}"
    end
  end

  def install
    bin.install "discodrive"
  end

  def caveats
    <<~EOS
      Pair with your DiscoDrive server first:
        discodrive pair --server https://your-server.example
      Then run it in the foreground with \`discodrive run\`, or install it
      as a login service with \`discodrive install\`.
${TRAY_NOTE}    EOS
  end

  test do
    output = shell_output("#{bin}/discodrive 2>&1", 2)
    assert_match "pair|run|tray|status|install|uninstall", output
  end
end
EOF
