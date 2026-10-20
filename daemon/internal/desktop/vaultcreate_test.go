package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

// seededVaultServer is a vaultTestServer holding a plain folder "Documents" with files,
// an empty folder "Empty", a file "notes.txt" and a folder "Work".
func seededVaultServer(t *testing.T) *vaultTestServer {
	t.Helper()
	ctx := context.Background()
	srv := &vaultTestServer{nodes: map[string][]byte{}, relToNode: map[string]string{}}
	for _, d := range []string{"Documents", "Documents/Taxes", "Empty", "Work"} {
		if _, err := srv.EnsureDir(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"Documents/Car insurance.pdf", "Documents/Taxes/2025.pdf", "notes.txt"} {
		if _, _, err := srv.PushFile(ctx, f, nil, strings.NewReader("plain "+f), time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

// A vault written into an existing folder mixes ciphertext with the user's plain files
// (and the folder then passes for a vault); onto a file it cannot be created at all.
// Either way nothing may reach the server.
func TestCreateVaultRejectsTakenName(t *testing.T) {
	for _, tc := range []struct{ parent, name string }{
		{"", "Documents"},
		{"", "documents"},
		{"", "Empty"},
		{"", "notes.txt"},
		{"Documents", "Taxes"},
		{"Documents", "Car insurance.pdf"},
	} {
		t.Run(tc.parent+"/"+tc.name, func(t *testing.T) {
			srv := seededVaultServer(t)
			c, _ := newTestController(t, srv)
			before := len(srv.changes)
			_, err := c.CreateVault(context.Background(), tc.parent, tc.name, "pw")
			if !errors.Is(err, ErrVaultNameTaken) {
				t.Fatalf("CreateVault(%q, %q) = %v, want ErrVaultNameTaken", tc.parent, tc.name, err)
			}
			if got := len(srv.changes) - before; got != 0 {
				t.Fatalf("rejected CreateVault still made %d server changes: %+v", got, srv.changes[before:])
			}
			vaults, err := c.ListVaults()
			if err != nil {
				t.Fatal(err)
			}
			if len(vaults) != 0 {
				t.Fatalf("rejected CreateVault left vaults %+v", vaults)
			}
		})
	}
}

func TestCreateVaultInParentFolder(t *testing.T) {
	ctx := context.Background()
	srv := seededVaultServer(t)
	c, _ := newTestController(t, srv)
	phrase, err := c.CreateVault(ctx, "Work", "Secret", "pw")
	if err != nil {
		t.Fatalf("CreateVault: %v", err)
	}
	if phrase == "" {
		t.Fatal("no recovery phrase")
	}
	for _, p := range []string{"Work/Secret", "Work/Secret/vault.cryptomator", "Work/Secret/masterkey.cryptomator"} {
		if _, ok := srv.relToNode[p]; !ok {
			t.Fatalf("server has no %q", p)
		}
	}
	for p := range srv.relToNode {
		if strings.HasPrefix(p, "Secret") {
			t.Fatalf("vault leaked to the root: %q", p)
		}
	}
	vaults, err := c.ListVaults()
	if err != nil {
		t.Fatal(err)
	}
	if len(vaults) != 1 || vaults[0].RelPath != "Work/Secret" || vaults[0].Name != "Secret" {
		t.Fatalf("ListVaults = %+v, want Work/Secret", vaults)
	}
}

// The parent comes from a folder picker; one deleted meanwhile, or a file, must not be
// recreated as a folder chain by EnsureDir.
func TestCreateVaultRejectsBadParentOrName(t *testing.T) {
	for _, tc := range []struct {
		parent, name string
		want         error
	}{
		{"Gone", "V", ErrVaultParentMissing},
		{"notes.txt", "V", ErrVaultParentMissing},
		{"../x", "V", vault.ErrInvalidName},
		{"/Work", "V", vault.ErrInvalidName},
		{"", "a/b", vault.ErrInvalidName},
		{"", "..", vault.ErrInvalidName},
		{"", `a\b`, vault.ErrInvalidName},
	} {
		t.Run(tc.parent+"|"+tc.name, func(t *testing.T) {
			srv := seededVaultServer(t)
			c, _ := newTestController(t, srv)
			before := len(srv.changes)
			_, err := c.CreateVault(context.Background(), tc.parent, tc.name, "pw")
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateVault(%q, %q) = %v, want %v", tc.parent, tc.name, err, tc.want)
			}
			if got := len(srv.changes) - before; got != 0 {
				t.Fatalf("rejected CreateVault still made %d server changes", got)
			}
		})
	}
}

// A vault folder may already hold the user's plain files (made by an older client, or
// by hand). Opening, editing and closing it must leave those files alone.
func TestCloseVaultKeepsForeignFiles(t *testing.T) {
	ctx := context.Background()
	srv := seededVaultServer(t)
	c, _ := newTestController(t, srv)
	encrypted, cache := t.TempDir(), t.TempDir()
	if _, err := vault.Create(encrypted, "pw"); err != nil {
		t.Fatal(err)
	}
	// What the old CreateVault did: upload straight into the existing folder.
	if err := c.uploadTree(ctx, encrypted, "Documents"); err != nil {
		t.Fatal(err)
	}
	foreign := map[string]string{}
	for _, p := range []string{"Documents/Taxes", "Documents/Car insurance.pdf", "Documents/Taxes/2025.pdf"} {
		foreign[p] = srv.relToNode[p]
	}
	for _, step := range []func(plain string) error{
		func(plain string) error { return os.WriteFile(filepath.Join(plain, "secret.txt"), []byte("one"), 0o600) },
		func(plain string) error { return os.WriteFile(filepath.Join(plain, "secret.txt"), []byte("two"), 0o600) },
		func(plain string) error { return os.Remove(filepath.Join(plain, "secret.txt")) },
	} {
		plain, err := c.openVaultCore(ctx, "Documents", func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
			vm.CacheRoot = cache
			return vm.Open(vi, "pw")
		})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if _, err := os.Stat(filepath.Join(plain, "Car insurance.pdf")); err == nil {
			t.Fatal("foreign plain file shows up inside the decrypted vault")
		}
		if err := step(plain); err != nil {
			t.Fatal(err)
		}
		if err := c.CloseVault(ctx, "Documents"); err != nil {
			t.Fatalf("close: %v", err)
		}
		for p, id := range foreign {
			if srv.relToNode[p] != id {
				t.Fatalf("close removed or replaced foreign %q", p)
			}
		}
	}
	if got := string(srv.nodes[foreign["Documents/Car insurance.pdf"]]); got != "plain Documents/Car insurance.pdf" {
		t.Fatalf("foreign file content changed: %q", got)
	}
}

// racingVaultServer answers a create-only upload of masterkey.cryptomator as a conflict,
// as the server does when the name got taken after CreateVault's check.
type racingVaultServer struct {
	*vaultTestServer
	deleted []string
}

func (s *racingVaultServer) PushFile(ctx context.Context, relPath string, base *int64, r io.Reader, mod time.Time) (engine.RemoteNode, bool, error) {
	if path.Base(relPath) != "masterkey.cryptomator" {
		return s.vaultTestServer.PushFile(ctx, relPath, base, r, mod)
	}
	if base == nil || *base != 0 {
		return engine.RemoteNode{}, false, fmt.Errorf("masterkey upload is not create-only (base %v)", base)
	}
	node, _, err := s.vaultTestServer.PushFile(ctx, relPath+" (conflict)", base, r, mod)
	return node, true, err
}

func (s *racingVaultServer) DeleteNode(ctx context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return s.vaultTestServer.DeleteNode(ctx, id)
}

func TestCreateVaultNameTakenDuringUpload(t *testing.T) {
	srv := &racingVaultServer{vaultTestServer: seededVaultServer(t)}
	c, _ := newTestController(t, srv)
	_, err := c.CreateVault(context.Background(), "", "Fresh", "pw")
	if !errors.Is(err, ErrVaultNameTaken) {
		t.Fatalf("CreateVault = %v, want ErrVaultNameTaken", err)
	}
	if len(srv.deleted) != 1 {
		t.Fatalf("conflict copy not removed: deleted %v", srv.deleted)
	}
	for p := range srv.relToNode {
		if strings.Contains(p, "(conflict)") {
			t.Fatalf("conflict copy %q left on the server", p)
		}
	}
}
