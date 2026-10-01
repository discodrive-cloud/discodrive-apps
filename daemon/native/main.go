// Package main builds the sync engine into the macOS application as a C archive.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"discodrive.org/daemon/internal/diagnostics"
	"encoding/json"
	"errors"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/syncer"
)

var diagnosticLog diagnostics.Writer

func init() {
	if runtime.GOOS == "ios" {
		log.SetOutput(&diagnosticLog)
	}
}

//export DDFullSyncSetLogPath
func DDFullSyncSetLogPath(path *C.char) *C.char {
	if err := diagnosticLog.Configure(C.GoString(path)); err != nil {
		return C.CString(err.Error())
	}
	if C.GoString(path) != "" {
		log.Print("diagnostics enabled")
	}
	return nil
}

// configuration is the JSON the app passes to DDFullSyncStart. Pin is the fingerprint of the
// server certificate the user trusted at pairing, "" for a server the system trusts.
type configuration struct{ Server, Token, Root, Database, Pin string }

var native struct {
	sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	status syncer.Status
	engine *engine.Engine
	runner *syncer.Syncer
}

//export DDFullSyncStart
func DDFullSyncStart(raw *C.char) *C.char {
	var cfg configuration
	if err := json.Unmarshal([]byte(C.GoString(raw)), &cfg); err != nil {
		return C.CString(err.Error())
	}
	if err := start(cfg); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

func start(cfg configuration) error {
	native.Lock()
	defer native.Unlock()
	if native.cancel != nil {
		return errors.New("already running")
	}

	if cfg.Server == "" || cfg.Token == "" || cfg.Root == "" || cfg.Database == "" {
		return errors.New("incomplete configuration")
	}
	rootInfo, err := os.Stat(cfg.Root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		return errors.New("sync root is not a directory")
	}
	idx, err := index.Open(cfg.Database)
	if err != nil {
		return err
	}
	ready, err := idx.MirrorReady()
	if err != nil {
		idx.Close()
		return err
	}
	if !ready {
		if err := idx.SetMirrorReady(false); err != nil {
			idx.Close()
			return err
		}
	}
	client := protocol.NewPinned(strings.TrimRight(cfg.Server, "/"), cfg.Token, cfg.Pin)
	eng := engine.NewPrepared(client, idx, cfg.Root)
	eng.ObserveChanges(func(c engine.Change, phase string, elapsed time.Duration, err error) {
		log.Printf("pull %s seq=%d path=%q bytes=%d elapsed=%s error=%v", phase, c.Seq, c.RelPath, c.Size, elapsed.Round(time.Millisecond), err)
	})
	runner := syncer.New(client, eng, cfg.Root, "")
	runner.BeforePass(func() error { return checkRoot(cfg.Root, rootInfo) })
	runner.ObserveStatus(func(st syncer.Status) {
		native.Lock()
		native.status = st
		native.Unlock()
		log.Printf("sync state=%s error=%s", st.State, st.LastError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	native.runner = runner
	native.engine = eng
	native.cancel = cancel
	native.done = make(chan struct{})
	native.status = syncer.Status{State: syncer.StateSyncing}
	done := native.done
	go func() { defer close(done); defer idx.Close(); _ = runner.Run(ctx) }()
	return nil
}

//export DDFullSyncStop
func DDFullSyncStop() { stop() }

func stop() {
	native.Lock()
	cancel, done := native.cancel, native.done
	native.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	native.Lock()
	native.runner = nil
	native.engine = nil
	native.status = syncer.Status{}
	native.cancel = nil
	native.done = nil
	native.Unlock()
}

//export DDFullSyncConfirmDeletion
func DDFullSyncConfirmDeletion() {
	native.Lock()
	defer native.Unlock()
	if native.runner != nil && native.status.ErrorKind == "bulk_delete" {
		native.runner.RequestBulkDelete()
	}
}

//export DDFullSyncStatus
func DDFullSyncStatus() *C.char {
	native.Lock()
	defer native.Unlock()
	st := native.status
	if native.engine != nil {
		st.Activity = native.engine.Activity()
	}
	b, _ := json.Marshal(st)
	return C.CString(string(b))
}

//export DDFullSyncFree
func DDFullSyncFree(p *C.char) { C.free(unsafe.Pointer(p)) }
func main()                    {}

func checkRoot(path string, expected os.FileInfo) error {
	actual, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, actual) {
		return errors.New("sync folder was replaced")
	}
	return nil
}
