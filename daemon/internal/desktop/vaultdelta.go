package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/vault"
)

type cipherEntry struct {
	file string
	dir  bool
}
type cipherChange struct {
	cipherEntry
	remove  bool
	replace bool
	hash    string
}

func cipherTree(root string) (map[string]cipherEntry, error) {
	entries := map[string]cipherEntry{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		entries[filepath.ToSlash(rel)] = cipherEntry{file: p, dir: info.IsDir()}
		return nil
	})
	return entries, err
}
func (c *Controller) vaultRemote(root string) (map[string]index.Node, error) {
	nodes, err := c.idx.All()
	if err != nil {
		return nil, err
	}
	result := map[string]index.Node{}
	for _, n := range nodes {
		if strings.HasPrefix(n.RelPath, root+"/") {
			result[strings.TrimPrefix(n.RelPath, root+"/")] = n
		}
	}
	return result, nil
}
func vaultDelta(before, after string) (map[string]cipherChange, error) {
	changes := map[string]cipherChange{}
	if before == after {
		return changes, nil
	}
	old, err := cipherTree(before)
	if err != nil {
		return nil, err
	}
	current, err := cipherTree(after)
	if err != nil {
		return nil, err
	}
	for rel, n := range current {
		if prev, ok := old[rel]; ok {
			if n.dir == prev.dir {
				if n.dir {
					continue
				}
				same, err := vault.EqualCipherFile(prev.file, n.file)
				if err != nil {
					return nil, err
				}
				if same {
					continue
				}
			}
		}
		change := cipherChange{cipherEntry: n}
		if prev, ok := old[rel]; ok && prev.dir != n.dir {
			change.replace = true
		}
		if !n.dir {
			change.hash, err = fileSHA256(n.file)
			if err != nil {
				return nil, err
			}
		}
		changes[rel] = change
	}
	for rel, n := range old {
		if _, ok := current[rel]; !ok {
			changes[rel] = cipherChange{cipherEntry: n, remove: true}
		}
	}
	return changes, nil
}

func sameVaultNode(a, b index.Node) bool {
	return a.NodeID == b.NodeID && a.Version == b.Version && a.IsDir == b.IsDir && a.ContentHash == b.ContentHash
}

// A retry may find some staged writes already committed (including a lost HTTP
// reply). Accept those exact bytes, but never overwrite unrelated remote edits.
func checkVaultDelta(before, remote map[string]index.Node, changes map[string]cipherChange) error {
	all := map[string]bool{}
	for p := range before {
		all[p] = true
	}
	for p := range remote {
		all[p] = true
	}
	for p := range all {
		old, had := before[p]
		current, has := remote[p]
		if had && has && sameVaultNode(old, current) {
			continue
		}
		ch, planned := changes[p]
		if planned {
			if (ch.remove || ch.replace) && !has {
				continue
			}
			if !ch.remove && has && ch.dir == current.IsDir {
				if ch.dir && (!had || ch.replace) {
					continue
				}
				if !ch.dir && strings.EqualFold(ch.hash, current.ContentHash) {
					continue
				}
			}
		}
		return fmt.Errorf("vault: remote contents changed while the vault was open; local files were preserved")
	}
	return nil
}

func (c *Controller) commitVaultDelta(ctx context.Context, s *vaultSession) error {
	changes, err := vaultDelta(s.tmpDir, s.pending.Dir)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	defer c.trimVaultCache()
	if _, err := c.Refresh(ctx); err != nil {
		return err
	}
	remote, err := c.vaultRemote(s.relPath)
	if err != nil {
		return err
	}
	if err := checkVaultDelta(s.remote, remote, changes); err != nil {
		return err
	}
	var dirs, files, deletes []string
	for p, ch := range changes {
		switch {
		case ch.remove:
			deletes = append(deletes, p)
		case ch.dir:
			dirs = append(dirs, p)
		default:
			files = append(files, p)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	sort.Slice(deletes, func(i, j int) bool { return len(deletes[i]) > len(deletes[j]) })
	// A file/directory type change uses the same encrypted entry name. Remove
	// the old type first; the staged ciphertext and plaintext remain available on failure.
	for p, ch := range changes {
		if !ch.replace {
			continue
		}
		n, ok := remote[p]
		if !ok || n.IsDir == ch.dir {
			continue
		}
		if err := c.srv.DeleteNode(ctx, n.NodeID); err != nil {
			return err
		}
		for rel := range remote {
			if rel == p || strings.HasPrefix(rel, p+"/") {
				delete(remote, rel)
			}
		}
	}
	for _, p := range dirs {
		if _, ok := remote[p]; ok {
			continue
		}
		if _, err := c.srv.EnsureDir(ctx, s.relPath+"/"+p); err != nil {
			return err
		}
	}
	for _, p := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		ch := changes[p]
		current, exists := remote[p]
		if exists && strings.EqualFold(current.ContentHash, ch.hash) {
			continue
		}
		base := int64(0)
		if exists {
			base = current.Version
		}
		f, err := os.Open(ch.file)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		node, conflict, err := c.srv.PushFile(ctx, s.relPath+"/"+p, &base, f, info.ModTime())
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if conflict {
			// A server-generated conflict filename is not a valid encrypted vault name.
			// Keep the local plaintext/staging tree, and remove only our conflict node.
			if node.NodeID != "" && node.NodeID != current.NodeID {
				if err := c.srv.DeleteNode(ctx, node.NodeID); err != nil {
					return fmt.Errorf("vault: upload conflict cleanup: %w", err)
				}
			}
			return fmt.Errorf("vault: concurrent remote edit; local files were preserved")
		}
		c.cacheVaultCiphertext(ch.file)
	}
	// Publish all replacements before unlinking old entries. In particular a moved
	// directory's new marker must exist before removing its previous marker.
	for _, p := range deletes {
		if n, ok := remote[p]; ok {
			if err := c.srv.DeleteNode(ctx, n.NodeID); err != nil {
				return err
			}
		}
	}
	if _, err := c.Refresh(ctx); err != nil {
		return err
	}
	committed, err := c.vaultRemote(s.relPath)
	if err != nil {
		return err
	}
	if err := checkVaultDelta(s.remote, committed, changes); err != nil {
		return err
	}
	for p, ch := range changes {
		n, ok := committed[p]
		if ch.remove {
			if ok {
				return fmt.Errorf("vault: deletion was not committed")
			}
			continue
		}
		if !ok || n.IsDir != ch.dir || (!ch.dir && !strings.EqualFold(n.ContentHash, ch.hash)) {
			return fmt.Errorf("vault: remote contents changed during save; local files were preserved")
		}
	}
	s.remote = committed
	return nil
}
