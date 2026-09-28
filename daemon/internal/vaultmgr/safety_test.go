package vaultmgr_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/vaultmgr"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Two vaults with the same folder name (e.g. A/Secrets and B/Secrets, or one from the
// tray and one from the desktop app) must never share a plaintext folder: closing one
// would encrypt the other's files with its own key.
func TestSameNameVaultsGetSeparatePlaintext(t *testing.T) {
	cache := t.TempDir()
	m1 := &vaultmgr.Manager{SyncDir: t.TempDir(), CacheRoot: cache}
	m2 := &vaultmgr.Manager{SyncDir: t.TempDir(), CacheRoot: cache}
	a, err := m1.Create("Secrets", "pa")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m2.Create("Secrets", "pb")
	if err != nil {
		t.Fatal(err)
	}
	pa, err := m1.Open(a, "pa")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pa, "note.txt"), "A")
	pb, err := m2.Open(b, "pb")
	if err != nil {
		t.Fatal(err)
	}
	if pa == pb {
		t.Fatalf("both vaults decrypt into %s", pa)
	}
	writeFile(t, filepath.Join(pb, "note.txt"), "B")
	if err := m1.Close(a); err != nil {
		t.Fatal(err)
	}
	pa2, err := m1.Open(a, "pa")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(pa2, "note.txt")); got != "A" {
		t.Fatalf("vault A holds %q after closing next to B", got)
	}
}

// A crash (or a failed close) leaves edited plaintext behind without keys. Reopening
// must not decrypt over it: the edits come back inside the vault, so the next close
// encrypts them instead of losing them.
func TestReopenAfterCrashKeepsUnsavedEdits(t *testing.T) {
	m := newManager(t)
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "same.txt"), "stable")
	writeFile(t, filepath.Join(pd, "doc.txt"), "v1")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "doc.txt"), "v2 unsaved")
	writeFile(t, filepath.Join(pd, "sub", "new.txt"), "brand new")

	// Crash: a new process has no keys for the open vault.
	m2 := &vaultmgr.Manager{SyncDir: m.SyncDir, CacheRoot: m.CacheRoot}
	pd2, err := m2.Open(vi, "pw")
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	if got := readFile(t, filepath.Join(pd2, "doc.txt")); got != "v1" {
		t.Fatalf("doc.txt = %q, want the saved v1", got)
	}
	rec, _ := filepath.Glob(filepath.Join(pd2, vaultmgr.RecoveredPrefix+"*"))
	if len(rec) != 1 {
		t.Fatalf("want one recovered folder, got %v", rec)
	}
	if got := readFile(t, filepath.Join(rec[0], "doc.txt")); got != "v2 unsaved" {
		t.Fatalf("recovered doc.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(rec[0], "sub", "new.txt")); got != "brand new" {
		t.Fatalf("recovered new.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(rec[0], "same.txt")); !os.IsNotExist(err) {
		t.Fatalf("unchanged files must not be duplicated into the recovered folder")
	}
	// The recovered files are ordinary edits: closing saves them into the vault.
	if err := m2.Close(vi); err != nil {
		t.Fatal(err)
	}
	pd3, err := m2.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ = filepath.Glob(filepath.Join(pd3, vaultmgr.RecoveredPrefix+"*", "doc.txt"))
	if len(rec) != 1 || readFile(t, rec[0]) != "v2 unsaved" {
		t.Fatalf("recovered edits were not saved into the vault: %v", rec)
	}
	if left, _ := filepath.Glob(filepath.Join(m.CacheRoot, ".*")); len(left) != 0 {
		t.Fatalf("stash left behind: %v", left)
	}
}

// A failed open must not leave plaintext behind, and must put back any leftovers it
// moved aside.
func TestFailedOpenLeavesNoPlaintext(t *testing.T) {
	m := newManager(t)
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "a.txt"), "saved")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	// Corrupt every ciphertext file so decryption fails part-way.
	filepath.Walk(filepath.Join(vi.Dir, "d"), func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() && strings.HasSuffix(p, ".c9r") {
			os.WriteFile(p, []byte("garbage that is long enough to be a header but is not one at all......"), 0o644)
		}
		return nil
	})
	if _, err := m.Open(vi, "pw"); err == nil {
		t.Fatal("open of a corrupt vault must fail")
	}
	if left, _ := os.ReadDir(m.CacheRoot); len(left) != 0 {
		t.Fatalf("failed open left plaintext: %v", left)
	}
}

// Closing in the tray runs next to the live syncer. It must only touch the ciphertext
// of what changed — never delete the whole d/ tree and rebuild it with new ids — and
// must build its staging tree outside the sync folder.
func TestCloseRewritesOnlyChangedCiphertext(t *testing.T) {
	m := newManager(t)
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "keep.txt"), "keep")
	writeFile(t, filepath.Join(pd, "dir", "inner.txt"), "inner")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	before := map[string]os.FileInfo{}
	filepath.Walk(vi.Dir, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			before[p] = fi
		}
		return nil
	})

	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "added.txt"), "added")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	for p, fi := range before {
		now, err := os.Stat(p)
		if err != nil {
			t.Fatalf("unchanged ciphertext %s was removed: %v", p, err)
		}
		if !os.SameFile(fi, now) {
			t.Fatalf("unchanged ciphertext %s was rewritten", p)
		}
	}
	if st, _ := filepath.Glob(filepath.Join(m.SyncDir, "ddvclose-*")); len(st) != 0 {
		t.Fatalf("staging tree created inside the sync folder: %v", st)
	}
	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"keep.txt": "keep", "dir/inner.txt": "inner", "added.txt": "added"} {
		if got := readFile(t, filepath.Join(pd, f)); got != want {
			t.Fatalf("%s = %q, want %q", f, got, want)
		}
	}
}

func cipherFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(filepath.Join(dir, "d"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			b, _ := os.ReadFile(p)
			out[p] = string(b)
		}
		return nil
	})
	return out
}

// While the tray has a vault open, the syncer keeps pulling: another device may add
// entries to it. Closing must not delete what it never saw.
func TestCloseKeepsEntriesAddedElsewhere(t *testing.T) {
	m := newManager(t)
	vi, _ := m.Create("v", "pw")
	pd, _ := m.Open(vi, "pw")
	writeFile(t, filepath.Join(pd, "mine.txt"), "mine")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	// "Another device": open the same vault through a second manager/cache and add a file.
	other := &vaultmgr.Manager{SyncDir: m.SyncDir, CacheRoot: t.TempDir()}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	opd, err := other.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(opd, "theirs.txt"), "theirs")
	if err := other.Close(vi); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "mine2.txt"), "mine2")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"mine.txt": "mine", "mine2.txt": "mine2", "theirs.txt": "theirs"} {
		if got := readFile(t, filepath.Join(pd, f)); got != want {
			t.Fatalf("%s = %q", f, got)
		}
	}
}

// The same entry edited here and elsewhere while open: closing must not silently put
// back an older copy over theirs — it refuses and keeps the plaintext.
func TestCloseRefusesWhenAnEntryChangedElsewhere(t *testing.T) {
	m := newManager(t)
	vi, _ := m.Create("v", "pw")
	pd, _ := m.Open(vi, "pw")
	writeFile(t, filepath.Join(pd, "doc.txt"), "v1")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	other := &vaultmgr.Manager{SyncDir: m.SyncDir, CacheRoot: t.TempDir()}
	pd, _ = m.Open(vi, "pw")
	opd, _ := other.Open(vi, "pw")
	writeFile(t, filepath.Join(opd, "doc.txt"), "theirs")
	if err := other.Close(vi); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pd, "doc.txt"), "mine")
	if err := m.Close(vi); err == nil {
		t.Fatal("close must refuse to overwrite an entry changed elsewhere")
	}
	if got := readFile(t, filepath.Join(pd, "doc.txt")); got != "mine" {
		t.Fatalf("plaintext lost: %q", got)
	}
	other2 := &vaultmgr.Manager{SyncDir: m.SyncDir, CacheRoot: t.TempDir()}
	opd, _ = other2.Open(vi, "pw")
	if got := readFile(t, filepath.Join(opd, "doc.txt")); got != "theirs" {
		t.Fatalf("their edit was overwritten: %q", got)
	}
}

// A file replaced by a folder of the same name (and back) maps to the same ciphertext
// path; closing must handle the type change.
func TestCloseHandlesFileFolderSwap(t *testing.T) {
	m := newManager(t)
	vi, _ := m.Create("v", "pw")
	pd, _ := m.Open(vi, "pw")
	writeFile(t, filepath.Join(pd, "x"), "file")
	if err := m.Close(vi); err != nil {
		t.Fatal(err)
	}
	pd, _ = m.Open(vi, "pw")
	os.Remove(filepath.Join(pd, "x"))
	writeFile(t, filepath.Join(pd, "x", "inner.txt"), "inner")
	if err := m.Close(vi); err != nil {
		t.Fatalf("file → folder: %v", err)
	}
	pd, _ = m.Open(vi, "pw")
	os.RemoveAll(filepath.Join(pd, "x"))
	writeFile(t, filepath.Join(pd, "x"), "file again")
	if err := m.Close(vi); err != nil {
		t.Fatalf("folder → file: %v", err)
	}
	pd, _ = m.Open(vi, "pw")
	if got := readFile(t, filepath.Join(pd, "x")); got != "file again" {
		t.Fatalf("x = %q", got)
	}
}

// Plaintext a crash left under the pre-hash folder name is recovered too.
func TestReopenRecoversLegacyPlaintextFolder(t *testing.T) {
	m := newManager(t)
	vi, _ := m.Create("v", "pw")
	writeFile(t, filepath.Join(m.CacheRoot, "v", "old edit.txt"), "unsaved")
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := filepath.Glob(filepath.Join(pd, vaultmgr.RecoveredPrefix+"*", "old edit.txt"))
	if len(rec) != 1 {
		t.Fatalf("legacy plaintext not recovered")
	}
	if _, err := os.Stat(filepath.Join(m.CacheRoot, "v")); !os.IsNotExist(err) {
		t.Fatalf("legacy folder left behind")
	}
}

// If recovering fails part-way, the leftovers must be intact, not half moved into a
// folder that the failed open then deletes.
func TestFailedRecoveryKeepsLeftovers(t *testing.T) {
	m := newManager(t)
	vi, _ := m.Create("v", "pw")
	pd, _ := m.Open(vi, "pw")
	m.Close(vi)
	pd, _ = m.Open(vi, "pw")
	writeFile(t, filepath.Join(pd, "a.txt"), "unsaved a")
	writeFile(t, filepath.Join(pd, "z", "b.txt"), "unsaved b")
	os.Chmod(filepath.Join(pd, "z"), 0) // unreadable: the walk fails after a.txt
	t.Cleanup(func() { os.Chmod(filepath.Join(pd, "z"), 0o700) })
	m2 := &vaultmgr.Manager{SyncDir: m.SyncDir, CacheRoot: m.CacheRoot}
	if _, err := m2.Open(vi, "pw"); err == nil {
		t.Skip("walk did not fail (running as root?)")
	}
	os.Chmod(filepath.Join(pd, "z"), 0o700)
	if got := readFile(t, filepath.Join(pd, "a.txt")); got != "unsaved a" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(pd, "z", "b.txt")); got != "unsaved b" {
		t.Fatalf("b.txt = %q", got)
	}
}
