package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application struct; every exported method is bound to the
// frontend as window.go.main.App.*
type App struct {
	ctx context.Context

	store    *instanceStore
	settings *settingsStore
	logs     *logStore

	mu        sync.Mutex
	processes map[string]*managedProcess // instanceID -> running process
	quitting  bool                       // set when the user quits from the tray
	tray      *trayState                 // system tray state (instance submenu)

	// inFlight tracks launch/install attempts that have not yet registered
	// their process in a.processes (including the auto-start stagger sleep).
	// shutdown waits for them so a process can never outlive the app by
	// being spawned *after* the final reap.
	inFlight sync.WaitGroup

	autoStartIDs     atomic.Value // []string — instances to launch when the frontend is ready
	autoStartStarted atomic.Bool  // guards one-shot launch per app run
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		store:     newInstanceStore(),
		settings:  newSettingsStore(),
		logs:      newLogStore(),
		processes: make(map[string]*managedProcess),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = a.EnsureConfigDir()
	// Reconcile persisted instances against live processes on cold start.
	a.mu.Lock()
	for _, inst := range a.store.list() {
		inst.Status = "stopped"
		inst.PID = 0
	}
	autoStart := make([]string, 0)
	for _, inst := range a.store.list() {
		if inst.AutoStart {
			autoStart = append(autoStart, inst.ID)
		}
	}
	a.mu.Unlock()
	a.autoStartIDs.Store(autoStart)

	// Restore last window geometry (defaults remain when unset).
	if st := a.settings.get(); st.WinW > 200 && st.WinH > 150 {
		runtime.WindowSetSize(ctx, st.WinW, st.WinH)
	}
	if st := a.settings.get(); st.WinX != 0 || st.WinY != 0 {
		if x, y := st.WinX, st.WinY; x > -30000 && y > -30000 {
			runtime.WindowSetPosition(ctx, x, y)
		}
	}

	// Install the system tray icon + menu.
	a.startTray()

	// NOTE: auto-start is NOT launched here. Wails events are fire-and-forget:
	// anything emitted before the frontend registers its EventsOn listeners is
	// silently dropped (that is why auto-started instances showed no logs).
	// Instead the frontend calls RunAutoStartInstances() once its listeners
	// are wired up — see that method.
}

// RunAutoStartInstances launches every instance flagged "随启动器自动启动".
// Called by the frontend AFTER its dsh:log/dsh:status listeners are registered,
// guaranteeing no startup events are lost. Idempotent per app run: repeated
// calls return the same IDs but never launch twice.
func (a *App) RunAutoStartInstances() []string {
	ids, ok := a.autoStartIDs.Load().([]string)
	if !ok || len(ids) == 0 {
		return nil
	}
	if a.autoStartStarted.CompareAndSwap(false, true) {
		// Stagger so several npx downloads don't stampede at once.
		for i, id := range ids {
			a.protectedDelayedLaunch(id, time.Duration(800*i)*time.Millisecond)
		}
	}
	return ids
}

// protectedDelayedLaunch launches an instance after a delay, keeping the
// in-flight counter raised for the WHOLE delay+spawn window so shutdown can
// never exit between "scheduled" and "spawned" (the orphan-DSH race).
func (a *App) protectedDelayedLaunch(id string, delay time.Duration) {
	a.inFlight.Add(1)
	go func() {
		defer a.inFlight.Done()
		if delay > 0 {
			time.Sleep(delay)
		}
		a.systemLog(id, 0, "自动启动（随启动器）...")
		if err := a.LaunchInstance(id); err != nil {
			a.systemLog(id, 0, "自动启动失败: "+err.Error())
		}
	}()
}

// shutdown stops every managed process so no orphan DSH keeps running, then
// removes the tray icon.
func (a *App) shutdown(ctx context.Context) {
	a.saveWindowGeometry()
	a.logs.note("shutdown begin")

	// Wait (bounded) for in-flight launch attempts: an auto-start goroutine
	// may still be inside its stagger sleep or mid-spawn. Without this, the
	// app could exit *before* the child was spawned, leaving an orphan that
	// nothing would ever kill.
	drained := make(chan struct{})
	go func() {
		a.inFlight.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		a.logs.note("shutdown: in-flight launches drained")
	case <-time.After(5 * time.Second):
		// a wedged launch must not block exit forever; best effort
		a.logs.note("shutdown: TIMEOUT waiting in-flight launches")
	}

	a.mu.Lock()
	procs := make([]*managedProcess, 0, len(a.processes))
	for _, p := range a.processes {
		procs = append(procs, p)
	}
	a.processes = map[string]*managedProcess{}
	a.mu.Unlock()
	a.logs.note(fmt.Sprintf("shutdown: reaping %d managed process(es)", len(procs)))
	for _, p := range procs {
		p.stop()
	}
	a.logs.closeAll()
	if a.tray != nil {
		systrayQuit()
	}
}

// saveWindowGeometry persists the current window rectangle to settings.
func (a *App) saveWindowGeometry() {
	if a.ctx == nil {
		return
	}
	x, y := runtime.WindowGetPosition(a.ctx)
	w, h := runtime.WindowGetSize(a.ctx)
	if w <= 0 || h <= 0 {
		return
	}
	a.settings.setWindowGeometry(x, y, w, h)
}

// onBeforeClose intercepts the window close request (titlebar ✕, Alt+F4).
// The actual decision is made in the frontend: it shows the "minimize to
// tray or quit?" chooser — unless the user ticked "don't ask again this
// launch" and a choice is already remembered. Blocking here keeps the app
// alive until the frontend answers. Explicit quits (tray menu / chooser's
// 直接退出) set a.quitting first and fall through.
func (a *App) onBeforeClose(ctx context.Context) bool {
	a.mu.Lock()
	quitting := a.quitting
	a.mu.Unlock()
	if quitting {
		return false // allow the app to close
	}
	a.emit("dsh:close-requested", nil)
	return true // block native close; the frontend decides what to do
}

