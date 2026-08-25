package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"fyne.io/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIconICO []byte

//go:embed build/appicon.png
var trayIconPNG []byte

// trayState holds the dynamic per-instance tray menu. Entries persist for the
// lifetime of the app; only titles/checkmarks are updated on status changes,
// which avoids leaking a listener goroutine per rebuild.
type trayState struct {
	ready chan struct{} // closed once systray.Run finished initializing

	mu      sync.Mutex
	parent  *systray.MenuItem       // the "实例" submenu
	entries map[string]*trayEntry    // instanceID -> menu entry
}

type trayEntry struct {
	item   *systray.MenuItem
	status string
	seen   bool // whether an initial title was applied
}

// startTray installs the system tray icon and its menu. The systray message
// loop runs in its own goroutine, which coexists safely with the Wails window
// message loop.
func (a *App) startTray() {
	a.tray = &trayState{
		ready:   make(chan struct{}),
		entries: map[string]*trayEntry{},
	}
	go systray.Run(func() {
		if err := applyTrayIcon(); err != nil {
			println("tray: set icon failed:", err.Error())
		}
		systray.SetTooltip("DSH Launcher")

		mShow := systray.AddMenuItem("显示主界面", "Show DSH Launcher")
		mHide := systray.AddMenuItem("隐藏", "Hide DSH Launcher to tray")
		systray.AddSeparator()

		// Per-instance submenu: one checkbox-style item per instance;
		// clicking toggles start/stop without opening the main window.
		a.tray.mu.Lock()
		a.tray.parent = systray.AddMenuItem("实例", "Start / stop DSH instances")
		a.tray.mu.Unlock()

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

		close(a.tray.ready)
		a.refreshTrayInstances()
	}, func() {})
}

// trayStatusLabel maps an instance status to a short Chinese label.
func trayStatusLabel(s string) string {
	switch s {
	case "starting":
		return "启动中…"
	case "running", "ready":
		return "运行中"
	case "stopping":
		return "停止中…"
	case "crashed":
		return "异常退出"
	default:
		return "已停止"
	}
}

// refreshTrayInstances coalesces refresh requests and updates the submenu.
// Safe to call from any goroutine at any time; no-ops until the tray is up.
func (a *App) refreshTrayInstances() {
	if a.tray == nil {
		return
	}
	select {
	case <-a.tray.ready:
	default:
		return // tray not initialized yet; initial build happens in startTray
	}

	a.tray.mu.Lock()
	defer a.tray.mu.Unlock()

	insts := a.store.list()
	live := map[string]bool{}
	for _, inst := range insts {
		live[inst.ID] = true
		title := fmt.Sprintf("%s　·　%s", inst.Name, trayStatusLabel(inst.Status))
		e, ok := a.tray.entries[inst.ID]
		if !ok {
			item := a.tray.parent.AddSubMenuItem(title, inst.Directory)
			e = &trayEntry{item: item}
			a.tray.entries[inst.ID] = e
			go func(entryID string, ch <-chan struct{}) {
				for range ch {
					a.toggleFromTray(entryID)
				}
			}(inst.ID, item.ClickedCh)
		}
		if !e.seen || e.status != inst.Status {
			e.item.SetTitle(title)
			e.item.SetTooltip(inst.Directory)
			if inst.Status == "starting" || inst.Status == "running" ||
				inst.Status == "ready" || inst.Status == "stopping" {
				e.item.Check()
			} else {
				e.item.Uncheck()
			}
			e.status = inst.Status
			e.seen = true
		}
	}
	// hide entries whose instance was deleted
	for id, e := range a.tray.entries {
		if !live[id] {
			e.item.Hide()
			delete(a.tray.entries, id)
		}
	}
}

// toggleFromTray starts the instance if stopped, stops it if running.
func (a *App) toggleFromTray(id string) {
	a.mu.Lock()
	p := a.processes[id]
	a.mu.Unlock()
	if p != nil {
		_ = a.StopInstance(id)
		return
	}
	go func() {
		if err := a.LaunchInstance(id); err != nil {
			a.systemLog(id, 0, "托盘启动失败: "+err.Error())
		}
	}()
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
