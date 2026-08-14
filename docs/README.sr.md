<p align="center">
  <img src="img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[English](../README.md) · [Deutsch](README.de.md) · [Українська](README.uk.md) · [Français](README.fr.md) · [Español](README.es.md) · [Русский](README.ru.md) · [**Српски**](README.sr.md)

**DiscoDrive клијентске апликације — за све твоје уређаје.** Позадински демон за синхронизацију, десктоп клијент и нативне мобилне апликације у једном репозиторијуму.

Ово је репозиторијум клијената за [DiscoDrive сервер](https://github.com/kosmosoid/discodrive) — твој лични облак за фајлове, календаре, контакте, задатке, музику и књиге.

Вишеплатформски: macOS, Windows, Linux и Android. Један `Makefile` гради одговарајућу варијанту за одговарајући ОС.

---

## Могућности

### 🔄 Демон за синхронизацију

Позадинска двосмерна синхронизација фасцикле, у Dropbox стилу: локална фасцикла ↔ сервер. Шта се синхронизује — једна фасцикла или цео корен складишта — подешава се **на серверу**.

- **Headless** — ради без графичког интерфејса, идеалан за сервере и NAS.
- **Трака менија / треј** (билд са `TRAY=1`) — икона стања и брзе радње.
- **E2E сеф** — шифровање на страни клијента у формату компатибилном са [Cryptomator](https://cryptomator.org)-ом: фајлови се шифрују на уређају пре слања.
- **Без губитка података** — ако се исти фајл промени на два места истовремено, прави се конфликтна копија уместо тихог преписивања.
- **Аутоматско покретање при пријави.**

### 🖥️ Десктоп клијент

Вишеплатформска апликација са графичким интерфејсом (macOS, Windows, Linux) са моделом **на захтев**.

- **Преглед целог складишта** — види се цело стабло фајлова.
- **Отпремање и преузимање** фајлова и фасцикли, управљање E2E сефовима директно из апликације.
- **Упаривање са сервером** у пар кликова.
- **Системски треј** и аутоматско покретање.
- **7 језика интерфејса** — енглески, немачки, украјински, француски, шпански, руски и српски.

### 📱 Мобилне апликације

- **Потпуни клијенти** — `android-discodrive` (Android) и `ios` (iOS): приступ на захтев целом складишту.
- **Folder-sync** — `android-fastsync` и `ios-fastsync`: минималне апликације за потпуну синхронизацију изабране фасцикле.

---

## Готови билдови

Готове бинарне датотеке за Linux, Windows и macOS (демон и десктоп клијент) и `.apk` за Android објављују се на страници издања:

### 👉 [github.com/discodrive-cloud/discodrive-apps/releases](https://github.com/discodrive-cloud/discodrive-apps/releases)

- **macOS** (`.dmg`) и **Windows** (`.exe` + инсталер) **нису потписани**. При првом покретању, Gatekeeper (macOS) или SmartScreen (Windows) приказаће упозорење.
- **iOS** није укључен у издања (треба га сам изградити и инсталирати на iPhone помоћу Xcode-а).

### 🍺 Homebrew (демон — macOS и Linux)

Демон за синхронизацију може се инсталирати и из нашег Homebrew tap-а:

```sh
brew tap discodrive-cloud/tap
brew trust discodrive-cloud/tap   # само за новије верзије Homebrew-а
brew install discodrive-daemon
```

Затим упарите демон са сервером и укључите аутоматско покретање:

```sh
discodrive pair --server https://vas-server.example
discodrive install    # или `discodrive run` за рад у првом плану
```

---

## Изградња из изворног кода

Ако готови билдови нису довољни или ти треба друга платформа — изгради сам. `make doctor` показује који су алати инсталирани, а којих нема.

### Шта је потребно

Команде и алати испод подразумевају **macOS као хост систем**: демон се крос-компајлира на било ком ОС-у, док се десктоп клијент гради на macOS-у — нативни `.dmg` плус Windows и Linux крос-билдом (Windows директно, Linux у Docker контејнеру). На самим Windows/Linux системима скуп алата је другачији (види напомену испод).

**Заједничко (свака платформа):**

- **Go 1.25+** — обавезно за све.
- **Node.js** — за изградњу фронтенда десктоп клијента.
- **Алати десктоп клијента** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Десктоп клијент (хост — macOS):**

- **makensis** (`brew install makensis`) — Windows инсталери (NSIS), циљ `desktop-windows`.
- **Docker** — изградња за Linux у Debian контејнеру са WebKitGTK, циљ `desktop-linux`.

**Мобилне апликације:**

- **Xcode + XcodeGen** — Apple апликације (само на macOS-у).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Изградња ван macOS-а.** Демон се гради свуда, без ограничења. Десктоп клијент за Linux/Windows може се изградити и нативно — директно преко `wails build` са системским зависностима платформе (на Linux-у — dev пакети GTK 3 и WebKit2GTK; на Windows-у — WebView2); за то не постоје посебни `make` циљеви. Циљ `desktop-linux` (Docker) при том ради са било ког хоста.

### Демон

```bash
make daemon                       # за тренутни ОС → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # крос-компилација (чист Go, без CGO)
make daemon TRAY=1                # са траком менија / трејом (CGO)
make daemon-all                   # одмах за све ОС
```

### Десктоп клијент

```bash
make desktop              # macOS апликација (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<верзија>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (крос-билд са macOS-а)
make desktop-linux        # Linux (преко Docker-а, било који хост) → dist/linux/
```

### Мобилне апликације

```bash
make app-android            # Android, пун UI
make app-android-fastsync   # Android, folder-sync
make app-macos              # macOS апликација (Xcode)
make app-ios                # iOS апликација (Xcode)
make bind-ios               # само gomobile биндинг за Apple
make bind-android           # само gomobile биндинг за Android
```

Артефакти билда смештају се у `dist/`. Потпуна листа циљева — `make help`.

---

## Лиценца и комерцијална употреба

DiscoDrive се дистрибуира као **source-available** под лиценцом [PolyForm Noncommercial License 1.0.0](../LICENSE).

- ✅ **Бесплатно за сваку некомерцијалну употребу** — постави га за себе, породицу, хоби, учење или експерименте. Управо због тога је и направљен.
- ✅ **Мењај како год хоћеш** — уз услов да задржиш обавезно навођење ауторства.
- ❌ **Комерцијална употреба није дозвољена.**

Треба ти комерцијална употреба? Доступна је засебна комерцијална лиценца — пиши на [info@discodrive.app](mailto:info@discodrive.app).

---

## ❤️ Подржи пројекат

DiscoDrive се развија сопственим снагама, без инвестиција. Повратне информације и предлози су добродошли — пиши на [info@discodrive.app](mailto:info@discodrive.app).

А ако ти се DiscoDrive свиђа и желиш да расте и даље — подржи пројекат:

- **[Дај звездицу на GitHub-у](https://github.com/discodrive-cloud/discodrive-apps)** ⭐
- **[Ko-fi](https://ko-fi.com/kosmosoid)**
- **[Boosty](https://boosty.to/kosmosoid/donate)**
- **[Криптовалуте](https://nowpayments.io/donation?api_key=7P893T0-48B4KZH-Q6PXBBT-D79X9TK)** — преко NOWPayments

Хвала! Сваки допринос помаже пројекту да иде напред.
