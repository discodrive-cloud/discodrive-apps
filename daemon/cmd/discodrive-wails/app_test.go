package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"discodrive.org/daemon/internal/protocol"
)

func TestNameTooLong(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"short ascii", "a.pdf", false},
		{"exactly 255 bytes", strings.Repeat("a", 255), false},
		{"256 bytes", strings.Repeat("a", 256), true},
		// Cyrillic is 2 bytes/char in UTF-8: 128 chars = 256 bytes > 255.
		{"128 cyrillic chars (256 bytes)", strings.Repeat("я", 128), true},
		{"127 cyrillic chars (254 bytes)", strings.Repeat("я", 127), false},
		// The real-world file that triggered the server 500 (258 bytes).
		{"reported pdf (258 bytes)",
			"Заявление_о_прекраении_предпринимательской_деятельности,_в_отношении_которой_применялась_патентная_система_налогообложения_(форма_N_26.5-4).pdf",
			true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nameTooLong(c.in); got != c.want {
				t.Fatalf("nameTooLong(%d bytes) = %v, want %v", len(c.in), got, c.want)
			}
		})
	}
}

func TestLinuxDesktopEntry(t *testing.T) {
	out := linuxDesktopEntry("/home/u/.local/bin/My App", "", "/home/u/.local/share/discodrive/discodrive.png")
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		`Exec="/home/u/.local/bin/My App"`, // quoted so spaces survive
		"X-GNOME-Autostart-enabled=true",
		"StartupWMClass=discodrive", // matches the window's program name
		"Icon=/home/u/.local/share/discodrive/discodrive.png",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("desktop entry missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, hiddenFlag) {
		t.Fatalf("entry should not carry %s when flag is empty:\n%s", hiddenFlag, out)
	}

	withFlag := linuxDesktopEntry("/home/u/app", hiddenFlag, "")
	if !strings.Contains(withFlag, `Exec="/home/u/app" --hidden`) {
		t.Fatalf("entry missing flagged exec:\n%s", withFlag)
	}
	if strings.Contains(withFlag, "Icon=") {
		t.Fatalf("entry without an icon path should not carry Icon=:\n%s", withFlag)
	}
}

func TestLinuxLauncherEntry(t *testing.T) {
	out := linuxLauncherEntry("/opt/Disco Drive/DiscoDrive", "/home/u/.local/share/discodrive/discodrive.png")
	for _, want := range []string{
		"[Desktop Entry]",
		`Exec="/opt/Disco Drive/DiscoDrive"`,
		"StartupWMClass=discodrive",
		"Icon=/home/u/.local/share/discodrive/discodrive.png",
		"Terminal=false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("launcher entry missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "X-GNOME-Autostart") {
		t.Fatalf("launcher entry must not be an autostart entry:\n%s", out)
	}
}

func TestCheckOpenURL(t *testing.T) {
	ok := []struct{ u, host string }{
		{"https://drive.example.com/pair?code=1", ""},
		{"http://localhost:8080/pair", ""},
		{"https://Drive.Example.com/apple-profile/x", "drive.example.com"},
		{"https://drive.example.com:8443/p", "DRIVE.example.com"},
	}
	for _, c := range ok {
		if _, err := checkOpenURL(c.u, c.host); err != nil {
			t.Errorf("checkOpenURL(%q, %q) = %v", c.u, c.host, err)
		}
	}
	bad := []struct{ u, host string }{
		{"", ""},
		{"smb://attacker/share", ""},
		{"vnc://attacker", ""},
		{"x-apple.systempreferences:com.apple.preference", ""},
		{"file:///etc/passwd", ""},
		{"javascript:alert(1)", ""},
		{"ms-settings:", ""},
		{"/relative/path", ""},
		{"https://user:pw@drive.example.com/", ""},
		{"https://evil.example.net/p", "drive.example.com"},
		{"https://drive.example.com.evil.net/p", "drive.example.com"},
		{"smb://drive.example.com/share", "drive.example.com"},
	}
	for _, c := range bad {
		if _, err := checkOpenURL(c.u, c.host); err == nil {
			t.Errorf("checkOpenURL(%q, %q) accepted", c.u, c.host)
		}
	}
}

