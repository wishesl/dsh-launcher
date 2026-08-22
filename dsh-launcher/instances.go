package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Instance represents one launcher entry: a directory + a DSH version.
type Instance struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Directory    string    `json:"directory"`
	Version      string    `json:"version"`     // "latest" or an exact version
	LocalVersion string    `json:"localVersion"` // detected in dir, informational
	ExtraArgs    string    `json:"extraArgs"`    // optional extra CLI args after "web"
	PkgMgr       string    `json:"pkgMgr"`       // "pnpm" (recommended) or "npx"
	AutoStart    bool      `json:"autoStart"`
	CreatedAt    time.Time `json:"createdAt"`

	// Runtime fields (not persisted as meaningful state on disk):
	PID    int    `json:"pid"`
	Status string `json:"status"` // "stopped" | "running" | "starting" | "stopping"
}

// instanceStore persists the instance list as JSON in the user config dir.
type instanceStore struct {
	mu     sync.Mutex
	path   string
	loaded []Instance
}

func newInstanceStore() *instanceStore {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	p := filepath.Join(dir, "DSHLauncher", "instances.json")
	s := &instanceStore{path: p}
	s.load()
	return s
}

func (s *instanceStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.loaded = []Instance{}
		return
	}
	var list []Instance
	if err := json.Unmarshal(data, &list); err != nil {
		s.loaded = []Instance{}
		return
	}
	if list == nil {
		list = []Instance{}
	}
	s.loaded = list
}

func (s *instanceStore) saveAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s.loaded, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}

// list returns a copy safe to hand to the frontend.
func (s *instanceStore) list() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Instance, len(s.loaded))
	copy(out, s.loaded)
	return out
}

func (s *instanceStore) find(id string) *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.loaded {
		if s.loaded[i].ID == id {
			return &s.loaded[i]
		}
	}
	return nil
}

func (s *instanceStore) add(inst Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = append(s.loaded, inst)
}

func (s *instanceStore) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.loaded {
		if s.loaded[i].ID == id {
			s.loaded = append(s.loaded[:i], s.loaded[i+1:]...)
			return
		}
	}
}

func (s *instanceStore) replace(list []Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = list
}

// newID returns a short random hex id.
func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
