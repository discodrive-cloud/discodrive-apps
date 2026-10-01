package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
	"discodrive.org/daemon/internal/fullsync"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

// App is the Wails-bound backend. It reuses the tested on-demand desktop Controller,
// so the Wails UI is just a new view layer over the same Go core.
type App struct {
	accountMu sync.RWMutex // Recovery and sharing requests finish before account teardown.
	mirror    *fullsync.Manager
	ctx       context.Context
	ctrl      *desktop.Controller
	idx       *index.Index
	ready     bool

	up        *protocol.Client // chunked-upload + EnsureDir client (separate JWT cache)
	uploadSem chan struct{}    // caps concurrent uploads at 3
	uploadSeq atomic.Int64     // unique id per uploaded file

	startHidden bool // launched with --hidden (auto-start minimized): no dock icon, tray only

	// allowedPaths holds the local paths the user chose through a native file dialog or
	// dropped on the window. Upload and vault imports read only these (or files under a
	// chosen folder): paths arriving from the web view are never trusted on their own.
	allowMu      sync.Mutex
	allowedPaths map[string]struct{}

	// The browser links this backend opens are the ones it produced itself, never a URL
	// passed in from the web view.
	urlMu         sync.Mutex
	lastVerifyURL string // set by PairInit
	lastDAVURL    string // set by PrepareDAV / PrepareDAVAutomatic

	// The certificate the last PairInit showed for the user to trust, and the pin of the
	// pairing in progress. TrustAndPair pins only what was fetched and shown here — never
	// a fingerprint passed in from the web view.
	pinMu         sync.Mutex
	pendingPin    string // fetched by PairInit, awaiting the user's trust
	pendingPinURL string
	pairPin       string // trusted by TrustAndPair; PairPoll saves it with the token
	pairPinURL    string

	// uploadEvents replaces the Wails event bus in tests.
	uploadEvents func(event string, e uploadEvent)
}

// account returns the paired account's handles. The caller holds accountMu (read or
// write) for as long as it uses them, so Unpair cannot close the index or drop the
// controller underneath a running call.
func (a *App) account() (ctrl *desktop.Controller, idx *index.Index, up *protocol.Client, ok bool) {
	return a.ctrl, a.idx, a.up, a.ready
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
		a.allowPaths(paths...)
		wruntime.EventsEmit(ctx, "upload:drop", paths)
	})

	if profile, err := desktop.ProfileDir(); err == nil {
		a.mirror = fullsync.New(profile)
		// Ignore the error: not paired yet → ready stays false and the UI shows pairing.
		_ = a.openProfile(profile)
	}

	// Started minimized (auto-launch): the window is already hidden via StartHidden;
	// also drop the macOS dock icon so the app lives only in the tray until reopened.
	if a.startHidden {
		setDockVisible(false)
	}
}

// openProfile opens the paired profile's controller and upload client. Shared by
// startup and pairing; the browser awaits the first refresh to show progress and errors.
func (a *App) openProfile(profile string) error {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	ctrl, idx, err := desktop.Open(profile)
	if err != nil {
		return err
	}
	a.ctrl, a.idx, a.ready = ctrl, idx, true
	// A dedicated upload client for the chunked /upload/* path.
	if cfg, cerr := config.Load(desktop.DesktopConfigPath(profile)); cerr == nil {
		a.up = protocol.NewUnscopedPinned(cfg.ServerURL, cfg.DeviceToken, cfg.ServerPin)
		if a.mirror != nil {
			_ = a.mirror.Attach(a.ctx, cfg)
		}
	}
	return nil
}

// PairInfo is returned to the frontend after starting a pairing. When the server's
// certificate is not trusted by the system, NeedsTrust is set and Certificate describes it;
// the UI asks the user and calls TrustAndPair.
type PairInfo struct {
	UserCode    string    `json:"userCode"`
	VerifyURL   string    `json:"verifyUrl"`
	DeviceCode  string    `json:"deviceCode"`
	Interval    int       `json:"interval"`
	NeedsTrust  bool      `json:"needsTrust"`
	Certificate *CertView `json:"certificate,omitempty"`
}

// CertView is a server certificate as the pairing dialog shows it.
type CertView struct {
	Host        string `json:"host"`
	Fingerprint string `json:"fingerprint"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer"`
	NotAfter    string `json:"notAfter"` // RFC 3339
	SelfSigned  bool   `json:"selfSigned"`
}