// hideToTrayTip hides the window; on the very first hide it shows a short
// notice first so users learn the app keeps running in the tray.
func (a *App) hideToTrayTip(ctx context.Context) {
	if !a.settings.get().TrayTipShown {
		a.settings.setTrayTipShown(true)
		a.emit("dsh:notice", map[string]string{
			"msg": "已最小化到托盘，DSH 实例仍在后台运行。可从托盘图标唤起。",
		})
		time.AfterFunc(1500*time.Millisecond, func() {
			a.saveWindowGeometry()
			if a.ctx != nil {
				runtime.WindowHide(a.ctx)
			}
		})
		return
	}
	a.saveWindowGeometry()
	runtime.WindowHide(ctx)
}

// emit sends an event to the frontend (guarded against nil context during tests).
// Status events also refresh the tray instance submenu.
func (a *App) emit(event string, payload interface{}) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, event, payload)
	}
	if event == "dsh:status" {
		go a.refreshTrayInstances()
	}
}

// ---------------------------------------------------------------------------
// Instance CRUD
// ---------------------------------------------------------------------------

// GetInstances returns all saved instances.
func (a *App) GetInstances() []Instance {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store.list()
}

// SaveInstance adds a new instance or updates the matching one. It auto-fills
// the local version detected in the target directory.
func (a *App) SaveInstance(inst Instance) ([]Instance, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if inst.Name == "" {
		inst.Name = filepath.Base(inst.Directory)
	}
	if inst.Version == "" {
		inst.Version = "latest"
	}
	if inst.PkgMgr == "" {
		// 本地副本 is the officially recommended launch mode: it runs the
		// DSH source installed in the instance directory (readable by the
		// agent), instead of pulling from a registry cache.
		inst.PkgMgr = "local"
	}
	if inst.CreatedAt.IsZero() {
		inst.CreatedAt = time.Now()
	}
	if inst.LocalVersion == "" {
		inst.LocalVersion = detectLocalVersion(inst.Directory)
	}

	existing := a.store.find(inst.ID)
	if existing != nil {
		// keep runtime fields, copy edited fields
		existing.Name = inst.Name
		existing.Directory = inst.Directory
		existing.Version = inst.Version
		existing.LocalVersion = inst.LocalVersion
		existing.ExtraArgs = inst.ExtraArgs
		existing.PkgMgr = inst.PkgMgr
		existing.AutoStart = inst.AutoStart
		a.store.saveAll()
		go a.refreshTrayInstances()
		return a.store.list(), nil
	}

	inst.ID = newID()
	inst.Status = "stopped"
	inst.PID = 0
	a.store.add(inst)
	a.store.saveAll()
	go a.refreshTrayInstances()
	return a.store.list(), nil
}

// RemoveInstance deletes an instance (stopping it first if it is running).
func (a *App) RemoveInstance(id string) ([]Instance, error) {
	a.mu.Lock()
	if p, ok := a.processes[id]; ok {
		p.stop()
		delete(a.processes, id)
	}
	inst := a.store.find(id)
	if inst != nil {
		inst.Status = "stopped"
	}
	a.mu.Unlock()

	a.store.remove(id)
	a.store.saveAll()
	go a.refreshTrayInstances()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store.list(), nil
}

// ---------------------------------------------------------------------------
// Directory + version helpers
// ---------------------------------------------------------------------------

// SelectDirectory opens a native folder picker and returns the chosen path.
func (a *App) SelectDirectory() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 DSH 启动目录",
	})
}

// DetectLocalVersion reads node_modules/@deepseek-ai/dsh/package.json inside dir.
func (a *App) DetectLocalVersion(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	return detectLocalVersion(dir), nil
}

// DirectoryExists reports whether the given path exists and is a directory.
// Used by the instance form to warn about typos before saving/launching.
func (a *App) DirectoryExists(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// GetAppDataPath returns the config file location, for debugging.
func (a *App) GetAppDataPath() string {
	return a.store.path
}

// SortInstances is a small helper to keep the list deterministic (name asc).
func (a *App) SortInstances() {
	a.mu.Lock()
	defer a.mu.Unlock()
	insts := a.store.list()
	sort.Slice(insts, func(i, j int) bool { return insts[i].Name < insts[j].Name })
	a.store.replace(insts)
}

// EnsureConfigDir is used at startup to make sure the config directory exists.
func (a *App) EnsureConfigDir() error {
	dir := filepath.Dir(a.store.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tray
// ---------------------------------------------------------------------------

// HideToTray hides the main window to the system tray (called from the header
// button). The app keeps running.
func (a *App) HideToTray() {
	if a.ctx == nil {
		return
	}
	a.hideToTrayTip(a.ctx)
}

// QuitApp fully exits the launcher (bound to the frontend exit chooser).
// It reuses the tray quit path: the quitting flag is set first so
// OnBeforeClose lets the app close instead of asking again / hiding.
func (a *App) QuitApp() {
	a.requestQuit()
}

// SetAutoStart toggles an instance's "随启动器自动启动" flag straight from
// its card on the main page, without opening the edit form. Returns the
// updated instance list; the tray submenu is refreshed to stay in sync.
func (a *App) SetAutoStart(id string, enabled bool) ([]Instance, error) {
	a.mu.Lock()
	inst := a.store.find(id)
	if inst == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("实例不存在: %s", id)
	}
	inst.AutoStart = enabled
	a.store.saveAll()
	list := a.store.list()
	a.mu.Unlock()
	go a.refreshTrayInstances()
	return list, nil
}
