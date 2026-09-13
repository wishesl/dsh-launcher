//go:build !windows

package main

import (
	"syscall"
	"time"
)

// waitForProcessExit 等 pid 对应的进程退出（非 Windows 实现：轮询 signal 0）。
// 语义与 Windows 版一致：best-effort，超时就继续启动。
func waitForProcessExit(pid int, timeout time.Duration) {
	if pid <= 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH（或没有权限）：进程已经不在
		}
		time.Sleep(120 * time.Millisecond)
	}
}
