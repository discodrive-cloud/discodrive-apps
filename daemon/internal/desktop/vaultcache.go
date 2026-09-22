package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The persistent cache contains ciphertext only. A fresh server index supplies
// its SHA-256 keys; neither paths nor cached version numbers establish freshness.
const vaultCacheLimit int64 = 2 << 30

func validVaultHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == sha256.Size
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// restoreVaultCiphertext verifies while copying: a damaged cache entry must
// trigger a fresh download, never turn into a broken or stale decrypted file.
func (c *Controller) restoreVaultCiphertext(hash, dest string) bool {
	if c.vaultCacheDir == "" || !validVaultHash(hash) {
		return false
	}
	cachePath := filepath.Join(c.vaultCacheDir, strings.ToLower(hash)+".cipher")
	in, err := os.Open(cachePath)
	if err != nil {
		return false
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return false
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	closeErr := out.Close()
	if err != nil || closeErr != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), hash) {
		// The downloader truncates dest before retrying. Do not unlink cachePath here:
		// another open may have atomically replaced the corrupt entry in the meantime.
		return false
	}
	now := time.Now()
	_ = os.Chtimes(cachePath, now, now)
	return true
}

// Cache writes are best effort: lack of cache space must not fail an upload/open.
// Copy to a private temporary file before publication, never hard-link the working
// ciphertext tree (CloseVault replaces it). Hash the actual bytes being cached.
func (c *Controller) cacheVaultCiphertext(src string) {
	if c.vaultCacheDir == "" {
		return
	}
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil || st.Size() > vaultCacheLimit {
		return
	}
	if err := os.MkdirAll(c.vaultCacheDir, 0o700); err != nil {
		return
	}
	out, err := os.CreateTemp(c.vaultCacheDir, ".pending-")
	if err != nil {
		return
	}
	defer os.Remove(out.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		return
	}
	hash := hex.EncodeToString(h.Sum(nil))
	_ = os.Rename(out.Name(), filepath.Join(c.vaultCacheDir, hash+".cipher"))
}

// Bound disk use across vaults and successive re-encryptions. Eviction can race
// a reader safely: a miss falls back to the server, and open files stay usable.
func (c *Controller) trimVaultCache() {
	if c.vaultCacheDir == "" {
		return
	}
	entries, err := os.ReadDir(c.vaultCacheDir)
	if err != nil {
		return
	}
	var files []os.FileInfo
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cipher") {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, st)
		total += st.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	for _, f := range files {
		if total <= vaultCacheLimit {
			break
		}
		if os.Remove(filepath.Join(c.vaultCacheDir, f.Name())) == nil {
			total -= f.Size()
		}
	}
}
