package main

import "testing"

func TestExtractWebURL(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"DSH web listening on http://127.0.0.1:3080", "http://127.0.0.1:3080"},
		{"ready at http://localhost:49213/", "http://127.0.0.1:49213"},
		{"http://0.0.0.0:8080 (all interfaces)", "http://127.0.0.1:8080"},
		{"see https://example.com for docs", ""},
		{"no url in this line", ""},
		{"http://127.0.0.1 without port", ""},
	}
	for _, c := range cases {
		if got := extractWebURL(c.line); got != c.want {
			t.Errorf("extractWebURL(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// 内嵌视图用的带 token 地址：只认启动日志里那行带 token= 的 URL。
func TestExtractAuthWebURL(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		// DSH 启动时打印的真实行（取自 launcher 日志面板）
		{
			"dsh web: http://127.0.0.1:3080/?token=ONalrT9n17CHnxBJL7wpuVD1jxbkUIvzA0a9qarJ88",
			"http://127.0.0.1:3080/?token=ONalrT9n17CHnxBJL7wpuVD1jxbkUIvzA0a9qarJ88",
		},
		{"listening on http://localhost:49213/?token=abc123", "http://localhost:49213/?token=abc123"},
		// 没有 token 的行不认：普通地址已由 extractWebURL 覆盖
		{"dsh web: http://127.0.0.1:3080/", ""},
		{"DSH web listening on http://127.0.0.1:3080", ""},
		{"see https://example.com/?token=x for docs", ""}, // 非本机地址
		{"no url in this line", ""},
	}
	for _, c := range cases {
		if got := extractAuthWebURL(c.line); got != c.want {
			t.Errorf("extractAuthWebURL(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// 只有 DSH 自己的启动横幅能产出 web 地址候选；插件 / MCP 日志里的 loopback 地址
// 属于别的进程，必须被忽略（实测踩坑：billion-context 的 helper 监听 18787，
// native-attach 的清理日志把它打到 stderr，探针一连就通，实例被提前 ~40s 标成
// ready 并公布了错误地址，而 DSH 真正监听的是 3080）。
func TestWebAddressesOnlyFromBanner(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		candidate string
		authURL   string
	}{
		{
			"DSH 启动横幅（带 token）",
			"dsh web: http://127.0.0.1:3080/?token=ONalrT9n17CHnxBJL7wpuVD1jxbkUIvzA0a9qarJ88",
			"http://127.0.0.1:3080",
			"http://127.0.0.1:3080/?token=ONalrT9n17CHnxBJL7wpuVD1jxbkUIvzA0a9qarJ88",
		},
		{
			"DSH 启动横幅（不带 token：地址仍认，内嵌地址为空）",
			"dsh web: http://127.0.0.1:3081/",
			"http://127.0.0.1:3081",
			"",
		},
		{
			"插件清理日志里的别的进程端口（本次踩坑原文）",
			"2026-10-01T09:25:28.764Z [info] [v=dev] native-attach: drop http://127.0.0.1:18787 (pid 26140): owner process gone",
			"",
			"",
		},
		{
			"bili 代理的 MCP 端点日志",
			"bili-native-dsh: request sent DIRECT (uncompressed) — http://127.0.0.1:21724/mcp is not a recognized model endpoint",
			"",
			"",
		},
		{
			"chrome-devtools 的说明文字（无地址）",
			"chrome-devtools-mcp exposes content of the browser instance to the MCP clients",
			"",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCand, gotAuth := webAddresses(c.line)
			if gotCand != c.candidate || gotAuth != c.authURL {
				t.Errorf("webAddresses(%q) = (%q, %q), want (%q, %q)",
					c.line, gotCand, gotAuth, c.candidate, c.authURL)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	// local 模式不拼接版本号，任何值都放行
	if err := validateVersion("local", "not-a-version"); err != nil {
		t.Errorf("local mode should skip validation, got %v", err)
	}
	ok := []string{"latest", "", "0.1.1-rc.2", "1.2.3"}
	for _, v := range ok {
		if err := validateVersion("npx", v); err != nil {
			t.Errorf("validateVersion(npx, %q) = %v, want nil", v, err)
		}
	}
	bad := []string{"abc", "1.2", "latest && calc", "1.2.3 x"}
	for _, v := range bad {
		if err := validateVersion("pnpm", v); err == nil {
			t.Errorf("validateVersion(pnpm, %q) = nil, want error", v)
		}
	}
}
