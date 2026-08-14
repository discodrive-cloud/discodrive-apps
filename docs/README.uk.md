<p align="center">
  <img src="img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[English](../README.md) · [Deutsch](README.de.md) · [**Українська**](README.uk.md) · [Français](README.fr.md) · [Español](README.es.md) · [Русский](README.ru.md) · [Српски](README.sr.md)

**Клієнтські застосунки DiscoDrive — для всіх Ваших пристроїв.** Фоновий демон-синхронізатор, десктоп-клієнт і нативні мобільні застосунки в одному репозиторії.

Це репозиторій клієнтів до [сервера DiscoDrive](https://github.com/kosmosoid/discodrive) — Вашої особистої хмари для файлів, календарів, контактів, завдань, музики та книг.

Кросплатформність: macOS, Windows, Linux та Android. Один `Makefile` збирає потрібний варіант під потрібну ОС.

---

## Можливості

### 🔄 Демон-синхронізатор

Фонова двостороння синхронізація теки за моделлю «як Dropbox»: локальна тека ↔ сервер. Що саме синхронізувати — окрему теку чи весь корінь сховища — задається в налаштуваннях **на сервері**.

- **Headless** — працює без графічного інтерфейсу, ідеальний для серверів і NAS.
- **Менюбар / трей** (збірка з `TRAY=1`) — іконка стану та швидкі дії.
- **E2E-сейф** — клієнтське шифрування у [Cryptomator](https://cryptomator.org)-сумісному форматі: файли шифруються на пристрої перед надсиланням.
- **Захист від втрати даних** — якщо той самий файл змінюється у двох місцях одночасно, створюється конфліктна копія, а не мовчазний перезапис.
- **Автозапуск при вході в систему.**

### 🖥️ Десктоп-клієнт

Кросплатформний застосунок із графічним інтерфейсом (macOS, Windows, Linux) з моделлю **на вимогу**.

- **Перегляд усього сховища** — видно все дерево файлів.
- **Завантаження та вивантаження** файлів і тек, керування E2E-сейфами просто із застосунку.
- **Прив’язка до сервера** у пару кліків.
- **Системний трей** та автозапуск.
- **7 мов інтерфейсу** — англійська, німецька, українська, французька, іспанська, російська та сербська.

### 📱 Мобільні застосунки

- **Повні клієнти** — `android-discodrive` (Android) та `ios` (iOS): доступ на вимогу до всього сховища.
- **Folder-sync** — `android-fastsync` та `ios-fastsync`: мінімальні застосунки для повної синхронізації обраної теки.

---

## Готові збірки

Готові бінарні файли під Linux, Windows і macOS (демон і десктоп-клієнт) та `.apk` для Android публікуються на сторінці релізів:

### 👉 [github.com/discodrive-cloud/discodrive-apps/releases](https://github.com/discodrive-cloud/discodrive-apps/releases)

- **macOS** (`.dmg`) і **Windows** (`.exe` + інсталятор) **не підписані**. Під час першого запуску Gatekeeper (macOS) або SmartScreen (Windows) покажуть попередження.
- **iOS** до релізів не входить (потрібно зібрати самостійно та встановити на iPhone за допомогою Xcode).

### 🍺 Homebrew (демон — macOS і Linux)

Демон синхронізації можна також установити з нашого Homebrew tap:

```sh
brew tap discodrive-cloud/tap
brew trust discodrive-cloud/tap   # лише для свіжих версій Homebrew
brew install discodrive-daemon
```

Потім під’єднайте демон до сервера й увімкніть автозапуск:

```sh
discodrive pair --server https://your-server.example
discodrive install    # або `discodrive run` — запуск у консолі
```

---

## Збірка з джерел

Якщо готових збірок недостатньо або потрібна інша платформа — зберіть вручну. `make doctor` покаже, які інструменти встановлено, а яких бракує.

### Що потрібно

Команди та інструменти нижче розраховані на **macOS як хост-систему**: демон крос-компілюється на будь-якій ОС, а десктоп-клієнт збирається на macOS — нативний `.dmg` плюс Windows і Linux крос-збіркою (Windows — напряму, Linux — у Docker-контейнері). На самих Windows/Linux набір інструментів буде іншим (див. примітку нижче).

**Спільне (будь-яка платформа):**

- **Go 1.25+** — обов’язково для всього.
- **Node.js** — для збірки фронтенду десктоп-клієнта.
- **Тулчейн десктоп-клієнта** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Десктоп-клієнт (хост — macOS):**

- **makensis** (`brew install makensis`) — Windows-інсталятори (NSIS), ціль `desktop-windows`.
- **Docker** — збірка під Linux у Debian-контейнері з WebKitGTK, ціль `desktop-linux`.

**Мобільні застосунки:**

- **Xcode + XcodeGen** — застосунки Apple (лише на macOS).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Збірка не на macOS.** Демон збирається на будь-якій ОС без застережень. Десктоп-клієнт під Linux/Windows можна зібрати й нативно — напряму через `wails build` із системними залежностями платформи (на Linux — dev-пакети GTK 3 і WebKit2GTK; на Windows — WebView2); окремих `make`-цілей під це немає. Ціль `desktop-linux` (Docker) при цьому працює з будь-якого хоста.

### Демон

```bash
make daemon                       # під поточну ОС → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # крос-компіляція (чистий Go, без CGO)
make daemon TRAY=1                # з менюбаром / треєм (CGO)
make daemon-all                   # одразу під усі ОС
```

### Десктоп-клієнт

```bash
make desktop              # застосунок macOS (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<версія>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (крос-збірка з macOS)
make desktop-linux        # Linux (через Docker, будь-який хост) → dist/linux/
```

### Мобільні застосунки

```bash
make app-android            # Android, повний UI
make app-android-fastsync   # Android, folder-sync
make app-macos              # застосунок macOS (Xcode)
make app-ios                # застосунок iOS (Xcode)
make bind-ios               # лише gomobile-біндинг для Apple
make bind-android           # лише gomobile-біндинг для Android
```

Артефакти збірки складаються в `dist/`. Повний перелік цілей — `make help`.

---

## Ліцензія та комерційне використання

DiscoDrive поширюється як **source-available** за ліцензією [PolyForm Noncommercial License 1.0.0](../LICENSE).

- ✅ **Безкоштовно для будь-якого некомерційного використання** — користуйся для себе, родини, хобі, навчання чи експериментів. Заради цього все й зроблено.
- ✅ **Змінюй як завгодно** — за умови збереження обов'язкового зазначення авторства.
- ❌ **Комерційне використання заборонено.**

Потрібне комерційне використання? Окрема комерційна ліцензія доступна — напиши на [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).

---

DiscoDrive — некомерційний проєкт, який робить одна людина. Відгуки та пропозиції вітаються: [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).
