package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/localname"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

// downServer is the vault server unreachable (or the device revoked): every write fails.
type downServer struct{ *vaultTestServer }

var errDown = errors.New("dial tcp: connection refused")

func (downServer) PushFile(context.Context, string, *int64, io.Reader, time.Time) (engine.RemoteNode, bool, error) {
	return engine.RemoteNode{}, false, errDown
}
func (downServer) EnsureDir(context.Context, string) (engine.RemoteNode, error) {
	return engine.RemoteNode{}, errDown
}
func (downServer) UploadFile(context.Context, string, string, io.Reader, time.Time) error {
	return errDown
}
func (downServer) Changes(context.Context, int64, int) ([]engine.Change, int64, bool, error) {
	return nil, 0, false, errDown
}

// decryptVault opens a recovered vault folder with the vault's password.
func decryptVault(t *testing.T, dir string) map[string]string {
	t.Helper()
	v, err := vault.Open(dir, "pw")
	if err != nil {
		t.Fatalf("recovered vault does not open: %v", err)
	}
	out := t.TempDir()
	if err := v.DecryptTree(dir, out); err != nil {
		t.Fatal(err)
	}
	return readPlainTree(t, out)
}

// A vault that cannot be saved (server gone) is closed by force: its unsaved changes are
// kept as an encrypted vault folder in the recovery folder, which the same password opens,
// and only then are the plaintext and the session dropped.
func TestForceCloseKeepsUnsavedChangesEncrypted(t *testing.T) {
	c, srv, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.srv = downServer{srv}
	if err := c.CloseAllVaults(context.Background()); err == nil {
		t.Fatal("close with the server down: want an error")
	}
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err != nil {
		t.Fatalf("ForceCloseAllVaults: %v", err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], recovery) {
		t.Fatalf("recovered = %v, want one folder in %s", got, recovery)
	}
	tree := decryptVault(t, got[0])
	if tree[filepath.Join("folder", "new.txt")] != fmt.Sprintf("%x", sha256.Sum256([]byte("unsaved"))) || tree[filepath.Join("folder", "keep")] == "" {
		t.Fatalf("recovered vault = %v, want the unsaved file and the rest", tree)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("plaintext left at %s: %v", plain, err)
	}
	if c.IsVaultOpen("vault") {
		t.Error("session still open")
	}
}

// Nothing unsaved: nothing is kept, the plaintext goes.
func TestForceCloseWithoutChangesKeepsNothing(t *testing.T) {
	c, srv, plain, _ := openCloseFixture(t)
	c.srv = downServer{srv}
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err != nil || len(got) != 0 {
		t.Fatalf("ForceCloseAllVaults = %v, %v; want nothing kept", got, err)
	}
	if entries, _ := os.ReadDir(recovery); len(entries) != 0 {
		t.Errorf("recovery folder holds %d entries", len(entries))
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("plaintext left: %v", err)
	}
}

// Without the keys (nothing to encrypt with), the plaintext itself is moved to a clearly
// named folder in the recovery folder: never deleted.
func TestForceCloseWithoutKeysMovesPlaintext(t *testing.T) {
	c, _, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := c.sessions["vault"]
	s.vm = &vaultmgr.Manager{SyncDir: s.vm.SyncDir, CacheRoot: s.vm.CacheRoot} // no keys
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err != nil || len(got) != 1 {
		t.Fatalf("ForceCloseAllVaults = %v, %v", got, err)
	}
	if !strings.Contains(filepath.Base(got[0]), "NOT ENCRYPTED") {
		t.Errorf("plaintext recovery %s is not labelled as unencrypted", got[0])
	}
	b, err := os.ReadFile(filepath.Join(got[0], "folder", "new.txt"))
	if err != nil || string(b) != "unsaved" {
		t.Fatalf("unsaved file not in the recovery folder: %q %v", b, err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("plaintext still at %s", plain)
	}
}

// Plaintext a crash left in the desktop's own plaintext folder blocks an ordinary sign-out
// (it may hold unsaved work), is moved to the recovery folder by a forced one, and saved
// leftovers (.closing-*) go; shared temporary folders must survive.
func TestSweepVaultLeftovers(t *testing.T) {
	c, _ := newTestController(t, &vaultTestServer{nodes: map[string][]byte{}})
	c.vaultPlainRoot, c.tempRoot = t.TempDir(), t.TempDir()
	leftover := filepath.Join(c.vaultPlainRoot, "Secrets-0a1b2c3d")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "note.md"), []byte("crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(c.vaultPlainRoot, ".closing-1"), filepath.Join(c.tempRoot, "ddvopen-1"), filepath.Join(c.tempRoot, "ddvclose-1"), filepath.Join(c.tempRoot, "ddvault-1")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(c.tempRoot, "unrelated")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	recovery := t.TempDir()
	if _, err := c.SweepVaultLeftovers(recovery, false); !errors.Is(err, ErrVaultLeftovers) {
		t.Fatalf("sweep = %v, want ErrVaultLeftovers", err)
	}
	if _, err := os.Stat(filepath.Join(leftover, "note.md")); err != nil {
		t.Fatalf("leftover touched by a refused sweep: %v", err)
	}
	got, err := c.SweepVaultLeftovers(recovery, true)
	if err != nil || len(got) != 1 {
		t.Fatalf("forced sweep = %v, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(got[0], "note.md")); err != nil || string(b) != "crash" {
		t.Fatalf("leftover not recovered: %q %v", b, err)
	}
	for _, d := range []string{c.vaultPlainRoot} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s left: %v", d, err)
		}
	}
	for _, p := range []string{other, filepath.Join(c.tempRoot, "ddvopen-1"), filepath.Join(c.tempRoot, "ddvclose-1"), filepath.Join(c.tempRoot, "ddvault-1")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("an unrelated temp folder was removed: %v", err)
		}
	}
}

