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

// maxComponent is the longest a single name may be. Every filesystem in play settled on 255;
// they disagree only on the unit, which [policy.utf16Units] carries.
//
// The path as a whole needs no attention: Go's os package adds the \\?\ prefix itself when a
// Windows path outgrows MAX_PATH (see os.fixLongPath), and nothing else is close to a limit.
const maxComponent = 255

// policy is what the platform underfoot refuses to store.
type policy struct {
	// runes are the characters to substitute; nil where the filesystem takes them all.
	runes map[rune]rune
	// windowsNames covers the two rules that are Windows's alone: names reserved for
	// devices, and names ending in a dot or a space.
	windowsNames bool
	// utf16Units counts a name the way NTFS does. Elsewhere the limit is on UTF-8 bytes,
	// which runs out twice as fast for Cyrillic: about 127 characters, well within reach
	// of a note titled with a question.
	utf16Units bool
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
		return policy{runes: restricted, windowsNames: true, utf16Units: true}
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
		return fit(out)
	}
	// A trailing dot or space is dropped by Windows rather than refused, which would
	// silently merge two names; substitute so the name stays its own.
	out = fixTrailing(out)
	if base, _, _ := strings.Cut(out, "."); windowsDeviceNames[strings.ToLower(base)] {
		// Suffix the stem, so "nul.md" stays a Markdown file.
		if i := strings.Index(out, "."); i >= 0 {
			return fit(out[:i] + "_" + out[i:])
		}
		return fit(out + "_")
	}
	return fit(out)
}

// nameLen measures a name in the units the filesystem counts.
func nameLen(name string) int {
	if !current.utf16Units {
		return len(name)
	}
	n := 0
	for _, r := range name {
		n++
		if r > 0xFFFF { // outside the BMP: two UTF-16 units
			n++
		}
	}
	return n
}

// fit shortens a name that no filesystem would accept, keeping the extension so the file
// still opens in the right application. Two long names often share a long prefix — a run of
// notes from the same source, say — so a short digest of the original goes in as well:
// truncation alone would merge them into one file.
func fit(name string) string {
	if nameLen(name) <= maxComponent {
		return name
	}
	ext := path.Ext(name)
	if nameLen(ext) > 16 { // not an extension, just a dot late in a long name
		ext = ""
	}
	sum := sha256.Sum256([]byte(name))
	digest := "~" + hex.EncodeToString(sum[:])[:6]

	budget := maxComponent - nameLen(ext) - nameLen(digest)
	stem := strings.TrimSuffix(name, ext)
	var b strings.Builder
	for _, r := range stem {
		rl := 1
		if !current.utf16Units {
			rl = len(string(r))
		} else if r > 0xFFFF {
			rl = 2
		}
		if nameLen(b.String())+rl > budget {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + digest + ext
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
		// The length limit holds everywhere, unlike the rest of this.
		if nameLen(part) > maxComponent {
			return true
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
