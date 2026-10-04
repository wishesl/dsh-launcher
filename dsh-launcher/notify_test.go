package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 会话通知的纯逻辑用例：文本压平/限长、开关默认值与持久化、去重、帧处理分支。
// 真 socket 的协议用例在 launcher_bridge_test.go，真插件代码的端到端用例在
// launcher_bridge_plugin_test.go（plugin-notify-harness.mjs）。

type notifyCall struct {
	title string
	body  string
}

// stubNotify 替换系统通知后端：记录调用、返回预置错误，用例结束自动还原。
func stubNotify(t *testing.T, err error) *[]notifyCall {
	t.Helper()
	calls := &[]notifyCall{}
	old := notifySystem
	notifySystem = func(title, body string) error {
		*calls = append(*calls, notifyCall{title: title, body: body})
		return err
	}
	t.Cleanup(func() { notifySystem = old })
	return calls
}

func TestNotifyOneLine(t *testing.T) {
	long := strings.Repeat("a", 130)
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"普通文本原样", "hello", 120, "hello"},
		{"换行压成空格", "a\nb", 120, "a b"},
		{"CRLF 与制表符", "a\r\nb\tc", 120, "a b c"},
		{"合并连续空格并去首尾", "  spaced   out  ", 120, "spaced out"},
		{"控制字符丢弃", "ctl\x00\x07\x0bx", 120, "ctlx"},
		{"不换行空格算空格", "a\u00a0b", 120, "a b"},
		{"按 rune 截断补省略号", long, 120, strings.Repeat("a", 120) + "…"},
		{"中文按字截断", "中文测试", 2, "中文…"},
		{"emoji 不劈开代理对", "👍👍👍", 2, "👍👍…"},
		{"上限为 0 返回空", "abc", 0, ""},
		{"空串", "", 10, ""},
		{"只有空白", "  \n\t ", 10, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notifyOneLine(tc.in, tc.max); got != tc.want {
				t.Fatalf("notifyOneLine(%q, %d) = %q，想要 %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestNotifySettingsDefaultsAndPersistence(t *testing.T) {
	// settings 缺失 → 默认全开（fail-open：没有偏好不等于关掉）。
	empty := &App{}
	if s := empty.GetNotifySettings(); !s.TurnComplete || !s.Question {
		t.Fatalf("没有 settings 时必须默认全开，得到 %+v", s)
	}
	if !empty.notifyEnabled(notifyKindTurnComplete) || !empty.notifyEnabled(notifyKindQuestion) {
		t.Fatal("没有 settings 时两类通知都必须默认开启")
	}
	if empty.notifyEnabled("nope") {
		t.Fatal("未知 kind 必须为 false")
	}
	if err := empty.SetNotifySettings(NotifySettings{}); err != nil {
		t.Fatalf("没有 settings 时保存不该报错: %v", err)
	}

	// 用户表态 → 写显式值，重新加载后仍然是它（"默认开启"不再适用）。
	path := filepath.Join(t.TempDir(), "settings.json")
	a := &App{settings: &settingsStore{path: path}}
	if err := a.SetNotifySettings(NotifySettings{TurnComplete: false, Question: true}); err != nil {
		t.Fatalf("SetNotifySettings: %v", err)
	}
	if s := a.GetNotifySettings(); s.TurnComplete || !s.Question {
		t.Fatalf("保存后应读回 (false, true)，得到 %+v", s)
	}
	if a.notifyEnabled(notifyKindTurnComplete) {
		t.Fatal("回答完成通知已关闭")
	}
	if !a.notifyEnabled(notifyKindQuestion) {
		t.Fatal("AI 提问通知应仍然开启")
	}
	reloaded := &settingsStore{path: path}
	reloaded.load()
	a2 := &App{settings: reloaded}
	if s := a2.GetNotifySettings(); s.TurnComplete || !s.Question {
		t.Fatalf("重新加载后应仍是 (false, true)，得到 %+v", s)
	}
}

func TestNotifySeenSetDedupe(t *testing.T) {
	s := newNotifySeenSet()
	if !s.first("turn-s1-1") {
		t.Fatal("首次必须放行")
	}
	if s.first("turn-s1-1") {
		t.Fatal("重复必须拦掉")
	}
	if !s.first("") || !s.first("") {
		t.Fatal("空 ID 一律放行（没有标识就不下结论）")
	}
	// 过期条目重新放行。
	s.seen["stale"] = time.Now().Add(-2 * notifyDedupeTTL)
	if !s.first("stale") {
		t.Fatal("超过 TTL 的条目必须重新放行")
	}
	// 表满：新鲜条目清不掉就整表清空，新的 ID 仍然放行（宁可漏一次去重，也不无限增长）。
	full := newNotifySeenSet()
	for i := 0; i < notifyDedupeMax; i++ {
		full.seen[strconv.Itoa(i)] = time.Now()
	}
	if !full.first("fresh") {
		t.Fatal("表满时新 ID 必须放行")
	}
	if len(full.seen) > notifyDedupeMax {
		t.Fatalf("去重表必须收敛在上限内，实际 %d", len(full.seen))
	}
}

func TestHandleNotifyFrameTurnComplete(t *testing.T) {
	calls := stubNotify(t, nil)
	app, _ := newSelfRestartTestApp(t)
	c := &bridgeClient{instanceID: "inst-a"}

	app.handleNotifyFrame(c, bridgeNotifyPayload{
		ID: "turn-s1-3", Kind: notifyKindTurnComplete, SessionID: "s1",
		Title: "会话标题", Text: "最终回复前 50 字",
	})
	if len(*calls) != 1 {
		t.Fatalf("应弹一条通知，实际 %d", len(*calls))
	}
	if (*calls)[0].title != "会话标题" || (*calls)[0].body != "最终回复前 50 字" {
		t.Fatalf("通知内容不对: %+v", (*calls)[0])
	}

	// 同一 ID 重发 → 去重（插件 bug / 重连补发都不能弹两次）。
	app.handleNotifyFrame(c, bridgeNotifyPayload{
		ID: "turn-s1-3", Kind: notifyKindTurnComplete, SessionID: "s1",
		Title: "会话标题", Text: "最终回复前 50 字",
	})
	if len(*calls) != 1 {
		t.Fatalf("重复 ID 必须被去重，实际 %d 条", len(*calls))
	}

	// 未知 kind / 空正文 / 空标题兜底。
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "x1", Kind: "nope", Title: "t", Text: "b"})
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "x2", Kind: notifyKindQuestion, Title: "t", Text: "   "})
	if len(*calls) != 1 {
		t.Fatalf("未知 kind 与空正文都不该弹，实际 %d 条", len(*calls))
	}
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "x3", Kind: notifyKindQuestion, Title: "  ", Text: "我有一些问题"})
	if len(*calls) != 2 || (*calls)[1].title != notifyTitleFallback {
		t.Fatalf("空标题必须兜底为 %q，实际 %+v", notifyTitleFallback, *calls)
	}

	// 多行正文被压成一行（系统通知里多行会被裁掉）。
	app.handleNotifyFrame(c, bridgeNotifyPayload{
		ID: "x4", Kind: notifyKindTurnComplete, Title: "标题\n标题", Text: "第一行\n第二行",
	})
	last := (*calls)[len(*calls)-1]
	if last.title != "标题 标题" || last.body != "第一行 第二行" {
		t.Fatalf("多行必须压成一行，实际 %+v", last)
	}
}

