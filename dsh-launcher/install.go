package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// defaultBuildPackages approves native-module build scripts for pnpm so
// koffi / node-pty / etc. get their binaries compiled. pnpm 11 reads this
// from pnpm-workspace.yaml (it writes a "set this to true or false" placeholder
// file on first install when builds are ignored).
var defaultBuildPackages = []string{
	"@deepseek-ai/dsh-subprocess-local",
	"@google/genai",
	"koffi",
	"node-pty",
	"protobufjs",
}

// effectiveVersion returns the version used for pnpm add (latest passthrough).
func effectiveVersion(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return "latest"
	}
	return v
}

// InstallToDirectory installs the instance's chosen DSH version as a REAL,
// readable node_modules inside the instance's directory (the "official
// design": the working directory contains the DSH source, so an agent can
// read it and develop plugins). Uses pnpm to dodge the npm-install hang.
//
// Steps:
//  1. pnpm add @deepseek-ai/dsh@<version>   (creates package.json + node_modules)
//  2. write pnpm-workspace.yaml with allowBuilds: true
//  3. pnpm install                          (runs native-module build scripts)
//  4. refresh the detected local version
func (a *App) InstallToDirectory(id string) ([]Instance, error) {
	// In-flight protection: a running install spawns pnpm children that must
	// not outlive the app if the user quits mid-install.
	a.inFlight.Add(1)
	defer a.inFlight.Done()

	a.mu.Lock()
	inst := a.store.find(id)
	if inst == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("实例不存在: %s", id)
	}
	if p, ok := a.processes[id]; ok && p != nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("实例正在运行，请先停止再安装: %s", inst.Name)
	}
	snapshot := *inst
	a.mu.Unlock()

	if err := validateVersion(snapshot.PkgMgr, snapshot.Version); err != nil {
		return nil, err
	}

	a.systemLog(snapshot.ID, 0, fmt.Sprintf("开始安装 DSH %s 到目录: %s", versionLabel(snapshot.Version), snapshot.Directory))
	if err := os.MkdirAll(snapshot.Directory, 0o755); err != nil {
		return nil, err
	}

	// 1) pnpm add
	if err := a.runStreamed(snapshot, fmt.Sprintf("pnpm add @deepseek-ai/dsh@%s", effectiveVersion(snapshot.Version))); err != nil {
		a.systemLog(snapshot.ID, 0, "安装失败: "+err.Error())
		return nil, err
	}

	// 2) approve native builds (merge — preserve any existing entries the
	// user already approved instead of overwriting the whole file)
	a.systemLog(snapshot.ID, 0, "批准原生模块构建 (koffi / node-pty 等)...")
	if _, err := mergeAllowBuilds(snapshot.Directory, defaultBuildPackages); err != nil {
		return nil, err
	}

	// 3) run build scripts
	if err := a.runStreamed(snapshot, "pnpm install"); err != nil {
		a.systemLog(snapshot.ID, 0, "原生模块构建失败: "+err.Error())
		return nil, err
	}

	// 4) refresh local version
	local := detectLocalVersion(snapshot.Directory)
	a.mu.Lock()
	if cur := a.store.find(id); cur != nil {
		cur.LocalVersion = local
	}
	a.store.saveAll()
	list := a.store.list()
	a.mu.Unlock()
	a.systemLog(snapshot.ID, 0, fmt.Sprintf("安装完成，本地副本: %s", displayLocal(local)))
	return list, nil
}

// runStreamed runs a command in the instance's directory and streams each
// output line to the frontend log panel.
func (a *App) runStreamed(snapshot Instance, cmdStr string) error {
	a.systemLog(snapshot.ID, 0, "执行: "+cmdStr)
	if pl := a.proxyLogLine(); pl != "" {
		a.systemLog(snapshot.ID, 0, pl)
	}
	cmd := shellCommand(context.Background(), cmdStr)
	cmd.Dir = snapshot.Directory
	// Route pnpm/npm downloads through the configured proxy (if any).
	a.applyProxyToCmd(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	emit := func(r *bufio.Scanner, tag string) {
		for r.Scan() {
			line := strings.TrimRight(r.Text(), "\r")
			if line == "" {
				continue
			}
			a.logEvent(LogEvent{
				InstanceID: snapshot.ID,
				PID:        cmd.Process.Pid,
				Line:       line,
				Stream:     tag,
				Time:       time.Now().Format(time.RFC3339),
			})
		}
	}
	go emit(bufio.NewScanner(stdout), "stdout")
	go emit(bufio.NewScanner(stderr), "stderr")
	return cmd.Wait()
}

func displayLocal(v string) string {
	if v == "" {
		return "（无）"
	}
	return v
}
