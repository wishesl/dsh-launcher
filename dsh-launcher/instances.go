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
	Version      string    `json:"version"`      // "latest" or an exact version
	LocalVersion string    `json:"localVersion"` // detected in dir, informational
	ExtraArgs    string    `json:"extraArgs"`    // optional extra CLI args after "web"
	PkgMgr       string    `json:"pkgMgr"`       // "local" (recommended), "pnpm" or "npx"
	AutoStart    bool      `json:"autoStart"`
	SelfRestart  bool      `json:"selfRestart"`  // 自管理重启（dsh-restart）：启动时挂载 dsh-self-mcp 插件（需已安装）
	CreatedAt    time.Time `json:"createdAt"`

	// 源码启动 (Source=true): 不用 npm 版本，直接运行目录内的 DSH 源码。
	// 初始化 / 构建 / 启动由下面的自定义命令控制，默认
	// "pnpm install" / "pnpm run build" / "pnpm dsh web"，用户可在表单中自行输入。
	Source   bool   `json:"source"`
	InitCmd  string `json:"initCmd"`  // 初始化命令（「安装到目录」第一步）
	BuildCmd string `json:"buildCmd"` // 构建命令（「安装到目录」第二步）
	StartCmd string `json:"startCmd"` // 启动命令（「启动」时执行）

	// Runtime fields — live process state. Persisted to disk for continuity,
	// but always re-derived at cold start via resetRuntime: a persisted
	// "ready"/PID must never survive a launcher restart (it previously leaked
	// into the UI as a phantom 运行中 with no process running).
	PID    int    `json:"pid"`
	Status string `json:"status"` // "stopped" | "running" | "starting" | "stopping"
	WebUrl string `json:"webUrl"` // runtime-captured working URL (informational)
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

// resetRuntime clears per-process runtime fields on every stored instance.
// Called on cold start: whatever PID/status was persisted (or was running in a
// previous launcher session) can no longer be trusted. Note this mutates the
// store directly — iterating list()'s copy and assigning would be a no-op.
func (s *instanceStore) resetRuntime() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.loaded {
		s.loaded[i].Status = "stopped"
		s.loaded[i].PID = 0
		s.loaded[i].WebUrl = ""
	}
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
