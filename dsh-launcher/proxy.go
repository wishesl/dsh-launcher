package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Network-proxy support for launcher-run downloads. Users behind a restricted
// network (or a flaky route to npm registries) can set e.g.
// http://127.0.0.1:20171 in Settings; the proxy is then:
//
//   - injected into every child command's environment (pnpm/npm/node and git
//     all honor HTTP(S)_PROXY) — this is what actually fixes the "plugin
//     install times out" symptom, because `dsh plugin add` runs `pnpm add`,
//   - used by the Go HTTP clients that fetch the plugin catalog and the npm
//     version registry.
//
// An empty value means "direct" (no proxy), falling back to the environment /
// system settings exactly like a console-launched process.

// ProxySettings is the network-proxy configuration shown in Settings.
type ProxySettings struct {
	Proxy string `json:"proxy"`
}

// proxySchemeRe accepts the proxy URL schemes npm/pnpm understand: http(s)
// and socks4/5 (with or without the hostname-resolution h/a suffixes).
// A missing scheme is the most common typo, so we reject it up front.
var proxySchemeRe = regexp.MustCompile(`^(https?|socks4a?|socks5h?)://`)

// proxyURL returns the configured proxy URL ("" = direct / system default).
func (a *App) proxyURL() string {
	if a != nil && a.settings != nil {
		return strings.TrimSpace(a.settings.get().Proxy)
	}
	return ""
}

// GetProxySettings exposes the current proxy configuration to the frontend.
func (a *App) GetProxySettings() ProxySettings {
	return ProxySettings{Proxy: a.proxyURL()}
}

// SetProxy updates the network proxy. Empty clears it; a non-empty value must
// start with a supported scheme and carry a host:port.
func (a *App) SetProxy(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if a.settings != nil {
			a.settings.setProxy("")
		}
		return nil
	}
	if !proxySchemeRe.MatchString(value) {
		return fmt.Errorf("代理地址必须以 http://、https://、socks4:// 或 socks5:// 开头")
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理地址格式不合法: %s", value)
	}
	if a.settings != nil {
		a.settings.setProxy(value)
	}
	return nil
}

// applyProxyToCmd adds the configured proxy to a child command's environment.
// Both upper- and lower-case spellings are set for cross-tool compatibility
// (pnpm/npm/node read the upper-case forms; some tools read lower-case on
// non-Windows). Loopback is always excluded so the local DSH web server and
// npx metadata traffic stay direct. cmd.Env must carry the whole parent
// environment forward — setting it replaces the inherited env entirely.
func (a *App) applyProxyToCmd(cmd *exec.Cmd) {
	proxy := a.proxyURL()
	if proxy == "" || cmd == nil {
		return
	}
	cmd.Env = append(os.Environ(),
		"HTTP_PROXY="+proxy,
		"HTTPS_PROXY="+proxy,
		"http_proxy="+proxy,
		"https_proxy="+proxy,
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
	)
}

// proxyLogLine returns a short log line advertising the active proxy, or ""
// when none is configured (used right after the "执行: …" command line).
func (a *App) proxyLogLine() string {
	if p := a.proxyURL(); p != "" {
		return "网络代理: " + p
	}
	return ""
}

// proxyHTTPClient returns an http.Client whose transport routes through the
// configured proxy. With no proxy configured it falls back to the
// environment / system settings (http.ProxyFromEnvironment), matching how a
// console-launched process behaves.
func (a *App) proxyHTTPClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if u := a.proxyURL(); u != "" {
		if pu, err := url.Parse(u); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
