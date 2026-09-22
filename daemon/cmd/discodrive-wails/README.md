# Wails desktop client

The on-demand browser and the optional full folder mirror share the application's pairing,
while using separate clients and SQLite indexes. Full synchronization uses the existing Go
engine inside the application; no external daemon, sidecar executable or new dependency.

## Folder synchronization

In Settings, choose a dedicated local folder and enable synchronization. The switch stays
disabled until a folder is selected. Settings are stored atomically in the desktop profile's
`full-sync.json`, separately from theme/login preferences. Launch at login and Start minimized
continue to control application startup. Closing the window leaves sync running in the tray;
quitting cancels and drains it, while unpairing also disables its saved preference.

The server's existing account-wide scope applies (`X-Discodrive-Scope: 1`, `scope_epoch`).
Turning off the server's single-folder restriction means mirroring the **entire storage**.
The app does not change the server selection. The browser still shows the complete tree.

Before the first sync for an account/directory, existing files, including hidden directories,
are moved to a sibling `<folder>.old-<timestamp>-<suffix>` backup on the same volume.
The selected root keeps its identity. Backups are accessible from Settings. A failed move
prevents network synchronization and preserves both remaining originals and moved files.
Prepared mirrors resume without repeating the backup after a restart or interrupted download.

Mirror indexes live under `desktop/full-sync/<identity>/state.db`, isolated by account, path
and filesystem identity. Browser cache, profile ancestors/descendants, decrypted vault-session
folders and cloud-provider folders are rejected. Folder identity is checked before each pass:
a replacement or disconnected volume must not be interpreted as user deletions. Large local
deletions require explicit confirmation in Settings before they propagate to the server.

Only one synchronization owner should use a local folder. Stop an existing standalone daemon
before assigning its folder to the application. Its old state database is not imported.

The interface is translated into all seven supported languages. Windows/Linux use the same
manager with platform-specific filesystem identity; macOS Wails is not sandboxed and does
not need the native Swift application's security-scoped bookmark mechanism.

## Validation

From `daemon`:

```sh
go test -race ./internal/fullsync ./internal/syncer ./internal/engine ./cmd/discodrive-wails
```

From this directory: `wails build -platform darwin/universal`. The frontend can be built
with `npm run build` in `frontend`. Lifecycle, backup, account isolation and network tests
use temporary directories and local HTTP servers, never a user's paired account.

## macOS branding

The packaged application is `build/bin/DiscoDrive.app`. A macOS-only Wails post-build hook
compiles `clients/macos/Resources/DiscoDrive.icon` and `Assets.xcassets`, the same sources as
the Swift app, including the 18/36-pixel template tray images. This requires Xcode with
Icon Composer support (26+). The generated ICNS covers older macOS versions; current macOS
uses the layered asset catalog. Windows and Linux retain their existing platform assets.

The pinned systray v1.12.2 C main-thread setter is used directly on macOS because its public
icon setter forces 16 points. The adapter keeps the native 18-point glyph and existing tray
menu/lifecycle. Check this adapter when upgrading systray. The post-build hook restores an
ad-hoc signature after adding resources; `scripts/macos-sign.sh` applies Developer ID signing.

## Vault opening

Wails materializes the complete plaintext folder before opening the file manager. The
first open therefore transfers the entire vault over the network. Subsequent opens reuse
ciphertext from the profile's `vault-cache`, including ciphertext uploaded when closing.
The index is refreshed first and SHA-256 is checked against the server's current content
hash; missing, changed or corrupt entries are downloaded again. Plaintext and passwords
are never put in this cache. Completed cache entries are trimmed to 2 GiB after download
or upload, oldest first; temporary disk use can exceed that during an operation. Unpair
and server changes wipe the cache. Cache write failures fall back to uncached operation.

Local decryption still runs on every open. This is not an on-demand filesystem mount.

Closing compares plaintext contents with the opening snapshot (including same-size edits
with preserved timestamps), keeps unchanged ciphertext and directory IDs, and sends only
the ciphertext delta. Directory renames/moves change entry metadata only. File renames
reuse the encrypted bytes but currently upload that file at its new encrypted path.
Unchanged closes make no remote writes; local content verification still reads the files.

A staged close retains keys and plaintext until all writes/deletions succeed. Retries
reuse the staged ciphertext and recognize already committed hashes after a lost response.
Edits made during upload remain local for the next close. A changed remote tree stops the
save rather than silently replacing it; file writes also supply their base version. The
server does not provide a transaction for a whole vault or conditional deletes, so this
is not an atomic multi-client commit. Existing orphan storage is not garbage-collected.

After a successful save, plaintext is moved to a private `.closing-*` directory before
removal. Failed removal stays in the cleanup phase; retrying never derives a sync delta
from partially removed plaintext. Finder's `.DS_Store` is ignored when detecting edits.

## Recovery, sharing, and sync activity

Trash lists server-deleted files and folders. Restore returns an entry to the server;
permanent deletion and emptying the trash require confirmation. Version history is
available for ordinary files. Restoring a saved version replaces the server content,
propagates to synced devices, and preserves the previous content in history. The normal
index refresh marks any old downloaded copy stale; recovery never relabels old bytes as new.

Sharing supports read-only public links or access by email, with optional 1/7/30-day
expiration and revocation. A public link is shown when created; the server's list of
existing shares does not return its token.

Settings → Sync activity shows the current operation/path, the number of completed
operations, and up to 20 recent errors (one per path). Counts are session-local operations,
not unique files or a whole-storage percentage. History is in memory only and clears when
the sync engine stops; diagnostic logging is independent. Network failures and individual
file failures remain visible, and the engine retries according to its normal policy.
