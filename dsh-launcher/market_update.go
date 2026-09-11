package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Plugin update checking + single-plugin update.
//
// Execution deliberately reuses the SAME `dsh plugin --profile web <args>`
// pipeline as install/uninstall (market_ops.go): the official CLI is a thin
// pnpm forwarder whose reconcilePlugins() rebuilds the dsh.profile.bundles
// layer stack from the INSTALLED state, so `update` / `add <name>@latest`
// activates a package that only declared `dsh.bundle` in a newer version.
// Nothing here talks to pnpm directly.
//
// This file owns the read side (is there a newer version, and how big is the
// jump) and the per-plugin write side (UpdatePlugin). Batch update is out of
// scope by decision — see 插件更新功能实现方案.md §12.

// --- semver + range helpers (pure, no network) ---

var (
	// semverCoreRe splits x.y.z out of a version (prerelease/build kept in the
	// remainder by compareSemver's own split).
	semverCoreRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)
	prereleaseRe = regexp.MustCompile(`-([0-9A-Za-z.-]+)`)
)

// compareSemver orders two semver strings: -1 / 0 / 1. A version without a
// prerelease outranks the same x.y.z with one (1.2.3 > 1.2.3-rc.1), and
// prerelease identifiers follow semver: numeric identifiers compare
// numerically and rank below alphanumeric ones.
//
// Unparsable input falls back to a stable string compare so a malformed
// version can never panic or silently report "up to date".
func compareSemver(a, b string) int {
	am := semverCoreRe.FindStringSubmatch(a)
	bm := semverCoreRe.FindStringSubmatch(b)
	if am == nil || bm == nil {
		return strings.Compare(a, b)
	}
	for i := 1; i <= 3; i++ {
		ai, _ := strconv.Atoi(am[i])
		bi, _ := strconv.Atoi(bm[i])
		if ai != bi {
			return sign(ai - bi)
		}
	}
	ap, bp := prereleaseOf(a), prereleaseOf(b)
	if ap == "" && bp == "" {
		return 0
	}
	if ap == "" {
		return 1 // release > prerelease
	}
	if bp == "" {
		return -1
	}
	return comparePrerelease(ap, bp)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// prereleaseOf extracts the prerelease identifier of a version ("" when none,
// and "" when the dash belongs to build metadata such as 1.2.3+build-1).
func prereleaseOf(v string) string {
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	m := prereleaseRe.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return m[1]
}

// comparePrerelease orders two "rc.1" style identifiers (semver §11.4).
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if ai != bi {
				return sign(ai - bi)
			}
		case aerr == nil:
			return -1 // numeric ranks below alphanumeric
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(as) - len(bs))
}

// jumpType classifies the size of a version move for the UI:
// none | patch | minor | major | prerelease | downgrade | unknown.
func jumpType(cur, next string) string {
	if cur == "" || next == "" {
		return "unknown"
	}
	c := compareSemver(cur, next)
	if c == 0 {
		return "none"
	}
	if c > 0 {
		return "downgrade"
	}
	cm := semverCoreRe.FindStringSubmatch(cur)
	nm := semverCoreRe.FindStringSubmatch(next)
	if cm == nil || nm == nil {
		return "unknown"
	}
	// Same x.y.z, different prerelease (0.1.1-rc.1 → 0.1.1-rc.2).
	if cm[1] == nm[1] && cm[2] == nm[2] && cm[3] == nm[3] {
		return "prerelease"
	}
	if cm[1] != nm[1] {
		return "major"
	}
	if cm[2] != nm[2] {
		return "minor"
	}
	return "patch"
}

// npmRange is a parsed npm version range, limited to the shapes plugin
// manifests actually use. anything=false means "we refuse to guess".
type npmRange struct {
	raw       string
	kind      string // any | exact | minor | caret | tilde | gte
	major     int
	minor     int
	patch     int
	anything  bool
	parseable bool
}

