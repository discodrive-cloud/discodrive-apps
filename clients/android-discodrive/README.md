# DiscoDrive (Android)

The Android client browses the complete storage through an offline index, downloads files
on demand and embeds the same Go folder-sync engine as the desktop clients. No separate
daemon is required.

## Build

1. From `daemon/`, run `gomobile bind -target=android -androidapi 21 -ldflags='-extldflags=-Wl,-z,max-page-size=16384' -o mobile/build/kfmobile.aar ./mobile`.
2. Copy `daemon/mobile/build/kfmobile.aar` to `clients/android-discodrive/app/libs/kfmobile.aar`.
3. Open this directory in Android Studio with the project's existing Gradle/AGP versions.
4. Build `assembleDebug` and run `testDebugUnitTest`, or select a device and Run.

Android 11 or later is required. Do not uninstall an existing app merely to update it.

## Files and vaults

- Pair with the server, then browse, upload, pin, rename, move or delete files.
- Browsing downloads live in the app's private storage. Old public `DiscoDrive` folders
  from earlier versions are left untouched; they are not imported automatically.
- The Android system document picker exposes a read-only DiscoDrive location. Editing,
  deletion and cache removal remain available inside the app. Document grants are bound
  to the pairing and cannot follow an account change.
- Ordinary files and vault files share a preview with Close and previous/next controls.
  Images, PDF pages and small text files preview internally; other types open in a compatible app.
- Vaults open inside DiscoDrive. File encryption/decryption streams through private temporary
  files; only small verified metadata is cached in memory. Closing removes plaintext previews.
- File menus provide version history, restoration and read-only link/email sharing with
  expiration and revocation. The browser menu opens the server Trash; permanent deletion
  and version replacement require confirmation.

## Folder synchronization

Enable **Folder synchronization** in Settings to mirror the server-selected scope into
`/storage/emulated/0/DiscoDriveSync`. Grant All files access for this feature or auto-upload;
ordinary browsing does not require it. KeePass and other apps can use files in this folder.

The first pass after a new pairing saves existing local contents separately and downloads
from the server. The old contents are never treated as new uploads. Later changes and
removals synchronize in both directions. Turning synchronization off leaves the folder in
place. Unpairing drains active operations and clears account indexes; the next pairing
starts with the server copy again. Large local deletion batches pause for confirmation.

WorkManager schedules background passes subject to Android's power and network policies.
The app listens for server changes while visible. The sync screen shows current activity,
completed operations, recent errors and the location of any saved previous contents.

## Diagnostics

Logging is off by default. **Save sync logs** writes rotating logs inside private app storage
(up to approximately 4 MB). **Share log** exports the current log through Android's share
sheet. Logs include paths, operation times and errors. Turning logging off stops writes and
retains existing logs. Never share a diagnostic log without checking its contents.

Themed launcher icons and new client features support the app's seven languages.
