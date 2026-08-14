<p align="center">
  <img src="img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[English](../README.md) · [**Deutsch**](README.de.md) · [Українська](README.uk.md) · [Français](README.fr.md) · [Español](README.es.md) · [Русский](README.ru.md) · [Српски](README.sr.md)

**DiscoDrive-Client-Apps — für all deine Geräte.** Ein Hintergrund-Sync-Daemon, ein Desktop-Client und native Mobil-Apps in einem Repository.

Dies ist das Client-Repository für den [DiscoDrive-Server](https://github.com/kosmosoid/discodrive) — deine eigene private Cloud für Dateien, Kalender, Kontakte, Aufgaben, Musik und Bücher.

Plattformübergreifend: macOS, Windows, Linux und Android. Ein einziges `Makefile` baut die passende Variante für das jeweilige OS.

---

## Funktionen

### 🔄 Sync-Daemon

Bidirektionale Ordnersynchronisation im Hintergrund, Dropbox-Stil: ein lokaler Ordner ↔ Server. Was synchronisiert wird — ein einzelner Ordner oder der gesamte Speicher-Root — wird **auf dem Server** konfiguriert.

- **Headless** — läuft ohne grafische Oberfläche, ideal für Server und NAS.
- **Menüleiste / Tray** (Build mit `TRAY=1`) — Status-Icon und Schnellaktionen.
- **E2E-Tresor** — clientseitige Verschlüsselung in einem [Cryptomator](https://cryptomator.org)-kompatiblen Format: Dateien werden auf dem Gerät verschlüsselt, bevor sie gesendet werden.
- **Keine Datenverluste** — ändert sich dieselbe Datei an zwei Stellen gleichzeitig, wird eine Konfliktkopie erstellt, statt still zu überschreiben.
- **Autostart bei der Anmeldung.**

### 🖥️ Desktop-Client

Eine plattformübergreifende GUI-App (macOS, Windows, Linux) mit **On-Demand**-Modell.

- **Den gesamten Speicher durchsuchen** — der ganze Dateibaum ist sichtbar.
- **Hoch- und Herunterladen** von Dateien und Ordnern, Verwaltung von E2E-Tresoren direkt in der App.
- **Mit dem Server koppeln** in wenigen Klicks.
- **System-Tray** und Autostart.
- **7 Oberflächensprachen** — Englisch, Deutsch, Ukrainisch, Französisch, Spanisch, Russisch und Serbisch.

### 📱 Mobil-Apps

- **Vollständige Clients** — `android-discodrive` (Android) und `ios` (iOS): On-Demand-Zugriff auf den gesamten Speicher.
- **Folder-Sync** — `android-fastsync` und `ios-fastsync`: minimale Apps für die vollständige Synchronisation eines ausgewählten Ordners.

---

## Fertige Builds

Fertige Binärdateien für Linux, Windows und macOS (Daemon und Desktop-Client) sowie eine `.apk` für Android werden auf der Releases-Seite veröffentlicht:

### 👉 [github.com/discodrive-cloud/discodrive-apps/releases](https://github.com/discodrive-cloud/discodrive-apps/releases)

- **macOS** (`.dmg`) und **Windows** (`.exe` + Installer) sind **nicht signiert**. Beim ersten Start zeigen Gatekeeper (macOS) bzw. SmartScreen (Windows) eine Warnung.
- **iOS** ist nicht in den Releases enthalten (du musst es selbst bauen und per Xcode auf einem iPhone installieren).

### 🍺 Homebrew (Daemon — macOS und Linux)

Der Sync-Daemon lässt sich auch aus unserem Homebrew-Tap installieren:

```sh
brew tap kosmosoid/tap
brew trust kosmosoid/tap   # nur bei neueren Homebrew-Versionen nötig
brew install discodrive-daemon
```

Danach den Daemon mit dem Server koppeln und den Autostart aktivieren:

```sh
discodrive pair --server https://dein-server.example
discodrive install    # oder `discodrive run` für den Vordergrund
```

---

## Aus dem Quellcode bauen

Wenn die fertigen Builds nicht ausreichen oder du eine andere Plattform brauchst — baue selbst. `make doctor` zeigt, welche Werkzeuge installiert sind und welche fehlen.

### Was du brauchst

Die Befehle und Werkzeuge unten gehen von **macOS als Host-System** aus: der Daemon cross-kompiliert auf jedem OS, während der Desktop-Client auf macOS gebaut wird — eine native `.dmg` plus Windows und Linux per Cross-Build (Windows direkt, Linux in einem Docker-Container). Auf Windows/Linux selbst sieht der Werkzeugsatz anders aus (siehe Hinweis unten).

**Allgemein (jede Plattform):**

- **Go 1.25+** — für alles erforderlich.
- **Node.js** — zum Bauen des Frontends des Desktop-Clients.
- **Desktop-Client-Toolchain** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Desktop-Client (Host — macOS):**

- **makensis** (`brew install makensis`) — Windows-Installer (NSIS), Ziel `desktop-windows`.
- **Docker** — Linux-Build in einem Debian-Container mit WebKitGTK, Ziel `desktop-linux`.

**Mobil-Apps:**

- **Xcode + XcodeGen** — Apple-Apps (nur auf macOS).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Bauen außerhalb von macOS.** Der Daemon baut überall ohne Einschränkungen. Der Desktop-Client für Linux/Windows lässt sich auch nativ bauen — direkt über `wails build` mit den Systemabhängigkeiten der Plattform (unter Linux — GTK-3- und WebKit2GTK-Dev-Pakete; unter Windows — WebView2); dafür gibt es keine eigenen `make`-Ziele. Das Ziel `desktop-linux` (Docker) funktioniert von jedem Host aus.

### Daemon

```bash
make daemon                       # für das aktuelle OS → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # Cross-Kompilierung (reines Go, kein CGO)
make daemon TRAY=1                # mit Menüleiste / Tray (CGO)
make daemon-all                   # alle OS auf einmal
```

### Desktop-Client

```bash
make desktop              # macOS-App (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<Version>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (Cross-Build von macOS)
make desktop-linux        # Linux (via Docker, jeder Host) → dist/linux/
```

### Mobil-Apps

```bash
make app-android            # Android, volle UI
make app-android-fastsync   # Android, Folder-Sync
make app-macos              # macOS-App (Xcode)
make app-ios                # iOS-App (Xcode)
make bind-ios               # nur gomobile-Binding für Apple
make bind-android           # nur gomobile-Binding für Android
```

Build-Artefakte landen in `dist/`. Vollständige Liste der Ziele — `make help`.

---

## Lizenz & kommerzielle Nutzung

DiscoDrive ist **source-available** unter der [PolyForm Noncommercial License 1.0.0](../LICENSE).

- ✅ **Kostenlos für jede nicht-kommerzielle Nutzung** — nutze es für dich selbst, deine Familie, als Hobby, zum Lernen oder für Experimente. Genau dafür ist es gemacht.
- ✅ **Ändern, wie du willst** — solange du den vorgeschriebenen Urheberrechtshinweis beibehältst.
- ❌ **Kommerzielle Nutzung ist nicht erlaubt.**

Du brauchst kommerzielle Nutzung? Eine separate kommerzielle Lizenz ist verfügbar — schreib an [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).

---

DiscoDrive ist ein nicht-kommerzielles Projekt, das von einer einzigen Person erstellt wird. Rückmeldungen und Vorschläge sind willkommen: [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).