// parseNpmRange parses ^x.y.z / ~x.y.z / >=x.y.z / x.y.z / x.y / x / * /
// latest. Deliberately narrow: the npm range grammar is huge, and guessing
// wrong would either nag the user forever or hide a real update.
func parseNpmRange(spec string) npmRange {
	r := npmRange{raw: strings.TrimSpace(spec)}
	s := strings.TrimSpace(spec)
	if s == "" {
		return r
	}
	switch s {
	case "*", "x", "X", "latest":
		r.kind, r.anything, r.parseable = "any", true, true
		return r
	}
	op := ""
	for _, cand := range []string{"^", "~", ">=", ">", "<=", "<", "="} {
		if strings.HasPrefix(s, cand) {
			op = cand
			s = strings.TrimSpace(strings.TrimPrefix(s, cand))
			break
		}
	}
	if strings.ContainsAny(s, "| ") || strings.Contains(s, "||") {
		return r // unions / hyphen ranges: not supported, not guessed
	}
	nums := strings.Split(s, ".")
	if len(nums) > 3 {
		return r
	}
	parts := make([]int, 0, 3)
	wild := false
	for _, n := range nums {
		n = strings.TrimSpace(n)
		if n == "" || n == "x" || n == "X" || n == "*" {
			wild = true
			break
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			return r // tags like `next` / `beta` are not ordered here
		}
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		return r
	}
	r.major = parts[0]
	if len(parts) > 1 {
		r.minor = parts[1]
	}
	if len(parts) > 2 {
		r.patch = parts[2]
	}
	switch {
	case op == "^":
		r.kind, r.parseable = "caret", true
	case op == "~":
		r.kind, r.parseable = "tilde", true
	case op == ">=":
		r.kind, r.parseable = "gte", true
	case op == ">" || op == "<" || op == "<=":
		return r // exclusive comparators: refuse to guess
	case wild:
		r.kind, r.parseable = "minor", true
	case len(parts) == 3:
		r.kind, r.parseable = "exact", true
	case len(parts) == 2:
		r.kind, r.parseable = "minor", true
	default:
		r.kind, r.anything, r.parseable = "any", true, true
	}
	return r
}

// inRange reports whether version satisfies spec. known=false means the spec
// shape is unsupported, so the caller must treat the update as needing
// explicit confirmation (and a spec rewrite) rather than assuming it fits.
func inRange(version, spec string) (ok bool, known bool) {
	r := parseNpmRange(spec)
	if !r.parseable {
		return false, false
	}
	if r.anything {
		return true, true
	}
	m := semverCoreRe.FindStringSubmatch(version)
	if m == nil {
		return false, false
	}
	vMajor, _ := strconv.Atoi(m[1])
	vMinor, _ := strconv.Atoi(m[2])
	vPre := prereleaseOf(version)

	switch r.kind {
	case "exact":
		return version == strings.TrimSpace(strings.TrimPrefix(spec, "=")), true
	case "minor":
		return vMajor == r.major && (r.minor == 0 || vMinor == r.minor), true
	case "caret":
		if vMajor != r.major {
			return false, true
		}
		// ^0.x.y only allows patch-level moves (semver caret for 0.x).
		if r.major == 0 && r.minor != vMinor {
			return false, true
		}
		// A prerelease target never counts as satisfying a plain range.
		return vPre == "", true
	case "tilde":
		if vMajor != r.major || vMinor != r.minor {
			return false, true
		}
		return vPre == "", true
	case "gte":
		return compareSemver(version, fmt.Sprintf("%d.%d.%d", r.major, r.minor, r.patch)) >= 0, true
	}
	return false, false
}

