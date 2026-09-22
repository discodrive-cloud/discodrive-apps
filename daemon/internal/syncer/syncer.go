// Package syncer drives the daemon's bidirectional sync: push→pull on triggers.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/i18n"
	"discodrive.org/daemon/internal/protocol"
)

type Syncer struct {
	client     *protocol.Client
	eng        *engine.Engine
	root       string
	statusPath string
	observer   func(Status)
	beforePass func() error
	confirm    chan struct{}
}

func New(client *protocol.Client, eng *engine.Engine, root string, statusPath string) *Syncer {
	return &Syncer{client: client, eng: eng, root: root, statusPath: statusPath, confirm: make(chan struct{}, 1)}
}

// ObserveStatus installs a host callback before Run starts.
func (s *Syncer) ObserveStatus(f func(Status)) { s.observer = f }

// BeforePass installs a host guard before Run starts (for example, folder identity).
func (s *Syncer) BeforePass(f func() error) { s.beforePass = f }

// RequestBulkDelete is the UI-safe counterpart: the run loop owns the engine.
func (s *Syncer) RequestBulkDelete() {
	select {
	case s.confirm <- struct{}{}:
	default:
	}
}

// ConfirmBulkDelete lets the next pass carry deletions the mass-deletion guard would stop.
// The daemon exposes it as `run -confirm-bulk-delete`, for when the folder really was emptied
// on purpose; without it a lost mirror would take the server's copy down with it.
func (s *Syncer) ConfirmBulkDelete() { s.eng.ConfirmBulkDelete() }

// SyncOnce runs a single pass. First it checks the server's scope epoch: if it differs from
// what we last reconciled to, the user changed their sync scope, so we reconcile (wipe + fresh
// pull + sweep orphans) and skip push this pass — local files were mapped to the old scope and
// must not leak into the new one. Otherwise it's the normal PUSH→PULL (order matters, 3.2c).
func (s *Syncer) SyncOnce(ctx context.Context) error {
	if s.beforePass != nil {
		if err := s.beforePass(); err != nil {
			return err
		}
	}
	epoch, err := s.client.SyncMeta(ctx)
	if err != nil {
		return err
	}
	last, err := s.eng.ScopeEpoch()
	if err != nil {
		return err
	}
	if epoch != last {
		return s.eng.ResetForScope(ctx, epoch)
	}
	if err := s.eng.PushLocal(ctx, s.client); err != nil {
		return err
	}
	return s.eng.PullOnce(ctx)
}

// Run drives sync on triggers (fsnotify+SSE+ticker) with debounce and backoff. Blocks until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	spawn := func(f func()) { workers.Add(1); go func() { defer workers.Done(); f() }() }

	trigger := make(chan struct{}, 1)
	notify := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	spawn(func() { s.watch(ctx, notify) })
	spawn(func() {
		for ctx.Err() == nil {
			if err := s.client.ListenEvents(ctx, notify); err != nil && ctx.Err() == nil {
				if !waitFor(ctx, 2*time.Second) {
					return
				}
			}
		}
	})
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	spawn(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				notify()
			}
		}
	})

	notify()
	backoff := time.Second
	failing := false   // whether any sync errors occurred since the last success
	announced := false // the set-aside folder, if any, is reported once
	debounce := time.NewTimer(time.Hour)
	defer debounce.Stop()
	debounce.Stop()
	pending := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.confirm:
			s.eng.ConfirmBulkDelete()
			notify()
		case <-trigger:
			if !pending {
				pending = true
				debounce.Reset(500 * time.Millisecond)
			}
		case <-debounce.C:
			pending = false
			s.writeStatus(Status{State: StateSyncing})
			if err := s.SyncOnce(ctx); err != nil {
				log.Printf("discodrive: sync failed: %v (retrying in %s)", err, backoff)
				kind := ""
				var bulk *engine.BulkDeleteError
				if errors.As(err, &bulk) {
					kind = "bulk_delete"
				}
				s.writeStatus(Status{State: StateOffline, LastError: err.Error(), ErrorKind: kind})
				failing = true
				if !waitFor(ctx, backoff) {
					return ctx.Err()
				}
				if backoff < 30*time.Second {
					backoff *= 2
				}
				notify()
			} else {
				now := time.Now()
				s.writeStatus(Status{State: StateIdle, LastSync: now})
				if aside := s.eng.SetAside(); aside != "" && !announced {
					log.Print(fmt.Sprintf(i18n.T("run_set_aside"), aside))
					announced = true
				}
				if failing {
					log.Printf("discodrive: connection restored, sync complete")
					failing = false
				}
				backoff = time.Second
			}
		}
	}
}

func (s *Syncer) watch(ctx context.Context, notify func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("discodrive: fsnotify unavailable: %v", err)
		return
	}
	defer w.Close()
	addRecursive(w, s.root)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-w.Events:
			if !ok {
				return
			}
			if strings.HasPrefix(filepath.Base(e.Name), ".kf-tmp-") {
				continue
			}
			if e.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(e.Name); err == nil && fi.IsDir() {
					addRecursive(w, e.Name)
				}
			}
			notify()
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		}
	}
}

func addRecursive(w *fsnotify.Watcher, dir string) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = w.Add(p)
		}
		return nil
	})
}

func waitFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
