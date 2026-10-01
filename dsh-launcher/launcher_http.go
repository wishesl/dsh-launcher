package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// launcherBridge —— dsh-launcher 的 loopback 桥接服务（实现方案 §2）。
//
// 一条线承载三个功能：主题同步、dsh-restart、能力握手。127.0.0.1 临时端口 +
// 随机 Bearer token，随实例启动注入 env（DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN）。
// 全部状态在内存：launcher 重启即清零，与"本次启动"天然对齐，陈旧判定整个消失。

type launcherBridge struct {
	app   *App
	srv   *http.Server
	ln    net.Listener
	url   string // http://127.0.0.1:<port>；空 = 桥接未就绪（门控随之 fail-open）
	token string

	mu         sync.Mutex
	handshakes map[string]pluginCapabilityReport // instanceID → 最近一次 /connect 全量快照
	restarts   map[string]string                 // instanceID → 已 ack 的发起 launchID（一次性重启标志）
	pending    map[string]json.RawMessage        // instanceID → 重启完成续跑负载（注入成功确认后才清）
}

// bridgeEnvelope —— 所有请求的统一信封（实现方案 §2.2）。
type bridgeEnvelope struct {
	Type       string          `json:"type"`
	InstanceID string          `json:"instanceId"`
	LaunchID   string          `json:"launchId"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type bridgeTheme struct {
	Preference string `json:"preference"` // light | dark | system（服务端兜底归一为 system）
}

type bridgeConnectPayload struct {
	Plugin        string             `json:"plugin"`
	PluginVersion string             `json:"pluginVersion"`
	ReportedAt    string             `json:"reportedAt"`
	Capabilities  []pluginCapability `json:"capabilities"`
	Theme         *bridgeTheme       `json:"theme,omitempty"`
}

type bridgeRestartPayload struct {
	Reason  string          `json:"reason"`
	Pending json.RawMessage `json:"pending,omitempty"`
}

// themeEvent —— EmitEvent("dsh:theme") 的载荷。
type themeEvent struct {
	InstanceID string `json:"instanceId"`
	Preference string `json:"preference"`
}

func newLauncherBridge(a *App) *launcherBridge {
	b := &launcherBridge{
		app:        a,
		handshakes: map[string]pluginCapabilityReport{},
		restarts:   map[string]string{},
		pending:    map[string]json.RawMessage{},
	}
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		// crypto/rand 失败属于环境级异常：token 留空 → auth 恒 401 → 桥接不可用，
		// 门控 fail-open（实例照常跑，只是没有插件能力）。
		return b
	}
	b.token = hex.EncodeToString(token)
	return b
}

// start 起 HTTP 服务。失败时 url 保持为空（门控 fail-open）。
func (b *launcherBridge) start() {
	if b == nil || b.token == "" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.app.logs.note("bridge: 监听 127.0.0.1 失败: " + err.Error())
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/connect", b.auth(b.handleConnect))
	mux.HandleFunc("/theme", b.auth(b.handleTheme))
	mux.HandleFunc("/restart", b.auth(b.handleRestart))
	mux.HandleFunc("/pending", b.auth(b.handlePending))
	mux.HandleFunc("/pending-consumed", b.auth(b.handlePendingConsumed))
	b.ln = ln
	b.url = "http://" + ln.Addr().String()
	b.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := b.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			b.app.logs.note("bridge: HTTP 服务退出: " + err.Error())
		}
	}()
}

func (b *launcherBridge) stop() {
	if b == nil || b.srv == nil {
		return
	}
	_ = b.srv.Close()
	b.url = ""
}

// auth —— Bearer token 常量时间校验（token 由 launcher 随 env 下发，插件侧原样回带）。
func (b *launcherBridge) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if b.token == "" || len(got) != len(b.token) ||
			subtle.ConstantTimeCompare([]byte(got), []byte(b.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// 握手：全量替换该实例的最近一次握手；快照里带主题就顺路推事件。
func (b *launcherBridge) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env bridgeEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil || env.InstanceID == "" {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	var payload bridgeConnectPayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &payload)
	}
	report := pluginCapabilityReport{
		Plugin:        payload.Plugin,
		PluginVersion: payload.PluginVersion,
		LaunchID:      env.LaunchID,
		ReportedAt:    payload.ReportedAt,
		InstanceID:    env.InstanceID,
		Capabilities:  payload.Capabilities,
	}
	b.mu.Lock()
	b.handshakes[env.InstanceID] = report
	b.mu.Unlock()
	if payload.Theme != nil {
		b.emitTheme(env.InstanceID, payload.Theme.Preference)
	}
	writeJSON(w, map[string]any{"ok": true})
}

// 主题增量：只转发事件，不做实例校验（幂等状态，丢一条无所谓）。
func (b *launcherBridge) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env bridgeEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil || env.InstanceID == "" {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	var payload bridgeTheme
	_ = json.Unmarshal(env.Payload, &payload)
	b.emitTheme(env.InstanceID, payload.Preference)
	writeJSON(w, map[string]any{"ok": true})
}

// 重启：命令语义 —— 校验发起者确实是当前在跑的那次 launch 才记标志 + ack。
// 拿不到 ack 插件就不会退出（实例白死一次的窗口整个关掉）。
func (b *launcherBridge) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env bridgeEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil || env.InstanceID == "" || env.LaunchID == "" {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	b.app.mu.Lock()
	mp := b.app.processes[env.InstanceID]
	b.app.mu.Unlock()
	if mp == nil || mp.launchID != env.LaunchID {
		// 发起者不是当前进程（陈旧/冒名）：拒绝，插件侧报错不退出。
		http.Error(w, "launch mismatch", http.StatusConflict)
		return
	}
	var payload bridgeRestartPayload
	_ = json.Unmarshal(env.Payload, &payload)
	b.mu.Lock()
	b.restarts[env.InstanceID] = env.LaunchID
	if len(payload.Pending) > 0 {
		b.pending[env.InstanceID] = payload.Pending
	}
	b.mu.Unlock()
	b.app.logs.note("dsh-restart: 收到自重启请求（instance=" + env.InstanceID + "），已 ack，等待进程退出")
	writeJSON(w, map[string]any{"ok": true})
}

// 回取续跑负载：不消费（注入成功由 POST /pending-consumed 确认）。
func (b *launcherBridge) handlePending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID := r.URL.Query().Get("instanceId")
	if instanceID == "" {
		http.Error(w, "instanceId required", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	raw, ok := b.pending[instanceID]
	b.mu.Unlock()
	if !ok || len(raw) == 0 {
		writeJSON(w, map[string]any{"pending": nil})
		return
	}
	writeJSON(w, map[string]any{"pending": json.RawMessage(raw)})
}

// 注入成功确认：此后才不再重复回取。
func (b *launcherBridge) handlePendingConsumed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var env bridgeEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil || env.InstanceID == "" {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	delete(b.pending, env.InstanceID)
	b.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (b *launcherBridge) emitTheme(instanceID, preference string) {
	switch preference {
	case "light", "dark":
	default:
		preference = "system"
	}
	b.app.emit("dsh:theme", themeEvent{InstanceID: instanceID, Preference: preference})
}

// handshake —— capabilities.go 读最近一次握手（握手即报告，无文件、无陈旧比对）。
func (b *launcherBridge) handshake(instanceID string) (pluginCapabilityReport, bool) {
	if b == nil {
		return pluginCapabilityReport{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	report, ok := b.handshakes[instanceID]
	return report, ok
}

// clearHandshake —— 实例（重新）启动时丢弃上一次的握手。
func (b *launcherBridge) clearHandshake(instanceID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	delete(b.handshakes, instanceID)
	b.mu.Unlock()
}

// consumeRestart —— exit-reconcile 用：取走一次性重启标志（无论是否匹配都清掉，
// 避免标志残留引发下次误重启），返回是否属于当前这次 launch。
func (b *launcherBridge) consumeRestart(instanceID, launchID string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.restarts[instanceID]
	delete(b.restarts, instanceID)
	return ok && stored == launchID
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