// resolveNpmName extracts the npm package name a manifest spec installs.
// Handles `npm:alias@^1`, `@scope/pkg@^1`, `pkg@1.2.3`, bare names, and
// returns "" for non-registry specs (file:, link:, workspace:, github:, git+).
//
// fallback is the dependency's manifest KEY, used when the spec carries no
// name at all — a hand-written `"pkg": "^1.2.3"` (range-only spec) is valid
// npm but the registry name only exists in the key.
func resolveNpmName(spec string, fallback ...string) string {
	s := strings.TrimSpace(spec)
	if s == "" {
		return npmNameFallback(fallback)
	}
	for _, bad := range []string{"file:", "link:", "workspace:", "github:", "git+", "http:", "https:", "portal:"} {
		if strings.HasPrefix(s, bad) {
			return ""
		}
	}
	if strings.HasPrefix(s, "npm:") {
		s = strings.TrimPrefix(s, "npm:")
	}
	if strings.HasPrefix(s, "@") {
		i := strings.Index(s, "/")
		if i < 0 {
			return npmNameFallback(fallback)
		}
		rest := s[i+1:]
		if j := strings.Index(rest, "@"); j >= 0 {
			rest = rest[:j]
		}
		name := s[:i] + "/" + rest
		if npmNameRe.MatchString(name) {
			return name
		}
		return npmNameFallback(fallback)
	}
	if i := strings.Index(s, "@"); i > 0 {
		s = s[:i]
	}
	if npmNameRe.MatchString(s) {
		return s
	}
	// Anything else (range-only spec such as `^1.2.3`, `~1.0.0`, `1.x`, `*`):
	// the registry name only exists in the dependency key.
	return npmNameFallback(fallback)
}

// npmNameFallback returns the dependency key when it is a plausible npm name.
func npmNameFallback(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	k := strings.TrimSpace(keys[0])
	if npmNameRe.MatchString(k) {
		return k
	}
	return ""
}

// --- pnpm-lock.yaml pinned commit (written now, wired to GitHub HEAD in M3) ---

var codeloadCommitRe = regexp.MustCompile(`codeload\.github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/tar\.gz/([0-9a-fA-F]{40})`)

// readLockedCommit scans the profile's pnpm-lock.yaml for the commit a GitHub
// dependency is pinned to. Pure text scan (no YAML library, matching the
// patch-layer editing style in market_installed.go). Returns "" when the
// plugin is not a git dependency or the lock file is missing/corrupt.
func readLockedCommit(repo string) string {
	repo = strings.ToLower(strings.TrimSpace(repo))
	if repo == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(marketProfileDir(), "pnpm-lock.yaml"))
	if err != nil {
		return ""
	}
	for _, m := range codeloadCommitRe.FindAllStringSubmatch(string(data), -1) {
		if strings.ToLower(m[1]) == repo {
			return strings.ToLower(m[2])
		}
	}
	return ""
}

// --- registry lookup ---

// marketRegistryBase / marketRegistryMirror are vars so tests can point the
// lookups at an httptest server.
var (
	marketRegistryBase   = "https://registry.npmjs.org/"
	marketRegistryMirror = "https://registry.npmmirror.com/"
)

