package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// 会话通知（回答完成 / AI 提问 → 系统通知）。
//
// 链路：实例里的 dsh-launcher-plugin 监听 DSH 的 session/event —— 一次回答正常结束
// （turn/end 且 reason.kind==="completed"）或模型用 ask_user_question 提问时，经既有的
// loopback 桥推一帧 notify（见 launcher_bridge.go 的 bridgeNotifyPayload）。**是否真的弹
// 系统通知由这里决定**：开关在启动器「设置 → 通知」（settings.go 的两个 *bool，默认开启），
// 改完立即生效，不需要重启 DSH。
//
// 三条纪律（与 AGENTS §9 一致）：
//   - 只有能确证的才拦：帧缺 ID 就不去重、任一侧缺 launchId 就不判"陈旧连接"；
//   - 系统通知后端失败**不重试**：通知是时效性的，晚到的通知比没有更糟；
//   - 后端返回错误时**不断言"没弹出来"**：Windows 上 go-toast 的 COM 失败但 PowerShell
//     兜底成功时同样返回非 nil 错误（errors.Join 保留 COM 错误），所以只记一行日志。

const (
	// 通知类型（插件 → launcher 的 notify 帧）。未知 kind 一律忽略：协议只增不改。
	notifyKindTurnComplete = "turn-complete"
	notifyKindQuestion     = "question"

	// 文本上限（插件已把正文截到 50 字，这里只是不信任输入的兜底）。
	notifyTitleMax = 120
	notifyTextMax  = 200

	// 去重窗口：同一个 ID（插件按 会话+回合 / 调用 id 生成）在这个时间内重复到达一律忽略。
	notifyDedupeTTL = 10 * time.Minute
	// 去重表上限：超出先按时间清理，仍超就整表清空（宁可漏一次去重，也不无限增长）。
	notifyDedupeMax = 128

	// 标题兜底（插件已经会兜底，这里只是防脏输入）。
	notifyTitleFallback = "DSH 会话"
)

// NotifySettings 是「通知」相关的偏好（存 settings.json）。两个字段都来自 *bool：nil =
// 用户没表态 = 默认开启；用户一旦表态就写显式值（同 UpdateSettings 的 AutoCheck）。
type NotifySettings struct {
	TurnComplete bool `json:"turnComplete"`
	Question     bool `json:"question"`
}

// notifySystem 是可替换的系统通知后端入口（测试替换它，见 notify_test.go）。平台实现见
// notify_windows.go / notify_darwin.go / notify_linux.go / notify_other.go。
var notifySystem = platformNotify

// notifySeenSet 是通知去重表。插件重发同一帧（bug / 重连补发）时只放行一次。
type notifySeenSet struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newNotifySeenSet() *notifySeenSet {
	return &notifySeenSet{seen: map[string]time.Time{}}
}

// first 报告这个 ID 是不是第一次出现（空 ID 一律放行：没有标识就不下结论）。
func (s *notifySeenSet) first(id string) bool {
	if s == nil || id == "" {
		return true
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if at, ok := s.seen[id]; ok && now.Sub(at) < notifyDedupeTTL {
		return false
	}
	if len(s.seen) >= notifyDedupeMax {
		for key, at := range s.seen {
			if now.Sub(at) >= notifyDedupeTTL {
				delete(s.seen, key)
			}
		}
		if len(s.seen) >= notifyDedupeMax {
			s.seen = map[string]time.Time{}
		}
	}
	s.seen[id] = now
	return true
}

// notifyOneLine 把任意文本压成一行（换行 / 制表符 → 空格、丢掉其余控制字符、合并连续空格）
// 并按 rune 限长。系统通知里多行文本会被裁掉，所以入口就压平。
func notifyOneLine(raw string, max int) string {
	if max <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	space := true // 前导空白一并吃掉
	for _, r := range raw {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == '\u00a0':
			if !space {
				b.WriteByte(' ')
				space = true
			}
		case r < 0x20 || r == 0x7f:
			// 其余控制字符（\v \f \x00…）直接丢弃
		default:
			b.WriteRune(r)
			space = false
		}
	}
	out := strings.TrimRight(b.String(), " ")
	runes := []rune(out)
	if len(runes) <= max {
		return out
	}
	return string(runes[:max]) + "…"
}

