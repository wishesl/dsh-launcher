package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePnpmScript 是一个假 pnpm：把每次调用记进 calls.log，并在记录时附带"此刻
// pnpm-workspace.yaml 里有没有 allowBuilds"，这样测试能直接断言**顺序**。
// FAKE_PNPM_FAIL_ADD=1 时让 `add` 以 pnpm 11 的 ignored-builds 错误退出（exit 1）。
const fakePnpmScript = `import { appendFileSync, existsSync, readFileSync } from "node:fs";
import path from "node:path";
const args = process.argv.slice(2);
const yaml = path.join(process.cwd(), "pnpm-workspace.yaml");
const hasAllow = existsSync(yaml) && readFileSync(yaml, "utf8").includes("allowBuilds");
appendFileSync(process.env.FAKE_PNPM_LOG, args[0] + " allowBuilds=" + String(hasAllow) + "\n");
if (args[0] === "add" && process.env.FAKE_PNPM_FAIL_ADD === "1") {
	console.error("[ERR_PNPM_IGNORED_BUILDS] Ignored build scripts: koffi@3.1.1, node-pty@1.2.0-beta.15");
	console.error('Run "pnpm approve-builds" to pick which dependencies should be allowed to run scripts.');
	process.exit(1);
}
console.log("fake pnpm ok");
`

// TestInstallApprovesBuildsBeforePnpmAdd 钉住安装顺序。
//
// pnpm 11 把"依赖带构建脚本但没被批准"当成**错误**（ERR_PNPM_IGNORED_BUILDS → exit 1）：
// 全新目录里先 `pnpm add` 必然踩它，而批准清单若写在后面就永远轮不到 —— 新实例直接
// "安装失败"，报错还只提构建脚本（2026-10-17 真机踩到，五个包全被忽略）。
//
// 用假 pnpm 跑**真的** InstallToDirectory（不联网、不下包），断言两件事：
//  1. `pnpm add` 跑的时候，pnpm-workspace.yaml 里已经有 allowBuilds；
//  2. 即使 add 仍报 ignored-builds，流程也必须继续走到 `pnpm install`（包其实装好了）。
func TestInstallApprovesBuildsBeforePnpmAdd(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("没有 node，跳过假 pnpm 测试")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "fake-pnpm.mjs"), []byte(fakePnpmScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		shim := "@echo off\r\nnode \"%~dp0fake-pnpm.mjs\" %*\r\nexit /b %ERRORLEVEL%\r\n"
		if err := os.WriteFile(filepath.Join(bin, "pnpm.cmd"), []byte(shim), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		shim := "#!/bin/sh\nexec node \"$(dirname \"$0\")/fake-pnpm.mjs\" \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := filepath.Join(bin, "calls.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_PNPM_LOG", calls)

	for _, tc := range []struct {
		name    string
		failAdd bool
	}{
		{name: "正常安装"},
		{name: "add 报 ignored-builds 也要继续", failAdd: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(calls)
			dir := t.TempDir()
			app := &App{
				store:     &instanceStore{path: filepath.Join(dir, "instances.json")},
				processes: map[string]*managedProcess{},
				logs: &logStore{
					dir:   filepath.Join(dir, "logs"),
					files: map[string]*os.File{},
					sizes: map[string]int64{},
				},
			}
			instDir := filepath.Join(dir, "inst")
			app.store.add(Instance{ID: "t1", Name: "t", Directory: instDir, Version: "0.2.1-rc.2", PkgMgr: "local"})
			// 日志文件句柄要关掉，否则 TempDir 清理会失败（Windows 上文件被占用）。
			defer app.logs.closeAll()
			if tc.failAdd {
				t.Setenv("FAKE_PNPM_FAIL_ADD", "1")
			} else {
				t.Setenv("FAKE_PNPM_FAIL_ADD", "")
			}

			if _, err := app.InstallToDirectory("t1"); err != nil {
				t.Fatalf("InstallToDirectory 失败: %v", err)
			}

			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("假 pnpm 没被调用: %v", err)
			}
			got := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(got) < 2 {
				t.Fatalf("调用序列不完整: %v", got)
			}
			if !strings.HasPrefix(got[0], "add allowBuilds=true") {
				t.Errorf("`pnpm add` 时批准清单还没写好 → 真机就是 ERR_PNPM_IGNORED_BUILDS: %v", got)
			}
			if !strings.HasPrefix(got[1], "install") {
				t.Errorf("add 之后必须继续跑 `pnpm install`: %v", got)
			}

			// add 失败那一档：日志里要留下"为什么没当场判死刑"，别让用户只看到一句失败。
			logData, err := os.ReadFile(filepath.Join(dir, "logs", "t1.log"))
			if err != nil {
				t.Fatalf("读实例日志失败: %v", err)
			}
			logText := string(logData)
			if tc.failAdd && !strings.Contains(logText, "未批准的原生构建") {
				t.Errorf("ignored-builds 被放宽时要留痕，实际日志:\n%s", logText)
			}
			if !strings.Contains(logText, "安装完成，本地副本") {
				t.Errorf("安装应当走到收尾，实际日志:\n%s", logText)
			}
		})
	}
}

func TestIgnoredBuildsHelpers(t *testing.T) {
	tail := []string{
		"Progress: resolved 629, reused 264, downloaded 295, added 557, done",
		`[ERR_PNPM_IGNORED_BUILDS] Ignored build scripts: @deepseek-ai/dsh-subprocess-local@0.2.1-rc.2, koffi@3.1.1, node-pty@1.2.0-beta.15`,
		`Run "pnpm approve-builds" to pick which dependencies should be allowed to run scripts.`,
	}
	if !isIgnoredBuildsFailure(tail) {
		t.Fatal("应当认出 pnpm 的 ignored-builds 错误")
	}
	pkgs := ignoredBuildPackages(tail)
	want := []string{"@deepseek-ai/dsh-subprocess-local@0.2.1-rc.2", "koffi@3.1.1", "node-pty@1.2.0-beta.15"}
	if strings.Join(pkgs, "|") != strings.Join(want, "|") {
		t.Errorf("解析出的包 = %v, want %v", pkgs, want)
	}

	// 别的失败一律不当成放宽理由：认不出就是认不出（fail-closed 的方向是"照常失败"）。
	for _, other := range [][]string{
		nil,
		{""},
		{"ERR_PNPM_NO_MATCHING_VERSION  No matching version found for @deepseek-ai/dsh-spill-local@0.2.1-rc.1"},
		{"Ignored build scripts without the marker prefix"},
	} {
		if isIgnoredBuildsFailure(other) {
			t.Errorf("不该把 %v 当成 ignored-builds 放宽", other)
		}
	}
}
