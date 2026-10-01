package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 桥接的 WebSocket 契约测试：用真实的 WebSocket 客户端（gorilla）扮演插件侧，
// 驱动握手 / 命令下发 / 重启 ack / 续跑负载 / 鉴权与 Origin 拒绝。这些是协议本身
// 的行为，跑真 socket 才能验（旧版 HTTP handler 的路径已被整体替换）。

// dialBridge 连桥接（模拟插件侧）。
func dialBridge(t *testing.T, app *App, token string, extra http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	for k, vs := range extra {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	wsURL := "ws" + strings.TrimPrefix(app.bridge.url, "http") + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	return dialer.Dial(wsURL, header)
}

func sendBridgeFrame(t *testing.T, c *websocket.Conn, env bridgeEnvelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
}

func readBridgeFrame(t *testing.T, c *websocket.Conn) bridgeEnvelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	var env bridgeEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("解析帧失败: %v (%s)", err, data)
	}
	return env
}

func helloFrame(launchID, theme string) bridgeEnvelope {
	payload, _ := json.Marshal(bridgeConnectPayload{
		Plugin:        selfRestartPluginName,
		PluginVersion: "0.2.1",
		ReportedAt:    time.Now().UTC().Format(time.RFC3339),
		Capabilities:  []pluginCapability{{ID: "pluginLoaded", OK: true}},
		Theme:         &bridgeTheme{Preference: theme},
	})
	return bridgeEnvelope{Type: frameHello, InstanceID: "inst-a", LaunchID: launchID, Payload: payload}
}

// waitHandshake 等到该实例的握手记录的 launchID 符合预期（重连会替换旧记录）。
func waitHandshake(t *testing.T, b *launcherBridge, id, launchID string) pluginCapabilityReport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := b.handshake(id); ok && r.LaunchID == launchID {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("3s 内没有出现 launchID=%s 的握手记录", launchID)
	return pluginCapabilityReport{}
}

func TestBridgeWebSocketAuthAndOrigin(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)
	if app.bridge.url == "" {
		t.Fatal("桥接必须监听成功")
	}

	// 没有 token → 401（token 只在握手头里，不进 URL）。
	_, resp, err := dialBridge(t, app, "", nil)
	if err == nil {
		t.Fatal("没有 token 不该升级成功")
	}
	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("无 token 期望 401，得到 %d", resp.StatusCode)
		}
	}

	// 带外部 Origin（浏览器页面）→ 403：本地网页不能借道。
	_, resp2, err2 := dialBridge(t, app, app.bridge.token, http.Header{"Origin": []string{"http://evil.local"}})
	if err2 == nil {
		t.Fatal("外部 Origin 不该升级成功")
	}
	if resp2 != nil {
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusForbidden {
			t.Fatalf("外部 Origin 期望 403，得到 %d", resp2.StatusCode)
		}
	}
}

