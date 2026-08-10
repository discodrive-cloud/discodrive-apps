// discodrive is the desktop sync daemon (Dropbox-style).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/i18n"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/syncer"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, i18n.T("usage"))
		os.Exit(2)
	}
	switch os.Args[1] {
	case "pair":
		cmdPair(os.Args[2:])
	case "run":
		cmdRun(os.Args[2:])
	case "tray":
		cmdTray(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "install":
		cmdInstall(os.Args[2:])
	case "uninstall":
		cmdUninstall(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, i18n.T("unknown_command")+"\n", os.Args[1])
		os.Exit(2)
	}
}

func cmdPair(args []string) {
	// pair runs before the device is paired, so we use English (default).
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	server := fs.String("server", "", i18n.T("flag_server"))
	name := fs.String("name", hostname(), i18n.T("flag_name"))
	dir := fs.String("dir", defaultSyncDir(), i18n.T("flag_dir"))
	cfgPath := fs.String("config", mustDefaultCfgPath(), i18n.T("flag_config"))
	_ = fs.Parse(args)
	if *server == "" {
		fatal(i18n.T("pair_need_server"))
	}
	dirExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "dir" {
			dirExplicit = true
		}
	})
	oldServer := ""
	if oldCfg, err := config.Load(*cfgPath); err == nil {
		oldServer = oldCfg.ServerURL
	}
	// Re-pairing to a different server: forget the old index and (unless --dir was
	// given) sync into a fresh per-server folder, so nothing from the old server
	// leaks into — or gets uploaded to — the new one.
	syncDir := resolvePairDir(*dir, dirExplicit, oldServer, *server, defaultSyncDir())
	ctx := context.Background()
	p, err := protocol.PairInit(ctx, *server, *name, "desktop")
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("pair_init_error"), err))
	}
	url := *server + p.VerificationURI
	fmt.Printf(i18n.T("pair_open_browser"), *name, url, p.UserCode)
	_ = openBrowser(url)
	interval := time.Duration(p.Interval) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	token, err := protocol.PairPoll(ctx, *server, p.DeviceCode, interval)
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("pair_wait_error"), err))
	}
	if oldServer != "" && oldServer != *server {
		if err := removeStateDB(*cfgPath); err != nil {
			fatal(fmt.Sprintf(i18n.T("pair_reset_error"), err))
		}
		fmt.Printf(i18n.T("pair_server_changed"), syncDir)
	}
	cfg := config.Config{ServerURL: *server, DeviceToken: token, SyncDir: syncDir}
	if err := cfg.Save(*cfgPath); err != nil {
		fatal(fmt.Sprintf(i18n.T("pair_save_error"), err))
	}
	fmt.Printf(i18n.T("pair_done"), *cfgPath, syncDir)
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", mustDefaultCfgPath(), i18n.T("flag_config"))
	detach := fs.Bool("detach", false, i18n.T("flag_detach"))
	foreground := fs.Bool("foreground", false, i18n.T("flag_foreground"))
	// Escape hatch for the mass-deletion guard: a sync that would remove a large share of the
	// synced files stops instead, since an emptied folder is far more often a lost mirror
	// (an unmounted disk, a folder moved, a reinstall onto a surviving index) than an intent.
	// This lets one run through, for when it really was intended.
	confirmBulkDelete := fs.Bool("confirm-bulk-delete", false, i18n.T("flag_confirm_bulk_delete"))
	_ = fs.Parse(args)
	release, proceed := maybeDaemonize("run", *cfgPath, *detach, *foreground)
	if !proceed {
		return
	}
	defer release()
	s, cleanup, cfg, err := buildSyncer(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("run_init_error"), err))
	}
	defer cleanup()
	if *confirmBulkDelete {
		s.ConfirmBulkDelete()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf(i18n.T("run_syncing"), cfg.SyncDir, cfg.ServerURL)
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		fatal(fmt.Sprintf(i18n.T("run_sync_error"), err))
	}
}

