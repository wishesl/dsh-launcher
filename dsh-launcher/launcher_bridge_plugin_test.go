package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 端到端契约测试：用 node 跑**内嵌插件的真代码**（testdata/plugin-harness.mjs 提供
// 最小 ctx + 假 settings），经真 WebSocket 连真桥接，验证最要命的那条链路：
// 握手 → 启动器下发 set-theme → 插件写 ui-theme（settings.update）→ 回执。
//
// 没有 node 就跳过：缺 node 不该让 go test 变红，但开发机上应当在跑。
func TestBridgeEndToEndWithRealPlugin(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过真插件端到端测试")
	}
	harness := filepath.Join("testdata", "plugin-harness.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("找不到 harness: %v", err)
	}

	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)
	if app.bridge.url == "" {
		t.Fatal("桥接必须监听成功")
	}
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Unlock()

	// 子进程输出落到临时文件而不是管道：免得碰 Windows 沙箱对管道的限制。
	outFile, err := os.CreateTemp(t.TempDir(), "plugin-harness-out-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "plugin-harness-err-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	cmd := exec.Command(node, "--import", "./testdata/plugin-test-register.mjs", harness)
	cmd.Env = append(os.Environ(),
		"DSH_LAUNCHER=1",
		"DSH_LAUNCHER_EVENTS="+app.bridge.url,
		"DSH_LAUNCHER_TOKEN="+app.bridge.token,
		"DSH_INSTANCE_ID=inst-a",
		"DSH_LAUNCH_ID=L1",
	)
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 harness 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// 1) 插件必须自己连上来并握手。themeSet 由 settings.update 是否可用来上报，
	//    所以它出现就说明插件真的走到了 settings 注入那一层。
	report := waitHandshake(t, app.bridge, "inst-a", "L1")
	if report.Plugin != selfRestartPluginName {
		t.Fatalf("握手插件名不对: %+v", report)
	}
	if !hasCapability(report, "themeSet") || !hasCapability(report, "themeReport") {
		t.Fatalf("真插件应上报 themeReport/themeSet: %+v", report.Capabilities)
	}

	// 2) 启动器下发主题命令 → 插件必须写进 settings（harness 里只有这条路能改成 light）。
	if err := app.SetThemePreference("light"); err != nil {
		t.Fatalf("下发主题失败: %v", err)
	}

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("harness 退出码非 0: %v\nstdout=%s\nstderr=%s",
				werr, readFileString(outFile.Name()), readFileString(errFile.Name()))
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("harness 20s 没结束\nstdout=%s\nstderr=%s",
			readFileString(outFile.Name()), readFileString(errFile.Name()))
	}

	out := readFileString(outFile.Name())
	if !strings.Contains(out, "HARNESS OK") {
		t.Fatalf("harness 没有报告成功\nstdout=%s\nstderr=%s", out, readFileString(errFile.Name()))
	}
	if !strings.Contains(out, `"preference":"light"`) {
		t.Fatalf("settings 里没有写成 light\nstdout=%s", out)
	}
	// 0.2.6 即时通道：同一份代码必须把页面坐标注入 DSH 页面（webserver/index-inject）。
	if !strings.Contains(out, "HARNESS INJECT OK") {
		t.Fatalf("没有给页面注入桥接坐标\nstdout=%s\nstderr=%s", out, readFileString(errFile.Name()))
	}
}

