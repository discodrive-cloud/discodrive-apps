package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points HOME and the XDG directories at a throwaway folder, so no test
// writes into the real ~/Library/Caches, ~/.config or ~/.local of the machine
// running it (the vault cache under Caches/discodrive/open used to fill up).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "discodrive-test-home-")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
	} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	// Never run tmutil from tests: TestRecoveryFolderHygiene checks the calls instead.
	excludeFromBackup = func(string) {}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
