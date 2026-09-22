package desktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

type measuredVaultServer struct {
	*vaultTestServer
	measureMu sync.Mutex
	bytes     int
}

func (s *measuredVaultServer) Download(ctx context.Context, id string, w io.Writer) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	s.mu.Lock()
	data := append([]byte(nil), s.nodes[id]...)
	s.mu.Unlock()
	s.measureMu.Lock()
	s.bytes += len(data)
	s.measureMu.Unlock()
	_, err := w.Write(data)
	return err
}
func (s *measuredVaultServer) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, mod time.Time) (engine.RemoteNode, bool, error) {
	node, conflict, err := s.vaultTestServer.PushFile(ctx, rel, base, r, mod)
	if err == nil {
		s.mu.Lock()
		s.changes[len(s.changes)-1].ContentHash = fmt.Sprintf("%x", sha256.Sum256(s.nodes[node.NodeID]))
		s.mu.Unlock()
	}
	return node, conflict, err
}

func TestVaultReopenReusesCiphertext(t *testing.T) {
	srv := &measuredVaultServer{vaultTestServer: &vaultTestServer{nodes: map[string][]byte{}}}
	c, _ := newTestController(t, srv)
	c.vaultCacheDir = t.TempDir()
	ctx := context.Background()
	body := strings.Repeat("encrypted", 128*1024)
	for i := 0; i < 12; i++ {
		_, _, err := srv.PushFile(ctx, fmt.Sprintf("vault/d/%d", i), nil, strings.NewReader(fmt.Sprint(i)+body), time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		srv.bytes = 0
		start := time.Now()
		_, _, dir, err := c.downloadVaultCiphertext(ctx, "vault")
		if err != nil {
			t.Fatal(err)
		}
		os.RemoveAll(dir)
		t.Logf("pass %d: downloaded %d bytes in %s", pass, srv.bytes, time.Since(start))
		if pass == 1 && srv.bytes != 0 {
			t.Fatalf("unchanged vault downloaded again: %d bytes", srv.bytes)
		}
	}
}

func TestVaultCacheChangedAndCorrupt(t *testing.T) {
	ctx := context.Background()
	srv := &measuredVaultServer{vaultTestServer: &vaultTestServer{nodes: map[string][]byte{}}}
	c, _ := newTestController(t, srv)
	c.vaultCacheDir = t.TempDir()
	fetch := func(want string, wantBytes int) {
		t.Helper()
		srv.bytes = 0
		_, _, dir, err := c.downloadVaultCiphertext(ctx, "vault")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		got, err := os.ReadFile(filepath.Join(dir, "data"))
		if err != nil || string(got) != want {
			t.Fatalf("got %q, %v", got, err)
		}
		if srv.bytes != wantBytes {
			t.Fatalf("downloaded %d, want %d", srv.bytes, wantBytes)
		}
	}
	if _, _, err := srv.PushFile(ctx, "vault/data", nil, strings.NewReader("old"), time.Now()); err != nil {
		t.Fatal(err)
	}
	fetch("old", 3)
	// A new controller reuses the persistent cache, without an in-memory manifest.
	cacheDir := c.vaultCacheDir
	c = NewController(srv, c.idx, c.contentDir)
	c.vaultCacheDir = cacheDir
	fetch("old", 0)
	// Corrupt a cached entry under the expected digest; same size is not enough.
	if err := os.MkdirAll(c.vaultCacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("old")))
	if err := os.WriteFile(filepath.Join(c.vaultCacheDir, hash+".cipher"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	fetch("old", 3)
	fetch("old", 0)
	if _, _, err := srv.PushFile(ctx, "vault/data", nil, strings.NewReader("new"), time.Now()); err != nil {
		t.Fatal(err)
	}
	fetch("new", 3)
	fetch("new", 0)
}

func TestVaultCacheAfterWriteBack(t *testing.T) {
	ctx := context.Background()
	srv := &measuredVaultServer{vaultTestServer: &vaultTestServer{nodes: map[string][]byte{}}}
	c, _ := newTestController(t, srv)
	c.vaultCacheDir = t.TempDir()
	encrypted, plain, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(filepath.Join(plain, fmt.Sprint(i)), []byte(strings.Repeat("x", 1<<20)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.EncryptTree(plain, encrypted); err != nil {
		t.Fatal(err)
	}
	if err := c.uploadTree(ctx, encrypted, "vault"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(c.vaultCacheDir); err != nil {
		t.Fatal(err)
	} // cold start
	open := func() string {
		t.Helper()
		start := time.Now()
		p, err := c.openVaultCore(ctx, "vault", func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
			vm.CacheRoot = cache
			start := time.Now()
			p, err := vm.Open(vi, "pw")
			t.Logf("unlock + decrypt: %s", time.Since(start))
			return p, err
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("complete open: %s; downloaded: %d bytes", time.Since(start), srv.bytes)
		return p
	}
	p := open()
	if err := os.WriteFile(filepath.Join(p, "0"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseVault(ctx, "vault"); err != nil {
		t.Fatal(err)
	}
	srv.bytes = 0
	p = open()
	defer os.RemoveAll(c.sessions["vault"].tmpDir)
	if srv.bytes != 0 {
		t.Fatalf("uploaded ciphertext downloaded again: %d bytes", srv.bytes)
	}
	got, err := os.ReadFile(filepath.Join(p, "0"))
	if err != nil || string(got) != "edited" {
		t.Fatalf("edit lost: %q, %v", got, err)
	}
}

func TestVaultCacheTrim(t *testing.T) {
	c, _ := newTestController(t, &vaultTestServer{})
	c.vaultCacheDir = t.TempDir()
	for i := 0; i < 3; i++ {
		name := filepath.Join(c.vaultCacheDir, fmt.Sprintf("%d.cipher", i))
		f, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(vaultCacheLimit / 2)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("sparse file: %v %v", err, closeErr)
		}
		stamp := time.Unix(int64(i), 0)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	c.trimVaultCache()
	if _, err := os.Stat(filepath.Join(c.vaultCacheDir, "0.cipher")); !os.IsNotExist(err) {
		t.Fatalf("oldest entry kept: %v", err)
	}
	for _, name := range []string{"1.cipher", "2.cipher"} {
		if _, err := os.Stat(filepath.Join(c.vaultCacheDir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVaultCacheRejectsChangedDownload(t *testing.T) {
	srv := &measuredVaultServer{vaultTestServer: &vaultTestServer{nodes: map[string][]byte{}}}
	c, _ := newTestController(t, srv)
	c.vaultCacheDir = t.TempDir()
	node, _, err := srv.PushFile(context.Background(), "vault/data", nil, strings.NewReader("old"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Download races a remote edit not yet represented in the index.
	srv.nodes[node.NodeID] = []byte("new")
	_, _, dir, err := c.downloadVaultCiphertext(context.Background(), "vault")
	if err == nil || dir != "" {
		t.Fatalf("accepted mismatched ciphertext: %s, %v", dir, err)
	}
	entries, err := os.ReadDir(c.vaultCacheDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed download cached: %v %v", entries, err)
	}
}
