# Changelog

All notable changes to this project are documented in this file.

## Unreleased

### Added

- macOS app: a Cryptomator vault opens as its own place in Finder. "Open vault" in the
  context menu of a vault folder asks for the password once, and the vault appears in the
  sidebar as "🔓 <name>" with its files in the clear; creating, editing, renaming, moving
  and deleting inside it are encrypted on the way to the server. "Close vault" on any of
  its files takes it away again, and quitting the app closes every open vault. The keys
  live in the keychain only while a vault is open.

- macOS app: a breadcrumb bar above the file list, so a deep folder is one click from any
  of its parents instead of only from the root.

### Changed

- Vault opening: native apps read independent metadata in parallel; Wails downloads
  ciphertext with up to six requests at a time, draining workers before cleanup on failure.

- macOS and iOS apps: the window explains failures in plain words — an expired session, an
  unreachable server, a request the server refused — and keeps internal errors out of
  sight; every error goes to the system log under `org.discodrive.app` in full.

- macOS app: "Remove local copy" also removes the folders the download was placed in
  once they hold nothing but Finder's own housekeeping files.

- macOS app: logging out forgets the index and the download bookkeeping and sets the
  downloaded files aside under a dated name, so the next pairing starts from the server
  alone. The pairing is readable while the screen is locked, so the app no longer shows
  the pairing screen after a login with the screen still locked.

### Fixed

- macOS and iOS apps: outgoing account operations are cancelled and drained before
  replacing local state. Late downloads and imports cannot alter the next account's
  files, and stale results cannot repopulate its interface.
- macOS app: logout waits for the main Finder domain as well as vault domains to be
  removed, including registrations already in progress. A failed removal keeps the
  account signed in and explains the failure.
- macOS Finder extension: change pages are reduced to the last event per node, so a
  file restored during one delta is not reported as deleted afterwards. Remote-change
  notifications also reach every open vault domain.

- macOS vaults: long file and folder names claim the same encrypted name before a
  create, rename or move, so a late collision between different entry types keeps both
  originals. Failed conflict-copy removals are retried after restart; their server names
  no longer prevent listing the rest of a vault directory.
- macOS Finder extension: deleted vault-directory mappings remain available to lagging
  enumerators instead of being discarded when another enumerator advances its anchor.
- macOS app: quitting is cancelled with an explanation if vault domains cannot be
  closed. Spanish and German messages use formal address.

- macOS app: "database is locked" between the app and the Finder extension, which share
  the index. Writes now take their lock up front and resolve folder parents for the rows
  they touched rather than for the whole tree.

- macOS Finder extension: a request to "create" a file the server already has (the
  system sends those after a reimport) answers with the server's item instead of
  uploading an empty file over it.

- macOS Finder extension: the same inside an open vault, where such a request still wrote
  over the entry — an empty file over an existing one, or a fresh directory id over a
  folder, which put everything under the old one out of reach.

- macOS Finder extension: saving from Finder is guarded by the version the file was
  actually edited from, not by whatever the index held at the time. An edit of a version
  the server has since replaced is filed as a conflict copy next to the newer one instead
  of landing over it; so is an edit whose base cannot be told. A downloaded file is
  reported as the version its bytes are, checked by their hash. Because a file's version
  now travels with its contents, Finder may fetch a file again after a rename or a move.

- macOS Finder extension: files added or changed elsewhere show up inside an open vault;
  the lookup of which vault folder a change belongs to never matched. Files with long
  names there show their real size and follow remote edits.

- macOS app: importing from the local folder no longer sends downloaded copies back up.
  Every copy counted as new, and was uploaded over the server's file with no version
  check whenever the window came forward, so an old copy could replace another device's
  changes. Only files that are not downloads are imported now — a copy of a file since
  deleted or renamed on the server is still a copy — only after the index is up to date,
  and never over a name taken meanwhile: both files are kept.

- macOS and iOS apps: a pinned file stays pinned when a newer version of it is
  downloaded by opening it; "Free up space" used to remove it after that.

