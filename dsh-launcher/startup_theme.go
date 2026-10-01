package main

import "github.com/wailsapp/wails/v2/pkg/options"

// 主窗口底色：从 wails.Run 起来到前端画出第一帧之间，窗口涂的就是这个颜色。
//
// 这里以前固定浅色（#f7f8fc），所以深色主题下每次启动都会白闪一下 —— 窗口先白、
// 前端起来后才变黑。现在改成跟随主题偏好。
//
// 取值与 CSS 的 surface 对齐：浅色沿用原来的 #f7f8fc（与 light 的 --bg/#f3f5fa、
// --panel/#ffffff 同族，改动面最小）；深色取 --panel #151517 —— 它同时是 DSH 的
// --dsw-alias-bg-base，于是启动器窗口和 DSH 窗口在那一瞬间是同一个底色。
var (
	startupColourLight = options.RGBA{R: 247, G: 248, B: 252, A: 1}
	startupColourDark  = options.RGBA{R: 21, G: 21, B: 23, A: 1}
)

// startupBackgroundColour 在创建窗口之前解析底色。
//
// 主题偏好是前端在切主题时落盘到 settings.json 的（theme 字段），这里只读文件、
// 不碰前端，所以不存在"要等 WebView 起来才知道主题"的先后矛盾：这次读到的永远是
// 上一次已知的主题，而它正是用户此刻看到的那一套。
func startupBackgroundColour(s *settingsStore) *options.RGBA {
	pref := ""
	if s != nil {
		pref = s.get().Theme
	}
	return backgroundColourFor(pref, systemPrefersDark)
}

// backgroundColourFor 是纯函数（系统深色探测注入进来），便于直接测三态与兜底。
//
// 未表态与 system 都跟随系统；系统探测失败时保持浅色 —— 这是改动前的行为，
// 有刷新失败时宁可维持原样，也不要把窗口涂成另一种颜色。
func backgroundColourFor(preference string, systemDark func() bool) *options.RGBA {
	switch preference {
	case "light":
		return &startupColourLight
	case "dark":
		return &startupColourDark
	}
	if systemDark != nil && systemDark() {
		return &startupColourDark
	}
	return &startupColourLight
}
