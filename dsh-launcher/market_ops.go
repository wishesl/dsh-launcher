package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Install / uninstall of community plugins, ported from dsh-market's
// routes.ts + dsh-cli.ts. The launcher re-invokes the SAME dsh CLI entry an
// instance runs (`dsh plugin --profile web add|remove …`), so the CLI's own
// reconcile keeps the profile's dsh.profile.bundles layer stack correct and
// the version stays consistent with what the instance runs.
//
// Security mirrors dsh-market: the install target is derived server-side from
// a curated registry entry (never from arbitrary frontend input), validated
// against a shell-safe whitelist, and the source must be present in the
// catalog at install time.

const marketOpTimeout = 15 * time.Minute

// TARGET_RE is the shell-safety whitelist for install targets (same as
// dsh-market): `^`, `~` and `=` are allowed because registry specs carry
// semver ranges such as `pkg@^0.14.0`.
var targetRe = regexp.MustCompile(`^[A-Za-z0-9@:./_#+~^=-]+$`)

var (
	githubURLRe = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:/tree/[^/]+/(.+?))?/?$`)
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	npmNameRe   = regexp.MustCompile(`^(@[a-z0-9-~][a-z0-9-._~]*/)?[a-z0-9-~][a-z0-9-._~]*$`)
)

// validSubpath rejects path segments that could escape the repo in a
// `github:owner/repo#path:/…` selector.
func validSubpath(sub string) bool {
	if !regexp.MustCompile(`^[A-Za-z0-9_./-]+$`).MatchString(sub) {
		return false
	}
	for _, seg := range strings.Split(sub, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// installTargetFor maps a curated registry entry to its pnpm install target
// (port of dsh-market sources.ts). npm tarballs win over full-repo GitHub
// downloads; monorepo subpackages use `github:owner/repo#path:/sub`.
// Returns (target, ok).
func installTargetFor(entryURL string, npm *string) (string, bool) {
	m := githubURLRe.FindStringSubmatch(strings.TrimRight(strings.TrimSpace(entryURL), "/"))
	if m == nil {
		return "", false
	}
	repo := m[1]
	if !repoRe.MatchString(repo) {
		return "", false
	}
	if npm != nil && *npm != "" && npmNameRe.MatchString(*npm) {
		return *npm, true
	}
	sub := ""
	if len(m) > 2 {
		sub = m[2]
	}
	if sub != "" {
		if !validSubpath(sub) {
			return "", false
		}
		return "github:" + repo + "#path:/" + sub, true
	}
	return "github:" + repo, true
}

// repoOf extracts `owner/repo` from a registry entry URL, or "".
func repoOf(entryURL string) string {
	m := githubURLRe.FindStringSubmatch(strings.TrimRight(strings.TrimSpace(entryURL), "/"))
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// installedAlias returns the installed package name when the entry is already
// present (same name, same npm package, or same GitHub repo), else "".
func installedAlias(entry MarketPlugin, installed map[string]string) string {
	repo := repoOf(entry.URL)
	for name, spec := range installed {
		if strings.EqualFold(name, entry.Name) {
			return name
		}
		if entry.NPM != nil && strings.EqualFold(name, *entry.NPM) {
			return name
		}
		if repo != "" && strings.Contains(strings.ToLower(spec), repo) {
			return name
		}
	}
	return ""
}

// parseIgnoredBuilds extracts the package names pnpm refuses to run build
// scripts for (pnpm >= 10 blocks them by default). We do not pass
// --reporter=ndjson, so we read the human "Ignored build scripts: …" line.
func parseIgnoredBuilds(output string) []string {
	lineRe := regexp.MustCompile(`(?i)ignored build scripts:\s*([^\r\n.]+)`)
	var out []string
	for _, m := range lineRe.FindAllStringSubmatch(output, -1) {
		for _, part := range strings.Split(m[1], ",") {
			p := strings.TrimSpace(part)
			if p != "" && !strings.ContainsAny(p, " \t") {
				out = append(out, p)
			}
		}
	}
	return out
}

// --- single-flight market operation state ---

var (
	marketBusy    atomic.Bool
	marketOpMu    sync.Mutex
	marketOpJob   *winJob
	marketOpCmd   *exec.Cmd
	marketOpCancel atomic.Bool
)

// MarketOpRunning reports whether an install/uninstall is currently in
// flight (used by the frontend on mount to render the busy state).
func (a *App) MarketOpRunning() bool {
	return marketBusy.Load()
}

// CancelMarketOp terminates the running plugin command's process tree.
func (a *App) CancelMarketOp() bool {
	marketOpMu.Lock()
	defer marketOpMu.Unlock()
	marketOpCancel.Store(true)
	if marketOpCmd != nil && marketOpCmd.Process != nil {
		_ = exec.Command("taskkill", "/PID", fmt.Sprint(marketOpCmd.Process.Pid), "/T", "/F").Run()
	}
	if marketOpJob != nil {
		marketOpJob.close() // kernel kills the whole tree
	}
	return true
}

// marketOpStatus streams operation state to the frontend on dsh:market-status.
type MarketOpStatus struct {
	State   string   `json:"state"` // running | done | failed | cancelled
	Kind    string   `json:"kind"`  // install | uninstall
	Target  string   `json:"target"`
	Error   string   `json:"error,omitempty"`
	Blocked []string `json:"blockedBuilds,omitempty"`
}

// MarketOpResult is the structured return of an install/uninstall call.
type MarketOpResult struct {
	OK            bool     `json:"ok"`
	Cancelled     bool     `json:"cancelled"`
	Installed     []string `json:"installed"`
	BlockedBuilds []string `json:"blockedBuilds"`
	Output        string   `json:"output"`
	Error         string   `json:"error"`
}

func (a *App) emitMarketStatus(s MarketOpStatus) {
	a.emit("dsh:market-status", s)
}

// pluginCommand builds the CLI invocation for plugin management, mirroring
// buildCommand(): same dsh entry, `web` replaced with `plugin --profile <p>`.
// The instance's own extraArgs (e.g. --port) are intentionally NOT forwarded.
func pluginCommand(inst Instance, args ...string) string {
	switch inst.PkgMgr {
	case "local":
		return strings.Join(append([]string{"npx", "@deepseek-ai/dsh", "plugin", "--profile", marketProfileName}, args...), " ")
	case "npx":
		v := strings.TrimSpace(inst.Version)
		if v == "" {
			v = "latest"
		}
		return strings.Join(append([]string{"npx", "-y", "@deepseek-ai/dsh@" + v, "plugin", "--profile", marketProfileName}, args...), " ")
	default:
		v := strings.TrimSpace(inst.Version)
		if v == "" {
			v = "latest"
		}
		return strings.Join(append([]string{"pnpm", "dlx", "@deepseek-ai/dsh@" + v, "plugin", "--profile", marketProfileName}, args...), " ")
	}
}

// runMarketCommand runs a plugin command in the given directory, streaming
// every line to dsh:market-log. On success returns the combined output; on
// failure returns the output and the error (or a cancelled marker).
func (a *App) runMarketCommand(dir, cmdStr string) (output string, cancelled bool, err error) {
	a.inFlight.Add(1)
	defer a.inFlight.Done()

	cmd := exec.Command("cmd", "/c", cmdStr)
	cmd.Dir = dir
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}

	// Assign to a KILL_ON_CLOSE job so cancel/timeout/app-death reaps the
	// whole pnpm tree (same contract as managedProcess).
	job := newKillOnCloseJob()
	if h, herr := openProcessForJob(cmd.Process.Pid); herr == nil {
		if jerr := job.assign(h); jerr != nil {
			job = nil
		}
	} else {
		job = nil
	}

	marketOpMu.Lock()
	marketOpCmd = cmd
	marketOpJob = job
	marketOpCancel.Store(false)
	marketOpMu.Unlock()

	var (
		buf    strings.Builder
		timed  bool
		doneCh = make(chan struct{})
	)
	scan := func(r *bufio.Scanner) {
		for r.Scan() {
			line := strings.TrimRight(r.Text(), "\r")
			if line == "" {
				continue
			}
			buf.WriteString(line)
			buf.WriteString("\n")
			a.emit("dsh:market-log", map[string]string{"line": line})
		}
	}
	go func() { scan(bufio.NewScanner(stdout)); doneCh <- struct{}{} }()
	go func() { scan(bufio.NewScanner(stderr)); doneCh <- struct{}{} }()

	// Timeout: kill the tree; the command's wait below then resolves.
	timeout := time.AfterFunc(marketOpTimeout, func() {
		timed = true
		marketOpMu.Lock()
		if marketOpJob != nil {
			marketOpJob.close()
		}
		if marketOpCmd != nil && marketOpCmd.Process != nil {
			_ = exec.Command("taskkill", "/PID", fmt.Sprint(marketOpCmd.Process.Pid), "/T", "/F").Run()
		}
		marketOpMu.Unlock()
	})

	waitErr := cmd.Wait()
	timeout.Stop()
	<-doneCh
	<-doneCh

	marketOpMu.Lock()
	marketOpCmd = nil
	marketOpJob = nil
	marketOpMu.Unlock()

	cancelled = marketOpCancel.Load()
	output = strings.TrimRight(buf.String(), "\n")
	if timed {
		return output, false, fmt.Errorf("操作超时（%s）", marketOpTimeout)
	}
	if waitErr != nil {
		if cancelled {
			return output, true, nil
		}
		return output, false, waitErr
	}
	if cancelled {
		return output, true, nil
	}
	return output, false, nil
}

// instanceRunning reports whether an instance has a live managed process.
func (a *App) instanceRunning(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.processes[id]
	return ok
}

// requireStoppedInstance guards install/uninstall: replacing plugin files
// while the instance (and any agent in it) is running risks mixed-state reads.
func (a *App) requireStoppedInstance(id string) error {
	if a.instanceRunning(id) {
		return fmt.Errorf("实例正在运行，插件变更需重启才生效；请先停止该实例（前端会引导）")
	}
	return nil
}

// InstallPlugin installs the curated registry entry (by its url) into the
// profile used by the given instance. The instance must be stopped first.
func (a *App) InstallPlugin(instanceID, entryURL string) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	if err := a.requireStoppedInstance(instanceID); err != nil {
		return nil, err
	}

	a.mu.Lock()
	inst := a.store.find(instanceID)
	a.mu.Unlock()
	if inst == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if err := validateVersion(inst.PkgMgr, inst.Version); err != nil {
		return nil, err
	}

	// Whitelist: the source must be present in the curated catalog right now.
	catalog, err := a.FetchMarketCatalog(false)
	if err != nil {
		return nil, err
	}
	entry := findRegistryEntry(catalog, entryURL)
	if entry == nil {
		return nil, fmt.Errorf("该插件不在社区目录中，无法安装")
	}
	target, ok := installTargetFor(entry.URL, entry.NPM)
	if !ok || !targetRe.MatchString(target) {
		return nil, fmt.Errorf("不支持的插件来源: %s", entry.URL)
	}

	installed, _ := readInstalledPlugins()
	if alias := installedAlias(*entry, installed); alias != "" {
		return nil, fmt.Errorf("该插件已以「%s」安装，无需重复安装", alias)
	}

	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "install", Target: target})
	cmdStr := pluginCommand(*inst, "add", target)
	a.emit("dsh:market-log", map[string]string{"line": "执行: " + cmdStr})

	output, cancelled, runErr := a.runMarketCommand(inst.Directory, cmdStr)
	blocked := parseIgnoredBuilds(output)
	if runErr != nil && !cancelled {
		if len(blocked) > 0 {
			a.emitMarketStatus(MarketOpStatus{
				State: "failed", Kind: "install", Target: target,
				Error:   fmt.Sprintf("构建脚本被拦截: %s", strings.Join(blocked, ", ")),
				Blocked: blocked,
			})
			return &MarketOpResult{OK: false, BlockedBuilds: blocked, Output: output,
				Error: fmt.Sprintf("构建脚本被 pnpm 默认拦截（%s），请放行后重试", strings.Join(blocked, ", "))}, nil
		}
		msg := fmt.Sprintf("安装失败: %v\n%s", runErr, tailLines(output, 8))
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Target: target, Error: msg})
		return &MarketOpResult{OK: false, Output: output, Error: msg}, nil
	}
	if cancelled {
		a.emitMarketStatus(MarketOpStatus{State: "cancelled", Kind: "install", Target: target})
		return &MarketOpResult{OK: false, Cancelled: true, Output: output}, nil
	}

	newNames := diffInstalled(installed)
	a.emitMarketStatus(MarketOpStatus{State: "done", Kind: "install", Target: target})
	return &MarketOpResult{OK: true, Installed: newNames, Output: output}, nil
}

