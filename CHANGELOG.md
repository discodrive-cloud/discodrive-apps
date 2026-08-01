# Changelog

All notable changes to this project are documented in this file.

## Unreleased

## 0.0.4

### Added

- Uploads now carry each file's own modification date, so a photo taken in 2019 no
  longer arrives dated today. Servers that do not understand the date ignore it and
  keep dating uploads on arrival, as before.
- Chunked uploads from the desktop app declare the file's total size, letting the
  server reject an upload whose parts do not add up instead of publishing it as
  complete.

### Fixed

- Sync daemon, desktop and mobile apps: uploading a large file no longer holds the
  whole file in memory. Content is streamed from disk, so a multi-gigabyte upload no
  longer risks exhausting memory — most noticeably on phones.
- macOS and iOS apps: a file that could not be read was silently skipped during
  upload. The upload reported success and the file was simply missing from the
  server. Read failures are now reported.
- Uploads declare their exact length, so a transfer that ends early fails instead of
  quietly storing a truncated file that looks complete.
- Desktop app: profiles created by pre-0.0.3 versions (no server stamp in the index)
  are now reset on first open — such an index may hold a poisoned merge of two
  servers' trees and previously kept showing ghost folders after re-pairing.

## 0.0.3

### Fixed

- Sync daemon: renaming or moving a folder on the server no longer leaves an empty
  "ghost" copy of the old folder on disk that then got re-created on the server.
- Sync daemon: files moved or renamed locally are now sent to the server as a move,
  preserving file identity instead of deleting and re-uploading the content.
- Desktop app: logging out now clears all local state (index and cached files), so
  switching to another server no longer shows the old server's files and never
  uploads anything left over to the new server.
- Sync daemon: re-pairing to a different server resets local sync state and defaults
  to a fresh per-server sync folder; the old folder is left untouched on disk.
- Sync daemon: only one instance per profile can run at a time, and quitting from the
  tray menu is final — the macOS agent restarts the daemon only after a crash.
- Sync daemon: `run` and `tray` started from a terminal detach and release the
  console (use `--foreground` to keep it attached); service starts are unaffected.
- Desktop app: release builds now ship with the proper application icon.
- Sync daemon: the tray icon now uses the DiscoDrive app icon.

### Added

- Desktop app: a "Downloads" button in the file browser toolbar opens the folder
  with downloaded files.
- Releases now ship two daemon flavors: headless for servers and tray for desktops
  (`discodrive-daemon-<os>-<arch>-tray.tar.gz`).

## 0.0.2

### Security

- Hardening after an internal security audit: tightened validation of server-provided
  paths across the sync, desktop, and mobile file paths, and improved handling of the
  self-signed / insecure-TLS option.

## 0.0.1

First public release of DiscoDrive — a client for syncing with DiscoDrive server.

### Added

- Desktop app for macOS, Windows, and Linux.
- Sync daemon for darwin / linux / windows (amd64 and arm64).
- Android app (debug build).
