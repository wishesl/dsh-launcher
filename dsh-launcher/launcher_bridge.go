package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// launcherBridge —— dsh-launcher 的 loopback 桥接服务（实现方案 §2）。
//
// 一条 WebSocket 线承载全部功能：能力握手、主题同步（双向）、dsh-restart、
// 重启完成的续跑负载。127.0.0.1 临时端口 + 随机 Bearer token，随实例启动注入 env：
//
//	DSH_LAUNCHER_EVENTS = 桥接基地址 http://127.0.0.1:<port>（插件自己换成 ws://…/ws）
//	DSH_LAUNCHER_TOKEN  = Bearer token（握手请求头带它，不进 URL、不进日志）
//
// 全部状态在内存：launcher 重启即清零，与「本次启动」天然对齐，陈旧判定整个消失。
//
// 没有 HTTP REST 端点：旧的 /connect、/theme、/restart、/pending、/pending-consumed
// 已全部搬成帧（用户要求：只保留 ws，不做降级）。第一帧必须是 hello，它把连接绑定到
// 实例 + launchID —— 之前靠 URL 查询参数/请求体携带的身份，现在就是连接自身的状态。

const (
	bridgeReadLimit  = 1 << 20 // 单帧上限 1 MiB（续跑负载可能带一段上下文）
	bridgeWriteWait  = 10 * time.Second
	bridgePongWait   = 60 * time.Second
	bridgePingPeriod = 20 * time.Second
)

// bridgeRestartGrace —— restart 帧 ack 之后等进程退出的宽限。DSH 侧一旦真的调用退出
// 入口，最坏 5s 内必定 process.exit（PROCESS_SHUTDOWN_TIMEOUT_MS），所以 20s 足够松；
// 超时说明插件侧的退出调用没生效，看门狗强制收树（见 armRestartWatchdog）。
//
// 是变量而不是常量：测试要把它压到几十毫秒（否则一个用例得等 20 秒）。
var bridgeRestartGrace = 20 * time.Second

// 帧类型（信封里的 type）。
//
//	插件 → launcher：hello / theme / restart / pending-consumed / command-result
//	launcher → 插件：pending / set-theme / restart-result
//
// 只增不改：不认识的帧直接忽略（老 launcher + 新插件也不会互相打死）。
const (
	frameHello           = "hello"
	frameTheme           = "theme"
	frameRestart         = "restart"
	framePendingConsumed = "pending-consumed"
	frameCommandResult   = "command-result"

	framePending       = "pending"
	frameSetTheme      = "set-theme"
	frameRestartResult = "restart-result"
)

type launcherBridge struct {
	app *App
	srv *http.Server
	ln  net.Listener
	url string // http://127.0.0.1:<port>；空 = 桥接未就绪（门控随之 fail-open）
	// origin —— 本桥接自己的 origin；只有它和「没有 Origin 头」被放行（见 upgrader）。
	origin string
	token  string

	upgrader websocket.Upgrader

	mu         sync.Mutex
	clients    map[string]*bridgeClient          // instanceID → 当前连接（每实例只留最新一条）
	handshakes map[string]pluginCapabilityReport // instanceID → 最近一次 hello 全量快照
	restarts   map[string]string                 // instanceID → 已 ack 的发起 launchID（一次性重启标志）
	pending    map[string]json.RawMessage        // instanceID → 重启完成续跑负载（注入成功确认后才清）
	watchdogs  map[string]*time.Timer            // instanceID → ack 后的退出兜底定时器（进程退出即撤销）
}

// bridgeClient —— 一条已建立的插件连接。instanceID / launchID 由对方的第一帧 hello 绑定，
// 此后所有入帧都以这条连接的身份解释（不再信报文里的 instanceId/launchId）。
type bridgeClient struct {
	instanceID string
	launchID   string
	conn       *websocket.Conn
	writeMu    sync.Mutex // WriteMessage 不能并发；Ping 走 WriteControl，gorilla 允许并发
}

// bridgeEnvelope —— 所有帧的统一信封（实现方案 §2.2）。
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
	ID      string          `json:"id,omitempty"`
	Reason  string          `json:"reason"`
	Pending json.RawMessage `json:"pending,omitempty"`
}

