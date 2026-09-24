# DiscoDrive — native macOS client (standalone)

A windowed app for accessing DiscoDrive files: displays the full tree, fetches content
**on demand**, and lets you **keep individual files local**. A File Provider extension
exposes ordinary files and unlocked vaults in Finder.

## Integration

The app is sandboxed and signed with the team's Developer ID (paid Apple Developer account
since 2026-09). Files can be browsed in the app and in the DiscoDrive Finder location.
The index database lives in the App Group container (`~/Library/Group Containers/<team>.org.discodrive`)
and the device token in the keychain access group of the same name, so the extension can
share them. Both identifiers are declared in `Resources/Info.plist` next to the
entitlements.

## Requirements

- Go 1.25+ for the embedded sync engine (the build script finds standard Go installations).
- Xcode 26+, [XcodeGen](https://github.com/yonaskolb/XcodeGen) (`brew install xcodegen`).
- A paid Apple Developer team: `DEVELOPMENT_TEAM` in `project.yml`. The app is sandboxed
  and uses an App Group and a keychain access group, which a free team cannot sign.

## Build and run

The default Debug build is for development only. Do not distribute it or use it to update
a paired Release installation: it has a separate account identity and a development profile.

```bash
cd clients/macos
xcodegen generate
xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -derivedDataPath build build
open build/Build/Products/Debug/DiscoDrive.app
```
Or open `DiscoDrive.xcodeproj` in Xcode (scheme **DiscoDrive**) and click Run.

Debug uses `org.discodrive.app.debug`, its own App Group/keychain group and the
`discodrive-debug://` URL scheme. Pair it separately; it cannot replace the Release
provider or use the Release account's stored credentials. Regenerate the project with
XcodeGen after changing `project.yml`.

For a signed build to use with your existing Finder location:

```bash
xcodebuild -project DiscoDrive.xcodeproj -scheme DiscoDrive -configuration Release -derivedDataPath build build
codesign --verify --deep --strict build/Build/Products/Release/DiscoDrive.app
```

The bundle includes the file provider and its authentication UI extension. The Sign In
button opens this containing app; a successful refresh resumes Finder operations without
removing or reimporting the domain. macOS provider regression tests run with
`xcodebuild -project DiscoDrive.xcodeproj -scheme ProviderTests -destination 'platform=macOS' -derivedDataPath build test`.


## Pairing

On first launch, enter your DiscoDrive server address → click "Connect" → confirm the
device in the browser that opens. The token is saved in Keychain.

## Features

- Browse the full file tree (folder tree + list view, navigate up to the "DiscoDrive" root).
- Download a file on double-click (on-demand) and open it in an external app.
- "Keep local" (📌) — pinned copies are never evicted.
- "Free up space" — clears non-pinned copies.
- Multilingual UI (language is stored on the server and synced across devices).

- Upload, rename, delete and manage Cryptomator-compatible vaults.
- Open Trash from the toolbar; restore entries or permanently delete them after confirmation.
- Open Version history from a file's context menu and restore a saved version.
- Share files and ordinary folders by read-only link or email, set expiry, and revoke access.
- Inspect the current sync path, completed operation count, and recent errors in Settings → Sync activity.

## Distribution

Use an archive exported with Developer ID signing for the app **and both extensions**.
A normal Release build can still carry an Apple Development signature and a profile limited
to registered Macs. Release configuration alone does not make it distributable.

From the repository root (Apple Silicon):

```bash
xcodebuild -project clients/macos/DiscoDrive.xcodeproj -scheme DiscoDrive \
  -configuration Release -destination 'generic/platform=macOS' ARCHS=arm64 \
  -archivePath clients/macos/build/DiscoDrive.xcarchive archive
xcodebuild -exportArchive -archivePath clients/macos/build/DiscoDrive.xcarchive \
  -exportPath clients/macos/build/distribution \
  -exportOptionsPlist clients/macos/DistributionExport.plist -allowProvisioningUpdates
ditto -c -k --keepParent clients/macos/build/distribution/DiscoDrive.app /tmp/DiscoDrive-native.zip
SIGN_REQUIRED=1 bash scripts/macos-notarize.sh /tmp/DiscoDrive-native.zip
xcrun stapler staple clients/macos/build/distribution/DiscoDrive.app
scripts/verify-native-macos.sh clients/macos/build/distribution/DiscoDrive.app
```

Re-create the ZIP after stapling before transferring it. The verification rejects Debug
identities, development signing/entitlements, device-restricted profiles and Gatekeeper
rejection. The distribution bundle retains `org.discodrive.app` and its Release App Group
and Keychain group; do not reset pairing or remove the Finder domain to update it.
The App Store is a separate distribution channel.

## Full folder synchronization

Settings contains a separate **Folder synchronization** section. Choose a dedicated local
folder, then enable synchronization. The selection is a security-scoped bookmark; both it
and the switch survive app/system restarts. Enable **Launch at login** to resume after login.
Closing the main window keeps syncing in the menu bar; quitting stops all work before
releasing folder access. Logout disables synchronization for the old account.

The app links the existing Go engine as a static C archive: no installed or child daemon,
no additional credentials file, and no connection to Finder's disposable download cache.
Filesystem events, server events and a periodic scan trigger push/pull. The server's sync
scope is honored: disabling the server's single-folder restriction mirrors the whole storage.
The app does not change that server setting. Use one sync owner per local folder; stop an
existing standalone daemon before assigning its directory to the app.

On the first run for an account and local directory, previous files (including hidden files)
are moved into `Application Support/FullSync/Backups` in the app's sandbox, accessible through
**Show previous files**. The selected directory itself keeps its identity and access grant.
A failed preparation cannot start network synchronization. Account and directory identity
select an independent index; replacing the local directory cannot reuse old deletion history.
A mass-deletion guard pauses uploads until the user explicitly confirms in Settings.

The Xcode prebuild phase compiles `daemon/native` for the target architectures. Native
preparation tests: `xcodebuild -project DiscoDrive.xcodeproj -scheme SyncTests -destination
'platform=macOS' -derivedDataPath build test`. Engine/integration tests from `daemon`:
`go test -race ./native ./internal/engine ./internal/index ./internal/protocol ./internal/syncer`.
