package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"

	"discodrive.org/daemon/internal/davcredentials"
	"discodrive.org/daemon/internal/protocol"
)

// Serialize profile preparation/revocation while accountMu keeps the pairing alive.
var davSetupMu sync.Mutex

type DAVSetup struct {
	Supported  bool                    `json:"supported"`
	Access     protocol.DAVAccess      `json:"access"`
	Credential *protocol.DAVCredential `json:"credential,omitempty"`
	URL        string                  `json:"url,omitempty"`
}

func (a *App) davService() (string, error) {
	account, err := a.up.DAVAccount(a.ctx)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(a.ServerURL() + "\n" + account.ID))
	return fmt.Sprintf("org.discodrive.wails.dav.%x", sum), nil
}
func (a *App) GetDAVSetup() (DAVSetup, error) {
	out := DAVSetup{Supported: runtime.GOOS == "darwin"}
	if !out.Supported {
		return out, nil
	}
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	davSetupMu.Lock()
	defer davSetupMu.Unlock()
	if !a.ready || a.up == nil {
		return out, fmt.Errorf("not paired")
	}
	var err error
	out.Access, err = a.up.DAVAccess(a.ctx)
	if err != nil {
		return out, err
	}
	service, err := a.davService()
	if err != nil {
		return out, err
	}
	stored, err := davcredentials.Load(service)
	if err != nil {
		return out, err
	}
	if stored != "" {
		out.Credential = &protocol.DAVCredential{}
		err = json.Unmarshal([]byte(stored), out.Credential)
	}
	return out, err
}
func (a *App) PrepareDAV(calendars, contacts bool) (DAVSetup, error) {
	return a.rememberDAVURL(a.prepareDAV(calendars, contacts, false))
}
func (a *App) PrepareDAVAutomatic(calendars, contacts bool) (DAVSetup, error) {
	return a.rememberDAVURL(a.prepareDAV(calendars, contacts, true))
}

// rememberDAVURL keeps the profile link of a successful preparation for OpenDAVURL, which
// opens only that link, never one passed in from the web view.
func (a *App) rememberDAVURL(out DAVSetup, err error) (DAVSetup, error) {
	a.urlMu.Lock()
	defer a.urlMu.Unlock()
	a.lastDAVURL = ""
	if err == nil {
		a.lastDAVURL = out.URL
	}
	return out, err
}
func (a *App) prepareDAV(calendars, contacts, automatic bool) (DAVSetup, error) {
	out := DAVSetup{Supported: runtime.GOOS == "darwin"}
	if !out.Supported {
		return out, fmt.Errorf("unsupported platform")
	}
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	davSetupMu.Lock()
	defer davSetupMu.Unlock()
	if !a.ready || a.up == nil {
		return out, fmt.Errorf("not paired")
	}
	service, err := a.davService()
	if err != nil {
		return out, err
	}
	if automatic {
		enabled, err := a.up.AppleEnrollmentAvailable(a.ctx)
		if err != nil {
			return out, err
		}
		if !enabled {
			return out, fmt.Errorf("encrypted setup unavailable")
		}
	} else {
		out.URL, err = a.up.AppleProfile(a.ctx, service, calendars, contacts)
		if err != nil {
			return out, err
		}
	}
	stored, err := davcredentials.Load(service)
	if err != nil {
		return out, err
	}
	out.Credential = &protocol.DAVCredential{}
	if stored != "" {
		err = json.Unmarshal([]byte(stored), out.Credential)
		if err == nil && automatic {
			out.URL, err = a.up.AppleEnrollment(a.ctx, service, calendars, contacts, *out.Credential)
		}
		return out, err
	}
	created, err := a.up.CreateDAVPassword(a.ctx, "DiscoDrive · macOS calendars and contacts")
	if err != nil {
		return out, err
	}
	data, err := json.Marshal(created)
	if err == nil {
		err = davcredentials.Save(service, string(data))
	}
	if err != nil {
		_ = a.up.RevokeDAVPassword(a.ctx, created.ID)
		return out, err
	}
	out.Credential = &created
	if automatic {
		out.URL, err = a.up.AppleEnrollment(a.ctx, service, calendars, contacts, created)
	}
	return out, err
}
func (a *App) RevokeDAV() error {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	davSetupMu.Lock()
	defer davSetupMu.Unlock()
	if !a.ready || a.up == nil {
		return fmt.Errorf("not paired")
	}
	service, err := a.davService()
	if err != nil {
		return err
	}
	stored, err := davcredentials.Load(service)
	if err != nil {
		return err
	}
	if stored == "" {
		return nil
	}
	var credential protocol.DAVCredential
	if err = json.Unmarshal([]byte(stored), &credential); err != nil {
		return err
	}
	if err = a.up.RevokeDAVPassword(a.ctx, credential.ID); err != nil {
		return err
	}
	a.urlMu.Lock()
	a.lastDAVURL = ""
	a.urlMu.Unlock()
	return davcredentials.Delete(service)
}
