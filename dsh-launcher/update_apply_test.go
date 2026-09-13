package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ---------- 解包 ----------

// makeTarGz 打一个 tar.gz：members 是 "名字 → 内容"。
func makeTarGz(t *testing.T, members map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "asset.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// zipEntry 描述一个 zip 条目（支持目录/文件/符号链接）。
type zipEntry struct {
	name string
	body string
	dir  bool
	link string
	mode os.FileMode
}

func makeZip(t *testing.T, entries []zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.dir:
			hdr.SetMode(os.ModeDir | 0o755)
		case e.link != "":
			hdr.SetMode(os.ModeSymlink | 0o777)
		default:
			mode := e.mode
			if mode == 0 {
				mode = 0o644
			}
			hdr.SetMode(mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		body := e.body
		if e.link != "" {
			body = e.link
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "asset.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUpdatePayloadKind(t *testing.T) {
	cases := map[string]string{
		"dsh-launcher-windows-amd64.exe":  "raw",
		"dsh-launcher-linux-amd64.tar.gz": "targz",
		"dsh-launcher-darwin-arm64.zip":   "zip",
		"weird.TGZ":                       "targz",
	}
	for name, want := range cases {
		if got := updatePayloadKind(name); got != want {
			t.Errorf("updatePayloadKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestExtractTarGzBinary(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"README.md":    "无关文件",
		"dsh-launcher": "ELF-BINARY",
	})
	dest := t.TempDir()
	got, err := extractTarGzBinary(archive, dest, "dsh-launcher")
	if err != nil {
		t.Fatalf("extractTarGzBinary: %v", err)
	}
	body, err := os.ReadFile(got)
	if err != nil || string(body) != "ELF-BINARY" {
		t.Fatalf("解出来的内容不对: %q err=%v", body, err)
	}
	// Windows 上观察不到 POSIX 权限位（FS 不存），只在类 Unix 上断言执行位。
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(got); st == nil || st.Mode().Perm()&0o111 == 0 {
			t.Errorf("裸二进制必须带可执行位, mode=%v", st.Mode())
		}
	}
	// 无关文件不该被解出来
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Error("只该解出目标文件")
	}
	// 包里没有目标文件 → 报错
	if _, err := extractTarGzBinary(makeTarGz(t, map[string]string{"other": "x"}), t.TempDir(), "dsh-launcher"); err == nil {
		t.Error("包里没有目标文件时应报错")
	}
}

func TestExtractZipAppBundle(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{name: "dsh-launcher.app/", dir: true},
		{name: "dsh-launcher.app/Contents/", dir: true},
		{name: "dsh-launcher.app/Contents/Info.plist", body: "<plist/>"},
		{name: "dsh-launcher.app/Contents/MacOS/", dir: true},
		{name: "dsh-launcher.app/Contents/MacOS/dsh-launcher", body: "MACHO", mode: 0o755},
		{name: "无关目录/别的文件", body: "x"},
	})
	dest := t.TempDir()
	appDir, err := extractZipPayload(archive, dest)
	if err != nil {
		t.Fatalf("extractZipPayload: %v", err)
	}
	if filepath.Base(appDir) != "dsh-launcher.app" {
		t.Fatalf("应解出 .app 包, got %s", appDir)
	}
	exe := filepath.Join(appDir, "Contents", "MacOS", "dsh-launcher")
	body, err := os.ReadFile(exe)
	if err != nil || string(body) != "MACHO" {
		t.Fatalf("包里的可执行文件不对: %q err=%v", body, err)
	}
	// Windows 上观察不到 POSIX 权限位，只在类 Unix 上断言。
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(exe); st.Mode().Perm()&0o111 == 0 {
			t.Errorf("可执行位没保留: %v", st.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "无关目录")); !os.IsNotExist(err) {
		t.Error("包外的条目不该解出来")
	}
}

func TestExtractZipRestoresSymlinksAndExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要特权，跳过链接断言（解包逻辑本身与平台无关）")
	}
	archive := makeZip(t, []zipEntry{
		{name: "dsh-launcher.app/Contents/MacOS/dsh-launcher", body: "MACHO", mode: 0o644}, // 故意不带执行位
		{name: "dsh-launcher.app/Contents/Frameworks/Current", link: "A"},
	})
	dest := t.TempDir()
	appDir, err := extractZipPayload(archive, dest)
	if err != nil {
		t.Fatalf("extractZipPayload: %v", err)
	}
	link := filepath.Join(appDir, "Contents", "Frameworks", "Current")
	if target, err := os.Readlink(link); err != nil || target != "A" {
		t.Fatalf("符号链接没还原: target=%q err=%v", target, err)
	}
	exe := filepath.Join(appDir, "Contents", "MacOS", "dsh-launcher")
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o111 == 0 {
		t.Errorf("包里的可执行文件应被补上执行位: %v", st.Mode())
	}
}

func TestExtractZipSingleBinaryFallback(t *testing.T) {
	archive := makeZip(t, []zipEntry{{name: "dsh-launcher", body: "ELF", mode: 0o755}})
	dest := t.TempDir()
	got, err := extractZipPayload(archive, dest)
	if err != nil {
		t.Fatalf("没有 .app 时应退化成单个可执行文件: %v", err)
	}
	if body, _ := os.ReadFile(got); string(body) != "ELF" {
		t.Fatalf("内容不对: %q", body)
	}
}

