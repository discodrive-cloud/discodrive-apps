package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/index"
)

func TestSaveAndOpenProfile(t *testing.T) {
	profile := t.TempDir()
	cfg := config.Config{ServerURL: "https://example.test", DeviceToken: "kfd_x"}
	if err := SaveConfig(profile, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if filepath.Base(DesktopConfigPath(profile)) != "config.json" {
		t.Fatalf("DesktopConfigPath base = %q", filepath.Base(DesktopConfigPath(profile)))
	}

	ctrl, idx, err := Open(profile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	if ctrl == nil {
		t.Fatal("controller is nil")
	}
}

func TestOpenMissingConfigErrors(t *testing.T) {
	if _, _, err := Open(t.TempDir()); err == nil {
		t.Fatal("Open with no config should error (drives UI to pairing)")
	}
}

func TestOpenKeepsStateOnSameServer(t *testing.T) {
	profile := t.TempDir()
	_ = SaveConfig(profile, config.Config{ServerURL: "https://a.test", DeviceToken: "kfd_x"})

	_, idx, err := Open(profile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if u, _ := idx.ServerURL(); u != "https://a.test" {
		t.Fatalf("Open must stamp the server url into the index, got %q", u)
	}
	_ = idx.Put(index.Node{NodeID: "n1", RelPath: "keep.txt", Version: 1})
	idx.Close()

	_, idx2, err := Open(profile)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer idx2.Close()
	if _, ok, _ := idx2.Get("n1"); !ok {
		t.Fatal("same server — index must survive reopen")
	}
}

func TestOpenWipesStateOnServerChange(t *testing.T) {
	profile := t.TempDir()
	_ = SaveConfig(profile, config.Config{ServerURL: "https://old.test", DeviceToken: "kfd_x"})

	_, idx, err := Open(profile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = idx.Put(index.Node{NodeID: "n1", RelPath: "stale.txt", Version: 1})
	_ = idx.SetCursor(99)
	idx.Close()
	cached := filepath.Join(ContentDir(profile), "stale.txt")
	_ = os.MkdirAll(ContentDir(profile), 0o700)
	_ = os.WriteFile(cached, []byte("old"), 0o600)

	// Re-pair to a different server.
	_ = SaveConfig(profile, config.Config{ServerURL: "https://new.test", DeviceToken: "kfd_y"})
	_, idx2, err := Open(profile)
	if err != nil {
		t.Fatalf("Open after server change: %v", err)
	}
	defer idx2.Close()
	if _, ok, _ := idx2.Get("n1"); ok {
		t.Fatal("index from the old server must be wiped")
	}
	if c, _ := idx2.Cursor(); c != 0 {
		t.Fatalf("cursor must reset, got %d", c)
	}
	if u, _ := idx2.ServerURL(); u != "https://new.test" {
		t.Fatalf("index must be stamped with the new server, got %q", u)
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatalf("old cached content must be gone, stat err=%v", err)
	}
}

func TestOpenWipesLegacyUnstampedIndex(t *testing.T) {
	profile := t.TempDir()
	_ = SaveConfig(profile, config.Config{ServerURL: "https://new.test", DeviceToken: "kfd_x"})
	// A pre-stamp index (created by an old client version) may contain nodes from
	// ANY server — including a poisoned merge of two servers — so it can't be trusted.
	idx, err := index.Open(IndexDBPath(profile))
	if err != nil {
		t.Fatal(err)
	}
	_ = idx.Put(index.Node{NodeID: "legacy", RelPath: "junk", IsDir: true, Version: 1})
	idx.Close()

	_, idx2, err := Open(profile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx2.Close()
	if _, ok, _ := idx2.Get("legacy"); ok {
		t.Fatal("unstamped non-empty index must be wiped on open")
	}
	if u, _ := idx2.ServerURL(); u != "https://new.test" {
		t.Fatalf("index must be stamped after the wipe, got %q", u)
	}
}

func TestWipeState(t *testing.T) {
	profile := t.TempDir()
	_ = SaveConfig(profile, config.Config{ServerURL: "https://a.test", DeviceToken: "kfd_x"})
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm", "settings.json"} {
		if err := os.WriteFile(filepath.Join(profile, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(ContentDir(profile), "sub"), 0o700)
	_ = os.WriteFile(filepath.Join(ContentDir(profile), "sub", "f.txt"), []byte("x"), 0o600)

	_ = os.MkdirAll(filepath.Join(profile, "vault-cache"), 0700)
	_ = os.WriteFile(filepath.Join(profile, "vault-cache", "test.cipher"), []byte("ciphertext"), 0600)
	if err := WipeState(profile); err != nil {
		t.Fatalf("WipeState: %v", err)
	}
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm", "vault-cache"} {
		if _, err := os.Stat(filepath.Join(profile, f)); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed, stat err=%v", f, err)
		}
	}
	if _, err := os.Stat(ContentDir(profile)); !os.IsNotExist(err) {
		t.Fatalf("content dir must be removed, stat err=%v", err)
	}
	// The user's preferences and pairing config are not state — they stay.
	for _, f := range []string{"config.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(profile, f)); err != nil {
			t.Fatalf("%s must survive WipeState: %v", f, err)
		}
	}
}
