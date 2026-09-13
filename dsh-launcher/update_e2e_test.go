package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUpdateAgainstRealRelease 是**可选的真机端到端**：打真实的 GitHub API、下真实的资产、
// 用真实的 SHA256SUMS 校验、再真正替换一次 exe —— 只不过"被替换的那个 exe"是临时目录里的
// 一份拷贝，不是当前运行的程序（绝不能把测试二进制或用户装的启动器换掉）。
//
// 默认跳过（会走网络、拉 12 MB），要跑就显式打开：
//
//	DSH_UPDATE_E2E=1 go test -run TestUpdateAgainstRealRelease -v ./...
//
// 需要的环境：能访问 api.github.com（必要时先配好 HTTPS_PROXY），且线上存在一个
// **比 currentVersion 更新、且带 SHA256SUMS** 的 Release。
func TestUpdateAgainstRealRelease(t *testing.T) {
	if os.Getenv("DSH_UPDATE_E2E") == "" {
		t.Skip("默认跳过真机端到端；设 DSH_UPDATE_E2E=1 显式开启")
	}

	// 装成"上一个版本"，这样线上最新版会被判定为有更新。
	// 用 DSH_UPDATE_E2E_FROM 指定起始版本（默认 0.5.1）—— 每次发新版后拿"上一个已发布版本"
	// 跑一遍，验证的正是用户从旧版升上来的那条真实路径。
	currentVersion := os.Getenv("DSH_UPDATE_E2E_FROM")
	if currentVersion == "" {
		currentVersion = "0.5.1"
	}
	if _, err := parseVersion(currentVersion); err != nil {
		t.Fatalf("DSH_UPDATE_E2E_FROM 不是合法版本号: %q", currentVersion)
	}

	tmp := t.TempDir()
	// 假启动器按当前平台的真实安装形态造：Windows/Linux 是裸文件，macOS 是 .app 包。
	self := selfPathForTest(t, tmp)

	oldVersion := version
	oldSelf := updateSelfPath
	version = currentVersion
	updateSelfPath = func() (string, error) { return self, nil }
	t.Cleanup(func() {
		version = oldVersion
		updateSelfPath = oldSelf
		updCache.Lock()
		updCache.release, updCache.etag, updCache.at = nil, "", time.Time{}
		updCache.Unlock()
	})

	a := &App{
		settings: &settingsStore{path: filepath.Join(tmp, "settings.json")},
		store:    &instanceStore{path: filepath.Join(tmp, "instances.json")},
	}
	a.settings.setUpdateSettings(true, false, "", "")
	// 网络受限时（github.com 的 Release 下载在国内经常连不上）用设置里的代理再跑：
	// 这正好走的是用户在「设置 → 网络代理」里填地址的那条真实路径。
	if p := os.Getenv("DSH_UPDATE_E2E_PROXY"); p != "" {
		if err := a.SetProxy(p); err != nil {
			t.Fatalf("代理地址不合法: %v", err)
		}
		t.Logf("走代理 %s", p)
	}

	rel := a.CheckLauncherUpdate(true)
	t.Logf("当前 %s → 最新 %s（err=%q supported=%v asset=%s size=%d）",
		rel.Current, rel.Latest, rel.Err, rel.Supported, rel.Asset.Name, rel.Asset.Size)
	if rel.Err != "" {
		t.Fatalf("真实 GitHub 检查失败（网络/限流？）: %s", rel.Err)
	}
	if !rel.HasUpdate {
		t.Skipf("线上还没有比 %s 更新的版本，跳过替换验证", currentVersion)
	}
	if !rel.Supported {
		t.Fatalf("线上 Release 应支持应用内更新，实际: %s", rel.SupportNote)
	}

	if err := a.DownloadLauncherUpdate(); err != nil {
		t.Fatalf("真实下载/校验/替换失败: %v", err)
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("替换后读不到启动器本体: %v", err)
	}
	if bytes.Equal(got, []byte(oldSelfBody)) {
		t.Fatal("启动器没有被替换")
	}
	if len(got) == 0 {
		t.Fatal("替换后的启动器是空的")
	}

	// 独立核对：不复用被测代码的下载/解包路径，用自己的 HTTP 客户端把**资产本体**下下来算哈希。
	client := e2eHTTPClient()
	kind := updatePayloadKind(rel.Asset.Name)
	var checked int
	if kind == "raw" {
		// Windows：落地的东西就是资产本体，直接对替换后的文件算哈希。
		sum := sha256.Sum256(got)
		if err := verifyAgainstRealSums(t, client, rel.Asset.Name, hex.EncodeToString(sum[:])); err != nil {
			t.Fatalf("独立校验失败: %v", err)
		}
		if int64(len(got)) != rel.Asset.Size {
			t.Fatalf("替换后的文件大小 %d 与 Release 资产 %d 不一致", len(got), rel.Asset.Size)
		}
		checked = len(got)
		t.Logf("✅ 真机链路通过：%s → %s（裸二进制），替换后 %d 字节，sha256 %s…",
			rel.Current, rel.Latest, len(got), hex.EncodeToString(sum[:])[:12])
	} else {
		// Linux(.tar.gz) / macOS(.zip)：核对**压缩包**的哈希（解包正确性由单测覆盖），
		// 并确认落地出来的内容非空。
		archiveSum, n, err := downloadAndHash(t, client, rel.Asset.URL)
		if err != nil {
			t.Fatalf("独立下载资产失败: %v", err)
		}
		if n != rel.Asset.Size {
			t.Fatalf("独立下载大小 %d 与 Release 声明 %d 不一致", n, rel.Asset.Size)
		}
		if err := verifyAgainstRealSums(t, client, rel.Asset.Name, archiveSum); err != nil {
			t.Fatalf("独立校验失败: %v", err)
		}
		if int64(len(got)) == rel.Asset.Size {
			t.Logf("提示：解包后的本体大小恰好等于压缩包大小（%d），属巧合", len(got))
		}
		checked = len(got)
		t.Logf("✅ 真机链路通过：%s → %s（%s），压缩包 sha256 %s…，解包后 %d 字节",
			rel.Current, rel.Latest, kind, archiveSum[:12], len(got))
	}
	if v := a.UpdateAppliedVersion(); v != rel.Latest {
		t.Errorf("已就位版本 = %q, want %q", v, rel.Latest)
	}
	_ = checked
}

