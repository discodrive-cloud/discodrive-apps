package main

import (
	"discodrive.org/daemon/internal/protocol"
	"fmt"
	"net/http"
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
	out, err := a.up.Versions(a.ctx, id)
	return out, a.forgetIfGone(id, err)
}
func (a *App) Shares(id string) ([]protocol.Share, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return nil, fmt.Errorf("not paired")
	}
	out, err := a.up.Shares(a.ctx, id)
	return out, a.forgetIfGone(id, err)
}
func (a *App) CreateShare(id, email string, days int) (protocol.ShareResult, error) {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return protocol.ShareResult{}, fmt.Errorf("not paired")
	}
	out, err := a.up.CreateShare(a.ctx, id, strings.TrimSpace(email), days)
	// An unknown recipient is answered with the same 404 as a missing node.
	return out, a.forgetIfConfirmedGone(id, err)
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
	return trashBlocked(a.up.Purge(a.ctx, id))
}
func (a *App) EmptyTrash() error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return trashBlocked(a.up.EmptyTrash(a.ctx))
}
func (a *App) RestoreVersion(id string, version int64) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	// A missing version, or a folder, is answered with the same 404 as a missing node.
	return a.forgetIfConfirmedGone(id, a.up.RestoreVersion(a.ctx, id, version))
}
func (a *App) RevokeShare(id string) error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	return a.up.RevokeShare(a.ctx, id)
}

// forgetIfGone drops a node the server says it no longer has from the index (a delete
// event that never arrived) and reports it as desktop.ErrNodeGone. The caller holds
// accountMu. Trash items and share ids are not index nodes and do not come through here.
func (a *App) forgetIfGone(nodeID string, err error) error {
	if err == nil || a.ctrl == nil {
		return err
	}
	return a.ctrl.ForgetIfGone(nodeID, err)
}

// forgetIfConfirmedGone is forgetIfGone for a request whose 404 can also be about
// something else it names: the node is forgotten only once the server confirms it is gone.
func (a *App) forgetIfConfirmedGone(nodeID string, err error) error {
	if err == nil || a.ctrl == nil {
		return err
	}
	return a.ctrl.ForgetIfConfirmedGone(a.ctx, nodeID, err)
}

// trashBlocked tags the server's 409 on purge/empty-trash — a trashed folder still holds an
// item that is not in the trash, so it (and what it holds) was kept — for the UI to explain
// in the user's language. Whatever else the server removed is gone; the UI relists either way.
func trashBlocked(err error) error {
	if protocol.StatusCode(err) == http.StatusConflict {
		return fmt.Errorf("trash_blocked: %w", err)
	}
	return err
}
