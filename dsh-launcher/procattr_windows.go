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

// createNoWindow is CreateProcess's CREATE_NO_WINDOW: the child is a console
// application run with no console allocated at all.
const createNoWindow = 0x08000000

// consolelessProcAttr is for console children the user must never see. It is
// stricter than newSysProcAttr: HideWindow only hides a console that was still
// allocated (so it can flash for a frame), while CREATE_NO_WINDOW means no
// console is ever created. Used for short-lived helpers such as taskkill, not
// for the instance/pnpm launch path, whose observable behavior is settled.
func consolelessProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// killProcessTree terminates the whole process tree rooted at pid
// (cmd /c → npx/pnpm → node). taskkill /T /F is the reliable Windows path;
// the direct Process.Kill is a fallback when the tree already collapsed.
//
// taskkill is a console program and this launcher is a GUI-subsystem binary
// with no console of its own, so Windows would otherwise allocate one for it —
// that is the black window that used to flash on every 停止 / market-op cancel.
func killProcessTree(pid int, cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		killer := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
		killer.SysProcAttr = consolelessProcAttr()
		_ = killer.Run()
		_ = cmd.Process.Kill()
	}
}
