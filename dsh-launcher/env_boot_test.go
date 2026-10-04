package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- env helpers ---------------------------------------------------------

// setForTest sets an env var for the duration of one test and restores the
// previous state afterwards.
func setForTest(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	_ = os.Setenv(key, value)
}

// unsetForTest clears an env var for the duration of one test and restores the
// previous state afterwards.
func unsetForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	_ = os.Unsetenv(key)
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ---- version ordering ----------------------------------------------------

func TestVersionFields(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"v22.14.0", []int{22, 14, 0}},
		{"22.14.0", []int{22, 14, 0}},
		{"22", []int{22}},
		{"v8", []int{8}},
		{"v22.14.0-rc.1", []int{22, 14, 0}},
		{"v22.14.0+sha.abc", []int{22, 14, 0}},
		{" lts/*", nil},
		{"node", nil},
		{"", nil},
		{"v", nil},
	}
	for _, c := range cases {
		got := versionFields(c.in)
		if len(got) != len(c.want) {
			t.Errorf("versionFields(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("versionFields(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestCompareNodeVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// 9.10 > 9.9 —— 字典序会判反，这正是不能用字符串比较的原因
		{"v9.10.0", "v9.9.0", 1},
		{"v9.9.0", "v9.10.0", -1},
		{"v22.14.0", "v9.10.0", 1},
		{"v20.11.1", "v20.11.1", 0},
		{"22.14.0", "v22.14.0", 0},
		// 非 x.y.z 的输入落到字符串比较（nvm 的目录名都是完整版本，构造上不会出现）
		{"v22", "v22.0.0", -1},
		{"v20.11.1-rc.1", "v20.11.1", -1},
		{"v20.11.1", "v20.11.1-rc.1", 1},
	}
	for _, c := range cases {
		if got := compareNodeVersions(c.a, c.b); got != c.want {
			t.Errorf("compareNodeVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPickNodeVersion(t *testing.T) {
	versions := []string{"v9.10.0", "v18.20.4", "v20.11.1", "v22.9.0", "v22.14.0"}
	cases := []struct {
		def  string
		want string
	}{
		{"", "v22.14.0"},         // 没有 alias → 装的最新版
		{"22", "v22.14.0"},       // 数字前缀取该 major 里最新的
		{"v22", "v22.14.0"},      //
		{"22.9", "v22.9.0"},      // major.minor 前缀
		{"v20.11.1", "v20.11.1"}, // 精确
		{"20.11.1", "v20.11.1"},  // 去掉 v 的精确
		{"v18", "v18.20.4"},      //
		{"lts/*", "v22.14.0"},    // 解不开的别名 → 最新
		{"node", "v22.14.0"},     //
		{"v99", "v22.14.0"},      // 前缀没匹配上 → 最新
	}
	for _, c := range cases {
		if got := pickNodeVersion(versions, c.def); got != c.want {
			t.Errorf("pickNodeVersion(%q) = %q, want %q", c.def, got, c.want)
		}
	}
	if got := latestNodeVersion(nil); got != "" {
		t.Errorf("latestNodeVersion(nil) = %q, want empty", got)
	}
}

// ---- nvm layout ----------------------------------------------------------

func TestNvmDefaultAlias(t *testing.T) {
	nvm := filepath.FromSlash("/home/u/.nvm")
	alias := func(name string) string { return filepath.Join(nvm, "alias", name) }

	// alias/default → lts/* → 具体版本：要跟一层
	following := func(p string) (string, error) {
		switch p {
		case alias("default"):
			return "lts/*\n", nil
		case alias("lts/*"):
			return "v20.11.1\n", nil
		}
		return "", os.ErrNotExist
	}
	if got := nvmDefaultAlias(nvm, following); got != "v20.11.1" {
		t.Errorf("跟随 lts/* 失败: %q", got)
	}

	// alias/default 直接写 major 前缀
	direct := func(p string) (string, error) {
		if p == alias("default") {
			return "22\n", nil
		}
		return "", os.ErrNotExist
	}
	if got := nvmDefaultAlias(nvm, direct); got != "22" {
		t.Errorf("major 前缀 alias: %q", got)
	}

	// 自指/循环不能死循环
	looping := func(p string) (string, error) {
		switch p {
		case alias("default"):
			return "stable\n", nil
		case alias("stable"):
			return "stable\n", nil
		}
		return "", os.ErrNotExist
	}
	if got := nvmDefaultAlias(nvm, looping); got != "" {
		t.Errorf("循环 alias 应返回空: %q", got)
	}

	if got := nvmDefaultAlias(nvm, func(string) (string, error) { return "", os.ErrNotExist }); got != "" {
		t.Errorf("没有 alias/default 应返回空: %q", got)
	}
}

func TestNvmBinDir(t *testing.T) {
	nvm := filepath.FromSlash("/home/u/.nvm")
	readDir := func(dir string) ([]string, error) {
		if dir == filepath.Join(nvm, "versions", "node") {
			// "not-a-version" 必须被过滤掉，否则会被当成候选
			return []string{"v22.14.0", "v20.11.1", "not-a-version", "v22.9.0"}, nil
		}
		return nil, os.ErrNotExist
	}
	readFile := func(p string) (string, error) {
		switch p {
		case filepath.Join(nvm, "alias", "default"):
			return "lts/*", nil
		case filepath.Join(nvm, "alias", "lts/*"):
			return "v20.11.1", nil
		}
		return "", os.ErrNotExist
	}
	want := filepath.Join(nvm, "versions", "node", "v20.11.1", "bin")
	if got := nvmBinDir(nvm, readDir, readFile); got != want {
		t.Errorf("nvmBinDir = %q, want %q", got, want)
	}

	// 别名解不开 → 退回装的最新版（v22.14.0 > v22.9.0）
	wantLatest := filepath.Join(nvm, "versions", "node", "v22.14.0", "bin")
	if got := nvmBinDir(nvm, readDir, func(string) (string, error) { return "", os.ErrNotExist }); got != wantLatest {
		t.Errorf("退回最新版失败: %q", got)
	}

	// 没有 versions/node 目录 → 不产出候选
	if got := nvmBinDir(nvm, func(string) ([]string, error) { return nil, os.ErrNotExist }, readFile); got != "" {
		t.Errorf("没有已装版本时应返回空: %q", got)
	}
}

// ---- candidate probing ---------------------------------------------------

func TestCandidateNodeDirs(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	env := map[string]string{"NVM_DIR": filepath.Join(home, ".nvm")}
	getenv := func(k string) string { return env[k] }
	readDir := func(dir string) ([]string, error) {
		if dir == filepath.Join(home, ".nvm", "versions", "node") {
			return []string{"v22.14.0", "v20.11.1"}, nil
		}
		return nil, os.ErrNotExist
	}
	readFile := func(p string) (string, error) {
		if p == filepath.Join(home, ".nvm", "alias", "default") {
			return "20\n", nil
		}
		return "", os.ErrNotExist
	}

	dirs := candidateNodeDirs(home, getenv, readDir, readFile)
	if len(dirs) == 0 {
		t.Fatal("候选目录为空")
	}
	// nvm 的 bin 排第一（对 "" 之后的 PATH 顺序最要紧）
	wantFirst := filepath.Join(home, ".nvm", "versions", "node", "v20.11.1", "bin")
	if dirs[0] != wantFirst {
		t.Errorf("dirs[0] = %q, want %q", dirs[0], wantFirst)
	}
	for _, want := range []string{
		filepath.Join(home, ".local/share/pnpm"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".local/bin"),
		"/usr/local/bin",
	} {
		if !containsStr(dirs, want) {
			t.Errorf("候选里缺 %q：%v", want, dirs)
		}
	}
	if last := dirs[len(dirs)-1]; last != "/usr/bin" {
		t.Errorf("系统目录应排在最后, got %q", last)
	}

	// home 未知、env 也没有 NVM_DIR 时：不能产出空条目（空 PATH 分量 = 当前目录）
	bare := candidateNodeDirs("", func(string) string { return "" }, readDir, readFile)
	for _, d := range bare {
		if strings.TrimSpace(d) == "" {
			t.Errorf("候选里出现了空条目: %v", bare)
		}
	}
	if !containsStr(bare, "/usr/bin") {
		t.Errorf("home 未知时也该保留系统目录: %v", bare)
	}
}

func TestExistingToolDirs(t *testing.T) {
	hasTool := func(dir string) bool { return dir == "/b" || dir == "/d" }
	got := existingToolDirs([]string{"/a", "/b", "/b", "", "/c", "/d"}, hasTool)
	want := []string{"/b", "/d"}
	if len(got) != len(want) {
		t.Fatalf("existingToolDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("existingToolDirs = %v, want %v", got, want)
		}
	}
	if n := len(existingToolDirs([]string{"/nope"}, func(string) bool { return false })); n != 0 {
		t.Errorf("没有命中目录时应返回空, got %d 项", n)
	}
}

func TestDirHasNodeTool(t *testing.T) {
	dir := t.TempDir()
	if dirHasNodeTool(dir) {
		t.Error("空目录不该判为含 node 工具")
	}
	// 同名子目录不算（npm 是文件/软链，不是目录）
	if err := os.Mkdir(filepath.Join(dir, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if dirHasNodeTool(dir) {
		t.Error("名为 npm 的目录不该判为含 node 工具")
	}
	if err := os.Remove(filepath.Join(dir, "npm")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !dirHasNodeTool(dir) {
		t.Error("含 node 文件的目录应判为真")
	}
}

// ---- PATH list handling --------------------------------------------------

func TestSplitAndMergePathList(t *testing.T) {
	got := splitPathList("/usr/bin::/bin:  ", ":")
	if len(got) != 2 || got[0] != "/usr/bin" || got[1] != "/bin" {
		t.Fatalf("splitPathList = %v", got)
	}

	// primary 决定顺序，fallback 只补 primary 里没有的
	merged := mergePathList([]string{"/nvm/bin", "/usr/bin"}, []string{"/gui-only/bin", "/usr/bin", ""})
	want := []string{"/nvm/bin", "/usr/bin", "/gui-only/bin"}
	if len(merged) != len(want) {
		t.Fatalf("mergePathList = %v, want %v", merged, want)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("mergePathList = %v, want %v", merged, want)
		}
	}
	if joinPathList(merged, ":") != "/nvm/bin:/usr/bin:/gui-only/bin" {
		t.Errorf("joinPathList = %q", joinPathList(merged, ":"))
	}

	// pathListDiff：只报真正新加的目录（日志不能说谎）
	diff := pathListDiff([]string{"/nvm/bin", "/usr/bin"}, []string{"/usr/bin", "/bin"})
	if len(diff) != 1 || diff[0] != "/nvm/bin" {
		t.Errorf("pathListDiff = %v, want [/nvm/bin]", diff)
	}
	if got := pathListDiff([]string{"/usr/bin", ""}, []string{"/usr/bin"}); len(got) != 0 {
		t.Errorf("pathListDiff 应为空, got %v", got)
	}
}

func TestResolvesAnyTool(t *testing.T) {
	has := func(dir, tool string) bool { return dir == "/nvm/bin" }
	if !resolvesAnyTool("/usr/bin:/nvm/bin", []string{"npx"}, ":", has) {
		t.Error("应能在第二个目录里找到 npx")
	}
	if resolvesAnyTool("/usr/bin:/bin", []string{"npx", "npm"}, ":", has) {
		t.Error("不该在别的目录里找到工具")
	}
	if resolvesAnyTool("", []string{"npx"}, ":", has) {
		t.Error("空 PATH 不该判为找到")
	}
}

// ---- login shell dump parsing -------------------------------------------

func TestParseEnvDump(t *testing.T) {
	const marker = "MARK"
	allow := []string{"PATH", "NVM_DIR", "PNPM_HOME"}

	cases := []struct {
		name     string
		in       string
		wantPath string
		wantNvm  string
		wantNil  bool
	}{
		{name: "正常", in: "MARKPATH=/a:/b\x00NVM_DIR=/nvm\x00HOME=/root\x00", wantPath: "/a:/b", wantNvm: "/nvm"},
		{name: "rc 杂音在标记之前", in: "bash: no job control\nMARKPATH=/a\x00", wantPath: "/a"},
		{name: "值里含等号", in: "MARKPATH=/a=1\x00", wantPath: "/a=1"},
		{name: "标记紧跟第一个键", in: "MARKPATH=/only\x00", wantPath: "/only"},
		{name: "缺标记", in: "PATH=/a\x00", wantNil: true},
		{name: "缺 PATH", in: "MARKNVM_DIR=/nvm\x00", wantNil: true},
		{name: "空输出", in: "MARK", wantNil: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseEnvDump([]byte(c.in), marker, allow)
			if c.wantNil {
				if got != nil {
					t.Fatalf("parseEnvDump = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("parseEnvDump = nil")
			}
			if got["PATH"] != c.wantPath {
				t.Errorf("PATH = %q, want %q", got["PATH"], c.wantPath)
			}
			if c.wantNvm != "" && got["NVM_DIR"] != c.wantNvm {
				t.Errorf("NVM_DIR = %q, want %q", got["NVM_DIR"], c.wantNvm)
			}
			if _, ok := got["HOME"]; ok {
				t.Error("白名单之外的变量不该被导入")
			}
		})
	}
}

func TestLoginShellArgs(t *testing.T) {
	// dash/sh：-l 读 ~/.profile 优先，-i 读的是 $ENV，所以放第二
	sh := loginShellArgs("/bin/sh", "linux")
	if len(sh) != 2 || sh[0][0] != "-lc" || sh[1][0] != "-ic" {
		t.Fatalf("/bin/sh args = %v", sh)
	}
	// bash/zsh/fish：Linux 终端起的是"交互式非登录"shell，~/.bashrc / ~/.zshrc 里
	// 才有 nvm/fnm 追加的 PATH，所以 -ic 优先、-lic 次之、-lc 兜底
	for _, shell := range []string{"/bin/bash", "/usr/bin/zsh", "/usr/bin/fish"} {
		args := loginShellArgs(shell, "linux")
		if len(args) != 3 {
			t.Fatalf("%s args = %v, want -ic / -lic / -lc 三套", shell, args)
		}
		if args[0][0] != "-ic" || args[1][0] != "-lic" || args[2][0] != "-lc" {
			t.Errorf("linux %s args = %v", shell, args)
		}
		for _, set := range args {
			if !strings.Contains(set[1], envDumpMarker) || !strings.Contains(set[1], "env -0") {
				t.Errorf("%s 的 dump 命令不对: %q", shell, set[1])
			}
		}
	}
	// macOS 终端默认起登录 shell，顺序反过来
	mac := loginShellArgs("/bin/zsh", "darwin")
	if len(mac) != 3 || mac[0][0] != "-lic" || mac[1][0] != "-ic" {
		t.Errorf("darwin args = %v", mac)
	}
}

func TestApplyLoginShellEnv(t *testing.T) {
	const sep = ":"
	hasTool := func(dir, tool string) bool {
		return dir == "/nvm/bin" && (tool == "npx" || tool == "npm")
	}

	// (a) 没帮上忙的 shell：PATH 与任何变量都不许动
	setForTest(t, "PATH", "/usr/bin:/bin")
	unsetForTest(t, "NVM_DIR")
	vars := map[string]string{"PATH": "/opt/junk/bin:/usr/bin", "NVM_DIR": "/nvm"}
	if changed := applyLoginShellEnv(vars, []string{"npx"}, sep, hasTool); len(changed) != 0 {
		t.Fatalf("没帮上忙的 shell 不该改动任何东西: %v", changed)
	}
	if got := os.Getenv("PATH"); got != "/usr/bin:/bin" {
		t.Errorf("PATH = %q, want 原样", got)
	}
	if _, ok := os.LookupEnv("NVM_DIR"); ok {
		t.Error("没帮上忙的 shell 不该导入 NVM_DIR")
	}

	// (b) 帮上忙的 shell：shell 顺序在前，GUI 独有的目录保留在后面
	setForTest(t, "PATH", "/gui-only/bin:/usr/bin")
	unsetForTest(t, "NVM_DIR")
	vars = map[string]string{"PATH": "/nvm/bin:/usr/bin", "NVM_DIR": "/nvm"}
	changed := applyLoginShellEnv(vars, []string{"npx", "npm"}, sep, hasTool)
	if !containsStr(changed, "PATH") || !containsStr(changed, "NVM_DIR") {
		t.Fatalf("changed = %v, want PATH 与 NVM_DIR", changed)
	}
	if got, want := os.Getenv("PATH"), "/nvm/bin:/usr/bin:/gui-only/bin"; got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
	if got := os.Getenv("NVM_DIR"); got != "/nvm" {
		t.Errorf("NVM_DIR = %q, want /nvm", got)
	}

	// (c) 本进程已有的值不被覆盖（终端启动时就是这种情形）
	setForTest(t, "PATH", "/gui-only/bin")
	setForTest(t, "NVM_DIR", "/keep")
	changed = applyLoginShellEnv(map[string]string{"PATH": "/nvm/bin", "NVM_DIR": "/other"}, []string{"npx"}, sep, hasTool)
	if got := os.Getenv("NVM_DIR"); got != "/keep" {
		t.Errorf("NVM_DIR = %q, want /keep（已有值优先）", got)
	}
	if containsStr(changed, "NVM_DIR") {
		t.Error("没有真正改动的变量不该出现在 changed 里")
	}
}

// ---- entry point ---------------------------------------------------------

func TestBootstrapProcessEnv(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专有断言：非 Windows 平台的引导会真的去探测本机环境")
	}
	if got := bootstrapProcessEnv(); got != nil {
		t.Errorf("Windows 上不该做任何引导, got %v", got)
	}
}
