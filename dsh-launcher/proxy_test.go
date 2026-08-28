package main

import (
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testApp builds an App whose settings persist to a throwaway file, so proxy
// tests never touch the real %APPDATA%\DSHLauncher\settings.json.
func testApp(t *testing.T) *App {
	t.Helper()
	return &App{settings: &settingsStore{path: filepath.Join(t.TempDir(), "settings.json")}}
}

func TestSetProxyValidation(t *testing.T) {
	a := testApp(t)
	for _, v := range []string{"http://127.0.0.1:20171", "https://proxy.example:8080", "socks5://127.0.0.1:1080", "socks4://proxy:1", ""} {
		if err := a.SetProxy(v); err != nil {
			t.Errorf("SetProxy(%q) unexpected error: %v", v, err)
		}
	}
	for _, v := range []string{"127.0.0.1:20171", "proxy.example:8080", "ftp://proxy", "http://", "https://", "socks5://"} {
		if err := a.SetProxy(v); err == nil {
			t.Errorf("SetProxy(%q) should have been rejected", v)
		}
	}
}

func TestSetProxyRoundTrip(t *testing.T) {
	a := testApp(t)
	if err := a.SetProxy("http://127.0.0.1:20171"); err != nil {
		t.Fatal(err)
	}
	if got := a.GetProxySettings().Proxy; got != "http://127.0.0.1:20171" {
		t.Fatalf("GetProxySettings().Proxy = %q", got)
	}
	if err := a.SetProxy("  "); err != nil {
		t.Fatal(err)
	}
	if got := a.GetProxySettings().Proxy; got != "" {
		t.Fatalf("clearing proxy failed, still %q", got)
	}
}

func TestApplyProxyToCmd(t *testing.T) {
	a := testApp(t)
	if err := a.SetProxy("http://127.0.0.1:20171"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd", "/c", "echo hi")
	a.applyProxyToCmd(cmd)
	env := map[string]string{}
	for _, kv := range cmd.Env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	want := "http://127.0.0.1:20171"
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if env[k] != want {
			t.Errorf("env[%s] = %q, want %q", k, env[k], want)
		}
	}
	if !strings.Contains(env["NO_PROXY"], "127.0.0.1") {
		t.Errorf("NO_PROXY should exclude loopback, got %q", env["NO_PROXY"])
	}
	// setting cmd.Env replaces inheritance — parent vars must be carried over
	if _, ok := env["PATH"]; !ok {
		t.Error("applyProxyToCmd dropped the inherited environment (PATH)")
	}

	// no proxy configured → env untouched (nil = inherit)
	a2 := testApp(t)
	cmd2 := exec.Command("cmd", "/c", "echo hi")
	a2.applyProxyToCmd(cmd2)
	if cmd2.Env != nil {
		t.Fatalf("without proxy, cmd.Env should stay nil, got %v", cmd2.Env)
	}
}

func TestProxyHTTPClient(t *testing.T) {
	// configured proxy → transport routes through it
	a := testApp(t)
	if err := a.SetProxy("http://127.0.0.1:20171"); err != nil {
		t.Fatal(err)
	}
	tr := a.proxyHTTPClient(5 * time.Second).Transport.(*http.Transport)
	u, err := tr.Proxy(&http.Request{URL: mustURL(t, "https://registry.npmjs.org/x")})
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Host != "127.0.0.1:20171" {
		t.Fatalf("transport proxy = %v, want 127.0.0.1:20171", u)
	}

	// no proxy configured → falls back to ProxyFromEnvironment (env-based)
	a2 := testApp(t)
	tr2 := a2.proxyHTTPClient(5 * time.Second).Transport.(*http.Transport)
	if tr2.Proxy == nil {
		t.Fatal("expected ProxyFromEnvironment fallback without a configured proxy")
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
