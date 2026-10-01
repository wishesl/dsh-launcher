package main

import (
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
	if !strings.Contains(out, "退化为 process.exit(0)") {
		t.Fatalf("取不到 appExit 时必须走硬退出兜底\nstdout=%s\nstderr=%s", out, errOut)
	}
	// 3) 桥接侧确实把重启标志记上了（exit-reconcile 靠它决定重新拉起）。
	if !app.bridge.consumeRestart("inst-a", "L1") {
		t.Fatal("桥接应记下属于 L1 的重启标志")
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
