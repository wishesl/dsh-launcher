package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ---------------------------------------------------------------------------
// 启动器自更新（应用内升级）
//
// 闭环：检查 GitHub Releases（只读）→ 在线下载 → sha256 校验 → 就地替换运行中的
// exe → 默认"下次启动生效"（另给「立即重启生效」）。
//
// 三条硬约束（都是踩过的坑，别改回去）：
//  1. **fail-open**：查不到（断网 / 限流 / 接口变了）只写进 LauncherRelease.Err，前端当"没有结论"
//     中性展示，绝不拦别的功能，也绝不报红（AGENTS.md §9）。
//  2. **没有 SHA256SUMS 就不装**：宁可让用户走「打开 Release 页」，也不用没校验的 exe。
//  3. **就地替换靠"重命名运行中的 exe"**（本机 spike 验证过）：覆盖会被 Windows 拒绝，
//     但 rename 允许，且老进程会继续正常跑完自己这一条命。
// ---------------------------------------------------------------------------

const (
	// defaultUpdateRepo 是发布源。可在设置里覆盖（fork / 私有镜像场景）。
	defaultUpdateRepo = "wishesl/dsh-launcher"
	// 检查结果缓存：匿名 GitHub API 只有 60 次/小时/IP，绝不能每次开窗都打。
	// （名字带 launcher：market_update.go 里已有一个插件市场的 updateCheckTTL。）
	launcherCheckTTL = 10 * time.Minute
	// 元数据请求超时；下载另给一个长超时（大文件 + 慢代理）。
	updateAPITimeout      = 20 * time.Second
	updateDownloadTimeout = 20 * time.Minute
	// sumsAssetName 是 CI（release.yml）产出的校验和资产名。
	sumsAssetName = "SHA256SUMS"
)

// ghAsset / ghRelease 是 GitHub Releases API 的子集（只取我们用的字段）。
type ghAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt string    `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []ghAsset `json:"assets"`
}

// updateAPIBase 是 GitHub API 基地址 —— 变量而非常量：自更新全链路测试要把它指向
// httptest 的假 GitHub（真机行为不受影响）。
var updateAPIBase = "https://api.github.com"

// updateSelfPath 返回"要被替换的那个 exe"。生产是 os.Executable()；测试里换成临时文件，
// 免得单测把测试二进制自己给替换掉。
var updateSelfPath = func() (string, error) { return os.Executable() }

// UpdateAssetRef 是"本平台该下哪个文件"。
type UpdateAssetRef struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// LauncherRelease 是启动器**自己**的版本视图（与 DSH 的 RegistryInfo 无关）。
//
// 三态在这里就分好了：Err 非空 = 没有结论（前端中性展示）；Comparable=false = 本地 dev 构建，
// 不参与比较；Supported=false = 本平台/本 Release 不能应用内更新（给下载页兜底）。
type LauncherRelease struct {
	Current     string         `json:"current"`
	Latest      string         `json:"latest"`
	HasUpdate   bool           `json:"hasUpdate"`
	Comparable  bool           `json:"comparable"`
	Prerelease  bool           `json:"prerelease"`
	Name        string         `json:"name"`
	Notes       string         `json:"notes"`
	PublishedAt string         `json:"publishedAt"`
	HTMLURL     string         `json:"htmlUrl"`
	Asset       UpdateAssetRef `json:"asset"`
	Supported   bool           `json:"supported"`
	SupportNote string         `json:"supportNote"`
	CheckedAt   string         `json:"checkedAt"`
	Cached      bool           `json:"cached"`
	// Skipped = 用户点过「跳过此版本」。
	Skipped bool   `json:"skipped"`
	Err     string `json:"err"`

	// sumsURL 不暴露给前端（Wails 不序列化非导出字段）：校验和资产的地址，
	// 只在下一次下载时用，因为"哪个资产是校验和"只有 API 响应知道。
	sumsURL string
}

// UpdateStatus 走 dsh:update-status：弹窗的进度条 + 状态机。
type UpdateStatus struct {
	State   string `json:"state"` // running | done | failed | cancelled
	Phase   string `json:"phase"` // check | download | verify | apply
	Percent int    `json:"percent"`
	Bytes   int64  `json:"bytes"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
}