// With nothing prepared there is nothing to open, whatever the web view asks.
func TestOpenURLsWithoutPreparedLink(t *testing.T) {
	a := &App{}
	if err := a.OpenPairURL(); err == nil {
		t.Error("OpenPairURL opened with no pairing started")
	}
	a.lastVerifyURL = "smb://attacker/share"
	if err := a.OpenPairURL(); err == nil {
		t.Error("OpenPairURL opened a non-http link")
	}
}

func TestIsRisky(t *testing.T) {
	for _, p := range []string{"/c/Q3 report.pdf.js", "x.EXE", "a.Lnk", "run.command", "Evil.app",
		"x.js.", "x.vbs  ", "link.webloc", "s.sh", "t.terminal", "u.url", `C:\x\y.ps1`,
		"app.desktop", "a.cpl", "a.msc", "a.scf", "a.appref-ms", "a.settingcontent-ms", "help.CHM",
		"disk.iso", "disk.img", "disk.vhd", "disk.vhdx", "setup.pkg", "setup.dmg", "s.scpt", "Auto.workflow"} {
		if !isRisky(p) {
			t.Errorf("isRisky(%q) = false", p)
		}
	}
	for _, p := range []string{"report.pdf", "photo.JPG", "notes.md", "archive.zip", "folder", "Secrets-1a2b3c4d", "x.jsx"} {
		if isRisky(p) {
			t.Errorf("isRisky(%q) = true", p)
		}
	}
}

