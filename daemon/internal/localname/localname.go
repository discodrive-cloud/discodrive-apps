// Package localname maps server paths to names the local filesystem will accept.
//
// A server path may contain characters that some filesystems reject outright: Android's
// internal storage is a FUSE layer with FAT semantics, and Windows has the same list. A note
// named "is it worth it?.md" downloaded fine and then failed to be moved into place with
// "operation not permitted", which stalled the sync for everything behind it.
//
// Reserved characters are replaced with their full-width counterparts, which look nearly
// identical and, being distinct code points, keep the mapping one-to-one — so two different
// server names rarely collide, and when they do [Disambiguate] settles it.
package localname

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"runtime"
	"strings"
)

// restricted maps each character the restrictive filesystems reject to a look-alike they
// accept. The path separator is deliberately absent: it is structure, not content.
var restricted = map[rune]rune{
	'"':  '＂',
	'*':  '＊',
	':':  '：',
	'<':  '＜',
	'>':  '＞',
	'?':  '？',
	'\\': '＼',
	'|':  '｜',
}

// reserved is what this platform actually rejects. A var so tests can set it.
var reserved = platformReserved()

// platformReserved returns the substitutions this platform needs.
//
// Only Android's storage (FUSE with FAT semantics) and Windows reject these characters. APFS,
// ext4 and the rest take all of them, and rewriting names there would be worse than useless:
// files already synced under their real names would be renamed on the next pass, on every
// machine that updated. Whatever a platform does, the name on the server is left alone, so a
// note written on a Mac still arrives on a phone — under a look-alike name there, and its own
// name everywhere else.
func platformReserved() map[rune]rune {
	switch runtime.GOOS {
	case "android", "windows":
		return restricted
	default:
		return nil
	}
}

// Localize returns the path to use on disk for a server path. Paths that are already
// acceptable come back unchanged, so existing mirrors are not rewritten.
func Localize(relPath string) string {
	if !NeedsLocalizing(relPath) {
		return relPath
	}
	var b strings.Builder
	b.Grow(len(relPath))
	for _, r := range relPath {
		switch {
		case r == '/':
			b.WriteRune(r)
		case reserved[r] != 0:
			b.WriteRune(reserved[r])
		case r < 0x20 || r == 0x7f:
			// Control characters have no look-alike and no business in a filename.
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NeedsLocalizing reports whether Localize would change the path.
func NeedsLocalizing(relPath string) bool {
	for _, r := range relPath {
		if r == '/' {
			continue
		}
		if reserved[r] != 0 || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Disambiguate returns a variant of a localized path for the given node, for the rare case
// where two server paths localize to the same name (a literal "a？.md" alongside "a?.md").
// The suffix is derived from the node id, so a node keeps its local name across runs.
func Disambiguate(localPath, nodeID string) string {
	sum := sha256.Sum256([]byte(nodeID))
	suffix := "~" + hex.EncodeToString(sum[:])[:6]
	dir, file := path.Split(localPath)
	ext := path.Ext(file)
	return dir + strings.TrimSuffix(file, ext) + suffix + ext
}