// buildSyncer loads config, fetches the server language, and assembles a Syncer with a cleanup function (closes the index).
func buildSyncer(cfgPath string) (*syncer.Syncer, func(), config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, config.Config{}, fmt.Errorf(i18n.T("build_load_config"), cfgPath, err)
	}

	// Fetch language from server and set it for all subsequent output.
	client := protocol.New(cfg.ServerURL, cfg.DeviceToken)
	if lang, langErr := client.Language(context.Background()); langErr == nil {
		i18n.SetLanguage(lang)
	}

	if err := os.MkdirAll(cfg.SyncDir, 0o755); err != nil {
		return nil, nil, cfg, fmt.Errorf(i18n.T("build_mkdir"), err)
	}
	idx, err := index.Open(config.StateDBPath(cfgPath))
	if err != nil {
		return nil, nil, cfg, fmt.Errorf(i18n.T("build_open_index"), err)
	}
	// An index built against a different server is stale in every way (node ids,
	// cursor, hashes live in the old server's namespace) — wipe it rather than
	// merging two servers' trees. Local files in SyncDir are not touched.
	if stored, serr := idx.ServerURL(); serr == nil && stored != "" && stored != cfg.ServerURL {
		idx.Close()
		if err := removeStateDB(cfgPath); err != nil {
			return nil, nil, cfg, fmt.Errorf(i18n.T("build_open_index"), err)
		}
		if idx, err = index.Open(config.StateDBPath(cfgPath)); err != nil {
			return nil, nil, cfg, fmt.Errorf(i18n.T("build_open_index"), err)
		}
	}
	if err := idx.SetServerURL(cfg.ServerURL); err != nil {
		idx.Close()
		return nil, nil, cfg, fmt.Errorf(i18n.T("build_open_index"), err)
	}
	eng := engine.New(client, idx, cfg.SyncDir)
	s := syncer.New(client, eng, cfg.SyncDir, statusFilePath(cfgPath))
	return s, func() { idx.Close() }, cfg, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "desktop"
	}
	return h
}

func defaultSyncDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "discodrive"
	}
	return filepath.Join(home, "discodrive")
}

func mustDefaultCfgPath() string {
	p, err := config.DefaultPath()
	if err != nil {
		return "config.json"
	}
	return p
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func statusFilePath(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "status.json")
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", mustDefaultCfgPath(), i18n.T("flag_config"))
	_ = fs.Parse(args)

	st, err := syncer.ReadStatus(statusFilePath(*cfgPath))
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println(i18n.T("status_not_running"))
		} else {
			fmt.Fprintf(os.Stderr, i18n.T("status_read_error")+"\n", err)
			os.Exit(1)
		}
		return
	}

	switch st.State {
	case syncer.StateIdle:
		if st.LastSync.IsZero() {
			fmt.Println(i18n.T("status_synced_never"))
		} else {
			fmt.Printf(i18n.T("status_synced")+"\n",
				st.LastSync.Local().Format("2006-01-02 15:04:05"))
		}
	case syncer.StateSyncing:
		fmt.Println(i18n.T("status_syncing"))
	case syncer.StateOffline:
		if st.LastError != "" {
			fmt.Printf(i18n.T("status_offline")+"\n", st.LastError)
		} else {
			fmt.Println(i18n.T("status_offline_no_error"))
		}
	default:
		fmt.Printf(i18n.T("status_unknown")+"\n", st.State)
	}
	fmt.Printf(i18n.T("status_pid_updated")+"\n", st.Pid,
		st.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	cfgPath := fs.String("config", mustDefaultCfgPath(), i18n.T("flag_config"))
	_ = fs.Parse(args)

	binPath, err := os.Executable()
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("install_no_exe"), err))
	}
	logPath := filepath.Join(filepath.Dir(*cfgPath), "daemon.log")
	if err := installAutostart(binPath, *cfgPath, logPath); err != nil {
		fatal(fmt.Sprintf(i18n.T("install_error"), err))
	}
}

func cmdUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	_ = fs.Parse(args)

	if err := uninstallAutostart(); err != nil {
		fatal(fmt.Sprintf(i18n.T("uninstall_error"), err))
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, i18n.T("error_prefix")+msg)
	os.Exit(1)
}
