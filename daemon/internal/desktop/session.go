package desktop

import (
	"os"
	"path/filepath"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

// DesktopConfigPath is the desktop client's config.json inside the profile.
func DesktopConfigPath(profileDir string) string {
	return filepath.Join(profileDir, "config.json")
}

// SaveConfig writes the desktop config into the profile, creating the dir.
func SaveConfig(profileDir string, cfg config.Config) error {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return err
	}
	return cfg.Save(DesktopConfigPath(profileDir))
}

// WipeState removes the profile's server-derived state: the index database (with
// its SQLite sidecar files) and the content cache. The pairing config and the
// user's preferences (settings.json) are left untouched. Used on unpair and when
// the profile turns out to belong to a different server.
func WipeState(profileDir string) error {
	dbPath := IndexDBPath(profileDir)
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + "-journal"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(ContentDir(profileDir)); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(profileDir, "vault-cache"))
}

// Open loads the profile's config and index and builds a controller backed by an
// unscoped server client (the browser navigates the whole vault). The returned
// index is handed back so the caller controls its lifetime. A missing/invalid
// config returns an error, signalling the UI to show pairing.
//
// An index built against a different server than the config now points at is
// wiped before use: its node ids, cursor, and cached content all live in the old
// server's namespace, and reusing them merges the old tree into the new one and
// re-uploads stale data (e.g. leftover vaults) to the wrong server.
func Open(profileDir string) (*Controller, *index.Index, error) {
	cfg, err := config.Load(DesktopConfigPath(profileDir))
	if err != nil {
		return nil, nil, err
	}
	idx, err := index.Open(IndexDBPath(profileDir))
	if err != nil {
		return nil, nil, err
	}
	stored, err := idx.ServerURL()
	if err != nil {
		idx.Close()
		return nil, nil, err
	}
	suspect := stored != "" && stored != cfg.ServerURL
	if stored == "" {
		// A pre-stamp index (created by an old client version) may have been built
		// against any server — including a poisoned merge of two servers from the
		// pre-wipe unpair flow — so a non-empty one cannot be trusted either. The
		// one-time cache loss for legacy profiles is the price of correctness.
		if nodes, nerr := idx.All(); nerr == nil && len(nodes) > 0 {
			suspect = true
		}
	}
	if suspect {
		idx.Close()
		if err := WipeState(profileDir); err != nil {
			return nil, nil, err
		}
		if idx, err = index.Open(IndexDBPath(profileDir)); err != nil {
			return nil, nil, err
		}
	}
	if err := idx.SetServerURL(cfg.ServerURL); err != nil {
		idx.Close()
		return nil, nil, err
	}
	srv := protocol.NewUnscopedPinned(cfg.ServerURL, cfg.DeviceToken, cfg.ServerPin)
	c := NewController(srv, idx, ContentDir(profileDir))
	c.vaultCacheDir = filepath.Join(profileDir, "vault-cache")
	c.vaultPlainRoot = vaultPlainRootFor(cfg.ServerURL, profileDir)
	return c, idx, nil
}
