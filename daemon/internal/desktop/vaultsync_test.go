package desktop

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"discodrive.org/daemon/internal/engine"
)

// vaultTestServer is a richer fake that supports EnsureDir, PushFile, Download,
// and a dynamic Changes feed. Used by TestVaultRoundTrip and TestVaultWriteBack.
type vaultTestServer struct {
	mu        sync.Mutex
	seq       int64
	nodes     map[string][]byte // nodeID -> bytes (files only)
	changes   []engine.Change   // recorded in-order
	relToNode map[string]string // relPath -> latest nodeID (enables update-on-re-upload)
}

func (s *vaultTestServer) EnsureDir(_ context.Context, relPath string) (engine.RemoteNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	nodeID := fmt.Sprintf("dir-%d", s.seq)
	s.changes = append(s.changes, engine.Change{
		Seq: s.seq, Op: "upsert", NodeID: nodeID, RelPath: relPath, IsDir: true, Version: 1,
	})
	return engine.RemoteNode{NodeID: nodeID, Version: 1}, nil
}

func (s *vaultTestServer) PushFile(_ context.Context, relPath string, _ *int64, r io.Reader, _ time.Time) (engine.RemoteNode, bool, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return engine.RemoteNode{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-upload: reuse the existing nodeID so the index entry is updated in-place
	// rather than accumulating a second node for the same relPath.
	if s.relToNode != nil {
		if nodeID, ok := s.relToNode[relPath]; ok {
			s.seq++
			s.nodes[nodeID] = data
			s.changes = append(s.changes, engine.Change{
				Seq: s.seq, Op: "upsert", NodeID: nodeID, RelPath: relPath, IsDir: false, Version: 2, Size: int64(len(data)),
			})
			return engine.RemoteNode{NodeID: nodeID, Version: 2}, false, nil
		}
	}

	// First upload: allocate a new node.
	s.seq++
	nodeID := fmt.Sprintf("file-%d", s.seq)
	s.nodes[nodeID] = data
	if s.relToNode == nil {
		s.relToNode = make(map[string]string)
	}
	s.relToNode[relPath] = nodeID
	s.changes = append(s.changes, engine.Change{
		Seq: s.seq, Op: "upsert", NodeID: nodeID, RelPath: relPath, IsDir: false, Version: 1, Size: int64(len(data)),
	})
	return engine.RemoteNode{NodeID: nodeID, Version: 1}, false, nil
}

func (s *vaultTestServer) Download(_ context.Context, nodeID string, w io.Writer) error {
	s.mu.Lock()
	data := s.nodes[nodeID]
	s.mu.Unlock()
	_, err := w.Write(data)
	return err
}

func (s *vaultTestServer) Changes(_ context.Context, since int64, limit int) ([]engine.Change, int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []engine.Change
	for _, c := range s.changes {
		if c.Seq > since {
			out = append(out, c)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	var next int64 = since
	for _, c := range out {
		if c.Seq > next {
			next = c.Seq
		}
	}
	return out, next, false, nil
}

// Stubs for the remaining ServerAPI methods.
func (s *vaultTestServer) CreateFolder(_ context.Context, _, _ string) error { return nil }
func (s *vaultTestServer) RenameNode(_ context.Context, _, _ string) error   { return nil }
func (s *vaultTestServer) MoveNode(_ context.Context, _, _ string) error     { return nil }
func (s *vaultTestServer) DeleteNode(_ context.Context, _ string) error      { return nil }
func (s *vaultTestServer) UploadFile(_ context.Context, _, _ string, r io.Reader, _ time.Time) error {
	_, _ = io.Copy(io.Discard, r)
	return nil
}

// TestVaultWriteBack exercises the write-back pipeline:
// create → open → edit → re-encrypt (EncryptTree) → upload → refresh → download →
// decrypt (DecryptTree) → verify content.
func TestVaultWriteBack(t *testing.T) {
	ctx := context.Background()

	srv := &vaultTestServer{
		nodes:     make(map[string][]byte),
		relToNode: make(map[string]string),
	}
	ctrl, _ := newTestController(t, srv)

	// --- Step 1: CreateVault + Refresh ---
	phrase, err := ctrl.CreateVault(ctx, "", "myvault", "pw")
	if err != nil {
		t.Fatalf("CreateVault: %v", err)
	}
	if len(strings.Fields(phrase)) != 44 {
		t.Fatalf("recovery phrase = %d words, want 44", len(strings.Fields(phrase)))
	}
	if _, err := ctrl.Refresh(ctx); err != nil {
		t.Fatalf("Refresh (1): %v", err)
	}

	// --- Step 2: OpenVault ---
	plain, err := ctrl.OpenVault(ctx, "myvault", "pw")
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}

	// --- Step 3: Write hello.txt into the plaintext directory ---
	if err := os.WriteFile(filepath.Join(plain, "hello.txt"), []byte("world"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// --- Step 4: CloseVault re-encrypts and uploads ---
	if err := ctrl.CloseVault(ctx, "myvault"); err != nil {
		t.Fatalf("CloseVault: %v", err)
	}
	if ctrl.IsVaultOpen("myvault") {
		t.Fatal("IsVaultOpen: expected false after CloseVault")
	}

	// --- Step 5: Refresh to pick up re-uploaded ciphertext ---
	if _, err := ctrl.Refresh(ctx); err != nil {
		t.Fatalf("Refresh (2): %v", err)
	}

	// --- Step 6: Reopen vault ---
	plain2, err := ctrl.OpenVault(ctx, "myvault", "pw")
	if err != nil {
		t.Fatalf("OpenVault (2): %v", err)
	}

	// --- Step 7: Assert hello.txt round-tripped correctly ---
	got, err := os.ReadFile(filepath.Join(plain2, "hello.txt"))
	if err != nil {
		t.Fatalf("ReadFile hello.txt: %v", err)
	}
	if string(got) != "world" {
		t.Fatalf("hello.txt content: got %q, want %q", string(got), "world")
	}
}

// TestVaultRoundTrip exercises the full vault lifecycle against an in-memory fake server:
// create → upload → refresh index → list → open → verify plaintext dir exists.
func TestVaultRoundTrip(t *testing.T) {
	ctx := context.Background()

	srv := &vaultTestServer{nodes: make(map[string][]byte)}
	ctrl, _ := newTestController(t, srv)

	// --- Step 1: CreateVault ---
	if _, err := ctrl.CreateVault(ctx, "", "myvault", "s3cret"); err != nil {
		t.Fatalf("CreateVault: %v", err)
	}

	// --- Step 2: CreateVault already pulled the upload into the index (internal Refresh),
	// so ListVaults sees it without a manual Refresh. ---

	// --- Step 3: ListVaults ---
	vaults, err := ctrl.ListVaults()
	if err != nil {
		t.Fatalf("ListVaults: %v", err)
	}
	found := false
	for _, v := range vaults {
		if v.Name == "myvault" && v.RelPath == "myvault" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListVaults: {Name:\"myvault\", RelPath:\"myvault\"} not found; got %v", vaults)
	}

	// --- Step 4: wrong password (no open session yet) returns an error ---
	if _, err := ctrl.OpenVault(ctx, "myvault", "wrongpassword"); err == nil {
		t.Fatal("OpenVault with wrong password: expected error, got nil")
	}

	// --- Step 5: OpenVault with correct password ---
	plainDir, err := ctrl.OpenVault(ctx, "myvault", "s3cret")
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	if _, err := os.Stat(plainDir); err != nil {
		t.Fatalf("plainDir %q does not exist: %v", plainDir, err)
	}
}

// TestVaultRecoveryOpen verifies opening a vault by its recovery phrase, and that a
// phrase from a different vault is rejected.
func TestVaultRecoveryOpen(t *testing.T) {
	ctx := context.Background()
	srv := &vaultTestServer{nodes: make(map[string][]byte)}
	ctrl, _ := newTestController(t, srv)

	phrase, err := ctrl.CreateVault(ctx, "", "v1", "pw")
	if err != nil {
		t.Fatalf("CreateVault v1: %v", err)
	}
	otherPhrase, err := ctrl.CreateVault(ctx, "", "v2", "pw2")
	if err != nil {
		t.Fatalf("CreateVault v2: %v", err)
	}

	// A recovery phrase from a different vault must be rejected.
	if _, err := ctrl.OpenVaultWithRecovery(ctx, "v1", otherPhrase); err == nil {
		t.Fatal("OpenVaultWithRecovery with wrong-vault phrase: expected error")
	}
	if ctrl.IsVaultOpen("v1") {
		t.Fatal("v1 should not be open after a rejected recovery phrase")
	}

	// The correct recovery phrase opens the vault.
	plain, err := ctrl.OpenVaultWithRecovery(ctx, "v1", phrase)
	if err != nil {
		t.Fatalf("OpenVaultWithRecovery: %v", err)
	}
	if !ctrl.IsVaultOpen("v1") {
		t.Fatal("v1 should be open after recovery open")
	}
	if _, err := os.Stat(plain); err != nil {
		t.Fatalf("plaintext dir should exist: %v", err)
	}
}
