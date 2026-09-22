package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

func TestVaultCloseUnchangedDoesNotUpload(t *testing.T) {
	ctx := context.Background()
	srv := &vaultTestServer{nodes: map[string][]byte{}}
	c, _ := newTestController(t, srv)
	encrypted, source, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "large"), []byte(strings.Repeat("x", 1<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(source, encrypted); err != nil {
		t.Fatal(err)
	}
	if err := c.uploadTree(ctx, encrypted, "vault"); err != nil {
		t.Fatal(err)
	}
	_, err = c.openVaultCore(ctx, "vault", func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
		vm.CacheRoot = cache
		return vm.Open(vi, "pw")
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(srv.changes)
	if err := c.CloseVault(ctx, "vault"); err != nil {
		t.Fatal(err)
	}
	var uploaded int64
	for _, ch := range srv.changes[before:] {
		if !ch.IsDir {
			uploaded += ch.Size
		}
	}
	if uploaded != 0 || len(srv.changes) != before {
		t.Fatalf("unchanged vault uploaded %d bytes, %d mutations", uploaded, len(srv.changes)-before)
	}
	if c.IsVaultOpen("vault") {
		t.Fatal("unchanged session remained open")
	}
}

// All assertions observe the same server changes feed used by the real close path.
func TestVaultCloseDelta(t *testing.T) {
	cases := []struct {
		name     string
		edit     func(string) error
		maxBytes int64
	}{
		{"same size and mtime", func(p string) error {
			f := filepath.Join(p, "folder", "keep")
			st, err := os.Stat(f)
			if err != nil {
				return err
			}
			if err := os.WriteFile(f, []byte(strings.Repeat("y", 1<<20)), 0600); err != nil {
				return err
			}
			return os.Chtimes(f, st.ModTime(), st.ModTime())
		}, 2 << 20},
		{"add file", func(p string) error { return os.WriteFile(filepath.Join(p, "other", "new"), []byte("added"), 0600) }, 1000},
		{"file to folder", func(p string) error {
			f := filepath.Join(p, "folder", "keep")
			if err := os.Remove(f); err != nil {
				return err
			}
			return os.Mkdir(f, 0700)
		}, 1000},
		{"folder to file", func(p string) error {
			f := filepath.Join(p, "folder")
			if err := os.RemoveAll(f); err != nil {
				return err
			}
			return os.WriteFile(f, []byte("replacement"), 0600)
		}, 1000},
		{"swap folders", func(p string) error {
			a, b, tmp := filepath.Join(p, "folder"), filepath.Join(p, "other"), filepath.Join(p, "temp")
			if err := os.Rename(a, tmp); err != nil {
				return err
			}
			if err := os.Rename(b, a); err != nil {
				return err
			}
			return os.Rename(tmp, b)
		}, 1000},
		{"edit", func(p string) error { return os.WriteFile(filepath.Join(p, "folder", "keep"), []byte("edited"), 0600) }, 1000},
		{"delete", func(p string) error { return os.Remove(filepath.Join(p, "folder", "keep")) }, 0},
		{"rename folder", func(p string) error { return os.Rename(filepath.Join(p, "folder"), filepath.Join(p, "renamed")) }, 1000},
		{"move folder", func(p string) error { return os.Rename(filepath.Join(p, "folder"), filepath.Join(p, "other", "moved")) }, 1000},
		{"rename file", func(p string) error {
			return os.Rename(filepath.Join(p, "folder", "keep"), filepath.Join(p, "folder", "renamed"))
		}, 2 << 20},
		{"long names", func(p string) error {
			if err := os.Mkdir(filepath.Join(p, strings.Repeat("d", 180)), 0700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(p, strings.Repeat("d", 180), strings.Repeat("f", 180)), []byte("new"), 0600)
		}, 2000},
		{"delete folder", func(p string) error { return os.RemoveAll(filepath.Join(p, "folder")) }, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, srv, plain, open := openCloseFixture(t)
			if err := tc.edit(plain); err != nil {
				t.Fatal(err)
			}
			want := readPlainTree(t, plain)
			before := len(srv.changes)
			if err := c.CloseVault(context.Background(), "vault"); err != nil {
				t.Fatal(err)
			}
			var uploaded int64
			for _, ch := range srv.changes[before:] {
				if !ch.IsDir && !ch.Deleted {
					uploaded += ch.Size
				}
			}
			t.Logf("uploaded %d bytes", uploaded)
			if uploaded > tc.maxBytes {
				t.Fatalf("uploaded %d, limit %d", uploaded, tc.maxBytes)
			}
			reopened := open()
			got := readPlainTree(t, reopened)
			if !reflect.DeepEqual(want, got) {
				t.Fatal("plaintext differs after close/reopen")
			}
		})
	}
}

func openCloseFixture(t *testing.T) (*Controller, *vaultTestServer, string, func() string) {
	t.Helper()
	ctx := context.Background()
	srv := &vaultTestServer{nodes: map[string][]byte{}}
	c, _ := newTestController(t, srv)
	encrypted, source, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"folder", "other"} {
		if err := os.Mkdir(filepath.Join(source, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"folder/keep", "other/untouched"} {
		if err := os.WriteFile(filepath.Join(source, p), []byte(strings.Repeat("x", 1<<20)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.EncryptTree(source, encrypted); err != nil {
		t.Fatal(err)
	}
	if err := c.uploadTree(ctx, encrypted, "vault"); err != nil {
		t.Fatal(err)
	}
	open := func() string {
		t.Helper()
		p, err := c.openVaultCore(ctx, "vault", func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
			vm.CacheRoot = cache
			return vm.Open(vi, "pw")
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Cleanup(func() {
		for _, s := range c.sessions {
			os.RemoveAll(s.tmpDir)
			if s.pending != nil && s.pending.Dir != s.tmpDir {
				os.RemoveAll(s.pending.Dir)
			}
		}
	})
	return c, srv, open(), open
}
func readPlainTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			out[rel] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type interruptedCloseServer struct {
	ServerAPI
	failAfter bool
	once      bool
	hook      func()
}

func (s *interruptedCloseServer) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, mod time.Time) (engine.RemoteNode, bool, error) {
	if !s.once {
		s.once = true
		if s.hook != nil {
			s.hook()
		}
		if s.failAfter {
			_, _, err := s.ServerAPI.PushFile(ctx, rel, base, r, mod)
			if err != nil {
				return engine.RemoteNode{}, false, err
			}
			return engine.RemoteNode{}, false, errors.New("lost reply")
		}
		if s.hook == nil {
			return engine.RemoteNode{}, false, errors.New("offline")
		}
	}
	return s.ServerAPI.PushFile(ctx, rel, base, r, mod)
}
func TestVaultCloseRetry(t *testing.T) {
	for _, lostReply := range []bool{false, true} {
		t.Run(fmt.Sprint(lostReply), func(t *testing.T) {
			c, srv, plain, open := openCloseFixture(t)
			if err := os.WriteFile(filepath.Join(plain, "folder", "keep"), []byte("edited"), 0600); err != nil {
				t.Fatal(err)
			}
			c.srv = &interruptedCloseServer{ServerAPI: srv, failAfter: lostReply}
			if err := c.CloseVault(context.Background(), "vault"); err == nil {
				t.Fatal("expected network error")
			}
			if _, err := os.Stat(plain); err != nil {
				t.Fatal("plaintext removed on failed close")
			}
			if !c.IsVaultOpen("vault") {
				t.Fatal("failed session lost")
			}
			if err := c.CloseVault(context.Background(), "vault"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(open(), "folder", "keep"))
			if err != nil || string(b) != "edited" {
				t.Fatalf("edit lost: %s %v", b, err)
			}
		})
	}
}
func TestVaultClosePreservesEditsDuringUpload(t *testing.T) {
	c, srv, plain, open := openCloseFixture(t)
	file := filepath.Join(plain, "folder", "keep")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	c.srv = &interruptedCloseServer{ServerAPI: srv, hook: func() {
		if err := os.WriteFile(file, []byte("second"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if err := c.CloseVault(context.Background(), "vault"); err == nil {
		t.Fatal("expected changed plaintext error")
	}
	b, err := os.ReadFile(file)
	if err != nil || string(b) != "second" {
		t.Fatal("new edit lost")
	}
	if err := c.CloseVault(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(open(), "folder", "keep"))
	if err != nil || string(b) != "second" {
		t.Fatal("new edit not saved")
	}
}

func TestVaultCloseRejectsRemoteEdit(t *testing.T) {
	c, srv, plain, _ := openCloseFixture(t)
	local := filepath.Join(plain, "folder", "keep")
	if err := os.WriteFile(local, []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	var target string
	for p, id := range srv.relToNode {
		if len(srv.nodes[id]) > 1<<20 {
			target = p
			break
		}
	}
	if target == "" {
		t.Fatal("no remote content")
	}
	if _, _, err := srv.PushFile(context.Background(), target, nil, strings.NewReader("remote edit"), time.Now()); err != nil {
		t.Fatal(err)
	}
	before := len(srv.changes)
	if err := c.CloseVault(context.Background(), "vault"); err == nil {
		t.Fatal("remote edit silently overwritten")
	}
	if len(srv.changes) != before {
		t.Fatal("wrote despite remote edit")
	}
	b, err := os.ReadFile(local)
	if err != nil || string(b) != "local edit" {
		t.Fatal("local edit lost")
	}
}

func TestVaultOpenKeepsDownloadedVersions(t *testing.T) {
	c, srv, _, _ := openCloseFixture(t)
	if err := c.CloseVault(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	plain, err := c.openVaultCore(context.Background(), "vault", func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
		vm.CacheRoot = cache
		p, err := vm.Open(vi, "pw")
		if err != nil {
			return "", err
		}
		// Another index refresh can complete after download, before OpenVault returns.
		var target string
		for rel, id := range srv.relToNode {
			if len(srv.nodes[id]) > 1<<20 {
				target = rel
				break
			}
		}
		if _, _, err := srv.PushFile(context.Background(), target, nil, strings.NewReader("remote edit"), time.Now()); err != nil {
			return "", err
		}
		if _, err := c.Refresh(context.Background()); err != nil {
			return "", err
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "folder", "keep"), []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(srv.changes)
	if err := c.CloseVault(context.Background(), "vault"); err == nil {
		t.Fatal("adopted new index versions for old downloaded bytes")
	}
	if len(srv.changes) != before {
		t.Fatal("remote contents overwritten")
	}
}

func TestVaultCloseCleanupFailureNeverUploadsDeletions(t *testing.T) {
	c, srv, plain, _ := openCloseFixture(t)
	// Readable but not writable: hashing succeeds, deletion of its children fails.
	if err := os.Chmod(filepath.Join(plain, "folder"), 0500); err != nil {
		t.Fatal(err)
	}
	before := len(srv.changes)
	if err := c.CloseVault(context.Background(), "vault"); err == nil {
		t.Fatal("expected cleanup failure")
	}
	s := c.sessions["vault"]
	if s == nil || s.finishing == nil || !s.finishing.CleanupStarted() {
		t.Fatal("cleanup phase lost")
	}
	roots, err := filepath.Glob(filepath.Join(s.vm.CacheRoot, ".closing-*"))
	if err != nil || len(roots) != 1 {
		t.Fatalf("cleanup roots: %v %v", roots, err)
	}
	if err := filepath.Walk(roots[0], func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(p, 0700)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseVault(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	if len(srv.changes) != before {
		t.Fatal("cleanup retry modified the remote vault")
	}
	if c.IsVaultOpen("vault") {
		t.Fatal("cleanup did not finish")
	}
}
