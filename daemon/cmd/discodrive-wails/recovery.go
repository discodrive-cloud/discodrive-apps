package main

import (
	"discodrive.org/daemon/internal/protocol"
	"fmt"
	"strings"
)

// Keep each request attached to the account that initiated it until it completes.
func (a *App) Trash() ([]protocol.TrashItem, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return nil, fmt.Errorf("not paired")
	}
	return a.up.Trash(a.ctx)
}
func (a *App) Versions(id string) ([]protocol.FileVersion, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return nil, fmt.Errorf("not paired")
	}
	return a.up.Versions(a.ctx, id)
}
func (a *App) Shares(id string) ([]protocol.Share, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return nil, fmt.Errorf("not paired")
	}
	return a.up.Shares(a.ctx, id)
}
func (a *App) CreateShare(id, email string, days int) (protocol.ShareResult, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return protocol.ShareResult{}, fmt.Errorf("not paired")
	}
	return a.up.CreateShare(a.ctx, id, strings.TrimSpace(email), days)
}
func (a *App) Undelete(id string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.Undelete(a.ctx, id)
}
func (a *App) Purge(id string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.Purge(a.ctx, id)
}
func (a *App) EmptyTrash() error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.EmptyTrash(a.ctx)
}
func (a *App) RestoreVersion(id string, version int64) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.RestoreVersion(a.ctx, id, version)
}
func (a *App) RevokeShare(id string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.RevokeShare(a.ctx, id)
}