// downloadAndHash 用自己的客户端流式下载并算 sha256（不落盘、不复用产品代码的下载器）。
func downloadAndHash(t *testing.T, client *http.Client, url string) (string, int64, error) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(h, resp.Body)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// e2eHTTPClient 是复核用的客户端：同样走 DSH_UPDATE_E2E_PROXY（受限网络下必须如此）。
func e2eHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if p := os.Getenv("DSH_UPDATE_E2E_PROXY"); p != "" {
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: tr}
}

// verifyAgainstRealSums 直接问 GitHub 要 SHA256SUMS（不复用被测代码的下载路径），
// 用它独立核对替换后的文件内容。
func verifyAgainstRealSums(t *testing.T, client *http.Client, assetName, gotHash string) error {
	t.Helper()
	rel := fetchReleaseDocForTest(t, client)
	var sumsURL string
	for _, as := range rel.Assets {
		if as.Name == sumsAssetName {
			sumsURL = as.URL
		}
	}
	if sumsURL == "" {
		t.Fatalf("Release 上没有 %s", sumsAssetName)
	}
	resp, err := client.Get(sumsURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	want := parseSums(string(body), assetName)
	if want == "" {
		t.Fatalf("%s 里没有 %s", sumsAssetName, assetName)
	}
	if !bytes.Equal([]byte(want), []byte(gotHash)) {
		t.Fatalf("哈希不一致：Release=%s 本地=%s", want, gotHash)
	}
	return nil
}

func fetchReleaseDocForTest(t *testing.T, client *http.Client) *ghRelease {
	t.Helper()
	resp, err := client.Get(updateAPIBase + "/repos/" + defaultUpdateRepo + "/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatal(err)
	}
	return &rel
}
