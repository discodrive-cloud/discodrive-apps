# DiscoDrive — native macOS client (standalone)

A windowed app for accessing DiscoDrive files: displays the full tree, fetches content
**on demand**, and lets you **keep individual files local**. No Finder integration.

## Where this is going

The app is sandboxed and signed with the team's Developer ID (paid Apple Developer account
since 2026-09). A File Provider extension — the DiscoDrive folder in Finder, like iCloud
Drive — is being added next; until it lands, files are browsed in the app window. The
index database lives in the App Group container (`~/Library/Group Containers/<team>.org.discodrive`)
and the device token in the keychain access group of the same name, so the extension can
share them. Both identifiers are declared in `Resources/Info.plist` next to the
entitlements.

## Requirements

- Xcode 26+, [XcodeGen](https://github.com/yonaskolb/XcodeGen) (`brew install xcodegen`).
- A paid Apple Developer team: `DEVELOPMENT_TEAM` in `project.yml`. The app is sandboxed
  and uses an App Group and a keychain access group, which a free team cannot sign.

## Build and run

```bash
cd clients/macos
xcodegen generate
xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -derivedDataPath build build
open build/Build/Products/Debug/DiscoDrive.app
```
Or open `DiscoDrive.xcodeproj` in Xcode (scheme **DiscoDrive**) and click Run.

## Pairing

On first launch, enter your DiscoDrive server address → click "Connect" → confirm the
device in the browser that opens. The token is saved in Keychain.

## What it can do (v1)

- Browse the full file tree (folder tree + list view, navigate up to the "DiscoDrive" root).
- Download a file on double-click (on-demand) and open it in an external app.
- "Keep local" (📌) — pinned copies are never evicted.
- "Free up space" — clears non-pinned copies.
- Multilingual UI (language is stored on the server and synced across devices).

## Out of scope for v1

File upload/editing on the server, encrypted vault (Cryptomator), iOS app,
notarization. iOS will reuse the `clients/DiscoKit` core.

## Distribution

Releases are signed with Developer ID and notarized (`scripts/macos-sign.sh`,
`scripts/macos-notarize.sh`); the App Store is the eventual channel.
