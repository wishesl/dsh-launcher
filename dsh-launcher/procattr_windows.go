//go:build windows

package main

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// shellCommand builds the command that runs a shell command string. On Windows
// the DSH/plugin tools are .cmd shims (npm.cmd / pnpm.cmd), so they must go
// through cmd /c to resolve exactly like in the user's shell.
func shellCommand(ctx context.Context, cmdStr string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/c", cmdStr)
	cmd.SysProcAttr = newSysProcAttr()
	return cmd
}

// newSysProcAttr returns the child process attributes: a hidden window so no
// console flashes when the launcher spawns DSH / pnpm.
func newSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree terminates the whole process tree rooted at pid
// (cmd /c → npx/pnpm → node). taskkill /T /F is the reliable Windows path;
// the direct Process.Kill is a fallback when the tree already collapsed.
func killProcessTree(pid int, cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		_ = cmd.Process.Kill()
	}
}