func TestHandleNotifyFrameRespectsSwitches(t *testing.T) {
	calls := stubNotify(t, nil)
	app, _ := newSelfRestartTestApp(t)
	app.settings = &settingsStore{path: filepath.Join(t.TempDir(), "settings.json")}
	if err := app.SetNotifySettings(NotifySettings{TurnComplete: false, Question: true}); err != nil {
		t.Fatal(err)
	}
	c := &bridgeClient{instanceID: "inst-a"}

	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "t1", Kind: notifyKindTurnComplete, Title: "T", Text: "B"})
	if len(*calls) != 0 {
		t.Fatal("关掉「回答完成」后不该弹")
	}
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "q1", Kind: notifyKindQuestion, Title: "T", Text: "我有一些问题"})
	if len(*calls) != 1 {
		t.Fatal("「AI 提问」仍开着，必须弹")
	}
}

func TestHandleNotifyFrameDropsStaleLaunchOnly(t *testing.T) {
	calls := stubNotify(t, nil)
	app, _ := newSelfRestartTestApp(t)
	app.processes["inst-a"] = &managedProcess{launchID: "new-launch"}

	// 两侧都在且不等 → 丢弃（重启竞态里旧进程的迟到帧）。
	stale := &bridgeClient{instanceID: "inst-a", launchID: "old-launch"}
	app.handleNotifyFrame(stale, bridgeNotifyPayload{ID: "t1", Kind: notifyKindTurnComplete, Title: "T", Text: "B"})
	if len(*calls) != 0 {
		t.Fatal("陈旧连接的通知必须丢弃")
	}

	// 任一侧缺凭据 → 不下结论，照常弹（同 stalePluginReport 的纪律）。
	app.processes["inst-a"].launchID = ""
	app.handleNotifyFrame(stale, bridgeNotifyPayload{ID: "t2", Kind: notifyKindTurnComplete, Title: "T", Text: "B"})
	if len(*calls) != 1 {
		t.Fatal("进程侧缺 launchId 时不该拦")
	}
	app.processes["inst-a"].launchID = "new-launch"
	noLaunch := &bridgeClient{instanceID: "inst-a"}
	app.handleNotifyFrame(noLaunch, bridgeNotifyPayload{ID: "t3", Kind: notifyKindTurnComplete, Title: "T", Text: "B"})
	if len(*calls) != 2 {
		t.Fatal("连接侧缺 launchId 时不该拦")
	}
}

