//go:build !windows

package main

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand builds the command that runs a shell command string. On
// macOS/Linux the npx/pnpm entry points are shell scripts, so `sh -c` resolves
// them exactly like the user's shell.
func shellCommand(ctx context.Context, cmdStr string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.SysProcAttr = newSysProcAttr()
	return cmd
}

// newSysProcAttr places the child in its own session (Setsid), so its whole
// tree (sh → npx → node) forms one process group rooted at the child's pid —
// the Unix analogue of Windows job objects. killProcessTree can then reap the
// entire tree with a single group kill.
func newSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killProcessTree terminates the child's whole process group (negative pid),
// with a direct Process.Kill fallback if the group is already gone.
func killProcessTree(pid int, cmd *exec.Cmd) {
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