// UninstallPlugin removes an installed plugin via `dsh plugin … remove`.
func (a *App) UninstallPlugin(instanceID, name string) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	if err := a.requireStoppedInstance(instanceID); err != nil {
		return nil, err
	}
	if name == "dsh-market" || name == "dshmarket" {
		return nil, fmt.Errorf("不支持卸载插件市场本身")
	}
	if isInboxBundle(name) {
		return nil, fmt.Errorf("官方基础插件不可卸载")
	}

	installed, _ := readInstalledPlugins()
	if _, ok := installed[name]; !ok {
		return nil, fmt.Errorf("插件未安装: %s", name)
	}

	a.mu.Lock()
	inst := a.store.find(instanceID)
	a.mu.Unlock()
	if inst == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if err := validateVersion(inst.PkgMgr, inst.Version); err != nil {
		return nil, err
	}

	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "uninstall", Target: name})
	cmdStr := pluginCommand(*inst, "remove", name)
	a.emit("dsh:market-log", map[string]string{"line": "执行: " + cmdStr})

	output, cancelled, runErr := a.runMarketCommand(inst.Directory, cmdStr)
	if runErr != nil && !cancelled {
		msg := fmt.Sprintf("卸载失败: %v\n%s", runErr, tailLines(output, 8))
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "uninstall", Target: name, Error: msg})
		return &MarketOpResult{OK: false, Output: output, Error: msg}, nil
	}
	if cancelled {
		a.emitMarketStatus(MarketOpStatus{State: "cancelled", Kind: "uninstall", Target: name})
		return &MarketOpResult{OK: false, Cancelled: true, Output: output}, nil
	}

	// Best-effort: drop the disable row we may have written for this package.
	_ = applyPatchState(name, false)

	a.emitMarketStatus(MarketOpStatus{State: "done", Kind: "uninstall", Target: name})
	return &MarketOpResult{OK: true, Output: output}, nil
}

// ApproveBuilds writes the given packages into the profile's
// pnpm-workspace.yaml allowBuilds block (merge, never clobber), so a retry
// of an install that pnpm blocked can actually build.
func (a *App) ApproveBuilds(names []string) error {
	if len(names) == 0 {
		return nil
	}
	clean := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" && !strings.ContainsAny(n, " \t\r\n") {
			clean = append(clean, n)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	_, err := mergeAllowBuilds(marketProfileDir(), clean)
	return err
}

// diffInstalled returns package names newly present in the profile manifest.
func diffInstalled(before map[string]string) []string {
	after, _ := readInstalledPlugins()
	var out []string
	for name := range after {
		if _, ok := before[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// tailLines returns the last n lines of a (possibly huge) output buffer.
func tailLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return "… " + strings.Join(lines[len(lines)-n:], "\n")
}
