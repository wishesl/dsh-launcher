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