// GetNotifySettings 返回通知偏好（两个开关默认开启）。
func (a *App) GetNotifySettings() NotifySettings {
	s := NotifySettings{TurnComplete: true, Question: true}
	if a != nil && a.settings != nil {
		d := a.settings.get()
		if d.NotifyTurnComplete != nil {
			s.TurnComplete = *d.NotifyTurnComplete
		}
		if d.NotifyQuestion != nil {
			s.Question = *d.NotifyQuestion
		}
	}
	return s
}

// SetNotifySettings 保存通知偏好。显式写入：用户一旦表态，"默认开启"就不再适用。
func (a *App) SetNotifySettings(s NotifySettings) error {
	if a != nil && a.settings != nil {
		a.settings.setNotifySettings(s.TurnComplete, s.Question)
	}
	return nil
}

// notifyEnabled 报告某一类通知是否开启（settings 缺失 / 用户没表态 = 开启）。
func (a *App) notifyEnabled(kind string) bool {
	s := a.GetNotifySettings()
	switch kind {
	case notifyKindTurnComplete:
		return s.TurnComplete
	case notifyKindQuestion:
		return s.Question
	default:
		return false
	}
}

// handleNotifyFrame 处理插件推来的通知帧：校验 → 开关 → 系统通知 → 右栏日志一行。
//
// 静默丢弃的四种情况：未知 kind（协议只增不改）、陈旧连接（两侧 launchId 都在且不等）、
// 开关关闭（开关本身就是可见状态，不必每回合刷一行日志）、重复 ID（去重）。
func (a *App) handleNotifyFrame(c *bridgeClient, p bridgeNotifyPayload) {
	if a == nil {
		return
	}
	if p.Kind != notifyKindTurnComplete && p.Kind != notifyKindQuestion {
		return
	}
	instanceID := ""
	if c != nil {
		instanceID = c.instanceID
	}
	// 陈旧连接（重启竞态里旧进程的迟到帧）：只有两侧 launchId 都在且不相等时才判定 ——
	// 任一侧缺凭据就不下结论（同 stalePluginReport 的纪律）。
	if c != nil && c.launchID != "" {
		a.mu.Lock()
		mp := a.processes[instanceID]
		a.mu.Unlock()
		if mp != nil && mp.launchID != "" && mp.launchID != c.launchID {
			return
		}
	}
	if !a.notifyEnabled(p.Kind) {
		return
	}
	if !a.notifySeen.first(p.ID) {
		return
	}
	title := notifyOneLine(p.Title, notifyTitleMax)
	if title == "" {
		title = notifyTitleFallback
	}
	text := notifyOneLine(p.Text, notifyTextMax)
	if text == "" {
		return // 没有正文的通知没有信息量
	}
	label := "回答完成"
	if p.Kind == notifyKindQuestion {
		label = "AI 提问"
	}
	name := instanceID
	if a.store != nil {
		if inst := a.store.find(instanceID); inst != nil && strings.TrimSpace(inst.Name) != "" {
			name = inst.Name
		}
	}
	if err := notifySystem(title, text); err != nil {
		a.noteNotify(fmt.Sprintf("通知（%s）：实例 %s「%s」%s（系统通知后端返回错误：%v）", label, name, title, text, err))
		return
	}
	a.noteNotify(fmt.Sprintf("通知（%s）：实例 %s「%s」%s", label, name, title, text))
}

// noteNotify 写一行右栏运行日志（logStore 缺失时静默，测试友好）。
func (a *App) noteNotify(line string) {
	if a == nil || a.logs == nil {
		return
	}
	a.logs.note(line)
}