// UpdateSettings 是「版本升级」相关的偏好（存 settings.json）。
type UpdateSettings struct {
	AutoCheck         bool   `json:"autoCheck"`
	IncludePrerelease bool   `json:"includePrerelease"`
	SkippedVersion    string `json:"skippedVersion"`
	SourceRepo        string `json:"sourceRepo"`
}

// ---- 进程内状态 ----

var (
	updCache struct {
		sync.Mutex
		release        *LauncherRelease
		etag           string
		etagPrerelease bool // etag 属于哪条端点：两个端点的 ETag 不能混用
		at             time.Time
	}

	updateBusy     atomic.Bool
	updateApplied  atomic.Bool  // 本次运行内已完成替换（前端可恢复"已就位"）
	appliedVersion atomic.Value // string
	updateMu       sync.Mutex
	updateCancelFn context.CancelFunc
)

// ---------------------------------------------------------------------------
// 绑定方法
// ---------------------------------------------------------------------------

// GetUpdateSettings 返回当前更新偏好（AutoCheck 默认 true）。
func (a *App) GetUpdateSettings() UpdateSettings {
	auto := true
	repo := ""
	inc := false
	skip := ""
	if a != nil && a.settings != nil {
		d := a.settings.get()
		if d.AutoCheckUpdate != nil {
			auto = *d.AutoCheckUpdate
		}
		repo = d.UpdateSourceRepo
		inc = d.UpdateIncludePrerelease
		skip = d.UpdateSkippedVersion
	}
	return UpdateSettings{AutoCheck: auto, IncludePrerelease: inc, SkippedVersion: skip, SourceRepo: repo}
}

// SetUpdateSettings 保存更新偏好。sourceRepo 必须是 owner/repo（空 = 官方源）。
func (a *App) SetUpdateSettings(s UpdateSettings) error {
	repo := strings.Trim(strings.TrimSpace(s.SourceRepo), "/")
	if repo != "" {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(repo, " \t") {
			return fmt.Errorf("更新源要写成 owner/repo（例如 wishesl/dsh-launcher）")
		}
	}
	if a.settings != nil {
		a.settings.setUpdateSettings(s.AutoCheck, s.IncludePrerelease, strings.TrimSpace(s.SkippedVersion), repo)
	}
	return nil
}

// UpdateOpRunning 报告是否正在下载/安装更新（前端挂载时用它恢复状态）。
func (a *App) UpdateOpRunning() bool { return updateBusy.Load() }

// UpdateAppliedVersion 返回本次运行内已就位的版本（"" = 没有）。
func (a *App) UpdateAppliedVersion() string {
	if updateApplied.Load() {
		if v, ok := appliedVersion.Load().(string); ok {
			return v
		}
	}
	return ""
}

// OpenReleasePage 打开 Release 页（不支持的平台 / 权限不足 / 用户主动查看时的兜底）。
// 只打开我们自己仓库的地址，不接受前端传 URL。
func (a *App) OpenReleasePage() {
	url := "https://github.com/" + a.updateRepo() + "/releases"
	updCache.Lock()
	if updCache.release != nil && updCache.release.HTMLURL != "" {
		url = updCache.release.HTMLURL
	}
	updCache.Unlock()
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, url)
	}
}