// fetchDistTagLatest reads dist-tags.latest for one package. Official registry
// first, mirror as fallback (same two-source policy as dsh_query.go).
func (a *App) fetchDistTagLatest(name string) (string, error) {
	client := a.proxyHTTPClient(15 * time.Second)
	var lastErr error
	for _, base := range []string{marketRegistryBase, marketRegistryMirror} {
		if strings.TrimSpace(base) == "" {
			continue
		}
		latest, err := fetchDistTagFrom(client, base, name)
		if err == nil {
			return latest, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no registry source available")
	}
	return "", lastErr
}

func fetchDistTagFrom(client *http.Client, base, name string) (string, error) {
	url := strings.TrimRight(base, "/") + "/" + strings.ReplaceAll(name, "/", "%2F")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dsh-launcher/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("包 %s 在 npm 上不存在（可能已下架）", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry responded %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var doc struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("解析 registry 响应失败: %w", err)
	}
	latest := strings.TrimSpace(doc.DistTags["latest"])
	if latest == "" {
		return "", fmt.Errorf("registry 未返回 latest dist-tag")
	}
	return latest, nil
}

// embeddedBuiltinVersion reads the version of the plugin bundled in the
// launcher binary (single source of truth: the embedded package.json).
func embeddedBuiltinVersion() string {
	data, err := embeddedSelfRestart.ReadFile(embeddedSelfRestartRoot + "/package.json")
	if err != nil {
		return ""
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Version)
}

// materializedBuiltinVersion reads the version already materialized into the
// profile's .dsh-builtin directory ("" when never installed).
func materializedBuiltinVersion() string {
	data, err := os.ReadFile(filepath.Join(marketProfileDir(), selfRestartBuiltinRel, "package.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Version)
}

// --- update check API ---

// UpdateCheck is the availability verdict for one installed plugin.
type UpdateCheck struct {
	Name    string `json:"name"`
	Current string `json:"current"` // installed version (read from node_modules)
	Latest  string `json:"latest"`  // npm dist-tags.latest / embedded builtin version
	Kind    string `json:"kind"`    // npm | github | linked | builtin
	// HasUpdate means a newer version exists AND this kind can act on it.
	HasUpdate bool `json:"hasUpdate"`
	// Runnable is false for kinds this release cannot update (github / builtin).
	Runnable bool   `json:"runnable"`
	Jump     string `json:"jump"`    // patch | minor | major | prerelease | downgrade | none | unknown
	InRange  bool   `json:"inRange"` // fits the spec's range → plain `pnpm update`
	Risky    bool   `json:"risky"`   // needs explicit confirmation (major / out of range / prerelease)
	Target   string `json:"target"`  // pnpm target derived server-side (never trusted from the client)
	Remote   string `json:"remote"`  // npm name or owner/repo, for display
	Err      string `json:"err"`     // per-item failure; other rows are unaffected
}

// UpdateCheckResult is the whole installed set with per-plugin verdicts.
type UpdateCheckResult struct {
	Checked   string        `json:"checked"` // RFC3339
	Plugins   []UpdateCheck `json:"plugins"`
	Updatable int           `json:"updatable"`
}

const (
	updateCheckTTL = 5 * time.Minute
	// updateCheckConcurrency bounds registry fan-out (a profile typically has
	// a handful of plugins; 8 keeps a cold check well under a second).
	updateCheckConcurrency = 8
)

var (
	updateCacheMu sync.Mutex
	updateCache   *UpdateCheckResult
	updateCacheAt time.Time
)

// CheckPluginUpdates returns one verdict per installed plugin. force bypasses
// the 5-minute cache (the UI's explicit 「检查更新」 button).
//
// A failure on one plugin never fails the whole call: the row carries Err and
// the installed list keeps rendering (offline is a normal state, not an error
// page).
func (a *App) CheckPluginUpdates(force bool) (*UpdateCheckResult, error) {
	updateCacheMu.Lock()
	if !force && updateCache != nil && time.Since(updateCacheAt) < updateCheckTTL {
		cached := *updateCache
		updateCacheMu.Unlock()
		return &cached, nil
	}
	updateCacheMu.Unlock()

	installed, err := readInstalledPlugins()
	if err != nil {
		return nil, err
	}

	// Stable order: build the result rows up front, fill them concurrently.
	names := make([]string, 0, len(installed))
	for name := range installed {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]UpdateCheck, len(names))
	builtinLatest := embeddedBuiltinVersion()
	builtinCurrent := materializedBuiltinVersion()

	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, updateCheckConcurrency)
		mu  sync.Mutex
	)
	for i, name := range names {
		spec := installed[name]
		row := UpdateCheck{
			Name:    name,
			Current: readInstalledVersion(name),
			Kind:    pluginKind(spec),
			Target:  name,
		}
		switch {
		case name == selfRestartPluginName:
			// Launcher-bundled plugin: the comparison needs no network at all.
			row.Kind = "builtin"
			row.Latest = builtinLatest
			row.Remote = name
			row.Current = builtinCurrent
			row.Runnable = false
			if builtinLatest != "" && builtinCurrent != "" {
				row.HasUpdate = compareSemver(builtinCurrent, builtinLatest) < 0
				row.Jump = jumpType(builtinCurrent, builtinLatest)
			}
		case row.Kind == "linked":
			// A file:/link: dependency is the user's working checkout.
			row.Kind = "linked"
			row.Remote = ""
		case row.Kind == "github":
			row.Kind = "github"
			row.Remote = repoOf(githubURLFromSpec(spec))
		default:
			// npm: network work, one request per package.
			npmName := resolveNpmName(spec, name)
			if npmName == "" {
				row.Err = "无法从 spec 识别 npm 包名: " + spec
				break
			}
			row.Remote = npmName
			row.Target = npmName
			rows[i] = row // publish the row before the goroutine fills it
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, npmName, spec string) {
				defer wg.Done()
				defer func() { <-sem }()
				latest, ferr := a.fetchDistTagLatest(npmName)
				mu.Lock()
				defer mu.Unlock()
				if ferr != nil {
					rows[idx].Err = ferr.Error()
					return
				}
				rows[idx].Latest = latest
				fillNpmVerdict(&rows[idx], spec, latest)
			}(i, npmName, spec)
			continue
		}
		rows[i] = row
	}
	wg.Wait()

	result := &UpdateCheckResult{
		Checked: time.Now().Format(time.RFC3339),
		Plugins: rows,
	}
	for i := range rows {
		if rows[i].HasUpdate && rows[i].Runnable {
			result.Updatable++
		}
	}

	updateCacheMu.Lock()
	updateCache = result
	updateCacheAt = time.Now()
	updateCacheMu.Unlock()
	return result, nil
}