// 端到端契约测试（最要命的一条）：插件侧**取不到 appExit** 时，dsh-restart 也必须真的
// 把进程退掉。testdata/plugin-restart-harness.mjs 故意让三条取法全失败，复现并锁死
// 2026-10-01 的"点了重启没反应"事故：launcher ack 了、插件静默 no-op、进程一直活着。
func TestBridgeRestartEndToEndWithRealPlugin(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过真插件端到端测试")
	}
	harness := filepath.Join("testdata", "plugin-restart-harness.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("找不到 harness: %v", err)
	}

	app, _ := newSelfRestartTestApp(t)
	app.bridge.start()
	t.Cleanup(app.bridge.stop)
	if app.bridge.url == "" {
		t.Fatal("桥接必须监听成功")
	}
	// 当前进程身份：ack 的准入条件就是"发起者必须是当前这次 launch"。
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Unlock()

	outFile, err := os.CreateTemp(t.TempDir(), "plugin-restart-out-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "plugin-restart-err-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	cmd := exec.Command(node, "--import", "./testdata/plugin-test-register.mjs", harness)
	cmd.Env = append(os.Environ(),
		"DSH_LAUNCHER=1",
		"DSH_LAUNCHER_EVENTS="+app.bridge.url,
		"DSH_LAUNCHER_TOKEN="+app.bridge.token,
		"DSH_INSTANCE_ID=inst-a",
		"DSH_LAUNCH_ID=L1",
	)
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 harness 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// 1) 插件先自己连上来握手 —— harness 600ms 后才执行工具，连接必须先就绪。
	if report := waitHandshake(t, app.bridge, "inst-a", "L1"); !hasCapability(report, "restartTool") {
		t.Fatalf("真插件应上报 restartTool: %+v", report.Capabilities)
	}

	// 2) harness 执行 dsh-restart → 桥接 ack → 插件拿不到 appExit → process.exit(0)。
	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("harness 退出码非 0（进程没退？）: %v\nstdout=%s\nstderr=%s",
				werr, readFileString(outFile.Name()), readFileString(errFile.Name()))
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("harness 20s 没结束（重启没让进程退出）\nstdout=%s\nstderr=%s",
			readFileString(outFile.Name()), readFileString(errFile.Name()))
	}

	out := readFileString(outFile.Name())
	errOut := readFileString(errFile.Name())
	if !strings.Contains(out, "RESTART TOOL RESULT") || !strings.Contains(out, "restarting") {
		t.Fatalf("工具没有走到 restarting（ack 没回来？）\nstdout=%s\nstderr=%s", out, errOut)
	}
	// 退出阶梯必须留痕（restartTrace 走 console.error → stderr，双通道之一）。
	if !strings.Contains(errOut, "已装载（plugin") {
		t.Fatalf("插件装载打点应该出现在 stderr\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(errOut, "重启：ack 已收到，开始退出") {
		t.Fatalf("重启退出这一步没有被执行（Host 计时器没排上？）\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(errOut, "取不到 appExit，直接硬退") || !strings.Contains(errOut, "兜底硬退：拿不到 appExit") {
		t.Fatalf("取不到 appExit 时必须走硬退出兜底且留痕\nstdout=%s\nstderr=%s", out, errOut)
	}
	// 3) 桥接侧确实把重启标志记上了（exit-reconcile 靠它决定重新拉起）。
	if !app.bridge.consumeRestart("inst-a", "L1") {
		t.Fatal("桥接应记下属于 L1 的重启标志")
	}
}

// 端到端契约测试（网页端即时通道）：用 node 跑**真实的** lib/client.js（testdata/
// plugin-client-harness.mjs 提供 window.__ModuleLoader__ + 假 ctx/theme 服务），经真
// WebSocket 连真桥接，验证 0.2.6 那条新链路：page-hello 握手 → 启动器下发 page-set-theme
// → 页面当场 setTheme → page-result 回执 → 页面里换主题时 page-theme 回报被启动器采纳。
func TestBridgePageChannelEndToEndWithRealClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过真客户端半边端到端测试")
	}
	harness := filepath.Join("testdata", "plugin-client-harness.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("找不到 harness: %v", err)
	}

	app, _ := newSelfRestartTestApp(t)
	app.settings = newSettingsStore()
	app.bridge.start()
	t.Cleanup(app.bridge.stop)
	if app.bridge.url == "" {
		t.Fatal("桥接必须监听成功")
	}
	app.mu.Lock()
	app.processes["inst-a"] = &managedProcess{instanceID: "inst-a", launchID: "L1"}
	app.mu.Unlock()

	// 页面全局的桥接坐标（等价于服务端插件 webserver/index-inject 注入的那段脚本）。
	coordinates, err := json.Marshal(map[string]string{
		"url":           "ws" + strings.TrimPrefix(app.bridge.url, "http") + "/ws",
		"token":         app.bridge.token,
		"instanceId":    "inst-a",
		"launchId":      "L1",
		"plugin":        selfRestartPluginName,
		"pluginVersion": "0.2.6",
	})
	if err != nil {
		t.Fatal(err)
	}

	outFile, err := os.CreateTemp(t.TempDir(), "client-out-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "client-err-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	cmd := exec.Command(node, harness)
	cmd.Env = append(os.Environ(), "CLIENT_HARNESS_BRIDGE="+string(coordinates))
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 harness 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// 1) 网页端握手：launcher 侧必须把这条连接认成 page 角色（hasPage）。
	waitPage(t, app.bridge, "inst-a")

	// 2) 启动器改主题 → 页面收到 page-set-theme → 真客户端半边调 theme.setTheme。
	if err := app.SetThemePreference("dark"); err != nil {
		t.Fatal(err)
	}

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("harness 退出码非 0: %v\nstdout=%s\nstderr=%s",
				werr, readFileString(outFile.Name()), readFileString(errFile.Name()))
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("harness 20s 没结束\nstdout=%s\nstderr=%s",
			readFileString(outFile.Name()), readFileString(errFile.Name()))
	}

	out := readFileString(outFile.Name())
	errOut := readFileString(errFile.Name())
	if !strings.Contains(out, "HARNESS ID OK") || !strings.Contains(out, "HARNESS APPLY OK") {
		t.Fatalf("client.js 的模块契约不对\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(out, "HARNESS SET THEME dark") {
		t.Fatalf("页面没有收到/应用 page-set-theme\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(out, "HARNESS REPORTED light") || !strings.Contains(out, "HARNESS OK") {
		t.Fatalf("页面侧的回报流程没走完\nstdout=%s\nstderr=%s", out, errOut)
	}

	// 3) 页面里换的主题（page-theme）被启动器当场采纳 —— 不等 DSH 自己写一遍配置。
	if got := app.settings.get().Theme; got != "light" {
		t.Fatalf("网页端回报的主题该被启动器采纳，settings 里是 %q\nstdout=%s\nstderr=%s", got, out, errOut)
	}
	// 4) 页面在场时服务端插件不该被重复下发（同一次点击只写一遍配置）。
	if _, pluginCount := app.bridge.sendThemeCommand("probe", "light"); pluginCount != 0 {
		t.Fatalf("有网页端即时通道时不该再给服务端插件下发（pluginCount=%d）", pluginCount)
	}
}

// 崩溃回归（0.2.7）：0.2.6 在真机上"启动 5s 后 DSH 进程 exit 1"（2026-10-02 00:28/00:29 两次）。
// 机理：cordis 的 ctx.timeout() 把计时器登记在 timer 服务自己的 ctx 上，调用方 fiber 销毁
// 不会取消它 → 5s 主题轮询照跑 → 回调里再调 ctx.timeout 抛
// `cannot get required service "timer" in inactive context` → 没人接 → 进程 exit 1。
//
// testdata/plugin-dispose-harness.mjs 用假 ctx 复现同一时序（失效后 timeout 抛真机原文的错）：
// 0.2.6 的代码在这条测试里必崩（实测 exit 1，栈落在 poll → lib/index.js:625），
// 0.2.7 的护栏必须让它活着退出，并且留下"排不了（已忽略）"的痕。
//
// 不设 DSH_LAUNCHER*：supervised=false，不碰桥接、不发网络请求，只跑崩溃路径。
func TestPluginTimerGuardSurvivesStaleContext(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过真插件崩溃回归测试")
	}
	harness := filepath.Join("testdata", "plugin-dispose-harness.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("找不到 harness: %v", err)
	}

	outFile, err := os.CreateTemp(t.TempDir(), "plugin-dispose-out-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "plugin-dispose-err-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	// 显式剔掉 launcher 环境变量：这条用例必须在"非 launcher 拉起"的形态下跑。
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DSH_LAUNCHER") || strings.HasPrefix(kv, "DSH_INSTANCE_ID") || strings.HasPrefix(kv, "DSH_LAUNCH_ID") {
			continue
		}
		env = append(env, kv)
	}

	cmd := exec.Command(node, "--import", "./testdata/plugin-test-register.mjs", harness)
	cmd.Env = env
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 harness 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("harness 非 0 退出：失效 ctx 上的定时器回调把进程带崩了\n%v\nstdout=%s\nstderr=%s",
				werr, readFileString(outFile.Name()), readFileString(errFile.Name()))
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("harness 30s 没结束\nstdout=%s\nstderr=%s",
			readFileString(outFile.Name()), readFileString(errFile.Name()))
	}

	out := readFileString(outFile.Name())
	errOut := readFileString(errFile.Name())
	for _, marker := range []string{"HARNESS STALE POLL OK", "HARNESS SESSION END OK", "HARNESS OK"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("缺少 %s\nstdout=%s\nstderr=%s", marker, out, errOut)
		}
	}
	if !strings.Contains(errOut, "定时器 主题轮询 排不了") {
		t.Fatalf("失效 ctx 上的轮询没有留下护栏痕\nstdout=%s\nstderr=%s", out, errOut)
	}
	if !strings.Contains(errOut, "装载会话 #2 结束（fiber 销毁）") {
		t.Fatalf("fiber 销毁时没有收掉装载会话\nstdout=%s\nstderr=%s", out, errOut)
	}
}