// CheckLauncherUpdate 查 GitHub Releases 并给出"要不要更新"的结论。
//
// 这是**只读**操作：不接受 error 返回值 —— 所有失败都写进 LauncherRelease.Err，前端只有一条渲染路径
// （三态：有更新 / 已是最新 / 没有结论）。force=true 表示用户点了「检查更新」（绕过 10 分钟 TTL，
// 但仍然带 If-None-Match：304 不计入 GitHub 限流）。
func (a *App) CheckLauncherUpdate(force bool) LauncherRelease {
	cur := strings.TrimPrefix(strings.TrimSpace(version), "v")
	out := LauncherRelease{Current: cur, CheckedAt: time.Now().Format(time.RFC3339)}
	if _, err := parseVersion(cur); err == nil {
		out.Comparable = true
	}

	st := a.GetUpdateSettings()

	updCache.Lock()
	cached := updCache.release
	fresh := cached != nil && time.Since(updCache.at) < launcherCheckTTL
	etag := updCache.etag
	etagPre := updCache.etagPrerelease
	updCache.Unlock()

	if !force && fresh {
		res := *cached
		res.Cached = true
		res.CheckedAt = out.CheckedAt
		return res
	}
	// ETag 只对本端点有效：切换"含预发布"后必须重新拉取。
	if etagPre != st.IncludePrerelease {
		etag = ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateAPITimeout)
	defer cancel()
	client := a.proxyHTTPClient(updateAPITimeout)

	rel, newETag, status, err := fetchLatestRelease(ctx, client, a.updateRepo(), st.IncludePrerelease, etag)
	if err != nil {
		out.Err = "查询 GitHub 失败：" + err.Error()
		return out
	}
	if status == http.StatusNotModified && cached != nil {
		res := *cached
		res.Cached = true
		res.CheckedAt = out.CheckedAt
		updCache.Lock()
		updCache.at = time.Now()
		updCache.Unlock()
		return res
	}
	if status != http.StatusOK {
		out.Err = ghStatusError(status)
		return out
	}

	res := buildLauncherRelease(rel, cur, runtime.GOOS, runtime.GOARCH, st.SkippedVersion)
	res.CheckedAt = out.CheckedAt
	if res.Err == "" {
		updCache.Lock()
		updCache.release = &res
		updCache.etag = newETag
		updCache.etagPrerelease = st.IncludePrerelease
		updCache.at = time.Now()
		updCache.Unlock()
	}
	return res
}