// Nothing left: an ordinary sweep passes.
func TestSweepVaultLeftoversNothingLeft(t *testing.T) {
	c, _ := newTestController(t, &vaultTestServer{nodes: map[string][]byte{}})
	c.vaultPlainRoot, c.tempRoot = filepath.Join(t.TempDir(), "none"), t.TempDir()
	if got, err := c.SweepVaultLeftovers(t.TempDir(), false); err != nil || len(got) != 0 {
		t.Fatalf("sweep = %v, %v", got, err)
	}
}

// M-2: the desktop's plaintext folder is its own per server: a vault of another server can
// never find (and restore) another server's leftovers, and the tray's plaintext folder is
// never swept.
func TestVaultPlainRootIsPerServer(t *testing.T) {
	a := vaultPlainRootFor("https://a.example", "/p")
	b := vaultPlainRootFor("https://b.example", "/p")
	shared := filepath.Join(mustCacheDir(t), "discodrive", "open")
	if a == b || a == shared || b == shared || strings.HasPrefix(a, shared+string(filepath.Separator)) {
		t.Fatalf("plain roots a=%s b=%s shared=%s", a, b, shared)
	}
	c, _, plain, _ := openCloseFixtureRoot(t, a)
	if !strings.HasPrefix(plain, a+string(filepath.Separator)) {
		t.Fatalf("plaintext %s not under the server's root %s", plain, a)
	}
	_ = c
}

func mustCacheDir(t *testing.T) string {
	d, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Round 12.

// openSecondVault creates, uploads and opens a second vault named name on c.
func openSecondVault(t *testing.T, c *Controller, name string) string {
	t.Helper()
	ctx := context.Background()
	encrypted, source, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "docs", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(source, encrypted); err != nil {
		t.Fatal(err)
	}
	if err := c.uploadTree(ctx, encrypted, name); err != nil {
		t.Fatal(err)
	}
	p, err := c.openVaultCore(ctx, name, func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
		vm.CacheRoot = cache
		return vm.Open(vi, "pw")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s := c.sessions[name]; s != nil {
			os.RemoveAll(s.tmpDir)
		}
	})
	return p
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// m-2: re-encryption that fails for any reason other than missing keys (an unreadable
// file, a full disk) stops the forced close: nothing is moved out decrypted, the session
// stays open, and the recovery folder stays empty.
func TestForceCloseReencryptFailureMovesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable folders")
	}
	c, _, plain, _ := openCloseFixture(t)
	locked := filepath.Join(plain, "folder")
	if err := os.WriteFile(filepath.Join(locked, "new.txt"), []byte("unsaved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err == nil {
		t.Fatalf("ForceCloseAllVaults = %v, nil; want the re-encryption error", got)
	}
	os.Chmod(locked, 0o700)
	if b, err := os.ReadFile(filepath.Join(locked, "new.txt")); err != nil || string(b) != "unsaved" {
		t.Fatalf("plaintext touched: %q %v", b, err)
	}
	if !c.IsVaultOpen("vault") || len(got) != 0 || len(entries(t, recovery)) != 0 {
		t.Errorf("open=%v kept=%v recovery=%v", c.IsVaultOpen("vault"), got, entries(t, recovery))
	}
}

