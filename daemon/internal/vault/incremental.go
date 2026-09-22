package vault

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"discodrive.org/daemon/internal/safepath"
	"github.com/google/uuid"
)

// TreeSnapshot ties plaintext contents and file identities to their ciphertext.
// It stays in memory for an open session; no plaintext hashes are persisted.
type TreeSnapshot struct{ entries map[string]treeEntry }
type treeEntry struct {
	info                          os.FileInfo
	hash                          [sha256.Size]byte
	dirID, entryPath, contentPath string
}

func scanPlain(root string) (*TreeSnapshot, error) {
	s := &TreeSnapshot{entries: map[string]treeEntry{}}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			if !info.IsDir() {
				return fmt.Errorf("vault: plaintext root is not a directory")
			}
			return nil
		}
		if info.Name() == ".DS_Store" && !info.IsDir() {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("vault: unsupported file type: %s", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		e := treeEntry{info: info}
		if !info.IsDir() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			after, statErr := f.Stat()
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if statErr != nil {
				return statErr
			}
			if closeErr != nil {
				return closeErr
			}
			if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
				return fmt.Errorf("vault: file changed while reading: %s", p)
			}
			copy(e.hash[:], h.Sum(nil))
		}
		s.entries[filepath.ToSlash(rel)] = e
		return nil
	})
	return s, err
}

