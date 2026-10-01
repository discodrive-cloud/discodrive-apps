package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"discodrive.org/daemon/internal/i18n"
	"discodrive.org/daemon/internal/vault"
)

// The recovery phrase is the vault's master keys. The tray shows it once, in a dialog,
// and writes it to disk only when the user asks — and then never inside the synced
// folder, where the syncer would upload it next to the vault it unlocks.

// recoveryButtons are the two labels of the recovery dialog: save a copy, or finish.
var recoveryButtons = map[string][2]string{
	"en": {"Save to file…", "Done"},
	"ru": {"Сохранить в файл…", "Готово"},
	"uk": {"Зберегти у файл…", "Готово"},
	"de": {"In Datei speichern…", "Fertig"},
	"fr": {"Enregistrer dans un fichier…", "Terminé"},
	"es": {"Guardar en un archivo…", "Listo"},
	"sr": {"Сачувај у датотеку…", "Готово"},
}

// recoveryButtonLabels returns (save, done) in the active language.
func recoveryButtonLabels() (string, string) {
	b, ok := recoveryButtons[i18n.ActiveLanguage()]
	if !ok {
		b = recoveryButtons["en"]
	}
	return b[0], b[1]
}

// errRecoveryInSyncDir refuses a recovery file the syncer would upload.
var errRecoveryInSyncDir = errors.New("the recovery file would be inside the synced folder")

// recoveryFilePath is where a recovery phrase the user chose to save goes:
// <UserConfigDir>/discodrive/<name>-recovery.txt. name must be a plain vault name, and
// the result must lie outside syncDir.
func recoveryFilePath(configDir, syncDir, name string) (string, error) {
	if err := vault.CheckPlainName(name); err != nil || strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid vault name %q", name)
	}
	p := filepath.Join(configDir, "discodrive", name+"-recovery.txt")
	if syncDir != "" && within(syncDir, p) {
		return "", errRecoveryInSyncDir
	}
	return p, nil
}

// within reports whether p is root or lies under it, comparing real paths (the folders
// on either side may not exist yet, or be reached through a symlink such as /var on macOS).
func within(root, p string) bool {
	rel, err := filepath.Rel(realPath(root), realPath(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath resolves symlinks in the longest existing prefix of p.
func realPath(p string) string {
	p, _ = filepath.Abs(p)
	rest := ""
	for dir := p; ; {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// saveRecoveryPhrase writes phrase to the recovery file for name (mode 0600, directory
// 0700) and returns its path.
func saveRecoveryPhrase(syncDir, name, phrase string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p, err := recoveryFilePath(configDir, syncDir, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(phrase + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(p, 0o600) // an older file may carry wider permissions
	}
	return p, err
}

// writeDialogText puts text for a dialog script into a private (0600) temporary file:
// under <UserConfigDir>/discodrive, or the per-user temp dir when that would lie inside
// the synced folder. cleanup removes it; call it as soon as the dialog closes.
func writeDialogText(syncDir, text string) (path string, cleanup func(), err error) {
	dir := os.TempDir()
	if configDir, cerr := os.UserConfigDir(); cerr == nil {
		if d := filepath.Join(configDir, "discodrive"); syncDir == "" || !within(syncDir, d) {
			if os.MkdirAll(d, 0o700) == nil {
				dir = d
			}
		}
	}
	f, err := os.CreateTemp(dir, ".dialog-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}
