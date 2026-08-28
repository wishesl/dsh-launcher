package main

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ToolStatus reports whether a prerequisite CLI tool is installed.
type ToolStatus struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Version string `json:"version"` // "" when not found
}

// EnvReport is the prerequisite-environment snapshot shown in Settings.
type EnvReport struct {
	Npm  ToolStatus `json:"npm"`
	Pnpm ToolStatus `json:"pnpm"`
}

// envBusy guards InstallPnpm against concurrent invocations.
var envBusy atomic.Bool

// toolVersion runs `<tool> --version` and returns its trimmed first line.
// Goes through the platform shell (cmd /c on Windows) so npm.cmd / pnpm.cmd
// shims resolve exactly like they do in the user's shell.
func toolVersion(tool string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := shellCommand(ctx, tool+" --version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return line, nil
}

// CheckEnvironment probes npm and pnpm versions for the Settings panel.
// Both probes run concurrently; a missing tool simply reports Found=false.
func (a *App) CheckEnvironment() EnvReport {
	report := EnvReport{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		v, err := toolVersion("npm")
		report.Npm = ToolStatus{Name: "npm", Found: err == nil, Version: v}
	}()
	go func() {
		defer wg.Done()
		v, err := toolVersion("pnpm")
		report.Pnpm = ToolStatus{Name: "pnpm", Found: err == nil, Version: v}
	}()
	wg.Wait()
	return report
}

// InstallPnpm installs (or upgrades) pnpm globally via "npm install -g pnpm",
// streaming every output line to the frontend as dsh:env-log events. The
// binding resolves when the install finishes; callers re-run
// CheckEnvironment to refresh the version badge.
func (a *App) InstallPnpm() error {
	if !envBusy.CompareAndSwap(false, true) {
		return fmt.Errorf("已有环境安装任务在进行中，请稍候")
	}
	defer envBusy.Store(false)

	// A running npm install spawns children that must not outlive the app if
	// the user quits mid-install — same in-flight contract as instance
	// launches / installs.
	a.inFlight.Add(1)
	defer a.inFlight.Done()

	envLog := func(line string) {
		a.emit("dsh:env-log", map[string]string{"line": line})
	}

	envLog("执行: npm install -g pnpm")
	cmd := shellCommand(context.Background(), "npm install -g pnpm")
	// Route npm's registry download through the configured proxy (if any).
	a.applyProxyToCmd(cmd)
	if pl := a.proxyLogLine(); pl != "" {
		envLog(pl)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		envLog("启动失败: " + err.Error())
		return err
	}

	scan := func(r *bufio.Scanner) {
		for r.Scan() {
			line := strings.TrimRight(r.Text(), "\r")
			if line == "" {
				continue
			}
			envLog(line)
		}
	}
	done := make(chan struct{}, 2)
	go func() { scan(bufio.NewScanner(stdout)); done <- struct{}{} }()
	go func() { scan(bufio.NewScanner(stderr)); done <- struct{}{} }()
	<-done
	<-done

	if err := cmd.Wait(); err != nil {
		envLog("pnpm 安装失败: " + err.Error())
		return err
	}
	if v, verr := toolVersion("pnpm"); verr == nil {
		envLog("pnpm 安装完成: v" + v)
	} else {
		envLog("pnpm 安装完成（版本回读失败）")
	}
	return nil
}