func TestCheckChosen(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "picked.txt")
	dir := filepath.Join(root, "folder")
	a := &App{}
	for _, p := range []string{file, filepath.Join(dir, "a.txt"), dir, "relative.txt", ""} {
		if err := a.checkChosen(p); !errors.Is(err, errNotChosen) {
			t.Errorf("unregistered %q: %v", p, err)
		}
	}
	// Below a chosen folder, paths are checked against the disk, so they must exist.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.allowPaths(file, dir+string(filepath.Separator), "relative.txt")
	for _, p := range []string{file, dir, filepath.Join(dir, "sub", "a.txt")} {
		if err := a.checkChosen(p); err != nil {
			t.Errorf("registered %q: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(root, "other.txt"), dir + "x", filepath.Join(dir, "..", "other.txt"), "relative.txt", root} {
		if err := a.checkChosen(p); !errors.Is(err, errNotChosen) {
			t.Errorf("%q passed as chosen: %v", p, err)
		}
	}
	a.forgetPath(file)
	if err := a.checkChosen(file); !errors.Is(err, errNotChosen) {
		t.Error("forgotten path still accepted")
	}
}

// eventsApp is a paired App whose upload client talks to srv, with upload events captured.
func eventsApp(t *testing.T, srvURL string) (*App, chan uploadEvent) {
	t.Helper()
	events := make(chan uploadEvent, 16)
	a := &App{ctx: context.Background(), uploadSem: make(chan struct{}, 3), ready: true}
	a.up = protocol.NewUnscoped(srvURL, "device")
	a.uploadEvents = func(name string, e uploadEvent) {
		if name != "upload:progress" {
			events <- e
		}
	}
	return a, events
}

func TestUploadPathsRejectsUnchosenPaths(t *testing.T) {
	var reached atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true); w.WriteHeader(500) }))
	defer srv.Close()
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, events := eventsApp(t, srv.URL)
	a.UploadPaths("root", "", []string{secret})
	select {
	case e := <-events:
		if !strings.Contains(e.Error, errNotChosen.Error()) {
			t.Fatalf("event %+v, want %q", e, errNotChosen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no error event for an unchosen path")
	}
	if reached.Load() {
		t.Fatal("the server was contacted for an unchosen path")
	}
}

func TestUploadPathsUploadsChosenPaths(t *testing.T) {
	requests := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	picked := filepath.Join(t.TempDir(), "picked.txt")
	if err := os.WriteFile(picked, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, events := eventsApp(t, srv.URL)
	a.allowPaths(picked)
	a.UploadPaths("root", "", []string{picked})
	select {
	case e := <-events:
		if strings.Contains(e.Error, errNotChosen.Error()) {
			t.Fatalf("a chosen path was refused: %+v", e)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("upload never finished")
	}
	select {
	case <-requests:
	default:
		t.Fatal("a chosen path never reached the upload flow")
	}
}

func TestAddFilesToVaultRejectsUnchosenPaths(t *testing.T) {
	a := &App{}
	err := a.AddFilesToVault("vault", []string{filepath.Join(t.TempDir(), "x.txt")})
	if !errors.Is(err, errNotChosen) {
		t.Fatalf("AddFilesToVault = %v, want errNotChosen", err)
	}
	chosen := filepath.Join(t.TempDir(), "y.txt")
	a.allowPaths(chosen)
	if err := a.checkChosen(chosen); err != nil {
		t.Fatalf("a chosen path was refused: %v", err)
	}
}

// A symlink inside a chosen folder must not lead to files outside it.
func TestCheckChosenRefusesSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	chosen := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(chosen, "link-to-home")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	inside := filepath.Join(chosen, "sub", "ok.txt")
	if err := os.MkdirAll(filepath.Dir(inside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	a.allowPaths(chosen)
	if err := a.checkChosen(filepath.Join(chosen, "link-to-home", ".ssh", "id_rsa")); !errors.Is(err, errNotChosen) {
		t.Fatalf("path through a symlink out of the chosen folder: %v", err)
	}
	if err := a.checkChosen(inside); err != nil {
		t.Fatalf("real file inside the chosen folder refused: %v", err)
	}
}

// A retry after a partly failed import resends the whole batch, so no path of it may be
// forgotten until every copy succeeded.
func TestAddFilesToVaultKeepsPathsUntilBatchSucceeds(t *testing.T) {
	a := &App{}
	first, missing := filepath.Join(t.TempDir(), "a.txt"), filepath.Join(t.TempDir(), "gone.txt")
	a.allowPaths(first, missing)
	// Unpaired: nothing is copied, and nothing may be forgotten.
	_ = a.AddFilesToVault("vault", []string{first, missing})
	for _, p := range []string{first, missing} {
		if err := a.checkChosen(p); err != nil {
			t.Fatalf("%s forgotten without a completed batch: %v", p, err)
		}
	}
}

// Explorer splits its command line on commas; a folder name must never become switches.
func TestRevealCommandWindowsUsesShellHandler(t *testing.T) {
	p := `C:\Users\u\cache\a,/root,C:\evil\file.js`
	name, args := revealCommand("windows", p)
	if name != "rundll32" || len(args) != 2 || args[0] != "url.dll,FileProtocolHandler" {
		t.Fatalf("windows reveal = %s %q", name, args)
	}
	if name, args := revealCommand("darwin", "/x/a,b.app"); name != "open" || args[0] != "-R" || args[1] != "/x/a,b.app" {
		t.Fatalf("darwin reveal = %s %q", name, args)
	}
}

// A 409 from purge/empty-trash (a trashed folder still holds a live item) is tagged for the
// UI to explain; anything else passes through untouched.
func TestTrashBlockedTagsOnlyA409(t *testing.T) {
	if err := trashBlocked(&protocol.StatusError{Op: "/files/trash", Code: 409, Body: `{"error":"folder holds items"}`}); err == nil || !strings.HasPrefix(err.Error(), "trash_blocked:") {
		t.Errorf("409: %v, want a trash_blocked: tag", err)
	}
	other := &protocol.StatusError{Code: 500}
	if err := trashBlocked(other); err != other {
		t.Errorf("500: %v, want it unchanged", err)
	}
	if trashBlocked(nil) != nil {
		t.Error("nil must stay nil")
	}
}
