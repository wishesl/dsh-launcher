//go:build !windows

package main

// 非 Windows 平台没有可移植的"应用深色模式"探测方式（macOS 要读 AppleInterfaceStyle、
// Linux 得看各桌面环境），这里直接返回 false = 当浅色处理，也就是改动前的行为。
// 真在别的平台上跑起来再做，别为了猜一个颜色引入平台分支。
func systemPrefersDark() bool { return false }
