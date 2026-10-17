package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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

	// 源码启动：安装 = 初始化 + 构建（默认 pnpm install → pnpm run build），
	// 不安装 registry 版本，直接对目录内源码操作。
	if snapshot.Source {
		return a.installSourceToDirectory(snapshot)
	}

	if err := validateVersion(snapshot.PkgMgr, snapshot.Version); err != nil {
		return nil, err
	}

	a.systemLog(snapshot.ID, 0, fmt.Sprintf("开始安装 DSH %s 到目录: %s", versionLabel(snapshot.Version), snapshot.Directory))
	if err := os.MkdirAll(snapshot.Directory, 0o755); err != nil {
		return nil, err
	}

	// 0) 先批原生构建，再 `pnpm add`。
	//
	// ⚠️ 顺序不能反：pnpm 11 把"依赖带构建脚本但没被批准"当成**错误**
	// （ERR_PNPM_IGNORED_BUILDS → exit 1）。全新目录里先 `pnpm add` 必然踩它，而批准
	// 清单若写在后面就永远轮不到 —— 新实例直接"安装失败"，且报错只提构建脚本，看不出
	// 是顺序问题（2026-10-17 真机：dsh-subprocess-local / @google/genai / koffi /
	// node-pty / protobufjs 五个）。清单写在 add 之前，add 自己就会把构建跑掉。
	a.systemLog(snapshot.ID, 0, "批准原生模块构建 (koffi / node-pty 等)...")

	if _, err := mergeAllowBuilds(snapshot.Directory, defaultBuildPackages); err != nil {
		return nil, err
	}

	// 1) pnpm add
	addCmd := fmt.Sprintf("pnpm add @deepseek-ai/dsh@%s", effectiveVersion(snapshot.Version))
	tail, err := a.runStreamedTail(snapshot, addCmd, installTailKeep)
	if err != nil {
		// 只有"未批准的原生构建"不当场判死刑：包其实已经装上了，第 3 步还会带着同一份
		// 批准清单再跑一次 pnpm install（真失败在那边报也一样准）。其余错误照旧立刻终止。
		if !isIgnoredBuildsFailure(tail) {
			a.systemLog(snapshot.ID, 0, "安装失败: "+err.Error())
			return nil, err
		}
		a.systemLog(snapshot.ID, 0, fmt.Sprintf(
			"pnpm 报了未批准的原生构建（出现了批准清单外的包？）：%s —— 继续，交给下一步 pnpm install",
			strings.Join(ignoredBuildPackages(tail), ", ")))
	}

	// 2) 再合并一次批准清单：pnpm 在 add 里可能重写过 pnpm-workspace.yaml
	// （merge 幂等，已有条目不会被覆盖）。
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

