// Package syncer drives the daemon's bidirectional sync: push→pull on triggers.
package syncer

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/protocol"
)

type Syncer struct {
	client     *protocol.Client
	eng        *engine.Engine
	root       string
	statusPath string
}

func New(client *protocol.Client, eng *engine.Engine, root string, statusPath string) *Syncer {
	return &Syncer{client: client, eng: eng, root: root, statusPath: statusPath}
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
	trigger := make(chan struct{}, 1)
	notify := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	go s.watch(ctx, notify)
	go func() {
		for ctx.Err() == nil {
			if err := listenEvents(ctx, s.client, notify); err != nil && ctx.Err() == nil {
				time.Sleep(2 * time.Second)
			}
		}
	}()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				notify()
			}
		}
	}()

	notify()
	backoff := time.Second
	failing := false // whether any sync errors occurred since the last success
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	pending := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
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
				s.writeStatus(Status{State: StateOffline, LastError: err.Error()})
				failing = true
				time.Sleep(backoff)
				if backoff < 30*time.Second {
					backoff *= 2
				}
				notify()
			} else {
				now := time.Now()
				s.writeStatus(Status{State: StateIdle, LastSync: now})
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
