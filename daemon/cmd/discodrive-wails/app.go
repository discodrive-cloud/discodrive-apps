package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

// App is the Wails-bound backend. It reuses the tested on-demand desktop Controller,
// so the Wails UI is just a new view layer over the same Go core.
type App struct {
	ctx   context.Context
	ctrl  *desktop.Controller
	idx   *index.Index
	ready bool

	up        *protocol.Client // chunked-upload + EnsureDir client (separate JWT cache)
	uploadSem chan struct{}    // caps concurrent uploads at 3
	uploadSeq atomic.Int64     // unique id per uploaded file

	startHidden bool // launched with --hidden (auto-start minimized): no dock icon, tray only
}

// Node is a frontend-facing tree entry (marshalled to JSON for the Vue UI).
type Node struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	RelPath string `json:"relPath"`
	IsDir   bool   `json:"isDir"`
	State   string `json:"state"`
	Stale   bool   `json:"stale"`
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.uploadSem = make(chan struct{}, 3)
	// Native file drops forward their paths to the frontend, which uploads them into
	// the current folder.
	wruntime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		wruntime.EventsEmit(ctx, "upload:drop", paths)
	})

	if profile, err := desktop.ProfileDir(); err == nil {
		// Ignore the error: not paired yet → ready stays false and the UI shows pairing.
		_ = a.openProfile(profile)
	}

	// Started minimized (auto-launch): the window is already hidden via StartHidden;
	// also drop the macOS dock icon so the app lives only in the tray until reopened.
	if a.startHidden {
		setDockVisible(false)
	}
}

// openProfile opens the paired profile's controller + upload client and starts a
// background refresh. Shared by startup and the post-pairing flow so the session is
// built identically. Returns an error if the profile is not paired/openable.
func (a *App) openProfile(profile string) error {
	ctrl, idx, err := desktop.Open(profile)
	if err != nil {
		return err
	}
	a.ctrl, a.idx, a.ready = ctrl, idx, true
	// A dedicated upload client for the chunked /upload/* path.
	if cfg, cerr := config.Load(desktop.DesktopConfigPath(profile)); cerr == nil {
		a.up = protocol.NewUnscoped(cfg.ServerURL, cfg.DeviceToken)
	}
	go func() { _, _ = a.ctrl.Refresh(a.ctx) }()
	return nil
}

// PairInfo is returned to the frontend after starting a pairing.
type PairInfo struct {
	UserCode   string `json:"userCode"`
	VerifyURL  string `json:"verifyUrl"`
	DeviceCode string `json:"deviceCode"`
	Interval   int    `json:"interval"`
}

// verificationURL makes the server's (possibly relative) verification_uri absolute.
func verificationURL(server, verifyURI string) string {
	if strings.HasPrefix(verifyURI, "http://") || strings.HasPrefix(verifyURI, "https://") {
		return verifyURI
	}
	return strings.TrimRight(server, "/") + "/" + strings.TrimLeft(verifyURI, "/")
}

// PairInit starts a device-code pairing with serverURL, opens the verification URL in
// the browser, and returns the user code + absolute URL for the UI to display.
func (a *App) PairInit(serverURL string) (PairInfo, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "desktop"
	}
	p, err := protocol.PairInit(a.ctx, serverURL, hostname, "desktop")
	if err != nil {
		return PairInfo{}, err
	}
	verifyURL := verificationURL(serverURL, p.VerificationURI)
	wruntime.BrowserOpenURL(a.ctx, verifyURL)
	return PairInfo{UserCode: p.UserCode, VerifyURL: verifyURL, DeviceCode: p.DeviceCode, Interval: p.Interval}, nil
}

// PairPoll blocks until the pairing is approved, then saves the config and opens the
// session in-process (no restart). interval is the server-suggested poll seconds.
func (a *App) PairPoll(serverURL, deviceCode string, interval int) error {
	iv := time.Duration(interval) * time.Second
	if iv <= 0 {
		iv = 2 * time.Second
	}
	token, err := protocol.PairPoll(a.ctx, serverURL, deviceCode, iv)
	if err != nil {
		return err
	}
	profile, err := desktop.ProfileDir()
	if err != nil {
		return err
	}
	if err := desktop.SaveConfig(profile, config.Config{ServerURL: serverURL, DeviceToken: token}); err != nil {
		return err
	}
	return a.openProfile(profile)
}

