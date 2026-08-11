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

// windowsDeviceNames are the names Windows reserves for devices. They are rejected whatever
// the case and whatever the extension: "nul.md" cannot be created either.
var windowsDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// policy is what the platform underfoot refuses to store.
type policy struct {
	// runes are the characters to substitute; nil where the filesystem takes them all.
	runes map[rune]rune
	// windowsNames covers the two rules that are Windows's alone: names reserved for
	// devices, and names ending in a dot or a space.
	windowsNames bool
}

// current is this platform's policy. A var so tests can set it.
var current = platformPolicy()

// platformPolicy returns what this platform needs.
//
// Only Android's storage (FUSE with FAT semantics) and Windows reject these characters. APFS,
// ext4 and the rest take all of them, and rewriting names there would be worse than useless:
// files already synced under their real names would be renamed on the next pass, on every
// machine that updated. Whatever a platform does, the name on the server is left alone, so a
// note written on a Mac still arrives on a phone — under a look-alike name there, and its own
// name everywhere else.
func platformPolicy() policy {
	switch runtime.GOOS {
	case "android":
		return policy{runes: restricted}
	case "windows":
		return policy{runes: restricted, windowsNames: true}
	default:
		return policy{}
	}
}

// Localize returns the path to use on disk for a server path. Paths that are already
// acceptable come back unchanged, so existing mirrors are not rewritten.
func Localize(relPath string) string {
	if !NeedsLocalizing(relPath) {
		return relPath
	}
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		parts[i] = localizeComponent(part)
	}
	return strings.Join(parts, "/")
}

// localizeComponent applies the policy to one path segment.
func localizeComponent(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case current.runes[r] != 0:
			b.WriteRune(current.runes[r])
		case r < 0x20 || r == 0x7f:
			// Control characters have no look-alike and no business in a filename.
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if !current.windowsNames {
		return out
	}
	// A trailing dot or space is dropped by Windows rather than refused, which would
	// silently merge two names; substitute so the name stays its own.
	out = fixTrailing(out)
	if base, _, _ := strings.Cut(out, "."); windowsDeviceNames[strings.ToLower(base)] {
		// Suffix the stem, so "nul.md" stays a Markdown file.
		if i := strings.Index(out, "."); i >= 0 {
			return out[:i] + "_" + out[i:]
		}
		return out + "_"
	}
	return out
}

// fixTrailing replaces dots and spaces at the end of a name with look-alikes Windows keeps.
func fixTrailing(name string) string {
	end := len(name)
	for end > 0 {
		r := rune(name[end-1])
		if r != '.' && r != ' ' {
			break
		}
		end--
	}
	if end == len(name) {
		return name
	}
	var b strings.Builder
	b.WriteString(name[:end])
	for _, r := range name[end:] {
		if r == '.' {
			b.WriteRune('．')
		} else {
			b.WriteRune('\u00a0') // no-break space: looks the same, and Windows keeps it
		}
	}
	return b.String()
}

// NeedsLocalizing reports whether Localize would change the path.
func NeedsLocalizing(relPath string) bool {
	for _, part := range strings.Split(relPath, "/") {
		for _, r := range part {
			if current.runes[r] != 0 || r < 0x20 || r == 0x7f {
				return true
			}
		}
		if !current.windowsNames || part == "" {
			continue
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return true
		}
		if base, _, _ := strings.Cut(part, "."); windowsDeviceNames[strings.ToLower(base)] {
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
