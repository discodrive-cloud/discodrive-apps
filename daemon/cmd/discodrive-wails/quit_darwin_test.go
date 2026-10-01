//go:build darwin

package main

import (
	"context"
	"discodrive.org/daemon/internal/desktop"
	"errors"
	"testing"
)

func TestNativeQuitUsesSafeShutdown(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "save-failed"}[blocked], func(t *testing.T) {
			var saveErr error
			if blocked {
				saveErr = errors.New("offline")
			}
			calls := stubVaultOps(t, saveErr, false, nil, nil)
			oldExit, oldNotify := exitApplication, notifyQuitBlocked
			t.Cleanup(func() { exitApplication, notifyQuitBlocked = oldExit, oldNotify })
			exited, notified := false, false
			exitApplication = func() { exited = true }
			notifyQuitBlocked = func(_ *App, err error) { notified = errors.Is(err, saveErr) }
			app := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
			opts := appOptions(app)
			if !opts.HideWindowOnClose {
				t.Fatal("window close and native Quit still share the same hide handler")
			}
			if !opts.OnBeforeClose(app.ctx) {
				t.Fatal("native termination bypassed controlled shutdown")
			}
			if exited == blocked || notified != blocked || len(*calls) != 1 || (*calls)[0] != "close" {
				t.Fatalf("exit=%v notify=%v calls=%v", exited, notified, *calls)
			}
			if blocked && (!app.ready || app.ctrl == nil) {
				t.Fatal("failed save destroyed session")
			}
		})
	}
}