// OpenPairURL reopens the verification URL in the browser (the "open link again"
// button), mitigating a server login redirect that drops the code.
func (a *App) OpenPairURL(u string) {
	if a.ctx != nil && u != "" {
		wruntime.BrowserOpenURL(a.ctx, u)
	}
}

// Ready reports whether a paired profile was opened.
func (a *App) Ready() bool { return a.ready }

// List returns the children of relPath ("" = root) as frontend nodes.
func (a *App) List(relPath string) ([]Node, error) {
	if !a.ready {
		return []Node{}, nil
	}
	entries, err := a.ctrl.List(relPath)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(entries))
	for _, e := range entries {
		out = append(out, Node{
			ID:      e.Node.NodeID,
			Name:    path.Base(e.Node.RelPath),
			RelPath: e.Node.RelPath,
			IsDir:   e.Node.IsDir,
			State:   e.State,
			Stale:   e.Stale,
		})
	}
	return out, nil
}

// Refresh pulls the change delta from the server into the local index.
func (a *App) Refresh() error {
	if !a.ready {
		return nil
	}
	_, err := a.ctrl.Refresh(a.ctx)
	return err
}

// OpenFile downloads (if needed) and opens a file in its default application.
func (a *App) OpenFile(nodeID string) error {
	if !a.ready {
		return nil
	}
	p, err := a.ctrl.Open(a.ctx, nodeID)
	if err != nil {
		return err
	}
	openLocal(p)
	return nil
}

// NewFolder creates a folder named name under parentID ("" = root).
func (a *App) NewFolder(parentID, name string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.CreateFolder(a.ctx, parentID, name)
}

// Rename renames nodeID to newName.
func (a *App) Rename(nodeID, newName string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.Rename(a.ctx, nodeID, newName)
}

// Move reparents nodeID under newParentID ("" = root).
func (a *App) Move(nodeID, newParentID string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.Move(a.ctx, nodeID, newParentID)
}

// Delete removes nodeID on the server.
func (a *App) Delete(nodeID string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.Delete(a.ctx, nodeID)
}

// Pin downloads and keeps nodeID locally.
func (a *App) Pin(nodeID string) error {
	if !a.ready {
		return nil
	}
	_, err := a.ctrl.Pin(a.ctx, nodeID)
	return err
}

// Unpin demotes a pinned node back to cached.
func (a *App) Unpin(nodeID string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.Unpin(nodeID)
}

// RemoveLocal deletes the local cached copy of nodeID.
func (a *App) RemoveLocal(nodeID string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.RemoveLocal(nodeID)
}

// RevealFile ensures nodeID is cached locally, then opens its containing folder in
// the OS file manager. This is the "Download" action: materialise a copy on disk.
func (a *App) RevealFile(nodeID string) error {
	if !a.ready {
		return nil
	}
	p, err := a.ctrl.Open(a.ctx, nodeID)
	if err != nil {
		return err
	}
	openLocal(filepath.Dir(p))
	return nil
}