// m-3: every vault is kept in the recovery folder before any is closed. If one cannot be
// kept, none is closed and the recovery folder holds nothing.
func TestForceCloseAllOrNothingAcrossVaults(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable folders")
	}
	c, srv, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain2 := openSecondVault(t, c, "vault2")
	locked := filepath.Join(plain2, "docs")
	if err := os.WriteFile(filepath.Join(locked, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	c.srv = downServer{srv}
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err == nil {
		t.Fatal("want vault2's error")
	}
	if !c.IsVaultOpen("vault") || !c.IsVaultOpen("vault2") || len(got) != 0 || len(entries(t, recovery)) != 0 {
		t.Errorf("open=%v,%v kept=%v recovery=%v", c.IsVaultOpen("vault"), c.IsVaultOpen("vault2"), got, entries(t, recovery))
	}
	if _, err := os.Stat(filepath.Join(plain, "folder", "new.txt")); err != nil {
		t.Errorf("vault's plaintext touched: %v", err)
	}
}

// m-3: when closing fails after every vault was kept (a file changed in between), the
// vaults that did close are reported with the error, and the copy of the one still open
// is dropped (its plaintext is still there).
func TestForceClosePartialReportsWhatWasKept(t *testing.T) {
	c, srv, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain2 := openSecondVault(t, c, "vault2")
	if err := os.WriteFile(filepath.Join(plain2, "docs", "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.srv = downServer{srv}
	afterKeepingAll = func() {
		os.WriteFile(filepath.Join(plain2, "docs", "late.txt"), []byte("late"), 0o600)
	}
	t.Cleanup(func() { afterKeepingAll = nil })
	recovery := t.TempDir()
	got, err := c.ForceCloseAllVaults(recovery)
	if err == nil {
		t.Fatal("want vault2's close error")
	}
	if c.IsVaultOpen("vault") || !c.IsVaultOpen("vault2") {
		t.Fatalf("open = %v,%v; want vault closed, vault2 open", c.IsVaultOpen("vault"), c.IsVaultOpen("vault2"))
	}
	if len(got) != 1 || !strings.HasPrefix(filepath.Base(got[0]), "vault ") {
		t.Fatalf("kept = %v, want vault's folder only", got)
	}
	if es := entries(t, recovery); len(es) != 1 {
		t.Errorf("recovery = %v, want only vault's folder", es)
	}
	if b, err := os.ReadFile(filepath.Join(plain2, "docs", "late.txt")); err != nil || string(b) != "late" {
		t.Errorf("vault2's plaintext touched: %q %v", b, err)
	}
}

// Nits: a pre-existing recovery folder is made private, a vault name the file system
// rejects is made acceptable, and only decrypted copies are kept out of backups.
func TestRecoveryFolderHygiene(t *testing.T) {
	localname.RestrictForTest(t)
	root := filepath.Join(t.TempDir(), "DiscoDrive Recovery")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var excluded []string
	orig := excludeFromBackup
	excludeFromBackup = func(p string) { excluded = append(excluded, p) }
	t.Cleanup(func() { excludeFromBackup = orig })

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc, err := copyToRecovery(src, root, "a:b", "")
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(root); fi.Mode().Perm() != 0o700 {
		t.Errorf("recovery folder mode = %v, want 0700", fi.Mode().Perm())
	}
	if strings.Contains(filepath.Base(enc), ":") || !strings.HasPrefix(filepath.Base(enc), "a：b ") {
		t.Errorf("recovery name %q not made acceptable", filepath.Base(enc))
	}
	plainSrc := t.TempDir()
	dec, err := moveToRecovery(plainSrc, root, "v", notEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	// Excluded before it moved there (round 13): the sticky exclusion travels with it.
	if len(excluded) != 1 || excluded[0] != plainSrc {
		t.Errorf("excluded from backups = %v, want only %s (moved to %s)", excluded, plainSrc, dec)
	}
}

// m-4: a sign-out whose account never opened still finds that pairing's plaintext folder.
func TestSweepLeftoversForPairing(t *testing.T) {
	profile := t.TempDir()
	root := vaultPlainRootFor("https://a.example", profile)
	if err := os.MkdirAll(filepath.Join(root, "Secrets-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	recovery := t.TempDir()
	if _, err := SweepLeftoversFor("https://a.example", profile, recovery, t.TempDir(), false); !errors.Is(err, ErrVaultLeftovers) {
		t.Fatalf("sweep = %v, want ErrVaultLeftovers", err)
	}
	got, err := SweepLeftoversFor("https://a.example", profile, recovery, t.TempDir(), true)
	if err != nil || len(got) != 1 {
		t.Fatalf("forced sweep = %v, %v", got, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("plaintext root left: %v", err)
	}
}

// Round 13.

// n-1 (probe R12): without keys, an edit made between the copy and the close is not lost:
// the decrypted folder itself ends up in the recovery folder. Same volume (a rename) and
// across volumes (a rename that fails: copy again, check, then remove).
func TestForceCloseNoKeysKeepsLateEdit(t *testing.T) {
	for _, crossVolume := range []bool{false, true} {
		c, _, plain, _ := openCloseFixture(t)
		if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := c.sessions["vault"]
		s.vm = &vaultmgr.Manager{SyncDir: s.vm.SyncDir, CacheRoot: s.vm.CacheRoot} // no keys
		afterKeepingAll = func() {
			os.WriteFile(filepath.Join(plain, "folder", "late.txt"), []byte("late"), 0o600)
		}
		origRename := renameDir
		if crossVolume {
			renameDir = func(string, string) error { return errors.New("invalid cross-device link") }
		}
		recovery := t.TempDir()
		got, err := c.ForceCloseAllVaults(recovery)
		afterKeepingAll, renameDir = nil, origRename
		if err != nil || len(got) != 1 {
			t.Fatalf("crossVolume=%v: ForceCloseAllVaults = %v, %v", crossVolume, got, err)
		}
		for name, want := range map[string]string{"new.txt": "unsaved", "late.txt": "late"} {
			if b, err := os.ReadFile(filepath.Join(got[0], "folder", name)); err != nil || string(b) != want {
				t.Errorf("crossVolume=%v: %s in the recovery folder = %q, %v", crossVolume, name, b, err)
			}
		}
		if _, err := os.Stat(plain); !os.IsNotExist(err) {
			t.Errorf("crossVolume=%v: plaintext left: %v", crossVolume, err)
		}
		if es := entries(t, recovery); len(es) != 1 {
			t.Errorf("crossVolume=%v: recovery = %v, want one folder", crossVolume, es)
		}
	}
}

// n-2: a decrypted folder is excluded from backups before any decrypted file is written
// into it, or before it is moved into the recovery folder.
func TestNotEncryptedExcludedBeforeWriting(t *testing.T) {
	type call struct {
		path  string
		files int
	}
	var calls []call
	orig := excludeFromBackup
	excludeFromBackup = func(p string) {
		n := 0
		filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
			if err == nil && fi.Mode().IsRegular() {
				n++
			}
			return nil
		})
		calls = append(calls, call{p, n})
	}
	t.Cleanup(func() { excludeFromBackup = orig })
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dst, err := copyToRecovery(src, root, "v", notEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].path != dst || calls[0].files != 0 {
		t.Errorf("copy: exclusions = %+v, want %s excluded while still empty", calls, dst)
	}
	calls = nil
	moved, err := moveToRecovery(src, root, "w", notEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].path != src {
		t.Errorf("move: exclusions = %+v, want the source %s excluded before it moved to %s", calls, src, moved)
	}
}

func TestForceCloseRetriesDetachedCleanup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	c, _, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "folder", "new.txt"), []byte("unsaved"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := c.sessions["vault"].vm.CacheRoot
	unlock := func() {
		_ = os.Chmod(filepath.Join(plain, "folder"), 0700)
		paths, _ := filepath.Glob(filepath.Join(cache, ".closing-*", "contents", "folder"))
		for _, p := range paths {
			_ = os.Chmod(p, 0700)
		}
	}
	t.Cleanup(unlock)
	if err := os.Chmod(filepath.Join(plain, "folder"), 0500); err != nil {
		t.Fatal(err)
	}
	recovery := t.TempDir()
	kept, err := c.ForceCloseAllVaults(recovery)
	if err == nil || len(kept) != 1 {
		t.Fatalf("expected kept copy plus cleanup failure: %v %v", kept, err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("expected detached plaintext: %v", err)
	}
	// Another failure must preserve and report the same recovery, without trying
	// to encrypt the detached (possibly partly removed) tree again.
	again, err := c.ForceCloseAllVaults(recovery)
	if err == nil || len(again) != 1 || again[0] != kept[0] {
		t.Fatalf("lost recovery on repeat failure: %v %v", again, err)
	}
	if _, err := os.Stat(kept[0]); err != nil {
		t.Fatal(err)
	}
	unlock()
	finished, err := c.ForceCloseAllVaults(recovery)
	if err != nil {
		t.Fatalf("cleanup retry failed: %v", err)
	}
	if len(finished) != 1 || finished[0] != kept[0] {
		t.Fatalf("recovery not reported on completion: %v", finished)
	}
	if len(c.sessions) != 0 {
		t.Fatal("session still open")
	}
	if _, err := os.Stat(kept[0]); err != nil {
		t.Fatalf("lost recovery: %v", err)
	}
}