// DownloadLauncherUpdate 下载 → 校验 → 就地替换（不重启）。
//
// 完成后的语义是"已就位"：当前进程继续用旧版本跑，用户下次打开启动器就是新版。
// 想立刻生效由前端再调 RestartLauncherNow()。
func (a *App) DownloadLauncherUpdate() error {
	if !updateBusy.CompareAndSwap(false, true) {
		return fmt.Errorf("已有更新任务在进行中")
	}
	defer updateBusy.Store(false)

	a.emitUpdateStatus(UpdateStatus{State: "running", Phase: "check"})

	rel := a.CheckLauncherUpdate(true)
	a.updateLog("当前版本 " + rel.Current + "，线上最新 " + orDash(rel.Latest))
	if rel.Err != "" {
		return a.failUpdate("check", rel.Err+a.netHint())
	}
	if !rel.Comparable {
		return a.failUpdate("check", "本地构建（未注入版本号）不参与自动更新，请用「打开 Release 页」")
	}
	if !rel.HasUpdate {
		return a.failUpdate("check", "已经是最新版本 v"+rel.Current)
	}
	if !rel.Supported {
		msg := rel.SupportNote
		if msg == "" {
			msg = "当前平台不支持应用内更新，请用「打开 Release 页」手动下载"
		}
		return a.failUpdate("check", msg)
	}
	if rel.Asset.URL == "" {
		return a.failUpdate("check", "该 Release 没有适配本平台的产物")
	}
	if rel.sumsURL == "" {
		return a.failUpdate("check", "该 Release 未提供 "+sumsAssetName+"，无法校验完整性 —— 请用「打开 Release 页」手动下载")
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
	updateMu.Lock()
	updateCancelFn = cancel
	updateMu.Unlock()
	defer func() {
		updateMu.Lock()
		updateCancelFn = nil
		updateMu.Unlock()
		cancel()
	}()

	dir := a.updatesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return a.failUpdate("download", "无法创建下载目录："+err.Error())
	}
	part := filepath.Join(dir, rel.Asset.Name+".part")
	tmp := filepath.Join(dir, rel.Asset.Name)

	a.updateLog("目标资产 " + rel.Asset.Name + "（" + humanSize(rel.Asset.Size) + "）")
	a.updateLog("下载中 → " + part)
	a.emitUpdateStatus(UpdateStatus{State: "running", Phase: "download", Total: rel.Asset.Size})

	client := a.proxyHTTPClient(updateDownloadTimeout)
	got, err := downloadToFile(ctx, client, rel.Asset.URL, part, rel.Asset.Size, func(written, total int64) {
		pct := 0
		if total > 0 {
			pct = int(written * 100 / total)
		}
		a.emitUpdateStatus(UpdateStatus{State: "running", Phase: "download", Percent: pct, Bytes: written, Total: total})
	})
	if err != nil {
		_ = os.Remove(part)
		if ctx.Err() != nil {
			a.emitUpdateStatus(UpdateStatus{State: "cancelled", Phase: "download"})
			a.updateLog("已取消")
			return fmt.Errorf("已取消")
		}
		return a.failUpdate("download", err.Error()+a.netHint())
	}
	a.updateLog("下载完成 " + humanSize(int64(lenOfFile(part))) + "，sha256 " + shortHash(got))

	// ---- 校验 ----
	a.emitUpdateStatus(UpdateStatus{State: "running", Phase: "verify", Percent: 100})
	a.updateLog("读取 " + sumsAssetName + " 校验和…")
	want, err := fetchExpectedSum(ctx, client, rel.sumsURL, rel.Asset.Name)
	if err != nil {
		_ = os.Remove(part)
		return a.failUpdate("verify", err.Error())
	}
	if !strings.EqualFold(want, got) {
		_ = os.Remove(part)
		return a.failUpdate("verify", "sha256 校验不匹配（下载可能被截断或被篡改），已丢弃该文件")
	}
	a.updateLog("sha256 校验通过")

	if err := os.Rename(part, tmp); err != nil {
		_ = os.Remove(part)
		return a.failUpdate("verify", "无法保存安装包："+err.Error())
	}

	// ---- 替换 ----
	a.emitUpdateStatus(UpdateStatus{State: "running", Phase: "apply", Percent: 100})
	self, err := updateSelfPath()
	if err != nil {
		return a.failUpdate("apply", "无法定位启动器自身："+err.Error())
	}
	a.updateLog("就地替换 " + self)
	if err := applyUpdateExecutable(self, tmp); err != nil {
		return a.failUpdate("apply", err.Error())
	}
	_ = os.Remove(tmp) // 装完就删安装包；删不掉（被占用）也无所谓，下次启动会清

	updateApplied.Store(true)
	appliedVersion.Store(rel.Latest)
	a.updateLog("已就位：退出启动器后，下次打开即为 v" + rel.Latest)
	a.emitUpdateStatus(UpdateStatus{State: "done", Phase: "apply", Percent: 100})
	return nil
}

// CancelLauncherUpdate 取消正在进行的下载（与 CancelMarketOp 同构）。
func (a *App) CancelLauncherUpdate() bool {
	updateMu.Lock()
	defer updateMu.Unlock()
	if updateCancelFn == nil {
		return false
	}
	updateCancelFn()
	return true
}

// RestartLauncherNow 启动新版本并退出当前进程（「立即重启生效」）。
//
// 注意：退出会走 App.shutdown() → 停掉所有启动器托管的 DSH 实例（前端必须二次确认）。
// 新进程带 --updated-from=<pid>，在 wails.Run 之前等我们退出 —— 否则会被
// SingleInstanceLock 当成"第二实例"直接 os.Exit(0)（见 single_instance_windows.go）。
func (a *App) RestartLauncherNow() error {
	if !updateApplied.Load() {
		return fmt.Errorf("还没有已就位的更新，请先「下载并安装」")
	}
	self, err := updateSelfPath()
	if err != nil {
		return fmt.Errorf("无法定位启动器自身：%w", err)
	}
	cmd := exec.Command(self, "--updated-from="+strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(self)
	cmd.SysProcAttr = newSysProcAttr() // GUI 子系统，别给子进程开控制台
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动新版本：%w", err)
	}
	a.updateLog("已启动新版本，退出当前进程…")
	go func() {
		time.Sleep(400 * time.Millisecond)
		a.requestQuit()
	}()
	return nil
}