// verificationURL makes the server's (possibly relative) verification_uri absolute.
func verificationURL(server, verifyURI string) string {
	if strings.HasPrefix(verifyURI, "http://") || strings.HasPrefix(verifyURI, "https://") {
		return verifyURI
	}
	return strings.TrimRight(server, "/") + "/" + strings.TrimLeft(verifyURI, "/")
}

// bg is the app context, or a plain one before startup (tests).
func (a *App) bg() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// PairInit starts a device-code pairing with serverURL, opens the verification URL in
// the browser, and returns the user code + absolute URL for the UI to display. If the
// server's certificate is not trusted by the system, nothing is sent to it: the
// certificate is returned for the user to decide on (see TrustAndPair).
func (a *App) PairInit(serverURL string) (PairInfo, error) {
	a.pinMu.Lock()
	a.pendingPin, a.pendingPinURL, a.pairPin, a.pairPinURL = "", "", "", ""
	a.pinMu.Unlock()
	info, err := a.startPairing(serverURL, "")
	if err == nil || protocol.CheckServerURL(serverURL) != nil {
		return info, err
	}
	cert, cerr := protocol.FetchCertificate(a.bg(), serverURL)
	if cerr != nil || cert.Trusted {
		return PairInfo{}, err
	}
	a.pinMu.Lock()
	a.pendingPin, a.pendingPinURL = cert.Fingerprint, serverURL
	a.pinMu.Unlock()
	return PairInfo{NeedsTrust: true, Certificate: &CertView{
		Host:        cert.Host,
		Fingerprint: cert.Fingerprint,
		Subject:     cert.Subject,
		Issuer:      cert.Issuer,
		NotAfter:    cert.NotAfter.UTC().Format(time.RFC3339),
		SelfSigned:  cert.SelfSigned,
	}}, nil
}

// TrustAndPair starts pairing again, pinned to the certificate the last PairInit showed.
// It takes no arguments on purpose: what gets pinned is only ever the certificate this
// backend fetched itself.
func (a *App) TrustAndPair() (PairInfo, error) {
	a.pinMu.Lock()
	pin, serverURL := a.pendingPin, a.pendingPinURL
	a.pinMu.Unlock()
	if pin == "" {
		return PairInfo{}, errors.New("no server certificate to trust: start pairing first")
	}
	info, err := a.startPairing(serverURL, pin)
	if err != nil {
		return PairInfo{}, err
	}
	a.pinMu.Lock()
	a.pendingPin, a.pendingPinURL = "", ""
	a.pairPin, a.pairPinURL = pin, serverURL
	a.pinMu.Unlock()
	return info, nil
}

func (a *App) startPairing(serverURL, pin string) (PairInfo, error) {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "desktop"
	}
	p, err := protocol.PairInitPinned(a.bg(), serverURL, hostname, "desktop", pin)
	if err != nil {
		return PairInfo{}, err
	}
	verifyURL, err := checkOpenURL(verificationURL(serverURL, p.VerificationURI), "")
	if err != nil {
		return PairInfo{}, err
	}
	a.urlMu.Lock()
	a.lastVerifyURL = verifyURL
	a.urlMu.Unlock()
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, verifyURL)
	}
	return PairInfo{UserCode: p.UserCode, VerifyURL: verifyURL, DeviceCode: p.DeviceCode, Interval: p.Interval}, nil
}

// PairPoll blocks until the pairing is approved, then saves the config and opens the
// session in-process (no restart). interval is the server-suggested poll seconds. A
// certificate trusted through TrustAndPair for this server is used and saved with the token.
func (a *App) PairPoll(serverURL, deviceCode string, interval int) error {
	iv := time.Duration(interval) * time.Second
	if iv <= 0 {
		iv = 2 * time.Second
	}
	a.pinMu.Lock()
	pin := ""
	if a.pairPinURL == serverURL {
		pin = a.pairPin
	}
	a.pinMu.Unlock()
	token, err := protocol.PairPollPinned(a.bg(), serverURL, deviceCode, iv, pin)
	if err != nil {
		return err
	}
	profile, err := desktop.ProfileDir()
	if err != nil {
		return err
	}
	if err := desktop.SaveConfig(profile, config.Config{ServerURL: serverURL, DeviceToken: token, ServerPin: pin}); err != nil {
		return err
	}
	return a.openProfile(profile)
}

// OpenPairURL reopens the verification URL of the last PairInit in the browser (the
// "open link again" button), mitigating a server login redirect that drops the code.
func (a *App) OpenPairURL() error {
	a.urlMu.Lock()
	u := a.lastVerifyURL
	a.urlMu.Unlock()
	u, err := checkOpenURL(u, "")
	if err != nil {
		return err
	}
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, u)
	}
	return nil
}

