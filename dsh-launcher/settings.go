package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// settings holds user preferences persisted to %APPDATA%\DSHLauncher\settings.json.
type settings struct {
	// TrayTipShown remembers whether the "still running in tray" notice was
	// already shown once, so it only appears on the very first hide.
	TrayTipShown bool `json:"trayTipShown"`
	// Last window geometry (0 = unset / use built-in defaults).
	WinX int `json:"winX"`
	WinY int `json:"winY"`
	WinW int `json:"winW"`
	WinH int `json:"winH"`
}

// settingsStore persists launcher preferences next to instances.json.
type settingsStore struct {
	mu   sync.Mutex
	path string
	data settings
}

func newSettingsStore() *settingsStore {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	s := &settingsStore{path: filepath.Join(dir, "DSHLauncher", "settings.json")}
	s.load()
	return s
}

func (s *settingsStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // keep defaults
	}
	var d settings
	if err := json.Unmarshal(data, &d); err != nil {
		return
	}
	s.data = d
}

func (s *settingsStore) saveLocked() {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}

// get returns a copy of the current settings.
func (s *settingsStore) get() settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// setTrayTipShown marks the one-time "hidden to tray" notice as shown.
func (s *settingsStore) setTrayTipShown(shown bool) {
	s.mu.Lock()
	s.data.TrayTipShown = shown
	s.saveLocked()
	s.mu.Unlock()
}

// setWindowGeometry persists the last window rectangle.
func (s *settingsStore) setWindowGeometry(x, y, w, h int) {
	s.mu.Lock()
	s.data.WinX, s.data.WinY, s.data.WinW, s.data.WinH = x, y, w, h
	s.saveLocked()
	s.mu.Unlock()
}
