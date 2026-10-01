//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// systemPrefersDark 读 Windows 的"应用模式"（设置 → 个性化 → 颜色 → 选择默认应用模式）。
// AppsUseLightTheme = 0 表示深色。读不到（键被删/权限不足）就返回 false：宁可当浅色，
// 也不要在读不准的时候把窗口涂黑 —— 这是改动前的行为。
func systemPrefersDark() bool {
	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return v == 0
}
