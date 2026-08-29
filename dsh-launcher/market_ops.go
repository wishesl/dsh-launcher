package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
		killProcessTree(marketOpCmd.Process.Pid, marketOpCmd)
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
	Already       bool     `json:"already"` // already installed — not a real failure
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

	// pnpm >= 11 rejects `name@range` allowBuilds keys with
	// ERR_PNPM_INVALID_VERSION_UNION, which breaks EVERY install/uninstall in
	// the profile. Self-heal the profile's pnpm-workspace.yaml before invoking
	// pnpm so a key written by pnpm's own approve-builds can't wedge the market.
	if sanitizeAllowBuilds(marketProfileDir()) {
		a.emit("dsh:market-log", map[string]string{
			"line": "已修复 pnpm-workspace.yaml 中不兼容的 allowBuilds 版本键（pnpm 11）",
		})
	}

	cmd := shellCommand(context.Background(), cmdStr)
	cmd.Dir = dir
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	// Route pnpm's registry downloads through the configured proxy (if any);
	// without this, plugin installs on a restricted network time out.
	a.applyProxyToCmd(cmd)

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
			killProcessTree(marketOpCmd.Process.Pid, marketOpCmd)
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

// preflightMarketOp is the shared guard for install/uninstall: the instance
// must exist, be stopped (plugin files must not be replaced under a live
// process), and its pinned dsh version must be usable.
func (a *App) preflightMarketOp(instanceID string) (*Instance, error) {
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
	return inst, nil
}

// runInstall executes the shared install flow for an already-validated target:
// fast-start status → streamed command output → blocked-builds / failure /
// cancel / done handling. Callers own the marketBusy single-flight flag and
// the target derivation/validation.
func (a *App) runInstall(inst *Instance, target string) (*MarketOpResult, error) {
	before, _ := readInstalledPlugins()
	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "install", Target: target})
	cmdStr := pluginCommand(*inst, "add", target)
	a.emit("dsh:market-log", map[string]string{"line": "执行: " + cmdStr})
	if pl := a.proxyLogLine(); pl != "" {
		a.emit("dsh:market-log", map[string]string{"line": pl})
	}

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

	newNames := diffInstalled(before)
	a.emitMarketStatus(MarketOpStatus{State: "done", Kind: "install", Target: target})
	return &MarketOpResult{OK: true, Installed: newNames, Output: output}, nil
}

// InstallPlugin installs the curated registry entry (by its url) into the
// profile used by the given instance. The instance must be stopped first.
func (a *App) InstallPlugin(instanceID, entryURL string) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	inst, err := a.preflightMarketOp(instanceID)
	if err != nil {
		return nil, err
	}

	// Fast-start feedback: flip the frontend to "running" BEFORE the slow
	// catalog revalidation, so the button disables and the drawer shows
	// progress immediately instead of sitting silent for seconds on a slow
	// network (the previous "点安装没反应" symptom).
	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "install"})
	a.emit("dsh:market-log", map[string]string{"line": "正在校验插件来源…"})

	// Whitelist: the source must be present in the curated catalog right now.
	catalog, err := a.FetchMarketCatalog(false)
	if err != nil {
		msg := "获取插件目录失败，请检查网络后在设置中配置镜像源: " + err.Error()
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Error: msg})
		return &MarketOpResult{OK: false, Error: msg}, nil
	}
	entry := findRegistryEntry(catalog, entryURL)
	if entry == nil {
		msg := "该插件不在社区目录中，无法安装"
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Error: msg})
		return &MarketOpResult{OK: false, Error: msg}, nil
	}
	target, ok := installTargetFor(entry.URL, entry.NPM)
	if !ok || !targetRe.MatchString(target) {
		msg := "不支持的插件来源: " + entry.URL
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Error: msg})
		return &MarketOpResult{OK: false, Error: msg}, nil
	}

	installed, _ := readInstalledPlugins()
	if alias := installedAlias(*entry, installed); alias != "" {
		// Already installed — a soft state, not a failure. Return it
		// structurally so the frontend can say "已安装" instead of "安装失败".
		msg := fmt.Sprintf("该插件已以「%s」安装", alias)
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Target: alias, Error: msg})
		return &MarketOpResult{OK: false, Already: true, Error: msg}, nil
	}

	return a.runInstall(inst, target)
}

// InstallFavorite installs a locally-favorited plugin. Unlike InstallPlugin it
// does NOT require the entry to still be in the curated catalog (favorites
// must keep working offline and after an entry is unpublished/renamed): the
// stored install target is re-validated against the shell-safe whitelist, and
// the catalog (when reachable) is used only for an informational cross-check.
func (a *App) InstallFavorite(instanceID string, fav FavoritePlugin) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	inst, err := a.preflightMarketOp(instanceID)
	if err != nil {
		return nil, err
	}

	fav.Install = strings.TrimSpace(fav.Install)
	if !validateInstallSpec(fav.Install) {
		return nil, fmt.Errorf("收藏的安装来源无效: %s", fav.Install)
	}

	// Fast-start feedback (same as InstallPlugin) before any catalog work.
	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "install", Target: fav.Install})
	a.emit("dsh:market-log", map[string]string{"line": "正在校验插件来源…"})

	// Informational cross-check only, and only against the ALREADY-CACHED
	// catalog (never a network fetch — an offline install must start
	// immediately, not wait out a fetch timeout). No cache → no hint.
	if fav.URL != "" {
		if c := cachedCatalogData(); c != nil && findRegistryEntry(c, fav.URL) == nil {
			a.emit("dsh:market-log", map[string]string{
				"line": "该插件已不在社区目录中，仍按收藏快照安装",
			})
		}
	}

	return a.runInstall(inst, fav.Install)
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
	if pl := a.proxyLogLine(); pl != "" {
		a.emit("dsh:market-log", map[string]string{"line": pl})
	}

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

	// Best-effort: drop every disable trace (entry-id rows, package-name rows,
	// dsh-market's persisted list) so a reinstall starts enabled.
	clearPluginDisabled(name)
	// Drop the plugin's 适用 scope too — a reinstall restarts with 全部实例.
	a.scope.delete(name)

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