// uploadEvent is the payload emitted to the frontend for upload:progress/done/error.
type uploadEvent struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Sent  int64  `json:"sent"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
	// Code is a machine-readable error tag the frontend can localize (e.g.
	// "name_too_long"); empty for ordinary/opaque errors that show Error verbatim.
	Code string `json:"code,omitempty"`
}

// maxNameBytes is the per-path-component limit on common server filesystems (ext4
// and friends cap a single name at 255 bytes). A longer name makes the server 500
// at finalize, so we reject it client-side with a clear message instead.
const maxNameBytes = 255

// nameTooLong reports whether a file name exceeds the server's filesystem limit.
// len() counts bytes, which is the right unit — non-ASCII (e.g. Cyrillic) names are
// multi-byte in UTF-8, so a 143-character name can be 258 bytes.
func nameTooLong(name string) bool { return len(name) > maxNameBytes }

// PickFiles opens a native multi-file dialog and returns the chosen absolute paths.
func (a *App) PickFiles() ([]string, error) {
	if a.ctx == nil {
		return nil, nil
	}
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Upload files"})
}

// PickFolder opens a native folder dialog and returns the chosen absolute path.
func (a *App) PickFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Upload folder"})
}

// UploadPaths uploads each path into the current folder: files go into parentID, while
// directories are recreated under parentRelPath (their subtree mirrored on the server).
// Up to 3 files upload concurrently; progress is reported via upload:* events.
func (a *App) UploadPaths(parentID, parentRelPath string, paths []string) {
	if !a.ready || a.up == nil {
		return
	}
	for _, p := range paths {
		p := p
		fi, err := os.Stat(p)
		if err != nil {
			a.emitUpload("upload:error", uploadEvent{ID: a.uploadSeq.Add(1), Name: filepath.Base(p), Error: err.Error()})
			continue
		}
		if fi.IsDir() {
			go a.uploadFolder(parentRelPath, p)
		} else {
			go a.uploadFile(parentID, p)
		}
	}
}

// uploadFile uploads one local file into parentID with progress events. It blocks on
// the upload semaphore (max 3 concurrent), so run it in a goroutine.
func (a *App) uploadFile(parentID, p string) {
	a.uploadSem <- struct{}{}
	defer func() { <-a.uploadSem }()

	id := a.uploadSeq.Add(1)
	name := filepath.Base(p)
	if nameTooLong(name) {
		a.emitUpload("upload:error", uploadEvent{
			ID: id, Name: name, Code: "name_too_long",
			Error: fmt.Sprintf("file name is %d bytes; the server limit is %d", len(name), maxNameBytes),
		})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		a.emitUpload("upload:error", uploadEvent{ID: id, Name: name, Error: err.Error()})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		if err != nil {
			a.emitUpload("upload:error", uploadEvent{ID: id, Name: name, Error: err.Error()})
		}
		return
	}
	var lastEmit int64
	err = NewUploader(a.up).Upload(a.ctx, parentID, name, f, fi.Size(), fi.ModTime(), func(sent, total int64) {
		if sent-lastEmit < 512*1024 && sent != total {
			return // throttle: emit at most every ~512 KiB (plus the final byte)
		}
		lastEmit = sent
		a.emitUpload("upload:progress", uploadEvent{ID: id, Name: name, Sent: sent, Total: total})
	})
	if err != nil {
		a.emitUpload("upload:error", uploadEvent{ID: id, Name: name, Error: err.Error()})
		return
	}
	a.emitUpload("upload:done", uploadEvent{ID: id, Name: name, Sent: fi.Size(), Total: fi.Size()})
}

// uploadFolder mirrors a local folder under parentRelPath: it walks the tree, ensures
// each server subfolder exists (EnsureDir is idempotent and creates intermediates),
// and uploads every file into its folder. Folder uploads share the 3-file cap.
func (a *App) uploadFolder(parentRelPath, folderPath string) {
	root := filepath.Base(folderPath)
	dirID := map[string]string{} // server relPath -> node id (memoised)

	ensure := func(relDir string) (string, error) {
		full := path.Join(parentRelPath, relDir)
		if id, ok := dirID[full]; ok {
			return id, nil
		}
		n, err := a.up.EnsureDir(a.ctx, full)
		if err != nil {
			return "", err
		}
		dirID[full] = n.NodeID
		return n.NodeID, nil
	}

	_ = filepath.WalkDir(folderPath, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(folderPath, p)
		if err != nil {
			return nil
		}
		relDir := path.Join(root, path.Dir(filepath.ToSlash(rel))) // e.g. "myfolder/sub"
		pid, err := ensure(relDir)
		if err != nil {
			a.emitUpload("upload:error", uploadEvent{ID: a.uploadSeq.Add(1), Name: filepath.ToSlash(rel), Error: err.Error()})
			return nil
		}
		go a.uploadFile(pid, p)
		return nil
	})
}

func (a *App) emitUpload(event string, e uploadEvent) {
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, event, e)
	}
}

// VaultInfo is a frontend-facing vault entry.
type VaultInfo struct {
	Name    string `json:"name"`
	RelPath string `json:"relPath"`
	Open    bool   `json:"open"`
}

// Vaults lists the server vaults with their open state.
func (a *App) Vaults() []VaultInfo {
	if !a.ready {
		return []VaultInfo{}
	}
	refs, err := a.ctrl.ListVaults()
	if err != nil {
		return []VaultInfo{}
	}
	out := make([]VaultInfo, 0, len(refs))
	for _, r := range refs {
		out = append(out, VaultInfo{Name: r.Name, RelPath: r.RelPath, Open: a.ctrl.IsVaultOpen(r.RelPath)})
	}
	return out
}

// CreateVault creates an encrypted vault named name (at the vault root) protected by
// password, uploads it, and returns the recovery phrase to show the user.
func (a *App) CreateVault(name, password string) (string, error) {
	if !a.ready {
		return "", nil
	}
	return a.ctrl.CreateVault(a.ctx, "", name, password)
}

// CloseAllVaults closes every open vault (re-encrypt + upload). Called when leaving the
// vaults view so plaintext does not linger and changes are saved.
func (a *App) CloseAllVaults() error {
	if !a.ready {
		return nil
	}
	return a.ctrl.CloseAllVaults(a.ctx)
}

// OpenVault decrypts the vault and opens its plaintext folder in the OS file manager.
func (a *App) OpenVault(relPath, password string) error {
	if !a.ready {
		return nil
	}
	plainDir, err := a.ctrl.OpenVault(a.ctx, relPath, password)
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// OpenVaultWithRecovery opens the vault using its recovery phrase (when the password is
// lost) and reveals the plaintext folder.
func (a *App) OpenVaultWithRecovery(relPath, phrase string) error {
	if !a.ready {
		return nil
	}
	plainDir, err := a.ctrl.OpenVaultWithRecovery(a.ctx, relPath, phrase)
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// OpenVaultFolder reveals an already-open vault's plaintext folder.
func (a *App) OpenVaultFolder(relPath string) error {
	if !a.ready {
		return nil
	}
	plainDir, err := a.ctrl.OpenVault(a.ctx, relPath, "")
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// CloseVault re-encrypts the open vault and uploads it back to the server.
func (a *App) CloseVault(relPath string) error {
	if !a.ready {
		return nil
	}
	return a.ctrl.CloseVault(a.ctx, relPath)
}

// AddFilesToVault copies the given local files into the open vault's plaintext folder.
// The vault stays open; encryption + upload happen on Close.
func (a *App) AddFilesToVault(relPath string, paths []string) error {
	if !a.ready {
		return nil
	}
	plainDir, err := a.ctrl.OpenVault(a.ctx, relPath, "")
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := copyFileInto(plainDir, p); err != nil {
			return err
		}
	}
	return nil
}

// copyFileInto copies src into dir under src's base name.
func copyFileInto(dir, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(dir, filepath.Base(src)))
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ShowWindow re-shows the main window (called from the tray "Open" item) and brings
// the dock icon back.
func (a *App) ShowWindow() {
	setDockVisible(true)
	if a.ctx != nil {
		wruntime.WindowShow(a.ctx)
	}
}

// QuitApp terminates the app (called from the tray "Quit" item).
//
// wruntime.Quit does NOT reliably exit in the systray+Wails hybrid on macOS: it tears
// down the Wails window/run loop (which was pumping tray events) but the process keeps
// running, leaving an orphaned tray icon (the OS only releases the NSStatusItem when
// the process dies) and a dead menu. So we remove the status item ourselves and
// force-exit the process to guarantee a clean quit.
func (a *App) QuitApp() {
	// Close any open vaults first so plaintext is wiped and pending changes are saved
	// before the process exits (os.Exit skips deferred cleanup).
	if a.ready {
		_ = a.ctrl.CloseAllVaults(a.ctx)
	}
	systray.Quit()
	os.Exit(0)
}

func openLocal(p string) {
	switch runtime.GOOS {
	case "windows":
		// Open via the shell file handler directly instead of `cmd /c start`, which
		// re-parses the path through cmd.exe. p's basename comes from server/vault
		// filenames, so routing it through cmd.exe risks argument injection.
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", p).Start()
	case "darwin":
		_ = exec.Command("open", p).Start()
	default:
		_ = exec.Command("xdg-open", p).Start()
	}
}
