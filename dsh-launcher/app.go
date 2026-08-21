package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application struct; every exported method is bound to the
// frontend as window.go.main.App.*
type App struct {
	ctx context.Context

	store *instanceStore

	mu        sync.Mutex
	processes map[string]*managedProcess // instanceID -> running process
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		store:     newInstanceStore(),
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
	a.mu.Unlock()
}

// shutdown stops every managed process so no orphan DSH keeps running.
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	procs := make([]*managedProcess, 0, len(a.processes))
	for _, p := range a.processes {
		procs = append(procs, p)
	}
	a.processes = map[string]*managedProcess{}
	a.mu.Unlock()
	for _, p := range procs {
		p.stop()
	}
}

// emit sends an event to the frontend (guarded against nil context during tests).
func (a *App) emit(event string, payload interface{}) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, event, payload)
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
		a.store.saveAll()
		return a.store.list(), nil
	}

	inst.ID = newID()
	inst.Status = "stopped"
	inst.PID = 0
	a.store.add(inst)
	a.store.saveAll()
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
