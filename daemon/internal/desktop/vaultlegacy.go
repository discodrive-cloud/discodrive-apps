package desktop

import (
	"fmt"
	"os"
	"path/filepath"
)

func legacyVaultRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "discodrive", "open"), nil
}

// LegacyVaultFolder identifies plaintext from the old shared cache. Its filenames
// encode a local profile path and vault name, but NOT the account or server.
// Even a matching current index cannot prove ownership after a previous re-pairing.
// Report these files for explicit recovery; never import them into a current vault.
// The tray may also still be using this directory, so never move or delete it here.
func LegacyVaultFolder() (string, error) {
	root, err := legacyVaultRoot()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return root, err
	}
	for _, entry := range entries {
		// Finder can leave this file after the last vault folder was recovered.
		if entry.Name() == ".DS_Store" && entry.Type().IsRegular() {
			continue
		}
		return root, nil
	}
	return "", nil
}

func checkLegacyVaultLeftovers() error {
	root, err := LegacyVaultFolder()
	if err != nil {
		return err
	}
	if root != "" {
		return fmt.Errorf("%w: please preserve the files in %s and remove the recovered folders from that location before signing out", ErrVaultLeftovers, root)
	}
	return nil
}