func TestBridgeWebSocketProtocol(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	conn, resp, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("带 token 应能升级: %v (resp=%+v)", err, resp)
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	defer conn.Close()

	// hello 之前发别的帧：忽略，且不绑定身份（旧版靠 URL/请求体带身份，现在靠握手）。
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameTheme, InstanceID: "inst-a", Payload: json.RawMessage(`{"preference":"dark"}`),
	})
	if _, ok := app.bridge.handshake("inst-a"); ok {
		t.Fatal("hello 之前不该有握手记录")
	}

	// hello：全量快照（能力 + 主题）。
	sendBridgeFrame(t, conn, helloFrame("L1", "dark"))
	report := waitHandshake(t, app.bridge, "inst-a", "L1")
	if report.PluginVersion != "0.2.1" || len(report.Capabilities) != 1 || report.Capabilities[0].ID != "pluginLoaded" {
		t.Fatalf("握手内容不对: %+v", report)
	}

	// 启动器 → 插件：改主题命令。
	if err := app.SetThemePreference("light"); err != nil {
		t.Fatalf("下发主题失败: %v", err)
	}
	env := readBridgeFrame(t, conn)
	if env.Type != frameSetTheme {
		t.Fatalf("期望 %s，得到 %s", frameSetTheme, env.Type)
	}
	var setPayload bridgeSetThemePayload
	_ = json.Unmarshal(env.Payload, &setPayload)
	if setPayload.Preference != "light" || setPayload.ID == "" {
		t.Fatalf("set-theme 载荷不对: %+v", setPayload)
	}
	// 插件回执（幂等状态：只验不 panic、链路通）。
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameCommandResult, InstanceID: "inst-a",
		Payload: json.RawMessage(`{"id":"` + setPayload.ID + `","ok":true}`),
	})
	// 非法主题直接拒绝（Wails 绑定侧就是 Promise 拒绝）。
	if err := app.SetThemePreference("pink"); err == nil {
		t.Fatal("非法主题必须报错")
	}

	// restart：launch 匹配才 ack，续跑负载留 launcher 内存。
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Unlock()

	pending := json.RawMessage(`{"sessionId":"s-1","reason":"测试","requestedAt":1}`)
	restartPayload, _ := json.Marshal(bridgeRestartPayload{ID: "r-1", Reason: "测试", Pending: pending})
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameRestart, InstanceID: "inst-a", LaunchID: "L1", Payload: restartPayload,
	})
	env = readBridgeFrame(t, conn)
	if env.Type != frameRestartResult {
		t.Fatalf("期望 %s，得到 %s", frameRestartResult, env.Type)
	}
	var rr bridgeRestartResult
	_ = json.Unmarshal(env.Payload, &rr)
	if !rr.OK || rr.ID != "r-1" {
		t.Fatalf("restart 应答不对: %+v", rr)
	}
	if !app.bridge.consumeRestart("inst-a", "L1") {
		t.Fatal("重启标志应属于 L1")
	}

	// 重连（再一次 hello）→ 续跑负载重新下发（不消费）。
	sendBridgeFrame(t, conn, helloFrame("L1", "dark"))
	env = readBridgeFrame(t, conn)
	if env.Type != framePending {
		t.Fatalf("期望 %s，得到 %s", framePending, env.Type)
	}
	var pp bridgePendingPayload
	_ = json.Unmarshal(env.Payload, &pp)
	if len(pp.Pending) == 0 {
		t.Fatal("下发的 pending 负载不该为空")
	}

	// 注入成功确认 → launcher 才清。
	sendBridgeFrame(t, conn, bridgeEnvelope{Type: framePendingConsumed, InstanceID: "inst-a", LaunchID: "L1"})
	cleared := time.Now().Add(2 * time.Second)
	for {
		app.bridge.mu.Lock()
		_, still := app.bridge.pending["inst-a"]
		app.bridge.mu.Unlock()
		if !still {
			break
		}
		if time.Now().After(cleared) {
			t.Fatal("pending-consumed 之后负载该被清掉")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// launch 不匹配 → 拒绝（插件收到 ok=false 就不退出进程）。
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{instanceID: "inst-a", launchID: "L2"}
	app.mu.Unlock()
	badPayload, _ := json.Marshal(bridgeRestartPayload{ID: "r-2", Reason: "测试"})
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameRestart, InstanceID: "inst-a", LaunchID: "L1", Payload: badPayload,
	})
	env = readBridgeFrame(t, conn)
	var bad bridgeRestartResult
	_ = json.Unmarshal(env.Payload, &bad)
	if bad.OK || bad.Error != "launch mismatch" || bad.ID != "r-2" {
		t.Fatalf("launch 不匹配必须拒绝: %+v", bad)
	}
}

// 同实例的新连接必须踢掉旧连接：否则一条命令会被下发两次（旧连接还没死透时）。
func TestBridgeNewConnectionReplacesOld(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	first, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("首个连接失败: %v", err)
	}
	defer first.Close()
	sendBridgeFrame(t, first, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")

	second, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("第二个连接失败: %v", err)
	}
	defer second.Close()
	sendBridgeFrame(t, second, helloFrame("L2", "light"))
	waitHandshake(t, app.bridge, "inst-a", "L2")

	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, rerr := first.ReadMessage(); rerr == nil {
		t.Fatal("同实例的新连接必须把旧连接踢掉")
	}
}

