package main

import (
	"context"
	"discodrive.org/daemon/internal/fullsync"
	"errors"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetFullSync() fullsync.Status {
	if a.mirror == nil {
		return fullsync.Status{State: "stopped"}
	}
	return a.mirror.Status()
}
func (a *App) ChooseSyncFolder(title string) (fullsync.Status, error) {
	if a.mirror == nil || a.ctx == nil {
		return a.GetFullSync(), errors.New("not ready")
	}
	folder, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: title, DefaultDirectory: a.mirror.Status().Folder, CanCreateDirectories: true})
	if err == nil && folder != "" {
		err = a.mirror.Choose(folder)
	}
	return a.GetFullSync(), err
}
func (a *App) SetFullSync(enabled bool) (fullsync.Status, error) {
	if a.mirror == nil {
		return a.GetFullSync(), errors.New("not ready")
	}
	err := a.mirror.Enable(a.ctx, enabled)
	return a.GetFullSync(), err
}
func (a *App) ConfirmSyncDeletions() {
	if a.mirror != nil {
		a.mirror.ConfirmDeletion()
	}
}
func (a *App) RevealSyncBackup() {
	if a.mirror != nil {
		if p := a.mirror.Status().Backup; p != "" {
			openLocal(p)
		}
	}
}
func (a *App) shutdown(_ context.Context) {
	if a.mirror != nil {
		a.mirror.Close()
	}
}
