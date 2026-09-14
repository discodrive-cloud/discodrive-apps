//go:build tray

package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"discodrive.org/daemon/internal/i18n"
	"discodrive.org/daemon/internal/syncer"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
	"fyne.io/systray"
)

//go:embed icon.png
var trayIcon []byte

func cmdTray(args []string) {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	cfgPath := fs.String("config", mustDefaultCfgPath(), i18n.T("flag_config"))
	detach := fs.Bool("detach", false, i18n.T("flag_detach"))
	foreground := fs.Bool("foreground", false, i18n.T("flag_foreground"))
	_ = fs.Parse(args)
	release, proceed := maybeDaemonize("tray", *cfgPath, *detach, *foreground)
	if !proceed {
		return
	}
	defer release()

	s, cleanup, cfg, err := buildSyncer(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("tray_init_error"), err))
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = s.Run(ctx) }()

	systray.Run(
		func() { onReady(ctx, cancel, *cfgPath, cfg.SyncDir) },
		func() { cancel(); cleanup() },
	)
}

func onReady(ctx context.Context, cancel context.CancelFunc, cfgPath, syncDir string) {
	indicator := newTrayIndicator(applyTrayIcon)
	indicator.set("")
	systray.SetTitle("")
	systray.SetTooltip("DiscoDrive sync")

	mStatus := systray.AddMenuItem(i18n.T("tray_status_starting"), i18n.T("tray_status_tooltip"))
	mStatus.Disable()
	mOpen := systray.AddMenuItem(i18n.T("tray_open_folder"), i18n.T("tray_open_folder_tooltip"))
	systray.AddSeparator()
	mQuit := systray.AddMenuItem(i18n.T("tray_quit"), i18n.T("tray_quit_tooltip"))

	// Update status from status.json every 2 seconds.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		upd := func() {
			st, err := syncer.ReadStatus(statusFilePath(cfgPath))
			label := i18n.T("tray_status_starting")
			if err == nil {
				switch st.State {
				case syncer.StateIdle:
					label = i18n.T("tray_status_synced")
				case syncer.StateSyncing:
					label = i18n.T("tray_status_syncing")
				case syncer.StateOffline:
					label = i18n.T("tray_status_offline")
				}
			}
			mStatus.SetTitle(label)
			if err == nil {
				indicator.set(st.State)
			}
		}
		upd()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				upd()
			}
		}
	}()

	// Handle main menu clicks.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-mOpen.ClickedCh:
				openFolder(syncDir)
			case <-mQuit.ClickedCh:
				cancel()
				systray.Quit()
				return
			}
		}
	}()

	// Vault section.
	mgr, err := vaultmgr.New(syncDir)
	if err != nil {
		log.Printf("vaultmgr: %v", err)
		return
	}

	systray.AddSeparator()
	mVaultsHdr := systray.AddMenuItem(i18n.T("tray_vaults_header"), "")
	mVaultsHdr.Disable()

	vaults, _ := mgr.Detect()
	for _, vi := range vaults {
		vi := vi // capture loop variable
		m := systray.AddMenuItem(vaultLabel(mgr, vi), "")
		go wireVaultItem(ctx, mgr, vi, m)
	}

	mCreate := systray.AddMenuItem(i18n.T("tray_create_vault"), "")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-mCreate.ClickedCh:
				go func() {
					name, ok := promptText(i18n.T("tray_vault_new_name_title"), i18n.T("tray_vault_new_name_prompt"), false)
					if !ok || strings.TrimSpace(name) == "" {
						return
					}
					pw, ok := promptText(fmt.Sprintf(i18n.T("tray_vault_new_pw_title"), name), i18n.T("tray_vault_new_pw_prompt"), true)
					if !ok {
						return
					}
					newVI, err := mgr.Create(name, pw)
					if err != nil {
						notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_create_error"), err))
						return
					}
					// Save recovery key next to SyncDir.
					if v, openErr := vault.Open(newVI.Dir, pw); openErr == nil {
						home, _ := os.UserHomeDir()
						recoveryPath := filepath.Join(home, name+"-recovery.txt")
						phrase := v.RecoveryKey()
						if writeErr := os.WriteFile(recoveryPath, []byte(phrase+"\n"), 0o600); writeErr == nil {
							notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_created_recovery"), name, recoveryPath))
						} else {
							log.Printf("vaultmgr: saving recovery key: %v", writeErr)
							notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_created_no_recovery"), name, writeErr))
						}
					} else {
						notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_created"), name))
					}
					m := systray.AddMenuItem(vaultLabel(mgr, newVI), "")
					go wireVaultItem(ctx, mgr, newVI, m)
				}()
			}
		}
	}()

	// Warn about orphaned plaintext folders.
	if orph, _ := mgr.Orphans(); len(orph) > 0 {
		notify(i18n.T("tray_vault_notification_title"), i18n.T("tray_orphans_warning")+strings.Join(orph, ", "))
	}
}

// vaultLabel returns the menu item title for a vault.
func vaultLabel(mgr *vaultmgr.Manager, vi vaultmgr.VaultInfo) string {
	if mgr.IsOpen(vi.Name) {
		return fmt.Sprintf(i18n.T("tray_vault_close"), vi.Name)
	}
	return fmt.Sprintf(i18n.T("tray_vault_open"), vi.Name)
}

// wireVaultItem runs the click-handler goroutine for a vault menu item.
func wireVaultItem(ctx context.Context, mgr *vaultmgr.Manager, vi vaultmgr.VaultInfo, m *systray.MenuItem) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ClickedCh:
			go func() {
				if mgr.IsOpen(vi.Name) {
					if err := mgr.Close(vi); err != nil {
						notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_close_error"), err))
						return
					}
					m.SetTitle(fmt.Sprintf(i18n.T("tray_vault_open"), vi.Name))
					notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_closed"), vi.Name))
				} else {
					pw, ok := promptText(fmt.Sprintf(i18n.T("tray_vault_open_title"), vi.Name), i18n.T("tray_vault_new_pw_prompt"), true)
					if !ok {
						return
					}
					plain, err := mgr.Open(vi, pw)
					if err != nil {
						if err == vault.ErrWrongPassword {
							notify(i18n.T("tray_vault_notification_title"), i18n.T("tray_vault_wrong_password"))
						} else {
							notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_open_error"), err))
						}
						return
					}
					m.SetTitle(fmt.Sprintf(i18n.T("tray_vault_close"), vi.Name))
					openFolder(plain)
					notify(i18n.T("tray_vault_notification_title"), fmt.Sprintf(i18n.T("tray_vault_opened"), vi.Name))
				}
			}()
		}
	}
}

func openFolder(dir string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", dir).Start()
	case "windows":
		_ = exec.Command("explorer", dir).Start()
	default:
		_ = exec.Command("xdg-open", dir).Start()
	}
}
