package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LogEvent is streamed to the frontend on "dsh:log".
type LogEvent struct {
	InstanceID string `json:"instanceId"`
	PID        int    `json:"pid"`
	Line       string `json:"line"`
	Stream     string `json:"stream"` // "stdout" | "stderr" | "system"
	Time       string `json:"time"`
}

// StatusEvent is streamed to the frontend on "dsh:status".
type StatusEvent struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
}

// managedProcess wraps a running DSH process for one instance.
type managedProcess struct {
	instanceID string
	pid        int
	cmd        *exec.Cmd

	done chan struct{}
	once sync.Once
}

func (p *managedProcess) stop() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.done) })
	if p.cmd != nil && p.cmd.Process != nil {
		// taskkill /T kills the whole npx -> node process tree on Windows.
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(p.pid), "/T", "/F").Run()
		_ = p.cmd.Process.Kill()
	}
}

// LaunchInstance starts DSH in the instance's directory with its chosen
// version. Output is streamed to the frontend via the dsh:log event.
func (a *App) LaunchInstance(id string) error {
	a.mu.Lock()
	inst := a.store.find(id)
	if inst == nil {
		a.mu.Unlock()
		return fmt.Errorf("实例不存在: %s", id)
	}
	if _, running := a.processes[id]; running {
		a.mu.Unlock()
		return fmt.Errorf("实例已在运行: %s", inst.Name)
	}

	inst.Status = "starting"
	inst.PID = 0
	snapshot := *inst
	a.mu.Unlock()

	a.emitStatus(snapshot.ID, "starting", 0)
	a.systemLog(snapshot.ID, 0, fmt.Sprintf("正在启动 DSH %s (目录: %s)", versionLabel(snapshot.Version), snapshot.Directory))
	if snapshot.PkgMgr == "local" && snapshot.LocalVersion == "" {
		a.systemLog(snapshot.ID, 0, "提示: 目录内未检测到本地副本，npx 可能回退到 registry 下载。建议先「安装到目录」。")
	}

	cmdStr := buildCommand(snapshot.Version, snapshot.ExtraArgs, snapshot.PkgMgr)
	cmd := exec.Command("cmd", "/c", cmdStr)
	cmd.Dir = snapshot.Directory
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// Auto-answer prompts (e.g. pnpm asking whether to build native modules
	// like node-pty/koffi: "a" = select all, then "y" = confirm).
	cmd.Stdin = strings.NewReader("a\na\ny\ny\n")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.systemLog(snapshot.ID, 0, "创建输出管道失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.systemLog(snapshot.ID, 0, "创建错误管道失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return err
	}

	if err := cmd.Start(); err != nil {
		a.systemLog(snapshot.ID, 0, "启动失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return fmt.Errorf("启动失败: %w", err)
	}

	mp := &managedProcess{
		instanceID: snapshot.ID,
		pid:        cmd.Process.Pid,
		cmd:        cmd,
		done:       make(chan struct{}),
	}

	a.mu.Lock()
	// re-check instance still exists (user may have deleted meanwhile)
	if cur := a.store.find(id); cur != nil {
		cur.PID = cmd.Process.Pid
		cur.Status = "running"
	}
	a.processes[id] = mp
	a.mu.Unlock()

	a.systemLog(snapshot.ID, mp.pid, fmt.Sprintf("进程已启动 PID=%d，命令: %s", mp.pid, cmdStr))
	a.emitStatus(snapshot.ID, "running", mp.pid)

	// Stream output lines to the frontend.
	stream := func(r *bufio.Scanner, tag string) {
		for r.Scan() {
			line := r.Text()
			if line == "" {
				continue
			}
			a.emit("dsh:log", LogEvent{
				InstanceID: snapshot.ID,
				PID:        mp.pid,
				Line:       line,
				Stream:     tag,
				Time:       time.Now().Format(time.RFC3339),
			})
		}
	}
	go stream(bufio.NewScanner(stdout), "stdout")
	go stream(bufio.NewScanner(stderr), "stderr")

	// Wait for exit in the background, then reconcile state.
	go func() {
		_ = cmd.Wait()
		a.systemLog(snapshot.ID, mp.pid, "进程已退出")
		a.mu.Lock()
		if cur := a.store.find(id); cur != nil {
			cur.PID = 0
			cur.Status = "stopped"
		}
		delete(a.processes, id)
		a.mu.Unlock()
		a.emitStatus(snapshot.ID, "stopped", 0)
	}()

	return nil
}

// StopInstance stops a running instance (kills the process tree).
func (a *App) StopInstance(id string) error {
	a.mu.Lock()
	inst := a.store.find(id)
	mp := a.processes[id]
	if inst == nil {
		a.mu.Unlock()
		return fmt.Errorf("实例不存在: %s", id)
	}
	if mp == nil {
		if inst.Status != "stopped" {
			inst.Status = "stopped"
			inst.PID = 0
		}
		a.mu.Unlock()
		return nil // not running, nothing to do
	}
	inst.Status = "stopping"
	pid := mp.pid
	a.mu.Unlock()

	a.emitStatus(id, "stopping", pid)
	a.systemLog(id, pid, "正在停止进程树...")
	mp.stop()

	a.mu.Lock()
	if cur := a.store.find(id); cur != nil {
		cur.Status = "stopped"
		cur.PID = 0
	}
	delete(a.processes, id)
	a.mu.Unlock()
	a.emitStatus(id, "stopped", 0)
	return nil
}

func (a *App) setStopped(id string) {
	a.mu.Lock()
	if cur := a.store.find(id); cur != nil {
		cur.Status = "stopped"
		cur.PID = 0
	}
	a.mu.Unlock()
	a.emitStatus(id, "stopped", 0)
}

func (a *App) emitStatus(id, status string, pid int) {
	a.emit("dsh:status", StatusEvent{InstanceID: id, Status: status, PID: pid})
}

func (a *App) systemLog(id string, pid int, line string) {
	a.emit("dsh:log", LogEvent{
		InstanceID: id,
		PID:        pid,
		Line:       line,
		Stream:     "system",
		Time:       time.Now().Format(time.RFC3339),
	})
}

// buildCommand returns the shell command that launches DSH for a version.
// Examples:
//
//	pnpm dlx @deepseek-ai/dsh@0.1.1-rc.2 web --port 3081   (pkgMgr = "pnpm", default)
//	npx -y @deepseek-ai/dsh@0.1.1-rc.2 web                 (pkgMgr = "npx")
//	npx @deepseek-ai/dsh web --port 3081                   (pkgMgr = "local" — use the
//	                                                         directory's node_modules copy,
//	                                                         the "official design" where the
//	                                                         workspace holds readable DSH source)
func buildCommand(version, extraArgs, pkgMgr string) string {
	extra := strings.TrimSpace(extraArgs)
	switch pkgMgr {
	case "local":
		parts := []string{"npx", "@deepseek-ai/dsh", "web"}
		if extra != "" {
			parts = append(parts, extra)
		}
		return strings.Join(parts, " ")
	case "npx":
		v := strings.TrimSpace(version)
		if v == "" {
			v = "latest"
		}
		parts := []string{"npx", "-y", "@deepseek-ai/dsh@" + v, "web"}
		if extra != "" {
			parts = append(parts, extra)
		}
		return strings.Join(parts, " ")
	}
	// default: pnpm dlx (reliable on machines where npm install hangs)
	v := strings.TrimSpace(version)
	if v == "" {
		v = "latest"
	}
	parts := []string{"pnpm", "dlx", "@deepseek-ai/dsh@" + v, "web"}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " ")
}

func versionLabel(version string) string {
	v := strings.TrimSpace(version)
	if v == "" || v == "latest" {
		return "@latest"
	}
	return "@" + v
}