// bridgePendingPayload —— launcher → 插件：重启完成的续跑负载（替代旧版 GET /pending）。
type bridgePendingPayload struct {
	Pending json.RawMessage `json:"pending,omitempty"`
}

// bridgeSetThemePayload —— launcher → 插件：把 ui-theme 写成指定值。
type bridgeSetThemePayload struct {
	ID         string `json:"id"`
	Preference string `json:"preference"`
}

// bridgeCommandResult —— 插件 → launcher：上一条命令的执行结果（id 由 launcher 生成）。
type bridgeCommandResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Detail —— 非主题类命令回报的自由文本（例如插件退出走了 process.exit 兜底）。有它
	// 就原文记进 launcher 日志：真机排障时"插件说了什么"比"命令 id 对不对"有用得多。
	Detail string `json:"detail,omitempty"`
}

// bridgeRestartResult —— launcher → 插件：重启请求是否被受理（id 回带插件给的请求 id）。
type bridgeRestartResult struct {
	ID    string `json:"id,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// themeEvent —— EmitEvent("dsh:theme") 的载荷。
type themeEvent struct {
	InstanceID string `json:"instanceId"`
	Preference string `json:"preference"`
}

func newLauncherBridge(a *App) *launcherBridge {
	b := &launcherBridge{
		app:        a,
		clients:    map[string]*bridgeClient{},
		handshakes: map[string]pluginCapabilityReport{},
		restarts:   map[string]string{},
		pending:    map[string]json.RawMessage{},
		watchdogs:  map[string]*time.Timer{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
	}
	// Origin 白名单：Node 客户端不发 Origin，浏览器一定发（且改不了）。所以
	// 「没有 Origin」= 非浏览器客户端，放行；「Origin = 桥接自己的地址」= 握手 URL
	// 派生的自源（部分 WS 客户端会带上），也放行。其余一律 403 —— 任意本地网页
	// 都无法借道这条已认证的 loopback 通道（哪怕它猜到端口）。
	b.upgrader.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || (b.origin != "" && origin == b.origin)
	}
	b.token = newBridgeID(24)
	// crypto/rand 失败属于环境级异常：token 留空 → auth 恒 401 → 桥接不可用，
	// 门控 fail-open（实例照常跑，只是没有插件能力）。
	return b
}

// start 起 loopback 服务（只有 GET /ws 一条路由）。失败时 url 保持为空（门控 fail-open）。
func (b *launcherBridge) start() {
	if b == nil || b.token == "" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.note("bridge: 监听 127.0.0.1 失败: " + err.Error())
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", b.auth(b.handleWS))
	b.ln = ln
	b.url = "http://" + ln.Addr().String()
	b.origin = b.url
	b.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := b.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			b.note("bridge: WebSocket 服务退出: " + err.Error())
		}
	}()
}

func (b *launcherBridge) stop() {
	if b == nil || b.srv == nil {
		return
	}
	b.mu.Lock()
	clients := make([]*bridgeClient, 0, len(b.clients))
	for _, c := range b.clients {
		clients = append(clients, c)
	}
	b.clients = map[string]*bridgeClient{}
	b.mu.Unlock()
	for _, c := range clients {
		_ = c.conn.Close()
	}
	_ = b.srv.Close()
	b.url = ""
}

// note —— 进右栏运行日志（App 里没有日志存储时静默，测试友好）。
func (b *launcherBridge) note(line string) {
	if b == nil || b.app == nil || b.app.logs == nil {
		return
	}
	b.app.logs.note(line)
}

// auth —— Bearer token 常量时间校验（token 由 launcher 随 env 下发，插件在握手头里回带）。
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

// handleWS —— 升级 + 读循环。一条连接 = 一个实例（重连即替换旧连接）。
func (b *launcherBridge) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// 升级失败时 Upgrade 已经写过 HTTP 响应（401/403/400），这里只补一句日志。
		b.note("bridge: WebSocket 升级失败: " + err.Error())
		return
	}
	client := &bridgeClient{conn: conn}
	done := make(chan struct{})
	go b.pingLoop(client, done)
	b.readLoop(client)
	close(done)
	_ = conn.Close()
	b.unregister(client)
}

// readLoop —— 逐帧读。读超时由 RFC ping/pong 维持（PongHandler 续期）。
func (b *launcherBridge) readLoop(c *bridgeClient) {
	c.conn.SetReadLimit(bridgeReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(bridgePongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(bridgePongWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				b.note("bridge: 插件连接异常结束（instance=" + c.instanceID + "）: " + err.Error())
			}
			return
		}
		var env bridgeEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			b.note("bridge: 收到无法解析的帧，已忽略: " + err.Error())
			continue
		}
		b.handleFrame(c, env)
	}
}

// pingLoop —— 每 20s 一个 RFC ping（Node 客户端自动回 pong），60s 收不到任何帧就判定死连接。
func (b *launcherBridge) pingLoop(c *bridgeClient, done <-chan struct{}) {
	ticker := time.NewTicker(bridgePingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(bridgeWriteWait)); err != nil {
				_ = c.conn.Close()
				return
			}
		}
	}
}

func (b *launcherBridge) handleFrame(c *bridgeClient, env bridgeEnvelope) {
	if env.Type == frameHello {
		b.handleHello(c, env)
		return
	}
	if c.instanceID == "" {
		b.note("bridge: 收到 hello 之前的帧（type=" + env.Type + "），已忽略")
		return
	}
	switch env.Type {
	case frameTheme:
		var payload bridgeTheme
		if len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &payload)
		}
		b.emitTheme(c.instanceID, payload.Preference)
	case frameRestart:
		b.handleRestartFrame(c, env)
	case framePendingConsumed:
		b.consumePending(c)
	case frameCommandResult:
		var payload bridgeCommandResult
		if len(env.Payload) > 0 {
			_ = json.Unmarshal(env.Payload, &payload)
		}
		if payload.Detail != "" {
			// 非主题类回报（如插件侧的退出兜底）：原文照记，别套"主题"的句式。
			b.note("bridge: 实例 " + c.instanceID + " 回报：" + payload.Detail)
		} else if payload.OK {
			b.note("主题：实例 " + c.instanceID + " 已应用主题设置（命令 " + payload.ID + "）")
		} else {
			b.note("主题：实例 " + c.instanceID + " 写入 ui-theme 失败（命令 " + payload.ID + "）: " + payload.Error)
		}
	default:
		// 未知帧忽略：协议只增不改。
	}
}

// handleHello —— 全量快照：替换该实例的最近握手 + 绑定连接身份 + 下发待续跑负载。
//
// 每一次（重）连都发 hello：launcher 的内存态随启动清零，快照必须是最新的。
func (b *launcherBridge) handleHello(c *bridgeClient, env bridgeEnvelope) {
	if env.InstanceID == "" {
		b.note("bridge: hello 缺少 instanceId，已忽略")
		return
	}
	var payload bridgeConnectPayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &payload)
	}
	c.instanceID = env.InstanceID
	c.launchID = env.LaunchID
	report := pluginCapabilityReport{
		Plugin:        payload.Plugin,
		PluginVersion: payload.PluginVersion,
		LaunchID:      env.LaunchID,
		ReportedAt:    payload.ReportedAt,
		InstanceID:    env.InstanceID,
		Capabilities:  payload.Capabilities,
	}
	b.mu.Lock()
	prev := b.clients[c.instanceID]
	b.clients[c.instanceID] = c
	b.handshakes[c.instanceID] = report
	pending := b.pending[c.instanceID]
	b.mu.Unlock()
	if prev != nil && prev != c {
		// 同一实例只留最新一条连接：旧连接立即关掉，避免命令重复下发。
		_ = prev.conn.Close()
	}
	if payload.Theme != nil {
		b.emitTheme(c.instanceID, payload.Theme.Preference)
	}
	// 连接建立也写日志（只断开写会让人查不清"到底连上过没有"，真机排障时吃过这个亏）。
	name := payload.Plugin
	if name == "" {
		name = "未知插件"
	}
	version := payload.PluginVersion
	if version == "" {
		version = "版本未知"
	}
	b.note("bridge: 实例 " + c.instanceID + " 的插件已连接（" + name + " " + version + "，launch=" + env.LaunchID + "）")
	b.pushPending(c, pending)
}

// handleRestartFrame —— 重启：命令语义。校验发起者确实是当前在跑的那次 launch 才记标志 +
// 应答；拿不到应答插件就不会退出（实例白死一次的窗口整个关掉）。
func (b *launcherBridge) handleRestartFrame(c *bridgeClient, env bridgeEnvelope) {
	var payload bridgeRestartPayload
	if len(env.Payload) > 0 {
		_ = json.Unmarshal(env.Payload, &payload)
	}
	if c.launchID == "" {
		_ = c.send(frameRestartResult, bridgeRestartResult{ID: payload.ID, OK: false, Error: "launch mismatch"})
		return
	}
	b.app.mu.Lock()
	mp := b.app.processes[c.instanceID]
	b.app.mu.Unlock()
	if mp == nil || mp.launchID != c.launchID {
		// 发起者不是当前进程（陈旧/冒名）：拒绝，插件侧报错不退出。
		b.note("dsh-restart: 拒绝非当前进程的重启请求（instance=" + c.instanceID + "）")
		_ = c.send(frameRestartResult, bridgeRestartResult{ID: payload.ID, OK: false, Error: "launch mismatch"})
		return
	}
	b.mu.Lock()
	b.restarts[c.instanceID] = c.launchID
	if len(payload.Pending) > 0 {
		b.pending[c.instanceID] = payload.Pending
	}
	b.mu.Unlock()
	b.note("dsh-restart: 收到自重启请求（instance=" + c.instanceID + "），已 ack，等待进程退出")
	_ = c.send(frameRestartResult, bridgeRestartResult{ID: payload.ID, OK: true})
	b.armRestartWatchdog(c.instanceID, c.launchID)
}

// armRestartWatchdog —— ack 之后的兜底。插件拿到 ack 就该调用 DSH 的退出入口；可一旦
// 那一侧没生效（真机 2026-10-01 21:29：ack 了但进程一直活着，launcher 只能干等），
// 用户看到的就是"点了重启没反应"。宽限过后强制收掉进程树，并保留"自重启"语义
// （forceRestart），让 exit-reconcile 照常把实例拉起来 —— 用户要的结果一定拿到。
func (b *launcherBridge) armRestartWatchdog(instanceID, launchID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if timer := b.watchdogs[instanceID]; timer != nil {
		timer.Stop()
	}
	b.watchdogs[instanceID] = time.AfterFunc(bridgeRestartGrace, func() {
		b.fireRestartWatchdog(instanceID, launchID)
	})
	b.mu.Unlock()
}

func (b *launcherBridge) fireRestartWatchdog(instanceID, launchID string) {
	b.mu.Lock()
	delete(b.watchdogs, instanceID)
	b.mu.Unlock()
	b.app.mu.Lock()
	mp := b.app.processes[instanceID]
	b.app.mu.Unlock()
	if mp == nil || mp.launchID != launchID {
		// 进程已经退了（或已经被换掉）：兜底无事可做，别误伤新进程。
		return
	}
	b.note("dsh-restart: 实例 " + instanceID + " 已 ack 但 " + bridgeRestartGrace.String() + " 内没有退出，强制收掉进程树并重新拉起")
	b.app.systemLog(instanceID, mp.pid, "重启超时：插件已确认但进程没有退出，启动器强制重启")
	mp.forceRestart()
}

// disarmRestartWatchdog —— 进程退出路径（exit-reconcile 消费重启标志）撤销兜底。
func (b *launcherBridge) disarmRestartWatchdog(instanceID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if timer := b.watchdogs[instanceID]; timer != nil {
		timer.Stop()
		delete(b.watchdogs, instanceID)
	}
	b.mu.Unlock()
}

// pushPending —— 把该实例待续跑的负载推过去（没有就什么都不发）。不消费：注入成功由
// 插件回 pending-consumed 确认，确认前负载留在 launcher，下次启动还会再推一遍。
func (b *launcherBridge) pushPending(c *bridgeClient, pending json.RawMessage) {
	if len(pending) == 0 {
		return
	}
	if err := c.send(framePending, bridgePendingPayload{Pending: pending}); err != nil {
		b.note("bridge: 续跑负载下发失败（instance=" + c.instanceID + "）：" + err.Error())
	}
}

// consumePending —— 注入成功确认。只认当前进程的确认：重启后旧进程的迟到确认不会
// 把新进程刚要取的负载清掉。
func (b *launcherBridge) consumePending(c *bridgeClient) {
	b.app.mu.Lock()
	mp := b.app.processes[c.instanceID]
	b.app.mu.Unlock()
	if mp != nil && c.launchID != "" && mp.launchID != c.launchID {
		return
	}
	b.mu.Lock()
	delete(b.pending, c.instanceID)
	b.mu.Unlock()
}

func (b *launcherBridge) unregister(c *bridgeClient) {
	if c.instanceID == "" {
		return
	}
	b.mu.Lock()
	removed := b.clients[c.instanceID] == c
	if removed {
		delete(b.clients, c.instanceID)
		// 握手快照必须跟着连接一起失效：报告只在"有人还在线报着"时才成立。以前这里
		// 只删连接，实例停了/插件断了胶囊还一直显示"插件已连接"（真机反馈）。
		delete(b.handshakes, c.instanceID)
	}
	b.mu.Unlock()
	if removed {
		b.note("bridge: 实例 " + c.instanceID + " 的插件连接已断开（握手快照一并失效）")
	}
}

// sendCommand —— 向所有在线实例下发一条命令，返回实际发出的连接数。
func (b *launcherBridge) sendCommand(frameType string, payload any) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	targets := make([]*bridgeClient, 0, len(b.clients))
	for _, c := range b.clients {
		targets = append(targets, c)
	}
	b.mu.Unlock()
	sent := 0
	for _, c := range targets {
		if err := c.send(frameType, payload); err != nil {
			b.note("主题：下发失败（instance=" + c.instanceID + "）：" + err.Error())
			continue
		}
		sent++
	}
	return sent
}

// ---------------------------------------------------------------------------
// 启动器 → 插件 的命令（Wails 绑定：前端「设置 → 主题」用）
// ---------------------------------------------------------------------------

// SetThemePreference 把用户在启动器里选的主题下发给所有在线实例的桥接插件。
//
// 插件写进共享 profile 的 ui-theme 设置；打开的 DSH 网页端会自己实时采纳
// （dsh-client-ui-theme 订阅了同一个 ns），插件随后把新值推回来，启动器据此确认。
//
// 没有实例在线不算失败：前端保留「未同步」状态，等实例连上（hello 带回它当前的主题）
// 再补推一次。返回 error 只用于非法入参（Wails 会把它变成 Promise 拒绝）。
func (a *App) SetThemePreference(preference string) error {
	if preference != "light" && preference != "dark" && preference != "system" {
		return fmt.Errorf("未知主题 %q（只支持 light/dark/system）", preference)
	}
	if a.bridge == nil {
		return fmt.Errorf("桥接未初始化")
	}
	sent := a.bridge.sendCommand(frameSetTheme, bridgeSetThemePayload{
		ID:         newBridgeID(8),
		Preference: preference,
	})
	if sent == 0 {
		a.bridge.note("主题：" + preference + " 已在本机生效；当前没有已连接的实例，等实例连上后再同步")
	} else {
		a.bridge.note(fmt.Sprintf("主题：已向 %d 个实例下发 %s", sent, preference))
	}
	return nil
}

// ---------------------------------------------------------------------------
// 帧读写
// ---------------------------------------------------------------------------

// send —— 一行信封 + 写帧（自动带这条连接的身份）。
func (c *bridgeClient) send(frameType string, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = data
	}
	return c.write(bridgeEnvelope{
		Type:       frameType,
		InstanceID: c.instanceID,
		LaunchID:   c.launchID,
		Payload:    raw,
	})
}

func (c *bridgeClient) write(env bridgeEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(bridgeWriteWait))
	return c.conn.WriteMessage(websocket.TextMessage, data)
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
	// 进程已经退出：ack 后的退出兜底定时器没有意义了，撤掉（免得它去动新进程）。
	b.disarmRestartWatchdog(instanceID)
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, ok := b.restarts[instanceID]
	delete(b.restarts, instanceID)
	return ok && stored == launchID
}

// newBridgeID 生成 n 字节随机 hex（token / 命令 id）。crypto/rand 失败返回空串。
func newBridgeID(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
