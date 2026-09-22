# DiscoDrive for iOS

## Building

Requires Xcode, XcodeGen, and Go. No additional dependencies are needed: the embedded
engine in `daemon/native` is compiled into a C archive by
`clients/ios/SyncBridge/build.sh`.

From the repository root:

```sh
xcodegen generate --spec clients/ios/project.yml
xcodebuild -project clients/ios/DiscoDrive.xcodeproj -scheme DiscoDrive \
  -configuration Debug -sdk iphonesimulator -derivedDataPath clients/ios/build build
```

For device installation, the app and File Provider provisioning profiles must allow
App Group `group.org.discodrive.ios` and the shared Keychain group
`<AppIdentifierPrefix>org.discodrive.ios.shared`. The app retains access to the legacy
`<AppIdentifierPrefix>org.discodrive.ios` group to migrate existing pairing credentials.
The File Provider UI extension offers local copy removal and authentication guidance.
Vaults open only inside the app.

## Files and vaults

After pairing, the app registers DiscoDrive in Files. If needed, enable it under
Browse → Edit. Ordinary files download on demand. The file context menu offers
Remove local copy: this frees the File Provider cache while preserving the server
file. Close files that are in use and let pending changes finish syncing first.

Vaults open and display their contents inside DiscoDrive. The app no longer creates
separate vault locations in Files. On startup, the updated app removes old vault
locations through the system API; it deletes their keys only after the system confirms
removal. If removal fails, the key is retained and Settings offers a retry. This does
not reset the ordinary DiscoDrive location or the Sync folder. A new anchor version
requests a one-time refresh of menu metadata. On authentication errors, the extension
asks the user to open the app.

Reading, creating, and saving vault file contents use temporary files and encryption
in 32 KiB chunks. Rename and move operations through the shared `VaultWrite` still
use the previous in-memory content path. For File Provider save conflicts, the server
version is preserved and the local edit stays with File Provider for resolution.

The ordinary browser cache lives in Application Support rather than Documents.
Files left in Documents by an earlier version are neither deleted automatically nor
uploaded again. Logout and a new pairing preserve Documents.

## Folder synchronization

Settings enables a full two-way mirror of the folder selected on the server.
The device uses `DiscoDrive/Sync` under On My iPhone/iPad. Before the first run after
each new pairing, the entire existing folder, including hidden files, is renamed
and preserved under the adjacent `Sync Backups` directory. A new empty `Sync` folder
is populated from the server; old backup files are not uploaded. A backup failure
prevents startup. An ordinary restart preserves the current mirror.

Upgrading from a version without a pairing marker performs this backup once more.
The enabled flag and state survive restarts. The mirror is separate from both the
browser cache and File Provider locations.

The engine runs while the app is active and during time granted by iOS through
BGProcessingTask. Continuous background execution is not guaranteed. On entering the
background, the app stops the current pass and requests another opportunity; returning
to the app resumes work. Logout disables sync, waits for it to stop, and preserves
local files.

Replacing or losing the root folder during a run stops the pass. Mass deletion requires
separate confirmation. The mirror database is bound to the server, token, and local
folder identity.

## Sharing and diagnostics

Share in the app browser creates a read-only link or grants access by email, with
optional expiration and access revocation.

The Diagnostics section in Settings enables sync logging. No log files are created
by default. When enabled, logs appear under On My iPhone/iPad → DiscoDrive → Logs:
`sync.log` and one previous copy, up to 2 MiB each. They contain paths, operation
durations, and errors; passwords and tokens are not logged. Disabling logging stops
new writes and preserves existing files. The export button shares the current log
or saves it to a chosen folder through the system share sheet.

## Validation

- `swift test --package-path clients/DiscoKit` — shared library.
- `iOSTests` scheme — legacy-to-shared Keychain migration in iOS Simulator.
- `UserActionsUITests` — sharing, logging settings, previews, and Files actions;
  requires an isolated server with `remote.md`, `second.png`, and a `vault` vault
  containing `hello.txt` (fixture password: `test-password`).
- `iOSUITests` scheme, `FullSyncUITests` — enabling the mirror, restoring it after
  relaunch, resuming from the background, and listing provider files. Requires
  `TEST_RUNNER_DD_TEST_SERVER` and `TEST_RUNNER_DD_TEST_TOKEN` for an isolated server
  with a configured sync folder.
- macOS `ProviderTests` and `SyncTests` schemes — shared File Provider and backups.
- `cd daemon && go test -race ./native ./internal/fullsync` — embedded engine.

Simulator validation does not replace testing background scheduling, device locking,
and edits from third-party apps on a physical iPhone.

Files action checks (2026-09-22): the removal menu and system API call passed for a
placeholder. After Quick Look, the simulator returned POSIX EBUSY because the preview
still held the file open, so full eviction of the downloaded copy did not pass.
The user confirmed that the removal action was visible on the device. Vault opening
from Files was removed at the user's request after a physical-device failure;
vaults inside DiscoDrive remain supported.