// SnapshotTree is called immediately after decryption, before exposing plaintext.
func (v *Vault) SnapshotTree(plain, encrypted string) (*TreeSnapshot, error) {
	s, err := scanPlain(plain)
	if err != nil {
		return nil, err
	}
	var visit func(string, string) error
	seen := map[string]bool{}
	visit = func(rel, dirID string) error {
		if seen[dirID] {
			return fmt.Errorf("vault: repeated directory ID")
		}
		seen[dirID] = true
		entries, err := v.ListDir(OSIO{Root: encrypted}, dirID)
		if err != nil {
			if rel == "" && errors.Is(err, os.ErrNotExist) && len(s.entries) == 0 {
				return nil
			}
			return err
		}
		for _, entry := range entries {
			if entry.Name == ".DS_Store" && !entry.IsDir {
				continue
			}
			child := path.Join(rel, entry.Name)
			if _, err := safepath.Join(plain, child); err != nil {
				return err
			}
			e, ok := s.entries[child]
			if !ok || e.info.IsDir() != entry.IsDir {
				return fmt.Errorf("vault: plaintext tree does not match ciphertext: %s", child)
			}
			e.entryPath, _, err = v.entryLocation(dirID, entry.Name)
			if err != nil {
				return err
			}
			e.dirID = entry.DirID
			e.contentPath = entry.FileStoragePath
			s.entries[child] = e
			if entry.IsDir {
				if err := visit(child, entry.DirID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit("", ""); err != nil {
		return nil, err
	}
	for p, e := range s.entries {
		if e.entryPath == "" {
			return nil, fmt.Errorf("vault: untracked plaintext at %s; preserve it before reopening", p)
		}
	}
	return s, nil
}

func (v *Vault) entryLocation(parentID, name string) (entry, encoded string, err error) {
	root, err := v.DirIdHash(parentID)
	if err != nil {
		return "", "", err
	}
	encoded, err = v.EncryptName(name, parentID)
	if err != nil {
		return "", "", err
	}
	short := encoded
	if len(encoded) > shorteningThreshold {
		short = shortenedName(encoded)
	}
	return path.Join(root, short), encoded, nil
}

// MatchesPlain detects edits made while encryption/upload was in progress.
func (s *TreeSnapshot) MatchesPlain(root string) (bool, error) {
	now, err := scanPlain(root)
	if err != nil {
		return false, err
	}
	if len(s.entries) != len(now.entries) {
		return false, nil
	}
	for p, e := range s.entries {
		n, ok := now.entries[p]
		if !ok || n.info.IsDir() != e.info.IsDir() || n.hash != e.hash {
			return false, nil
		}
	}
	return true, nil
}

// PrepareTree builds an isolated ciphertext tree. Unchanged content is linked or
// copied verbatim; directory identities survive ordinary renames/moves. It never
// mutates the original ciphertext or removes the user's plaintext.
func (v *Vault) PrepareTree(plain, encrypted string, before *TreeSnapshot) (string, *TreeSnapshot, error) {
	now, err := scanPlain(plain)
	if err != nil {
		return "", nil, err
	}
	unchanged := len(now.entries) == len(before.entries)
	for p, n := range now.entries {
		old, ok := before.entries[p]
		if !ok || old.info.IsDir() != n.info.IsDir() || old.hash != n.hash {
			unchanged = false
			break
		}
	}
	if unchanged {
		return encrypted, before, nil
	}
	stage, err := os.MkdirTemp(filepath.Dir(encrypted), "ddvclose-")
	if err != nil {
		return "", nil, err
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(stage)
		}
	}()
	if err := cloneCipherTree(encrypted, stage); err != nil {
		return "", nil, err
	}
	// Drop the old reachable entries in the private staging tree. Unrelated storage
	// (including existing orphan directories) is left alone.
	for _, e := range before.entries {
		if e.entryPath != "" {
			if err := os.RemoveAll(filepath.Join(stage, filepath.FromSlash(e.entryPath))); err != nil {
				return "", nil, err
			}
		}
		if e.info.IsDir() && e.entryPath != "" {
			hash, err := v.DirIdHash(e.dirID)
			if err != nil {
				return "", nil, err
			}
			if err := os.RemoveAll(filepath.Join(stage, filepath.FromSlash(hash))); err != nil {
				return "", nil, err
			}
		}
	}
	used := map[string]bool{}
	// Match identities first across the entire tree, before same-path fallback.
	// This handles directory swaps without assigning one directory ID twice.
	matches := map[string]treeEntry{}
	for p, n := range now.entries {
		if e, ok := before.entries[p]; ok && e.info.IsDir() == n.info.IsDir() && os.SameFile(e.info, n.info) {
			matches[p] = e
			used[p] = true
		}
	}
	for p, n := range now.entries {
		if _, ok := matches[p]; ok {
			continue
		}
		for old, e := range before.entries {
			if !used[old] && e.info.IsDir() == n.info.IsDir() && os.SameFile(e.info, n.info) {
				matches[p] = e
				used[old] = true
				break
			}
		}
	}
	for p, n := range now.entries {
		if _, ok := matches[p]; ok {
			continue
		}
		if e, ok := before.entries[p]; ok && !used[p] && e.info.IsDir() == n.info.IsDir() {
			matches[p] = e
			used[p] = true
		}
	}
	dirIDs := map[string]string{".": ""}
	rootHash, err := v.DirIdHash("")
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Join(stage, rootHash), 0700); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(stage, rootHash, "dirid.c9r")); os.IsNotExist(err) {
		if err := v.writeDirIDFile(filepath.Join(stage, rootHash), ""); err != nil {
			return "", nil, err
		}
	}
	err = filepath.Walk(plain, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == plain {
			return nil
		}
		rel, err := filepath.Rel(plain, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.Name() == ".DS_Store" && !info.IsDir() {
			return nil
		}
		n, ok := now.entries[rel]
		if !ok {
			return fmt.Errorf("vault: plaintext changed while preparing close")
		}
		parentID, ok := dirIDs[path.Dir(rel)]
		if !ok {
			return fmt.Errorf("vault: missing parent directory")
		}
		entry, encoded, err := v.entryLocation(parentID, path.Base(rel))
		if err != nil {
			return err
		}
		n.entryPath = entry
		old, matched := matches[rel]
		base := filepath.Join(stage, filepath.FromSlash(entry))
		if info.IsDir() || len(encoded) > shorteningThreshold {
			if err := os.MkdirAll(base, 0700); err != nil {
				return err
			}
			if len(encoded) > shorteningThreshold {
				if err := os.WriteFile(filepath.Join(base, "name.c9s"), []byte(encoded), 0600); err != nil {
					return err
				}
			}
		}
		if info.IsDir() {
			id := old.dirID
			if !matched || old.entryPath == "" {
				id = uuid.NewString()
			}
			n.dirID = id
			dirIDs[rel] = id
			if err := os.WriteFile(filepath.Join(base, "dir.c9r"), []byte(id), 0600); err != nil {
				return err
			}
			hash, err := v.DirIdHash(id)
			if err != nil {
				return err
			}
			dir := filepath.Join(stage, hash)
			if err := os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			if matched && old.entryPath != "" {
				err = copyCipherFile(filepath.Join(encrypted, hash, "dirid.c9r"), filepath.Join(dir, "dirid.c9r"))
				if errors.Is(err, os.ErrNotExist) {
					err = v.writeDirIDFile(dir, id)
				}
			} else {
				err = v.writeDirIDFile(dir, id)
			}
			if err != nil {
				return err
			}
		} else {
			target := base
			n.contentPath = entry
			if len(encoded) > shorteningThreshold {
				target = filepath.Join(base, "contents.c9r")
				n.contentPath = path.Join(entry, "contents.c9r")
			}
			if matched && old.contentPath != "" && old.hash == n.hash {
				if err := copyCipherFile(filepath.Join(encrypted, filepath.FromSlash(old.contentPath)), target); err != nil {
					return err
				}
			} else {
				if err := v.encryptFile(p, target); err != nil {
					return err
				}
			}
		}
		now.entries[rel] = n
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	same, err := now.MatchesPlain(plain)
	if err != nil {
		return "", nil, err
	}
	if !same {
		return "", nil, fmt.Errorf("vault: plaintext changed while preparing close")
	}
	success = true
	return stage, now, nil
}

func cloneCipherTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("vault: unsupported ciphertext file type")
		}
		return copyCipherFile(p, target)
	})
}
func copyCipherFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Exclusive creation prevents accidentally truncating a hard link into src.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// EqualCipherFile compares metadata/small files without allocating their contents.
func EqualCipherFile(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	sa, err := fa.Stat()
	if err != nil {
		return false, err
	}
	sb, err := fb.Stat()
	if err != nil {
		return false, err
	}
	if os.SameFile(sa, sb) {
		return true, nil
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	ba, bb := make([]byte, 64*1024), make([]byte, 64*1024)
	for {
		na, ea := fa.Read(ba)
		nb, eb := fb.Read(bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		if ea == io.EOF && eb == io.EOF {
			return true, nil
		}
		if ea != nil && ea != io.EOF {
			return false, ea
		}
		if eb != nil && eb != io.EOF {
			return false, eb
		}
	}
}
