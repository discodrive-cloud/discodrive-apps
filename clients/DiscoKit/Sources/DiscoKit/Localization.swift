import Foundation

// DiscoDrive localization. The language comes from the server (not the system locale), so
// we maintain a simple string table and switch by the active language in AppState.
// Supported languages match the server list: en (default), de, uk, fr, es, ru, sr.
public enum L10n {
    public static let supported = ["en", "de", "uk", "fr", "es", "ru", "sr"]

    // Endonym: each language name is shown in that language itself — do not translate.
    public static let displayName: [String: String] = [
        "en": "English", "de": "Deutsch", "uk": "Українська",
        "fr": "Français", "es": "Español", "ru": "Русский", "sr": "Српски",
    ]

    public static func t(_ key: String, _ lang: String) -> String {
        table[key]?[lang] ?? table[key]?["en"] ?? key
    }

    // key → (lang → text). en is the base / fallback.
    static let table: [String: [String: String]] = [
        "pair.title": [
            "en": "Connect to server", "ru": "Подключение к серверу",
            "uk": "Підключення до сервера", "fr": "Connexion au serveur",
            "es": "Conectar al servidor", "sr": "Повезивање на сервер",
            "de": "Mit Server verbinden",
        ],
        "pair.connect": [
            "en": "Connect", "ru": "Подключить", "uk": "Підключити",
            "fr": "Connecter", "es": "Conectar", "sr": "Повежи",
            "de": "Verbinden",
        ],
        "pair.deviceCode": [
            "en": "Device code", "ru": "Код устройства", "uk": "Код пристрою",
            "fr": "Code de l’appareil", "es": "Código del dispositivo", "sr": "Код уређаја",
            "de": "Gerätecode",
        ],
        "pair.confirmHint": [
            "en": "Confirm the device in the browser that opened, then wait…",
            "ru": "Подтвердите устройство в открывшемся браузере, затем подождите…",
            "uk": "Підтвердьте пристрій у відкритому браузері, потім зачекайте…",
            "fr": "Confirmez l’appareil dans le navigateur qui s’est ouvert, puis patientez…",
            "es": "Confirma el dispositivo en el navegador que se abrió y espera…",
            "sr": "Потврдите уређај у претраживачу који се отворио, па сачекајте…",
            "de": "Bestätigen Sie das Gerät im geöffneten Browser und warten Sie dann…",
        ],
        "browse.count": [
            "en": "Items", "ru": "Элементов", "uk": "Елементів",
            "fr": "Éléments", "es": "Elementos", "sr": "Ставки",
            "de": "Elemente",
        ],
        "browse.empty": [
            "en": "Folder is empty or not selected",
            "ru": "Папка пуста или не выбрана",
            "uk": "Папка порожня або не вибрана",
            "fr": "Dossier vide ou non sélectionné",
            "es": "Carpeta vacía o no seleccionada",
            "sr": "Фасцикла је празна или није изабрана",
            "de": "Ordner ist leer oder nicht ausgewählt",
        ],
        "menu.download": [
            "en": "Download", "ru": "Скачать", "uk": "Завантажити",
            "fr": "Télécharger", "es": "Descargar", "sr": "Преузми",
            "de": "Herunterladen",
        ],
        "menu.keepLocal": [
            "en": "Keep on this Mac", "ru": "Держать локально", "uk": "Тримати локально",
            "fr": "Garder en local", "es": "Mantener local", "sr": "Чувај локално",
            "de": "Auf diesem Mac behalten",
        ],
        "menu.removeLocal": [
            "en": "Remove local copy", "ru": "Удалить локальную копию",
            "uk": "Видалити локальну копію", "fr": "Supprimer la copie locale",
            "es": "Eliminar copia local", "sr": "Уклони локалну копију",
            "de": "Lokale Kopie entfernen",
        ],
        "toolbar.refresh": [
            "en": "Refresh", "ru": "Обновить", "uk": "Оновити",
            "fr": "Actualiser", "es": "Actualizar", "sr": "Освежи",
            "de": "Aktualisieren",
        ],
        "toolbar.free": [
            "en": "Free up space", "ru": "Освободить место", "uk": "Звільнити місце",
            "fr": "Libérer de l’espace", "es": "Liberar espacio", "sr": "Ослободи простор",
            "de": "Speicher freigeben",
        ],
        "toolbar.import": [
            "en": "Import from folder", "ru": "Импорт из папки", "uk": "Імпорт із теки",
            "fr": "Importer du dossier", "es": "Importar de carpeta", "sr": "Увези из фасцикле",
            "de": "Aus Ordner importieren",
        ],
        "toolbar.openFinder": [
            "en": "Open folder in Finder", "ru": "Открыть папку в Finder",
            "uk": "Відкрити папку у Finder", "fr": "Ouvrir le dossier dans le Finder",
            "es": "Abrir carpeta en Finder", "sr": "Отвори фасциклу у Finder-у",
            "de": "Ordner im Finder öffnen",
        ],
        "toolbar.logout": [
            "en": "Sign out", "ru": "Выйти", "uk": "Вийти",
            "fr": "Se déconnecter", "es": "Cerrar sesión", "sr": "Одјави се",
            "de": "Abmelden",
        ],
        "dialog.logoutTitle": [
            "en": "Sign out?", "ru": "Выйти?", "uk": "Вийти?",
            "fr": "Se déconnecter ?", "es": "¿Cerrar sesión?", "sr": "Одјавити се?",
            "de": "Abmelden?",
        ],
        "dialog.logoutMessage": [
            "en": "This unpairs the app from the server. Pairing again deletes all saved data and starts over.",
            "ru": "Это отвяжет приложение от сервера. Повторное подключение удалит все сохранённые данные и начнёт историю заново.",
            "uk": "Це відв'яже застосунок від сервера. Повторне підключення видалить усі збережені дані й почне історію заново.",
            "fr": "Cela dissocie l'application du serveur. Un nouvel appairage supprime toutes les données enregistrées et repart de zéro.",
            "es": "Esto desvincula la app del servidor. Volver a emparejar elimina todos los datos guardados y empieza de cero.",
            "sr": "Ово раздваја апликацију од сервера. Поновно упаривање брише све сачуване податке и креће испочетка.",
            "de": "Das trennt die App vom Server. Eine erneute Kopplung löscht alle gespeicherten Daten und beginnt von vorn.",
        ],
        "status.updated": [
            "en": "Updated", "ru": "Обновлено", "uk": "Оновлено",
            "fr": "Mis à jour", "es": "Actualizado", "sr": "Ажурирано",
            "de": "Aktualisiert",
        ],
        "status.refreshError": [
            "en": "Update error", "ru": "Ошибка обновления", "uk": "Помилка оновлення",
            "fr": "Erreur de mise à jour", "es": "Error de actualización", "sr": "Грешка при ажурирању",
            "de": "Aktualisierungsfehler",
        ],
        "status.downloadError": [
            "en": "Download error", "ru": "Ошибка скачивания", "uk": "Помилка завантаження",
            "fr": "Erreur de téléchargement", "es": "Error de descarga", "sr": "Грешка при преузимању",
            "de": "Download-Fehler",
        ],
        "settings.title": [
            "en": "Settings", "ru": "Настройки", "uk": "Налаштування",
            "fr": "Réglages", "es": "Ajustes", "sr": "Подешавања",
            "de": "Einstellungen",
        ],
        "settings.language": [
            "en": "Language", "ru": "Язык", "uk": "Мова",
            "fr": "Langue", "es": "Idioma", "sr": "Језик",
            "de": "Sprache",
        ],
        "toolbar.newFolder": [
            "en": "New folder", "ru": "Новая папка", "uk": "Нова папка",
            "fr": "Nouveau dossier", "es": "Nueva carpeta", "sr": "Нова фасцикла",
            "de": "Neuer Ordner",
        ],
        "toolbar.addFile": [
            "en": "Add file…", "ru": "Добавить файл…", "uk": "Додати файл…",
            "fr": "Ajouter un fichier…", "es": "Añadir archivo…", "sr": "Додај датотеку…",
            "de": "Datei hinzufügen…",
        ],
        "menu.rename": [
            "en": "Rename", "ru": "Переименовать", "uk": "Перейменувати",
            "fr": "Renommer", "es": "Renombrar", "sr": "Преименуј",
            "de": "Umbenennen",
        ],
        "menu.delete": [
            "en": "Delete", "ru": "Удалить", "uk": "Видалити",
            "fr": "Supprimer", "es": "Eliminar", "sr": "Обриши",
            "de": "Löschen",
        ],
        "dialog.folderName": [
            "en": "Folder name", "ru": "Имя папки", "uk": "Назва папки",
            "fr": "Nom du dossier", "es": "Nombre de carpeta", "sr": "Назив фасцикле",
            "de": "Ordnername",
        ],
        "dialog.newName": [
            "en": "New name", "ru": "Новое имя", "uk": "Нова назва",
            "fr": "Nouveau nom", "es": "Nuevo nombre", "sr": "Нови назив",
            "de": "Neuer Name",
        ],
        "dialog.create": [
            "en": "Create", "ru": "Создать", "uk": "Створити",
            "fr": "Créer", "es": "Crear", "sr": "Направи",
            "de": "Erstellen",
        ],
        "dialog.cancel": [
            "en": "Cancel", "ru": "Отмена", "uk": "Скасувати",
            "fr": "Annuler", "es": "Cancelar", "sr": "Откажи",
            "de": "Abbrechen",
        ],
        "dialog.done": [
            "en": "Done", "ru": "Готово", "uk": "Готово",
            "fr": "Terminé", "es": "Listo", "sr": "Готово",
            "de": "Fertig",
        ],
        "status.uploadError": [
            "en": "Upload error", "ru": "Ошибка загрузки", "uk": "Помилка завантаження",
            "fr": "Erreur d’envoi", "es": "Error al subir", "sr": "Грешка при отпремању",
            "de": "Upload-Fehler",
        ],
        "vault.open": [
            "en": "Open vault", "ru": "Открыть сейф", "uk": "Відкрити сейф",
            "fr": "Ouvrir le coffre", "es": "Abrir caja fuerte", "sr": "Отвори сеф",
            "de": "Tresor öffnen",
        ],
        "vault.close": [
            "en": "Close vault", "ru": "Закрыть сейф", "uk": "Закрити сейф",
            "fr": "Fermer le coffre", "es": "Cerrar caja fuerte", "sr": "Затвори сеф",
            "de": "Tresor schließen",
        ],
        "vault.unlock": [
            "en": "Unlock", "ru": "Разблокировать", "uk": "Розблокувати",
            "fr": "Déverrouiller", "es": "Desbloquear", "sr": "Откључај",
            "de": "Entsperren",
        ],
        "vault.unlockWith": [
            "en": "Unlock with %@", "ru": "Открыть по %@", "uk": "Відкрити через %@",
            "fr": "Déverrouiller avec %@", "es": "Desbloquear con %@", "sr": "Откључај помоћу %@",
            "de": "Entsperren mit %@",
        ],
        "vault.remember": [
            "en": "Remember password", "ru": "Запомнить пароль", "uk": "Запам’ятати пароль",
            "fr": "Mémoriser le mot de passe", "es": "Recordar contraseña", "sr": "Запамти лозинку",
            "de": "Passwort merken",
        ],
        "vault.unlockReason": [
            "en": "Unlock the vault", "ru": "Открыть сейф", "uk": "Відкрити сейф",
            "fr": "Déverrouiller le coffre", "es": "Abrir la caja fuerte", "sr": "Откључај сеф",
            "de": "Tresor entsperren",
        ],
        "vault.unlocking": [
            "en": "Unlocking…", "ru": "Открываю…", "uk": "Розблокування…",
            "fr": "Déverrouillage…", "es": "Desbloqueando…", "sr": "Откључавање…",
            "de": "Wird entsperrt…",
        ],
        "vault.password": [
            "en": "Vault password", "ru": "Пароль сейфа", "uk": "Пароль сейфа",
            "fr": "Mot de passe du coffre", "es": "Contraseña de la caja", "sr": "Лозинка сефа",
            "de": "Tresor-Passwort",
        ],
        "vault.wrongPassword": [
            "en": "Wrong password", "ru": "Неверный пароль", "uk": "Невірний пароль",
            "fr": "Mot de passe incorrect", "es": "Contraseña incorrecta", "sr": "Погрешна лозинка",
            "de": "Falsches Passwort",
        ],
        "vault.createVault": [
            "en": "Create vault", "ru": "Создать сейф", "uk": "Створити сейф",
            "fr": "Créer un coffre", "es": "Crear caja fuerte", "sr": "Направи сеф",
            "de": "Tresor erstellen",
        ],
        "vault.name": [
            "en": "Vault name", "ru": "Имя сейфа", "uk": "Назва сейфа",
            "fr": "Nom du coffre", "es": "Nombre de la caja", "sr": "Назив сефа",
            "de": "Tresorname",
        ],
        "vault.createError": [
            "en": "Vault creation error", "ru": "Ошибка создания сейфа", "uk": "Помилка створення сейфа",
            "fr": "Erreur de création du coffre", "es": "Error al crear la caja", "sr": "Грешка при прављењу сефа",
            "de": "Fehler beim Erstellen des Tresors",
        ],
        "vault.addFile": [
            "en": "Add file", "ru": "Добавить файл", "uk": "Додати файл",
            "fr": "Ajouter un fichier", "es": "Añadir archivo", "sr": "Додај датотеку",
            "de": "Datei hinzufügen",
        ],
        "vault.recoveryTitle": [
            "en": "Recovery key", "ru": "Ключ восстановления", "uk": "Ключ відновлення",
            "fr": "Clé de récupération", "es": "Clave de recuperación", "sr": "Кључ за опоравак",
            "de": "Wiederherstellungsschlüssel",
        ],
        "vault.recoveryHint": [
            "en": "Save these words. They unlock the vault if you forget the password.",
            "ru": "Сохраните эти слова. Они откроют сейф, если забудете пароль.",
            "uk": "Збережіть ці слова. Вони відкриють сейф, якщо забудете пароль.",
            "fr": "Conservez ces mots. Ils déverrouillent le coffre en cas d’oubli du mot de passe.",
            "es": "Guarda estas palabras. Abren la caja si olvidas la contraseña.",
            "sr": "Сачувајте ове речи. Откључавају сеф ако заборавите лозинку.",
            "de": "Bewahren Sie diese Wörter auf. Sie entsperren den Tresor, falls Sie das Passwort vergessen.",
        ],
        "vault.recoverySaved": [
            "en": "I saved it", "ru": "Я сохранил", "uk": "Я зберіг",
            "fr": "Je l’ai enregistré", "es": "Lo guardé", "sr": "Сачувао сам",
            "de": "Gespeichert",
        ],
        "vault.copy": [
            "en": "Copy", "ru": "Копировать", "uk": "Копіювати",
            "fr": "Copier", "es": "Copiar", "sr": "Копирај",
            "de": "Kopieren",
        ],
        "vault.useRecovery": [
            "en": "Use recovery key", "ru": "Через ключ восстановления", "uk": "Через ключ відновлення",
            "fr": "Utiliser la clé de récupération", "es": "Usar clave de recuperación", "sr": "Користи кључ за опоравак",
            "de": "Wiederherstellungsschlüssel verwenden",
        ],
        "vault.usePassword": [
            "en": "Use password", "ru": "Через пароль", "uk": "Через пароль",
            "fr": "Utiliser le mot de passe", "es": "Usar contraseña", "sr": "Користи лозинку",
            "de": "Passwort verwenden",
        ],
        "tray.toggle": [
            "en": "Toggle window", "ru": "Показать/скрыть окно", "uk": "Показати/сховати вікно",
            "fr": "Afficher/masquer la fenêtre", "es": "Mostrar/ocultar ventana",
            "sr": "Прикажи/сакриј прозор", "de": "Fenster ein-/ausblenden",
        ],
        "tray.quit": [
            "en": "Quit DiscoDrive", "ru": "Выйти из DiscoDrive", "uk": "Вийти з DiscoDrive",
            "fr": "Quitter DiscoDrive", "es": "Salir de DiscoDrive", "sr": "Изађи из DiscoDrive-а",
            "de": "DiscoDrive beenden",
        ],
        "au.title": ["en": "Auto-upload", "ru": "Автозагрузка", "uk": "Автозавантаження", "de": "Automatischer Upload", "fr": "Envoi automatique", "es": "Subida automática", "sr": "Аутоматско отпремање"],
        "au.master": ["en": "Upload my photos", "ru": "Загружать мои фото", "uk": "Завантажувати мої фото", "de": "Meine Fotos hochladen", "fr": "Envoyer mes photos", "es": "Subir mis fotos", "sr": "Отпремај моје фотографије"],
        "au.destination": ["en": "Destination", "ru": "Куда", "uk": "Куди", "de": "Ziel", "fr": "Destination", "es": "Destino", "sr": "Одредиште"],
        "au.when": ["en": "WHEN TO UPLOAD", "ru": "КОГДА ЗАГРУЖАТЬ", "uk": "КОЛИ ЗАВАНТАЖУВАТИ", "de": "WANN HOCHLADEN", "fr": "QUAND ENVOYER", "es": "CUÁNDO SUBIR", "sr": "КАДА ОТПРЕМАТИ"],
        "au.wifi": ["en": "Wi-Fi only", "ru": "Только по Wi-Fi", "uk": "Лише через Wi-Fi", "de": "Nur über WLAN", "fr": "Wi-Fi uniquement", "es": "Solo por Wi-Fi", "sr": "Само преко Wi-Fi"],
        "au.charging": ["en": "Only while charging", "ru": "Только при зарядке", "uk": "Лише під час заряджання", "de": "Nur beim Laden", "fr": "Seulement en charge", "es": "Solo mientras se carga", "sr": "Само док се пуни"],
        "au.battery": ["en": "Not below 20% battery", "ru": "Не ниже 20% батареи", "uk": "Не нижче 20% заряду", "de": "Nicht unter 20 % Akku", "fr": "Pas sous 20 % de batterie", "es": "No por debajo del 20 % de batería", "sr": "Не испод 20% батерије"],
        "au.uploadNow": ["en": "Upload now", "ru": "Загрузить сейчас", "uk": "Завантажити зараз", "de": "Jetzt hochladen", "fr": "Envoyer maintenant", "es": "Subir ahora", "sr": "Отпреми одмах"],
        "au.log": ["en": "Log", "ru": "Журнал", "uk": "Журнал", "de": "Protokoll", "fr": "Journal", "es": "Registro", "sr": "Дневник"],
        "au.emptyLog": ["en": "Nothing uploaded yet", "ru": "Пока ничего не загружено", "uk": "Ще нічого не завантажено", "de": "Noch nichts hochgeladen", "fr": "Rien envoyé pour l’instant", "es": "Aún no se ha subido nada", "sr": "Још ништа није отпремљено"],
        "au.stats": ["en": "Sent %1 · skipped %2 · deferred %3", "ru": "Отправлено %1 · пропущено %2 · отложено %3", "uk": "Надіслано %1 · пропущено %2 · відкладено %3", "de": "Gesendet %1 · übersprungen %2 · zurückgestellt %3", "fr": "Envoyés %1 · ignorés %2 · reportés %3", "es": "Enviados %1 · omitidos %2 · aplazados %3", "sr": "Послато %1 · прескочено %2 · одложено %3"],
        "au.neverDeletes": ["en": "Photos stay on your phone. Auto-upload only copies them to your server.", "ru": "Фото остаются на телефоне. Автозагрузка только копирует их на сервер.", "uk": "Фото залишаються на телефоні. Автозавантаження лише копіює їх на сервер.", "de": "Fotos bleiben auf dem Telefon. Der Upload kopiert sie nur auf den Server.", "fr": "Les photos restent sur le téléphone. L’envoi ne fait que les copier sur le serveur.", "es": "Las fotos siguen en el teléfono. La subida solo las copia a tu servidor.", "sr": "Фотографије остају на телефону. Отпремање их само копира на сервер."],
        "au.background": ["en": "iOS decides when background uploads run — usually while the phone is idle and charging. Open the app to upload right away.", "ru": "iOS сама решает, когда запускать фоновую загрузку — обычно когда телефон не используется и заряжается. Откройте приложение, чтобы загрузить сразу.", "uk": "iOS сама вирішує, коли запускати фонове завантаження — зазвичай коли телефон не використовується та заряджається. Відкрийте застосунок, щоб завантажити одразу.", "de": "iOS entscheidet, wann Uploads im Hintergrund laufen — meist wenn das Telefon ungenutzt lädt. Öffne die App, um sofort hochzuladen.", "fr": "iOS décide quand les envois s’exécutent en arrière-plan — généralement quand le téléphone est inactif et en charge. Ouvrez l’app pour envoyer tout de suite.", "es": "iOS decide cuándo se ejecutan las subidas en segundo plano, normalmente con el teléfono inactivo y cargando. Abre la app para subir ahora.", "sr": "iOS одлучује када се отпремање извршава у позадини — обично док телефон мирује и пуни се. Отворите апликацију да отпремите одмах."],
        "au.blocked.wifi": ["en": "Waiting for Wi-Fi", "ru": "Ожидание Wi-Fi", "uk": "Очікування Wi-Fi", "de": "Warte auf WLAN", "fr": "En attente du Wi-Fi", "es": "Esperando Wi-Fi", "sr": "Чека се Wi-Fi"],
        "au.blocked.network": ["en": "Waiting for a connection", "ru": "Ожидание сети", "uk": "Очікування мережі", "de": "Warte auf Verbindung", "fr": "En attente de connexion", "es": "Esperando conexión", "sr": "Чека се веза"],
        "au.blocked.charging": ["en": "Waiting for the charger", "ru": "Ожидание зарядки", "uk": "Очікування зарядки", "de": "Warte auf das Ladegerät", "fr": "En attente du chargeur", "es": "Esperando el cargador", "sr": "Чека се пуњач"],
        "au.blocked.battery": ["en": "Battery too low", "ru": "Батарея разряжена", "uk": "Замало заряду", "de": "Akku zu schwach", "fr": "Batterie trop faible", "es": "Batería demasiado baja", "sr": "Батерија је прениска"],
        "au.noAccess": ["en": "DiscoDrive needs access to your photos", "ru": "Нужен доступ к фото", "uk": "Потрібен доступ до фото", "de": "Zugriff auf Fotos nötig", "fr": "Accès aux photos nécessaire", "es": "Se necesita acceso a las fotos", "sr": "Потребан је приступ фотографијама"],
        "au.backfill": ["en": "Upload existing photos too", "ru": "Загрузить и старые фото", "uk": "Завантажити й старі фото", "de": "Auch vorhandene Fotos hochladen", "fr": "Envoyer aussi les photos existantes", "es": "Subir también las fotos existentes", "sr": "Отпреми и постојеће фотографије"],
        "au.backfillAsk": ["en": "%1 photos are already on this phone and were marked as skipped. Upload them now? On a large library this takes a while and is best left on Wi-Fi and power.", "ru": "%1 фото уже есть на телефоне и помечены как пропущенные. Загрузить их сейчас? Для большой библиотеки это надолго — лучше по Wi-Fi и на зарядке.", "uk": "%1 фото вже є на телефоні й позначені як пропущені. Завантажити їх зараз? Для великої бібліотеки це надовго — краще через Wi-Fi і на зарядці.", "de": "%1 Fotos liegen bereits auf dem Telefon und wurden übersprungen. Jetzt hochladen? Bei einer großen Mediathek dauert das; am besten mit WLAN und Strom.", "fr": "%1 photos sont déjà sur ce téléphone et ont été ignorées. Les envoyer maintenant ? Sur une grande photothèque, c’est long — de préférence en Wi-Fi et branché.", "es": "%1 fotos ya están en el teléfono y se omitieron. ¿Subirlas ahora? Con una biblioteca grande lleva tiempo; mejor con Wi-Fi y cargando.", "sr": "%1 фотографија већ је на телефону и прескочене су. Отпремити их сада? Уз велику библиотеку то траје — најбоље уз Wi-Fi и пуњач."],
        "au.backfillStart": ["en": "Upload them", "ru": "Загрузить", "uk": "Завантажити", "de": "Hochladen", "fr": "Les envoyer", "es": "Subirlas", "sr": "Отпреми"],
        "au.backfillQueued": ["en": "%1 photos queued", "ru": "В очереди: %1", "uk": "У черзі: %1", "de": "%1 Fotos in der Warteschlange", "fr": "%1 photos en file", "es": "%1 fotos en cola", "sr": "У реду: %1"],
    ]

    // Current UI language (used by the tray menu, which is built outside SwiftUI).
    public static var currentLanguage: String {
        UserDefaults.standard.string(forKey: "ui_language") ?? "en"
    }
}
