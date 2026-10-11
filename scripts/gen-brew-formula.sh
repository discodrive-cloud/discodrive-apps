#!/bin/sh
# Emit the Homebrew formula for the released headless daemon of a given version.
# usage: gen-brew-formula.sh <version>   (e.g. 0.1.0) → formula on stdout
#
# Linux only: the daemon is for servers and NAS boxes. Desktops sync folders inside the
# apps (the macOS app is the cask), so there are no darwin builds and no tray flavour.
#
# Downloads the two published tarballs to hash them, so the release assets must already
# exist on GitHub.
set -eu

VER="${1:?usage: gen-brew-formula.sh <version>}"
BASE="https://github.com/discodrive-cloud/discodrive-apps/releases/download/v${VER}"

. "$(dirname "$0")/lib-sha256-url.sh"
sha() { sha256_url "$BASE/discodrive-daemon-$1.tar.gz"; }

SHA_LINUX_ARM64=$(sha linux-arm64)
SHA_LINUX_AMD64=$(sha linux-amd64)

cat <<FORMULA
class DiscodriveDaemon < Formula
  desc "Headless sync daemon for the DiscoDrive personal cloud"
  homepage "https://github.com/discodrive-cloud/discodrive-apps"
  version "${VER}"
  license "PolyForm-Noncommercial-1.0.0"

  depends_on :linux

  on_arm do
    url "${BASE}/discodrive-daemon-linux-arm64.tar.gz"
    sha256 "${SHA_LINUX_ARM64}"
  end
  on_intel do
    url "${BASE}/discodrive-daemon-linux-amd64.tar.gz"
    sha256 "${SHA_LINUX_AMD64}"
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
    EOS
  end

  test do
    output = shell_output("#{bin}/discodrive 2>&1", 2)
    assert_match "pair|run", output
  end
end
FORMULA