// ---------------------------------------------------------------------------
// 检查（纯函数 + 一次 HTTP）
// ---------------------------------------------------------------------------

func (a *App) updateRepo() string {
	if a != nil && a.settings != nil {
		if r := strings.Trim(strings.TrimSpace(a.settings.get().UpdateSourceRepo), "/"); r != "" {
			return r
		}
	}
	return defaultUpdateRepo
}

func (a *App) updatesDir() string {
	base := "."
	if a != nil && a.store != nil && a.store.path != "" {
		base = filepath.Dir(a.store.path)
	}
	return filepath.Join(base, "updates")
}

// fetchLatestRelease 拉 /releases/latest，或（含预发布时）从 /releases 列表里挑版本最高的一条。
// 返回 HTTP 状态码：304 时 rel 为 nil，调用方用缓存。
func fetchLatestRelease(ctx context.Context, client *http.Client, repo string, includePrerelease bool, etag string) (*ghRelease, string, int, error) {
	if !includePrerelease {
		body, newETag, status, err := ghGet(ctx, client, updateAPIBase+"/repos/"+repo+"/releases/latest", etag)
		if err != nil || status != http.StatusOK {
			return nil, newETag, status, err
		}
		var rel ghRelease
		if err := json.Unmarshal(body, &rel); err != nil {
			return nil, newETag, status, fmt.Errorf("无法解析 GitHub 响应: %w", err)
		}
		return &rel, newETag, status, nil
	}

	// 预发布渠道：列表端点，跳过 draft，取版本号最高的一条。
	body, _, status, err := ghGet(ctx, client, updateAPIBase+"/repos/"+repo+"/releases?per_page=20", "")
	if err != nil || status != http.StatusOK {
		return nil, "", status, err
	}
	var list []ghRelease
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, "", status, fmt.Errorf("无法解析 GitHub 响应: %w", err)
	}
	var best *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || strings.TrimSpace(r.TagName) == "" {
			continue
		}
		if best == nil || compareSemver(strings.TrimPrefix(r.TagName, "v"), strings.TrimPrefix(best.TagName, "v")) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, "", http.StatusNotFound, nil
	}
	return best, "", status, nil
}

func ghGet(ctx context.Context, client *http.Client, url, etag string) ([]byte, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dsh-launcher/"+strings.TrimSpace(version))
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return body, resp.Header.Get("ETag"), resp.StatusCode, nil
}

func ghStatusError(status int) string {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "GitHub 接口限流或拒绝访问（匿名 60 次/小时），请稍后再试"
	case http.StatusNotFound:
		return "更新源不存在（检查设置里的 owner/repo）"
	default:
		return fmt.Sprintf("GitHub 返回 %d", status)
	}
}

// buildLauncherRelease 把一次 API 响应折成前端要的结论（纯函数，便于单测）。
func buildLauncherRelease(rel *ghRelease, current, goos, goarch, skipped string) LauncherRelease {
	out := LauncherRelease{
		Current:     current,
		Latest:      strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v"),
		Prerelease:  rel.Prerelease,
		Name:        rel.Name,
		Notes:       strings.TrimSpace(rel.Body),
		PublishedAt: rel.PublishedAt,
		HTMLURL:     rel.HTMLURL,
	}
	if _, err := parseVersion(current); err == nil {
		out.Comparable = true
		out.HasUpdate = compareSemver(out.Latest, current) > 0
	}
	out.Skipped = skipped != "" && skipped == out.Latest

	appliable, note := updateAppliable(goos)
	if name, ok := updateAssetName(goos, goarch); ok {
		for _, as := range rel.Assets {
			if as.Name == name {
				out.Asset = UpdateAssetRef{Name: as.Name, Size: as.Size, URL: as.URL}
			}
			if as.Name == sumsAssetName {
				out.sumsURL = as.URL
			}
		}
	}
	switch {
	case !appliable:
		out.Supported = false
		out.SupportNote = note
	case out.Asset.URL == "":
		out.Supported = false
		out.SupportNote = "该 Release 没有适配 " + goos + "/" + goarch + " 的产物，请手动下载"
	case out.sumsURL == "":
		out.Supported = false
		out.SupportNote = "该 Release 未提供 " + sumsAssetName + "，无法校验完整性，请手动下载"
	default:
		out.Supported = true
	}
	return out
}

