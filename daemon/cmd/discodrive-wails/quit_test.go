package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"discodrive.org/daemon/internal/desktop"
)

func TestQuitRetainsSessionWhenVaultSaveFails(t *testing.T) {
	want := errors.New("offline")
	stubVaultOps(t, want, false, nil, nil)
	oldNotify, oldExit := notifyQuitBlocked, exitApplication
	t.Cleanup(func() { notifyQuitBlocked, exitApplication = oldNotify, oldExit })
	var reported error
	notifyQuitBlocked = func(_ *App, err error) { reported = err }
	exitApplication = func() { t.Fatal("quit after a failed vault save") }
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	a.QuitApp()
	if !errors.Is(reported, want) || !a.ready || a.ctrl == nil {
		t.Fatalf("reported=%v ready=%v controller=%v", reported, a.ready, a.ctrl)
	}
	// A failed quit must release accountMu so the user can retry saving.
	if !a.accountMu.TryLock() {
		t.Fatal("account remains locked")
	}
	a.accountMu.Unlock()
}

func TestQuitExitsOnlyAfterVaultSave(t *testing.T) {
	calls := stubVaultOps(t, nil, false, nil, nil)
	oldExit := exitApplication
	t.Cleanup(func() { exitApplication = oldExit })
	exited := false
	exitApplication = func() {
		if len(*calls) != 1 || (*calls)[0] != "close" {
			t.Fatalf("exit before close: %v", *calls)
		}
		exited = true
	}
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	a.QuitApp()
	if !exited {
		t.Fatal("successful quit did not exit")
	}
}

func TestTrayRemainsUsableAfterBlockedQuit(t *testing.T) {
	stubVaultOps(t, errors.New("offline"), false, nil, nil)
	oldNotify, oldExit := notifyQuitBlocked, exitApplication
	t.Cleanup(func() { notifyQuitBlocked, exitApplication = oldNotify, oldExit })
	blocked := make(chan struct{}, 2)
	notifyQuitBlocked = func(_ *App, _ error) { blocked <- struct{}{} }
	exitApplication = func() { panic("exit after failed save") }
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	open, quit := make(chan struct{}), make(chan struct{})
	shown, done := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(done)
		runTrayActions(open, quit, func() { shown <- struct{}{} }, a.QuitApp)
	}()
	defer func() { close(open); <-done }()
	send := func(ch chan struct{}) {
		t.Helper()
		select {
		case ch <- struct{}{}:
		case <-done:
			t.Fatal("tray event loop stopped after blocked quit")
		case <-time.After(time.Second):
			t.Fatal("tray event loop stuck")
		}
	}
	send(quit)
	<-blocked
	send(open)
	<-shown
	send(quit)
	<-blocked
}