// installSourceToDirectory runs the 源码启动 install flow: 初始化 + 构建
// (defaults pnpm install / pnpm run build, user-editable). Native-module
// builds are approved between the two so pnpm actually compiles koffi /
// node-pty etc. during install.
func (a *App) installSourceToDirectory(snapshot Instance) ([]Instance, error) {
	if err := os.MkdirAll(snapshot.Directory, 0o755); err != nil {
		return nil, err
	}
	initCmd := effectiveSourceCmd(snapshot.InitCmd, defaultSourceInitCmd)
	buildCmd := effectiveSourceCmd(snapshot.BuildCmd, defaultSourceBuildCmd)

	a.systemLog(snapshot.ID, 0, fmt.Sprintf("开始初始化（源码模式）: %s", snapshot.Directory))
	if err := a.runStreamed(snapshot, initCmd); err != nil {
		a.systemLog(snapshot.ID, 0, "初始化失败: "+err.Error())
		return nil, err
	}
	a.systemLog(snapshot.ID, 0, "批准原生模块构建 (koffi / node-pty 等)...")
	if _, err := mergeAllowBuilds(snapshot.Directory, defaultBuildPackages); err != nil {
		return nil, err
	}
	// 实例目录反复升级 DSH 版本时，pnpm 会把每次的新版本追加进
	// minimumReleaseAgeExclude，而它解析时同一个包只看第一条 —— 旧版本排在前面会让
	// 新版本永远被拒（见 normalizeReleaseAgeExcludes）。装之前先收敛一次。
	if n, nerr := normalizeReleaseAgeExcludes(snapshot.Directory); nerr == nil && n > 0 {
		a.systemLog(snapshot.ID, 0, fmt.Sprintf("已合并 %d 条重复的 pnpm 发布时效排除项", n))
	}
	a.systemLog(snapshot.ID, 0, "开始构建（源码模式）...")
	if err := a.runStreamed(snapshot, buildCmd); err != nil {
		a.systemLog(snapshot.ID, 0, "构建失败: "+err.Error())
		return nil, err
	}

	// refresh the detected local version
	local := detectLocalVersion(snapshot.Directory)
	a.mu.Lock()
	if cur := a.store.find(snapshot.ID); cur != nil {
		cur.LocalVersion = local
	}
	a.store.saveAll()
	list := a.store.list()
	a.mu.Unlock()
	a.systemLog(snapshot.ID, 0, fmt.Sprintf("初始化+构建完成，本地副本: %s", displayLocal(local)))
	return list, nil
}

// installTailKeep 是安装流程要回看的输出行数：pnpm 的报错行就在最后几行。
const installTailKeep = 200

// runStreamed runs a command in the instance's directory and streams each
// output line to the frontend log panel.
func (a *App) runStreamed(snapshot Instance, cmdStr string) error {
	_, err := a.runStreamedTail(snapshot, cmdStr, 0)
	return err
}

// runStreamedTail 与 runStreamed 相同，但额外返回最后 keep 行输出（keep<=0 不收集）。
//
// 为什么要看输出：pnpm 11 的"未批准原生构建"是 warning-turned-error（exit 1，但包已经
// 装好了），不认出这一种就会把正常安装判成失败（见 InstallToDirectory 第 0/1 步）。
func (a *App) runStreamedTail(snapshot Instance, cmdStr string, keep int) ([]string, error) {
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
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var (
		tailMu sync.Mutex
		tail   []string
	)
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
			if keep > 0 {
				tailMu.Lock()
				tail = append(tail, line)
				if len(tail) > keep {
					tail = tail[len(tail)-keep:]
				}
				tailMu.Unlock()
			}
		}
	}
	go emit(bufio.NewScanner(stdout), "stdout")
	go emit(bufio.NewScanner(stderr), "stderr")
	waitErr := cmd.Wait()
	tailMu.Lock()
	defer tailMu.Unlock()
	return append([]string(nil), tail...), waitErr
}

// ignoredBuildsMarker 是 pnpm 11 那句错误里的稳定片段（`[ERR_PNPM_IGNORED_BUILDS]
// Ignored build scripts: …`）。
const ignoredBuildsMarker = "Ignored build scripts:"

// isIgnoredBuildsFailure reports whether the captured output shows pnpm's
// "ignored build scripts" warning-as-error.
func isIgnoredBuildsFailure(tail []string) bool {
	return len(ignoredBuildPackages(tail)) > 0
}

// ignoredBuildPackages 从输出里取出未批准的构建脚本包（`koffi@3.1.1, node-pty@1.2.0`）。
// 认不出就返回空 —— 只有确凿认出这一句才放宽，别的失败照旧当失败。
func ignoredBuildPackages(tail []string) []string {
	for _, line := range tail {
		i := strings.Index(line, ignoredBuildsMarker)
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len(ignoredBuildsMarker):])
		if rest == "" {
			return nil
		}
		out := make([]string, 0, 4)
		for _, item := range strings.Split(rest, ",") {
			if pkg := strings.TrimSpace(item); pkg != "" {
				out = append(out, pkg)
			}
		}
		return out
	}
	return nil
}

func displayLocal(v string) string {
	if v == "" {
		return "（无）"
	}
	return v
}