// updateAssetName 把平台映射到 release.yml 里的资产名（命名是 CI 的硬约定）。
func updateAssetName(goos, goarch string) (string, bool) {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return "dsh-launcher-windows-amd64.exe", true
	case "darwin/arm64":
		return "dsh-launcher-darwin-arm64.zip", true
	case "darwin/amd64":
		return "dsh-launcher-darwin-amd64.zip", true
	case "linux/amd64":
		return "dsh-launcher-linux-amd64.tar.gz", true
	}
	return "", false
}

// updateAppliable 报告本平台能否"就地替换"（M2 只做 Windows；其它平台走下载页）。
func updateAppliable(goos string) (bool, string) {
	if goos == "windows" {
		return true, ""
	}
	return false, "当前平台暂不支持应用内更新，请用「打开 Release 页」手动下载"
}

// ---------------------------------------------------------------------------
// 下载 / 校验 / 替换
// ---------------------------------------------------------------------------

// downloadToFile 流式下载到 dest，边写边算 sha256，返回十六进制摘要。
// 大小不符（截断）直接报错 —— 校验和之外的第二道闸。
func downloadToFile(ctx context.Context, client *http.Client, url, dest string, expect int64, onProgress func(written, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "dsh-launcher/"+strings.TrimSpace(version))
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
	}
	total := expect
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("无法写入 %s：%w", dest, err)
	}
	hasher := sha256.New()
	pw := &progressWriter{total: total, emit: onProgress}
	written, err := io.Copy(io.MultiWriter(f, hasher, pw), resp.Body)
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("下载中断：%w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("写入失败：%w", closeErr)
	}
	if pw.emit != nil {
		pw.emit(written, total)
	}
	if expect > 0 && written != expect {
		return "", fmt.Errorf("下载大小不符（期望 %d 字节，实际 %d 字节）", expect, written)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// fetchExpectedSum 取 SHA256SUMS 里对应资产的哈希。缺失即报错（没有校验就不装）。
func fetchExpectedSum(ctx context.Context, client *http.Client, url, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "dsh-launcher/"+strings.TrimSpace(version))
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("无法获取 %s：%w", sumsAssetName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("无法获取 %s：HTTP %d", sumsAssetName, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败：%w", sumsAssetName, err)
	}
	sum := parseSums(string(body), assetName)
	if sum == "" {
		return "", fmt.Errorf("%s 里没有 %s 的哈希，无法校验完整性", sumsAssetName, assetName)
	}
	return sum, nil
}

// parseSums 解析 `sha256sum` 输出（"<hex>  <name>"），返回 assetName 对应的十六进制摘要。
func parseSums(body, assetName string) string {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		// sha256sum 输出形如 "<hash>  <name>"；某些工具会带 "*" 前缀标记二进制模式。
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName || filepath.Base(name) == assetName {
			if len(fields[0]) == 64 {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

// applyUpdateExecutable 用 newFile 就地替换正在运行的 self。
//
// Windows 允许重命名运行中的 exe（这不是"覆盖"，所以不会被拒），于是：
//
//	self → self.old，再把新文件写到 self 的路径上。
//
// 老进程继续跑完自己这一条命，下次启动就是新版本；self.old 由启动时的清理删掉
// （老进程还活着时删不掉，这是正常的，不是失败）。
func applyUpdateExecutable(self, newFile string) error {
	dir := filepath.Dir(self)
	if err := ensureWritableDir(dir); err != nil {
		return fmt.Errorf("启动器所在目录不可写（%s）—— 请用「打开 Release 页」手动替换", dir)
	}
	old := self + ".old"
	_ = os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		return fmt.Errorf("无法重命名正在运行的启动器（%w）—— 请用「打开 Release 页」手动替换", err)
	}
	if err := copyFile(newFile, self); err != nil {
		// 回滚：绝不允许留下"原路径没有 exe"的状态。
		if rbErr := os.Rename(old, self); rbErr != nil {
			return fmt.Errorf("写入新版本失败且回滚失败（%v / %v）—— 原程序在 %s，请手动改回", err, rbErr, old)
		}
		return fmt.Errorf("写入新版本失败（已回滚到原版本）：%w", err)
	}
	_ = os.Remove(old) // 老进程还占着它时删不掉 → 留给下次启动
	return nil
}

// ensureWritableDir 预检目录可写（Program Files / 只读介质 / 需要提权的场景提前拦住）。
func ensureWritableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".dsh-launcher-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// cleanupUpdateLeftovers 在启动时清理上一次自更新留下的 .old 与半截 .part。
// 只做 best-effort：删不掉不是错误（老进程可能还没退干净）。
func cleanupUpdateLeftovers() {
	// exe 旁边的 .old：上一个进程退出后这里才有权删。
	if self, err := os.Executable(); err == nil {
		_ = os.Remove(self + ".old")
	}
	// 下载缓存目录在配置目录下（可能与 exe 不同盘），单独扫一遍。
	if dir, err := configDirPath(); err == nil {
		upd := filepath.Join(dir, "updates")
		entries, err := os.ReadDir(upd)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".old") {
				_ = os.Remove(filepath.Join(upd, e.Name()))
			}
		}
	}
}

// configDirPath 返回 %APPDATA%\DSHLauncher（与 instances.json / settings.json 同层）。
func configDirPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", fmt.Errorf("无法定位配置目录")
	}
	return filepath.Join(dir, "DSHLauncher"), nil
}