- macOS and iOS apps: a downloaded file removed from the local folder by hand is
  downloaded again when opened, instead of nothing happening.

- macOS and iOS apps: creating a vault under a name that is already taken is refused.
  It used to write new keys into the existing folder — over another vault's, whose
  contents then no longer opened. The new vault's files are also sent as "create only if
  absent", so a name taken a moment before fails the creation instead.

- macOS app: logging out closes the vaults open in Finder before anything else; their
  locations, keys and decrypted files used to stay, showing the old account's files. If a
  vault cannot be closed the logout does not happen, and the window says so.

- macOS Finder extension: a new file never replaces one another device put under the same
  name a moment earlier; the server keeps both. Renaming or moving a vault entry onto a
  taken name is refused and Finder asks what to do — a folder written over another used to
  leave everything under the old one out of reach. The refusal holds when the name is
  taken by another device at that very moment: the destination is written only if absent. A vault folder deleted elsewhere
  disappears from Finder.

- macOS app and Finder extension, which share the index: a page of changes fetched earlier
  and applied late no longer puts an older version back or revives a deleted file, and
  Finder is not handed a position past changes it was never told about.

- macOS and iOS apps: removing a folder named with `_` or `%` on the server no longer
  drops similarly named folders' files from the local list (`a_` took `ab/…` with it).

## 0.0.6

### Added

- macOS releases are signed with Developer ID and notarized by Apple: the desktop `.dmg`
  and both flavours of the daemon. The app opens straight away instead of needing
  "right-click → Open", and the desktop client is now a Homebrew cask as well —
  `brew install --cask discodrive-cloud/tap/discodrive`. Windows builds are still unsigned.

- Homebrew carries both flavours of the daemon: `discodrive-daemon` as before, and
  `discodrive-daemon-tray` for the desktop one with a menu bar icon. They install the same
  binary, so Homebrew asks you to remove one before installing the other. `discodrive tray`
  on a headless build now says where to get the other flavour rather than how to compile
  one.

### Added

- Android app: the folder chosen on the server can be kept on the phone from the app
  itself — "Folder sync" in the settings — so Fast Sync no longer has to run next to it.
  It is the same engine the desktop app runs, in `/sdcard/DiscoDriveSync`, two-way, with a
  pass in the foreground so leaving the app does not cut it off, a check every twenty
  minutes in the background, and the server's event stream held while the app is on
  screen so a change made elsewhere lands within seconds. The separate Fast Sync app is
  no longer released for Android; the iOS one stays until the Files app integration
  replaces it.

### Changed

- Every upload longer than 8 MiB now goes through the resumable chunked protocol: the
  daemon's and Fast Sync's sync passes, files picked by hand in the mobile and macOS
  browsers, and vault contents, the way auto-upload and the desktop client already did.
  A connection dropped halfway through a large video continues from where the server got
  to instead of starting over; the file's size is declared up front, so a short upload is
  refused rather than published truncated. The sync engines keep their conflict
  detection: the session carries the version the file was edited from, and the server
  files a conflict copy rather than overwriting a newer one (needs a server with that
  support; an older server answers the session request with an error and the pass
  reports it). Small files still go up in one request.

### Fixed

- After pairing, the server is the source of truth — on every client that mirrors a folder
  (the daemon, Fast Sync on Android and iOS). A folder that already held files when the
  device was paired used to be read as a folder full of new files, and the first pass
  uploaded all of it: re-pairing a laptop or a phone pushed its stale folder onto the
  server, every time. Now nothing goes up before the first download has come down, and
  whatever the folder held before the pairing is moved next to it (`<folder>.old-<date>`),
  untouched and never uploaded, while a clean folder is filled from the server. The
  daemon's `pair` starts from a clean index on every pairing, the same server included;
  "download everything again" after a stopped sync still keeps the folder in place.
- Desktop app: turning "open at login" off unregistered nothing — it deleted the login
  item's file and left the system still holding the registration for the rest of the
  session, pointing at an app that may since have moved. Turning it back on could then
  fail outright.

## 0.0.5

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
