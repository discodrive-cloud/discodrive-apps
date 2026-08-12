package localname

import (
	"runtime"
	"strings"
	"testing"
)

// onRestrictedPlatform points the package at the substitutions Android and Windows need, so
// the mapping can be tested from a Mac.
func onRestrictedPlatform(t *testing.T) {
	t.Helper()
	prev := current
	current = policy{runes: restricted}
	t.Cleanup(func() { current = prev })
}

// Android's internal storage is a FUSE layer with FAT semantics, and Windows agrees with it:
// several characters a server path may legitimately contain cannot exist in a filename. A
// note called "…worth it?.md" downloaded fine and then failed to be renamed into place with
// EPERM, which stalled the whole sync.

func TestLocalizeReplacesReservedCharacters(t *testing.T) {
	onRestrictedPlatform(t)
	cases := []struct{ in, want string }{
		{`notes/is it worth it?.md`, "notes/is it worth it？.md"},
		{`a"b.txt`, "a＂b.txt"},
		{`re: subject.eml`, "re： subject.eml"},
		{`a*b?c<d>e|f\g.txt`, "a＊b？c＜d＞e｜f＼g.txt"},
		{"tab\tname.txt", "tab_name.txt"},
	}
	for _, c := range cases {
		if got := Localize(c.in); got != c.want {
			t.Errorf("Localize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Paths a filesystem already accepts must come through untouched, or every existing mirror
// would be rewritten on upgrade.
func TestLocalizeLeavesOrdinaryPathsAlone(t *testing.T) {
	onRestrictedPlatform(t)
	for _, p := range []string{
		"notes/plain.md",
		"Проекты/заметка.md",
		"a.b/c-d_e (1).txt",
		"",
	} {
		if got := Localize(p); got != p {
			t.Errorf("Localize(%q) = %q, want it unchanged", p, got)
		}
	}
}

// The separator is structure, not content: it must survive so the path keeps its shape.
func TestLocalizeKeepsSeparators(t *testing.T) {
	onRestrictedPlatform(t)
	if got := Localize("a?b/c:d/e.md"); got != "a？b/c：d/e.md" {
		t.Errorf("got %q", got)
	}
}

func TestNeedsLocalizing(t *testing.T) {
	onRestrictedPlatform(t)
	if NeedsLocalizing("notes/plain.md") {
		t.Error("plain path should not need localizing")
	}
	if !NeedsLocalizing("notes/why?.md") {
		t.Error("path with ? should need localizing")
	}
}

// Two different server names can localize to the same thing — "a?.md" and the already
// full-width "a？.md". Disambiguate deterministically, keyed on the node the name belongs to,
// so the same node keeps the same local name across runs.
func TestDisambiguate(t *testing.T) {
	first := Disambiguate("notes/a？.md", "node-1")
	if first == "notes/a？.md" {
		t.Fatal("Disambiguate must change the name")
	}
	if again := Disambiguate("notes/a？.md", "node-1"); again != first {
		t.Errorf("not stable: %q then %q", first, again)
	}
	if other := Disambiguate("notes/a？.md", "node-2"); other == first {
		t.Error("different nodes must get different names")
	}
	// The extension has to survive, or the file stops opening in the right app.
	if got := Disambiguate("notes/a.md", "node-1"); got[len(got)-3:] != ".md" {
		t.Errorf("extension lost: %q", got)
	}
}

// Everywhere else the name is left exactly as the server has it. Rewriting names on a
// filesystem that accepts them would rename files that are already synced — on every machine
// that took the update — for nothing.
func TestLocalizeIsAPassThroughWhereNothingIsRejected(t *testing.T) {
	if runtime.GOOS == "android" || runtime.GOOS == "windows" {
		t.Skipf("%s does reject these characters", runtime.GOOS)
	}
	for _, p := range []string{"notes/is it worth it?.md", `a"b*c<d>e|f\\g.txt`, "re: subject.eml"} {
		if got := Localize(p); got != p {
			t.Errorf("Localize(%q) = %q, want it unchanged on %s", p, got, runtime.GOOS)
		}
		if NeedsLocalizing(p) {
			t.Errorf("NeedsLocalizing(%q) is true on %s", p, runtime.GOOS)
		}
	}
}

// onWindows applies the rules Windows adds on top of the character substitutions, so they can
// be tested from a Mac.
func onWindows(t *testing.T) {
	t.Helper()
	prev := current
	current = policy{runes: restricted, windowsNames: true, utf16Units: true}
	t.Cleanup(func() { current = prev })
}

// Windows reserves a handful of names for devices, in any case and with any extension: a note
// called "nul.md" cannot be created at all.
func TestLocalizeEscapesWindowsDeviceNames(t *testing.T) {
	onWindows(t)
	cases := []struct{ in, want string }{
		{"nul.md", "nul_.md"},
		{"NUL.md", "NUL_.md"},
		{"notes/con", "notes/con_"},
		{"com4.txt", "com4_.txt"},
		{"LPT9", "LPT9_"},
		// Only the whole name counts — these are ordinary words.
		{"console.md", "console.md"},
		{"nullable.md", "nullable.md"},
		{"my nul.md", "my nul.md"},
	}
	for _, c := range cases {
		if got := Localize(c.in); got != c.want {
			t.Errorf("Localize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A name may not end in a dot or a space on Windows.
func TestLocalizeFixesTrailingDotsAndSpaces(t *testing.T) {
	onWindows(t)
	cases := []struct{ in, want string }{
		{"note .md", "note .md"}, // inner space is fine
		{"note.", "note．"},
		{"note ", "note "},
		{"deep./file..", "deep．/file．．"},
		{"ok.md", "ok.md"},
	}
	for _, c := range cases {
		if got := Localize(c.in); got != c.want {
			t.Errorf("Localize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Android rejects the characters but has no quarrel with device names or trailing dots, and
// rewriting those would change names for no reason.
func TestAndroidLeavesWindowsOnlyNamesAlone(t *testing.T) {
	prev := current
	current = policy{runes: restricted}
	t.Cleanup(func() { current = prev })

	for _, p := range []string{"nul.md", "note.", "note "} {
		if got := Localize(p); got != p {
			t.Errorf("Localize(%q) = %q, want it unchanged", p, got)
		}
	}
}

// Every filesystem in play caps a single name at 255 — NTFS counts UTF-16 units, ext4 and
// APFS count UTF-8 bytes, so a Cyrillic title runs out of room at about 127 characters. A
// note whose heading is a long question hits this, and the file simply cannot be created.

func TestLocalizeShortensOverlongNames(t *testing.T) {
	long := strings.Repeat("a", 300) + ".md"
	got := Localize(long)
	if len(got) > 255 {
		t.Errorf("length %d, want at most 255", len(got))
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("extension lost: %q", got)
	}
	if got != Localize(long) {
		t.Error("not stable across calls")
	}
}

// Shortening must not merge two different names into one.
func TestShortenedNamesStayDistinct(t *testing.T) {
	a := Localize(strings.Repeat("a", 300) + "-one.md")
	b := Localize(strings.Repeat("a", 300) + "-two.md")
	if a == b {
		t.Errorf("two names collapsed into %q", a)
	}
}

// Counted the way the filesystem counts: two bytes per Cyrillic character off Windows.
func TestOverlongIsMeasuredInTheFilesystemsUnits(t *testing.T) {
	name := strings.Repeat("я", 200) + ".md" // 400 bytes, 200 UTF-16 units
	if !NeedsLocalizing(name) {
		t.Error("400 bytes should be too long where names are counted in bytes")
	}
	if got := Localize(name); len(got) > 255 {
		t.Errorf("length %d bytes, want at most 255", len(got))
	}
}

// Ordinary names keep their exact form, including long-but-legal ones.
func TestNamesWithinTheLimitAreUntouched(t *testing.T) {
	for _, n := range []string{"note.md", strings.Repeat("a", 200) + ".md", "папка/заметка.md"} {
		if got := Localize(n); got != n {
			t.Errorf("Localize(%q) changed it to %q", n, got)
		}
	}
}

// NTFS counts UTF-16 units, so a Cyrillic name that is too long by bytes still fits there.
// Measuring in the wrong unit would shorten names on Windows for no reason — and, worse,
// rename files already sitting on disk.
func TestWindowsCountsUTF16NotBytes(t *testing.T) {
	onWindows(t)
	name := strings.Repeat("я", 200) + ".md" // 400 bytes, but 200 UTF-16 units
	if NeedsLocalizing(name) {
		t.Errorf("%d-character name should fit on NTFS", 200)
	}
	if got := Localize(name); got != name {
		t.Errorf("name was shortened on Windows: %q", got)
	}
}
