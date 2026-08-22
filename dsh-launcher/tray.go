package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"

	"fyne.io/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIconICO []byte

//go:embed build/appicon.png
var trayIconPNG []byte

// startTray installs the system tray icon and its menu. The systray message
// loop runs in its own goroutine, which coexists safely with the Wails window
// message loop.
func (a *App) startTray() {
	go systray.Run(func() {
		if err := applyTrayIcon(); err != nil {
			println("tray: set icon failed:", err.Error())
		}
		systray.SetTooltip("DSH Launcher")

		mShow := systray.AddMenuItem("显示主界面", "Show DSH Launcher")
		mHide := systray.AddMenuItem("隐藏", "Hide DSH Launcher to tray")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "Quit DSH Launcher")

		// Single left-click on the tray icon restores the window.
		systray.SetOnTapped(a.showWindow)

		go func() {
			for {
				select {
				case <-mShow.ClickedCh:
					a.showWindow()
				case <-mHide.ClickedCh:
					a.hideWindow()
				case <-mQuit.ClickedCh:
					a.quitFromTray()
				}
			}
		}()
	}, func() {})
}

// systrayQuit stops the tray icon (called from App.shutdown). Safe no-op if
// the tray was never started.
func systrayQuit() {
	systray.Quit()
}

// applyTrayIcon sets the tray icon. On Windows the tray needs a real .ico file
// on disk (LoadImage IMAGE_ICON), so the embedded ICO is extracted to a temp
// file; other platforms accept PNG bytes directly.
func applyTrayIcon() error {
	if runtime.GOOS == "windows" {
		p := filepath.Join(os.TempDir(), "dsh-launcher-tray.ico")
		if err := os.WriteFile(p, trayIconICO, 0o644); err != nil {
			return err
		}
		return systray.SetIconFromFilePath(p)
	}
	systray.SetIcon(trayIconPNG)
	return nil
}

// showWindow restores and activates the main window.
func (a *App) showWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowShow(a.ctx)
	wruntime.WindowUnminimise(a.ctx)
}

// hideWindow hides the main window, leaving the app running in the tray.
func (a *App) hideWindow() {
	if a.ctx == nil {
		return
	}
	wruntime.WindowHide(a.ctx)
}

// quitFromTray is the sanctioned way to fully exit while tray mode is on.
// It sets a flag first so OnBeforeClose lets the app close instead of hiding
// the window again.
func (a *App) quitFromTray() {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
}
