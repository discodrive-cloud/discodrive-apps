// Package fullsync owns a desktop application's persistent, fully materialized mirror.
// Its index and settings are independent from the on-demand browser and CLI daemon.
package fullsync

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/syncer"
)

type Preferences struct {
	Folder  string `json:"folder"`
	Enabled bool   `json:"enabled"`
}
type Status struct {
	Activity  engine.Activity `json:"activity"`
	LastError string          `json:"last_error,omitempty"`
	Preferences
	State     string `json:"state"`
	ErrorKind string `json:"errorKind,omitempty"`
	Backup    string `json:"backup,omitempty"`
}

type Manager struct {
	mu             sync.Mutex // serializes lifecycle and preference changes, including logout
	statusMu       sync.Mutex
	profile        string
	prefs          Preferences
	account        config.Config
	closed         bool
	cancel         context.CancelFunc
	done           chan struct{}
	runner         *syncer.Syncer
	status         Status
	activityEngine *engine.Engine // protected by statusMu
}

func New(profile string) *Manager {
	m := &Manager{profile: profile}
	b, err := os.ReadFile(filepath.Join(profile, "full-sync.json"))
	if err == nil {
		if json.Unmarshal(b, &m.prefs) != nil {
			m.prefs = Preferences{}
		}
	}
	if m.prefs.Folder == "" {
		m.prefs.Enabled = false
	}
	m.status = Status{Preferences: m.prefs, State: "stopped"}
	return m
}
func (m *Manager) Status() Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	st := m.status
	if m.activityEngine != nil {
		st.Activity = m.activityEngine.Activity()
	}
	return st
}
func (m *Manager) state(state, kind string) {
	m.statusMu.Lock()
	m.status.State = state
	m.status.ErrorKind = kind
	if state == "stopped" {
		m.activityEngine = nil
		m.status.Activity = engine.Activity{}
		m.status.LastError = ""
	}
	m.statusMu.Unlock()
}
func (m *Manager) save(p Preferences) error {
	if err := writeJSON(filepath.Join(m.profile, "full-sync.json"), p); err != nil {
		return err
	}
	m.prefs = p
	m.statusMu.Lock()
	m.status.Preferences = p
	m.statusMu.Unlock()
	return nil
}
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".full-sync-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Attach resumes only the explicitly enabled mirror. It is also used after pairing.
func (m *Manager) Attach(ctx context.Context, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("sync stopped")
	}
	m.stop()
	m.account = cfg
	if m.prefs.Enabled {
		return m.start(ctx)
	}
	return nil
}
func (m *Manager) Choose(folder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.prefs.Enabled {
		return errors.New("disable sync before changing folder")
	}
	root, err := validateRoot(folder, m.profile)
	if err != nil {
		return err
	}
	if err = m.save(Preferences{Folder: root}); err != nil {
		return err
	}
	m.statusMu.Lock()
	m.status.Backup = ""
	m.statusMu.Unlock()
	m.state("stopped", "")
	return nil
}
func (m *Manager) Enable(ctx context.Context, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("sync stopped")
	}
	if enabled && (m.prefs.Folder == "" || m.account.DeviceToken == "") {
		return errors.New("choose folder and pair first")
	}
	p := m.prefs
	p.Enabled = enabled
	if err := m.save(p); err != nil {
		return err
	}
	m.stop()
	if enabled {
		return m.start(ctx)
	}
	return nil
}

// Detach disables the old account before its credentials can be replaced.
func (m *Manager) Detach() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
	m.account = config.Config{}
	p := m.prefs
	p.Enabled = false
	return m.save(p)
}

