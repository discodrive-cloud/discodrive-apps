// discodrive-wails is a POC desktop GUI using Wails (Go + native WebView) over the
// same internal/desktop.Controller as the Fyne client.
package main

import (
	"context"
	"embed"
	"os"
	"slices"

	"fyne.io/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// trayIcon is provided per-platform: PNG on macOS/Linux, ICO on Windows
// (fyne/systray needs ICO bytes on Windows). See tray_other.go / tray_windows.go.

func main() {
	// --hidden is passed by the open-at-login registration when "start minimized" is on,
	// so an auto-launched instance opens straight to the tray; manual launches show the window.
	hidden := slices.Contains(os.Args[1:], hiddenFlag)
	app := &App{startHidden: hidden}

	onReady := func() {
		systray.SetTooltip("DiscoDrive")
		if len(trayIcon) > 0 {
			systray.SetIcon(trayIcon)
		}
		mOpen := systray.AddMenuItem("Open DiscoDrive", "Show the window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit DiscoDrive")
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					app.ShowWindow()
				case <-mQuit.ClickedCh:
					app.QuitApp()
					return
				}
			}
		}()
	}

	// Attempt C: create the status item on the MAIN thread at the very top of main(),
	// before Wails takes over. nativeStart uses the shared NSApplication (which Wails
	// then reuses), so the status item persists and Wails's run loop pumps tray clicks.
	trayStart, _ := systray.RunWithExternalLoop(onReady, func() {})
	trayStart()

	_ = wails.Run(&options.App{
		Title:            "DiscoDrive",
		Width:            1000,
		Height:           700,
		MinWidth:         720,
		MinHeight:        480,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 10, G: 14, B: 20, A: 1},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		StartHidden:      hidden, // auto-launch with --hidden opens to tray, not the window
		OnStartup:        app.startup,
		// Close → hide to the tray and drop the dock icon (Accessory), instead of quitting.
		OnBeforeClose: func(ctx context.Context) bool {
			setDockVisible(false)
			wruntime.WindowHide(ctx)
			return true
		},
		Bind: []interface{}{app},
	})
}
