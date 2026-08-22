package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// settings holds user preferences persisted to %APPDATA%\DSHLauncher\settings.json.
type settings struct {
	// CloseToTray hides the window into the system tray instead of quitting
	// when the user clicks the window close (X) button.
	CloseToTray bool `json:"closeToTray"`
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
	s.data.CloseToTray = true // default: hide to tray on close
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

func (s *settingsStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *settingsStore) setCloseToTray(v bool) {
	s.mu.Lock()
	s.data.CloseToTray = v
	s.mu.Unlock()
	s.save()
}
