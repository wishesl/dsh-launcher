//go:build windows

package main

import (
	"time"

	"golang.org/x/sys/windows"
)

// waitForProcessExit 等 pid 对应的进程退出（自更新重启时，新进程必须先等老进程让出
// Wails 的单实例互斥体 wails-app-dsh-launchersim，否则会被当成"第二实例"直接退出）。
//
// 拿不到句柄就当作"已经退出"直接返回：这是 best-effort 的等待，超时/失败都不能让新进程卡死
// （真冲突时 Wails 会把它当第二实例退出，用户再点一次图标即可，不会造成数据损坏）。
func waitForProcessExit(pid int, timeout time.Duration) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}
