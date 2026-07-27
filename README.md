<p align="center">
  <img src="docs/img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[**English**](README.md) · [Deutsch](docs/README.de.md) · [Українська](docs/README.uk.md) · [Français](docs/README.fr.md) · [Español](docs/README.es.md) · [Русский](docs/README.ru.md) · [Српски](docs/README.sr.md)

**DiscoDrive client apps — for all your devices.** A background sync daemon, a desktop client and native mobile apps in one repository.

This is the client repository for the [DiscoDrive server](https://github.com/kosmosoid/discodrive) — your own private cloud for files, calendars, contacts, tasks, music and books.

Cross-platform: macOS, Windows, Linux and Android. A single `Makefile` builds the right variant for the right OS.

---

## Features

### 🔄 Sync daemon

Background two-way folder sync, Dropbox-style: a local folder ↔ the server. What to sync — a single folder or the whole storage root — is configured **on the server**.

- **Headless** — runs without a GUI, ideal for servers and NAS.
- **Menu bar / tray** (build with `TRAY=1`) — status icon and quick actions.
- **E2E vault** — client-side encryption in a [Cryptomator](https://cryptomator.org)-compatible format: files are encrypted on the device before they are sent.
- **Never lose data** — if the same file changes in two places at once, a conflict copy is created instead of a silent overwrite.
- **Auto-start on login.**

### 🖥️ Desktop client

A cross-platform GUI app (macOS, Windows, Linux) with an **on-demand** model.

- **Browse the whole storage** — the entire file tree is visible.
- **Upload and download** files and folders, manage E2E vaults right from the app.
- **Pair with the server** in a couple of clicks.
- **System tray** and auto-start.
- **7 interface languages** — English, German, Ukrainian, French, Spanish, Russian and Serbian.

### 📱 Mobile apps

- **Full clients** — `android-discodrive` (Android) and `ios` (iOS): on-demand access to the whole storage.
- **Folder-sync** — `android-fastsync` and `ios-fastsync`: minimal apps for full sync of a chosen folder.

---

## Prebuilt releases

Prebuilt binaries for Linux, Windows and macOS (daemon and desktop client) and an `.apk` for Android are published on the releases page:

### 👉 [github.com/kosmosoid/discodrive-apps/releases](https://github.com/kosmosoid/discodrive-apps/releases)

- **macOS** (`.dmg`) and **Windows** (`.exe` + installer) are **unsigned**. On first launch Gatekeeper (macOS) or SmartScreen (Windows) will show a warning.
- **iOS** is not included in releases (you need to build it yourself and install it on an iPhone via Xcode).

### 🍺 Homebrew (daemon — macOS and Linux)

The sync daemon can also be installed from our Homebrew tap:

```sh
brew tap kosmosoid/tap
brew trust kosmosoid/tap   # newer Homebrew versions only
brew install discodrive-daemon
```

Then pair the daemon with your server and enable autostart:

```sh
discodrive pair --server https://your-server.example
discodrive install    # or `discodrive run` to run in the foreground
```

---

## Building from source

If the prebuilt binaries aren't enough or you need a different platform — build it yourself. `make doctor` shows which tools are installed and which are missing.

### What you need

The commands and tools below assume **macOS as the host system**: the daemon cross-compiles on any OS, while the desktop client is built on macOS — a native `.dmg` plus Windows and Linux via cross-build (Windows directly, Linux in a Docker container). On Windows/Linux themselves the toolset differs (see the note below).

**Common (any platform):**

- **Go 1.25+** — required for everything.
- **Node.js** — to build the desktop client's frontend.
- **Desktop client toolchain** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Desktop client (host — macOS):**

- **makensis** (`brew install makensis`) — Windows installers (NSIS), target `desktop-windows`.
- **Docker** — Linux build in a Debian container with WebKitGTK, target `desktop-linux`.

**Mobile apps:**

- **Xcode + XcodeGen** — Apple apps (macOS only).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Building off macOS.** The daemon builds anywhere, no caveats. The desktop client for Linux/Windows can also be built natively — directly via `wails build` with the platform's system dependencies (on Linux — GTK 3 and WebKit2GTK dev packages; on Windows — WebView2); there are no dedicated `make` targets for that. The `desktop-linux` target (Docker) works from any host.

### Daemon

```bash
make daemon                       # for the current OS → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # cross-compile (pure Go, no CGO)
make daemon TRAY=1                # with menu bar / tray (CGO)
make daemon-all                   # all OSes at once
```

### Desktop client

```bash
make desktop              # macOS app (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<version>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (cross-built from macOS)
make desktop-linux        # Linux (via Docker, any host) → dist/linux/
```

### Mobile apps

```bash
make app-android            # Android, full UI
make app-android-fastsync   # Android, folder-sync
make app-macos              # macOS app (Xcode)
make app-ios                # iOS app (Xcode)
make bind-ios               # gomobile binding for Apple only
make bind-android           # gomobile binding for Android only
```

Build artifacts go to `dist/`. Full list of targets — `make help`.

---

## License & commercial use

DiscoDrive is **source-available** under the [PolyForm Noncommercial License 1.0.0](LICENSE).

- ✅ **Free for any non-commercial use** — use it for yourself, your family, hobby, study or experiments. That's the whole point.
- ✅ **Modify it however you like** — as long as you keep the required attribution notice.
- ❌ **Commercial use is not allowed.**

Need commercial use? A separate commercial license is available — write to [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).

---

DiscoDrive is a non-commercial project made by one person. Feedback and suggestions are welcome: [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).