// fillNpmVerdict computes HasUpdate / Jump / InRange / Risky for an npm row.
// Runnable is false when the registry is BEHIND the local install (a pinned
// prerelease or a dist-tag rollback) — offering "update" there would be a
// downgrade in disguise.
func fillNpmVerdict(row *UpdateCheck, spec, latest string) {
	row.Jump = jumpType(row.Current, latest)
	row.HasUpdate = compareSemver(row.Current, latest) < 0
	row.Runnable = row.HasUpdate
	if !row.HasUpdate {
		return
	}
	inR, known := inRange(latest, spec)
	row.InRange = inR
	// Unknown range shape (aliases, ranges we refuse to guess) is treated as
	// out-of-range: the update then rewrites the spec, and the UI confirms.
	if !known {
		row.InRange = false
	}
	row.Risky = !row.InRange || row.Jump == "major" || row.Jump == "prerelease"
}

// UpdatePlugin updates one installed plugin to the newest npm version.
//
// allowRisky gates the destructive shapes (major bump / spec rewrite): the
// frontend asks for confirmation and only then retries with allowRisky=true,
// so a stray click can never rewrite a user's pin or cross a major boundary.
func (a *App) UpdatePlugin(instanceID, name string, allowRisky bool) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("插件名不能为空")
	}
	if isInboxBundle(name) {
		return nil, fmt.Errorf("官方基础插件不参与更新")
	}
	if err := a.requireStoppedInstance(instanceID); err != nil {
		return nil, err
	}

	installed, err := readInstalledPlugins()
	if err != nil {
		return nil, err
	}
	spec, ok := installed[name]
	if !ok {
		return nil, fmt.Errorf("插件未安装: %s", name)
	}

	kind := pluginKind(spec)
	switch {
	case name == selfRestartPluginName || kind == "builtin":
		return nil, fmt.Errorf("内置插件更新将在后续版本支持（当前可先「卸载」再「安装到全局」）")
	case kind == "linked":
		return nil, fmt.Errorf("本地开发插件（%s）不支持自动更新，请在你的源码目录自行更新", spec)
	case kind == "github":
		return nil, fmt.Errorf("git 来源插件请在「发现」页重新安装以拉取最新提交")
	}

	npmName := resolveNpmName(spec, name)
	if npmName == "" {
		return nil, fmt.Errorf("无法从 spec 识别 npm 包名: %s", spec)
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

	// Fresh verdict for THIS plugin: decides the command shape and gives the
	// UI the exact version span to show in the drawer.
	result, err := a.CheckPluginUpdates(true)
	if err != nil {
		return nil, err
	}
	cur := findUpdateCheck(result, name)
	if cur == nil || cur.Err != "" {
		msg := "检查更新失败，无法确定目标版本"
		if cur != nil && cur.Err != "" {
			msg = cur.Err
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if !cur.HasUpdate {
		return &MarketOpResult{OK: false, Already: true, Error: fmt.Sprintf("%s 已是最新版本 v%s", name, cur.Current)}, nil
	}
	if cur.Risky && !allowRisky {
		return nil, fmt.Errorf("该更新需要确认（%s）", riskyReason(*cur))
	}

	from, to := cur.Current, cur.Latest
	status := MarketOpStatus{
		State: "running", Kind: "update", Target: npmName,
		Name: name, From: from, To: to,
	}
	a.emitMarketStatus(status)
	if pl := a.proxyLogLine(); pl != "" {
		a.emit("dsh:market-log", map[string]string{"line": pl})
	}

	var cmdStr string
	if cur.InRange {
		// Inside the declared range: pnpm update moves the lockfile without
		// touching package.json (the user's spec pin is preserved).
		cmdStr = pluginCommand(*inst, "update", npmName)
	} else {
		// Out of range / major: rewrite the spec to the new latest. `add`
		// also re-reconciles the bundles layer stack.
		cmdStr = pluginCommand(*inst, "add", npmName+"@latest")
	}
	a.emit("dsh:market-log", map[string]string{
		"line": fmt.Sprintf("更新 %s: v%s → v%s（%s）", name, from, to, cur.Jump),
	})
	a.emit("dsh:market-log", map[string]string{"line": "执行: " + cmdStr})

	output, cancelled, runErr := a.runMarketCommand(inst.Directory, cmdStr)
	blocked := parseIgnoredBuilds(output)
	status.Blocked = blocked
	switch {
	case cancelled:
		status.State = "cancelled"
		a.emitMarketStatus(status)
		return &MarketOpResult{OK: false, Cancelled: true, Output: output}, nil
	case runErr != nil:
		if len(blocked) > 0 {
			status.State, status.Error = "failed", fmt.Sprintf("构建脚本被拦截: %s", strings.Join(blocked, ", "))
			a.emitMarketStatus(status)
			return &MarketOpResult{OK: false, BlockedBuilds: blocked, Output: output,
				Error: fmt.Sprintf("构建脚本被 pnpm 默认拦截（%s），请放行后重试", strings.Join(blocked, ", "))}, nil
		}
		msg := fmt.Sprintf("更新失败: %v\n%s", runErr, tailLines(output, 8))
		status.State, status.Error = "failed", msg
		a.emitMarketStatus(status)
		return &MarketOpResult{OK: false, Output: output, Error: msg}, nil
	}

	status.State = "done"
	a.emitMarketStatus(status)
	return &MarketOpResult{OK: true, Installed: []string{name}, Output: output}, nil
}

// riskyReason explains why an update needs confirmation, for the error string
// and the frontend's dialog copy.
func riskyReason(c UpdateCheck) string {
	switch {
	case c.Jump == "major":
		return fmt.Sprintf("主版本升级 v%s → v%s", c.Current, c.Latest)
	case c.Jump == "prerelease":
		return fmt.Sprintf("预发布版本 v%s → v%s", c.Current, c.Latest)
	default:
		return fmt.Sprintf("超出当前 spec 声明的范围（v%s → v%s），将改写 package.json", c.Current, c.Latest)
	}
}

// findUpdateCheck returns the row for a package name (nil when absent).
func findUpdateCheck(result *UpdateCheckResult, name string) *UpdateCheck {
	if result == nil {
		return nil
	}
	for i := range result.Plugins {
		if result.Plugins[i].Name == name {
			return &result.Plugins[i]
		}
	}
	return nil
}
