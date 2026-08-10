// Package i18n provides a minimal, cgo-free localisation mechanism for the
// discodrive sync daemon. Supported languages: en (default), de, uk, fr, es, ru, sr.
package i18n

import (
	"sync/atomic"
	"unsafe"
)

// catalog maps message-id → per-language string.
// Inner key is the BCP-47 language tag; outer key is the message id.
var catalog = map[string]map[string]string{

	// ── CLI: usage / general ──────────────────────────────────────────────────

	"usage": {
		"en": "usage: discodrive <pair|run|tray|status|install|uninstall> [flags]",
		"ru": "использование: discodrive <pair|run|tray|status|install|uninstall> [флаги]",
		"uk": "використання: discodrive <pair|run|tray|status|install|uninstall> [прапори]",
		"fr": "utilisation: discodrive <pair|run|tray|status|install|uninstall> [options]",
		"es": "uso: discodrive <pair|run|tray|status|install|uninstall> [opciones]",
		"sr": "употреба: discodrive <pair|run|tray|status|install|uninstall> [заставице]",
		"de": "Verwendung: discodrive <pair|run|tray|status|install|uninstall> [Optionen]",
	},
	"unknown_command": {
		"en": "unknown command %q",
		"ru": "неизвестная команда %q",
		"uk": "невідома команда %q",
		"fr": "commande inconnue %q",
		"es": "comando desconocido %q",
		"sr": "непозната команда %q",
		"de": "unbekannter Befehl %q",
	},
	"error_prefix": {
		"en": "error: ",
		"ru": "ошибка: ",
		"uk": "помилка: ",
		"fr": "erreur : ",
		"es": "error: ",
		"sr": "грешка: ",
		"de": "Fehler: ",
	},

	// ── flag descriptions ─────────────────────────────────────────────────────

	"flag_server": {
		"en": "server address, e.g. https://files.example.com",
		"ru": "адрес сервера, напр. https://files.example.com",
		"uk": "адреса сервера, напр. https://files.example.com",
		"fr": "adresse du serveur, ex. https://files.example.com",
		"es": "dirección del servidor, p.ej. https://files.example.com",
		"sr": "адреса сервера, нпр. https://files.example.com",
		"de": "Serveradresse, z. B. https://files.example.com",
	},
	"flag_name": {
		"en": "device name",
		"ru": "имя устройства",
		"uk": "ім'я пристрою",
		"fr": "nom de l'appareil",
		"es": "nombre del dispositivo",
		"sr": "naziv уређаја",
		"de": "Gerätename",
	},
	"flag_dir": {
		"en": "local sync folder",
		"ru": "локальная папка синка",
		"uk": "локальна тека синхронізації",
		"fr": "dossier de synchronisation local",
		"es": "carpeta de sincronización local",
		"sr": "локална фасцикла за синхронизацију",
		"de": "lokaler Sync-Ordner",
	},
	"flag_config": {
		"en": "path to config file",
		"ru": "путь к конфигу",
		"uk": "шлях до файлу конфігурації",
		"fr": "chemin du fichier de configuration",
		"es": "ruta al archivo de configuración",
		"sr": "путања до конфигурационог фајла",
		"de": "Pfad zur Konfigurationsdatei",
	},
	"flag_detach": {
		"en": "run in background, detach from console (log written to file)",
		"ru": "запустить в фоне, отпустив консоль (лог пишется в файл)",
		"uk": "запустити у фоні, відключившись від консолі (лог записується у файл)",
		"fr": "exécuter en arrière-plan, détacher de la console (log écrit dans un fichier)",
		"es": "ejecutar en segundo plano, desconectar de la consola (log escrito en archivo)",
		"sr": "покренути у позадини, одвојити од конзоле (лог се пише у фајл)",
		"de": "im Hintergrund ausführen, von der Konsole lösen (Log wird in Datei geschrieben)",
	},
	"flag_confirm_bulk_delete": {
		"en": "allow one pass to delete a large share of the synced files (the guard stops it otherwise)",
		"ru": "разрешить одному проходу удалить большую часть синхронизированных файлов (иначе защита его остановит)",
		"uk": "дозволити одному проходу видалити більшу частину синхронізованих файлів (інакше захист його зупинить)",
		"fr": "autoriser un passage à supprimer une grande partie des fichiers synchronisés (sinon la protection l'arrête)",
		"es": "permitir que una pasada elimine una gran parte de los archivos sincronizados (si no, la protección la detiene)",
		"sr": "дозволити једном пролазу да обрише велики део синхронизованих фајлова (иначе га заштита зауставља)",
		"de": "einem Durchlauf erlauben, einen großen Teil der synchronisierten Dateien zu löschen (sonst stoppt die Schutzprüfung ihn)",
	},
	"flag_foreground": {
		"en": "stay in the foreground (default when not started from a console)",
		"ru": "остаться на переднем плане (по умолчанию при запуске не из консоли)",
		"uk": "залишитися на передньому плані (типово при запуску не з консолі)",
		"fr": "rester au premier plan (par défaut hors lancement depuis une console)",
		"es": "permanecer en primer plano (por defecto si no se inicia desde una consola)",
		"sr": "остати у првом плану (подразумевано када се не покреће из конзоле)",
		"de": "im Vordergrund bleiben (Standard, wenn nicht von einer Konsole gestartet)",
	},

	// ── pair command ──────────────────────────────────────────────────────────

	"pair_need_server": {
		"en": "specify --server",
		"ru": "укажите --server",
		"uk": "вкажіть --server",
		"fr": "spécifiez --server",
		"es": "especifique --server",
		"sr": "наведите --server",
		"de": "geben Sie --server an",
	},
	"already_running": {
		"en": "discodrive is already running for this profile — nothing to do",
		"ru": "discodrive уже запущен для этого профиля — выходим",
		"uk": "discodrive вже запущено для цього профілю — виходимо",
		"fr": "discodrive est déjà en cours d'exécution pour ce profil — rien à faire",
		"es": "discodrive ya está en ejecución para este perfil — nada que hacer",
		"sr": "discodrive је већ покренут за овај профил — нема шта да се ради",
		"de": "discodrive läuft für dieses Profil bereits — nichts zu tun",
	},
	"lock_error": {
		"en": "acquiring instance lock: %v",
		"ru": "получение блокировки экземпляра: %v",
		"uk": "отримання блокування екземпляра: %v",
		"fr": "acquisition du verrou d'instance : %v",
		"es": "adquisición del bloqueo de instancia: %v",
		"sr": "преузимање закључавања инстанце: %v",
		"de": "Instanzsperre anfordern: %v",
	},
	"pair_server_changed": {
		"en": "server changed: local sync state was reset; syncing into %s (the old folder is left untouched)\n",
		"ru": "сервер изменился: локальное состояние синхронизации сброшено; синхронизация в %s (старая папка не тронута)\n",
		"uk": "сервер змінився: локальний стан синхронізації скинуто; синхронізація в %s (стара папка не зачеплена)\n",
		"fr": "serveur modifié : l'état de synchronisation local a été réinitialisé ; synchronisation dans %s (l'ancien dossier reste intact)\n",
		"es": "servidor cambiado: el estado de sincronización local se restableció; sincronizando en %s (la carpeta antigua queda intacta)\n",
		"sr": "сервер је промењен: локално стање синхронизације је ресетовано; синхронизација у %s (стара фасцикла остаје нетакнута)\n",
		"de": "Server geändert: lokaler Sync-Zustand wurde zurückgesetzt; Synchronisation in %s (der alte Ordner bleibt unberührt)\n",
	},
	"pair_reset_error": {
		"en": "resetting local sync state: %v",
		"ru": "сброс локального состояния синхронизации: %v",
		"uk": "скидання локального стану синхронізації: %v",
		"fr": "réinitialisation de l'état de synchronisation local : %v",
		"es": "restablecimiento del estado de sincronización local: %v",
		"sr": "ресетовање локалног стања синхронизације: %v",
		"de": "Zurücksetzen des lokalen Sync-Zustands: %v",
	},
	"pair_init_error": {
		"en": "pairing init: %v",
		"ru": "инициация привязки: %v",
		"uk": "ініціація прив'язки: %v",
		"fr": "initialisation du jumelage : %v",
		"es": "inicio de emparejamiento: %v",
		"sr": "иницијализација упаривања: %v",
		"de": "Kopplungs-Initialisierung: %v",
	},
	"pair_open_browser": {
		"en": "Open in browser and confirm pairing of %q:\n  %s\n  (code: %s)\n",
		"ru": "Открой в браузере и подтверди привязку «%s»:\n  %s\n  (код: %s)\n",
		"uk": "Відкрий у браузері та підтверди прив'язку «%s»:\n  %s\n  (код: %s)\n",
		"fr": "Ouvrez dans le navigateur et confirmez le jumelage de « %s » :\n  %s\n  (code : %s)\n",
		"es": "Abre en el navegador y confirma el emparejamiento de «%s»:\n  %s\n  (código: %s)\n",
		"sr": "Отвори у претраживачу и потврди упаривање «%s»:\n  %s\n  (код: %s)\n",
		"de": "Im Browser öffnen und Kopplung von %q bestätigen:\n  %s\n  (Code: %s)\n",
	},
	"pair_wait_error": {
		"en": "waiting for confirmation: %v",
		"ru": "ожидание подтверждения: %v",
		"uk": "очікування підтвердження: %v",
		"fr": "attente de confirmation : %v",
		"es": "esperando confirmación: %v",
		"sr": "чекање потврде: %v",
		"de": "Warten auf Bestätigung: %v",
	},
	"pair_save_error": {
		"en": "saving config: %v",
		"ru": "сохранение конфига: %v",
		"uk": "збереження конфігурації: %v",
		"fr": "enregistrement de la configuration : %v",
		"es": "guardando configuración: %v",
		"sr": "чување конфигурације: %v",
		"de": "Konfiguration speichern: %v",
	},
	"pair_done": {
		"en": "Done. Device paired, config: %s\nSync folder: %s\nRun: discodrive run\n",
		"ru": "Готово. Устройство привязано, конфиг: %s\nПапка синка: %s\nЗапусти: discodrive run\n",
		"uk": "Готово. Пристрій прив'язано, конфіг: %s\nТека синхронізації: %s\nЗапусти: discodrive run\n",
		"fr": "Terminé. Appareil jumelé, configuration : %s\nDossier de sync : %s\nLancez : discodrive run\n",
		"es": "Listo. Dispositivo emparejado, configuración: %s\nCarpeta de sync: %s\nEjecuta: discodrive run\n",
		"sr": "Готово. Уређај упарен, конфиг: %s\nФасцикла за синхронизацију: %s\nПокрени: discodrive run\n",
		"de": "Fertig. Gerät gekoppelt, Konfiguration: %s\nSync-Ordner: %s\nAusführen: discodrive run\n",
	},

	// ── run command ───────────────────────────────────────────────────────────

	"run_init_error": {
		"en": "syncer init: %v",
		"ru": "инициализация синкера: %v",
		"uk": "ініціалізація синкера: %v",
		"fr": "initialisation du syncer : %v",
		"es": "inicialización del sincronizador: %v",
		"sr": "иницијализација синкера: %v",
		"de": "Syncer-Initialisierung: %v",
	},
	"run_syncing": {
		"en": "discodrive: syncing %s ↔ %s\n",
		"ru": "discodrive: синкаю %s ↔ %s\n",
		"uk": "discodrive: синхронізую %s ↔ %s\n",
		"fr": "discodrive: synchronisation de %s ↔ %s\n",
		"es": "discodrive: sincronizando %s ↔ %s\n",
		"sr": "discodrive: синхронизујем %s ↔ %s\n",
		"de": "discodrive: synchronisiere %s ↔ %s\n",
	},
	"run_sync_error": {
		"en": "sync: %v",
		"ru": "синк: %v",
		"uk": "синхронізація: %v",
		"fr": "synchronisation : %v",
		"es": "sincronización: %v",
		"sr": "синхронизација: %v",
		"de": "Sync: %v",
	},

	// ── buildSyncer errors ────────────────────────────────────────────────────

	"build_load_config": {
		"en": "loading config (%s): %w — run `discodrive pair` first",
		"ru": "загрузка конфига (%s): %w — сначала `discodrive pair`",
		"uk": "завантаження конфігурації (%s): %w — спочатку `discodrive pair`",
		"fr": "chargement de la configuration (%s) : %w — exécutez d'abord `discodrive pair`",
		"es": "cargando configuración (%s): %w — ejecuta `discodrive pair` primero",
		"sr": "учитавање конфигурације (%s): %w — прво покрените `discodrive pair`",
		"de": "Konfiguration laden (%s): %w — führen Sie zuerst `discodrive pair` aus",
	},
	"build_mkdir": {
		"en": "creating sync folder: %w",
		"ru": "создание папки синка: %w",
		"uk": "створення теки синхронізації: %w",
		"fr": "création du dossier de sync : %w",
		"es": "creando carpeta de sync: %w",
		"sr": "креирање фасцикле за синхронизацију: %w",
		"de": "Sync-Ordner erstellen: %w",
	},
	"build_open_index": {
		"en": "opening index: %w",
		"ru": "открытие индекса: %w",
		"uk": "відкриття індексу: %w",
		"fr": "ouverture de l'index : %w",
		"es": "abriendo índice: %w",
		"sr": "отварање индекса: %w",
		"de": "Index öffnen: %w",
	},

	// ── status command ────────────────────────────────────────────────────────

	"status_not_running": {
		"en": "daemon not running",
		"ru": "демон не запущен",
		"uk": "демон не запущено",
		"fr": "démon non démarré",
		"es": "el demonio no está en ejecución",
		"sr": "демон није покренут",
		"de": "Daemon läuft nicht",
	},
	"status_read_error": {
		"en": "error reading status: %v",
		"ru": "ошибка чтения статуса: %v",
		"uk": "помилка читання статусу: %v",
		"fr": "erreur de lecture du statut : %v",
		"es": "error al leer el estado: %v",
		"sr": "грешка при читању статуса: %v",
		"de": "Fehler beim Lesen des Status: %v",
	},
	"status_synced_never": {
		"en": "Status: synced (no sync performed yet)",
		"ru": "Статус: синхронизировано (синк ещё не выполнялся)",
		"uk": "Статус: синхронізовано (синхронізацію ще не виконувалась)",
		"fr": "État : synchronisé (aucune synchronisation effectuée)",
		"es": "Estado: sincronizado (aún no se realizó ninguna sync)",
		"sr": "Статус: синхронизовано (синхронизација још није обављена)",
		"de": "Status: synchronisiert (noch keine Synchronisierung durchgeführt)",
	},
	"status_synced": {
		"en": "Status: synced, last sync: %s",
		"ru": "Статус: синхронизировано, последний синк: %s",
		"uk": "Статус: синхронізовано, остання синхронізація: %s",
		"fr": "État : synchronisé, dernière sync : %s",
		"es": "Estado: sincronizado, última sync: %s",
		"sr": "Статус: синхронизовано, последња синхронизација: %s",
		"de": "Status: synchronisiert, letzte Synchronisierung: %s",
	},
	"status_syncing": {
		"en": "Status: syncing…",
		"ru": "Статус: синхронизирую…",
		"uk": "Статус: синхронізую…",
		"fr": "État : synchronisation en cours…",
		"es": "Estado: sincronizando…",
		"sr": "Статус: синхронизујем…",
		"de": "Status: synchronisiere…",
	},
	"status_offline": {
		"en": "Status: offline, error: %s",
		"ru": "Статус: офлайн, ошибка: %s",
		"uk": "Статус: офлайн, помилка: %s",
		"fr": "État : hors ligne, erreur : %s",
		"es": "Estado: sin conexión, error: %s",
		"sr": "Статус: ван мреже, грешка: %s",
		"de": "Status: offline, Fehler: %s",
	},
	"status_offline_no_error": {
		"en": "Status: offline",
		"ru": "Статус: офлайн",
		"uk": "Статус: офлайн",
		"fr": "État : hors ligne",
		"es": "Estado: sin conexión",
		"sr": "Статус: ван мреже",
		"de": "Status: offline",
	},
	"status_unknown": {
		"en": "Status: %s",
		"ru": "Статус: %s",
		"uk": "Статус: %s",
		"fr": "État : %s",
		"es": "Estado: %s",
		"sr": "Статус: %s",
		"de": "Status: %s",
	},
	"status_pid_updated": {
		"en": "PID: %d  |  updated: %s",
		"ru": "PID: %d  |  обновлено: %s",
		"uk": "PID: %d  |  оновлено: %s",
		"fr": "PID : %d  |  mis à jour : %s",
		"es": "PID: %d  |  actualizado: %s",
		"sr": "PID: %d  |  ажурирано: %s",
		"de": "PID: %d  |  aktualisiert: %s",
	},

	// ── install / uninstall ───────────────────────────────────────────────────

	"install_no_exe": {
		"en": "couldn't find own binary: %v",
		"ru": "не нашёл свой бинарь: %v",
		"uk": "не знайшов власний бінарник: %v",
		"fr": "impossible de trouver le binaire : %v",
		"es": "no encontré el binario propio: %v",
		"sr": "нисам нашао свој бинарни фајл: %v",
		"de": "eigene Binärdatei nicht gefunden: %v",
	},
	"install_error": {
		"en": "installing autostart: %v",
		"ru": "установка автозапуска: %v",
		"uk": "встановлення автозапуску: %v",
		"fr": "installation du démarrage automatique : %v",
		"es": "instalando inicio automático: %v",
		"sr": "инсталација аутопокретања: %v",
		"de": "Autostart installieren: %v",
	},
	"uninstall_error": {
		"en": "removing autostart: %v",
		"ru": "удаление автозапуска: %v",
		"uk": "видалення автозапуску: %v",
		"fr": "suppression du démarrage automatique : %v",
		"es": "eliminando inicio automático: %v",
		"sr": "уклањање аутопокретања: %v",
		"de": "Autostart entfernen: %v",
	},
	"autostart_installed": {
		"en": "autostart installed (%s)",
		"ru": "автозапуск установлен (%s)",
		"uk": "автозапуск встановлено (%s)",
		"fr": "démarrage automatique installé (%s)",
		"es": "inicio automático instalado (%s)",
		"sr": "аутопокретање инсталирано (%s)",
		"de": "Autostart installiert (%s)",
	},
	"autostart_removed": {
		"en": "autostart removed",
		"ru": "автозапуск удалён",
		"uk": "автозапуск видалено",
		"fr": "démarrage automatique supprimé",
		"es": "inicio automático eliminado",
		"sr": "аутопокретање уклоњено",
		"de": "Autostart entfernt",
	},
	"autostart_not_supported": {
		"en": "autostart not supported on this platform",
		"ru": "автозапуск не поддерживается на этой платформе",
		"uk": "автозапуск не підтримується на цій платформі",
		"fr": "démarrage automatique non pris en charge sur cette plateforme",
		"es": "inicio automático no soportado en esta plataforma",
		"sr": "аутопокретање није подржано на овој платформи",
		"de": "Autostart wird auf dieser Plattform nicht unterstützt",
	},

	// ── detach ────────────────────────────────────────────────────────────────

	"detach_no_exe": {
		"en": "couldn't find own binary: %v",
		"ru": "не нашёл свой бинарь: %v",
		"uk": "не знайшов власний бінарник: %v",
		"fr": "impossible de trouver le binaire : %v",
		"es": "no encontré el binario propio: %v",
		"sr": "нисам нашао свој бинарни фајл: %v",
		"de": "eigene Binärdatei nicht gefunden: %v",
	},
	"detach_log_error": {
		"en": "log file (%s): %v",
		"ru": "лог-файл (%s): %v",
		"uk": "файл журналу (%s): %v",
		"fr": "fichier journal (%s) : %v",
		"es": "archivo de log (%s): %v",
		"sr": "лог фајл (%s): %v",
		"de": "Log-Datei (%s): %v",
	},
	"detach_start_error": {
		"en": "starting in background: %v",
		"ru": "запуск в фоне: %v",
		"uk": "запуск у фоні: %v",
		"fr": "lancement en arrière-plan : %v",
		"es": "iniciando en segundo plano: %v",
		"sr": "покретање у позадини: %v",
		"de": "Start im Hintergrund: %v",
	},
	"detach_started": {
		"en": "daemon started in background (pid %d)\nlog:  %s\nstop: kill %d\n",
		"ru": "демон запущен в фоне (pid %d)\nлог:       %s\nостановить: kill %d\n",
		"uk": "демон запущено у фоні (pid %d)\nжурнал:     %s\nзупинити:   kill %d\n",
		"fr": "démon démarré en arrière-plan (pid %d)\nlog :       %s\narrêter :   kill %d\n",
		"es": "demonio iniciado en segundo plano (pid %d)\nlog:        %s\ndetener:    kill %d\n",
		"sr": "демон покренут у позадини (pid %d)\nлог:        %s\nзауставити: kill %d\n",
		"de": "Daemon im Hintergrund gestartet (pid %d)\nLog:        %s\nstoppen:    kill %d\n",
	},

	// ── tray menu ─────────────────────────────────────────────────────────────

	"tray_init_error": {
		"en": "syncer init: %v",
		"ru": "инициализация синкера: %v",
		"uk": "ініціалізація синкера: %v",
		"fr": "initialisation du syncer : %v",
		"es": "inicialización del sincronizador: %v",
		"sr": "иницијализација синкера: %v",
		"de": "Syncer-Initialisierung: %v",
	},
	"tray_status_starting": {
		"en": "Status: starting…",
		"ru": "Статус: запуск…",
		"uk": "Статус: запуск…",
		"fr": "État : démarrage…",
		"es": "Estado: iniciando…",
		"sr": "Статус: покретање…",
		"de": "Status: Start…",
	},
	"tray_status_synced": {
		"en": "Status: synced",
		"ru": "Статус: синхронизировано",
		"uk": "Статус: синхронізовано",
		"fr": "État : synchronisé",
		"es": "Estado: sincronizado",
		"sr": "Статус: синхронизовано",
		"de": "Status: synchronisiert",
	},
	"tray_status_syncing": {
		"en": "Status: syncing…",
		"ru": "Статус: синхронизирую…",
		"uk": "Статус: синхронізую…",
		"fr": "État : synchronisation en cours…",
		"es": "Estado: sincronizando…",
		"sr": "Статус: синхронизујем…",
		"de": "Status: synchronisiere…",
	},
	"tray_status_offline": {
		"en": "Status: offline",
		"ru": "Статус: офлайн",
		"uk": "Статус: офлайн",
		"fr": "État : hors ligne",
		"es": "Estado: sin conexión",
		"sr": "Статус: ван мреже",
		"de": "Status: offline",
	},
	"tray_open_folder": {
		"en": "Open sync folder",
		"ru": "Открыть папку синка",
		"uk": "Відкрити теку синхронізації",
		"fr": "Ouvrir le dossier de sync",
		"es": "Abrir carpeta de sync",
		"sr": "Отвори фасциклу за синхронизацију",
		"de": "Sync-Ordner öffnen",
	},
	"tray_open_folder_tooltip": {
		"en": "Open local sync folder",
		"ru": "Открыть локальную папку синхронизации",
		"uk": "Відкрити локальну теку синхронізації",
		"fr": "Ouvrir le dossier de synchronisation local",
		"es": "Abrir carpeta de sincronización local",
		"sr": "Отвори локалну фасциклу за синхронизацију",
		"de": "Lokalen Sync-Ordner öffnen",
	},
	"tray_quit": {
		"en": "Quit",
		"ru": "Выход",
		"uk": "Вихід",
		"fr": "Quitter",
		"es": "Salir",
		"sr": "Излаз",
		"de": "Beenden",
	},
	"tray_quit_tooltip": {
		"en": "Stop daemon and quit",
		"ru": "Остановить демон и выйти",
		"uk": "Зупинити демон і вийти",
		"fr": "Arrêter le démon et quitter",
		"es": "Detener el demonio y salir",
		"sr": "Заустави демон и изађи",
		"de": "Daemon stoppen und beenden",
	},
	"tray_status_tooltip": {
		"en": "Sync status",
		"ru": "Статус синхронизации",
		"uk": "Статус синхронізації",
		"fr": "État de la synchronisation",
		"es": "Estado de sincronización",
		"sr": "Статус синхронизације",
		"de": "Sync-Status",
	},
	"tray_vaults_header": {
		"en": "Vaults",
		"ru": "Сейфы",
		"uk": "Сейфи",
		"fr": "Coffres",
		"es": "Cajas fuertes",
		"sr": "Трезори",
		"de": "Tresore",
	},
	"tray_vault_close": {
		"en": "%s: close",
		"ru": "%s: закрыть",
		"uk": "%s: закрити",
		"fr": "%s : fermer",
		"es": "%s: cerrar",
		"sr": "%s: затвори",
		"de": "%s: schließen",
	},
	"tray_vault_open": {
		"en": "%s: open",
		"ru": "%s: открыть",
		"uk": "%s: відкрити",
		"fr": "%s : ouvrir",
		"es": "%s: abrir",
		"sr": "%s: отвори",
		"de": "%s: öffnen",
	},
	"tray_create_vault": {
		"en": "Create vault…",
		"ru": "Создать сейф…",
		"uk": "Створити сейф…",
		"fr": "Créer un coffre…",
		"es": "Crear caja fuerte…",
		"sr": "Направи трезор…",
		"de": "Tresor erstellen…",
	},
	"tray_vault_new_name_title": {
		"en": "New vault",
		"ru": "Новый сейф",
		"uk": "Новий сейф",
		"fr": "Nouveau coffre",
		"es": "Nueva caja fuerte",
		"sr": "Нови трезор",
		"de": "Neuer Tresor",
	},
	"tray_vault_new_name_prompt": {
		"en": "Vault name:",
		"ru": "Имя сейфа:",
		"uk": "Назва сейфу:",
		"fr": "Nom du coffre :",
		"es": "Nombre del coffre:",
		"sr": "Назив трезора:",
		"de": "Tresorname:",
	},
	"tray_vault_new_pw_title": {
		"en": "New vault «%s»",
		"ru": "Новый сейф «%s»",
		"uk": "Новий сейф «%s»",
		"fr": "Nouveau coffre « %s »",
		"es": "Nueva caja fuerte «%s»",
		"sr": "Нови трезор «%s»",
		"de": "Neuer Tresor «%s»",
	},
	"tray_vault_new_pw_prompt": {
		"en": "Password:",
		"ru": "Пароль:",
		"uk": "Пароль:",
		"fr": "Mot de passe :",
		"es": "Contraseña:",
		"sr": "Лозинка:",
		"de": "Passwort:",
	},
	"tray_vault_created_recovery": {
		"en": "Vault «%s» created, recovery key: %s\nStore it separately!",
		"ru": "Сейф «%s» создан, recovery-ключ: %s\nХраните отдельно!",
		"uk": "Сейф «%s» створено, ключ відновлення: %s\nЗберігайте окремо!",
		"fr": "Coffre « %s » créé, clé de récupération : %s\nConservez-la séparément !",
		"es": "Caja fuerte «%s» creada, clave de recuperación: %s\n¡Guárdela por separado!",
		"sr": "Трезор «%s» направљен, кључ за опоравак: %s\nЧувајте га засебно!",
		"de": "Tresor «%s» erstellt, Wiederherstellungsschlüssel: %s\nGetrennt aufbewahren!",
	},
	"tray_vault_created_no_recovery": {
		"en": "Vault «%s» created (recovery key not saved: %v)",
		"ru": "Сейф «%s» создан (recovery-ключ не сохранён: %v)",
		"uk": "Сейф «%s» створено (ключ відновлення не збережено: %v)",
		"fr": "Coffre « %s » créé (clé de récupération non enregistrée : %v)",
		"es": "Caja fuerte «%s» creada (clave de recuperación no guardada: %v)",
		"sr": "Трезор «%s» направљен (кључ за опоравак није сачуван: %v)",
		"de": "Tresor «%s» erstellt (Wiederherstellungsschlüssel nicht gespeichert: %v)",
	},
	"tray_vault_created": {
		"en": "Vault «%s» created",
		"ru": "Сейф «%s» создан",
		"uk": "Сейф «%s» створено",
		"fr": "Coffre « %s » créé",
		"es": "Caja fuerte «%s» creada",
		"sr": "Трезор «%s» направљен",
		"de": "Tresor «%s» erstellt",
	},
	"tray_vault_create_error": {
		"en": "Creation error: %v",
		"ru": "Ошибка создания: %v",
		"uk": "Помилка створення: %v",
		"fr": "Erreur de création : %v",
		"es": "Error de creación: %v",
		"sr": "Грешка при креирању: %v",
		"de": "Erstellungsfehler: %v",
	},
	"tray_vault_close_error": {
		"en": "Close error: %v",
		"ru": "Ошибка закрытия: %v",
		"uk": "Помилка закриття: %v",
		"fr": "Erreur de fermeture : %v",
		"es": "Error al cerrar: %v",
		"sr": "Грешка при затварању: %v",
		"de": "Fehler beim Schließen: %v",
	},
	"tray_vault_closed": {
		"en": "Vault «%s» closed and re-encrypted",
		"ru": "Сейф «%s» закрыт и перешифрован",
		"uk": "Сейф «%s» закрито та перешифровано",
		"fr": "Coffre « %s » fermé et rechiffré",
		"es": "Caja fuerte «%s» cerrada y recifrada",
		"sr": "Трезор «%s» затворен и поново шифрован",
		"de": "Tresor «%s» geschlossen und neu verschlüsselt",
	},
	"tray_vault_open_title": {
		"en": "Open vault «%s»",
		"ru": "Открыть сейф «%s»",
		"uk": "Відкрити сейф «%s»",
		"fr": "Ouvrir le coffre « %s »",
		"es": "Abrir caja fuerte «%s»",
		"sr": "Отвори трезор «%s»",
		"de": "Tresor «%s» öffnen",
	},
	"tray_vault_wrong_password": {
		"en": "Wrong password",
		"ru": "Неверный пароль",
		"uk": "Невірний пароль",
		"fr": "Mot de passe incorrect",
		"es": "Contraseña incorrecta",
		"sr": "Погрешна лозинка",
		"de": "Falsches Passwort",
	},
	"tray_vault_open_error": {
		"en": "Open error: %v",
		"ru": "Ошибка открытия: %v",
		"uk": "Помилка відкриття: %v",
		"fr": "Erreur d'ouverture : %v",
		"es": "Error al abrir: %v",
		"sr": "Грешка при отварању: %v",
		"de": "Fehler beim Öffnen: %v",
	},
	"tray_vault_opened": {
		"en": "Vault «%s» opened",
		"ru": "Сейф «%s» открыт",
		"uk": "Сейф «%s» відкрито",
		"fr": "Coffre « %s » ouvert",
		"es": "Caja fuerte «%s» abierta",
		"sr": "Трезор «%s» отворен",
		"de": "Tresor «%s» geöffnet",
	},
	"tray_orphans_warning": {
		"en": "unclosed vaults: ",
		"ru": "есть незакрытые сейфы: ",
		"uk": "є незакриті сейфи: ",
		"fr": "coffres non fermés : ",
		"es": "cajas fuertes no cerradas: ",
		"sr": "незатворени трезори: ",
		"de": "nicht geschlossene Tresore: ",
	},
	"tray_vault_notification_title": {
		"en": "Vault",
		"ru": "Сейф",
		"uk": "Сейф",
		"fr": "Coffre",
		"es": "Caja fuerte",
		"sr": "Трезор",
		"de": "Tresor",
	},
	"tray_no_tray_build": {
		"en": "tray not built: rebuild with `go build -tags tray ./cmd/discodrive`",
		"ru": "трей не собран: пересобери с `go build -tags tray ./cmd/discodrive`",
		"uk": "трей не зібрано: перезбери з `go build -tags tray ./cmd/discodrive`",
		"fr": "tray non compilé : recompilez avec `go build -tags tray ./cmd/discodrive`",
		"es": "tray no compilado: recompila con `go build -tags tray ./cmd/discodrive`",
		"sr": "треј није компајлиран: поново компајлирај са `go build -tags tray ./cmd/discodrive`",
		"de": "Tray nicht kompiliert: neu kompilieren mit `go build -tags tray ./cmd/discodrive`",
	},

	// ── autostart platform strings ────────────────────────────────────────────

	"autostart_mkdir_launchagents": {
		"en": "mkdir LaunchAgents: %w",
		"ru": "mkdir LaunchAgents: %w",
		"uk": "mkdir LaunchAgents: %w",
		"fr": "mkdir LaunchAgents : %w",
		"es": "mkdir LaunchAgents: %w",
		"sr": "mkdir LaunchAgents: %w",
		"de": "mkdir LaunchAgents: %w",
	},
	"autostart_write_plist": {
		"en": "writing plist: %w",
		"ru": "запись plist: %w",
		"uk": "запис plist: %w",
		"fr": "écriture du plist : %w",
		"es": "escribiendo plist: %w",
		"sr": "писање plist: %w",
		"de": "plist schreiben: %w",
	},
	"autostart_remove_plist": {
		"en": "removing plist: %w",
		"ru": "удаление plist: %w",
		"uk": "видалення plist: %w",
		"fr": "suppression du plist : %w",
		"es": "eliminando plist: %w",
		"sr": "уклањање plist: %w",
		"de": "plist entfernen: %w",
	},
	"autostart_write_unit": {
		"en": "writing unit file: %w",
		"ru": "запись unit-файла: %w",
		"uk": "запис unit-файлу: %w",
		"fr": "écriture du fichier unit : %w",
		"es": "escribiendo archivo unit: %w",
		"sr": "писање unit фајла: %w",
		"de": "Unit-Datei schreiben: %w",
	},
	"autostart_remove_unit": {
		"en": "removing unit file: %w",
		"ru": "удаление unit-файла: %w",
		"uk": "видалення unit-файлу: %w",
		"fr": "suppression du fichier unit : %w",
		"es": "eliminando archivo unit: %w",
		"sr": "уклањање unit фајла: %w",
		"de": "Unit-Datei entfernen: %w",
	},
}

// activeLang holds the current language tag as a pointer to string so it
// can be swapped atomically without a mutex.
var activeLang unsafe.Pointer // *string

func init() {
	s := "en"
	atomic.StorePointer(&activeLang, unsafe.Pointer(&s))
}

// SetLanguage sets the active language for T(). Thread-safe.
// Unknown tags fall back to "en".
func SetLanguage(lang string) {
	if lang == "" {
		lang = "en"
	}
	s := lang
	atomic.StorePointer(&activeLang, unsafe.Pointer(&s))
}

// ActiveLanguage returns the current active language tag.
func ActiveLanguage() string {
	p := atomic.LoadPointer(&activeLang)
	return *(*string)(p)
}

// T returns the localised string for the given message id.
// Falls back to English if the active language has no translation, and to
// the message id itself if no translation exists at all.
// To interpolate values, use fmt.Sprintf(i18n.T("key"), args...).
func T(id string) string {
	lang := ActiveLanguage()
	langs, ok := catalog[id]
	if !ok {
		return id
	}
	s, ok := langs[lang]
	if !ok {
		return langs["en"]
	}
	return s
}