// Close retains the preference so the next application launch can resume it.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.stop()
}
func (m *Manager) stop() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel = nil
		m.runner = nil
	}
	m.state("stopped", "")
}
func (m *Manager) ConfirmDeletion() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runner != nil && m.Status().ErrorKind == "bulk_delete" {
		m.runner.RequestBulkDelete()
	}
}
func (m *Manager) start(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			m.state("error", "")
			log.Printf("folder sync: %v", err)
		}
	}()
	if m.account.ServerURL == "" || m.account.DeviceToken == "" {
		return errors.New("pair before enabling synchronization")
	}
	root, err := validateRoot(m.prefs.Folder, m.profile)
	if err != nil {
		return err
	}
	id, err := identity(root)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(m.account.ServerURL+"\n"+m.account.DeviceToken+"\n"+root+"\n"+id)))
	state := filepath.Join(m.profile, "full-sync", key)
	if err = os.MkdirAll(state, 0700); err != nil {
		return err
	}
	m.state("preparing", "")
	backup, err := prepare(root, state)
	m.statusMu.Lock()
	m.status.Backup = backup
	m.statusMu.Unlock()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	idx, err := index.Open(filepath.Join(state, "state.db"))
	if err != nil {
		return err
	}
	ready, err := idx.MirrorReady()
	if err == nil && !ready {
		err = idx.SetMirrorReady(false)
	}
	if err != nil {
		idx.Close()
		return err
	}
	client := protocol.NewStrict(m.account.ServerURL, m.account.DeviceToken)
	eng := engine.NewPrepared(client, idx, root)
	m.statusMu.Lock()
	m.activityEngine = eng
	m.status.LastError = ""
	m.statusMu.Unlock()
	runner := syncer.New(client, eng, root, "")
	runner.BeforePass(func() error { return checkIdentity(root, id) })
	runner.ObserveStatus(func(s syncer.Status) {
		m.statusMu.Lock()
		m.status.State = string(s.State)
		m.status.ErrorKind = s.ErrorKind
		m.status.LastError = s.LastError
		m.statusMu.Unlock()
	})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done, m.runner = cancel, done, runner
	m.state("syncing", "")
	go func() { defer close(done); defer idx.Close(); _ = runner.Run(runCtx) }()
	return nil
}

// A sibling backup stays on the same volume. Move entries, not the selected root,
// so restarting against the same file identity can retain the mirror's history.
func prepare(root, state string) (string, error) {
	marker := filepath.Join(state, "prepared.json")
	var previous struct {
		Backup string `json:"backup"`
	}
	data, err := os.ReadFile(marker)
	if err == nil {
		if err = json.Unmarshal(data, &previous); err != nil {
			return "", err
		}
		if _, err = os.Stat(filepath.Join(state, "state.db")); err == nil {
			return previous.Backup, nil
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", writeJSON(marker, previous)
	}
	backup, err := os.MkdirTemp(filepath.Dir(root), filepath.Base(root)+".old-"+time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return "", err
	}
	previous.Backup = backup
	// Save the location before moving files; on error the incomplete backup remains visible.
	if err = writeJSON(filepath.Join(state, "last-backup.json"), previous); err != nil {
		return backup, err
	}
	for _, entry := range entries {
		if err = os.Rename(filepath.Join(root, entry.Name()), filepath.Join(backup, entry.Name())); err != nil {
			return backup, err
		}
	}
	return backup, writeJSON(marker, previous)
}
func checkIdentity(root, expected string) error {
	actual, err := identity(root)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("sync folder was replaced; select it again")
	}
	return nil
}

func validateRoot(root, profile string) (string, error) {
	if root == "" {
		return "", errors.New("choose a folder")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a folder")
	}
	if err = os.MkdirAll(profile, 0700); err != nil {
		return "", err
	}
	profile, err = filepath.EvalSymlinks(profile)
	if err != nil {
		return "", err
	}
	profile, err = filepath.Abs(profile)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	home, _ = filepath.EvalSymlinks(home)
	canonical := func(p string) string {
		p = filepath.Clean(p)
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	r, p, h := canonical(root), canonical(profile), canonical(home)
	temp, _ := filepath.EvalSymlinks(os.TempDir())
	temp = canonical(temp)
	sep := string(filepath.Separator)
	if r == temp || strings.HasPrefix(temp, r+sep) || r == filepath.Dir(r) || r == h || r == p || strings.HasPrefix(r, p+sep) || strings.HasPrefix(p, r+sep) {
		return "", errors.New("choose a dedicated folder outside application data")
	}
	// Plaintext vault sessions and cloud-provider roots must never become upload mirrors.
	for _, part := range strings.Split(r, sep) {
		if strings.HasPrefix(part, "ddvault-") || strings.HasPrefix(part, "ddvopen-") || part == "CloudStorage" || part == "cloudstorage" {
			return "", errors.New("managed folder cannot be synchronized")
		}
	}
	current, err := identity(root)
	if err != nil {
		return "", err
	}
	parent, err := identity(filepath.Dir(root))
	if err != nil {
		return "", err
	}
	if strings.Split(current, ":")[0] != strings.Split(parent, ":")[0] {
		return "", errors.New("choose a folder inside the volume")
	}
	return root, nil
}
