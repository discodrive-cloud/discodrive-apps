<p align="center">
  <img src="img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[English](../README.md) · [Deutsch](README.de.md) · [Українська](README.uk.md) · [Français](README.fr.md) · [Español](README.es.md) · [**Русский**](README.ru.md) · [Српски](README.sr.md)

**Клиентские приложения DiscoDrive — для всех Ваших устройств.** Фоновый демон-синхронизатор, десктоп-клиент и нативные мобильные приложения в одном репозитории.

Это репозиторий клиентов к [серверу DiscoDrive](https://github.com/kosmosoid/discodrive) — Вашему личному облаку для файлов, календарей, контактов, задач, музыки и книг.

Кросс-платформенность: macOS, Windows, Linux и Android. Один `Makefile` собирает нужный вариант под нужную ОС.

---

## Возможности

### 🔄 Демон-синхронизатор

Фоновая двусторонняя синхронизация папки по модели "как Dropbox": локальная папка ↔ сервер. Что именно синхронизировать — отдельную папку или весь корень хранилища — задаётся в настройках **на сервере**.

- **Headless** — работает без графического интерфейса, идеален для серверов и NAS.
- **Меню-бар / трей** (сборка с `TRAY=1`) — иконка состояния и быстрые действия.
- **E2E-сейф** — клиентское шифрование на [Cryptomator](https://cryptomator.org)-совместимом формате: файлы шифруются на устройстве до отправки.
- **Защита от потери данных** — при одновременном изменении файла в двух местах создаётся конфликтная копия, а не молчаливая перезапись.
- **Автозапуск при входе в систему.**

### 🖥️ Десктоп-клиент

Кросс-платформенное GUI-приложение (macOS, Windows, Linux) с моделью **по-требованию**.

- **Просмотр всего хранилища** — видно всё дерево файлов.
- **Загрузка и выгрузка** файлов и папок, управление E2E-сейфами прямо из приложения.
- **Привязка к серверу** в пару кликов.
- **Системный трей** и автозапуск.
- **7 языков интерфейса** — английский, немецкий, украинский, французский, испанский, русский и сербский.

### 📱 Мобильные приложения

- **Полные клиенты** — `android-discodrive` (Android) и `ios` (iOS): on-demand-доступ ко всему хранилищу.
- **Folder-sync** — `android-fastsync` и `ios-fastsync`: минимальные приложения для полной синхронизации выбранной папки.

---

## Готовые сборки

Собранные бинарные файлы под Linux, Windows и macOS (демон и десктоп-клиент) и `.apk` для Android публикуются на странице релизов:

### 👉 [github.com/kosmosoid/discodrive-apps/releases](https://github.com/kosmosoid/discodrive-apps/releases)

- **macOS** (`.dmg`) и **Windows** (`.exe` + установщик) **не подписаны**. При первом запуске Gatekeeper (macOS) или SmartScreen (Windows) покажут предупреждение.
- **iOS** в релизы не входит (нужно собрать самостоятельно и установить на iPhone с помощью xcode).

### 🍺 Homebrew (демон — macOS и Linux)

Демон синхронизации можно также установить из нашего Homebrew tap:

```sh
brew tap kosmosoid/tap
brew trust kosmosoid/tap   # только для свежих версий Homebrew
brew install discodrive-daemon
```

Затем подключите демон к серверу и включите автозапуск:

```sh
discodrive pair --server https://your-server.example
discodrive install    # либо `discodrive run` — запуск в консоли
```

---

## Сборка из исходников

Если готовых сборок недостаточно или нужна другая платформа — соберите вручную. `make doctor` покажет, какие инструменты установлены, а каких не хватает.

### Что нужно

Команды и инструменты ниже рассчитаны на **macOS как хост-систему**: демон кросс-компилируется на любой ОС, а десктоп-клиент собирается на macOS — нативный `.dmg` плюс Windows и Linux кросс-сборкой (Windows — напрямую, Linux — в Docker-контейнере). На самих Windows/Linux набор инструментов будет другим (см. примечание ниже).

**Общее (любая платформа):**

- **Go 1.25+** — обязательно для всего.
- **Node.js** — для сборки фронтенда десктоп-клиента.
- **Тулчейн десктоп-клиента** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Десктоп-клиент (хост — macOS):**

- **makensis** (`brew install makensis`) — Windows-установщики (NSIS), цель `desktop-windows`.
- **Docker** — сборка под Linux в Debian-контейнере с WebKitGTK, цель `desktop-linux`.

**Мобильные приложения:**

- **Xcode + XcodeGen** — приложения Apple (только на macOS).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Сборка не на macOS.** Демон собирается на любой ОС без оговорок. Десктоп-клиент под Linux/Windows можно собрать и нативно — напрямую через `wails build` с системными зависимостями платформы (на Linux — dev-пакеты GTK 3 и WebKit2GTK; на Windows — WebView2); отдельных `make`-целей под это нет. Цель `desktop-linux` (Docker) при этом работает с любого хоста.

### Демон

```bash
make daemon                       # под текущую ОС → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # кросс-компиляция (чистый Go, без CGO)
make daemon TRAY=1                # с меню-баром/треем (CGO)
make daemon-all                   # сразу под все ОС
```

### Десктоп-клиент

```bash
make desktop              # macOS-приложение (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<версия>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (кросс-сборка с macOS)
make desktop-linux        # Linux (через Docker, любой хост) → dist/linux/
```

### Мобильные приложения

```bash
make app-android            # Android, полный UI
make app-android-fastsync   # Android, folder-sync
make app-macos              # macOS app (Xcode)
make app-ios                # iOS app (Xcode)
make bind-ios               # только gomobile-биндинг для Apple
make bind-android           # только gomobile-биндинг для Android
```

Артефакты сборки складываются в `dist/`. Полный список целей — `make help`.

---

## Лицензия и коммерческое использование

DiscoDrive распространяется как **source-available** под лицензией [PolyForm Noncommercial License 1.0.0](../LICENSE).

- ✅ **Бесплатно для любого некоммерческого использования** — используйте для себя, семьи, для хобби, учёбы или экспериментов. Ради этого всё и сделано.
- ✅ **Изменяйте как угодно** — при условии сохранения обязательного указания авторства.
- ❌ **Коммерческое использование запрещено.**

Нужно коммерческое использование? Доступна отдельная коммерческая лицензия — напишите на [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).

---

DiscoDrive — некоммерческий проект, который делает один человек. Отзывы и предложения приветствуются: [discodrive@kosmosoid.dev](mailto:discodrive@kosmosoid.dev).