// OpenDAVURL opens the profile download link of the last successful PrepareDAV or
// PrepareDAVAutomatic. It must point at the paired server: the OS hands any other scheme
// (smb:, vnc:, …) to whatever handler is registered for it.
func (a *App) OpenDAVURL() error {
	a.urlMu.Lock()
	u := a.lastDAVURL
	a.urlMu.Unlock()
	server, err := url.Parse(a.ServerURL())
	if err != nil || server.Hostname() == "" {
		return fmt.Errorf("not paired")
	}
	u, err = checkOpenURL(u, server.Hostname())
	if err != nil {
		return err
	}
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, u)
	}
	return nil
}

// checkOpenURL accepts only absolute http(s) URLs without credentials, and — when
// wantHost is set — only on that host (compared case-insensitively). Anything else is
// refused before it reaches the OS URL handler.
func checkOpenURL(raw, wantHost string) (string, error) {
	u, err := url.Parse(raw)
	if raw == "" || err != nil {
		return "", fmt.Errorf("no link to open")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", fmt.Errorf("refusing to open %q: only http(s) links can be opened", raw)
	}
	if wantHost != "" && !strings.EqualFold(u.Hostname(), wantHost) {
		return "", fmt.Errorf("refusing to open %q: not on the paired server", raw)
	}
	return u.String(), nil
}

// Ready reports whether a paired profile was opened.
func (a *App) Ready() bool {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	_, _, _, ok := a.account()
	return ok
}

// List returns the children of relPath ("" = root) as frontend nodes.
func (a *App) List(relPath string) ([]Node, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return []Node{}, nil
	}
	entries, err := ctrl.List(relPath)
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
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	_, err := ctrl.Refresh(a.ctx)
	return err
}

// OpenFile downloads (if needed) and opens a file in its default application.
func (a *App) OpenFile(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	p, err := ctrl.Open(a.ctx, nodeID)
	if err != nil {
		return err
	}
	openLocal(p)
	return nil
}

// NewFolder creates a folder named name under parentID ("" = root).
func (a *App) NewFolder(parentID, name string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.CreateFolder(a.ctx, parentID, name)
}

// Rename renames nodeID to newName.
func (a *App) Rename(nodeID, newName string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.Rename(a.ctx, nodeID, newName)
}

// Move reparents nodeID under newParentID ("" = root).
func (a *App) Move(nodeID, newParentID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.Move(a.ctx, nodeID, newParentID)
}

// Delete removes nodeID on the server.
func (a *App) Delete(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.Delete(a.ctx, nodeID)
}

// Pin downloads and keeps nodeID locally.
func (a *App) Pin(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	_, err := ctrl.Pin(a.ctx, nodeID)
	return err
}

// Unpin demotes a pinned node back to cached.
func (a *App) Unpin(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.Unpin(nodeID)
}

// RemoveLocal deletes the local cached copy of nodeID.
func (a *App) RemoveLocal(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.RemoveLocal(nodeID)
}

// RevealFile ensures nodeID is cached locally, then opens its containing folder in
// the OS file manager. This is the "Download" action: materialise a copy on disk.
func (a *App) RevealFile(nodeID string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	p, err := ctrl.Open(a.ctx, nodeID)
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
	paths, err := wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Upload files"})
	a.allowPaths(paths...)
	return paths, err
}

// PickFolder opens a native folder dialog and returns the chosen absolute path.
func (a *App) PickFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Upload folder"})
	if dir != "" {
		a.allowPaths(dir)
	}
	return dir, err
}

// errNotChosen refuses a local path the user did not pick or drop.
var errNotChosen = errors.New("path not chosen through the file picker")