// 静默回归（0.2.7）：真机 2026-10-02 00:58:33 日志
//
//	dsh: warning: 1 entry did not activate
//	mkt-client-dsh-launcher-plugin (…/node_modules/dsh-launcher-plugin/lib/index.js):
//	  Error: tool "dsh-restart" is already registered
//	    at trackRestartTool (…/lib/index.js:871:22)
//
// 机理：`dsh.client` 声明让宿主 Loader 给同一个包多挂一行（双半包），两行跑同一份 apply，
// 第二次 ctx.tools.register("dsh-restart") 必撞名。修复前 trackRestartTool 原样 rethrow
// → 这一行装载失败 → DSH 销毁它的 fiber，而它恰好是最后建会话的那一行（上一行已被
// beginSession 收掉）→ 插件静默：桥接不连、主题不再跟随。
//
// testdata/plugin-duplicate-tool-harness.mjs 用假 ctx 复现（tools.register 抛真机原文）：
// 修复前的代码在这条测试里 apply 必抛（实测 exit 1，且会话被收掉），修复后必须不抛、
// 留下"已由另一行装载注册（双半包）"的痕，并且会话还活着。
func TestPluginDuplicateToolSurvivesSecondRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过双半包撞名回归测试")
	}
	harness := filepath.Join("testdata", "plugin-duplicate-tool-harness.mjs")
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("找不到 harness: %v", err)
	}

	outFile, err := os.CreateTemp(t.TempDir(), "plugin-dup-tool-out-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(t.TempDir(), "plugin-dup-tool-err-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	// harness 自己会设 DSH_LAUNCHER*（要走 supervised 分支去连一个连不上的桥接），
	// 这里仍然显式剔掉继承来的值，避免真机环境串味。
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DSH_LAUNCHER") || strings.HasPrefix(kv, "DSH_INSTANCE_ID") || strings.HasPrefix(kv, "DSH_LAUNCH_ID") {
			continue
		}
		env = append(env, kv)
	}

	cmd := exec.Command(node, "--import", "./testdata/plugin-test-register.mjs", harness)
	cmd.Env = env
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 harness 失败: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("harness 非 0 退出：撞名注册把这一行装载弄死了（真机上插件会静默）\n%v\nstdout=%s\nstderr=%s",
				werr, readFileString(outFile.Name()), readFileString(errFile.Name()))
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("harness 30s 没结束\nstdout=%s\nstderr=%s",
			readFileString(outFile.Name()), readFileString(errFile.Name()))
	}

	out := readFileString(outFile.Name())
	errOut := readFileString(errFile.Name())
	for _, marker := range []string{"HARNESS DUP TOOL OK", "HARNESS OK"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("缺少 %s\nstdout=%s\nstderr=%s", marker, out, errOut)
		}
	}
	if !strings.Contains(errOut, "已由另一行装载注册（双半包）") {
		t.Fatalf("撞名注册没有留下护栏痕\nstdout=%s\nstderr=%s", out, errOut)
	}
	if strings.Contains(errOut, "装载会话 #1 结束") {
		t.Fatalf("撞名注册之后会话被收掉了（插件会静默）\nstdout=%s\nstderr=%s", out, errOut)
	}
}

func hasCapability(report pluginCapabilityReport, id string) bool {
	for _, c := range report.Capabilities {
		if c.ID == id && c.OK {
			return true
		}
	}
	return false
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "<读不到: " + err.Error() + ">"
	}
	return string(data)
}
