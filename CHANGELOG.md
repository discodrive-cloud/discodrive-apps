# Changelog

All notable changes to this project are documented in this file.

## Unreleased

### Changed

- Mobile apps: the first sync after pairing is far quicker. Change-feed pages are
  applied to the local index in one transaction instead of a write per row, and the
  index runs in WAL mode — together roughly two orders of magnitude less disk work on
  a phone, where each write was a separate flush to storage.
- Android app: while that first sync runs there is now a line saying the file list is
  being fetched, instead of an empty list with only a thin progress bar.
- Android apps (both the full client and Fast Sync): the interface follows the phone's
  light/dark setting. In dark mode the pairing screen used to put near-black text on the
  system's dark background, and past it every screen stayed white regardless of the
  setting.

### Added

- A sync that would delete a large share of the synced files (more than a fifth, and at
  least ten) now stops and says so instead of sending the deletions. An emptied local
  folder — a drive that did not mount, a folder moved by hand, an app reinstalled onto a
  surviving index — is indistinguishable from files deleted on purpose, and the second
  reading costs everyone their data. Fast Sync then offers two ways out: download
  everything again, which rebuilds the local copy and touches nothing on the server, or
  confirm the deletions. The daemon takes `run -confirm-bulk-delete` for one pass.

### Fixed

- Unpairing on mobile now deletes the local index, which it never did. An index that
  outlived a pairing describes files the device no longer has — pair again after the sync
  folder is gone and every one of them is reported as deleted, which is exactly how a
  re-paired phone emptied a whole vault.
- Mobile apps: an index built against a different server is discarded on open rather than
  applied, matching what the desktop client has done for a while.
- Android apps: switching to another app no longer interrupts a sync. The work ran in the
  screen itself, and Android caches a process whose interface is gone — a cached process
  loses its network, DNS first, so a transfer died with "lookup <host>: no such host" the
  moment you looked at something else. Syncing now runs as a foreground service and keeps
  going. Allow notifications to watch its progress; it works either way.
- Files whose names contain characters the local filesystem rejects — `? : * " < > | \`,
  common in note titles — now sync. Android's storage and Windows refuse such names, so
  the file downloaded and then could not be put in place. They are stored under
  look-alike characters, and the client remembers the real name, so editing one still
  updates the right file on the server instead of creating a copy. On Windows the same
  applies to names it reserves for devices (`nul.md`, `con`, `com1`) and to names ending
  in a dot or a space. Filesystems that accept all of these — macOS, Linux — store every
  name exactly as it is on the server.
- A file whose name is too long for the filesystem now syncs under a shortened one
  instead of failing to be created. The limit is 255 either way, but Windows counts
  characters while macOS and Linux count bytes, so a Cyrillic title runs out of room at
  about 127 characters — well within reach of a note titled with a question. The
  extension is kept, and a short digest goes in so two long titles sharing a prefix stay
  separate files.
- One file that cannot be written no longer stops the sync. The pass used to give up at
  the first such file and, never getting past it, every later pass failed the same way —
  a phone could sit there having created every folder and not one file. The rest is now
  applied and the failures reported together; a change that failed is retried on the next
  pass rather than skipped. A pass that cannot reach the server still stops at once,
  instead of spending mobile data on downloads that cannot succeed.
- Mobile apps: a sync that failed while writing to disk reported "offline", which reads
  as a network problem. Those now report an error, with the reason.
- Android app: pairing could complete on the server — the confirmation mail arrived —
  while the app stayed on the pairing screen for good, and pairing again changed
  nothing. Whether the device counts as paired now follows the token the server
  issued, not whether the first pull that came after it succeeded.
- Android app: launching no longer starts on the pairing screen on the way to the
  file list. The list is shown straight from the local index, with the pull from the
  server running behind it — so a launch with no connection lands on the files
  instead of stalling on pairing, and the toolbar's refresh retries the pull.
- Android apps (both the full client and Fast Sync): a pairing left waiting for approval
  is no longer lost when the app is killed in the background — which is likely, since approving happens in a browser and
  may happen on another device. Reopening the app picks the same pairing up instead of
  showing an untouched pairing screen.
- Android app: re-pairing while an auto-upload pass or a refresh was still running
  failed with "sql: database is closed". Closing the shared index now waits for work
  in flight to finish.
- Android apps (both the full client and Fast Sync): the server address and device token
  from a pairing are written in one synchronous step, so an app killed straight
  afterwards no longer comes back half paired.
- Sync daemon, desktop and mobile apps: requests no longer wait forever on a
  connection that has silently died — which is what a phone's connections do while
  it is in the background. Connect and TLS setup are bounded, idle connections are
  probed, and pairing and change-feed requests carry deadlines. Whole transfers stay
  unbounded, so slow uploads and downloads are unaffected.

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
