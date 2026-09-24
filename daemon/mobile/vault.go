package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/safepath"
	"discodrive.org/daemon/internal/vault"
)

// Vault is a bindable, lazy view of one Cryptomator vault stored as a subfolder of the user's
// storage. Reads come from a local change-feed index + on-demand Download; writes go through the
// path-based sync API. Decrypted files are written to tmpDir (app-private) for the UI to open.
type Vault struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	closed bool
	v      *vault.Vault
	io     serverIO
	tmpDir string
}

type vaultEntry struct {
	Name            string `json:"name"`
	IsDir           bool   `json:"isDir"`
	DirID           string `json:"dirID"`
	FileStoragePath string `json:"fileStoragePath"`
}

// OpenVault opens the Cryptomator vault rooted at vaultRoot within the user's storage.
// indexDBPath — sqlite index (app-private); tmpDir — app-private dir for decrypted files;
// insecure — accept self-signed TLS. Wrong password → vault.ErrWrongPassword.
func OpenVault(serverURL, deviceToken, vaultRoot, password, indexDBPath, tmpDir string, insecure bool) (*Vault, error) {
	setInsecure(insecure)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	idx, err := index.Open(indexDBPath)
	if err != nil {
		return nil, err
	}
	identity := sha256.Sum256([]byte(serverURL + "\n" + deviceToken))
	if err := idx.BindMirrorPairing(hex.EncodeToString(identity[:])); err != nil {
		idx.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	opened := false
	defer func() {
		if !opened {
			cancel()
		}
	}()
	sio := serverIO{client: protocol.NewUnscoped(serverURL, deviceToken), idx: idx, root: vaultRoot, ctx: ctx, cache: &vaultMetadata{values: map[string][]byte{}}}
	if err := pullChanges(ctx, sio.client, sio.idx); err != nil {
		idx.Close()
		return nil, err
	}
	sio.prefetch([]string{"masterkey.cryptomator", "vault.cryptomator"})
	v, err := vault.OpenWithSource(sio, password)
	if err != nil {
		idx.Close()
		return nil, err
	}
	opened = true
	return &Vault{v: v, io: sio, tmpDir: tmpDir, cancel: cancel}, nil
}

// Refresh re-pulls change-feed metadata into the index.
func (m *Vault) Refresh() error { m.mu.Lock(); defer m.mu.Unlock(); return m.refresh() }
func (m *Vault) refresh() error {
	if m.closed {
		return fmt.Errorf("vault is closed")
	}
	return pullChanges(m.io.ctx, m.io.client, m.io.idx)
}

// List returns the children of dirID ("" = root) as a JSON array of vaultEntry.
func (m *Vault) List(dirID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("vault is closed")
	}
	if hash, err := m.v.DirIdHash(dirID); err == nil {
		m.io.prefetchDirectory(hash)
	}
	entries, err := m.v.ListDir(m.io, dirID)
	if err != nil {
		return "", err
	}
	out := make([]vaultEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, vaultEntry{Name: e.Name, IsDir: e.IsDir, DirID: e.DirID, FileStoragePath: e.FileStoragePath})
	}
	js, err := json.Marshal(out)
	return string(js), err
}

// OpenFile decrypts the file at fileStoragePath into tmpDir/plainName and returns the local path.
func (m *Vault) OpenFile(fileStoragePath, plainName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("vault is closed")
	}
	dst, err := safepath.Join(m.tmpDir, plainName)
	if err != nil {
		return "", err
	}
	node, ok, err := m.io.idx.GetByPath(m.io.full(fileStoragePath))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("vault file not found")
	}
	cipher, err := os.CreateTemp(m.tmpDir, ".cipher-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(cipher.Name())
	defer cipher.Close()
	if err = m.io.client.Download(m.io.ctx, node.NodeID, cipher); err != nil {
		return "", err
	}
	if _, err = cipher.Seek(0, 0); err != nil {
		return "", err
	}
	plain, err := os.CreateTemp(m.tmpDir, ".plain-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(plain.Name())
	decryptErr := m.v.DecryptContent(plain, cipher)
	closeErr := plain.Close()
	if decryptErr != nil {
		return "", decryptErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(plain.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

// WriteFile encrypts the local plaintext file and writes it as name into parentDirID, then refreshes.
func (m *Vault) WriteFile(parentDirID, name, localPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("vault is closed")
	}
	plain, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer plain.Close()
	cipher, err := os.CreateTemp(m.tmpDir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(cipher.Name())
	defer cipher.Close()
	if err = m.v.EncryptContent(cipher, plain); err != nil {
		return err
	}
	if _, err = cipher.Seek(0, 0); err != nil {
		return err
	}
	if err = m.v.WriteEncryptedFile(m.io, parentDirID, name, cipher); err != nil {
		return err
	}
	return m.refresh()
}

// Mkdir creates subdirectory name in parentDirID, refreshes, and returns the new dirID.
func (m *Vault) Mkdir(parentDirID, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("vault is closed")
	}
	id, err := m.v.MakeDir(m.io, parentDirID, name)
	if err != nil {
		return "", err
	}
	if err := m.refresh(); err != nil {
		return "", err
	}
	return id, nil
}

// Remove deletes file/dir name from parentDirID, then refreshes.
func (m *Vault) Remove(parentDirID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("vault is closed")
	}
	if err := m.v.Remove(m.io, parentDirID, name); err != nil {
		return err
	}
	return m.refresh()
}

// Close releases the index.
func (m *Vault) Close() error {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		if err := m.io.idx.Close(); err != nil {
			return err
		}
		m.closed = true
		m.v = nil
	}
	// The caller supplies a dedicated private directory. Never traverse siblings.
	if filepath.Clean(m.tmpDir) == "." || filepath.Clean(m.tmpDir) == string(filepath.Separator) {
		return fmt.Errorf("invalid vault temporary directory")
	}
	return os.RemoveAll(m.tmpDir)
}
