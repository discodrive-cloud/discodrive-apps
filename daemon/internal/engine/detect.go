package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"discodrive.org/daemon/internal/index"
)

// isOSJunk reports whether a filename is a macOS system file we skip during sync
// (matching WebDAV projection behavior): .DS_Store and AppleDouble forks ._*.
func isOSJunk(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// LocalChange is a local modification discovered by scanning the sync folder.
// Op "move" is never produced by DetectLocal itself: PushLocal pairs a delete and a
// create with the same content hash into one move when the sink supports it.
type LocalChange struct {
	Op          string // create | update | delete | move
	RelPath     string
	OldRelPath  string   // move only: the previous rel path
	SourcePath  string   // create: inferred old server copy to retain if the upload fails
	HeldSources []string // possible sources of a renamed and edited pending file
	// LocalPath is where the file is on disk when it differs from RelPath (a server
	// name the local filesystem rejects, see internal/localname). Empty means RelPath.
	LocalPath string
	IsDir     bool
	Hash      string
	Size      int64
}

// DetectLocal scans root and diffs it against the index: new, modified, and deleted nodes.
func (e *Engine) DetectLocal() ([]LocalChange, error) {
	knownNodes, err := e.idx.All()
	if err != nil {
		return nil, err
	}
	pendingNames, err := e.idx.PendingNames()
	if err != nil {
		return nil, err
	}
	pending := make(map[string]index.PendingName, len(pendingNames))
	for _, n := range pendingNames {
		pending[n.LocalPath] = n
	}
	seen := make(map[string]bool, len(knownNodes))
	type prev struct {
		hash    string
		isDir   bool
		relPath string
	}
	// Keyed on where the node actually is on disk, which differs from its server path when
	// that path holds characters the filesystem rejects. Keyed on the server path instead,
	// a localized file read as a brand-new one and its server name as deleted — the push
	// would have uploaded a duplicate and removed the original.
	idxByPath := make(map[string]prev, len(knownNodes))
	for _, n := range knownNodes {
		idxByPath[n.LocalPath] = prev{n.ContentHash, n.IsDir, n.RelPath}
	}

	// serverPathOf is the server path for a new local path: its nearest indexed folder's
	// server path plus the rest as it is on disk. A folder can live under a local name that
	// differs from its server name (a generated name next to a case variant, a localized
	// name); a file created in it belongs in that server folder, never in a new one named
	// after the local name.
	//
	// Restored names are keyed by full local path, and only inferred for a moved
	// subtree witnessed by an unchanged file. A basename alone proves no relationship.
	var serverName map[string]string
	serverPathOf := func(local string) string {
		dir := path.Dir(local)
		for ; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			if k, ok := idxByPath[dir]; ok && k.isDir {
				break
			}
		}
		base, rest := "", local
		if k, ok := idxByPath[dir]; ok && k.isDir {
			base, rest = k.relPath, strings.TrimPrefix(local, dir+"/")
		}
		parts := strings.Split(rest, "/")
		prefix := dir
		for i, part := range parts {
			prefix = path.Join(prefix, part)
			if name, ok := serverName[prefix]; ok {
				parts[i] = name
			}
		}
		if base == "" {
			return strings.Join(parts, "/")
		}
		return base + "/" + strings.Join(parts, "/")
	}
	type newNode struct {
		local      string
		isDir      bool
		hash       string
		size       int64
		atPosition int // index in out where its change goes
	}
	var created []newNode

	var out []LocalChange
	err = filepath.WalkDir(e.root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == e.root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".kf-tmp-") {
			return nil
		}
		// Skip macOS system files (matching WebDAV projection): .DS_Store and
		// AppleDouble forks ._* have no place on the server.
		if isOSJunk(d.Name()) {
			return nil
		}
		// Never follow symlinks. d.Type() reports the type without following, so a
		// symlink (to a file or directory) is skipped outright — otherwise a link
		// pointing outside the sync folder (e.g. ~/.ssh/id_rsa) would be read and
		// uploaded, or a symlinked directory would expose data outside the tree.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		local := filepath.ToSlash(mustRel(e.root, p))
		seen[local] = true
		known, ok := idxByPath[local]
		// A known file reports the server's name for itself; a file the index has never
		// seen goes up under the name it carries on disk, inside its folder's server path.
		rel := local
		if ok {
			rel = known.relPath
		}
		if d.IsDir() {
			if !ok {
				created = append(created, newNode{local: local, isDir: true, atPosition: len(out)})
				out = append(out, LocalChange{})
			}
			return nil
		}
		h, size, herr := hashFile(p)
		if herr != nil {
			return herr
		}
		switch {
		case !ok:
			created = append(created, newNode{local: local, hash: h, size: size, atPosition: len(out)})
			out = append(out, LocalChange{})
		case known.hash != h:
			out = append(out, LocalChange{Op: "update", RelPath: rel, LocalPath: local, Hash: h, Size: size})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Find unambiguous unchanged files witnessing a directory move. Only descendants
	// of those directory pairs may retain their old localized/generated names.
	oldByHash := map[string][]string{}
	newByHash := map[string][]string{}
	newDirs := map[string]bool{}
	for _, n := range knownNodes {
		if !n.IsDir && !seen[n.LocalPath] && n.ContentHash != "" && n.ContentHash != emptyHash {
			oldByHash[n.ContentHash] = append(oldByHash[n.ContentHash], n.LocalPath)
		}
	}
	for _, c := range created {
		if c.isDir {
			newDirs[c.local] = true
		} else {
			newByHash[c.hash] = append(newByHash[c.hash], c.local)
		}
	}
	renamed := map[string]string{}
	ambiguous := map[string]bool{}
	for hash, olds := range oldByHash {
		news := newByHash[hash]
		if len(olds) != 1 || len(news) != 1 {
			continue
		}
		old, next := olds[0], news[0]
		for path.Base(old) == path.Base(next) && path.Dir(old) != "." && path.Dir(next) != "." {
			old, next = path.Dir(old), path.Dir(next)
			if k, ok := idxByPath[old]; !ok || !k.isDir || seen[old] || !newDirs[next] {
				break
			}
			if prev, ok := renamed[next]; ok && prev != old {
				ambiguous[next] = true
			}
			renamed[next] = old
		}
	}
	serverName = map[string]string{}
	sources := map[string]string{}
	heldSources := map[string][]string{}
	usedPending := map[string]bool{}
	pendingByHash := map[string][]index.PendingName{}
	for _, n := range pendingNames {
		if !n.IsDir && !seen[n.LocalPath] && n.Hash != "" && n.Hash != emptyHash {
			pendingByHash[n.Hash] = append(pendingByHash[n.Hash], n)
		}
	}
	for _, c := range created {
		if c.isDir {
			continue
		}
		olds := pendingByHash[c.hash]
		if len(olds) != 1 || len(newByHash[c.hash]) != 1 {
			continue
		}
		n := olds[0]
		// A file's explicit new basename wins; a plain move retains its server name.
		if path.Base(c.local) == path.Base(n.LocalPath) {
			serverName[c.local] = path.Base(n.RelPath)
		}
		sources[c.local], heldSources[c.local] = n.SourcePath, n.HeldSources
		usedPending[n.LocalPath] = true
	}
	for _, c := range created {
		// A previous push may already have indexed the witness and target folder.
		// Restore the basename, resolving parents against their current index paths.
		if n, ok := pending[c.local]; ok && n.IsDir == c.isDir {
			serverName[c.local] = path.Base(n.RelPath)
			sources[c.local] = n.SourcePath
			heldSources[c.local] = n.HeldSources
			usedPending[n.LocalPath] = true
		}
	}
	for _, c := range created {
		// A nested move overrides an outer one. Choose the closest witness even
		// when ambiguous: falling back to its parent would guess the wrong owner.
		nearest := ""
		for next := range renamed {
			if (c.local == next || strings.HasPrefix(c.local, next+"/")) && len(next) > len(nearest) {
				nearest = next
			}
		}
		if nearest == "" || ambiguous[nearest] {
			continue
		}
		old := renamed[nearest]
		// Preserve an explicit new name for the root of a moved subtree.
		if c.local == nearest && path.Base(old) != path.Base(nearest) {
			continue
		}
		previous := old + strings.TrimPrefix(c.local, nearest)
		if n, ok := pending[previous]; ok && n.IsDir == c.isDir {
			// The preceding move may still be awaiting upload. Carry its original
			// server source through the new move, rather than inventing an intermediate one.
			serverName[c.local] = path.Base(n.RelPath)
			sources[c.local] = n.SourcePath
			heldSources[c.local] = n.HeldSources
			usedPending[n.LocalPath] = true
		} else if n, ok := idxByPath[previous]; ok && n.isDir == c.isDir {
			serverName[c.local] = path.Base(n.relPath)
			sources[c.local] = n.relPath
		}
	}
	// A vanished pending file may have been renamed AND edited. Never guess its
	// new server name, but retain its possible sources if any new upload fails.
	// Carry these holds into that upload's plan so retries remain safe as well.
	var uncertainSources []string
	for _, n := range pendingNames {
		if !seen[n.LocalPath] && !usedPending[n.LocalPath] {
			if n.SourcePath != "" {
				uncertainSources = append(uncertainSources, n.SourcePath)
			}
			uncertainSources = append(uncertainSources, n.HeldSources...)
		}
	}
	for _, c := range created {
		rel := serverPathOf(c.local)
		source := sources[c.local]
		ch := LocalChange{Op: "create", RelPath: rel, SourcePath: source, IsDir: c.isDir, Hash: c.hash, Size: c.size}
		if !c.isDir {
			ch.HeldSources = uniqueSources(heldSources[c.local], uncertainSources)
		}
		if rel != c.local {
			ch.LocalPath = c.local // where the new node is on disk
		}
		out[c.atPosition] = ch
	}
	for i := range out {
		c := &out[i]
		if c.Op != "update" {
			continue
		}
		n := pending[c.LocalPath]
		c.HeldSources = uniqueSources(n.HeldSources, []string{n.SourcePath}, uncertainSources)
	}
	for _, n := range knownNodes {
		if !seen[n.LocalPath] {
			// Hash comes from the index so PushLocal can pair this delete with a
			// same-content create into a move.
			out = append(out, LocalChange{Op: "delete", RelPath: n.RelPath, IsDir: n.IsDir, Hash: n.ContentHash, Size: n.Size})
		}
	}
	return out, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func mustRel(base, target string) string {
	r, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return r
}

// uniqueSources bounds the durable holds even across repeated ambiguous moves.
func uniqueSources(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, group := range groups {
		for _, p := range group {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}
