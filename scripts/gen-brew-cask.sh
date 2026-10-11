#!/bin/sh
# Emit the Homebrew cask for the released desktop .dmg of a given version.
# usage: gen-brew-cask.sh <version>   (e.g. 0.0.6) → cask on stdout
#
# The native macOS app (clients/macos, Finder integration via File Provider; macOS 14+).
# The .dmg is universal (arm64 + x86_64), so one url serves both. It is signed with
# Developer ID and notarized, which is what makes a cask viable at all: an unsigned app
# installed by `brew install --cask` looks broken (Gatekeeper refuses to open it).
#
# Downloads the published .dmg to hash it, so the release asset must already exist.
set -eu

VER="${1:?usage: gen-brew-cask.sh <version>}"
URL="https://github.com/discodrive-cloud/discodrive-apps/releases/download/v${VER}/DiscoDrive-${VER}-macos.dmg"

. "$(dirname "$0")/lib-sha256-url.sh"
SHA=$(sha256_url "$URL")

cat <<EOF
cask "discodrive" do
  version "${VER}"
  sha256 "${SHA}"

  url "https://github.com/discodrive-cloud/discodrive-apps/releases/download/v#{version}/DiscoDrive-#{version}-macos.dmg"
  name "DiscoDrive"
  desc "DiscoDrive personal cloud in Finder"
  homepage "https://github.com/discodrive-cloud/discodrive-apps"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: ">= :sonoma"

  app "DiscoDrive.app"

  zap trash: [
    "~/Library/Containers/org.discodrive.app",
    "~/Library/Containers/org.discodrive.app.fileprovider",
    "~/Library/Containers/org.discodrive.app.fileproviderui",
    "~/Library/Group Containers/TQD2R45V4P.org.discodrive",
  ]
end
EOF