func TestHandleNotifyFrameLogsOutcome(t *testing.T) {
	app, _ := newSelfRestartTestApp(t)
	app.settings = &settingsStore{path: filepath.Join(t.TempDir(), "settings.json")}
	dir := t.TempDir()
	app.logs = &logStore{dir: filepath.Join(dir, "logs"), files: map[string]*os.File{}, sizes: map[string]int64{}}
	c := &bridgeClient{instanceID: "inst-a"}

	// 成功：一行「通知（回答完成）：实例 A「标题」正文」。
	stubNotify(t, nil)
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "t1", Kind: notifyKindTurnComplete, Title: "标题", Text: "正文"})
	if got := appLogCount(dir, "通知（回答完成）：实例 A「标题」正文"); got != 1 {
		data, _ := os.ReadFile(filepath.Join(dir, "logs", "app.log"))
		t.Fatalf("成功路径应写一行日志，实际 %d 次：\n%s", got, data)
	}

	// 后端报错：仍然留痕，但**不断言**"没弹出来"（Windows 上 COM 失败 + PowerShell 兜底成功
	// 也会返回非 nil 错误），所以文案只陈述后端返回了错误。
	stubNotify(t, errors.New("boom"))
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "q1", Kind: notifyKindQuestion, Title: "标题", Text: "我有一些问题"})
	if got := appLogCount(dir, "系统通知后端返回错误：boom"); got != 1 {
		data, _ := os.ReadFile(filepath.Join(dir, "logs", "app.log"))
		t.Fatalf("后端错误必须留痕一次，实际 %d 次：\n%s", got, data)
	}
	if got := appLogCount(dir, "通知（AI 提问）：实例 A「标题」我有一些问题"); got != 1 {
		t.Fatal("提问通知的日志文案不对")
	}

	// 开关关闭 → 静默丢弃，不刷日志。
	if err := app.SetNotifySettings(NotifySettings{TurnComplete: true, Question: false}); err != nil {
		t.Fatal(err)
	}
	app.handleNotifyFrame(c, bridgeNotifyPayload{ID: "q2", Kind: notifyKindQuestion, Title: "标题", Text: "我有一些问题"})
	if got := appLogCount(dir, "通知（AI 提问）"); got != 1 {
		t.Fatalf("关掉后不该再写通知日志，实际 %d 次", got)
	}
}
