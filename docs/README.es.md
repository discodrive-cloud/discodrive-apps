<p align="center">
  <img src="img/dd_logo.png" alt="DiscoDrive" width="160">
</p>

# DiscoDrive Apps

[English](../README.md) · [Deutsch](README.de.md) · [Українська](README.uk.md) · [Français](README.fr.md) · [**Español**](README.es.md) · [Русский](README.ru.md) · [Српски](README.sr.md)

**Las apps cliente de DiscoDrive — para todos tus dispositivos.** Un demonio de sincronización en segundo plano, un cliente de escritorio y apps móviles nativas en un solo repositorio.

Este es el repositorio de los clientes del [servidor DiscoDrive](https://github.com/kosmosoid/discodrive) — tu nube personal para archivos, calendarios, contactos, tareas, música y libros.

Multiplataforma: macOS, Windows, Linux y Android. Un único `Makefile` compila la variante adecuada para cada SO.

---

## Funciones

### 🔄 Demonio de sincronización

Sincronización bidireccional de carpetas en segundo plano, al estilo Dropbox: una carpeta local ↔ el servidor. Qué se sincroniza —una carpeta concreta o toda la raíz del almacenamiento— se configura **en el servidor**.

- **Headless** — funciona sin interfaz gráfica, ideal para servidores y NAS.
- **Barra de menús / bandeja** (compilación con `TRAY=1`) — icono de estado y acciones rápidas.
- **Caja fuerte E2E** — cifrado del lado del cliente en un formato compatible con [Cryptomator](https://cryptomator.org): los archivos se cifran en el dispositivo antes de enviarse.
- **Nunca pierdas datos** — si el mismo archivo cambia en dos sitios a la vez, se crea una copia de conflicto en lugar de sobrescribir en silencio.
- **Inicio automático al iniciar sesión.**

### 🖥️ Cliente de escritorio

Una app gráfica multiplataforma (macOS, Windows, Linux) con un modelo **bajo demanda**.

- **Explora todo el almacenamiento** — se ve todo el árbol de archivos.
- **Sube y descarga** archivos y carpetas, gestiona las cajas fuertes E2E desde la propia app.
- **Vincula con el servidor** en un par de clics.
- **Bandeja del sistema** e inicio automático.
- **7 idiomas de interfaz** — inglés, alemán, ucraniano, francés, español, ruso y serbio.

### 📱 Apps móviles

- **Clientes completos** — `android-discodrive` (Android) e `ios` (iOS): acceso bajo demanda a todo el almacenamiento.
- **Folder-sync** — `android-fastsync` e `ios-fastsync`: apps mínimas para la sincronización completa de una carpeta elegida.

---

## Builds listos para usar

Los binarios ya compilados para Linux, Windows y macOS (demonio y cliente de escritorio) y un `.apk` para Android se publican en la página de releases:

### 👉 [github.com/discodrive-cloud/discodrive-apps/releases](https://github.com/discodrive-cloud/discodrive-apps/releases)

- **macOS** (`.dmg`) y **Windows** (`.exe` + instalador) **no están firmados**. En el primer arranque, Gatekeeper (macOS) o SmartScreen (Windows) mostrarán una advertencia.
- **iOS** no se incluye en las releases (hay que compilarlo uno mismo e instalarlo en un iPhone mediante Xcode).

### 🍺 Homebrew (demonio — macOS y Linux)

El demonio de sincronización también puede instalarse desde nuestro tap de Homebrew:

```sh
brew tap discodrive-cloud/tap
brew trust discodrive-cloud/tap   # solo en versiones recientes de Homebrew
brew install discodrive-daemon
```

Después, empareja el demonio con tu servidor y activa el arranque automático:

```sh
discodrive pair --server https://tu-servidor.example
discodrive install    # o `discodrive run` para ejecutarlo en primer plano
```

---

## Compilar desde el código fuente

Si los builds listos no bastan o necesitas otra plataforma — compílalo tú mismo. `make doctor` muestra qué herramientas están instaladas y cuáles faltan.

### Qué necesitas

Los comandos y herramientas de abajo asumen **macOS como sistema anfitrión**: el demonio compila de forma cruzada en cualquier SO, mientras que el cliente de escritorio se compila en macOS — un `.dmg` nativo más Windows y Linux mediante compilación cruzada (Windows directamente, Linux en un contenedor Docker). En los propios Windows/Linux el conjunto de herramientas es distinto (ver la nota de abajo).

**Común (cualquier plataforma):**

- **Go 1.25+** — obligatorio para todo.
- **Node.js** — para compilar el frontend del cliente de escritorio.
- **Cadena de herramientas del cliente de escritorio** — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.

**Cliente de escritorio (anfitrión — macOS):**

- **makensis** (`brew install makensis`) — instaladores de Windows (NSIS), objetivo `desktop-windows`.
- **Docker** — compilación para Linux en un contenedor Debian con WebKitGTK, objetivo `desktop-linux`.

**Apps móviles:**

- **Xcode + XcodeGen** — apps de Apple (solo en macOS).
- **Android SDK + NDK + Gradle**, **gomobile** — Android.

> **Compilar fuera de macOS.** El demonio compila en todas partes, sin salvedades. El cliente de escritorio para Linux/Windows también puede compilarse de forma nativa — directamente con `wails build` y las dependencias de sistema de la plataforma (en Linux — paquetes de desarrollo de GTK 3 y WebKit2GTK; en Windows — WebView2); no hay objetivos `make` específicos para ello. El objetivo `desktop-linux` (Docker) funciona desde cualquier anfitrión.

### Demonio

```bash
make daemon                       # para el SO actual → dist/<os>-<arch>/discodrive
make daemon OS=linux ARCH=arm64   # compilación cruzada (Go puro, sin CGO)
make daemon TRAY=1                # con barra de menús / bandeja (CGO)
make daemon-all                   # todos los SO a la vez
```

### Cliente de escritorio

```bash
make desktop              # app de macOS (universal)
make dmg-desktop-macos    # macOS .dmg                  → dist/DiscoDrive-<versión>-macos.dmg
make desktop-windows      # Windows .exe + NSIS         → dist/windows/   (compilación cruzada desde macOS)
make desktop-linux        # Linux (vía Docker, cualquier anfitrión) → dist/linux/
```

### Apps móviles

```bash
make app-android            # Android, UI completa
make app-android-fastsync   # Android, folder-sync
make app-macos              # app de macOS (Xcode)
make app-ios                # app de iOS (Xcode)
make bind-ios               # binding gomobile solo para Apple
make bind-android           # binding gomobile solo para Android
```

Los artefactos de compilación van a `dist/`. Lista completa de objetivos — `make help`.

---

## Licencia y uso comercial

DiscoDrive es **source-available** bajo la licencia [PolyForm Noncommercial License 1.0.0](../LICENSE).

- ✅ **Gratis para cualquier uso no comercial** — autoalójalo para ti, tu familia, aficiones, estudios o experimentos. Para eso está hecho.
- ✅ **Modifícalo como quieras** — siempre que conserves el aviso de atribución obligatorio.
- ❌ **El uso comercial no está permitido.**

¿Necesitas uso comercial? Hay disponible una licencia comercial aparte — escribe a [info@discodrive.app](mailto:info@discodrive.app).

---

## ❤️ Apoya el proyecto

DiscoDrive lo crea y mantiene una sola persona. Los comentarios y sugerencias son bienvenidos — escribe a [info@discodrive.app](mailto:info@discodrive.app).

Y si te gusta DiscoDrive y quieres que siga creciendo — apoya el proyecto:

- **[Dale una estrella en GitHub](https://github.com/discodrive-cloud/discodrive-apps)** ⭐
- **[Ko-fi](https://ko-fi.com/kosmosoid)**
- **[Boosty](https://boosty.to/kosmosoid/donate)**
- **[Criptomonedas](https://nowpayments.io/donation?api_key=7P893T0-48B4KZH-Q6PXBBT-D79X9TK)** — a través de NOWPayments

¡Gracias! Cada aportación ayuda al proyecto a seguir adelante.
