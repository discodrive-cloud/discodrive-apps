package vault

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantEntry writes a vault entry whose decrypted name is `name`, bypassing
// EncryptName's validation — this is what a hostile vault member can produce with
// any Cryptomator implementation that does not validate names.
func plantEntry(t *testing.T, v *Vault, vaultDir, name string, content []byte) {
	t.Helper()
	ct, err := sivEncrypt(v.macKey, v.encKey, []byte(name), []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	hashPath, err := v.DirIdHash("")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(vaultDir, hashPath, base64.URLEncoding.EncodeToString(ct)+".c9r"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := v.EncryptContent(f, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
}

var hostileNames = []string{"../escaped.txt", "../../escaped.txt", "..", ".", "", "a/b.txt", "nul\x00.txt"}

func TestDecryptTreeSkipsHostileNames(t *testing.T) {
	src := t.TempDir()
	mustWriteFile(t, filepath.Join(src, "ok.txt"), []byte("fine"))
	vaultDir := t.TempDir()
	v, err := Create(vaultDir, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(src, vaultDir); err != nil {
		t.Fatal(err)
	}
	for _, n := range hostileNames {
		plantEntry(t, v, vaultDir, n, []byte("pwned"))
	}

	base := t.TempDir()
	out := filepath.Join(base, "a", "plain")
	if err := v.DecryptTree(vaultDir, out); err != nil {
		t.Fatalf("DecryptTree must skip hostile entries, not fail: %v", err)
	}
	for _, p := range []string{filepath.Join(base, "a", "escaped.txt"), filepath.Join(base, "escaped.txt"), filepath.Join(out, "a")} {
		if fileExists(p) {
			t.Fatalf("hostile entry written outside/unexpectedly: %s", p)
		}
	}
	if b, err := os.ReadFile(filepath.Join(out, "ok.txt")); err != nil || string(b) != "fine" {
		t.Fatalf("legit file lost: %q %v", b, err)
	}
}

func TestListDirSkipsHostileNames(t *testing.T) {
	vaultDir := t.TempDir()
	v, err := Create(vaultDir, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(t.TempDir(), vaultDir); err != nil {
		t.Fatal(err)
	}
	for _, n := range hostileNames {
		plantEntry(t, v, vaultDir, n, []byte("pwned"))
	}
	entries, err := v.ListDir(OSIO{Root: vaultDir}, "")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("hostile entries listed: %+v", entries)
	}
}

func TestEncryptNameRejectsHostileNames(t *testing.T) {
	v, err := Create(t.TempDir(), "pw")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range hostileNames {
		if _, err := v.EncryptName(n, ""); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("EncryptName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
	if _, err := v.EncryptName("fine name.txt", ""); err != nil {
		t.Fatal(err)
	}
}

// A Cryptomator symlink entry (a <name>.c9r folder holding symlink.c9r) is skipped,
// not a reason to fail the whole open.
func TestSymlinkEntriesAreSkipped(t *testing.T) {
	src := t.TempDir()
	mustWriteFile(t, filepath.Join(src, "ok.txt"), []byte("fine"))
	vaultDir := t.TempDir()
	v, err := Create(vaultDir, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(src, vaultDir); err != nil {
		t.Fatal(err)
	}
	enc, err := v.EncryptName("link", "")
	if err != nil {
		t.Fatal(err)
	}
	hashPath, _ := v.DirIdHash("")
	link := filepath.Join(vaultDir, hashPath, enc)
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(link, "symlink.c9r"), []byte("target"))
	out := t.TempDir()
	if err := v.DecryptTree(vaultDir, out); err != nil {
		t.Fatalf("DecryptTree: %v", err)
	}
	if entries, err := v.ListDir(OSIO{Root: vaultDir}, ""); err != nil || len(entries) != 1 {
		t.Fatalf("ListDir = %+v, %v", entries, err)
	}
}

// Conflict copies of ciphertext made by sync must not make the vault unopenable.
func TestConflictCopiesOfCiphertextAreSkipped(t *testing.T) {
	src := t.TempDir()
	mustWriteFile(t, filepath.Join(src, "ok.txt"), []byte("fine"))
	vaultDir := t.TempDir()
	v, _ := Create(vaultDir, "pw")
	if err := v.EncryptTree(src, vaultDir); err != nil {
		t.Fatal(err)
	}
	hashPath, _ := v.DirIdHash("")
	entries, _ := os.ReadDir(filepath.Join(vaultDir, hashPath))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".c9r") && e.Name() != "dirid.c9r" {
			b, _ := os.ReadFile(filepath.Join(vaultDir, hashPath, e.Name()))
			stem := strings.TrimSuffix(e.Name(), ".c9r")
			mustWriteFile(t, filepath.Join(vaultDir, hashPath, stem+" (conflict, local, 2026-09-25 12-00-00).c9r"), b)
		}
	}
	if err := v.DecryptTree(vaultDir, t.TempDir()); err != nil {
		t.Fatalf("DecryptTree: %v", err)
	}
	if got, err := v.ListDir(OSIO{Root: vaultDir}, ""); err != nil || len(got) != 1 {
		t.Fatalf("ListDir = %+v, %v", got, err)
	}
}
