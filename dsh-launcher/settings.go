package main

import (
	"encoding/json"
	"fmt"
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
	// Plugin-market registry mirror (empty = official curated catalog).
	MarketRegistryURL string `json:"marketRegistryURL"`
	// Network proxy routed to launcher-run downloads (pnpm/npm/git installs,
	// catalog & registry fetches). Empty = direct (no proxy).
	Proxy string `json:"proxy"`
	// UI layout override: "" = auto per OS, "mac" = Mac 布局, "win" = Win/Linux 布局.
	Layout string `json:"layout"`
	// 可拖拽栏宽（0 = 未设置，前端回落到默认值 204 / 440）。
	SidebarWidth int `json:"sidebarWidth"`
	LogWidth     int `json:"logWidth"`
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

// setMarketRegistryURL persists the plugin-market registry mirror (empty
// restores the official curated catalog).
func (s *settingsStore) setMarketRegistryURL(url string) {
	s.mu.Lock()
	s.data.MarketRegistryURL = url
	s.saveLocked()
	s.mu.Unlock()
}

// setProxy persists the network proxy (empty clears it).
func (s *settingsStore) setProxy(url string) {
	s.mu.Lock()
	s.data.Proxy = url
	s.saveLocked()
	s.mu.Unlock()
}

// setLayout persists the UI layout override ("" = auto per OS).
func (s *settingsStore) setLayout(layout string) {
	s.mu.Lock()
	s.data.Layout = layout
	s.saveLocked()
	s.mu.Unlock()
}

// setUIWidths persists the two draggable pane widths (0 = unset).
func (s *settingsStore) setUIWidths(sidebar, log int) {
	s.mu.Lock()
	s.data.SidebarWidth, s.data.LogWidth = sidebar, log
	s.saveLocked()
	s.mu.Unlock()
}

// GetLayout returns the UI layout override ("" = auto per platform).
func (a *App) GetLayout() string {
	if a.settings == nil {
		return ""
	}
	return a.settings.get().Layout
}

// SetLayout persists the UI layout override: "" = auto, "mac", "win".
func (a *App) SetLayout(layout string) error {
	switch layout {
	case "", "mac", "win":
	default:
		return fmt.Errorf("未知布局: %s（可用: 自动/空、mac、win）", layout)
	}
	if a.settings != nil {
		a.settings.setLayout(layout)
	}
	return nil
}

// maxUIWidth bounds a persisted pane width, so a hand-edited or corrupt
// settings.json cannot hand the frontend an absurd value.
const maxUIWidth = 4000

// UIWidths carries the two draggable pane widths to the frontend. 0 means
// "unset": the frontend then falls back to its own default (204 / 440).
type UIWidths struct {
	Sidebar int `json:"sidebar"`
	Log     int `json:"log"`
}

// GetUIWidths returns the persisted pane widths (0 = unset).
func (a *App) GetUIWidths() UIWidths {
	if a.settings == nil {
		return UIWidths{}
	}
	d := a.settings.get()
	return UIWidths{Sidebar: d.SidebarWidth, Log: d.LogWidth}
}

// SetUIWidths persists the pane widths when a drag settles (not per frame).
// Bounds stay permissive on purpose: the frontend clamps against the live
// viewport, and a width saved on a wide window must still load on a narrow one.
func (a *App) SetUIWidths(w UIWidths) error {
	if w.Sidebar < 0 || w.Log < 0 {
		return fmt.Errorf("栏宽不能为负: sidebar=%d log=%d", w.Sidebar, w.Log)
	}
	if w.Sidebar > maxUIWidth || w.Log > maxUIWidth {
		return fmt.Errorf("栏宽超出上限 %d: sidebar=%d log=%d", maxUIWidth, w.Sidebar, w.Log)
	}
	if a.settings != nil {
		a.settings.setUIWidths(w.Sidebar, w.Log)
	}
	return nil
}
