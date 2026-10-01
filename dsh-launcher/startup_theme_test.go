package main

import (
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestBackgroundColourFor(t *testing.T) {
	dark := func() bool { return true }
	light := func() bool { return false }
	cases := []struct {
		name string
		pref string
		sys  func() bool
		want options.RGBA
	}{
		{"显式浅色不跟系统", "light", dark, startupColourLight},
		{"显式深色不跟系统", "dark", light, startupColourDark},
		{"system 跟随系统深色", "system", dark, startupColourDark},
		{"system 跟随系统浅色", "system", light, startupColourLight},
		{"未表态按 system 处理", "", dark, startupColourDark},
		{"脏值按 system 处理", "pink", light, startupColourLight},
		{"探测函数缺失时退回浅色", "system", nil, startupColourLight},
	}
	for _, c := range cases {
		got := backgroundColourFor(c.pref, c.sys)
		if got == nil {
			t.Fatalf("%s: backgroundColourFor(%q) 返回 nil", c.name, c.pref)
		}
		if *got != c.want {
			t.Errorf("%s: backgroundColourFor(%q) = %+v, want %+v", c.name, c.pref, *got, c.want)
		}
	}
}

// 主题偏好必须能跨进程留下来：这正是"下次启动先涂对底色"的全部依据。
func TestStartupBackgroundColourReadsPersistedTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := &settingsStore{path: path}
	s.setTheme("dark")
	if got := startupBackgroundColour(s); *got != startupColourDark {
		t.Fatalf("dark 偏好应给出深色底色，得到 %+v", *got)
	}
	// 重新读盘（相当于下次启动）：偏好不能丢。
	s2 := &settingsStore{path: path}
	s2.load()
	if got := startupBackgroundColour(s2); *got != startupColourDark {
		t.Fatalf("重载后主题偏好丢了，得到 %+v", *got)
	}
	if got := startupBackgroundColour(nil); got == nil {
		t.Fatal("settings 为空时也必须给出兜底底色")
	}
}

func TestRememberThemeOnlyAcceptsTriState(t *testing.T) {
	s := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json")}
	app := &App{settings: s}

	app.rememberTheme("pink")
	if got := s.get().Theme; got != "" {
		t.Fatalf("脏值不该落盘，得到 %q", got)
	}
	app.rememberTheme("light")
	if got := s.get().Theme; got != "light" {
		t.Fatalf("light 应落盘，得到 %q", got)
	}
	app.rememberTheme("system")
	if got := s.get().Theme; got != "system" {
		t.Fatalf("system 应落盘，得到 %q", got)
	}
}