// TestArchivePathTraversalRefused 是安全底线：压缩包里的 ../ 绝不能把文件写到目标目录之外。
func TestArchivePathTraversalRefused(t *testing.T) {
	tarPath := makeTarGz(t, map[string]string{"../evil": "x", "dsh-launcher": "ELF"})
	if _, err := extractTarGzBinary(tarPath, t.TempDir(), "dsh-launcher"); err == nil {
		t.Error("tar 里的 ../ 必须被拒绝")
	}

	zipPath := makeZip(t, []zipEntry{
		{name: "dsh-launcher.app/", dir: true},
		{name: "dsh-launcher.app/../../evil", body: "x"},
	})
	if _, err := extractZipPayload(zipPath, t.TempDir()); err == nil {
		t.Error("zip 里的 ../ 必须被拒绝")
	}
}

func TestSafeArchivePath(t *testing.T) {
	ok := []string{"dsh-launcher", "a/b/c", "./x/y"}
	bad := []string{"", ".", "..", "../x", "a/../../b", "/abs/path", "C:/win"}
	for _, p := range ok {
		if !safeArchivePath(p) {
			t.Errorf("safeArchivePath(%q) 应放行", p)
		}
	}
	for _, p := range bad {
		if safeArchivePath(p) {
			t.Errorf("safeArchivePath(%q) 应拒绝", p)
		}
	}
}

// ---------- 落地（macOS 整包） ----------

func TestAppBundleRoot(t *testing.T) {
	// 路径按各平台的分隔符构造：macOS 上是 POSIX，Windows 上跑这条用例只是测"逐级上溯找 .app"。
	cases := map[string]string{
		filepath.FromSlash("/Applications/DSH Launcher.app/Contents/MacOS/dsh-launcher"): filepath.FromSlash("/Applications/DSH Launcher.app"),
		filepath.FromSlash("/tmp/x/dsh-launcher.app/Contents/MacOS/dsh-launcher"):        filepath.FromSlash("/tmp/x/dsh-launcher.app"),
		filepath.FromSlash("/usr/local/bin/dsh-launcher"):                                "", // 不在包里
	}
	for in, want := range cases {
		if got := appBundleRoot(in); got != want {
			t.Errorf("appBundleRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeBundle 造一个"正在运行的应用包"，返回包根与其中可执行文件路径。
func fakeBundle(t *testing.T, root, name, body string) (string, string) {
	t.Helper()
	bundle := filepath.Join(root, name)
	exe := filepath.Join(bundle, "Contents", "MacOS", "dsh-launcher")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle, exe
}

func TestApplyUpdateBundleReplacesKeepingName(t *testing.T) {
	root := t.TempDir()
	bundle, exe := fakeBundle(t, root, "DSH Launcher.app", "OLD")
	stagedRoot := t.TempDir()
	staged, _ := fakeBundle(t, stagedRoot, "dsh-launcher.app", "NEW")

	if err := applyUpdateBundle(bundle, staged); err != nil {
		t.Fatalf("applyUpdateBundle: %v", err)
	}
	// 包名沿用原来的（用户可能改过名），内容是新版
	if body, err := os.ReadFile(exe); err != nil || string(body) != "NEW" {
		t.Fatalf("替换后包内可执行文件应为新版: %q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, "dsh-launcher.app")); !os.IsNotExist(err) {
		t.Error("不该多出一个新名字的包")
	}
}

func TestApplyUpdateBundleRollsBack(t *testing.T) {
	root := t.TempDir()
	bundle, exe := fakeBundle(t, root, "DSH Launcher.app", "OLD")

	// staged 不存在 → 失败，原包必须原样回来
	if err := applyUpdateBundle(bundle, filepath.Join(root, "nope.app")); err == nil {
		t.Fatal("staged 不存在时应报错")
	}
	if body, err := os.ReadFile(exe); err != nil || string(body) != "OLD" {
		t.Fatalf("回滚失败：包内应还是旧版: %q err=%v", body, err)
	}
	if _, err := os.Stat(bundle + ".old"); !os.IsNotExist(err) {
		t.Error("回滚后不该留下 .old")
	}
}

func TestApplyUpdateDispatchesByBundle(t *testing.T) {
	root := t.TempDir()
	bundle, exe := fakeBundle(t, root, "DSH Launcher.app", "OLD")
	stagedRoot := t.TempDir()
	staged, _ := fakeBundle(t, stagedRoot, "dsh-launcher.app", "NEW")

	if err := applyUpdate(exe, staged, "darwin"); err != nil {
		t.Fatalf("applyUpdate(darwin): %v", err)
	}
	if body, _ := os.ReadFile(exe); string(body) != "NEW" {
		t.Fatalf("darwin 应走整包替换, got %q", body)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("包目录不该消失: %v", err)
	}
}

func TestApplyUpdateLinuxRawBinary(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, "dsh-launcher")
	if err := os.WriteFile(self, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "dsh-launcher")
	if err := os.WriteFile(staged, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyUpdate(self, staged, "linux"); err != nil {
		t.Fatalf("applyUpdate(linux): %v", err)
	}
	if body, _ := os.ReadFile(self); string(body) != "NEW" {
		t.Fatalf("linux 应就地替换裸二进制, got %q", body)
	}
}

// stageUpdatePayloadForTest 走一遍"按资产名解包"，供集成测试与真机 E2E 复用。
func stageUpdatePayloadForTest(t *testing.T, assetPath, workDir, goos string) string {
	t.Helper()
	got, err := stageUpdatePayload(assetPath, workDir, goos)
	if err != nil {
		t.Fatalf("stageUpdatePayload(%s): %v", goos, err)
	}
	return got
}