// cleanLocal is the form paths are registered and looked up in.
func cleanLocal(p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// allowPaths registers paths the user chose in a native dialog or dropped on the window.
func (a *App) allowPaths(paths ...string) {
	a.allowMu.Lock()
	defer a.allowMu.Unlock()
	if a.allowedPaths == nil {
		a.allowedPaths = map[string]struct{}{}
	}
	for _, p := range paths {
		if c, ok := cleanLocal(p); ok {
			a.allowedPaths[c] = struct{}{}
		}
	}
}

// forgetPath drops a registered path once its upload or import has succeeded.
func (a *App) forgetPath(p string) {
	c, ok := cleanLocal(p)
	if !ok {
		return
	}
	a.allowMu.Lock()
	delete(a.allowedPaths, c)
	a.allowMu.Unlock()
}

// checkChosen accepts a path that was registered, or lies under a registered folder.
// Below a chosen folder the path must still be there once symlinks are resolved: a link
// inside it (link-to-home/.ssh/id_rsa) must not reach files the user never chose.
func (a *App) checkChosen(p string) error {
	c, ok := cleanLocal(p)
	if !ok {
		return errNotChosen
	}
	a.allowMu.Lock()
	root, found := "", false
	for dir := c; ; {
		if _, ok := a.allowedPaths[dir]; ok {
			root, found = dir, true
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	a.allowMu.Unlock()
	if !found {
		return errNotChosen
	}
	if root == c {
		return nil // exactly what the dialog returned or the drop delivered
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return errNotChosen
	}
	realPath, err := filepath.EvalSymlinks(c)
	if err != nil {
		return errNotChosen
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errNotChosen
	}
	return nil
}

// UploadPaths uploads each path into the current folder: files go into parentID, while
// directories are recreated under parentRelPath (their subtree mirrored on the server).
// Up to 3 files upload concurrently; progress is reported via upload:* events. Only
// paths the user picked or dropped are read. Uploads already running outlive an Unpair
// on purpose: their token is revoked, and they never write into the content cache.
func (a *App) UploadPaths(parentID, parentRelPath string, paths []string) {
	a.accountMu.RLock()
	_, _, up, ok := a.account()
	a.accountMu.RUnlock()
	if !ok || up == nil {
		return
	}
	for _, p := range paths {
		p := p
		if err := a.checkChosen(p); err != nil {
			a.emitUpload("upload:error", uploadEvent{ID: a.uploadSeq.Add(1), Name: filepath.Base(p), Error: err.Error()})
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			a.emitUpload("upload:error", uploadEvent{ID: a.uploadSeq.Add(1), Name: filepath.Base(p), Error: err.Error()})
			continue
		}
		if fi.IsDir() {
			go a.uploadFolder(up, parentRelPath, p)
		} else {
			go a.uploadFile(up, parentID, p)
		}
	}
}

// uploadFile uploads one local file into parentID with progress events. It blocks on
// the upload semaphore (max 3 concurrent), so run it in a goroutine.
func (a *App) uploadFile(up *protocol.Client, parentID, p string) {
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
	err = NewUploader(up).Upload(a.ctx, parentID, name, f, fi.Size(), fi.ModTime(), func(sent, total int64) {
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
	a.forgetPath(p)
	a.emitUpload("upload:done", uploadEvent{ID: id, Name: name, Sent: fi.Size(), Total: fi.Size()})
}

// uploadFolder mirrors a local folder under parentRelPath: it walks the tree, ensures
// each server subfolder exists (EnsureDir is idempotent and creates intermediates),
// and uploads every file into its folder. Folder uploads share the 3-file cap.
func (a *App) uploadFolder(up *protocol.Client, parentRelPath, folderPath string) {
	root := filepath.Base(folderPath)
	dirID := map[string]string{} // server relPath -> node id (memoised)

	ensure := func(relDir string) (string, error) {
		full := path.Join(parentRelPath, relDir)
		if id, ok := dirID[full]; ok {
			return id, nil
		}
		n, err := up.EnsureDir(a.ctx, full)
		if err != nil {
			return "", err
		}
		dirID[full] = n.NodeID
		return n.NodeID, nil
	}

	_ = filepath.WalkDir(folderPath, func(p string, d fs.DirEntry, werr error) error {
		// A symlink inside the chosen folder may point anywhere; only real files go up.
		if werr != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
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
		go a.uploadFile(up, pid, p)
		return nil
	})
	a.forgetPath(folderPath)
}

func (a *App) emitUpload(event string, e uploadEvent) {
	if a.uploadEvents != nil {
		a.uploadEvents(event, e)
		return
	}
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
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return []VaultInfo{}
	}
	refs, err := ctrl.ListVaults()
	if err != nil {
		return []VaultInfo{}
	}
	out := make([]VaultInfo, 0, len(refs))
	for _, r := range refs {
		out = append(out, VaultInfo{Name: r.Name, RelPath: r.RelPath, Open: ctrl.IsVaultOpen(r.RelPath)})
	}
	return out
}

// CreateVault creates an encrypted vault named name (at the vault root) protected by
// password, uploads it, and returns the recovery phrase to show the user.
func (a *App) CreateVault(name, password string) (string, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return "", nil
	}
	return ctrl.CreateVault(a.ctx, "", name, password)
}

// CloseAllVaults closes every open vault (re-encrypt + upload). Called when leaving the
// vaults view so plaintext does not linger and changes are saved.
func (a *App) CloseAllVaults() error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.CloseAllVaults(a.ctx)
}

// OpenVault decrypts the vault and opens its plaintext folder in the OS file manager.
func (a *App) OpenVault(relPath, password string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	plainDir, err := ctrl.OpenVault(a.ctx, relPath, password)
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// OpenVaultWithRecovery opens the vault using its recovery phrase (when the password is
// lost) and reveals the plaintext folder.
func (a *App) OpenVaultWithRecovery(relPath, phrase string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	plainDir, err := ctrl.OpenVaultWithRecovery(a.ctx, relPath, phrase)
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// OpenVaultFolder reveals an already-open vault's plaintext folder.
func (a *App) OpenVaultFolder(relPath string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	plainDir, err := ctrl.OpenVault(a.ctx, relPath, "")
	if err != nil {
		return err
	}
	openLocal(plainDir)
	return nil
}

// CloseVault re-encrypts the open vault and uploads it back to the server.
func (a *App) CloseVault(relPath string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	return ctrl.CloseVault(a.ctx, relPath)
}

// AddFilesToVault copies the given local files into the open vault's plaintext folder.
// The vault stays open; encryption + upload happen on Close. Only paths the user picked
// or dropped are read.
func (a *App) AddFilesToVault(relPath string, paths []string) error {
	for _, p := range paths {
		if err := a.checkChosen(p); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
	}
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	ctrl, _, _, ok := a.account()
	if !ok {
		return nil
	}
	plainDir, err := ctrl.OpenVault(a.ctx, relPath, "")
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := copyFileInto(plainDir, p); err != nil {
			return err
		}
	}
	// Only once the whole batch is in: a retry after a partial failure resends all of it.
	for _, p := range paths {
		a.forgetPath(p)
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
	a.accountMu.Lock()
	if ctrl, _, _, ok := a.account(); ok {
		if err := closeOpenVaults(a.ctx, ctrl); err != nil {
			a.accountMu.Unlock()
			notifyQuitBlocked(a, err)
			return
		}
	}
	// Do not stop background sync until vaults are safely closed. Hold accountMu
	// through exit so another UI request cannot open a new vault in between.
	defer a.accountMu.Unlock()
	a.shutdown(a.ctx)
	exitApplication()
}

var notifyQuitBlocked = func(a *App, err error) {
	a.ShowWindow()
	wruntime.EventsEmit(a.ctx, "quit:blocked", err.Error())
}

var exitApplication = func() {
	systray.Quit()
	os.Exit(0)
}

// riskyExtensions are types the OS runs, or that point somewhere else, when "opened".
// The name comes from the server, which can rename "report.pdf" to "report.pdf.js", so
// such files are revealed in the file manager instead of opened.
var riskyExtensions = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".pif": true,
	".msi": true, ".js": true, ".jse": true, ".vbs": true, ".vbe": true, ".wsf": true,
	".wsh": true, ".hta": true, ".lnk": true, ".ps1": true, ".reg": true, ".jar": true,
	".app": true, ".command": true, ".sh": true, ".terminal": true, ".webloc": true,
	".inetloc": true, ".fileloc": true, ".url": true,
	".desktop": true, ".cpl": true, ".msc": true, ".scf": true, ".appref-ms": true,
	".settingcontent-ms": true, ".chm": true, ".iso": true, ".img": true, ".vhd": true,
	".vhdx": true, ".pkg": true, ".dmg": true, ".scpt": true, ".workflow": true,
}

// isRisky reports whether opening p would run it. Windows ignores trailing dots and
// spaces ("x.js." runs as x.js), so they are stripped before the extension is taken.
func isRisky(p string) bool {
	name := strings.TrimRight(filepath.Base(p), ". ")
	return riskyExtensions[strings.ToLower(filepath.Ext(name))]
}

// openLocal opens a downloaded file or a folder in its default application. A risky
// file is revealed in the file manager instead, so the user sees what it is first.
func openLocal(p string) {
	if isRisky(p) {
		revealLocal(p)
		return
	}
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

// revealLocal shows p in the file manager without opening it.
func revealLocal(p string) {
	name, args := revealCommand(runtime.GOOS, p)
	_ = exec.Command(name, args...).Start()
}

// revealCommand is the command revealLocal runs. On Windows the folder goes through the
// shell file handler, like openLocal: explorer.exe splits its command line on commas
// ("/select,", "/root,"), and Go quotes an argument only when it holds spaces, so a
// server-named folder could smuggle explorer switches in.
func revealCommand(goos, p string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{"-R", p}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", filepath.Dir(p)}
	default:
		return "xdg-open", []string{filepath.Dir(p)}
	}
}
