#!/bin/sh
# Emit the Homebrew cask for the released desktop .dmg of a given version.
# usage: gen-brew-cask.sh <version>   (e.g. 0.0.6) → cask on stdout
#
# The .dmg is universal (arm64 + x86_64), so one url serves both. It is signed with
# Developer ID and notarized, which is what makes a cask viable at all: an unsigned app
# installed by `brew install --cask` looks broken (Gatekeeper refuses to open it).
#
# Downloads the published .dmg to hash it, so the release asset must already exist.
set -eu

VER="${1:?usage: gen-brew-cask.sh <version>}"
URL="https://github.com/discodrive-cloud/discodrive-apps/releases/download/v${VER}/DiscoDrive-${VER}-macos.dmg"

if command -v sha256sum >/dev/null 2>&1; then
  SHA=$(curl -fsSL "$URL" | sha256sum | cut -d' ' -f1)
else
  SHA=$(curl -fsSL "$URL" | shasum -a 256 | cut -d' ' -f1)
fi

cat <<EOF
cask "discodrive" do
  version "${VER}"
  sha256 "${SHA}"

  url "https://github.com/discodrive-cloud/discodrive-apps/releases/download/v#{version}/DiscoDrive-#{version}-macos.dmg"
  name "DiscoDrive"
  desc "Desktop client for the DiscoDrive personal cloud"
  homepage "https://github.com/discodrive-cloud/discodrive-apps"

  livecheck do
    url :url
    strategy :github_latest
  end

  app "DiscoDrive.app"

  zap trash: [
    "~/Library/Application Support/discodrive/desktop",
    "~/Library/LaunchAgents/com.wails.discodrive-wails.plist",
  ]
end
EOF
