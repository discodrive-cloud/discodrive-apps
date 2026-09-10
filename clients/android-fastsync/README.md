# DiscoDriveFastSync (Android)

> **Deprecated (2026-09).** Folder sync is built into the full client
> (`clients/android-discodrive`, Settings → Folder sync) and this app is no longer
> released. The code stays one release for reference and will then be removed.

Minimal app: pair + manual/background sync of a folder via `kfmobile.aar`.

## Build
1. Build Block B (if not done yet): from `daemon/` — `mobile/build.sh` → produces `daemon/mobile/build/kfmobile.aar`.
2. Copy the aar into the project:

       cp ../../daemon/mobile/build/kfmobile.aar app/libs/kfmobile.aar

3. Open `clients/android-fastsync/` in Android Studio, let it sync
   (if it offers to upgrade AGP/Gradle — accept).
4. Pick a device, Run.

## Usage
1. On first launch grant **All files access** (the system screen opens).
2. Enter the server address (+ "Self-signed" if mkcert), Pair device → confirm the code in the browser.
3. "Sync now" — the `/storage/emulated/0/DiscoDriveFastSync/Sync` folder is synced; KeePass opens its database from there.
4. Background sync (~20 min) — via WorkManager; usual caveats about Doze / OEM background restrictions.
