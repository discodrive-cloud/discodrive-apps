// discodrive-wails is a POC desktop GUI using Wails (Go + native WebView) over the
// same internal/desktop.Controller as the Fyne client.
package main

import (
	"context"
	"embed"
	"os"
	"runtime"
	"slices"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// The tray icon is provided per platform — a template silhouette on macOS, a PNG on
// Linux, an ICO on Windows (fyne/systray needs ICO bytes there). See tray_*.go.

func main() {
	// --hidden is passed by the open-at-login registration when "start minimized" is on,
	// so an auto-launched instance opens straight to the tray; manual launches show the window.
	hidden := slices.Contains(os.Args[1:], hiddenFlag)
	app := &App{startHidden: hidden}

	onReady := func() {
		systray.SetTooltip("DiscoDrive")
		setTrayIcon()
		mOpen := systray.AddMenuItem("Open DiscoDrive", "Show the window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit DiscoDrive")
		go runTrayActions(mOpen.ClickedCh, mQuit.ClickedCh, app.ShowWindow, app.QuitApp)
	}

	// Attempt C: create the status item on the MAIN thread at the very top of main(),
	// before Wails takes over. nativeStart uses the shared NSApplication (which Wails
	// then reuses), so the status item persists and Wails's run loop pumps tray clicks.
	trayStart, _ := systray.RunWithExternalLoop(onReady, func() {})
	trayStart()

	installLauncher() // Linux: a launcher entry so the dock shows the app icon

	_ = wails.Run(appOptions(app))
}

func appOptions(app *App) *options.App {
	opts := &options.App{
		Linux:            linuxOptions(),
		Title:            "DiscoDrive",
		Width:            1000,
		Height:           700,
		MinWidth:         720,
		MinHeight:        480,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 10, G: 14, B: 20, A: 1},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		StartHidden:      app.startHidden, // auto-launch with --hidden opens to tray, not the window
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		// Close → hide to the tray and drop the dock icon (Accessory), instead of quitting.
		OnBeforeClose: func(ctx context.Context) bool {
			setDockVisible(false)
			wruntime.WindowHide(ctx)
			return true
		},
		Bind: []interface{}{app},
	}
	if runtime.GOOS == "darwin" {
		// Cocoa handles the close button by hiding the app before it reaches
		// OnBeforeClose. Native Quit (menu, Cmd+Q, Dock) still reaches this hook.
		opts.HideWindowOnClose = true
		opts.OnBeforeClose = func(context.Context) bool {
			app.QuitApp()
			// QuitApp exits after draining work, or retains the session if a vault
			// cannot be saved. Never let Wails tear down the event loop separately.
			return true
		}
	}
	return opts
}

// runTrayActions owns the menu event loop independently of the native tray.
func runTrayActions(open, quit <-chan struct{}, showWindow, quitApp func()) {
	for {
		select {
		case _, ok := <-open:
			if !ok {
				return
			}
			showWindow()
		case _, ok := <-quit:
			if !ok {
				return
			}
			// A rejected quit returns; keep accepting open and retry actions.
			quitApp()
		}
	}
}