// updatedFromPID 解析 --updated-from=<pid>（自更新重启时由老进程传入）。
func updatedFromPID(args []string) int {
	for _, a := range args {
		if strings.HasPrefix(a, "--updated-from=") {
			if pid, err := strconv.Atoi(strings.TrimPrefix(a, "--updated-from=")); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// 事件 / 小工具
// ---------------------------------------------------------------------------

func (a *App) emitUpdateStatus(s UpdateStatus) {
	a.emit("dsh:update-status", s)
}

func (a *App) updateLog(line string) {
	a.emit("dsh:update-log", map[string]string{"line": line})
}

// failUpdate 统一把失败写进日志 + 状态事件，并返回 error 给前端（前端只弹 toast，正文在弹窗日志里）。
func (a *App) failUpdate(phase, msg string) error {
	a.updateLog("✗ " + msg)
	a.emitUpdateStatus(UpdateStatus{State: "failed", Phase: phase, Error: msg})
	return fmt.Errorf("%s", msg)
}

// netHint 在没配代理时补一句人话。真机实测过：受限网络里 api.github.com 往往能通，
// 但 releases/download 的资产下载会直接超时 —— 用户看到的只是一句 "wsarecv: ..."，猜不到是网络。
func (a *App) netHint() string {
	if a.proxyURL() != "" {
		return ""
	}
	return "（当前没有配置网络代理；受限网络下可在「设置 → 网络代理」填好代理后重试，或用「打开 Release 页」手动下载）"
}

type progressWriter struct {
	total   int64
	written int64
	last    time.Time
	emit    func(written, total int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	if w.emit != nil && time.Since(w.last) >= 200*time.Millisecond {
		w.last = time.Now()
		w.emit(w.written, w.total)
	}
	return n, nil
}

func humanSize(n int64) string {
	if n <= 0 {
		return "未知大小"
	}
	const mb = 1024 * 1024
	if n >= mb {
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1024)
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "…"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// lenOfFile 只用于日志展示（失败时返回 0，不影响主流程）。
func lenOfFile(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// 版本比较复用 market_update.go 的 compareSemver（semver 规则 + 预发布排序 + 不可解析时
// 退化成字符串比较，永不 panic），这里不再重复实现一份。