// 连接断开 = 握手快照作废：否则实例已经停了/插件已经断了，顶栏胶囊还一直显示
// 「插件已连接」（真机反馈：一直是绿的，不刷新）。
func TestBridgeDisconnectClearsHandshake(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	conn, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	sendBridgeFrame(t, conn, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")

	_ = conn.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := app.bridge.handshake("inst-a"); !ok {
			// 连接也没了、快照也没了 —— 面板这下只能报「插件未连接」，是与事实一致的结论。
			app.bridge.mu.Lock()
			_, stillConnected := app.bridge.clients["inst-a"]
			app.bridge.mu.Unlock()
			if stillConnected {
				t.Fatal("握手清了，连接却还在")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("断开后握手快照必须一并失效")
}

// 进程断开后重连（hello）必须把快照恢复：拔线重连不该让能力面板一直空着。
func TestBridgeReconnectRestoresHandshake(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	first, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	sendBridgeFrame(t, first, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")
	_ = first.Close()
	waitHandshakeGone(t, app.bridge, "inst-a")

	second, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("重连失败: %v", err)
	}
	defer second.Close()
	sendBridgeFrame(t, second, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")
}

func waitHandshakeGone(t *testing.T, b *launcherBridge, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := b.handshake(id); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s 的握手快照该在断开后消失", id)
}

// ack 之后进程一直不退（插件侧退出调用没生效）→ 看门狗强制收树，但保留"自重启"语义，
// 让 exit-reconcile 照常重新拉起实例。真机事故：2026-10-01 21:29 点了重启没反应。
func TestBridgeRestartWatchdogForcesRestart(t *testing.T) {
	grace := bridgeRestartGrace
	bridgeRestartGrace = 30 * time.Millisecond
	t.Cleanup(func() { bridgeRestartGrace = grace })

	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	conn, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	mp := &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Lock()
	app.processes["inst-a"] = mp
	app.mu.Unlock()

	sendBridgeFrame(t, conn, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")

	restartPayload, _ := json.Marshal(bridgeRestartPayload{ID: "r-1", Reason: "看门狗测试"})
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameRestart, InstanceID: "inst-a", LaunchID: "L1", Payload: restartPayload,
	})
	if env := readBridgeFrame(t, conn); env.Type != frameRestartResult {
		t.Fatalf("期望 %s，得到 %s", frameRestartResult, env.Type)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mp.forcedRestart.Load() {
			// 强制收树必须仍然按"自重启"对账：stopReq 绝不能置（置了就变成"用户停止"，
			// exit-reconcile 不会重新拉起，用户看到的是实例被关掉）。
			if mp.stopRequested() {
				t.Fatal("看门狗不该置 stopReq：那会让 reconcile 不再拉起实例")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ack 后进程没退出，看门狗必须在宽限后强制收树")
}

// 进程自己按时退出了 → 撤销兜底，别去动下一次启动的新进程。
func TestBridgeRestartExitDisarmsWatchdog(t *testing.T) {
	grace := bridgeRestartGrace
	bridgeRestartGrace = 80 * time.Millisecond
	t.Cleanup(func() { bridgeRestartGrace = grace })

	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)

	conn, _, err := dialBridge(t, app, app.bridge.token, nil)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	mp := &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Lock()
	app.processes["inst-a"] = mp
	app.mu.Unlock()

	sendBridgeFrame(t, conn, helloFrame("L1", "dark"))
	waitHandshake(t, app.bridge, "inst-a", "L1")

	restartPayload, _ := json.Marshal(bridgeRestartPayload{ID: "r-1"})
	sendBridgeFrame(t, conn, bridgeEnvelope{
		Type: frameRestart, InstanceID: "inst-a", LaunchID: "L1", Payload: restartPayload,
	})
	if env := readBridgeFrame(t, conn); env.Type != frameRestartResult {
		t.Fatalf("期望 %s，得到 %s", frameRestartResult, env.Type)
	}

	// 模拟 exit-reconcile：进程退了，重启标志被消费 → 兜底必须撤销。
	if !app.bridge.consumeRestart("inst-a", "L1") {
		t.Fatal("重启标志应属于 L1")
	}
	time.Sleep(200 * time.Millisecond)
	if mp.forcedRestart.Load() {
		t.Fatal("进程已正常退出，看门狗不该再强制收树")
	}
}
