package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ServiceState answers "does the DSH service this instance is configured to
// serve answer on its port right now" — decoupled from whether the launcher
// itself is managing the process. It is emitted on "dsh:service" and returned
// by ProbeServices; it drives the header "DSH 已就绪" + the open button.
type ServiceState struct {
	InstanceID string `json:"instanceId"`
	URL        string `json:"url"`       // "" when not determinable (--port 0, no runtime URL yet)
	Reachable  bool   `json:"reachable"` // the URL answered an HTTP request
}

// extraPortRe mirrors the frontend's getWebUrl: it matches --port N / --port=N
// in extraArgs.
var extraPortRe = regexp.MustCompile(`--port(?:[=\s]+(\d+))?`)

// instanceServiceURL returns the URL the instance's DSH service should listen
// on: the runtime-captured URL when known (needed for --port 0 auto ports),
// otherwise the configured --port, defaulting to 3080. ok=false means the URL
// cannot be determined yet (--port 0 with no process output captured).
func instanceServiceURL(inst *Instance) (string, bool) {
	if u := strings.TrimSpace(inst.WebUrl); u != "" {
		return u, true
	}
	// 源码启动：端口可能写在自定义启动命令里（pnpm dsh web --port 3081），
	// 先于 extraArgs 解析。
	if inst.Source {
		if m := extraPortRe.FindStringSubmatch(strings.TrimSpace(inst.StartCmd)); m != nil && m[1] != "" {
			p, err := strconv.Atoi(m[1])
			if err == nil {
				if p == 0 {
					return "", false // OS-chosen port; unknown until process output
				}
				return fmt.Sprintf("http://127.0.0.1:%d", p), true
			}
		}
	}
	if m := extraPortRe.FindStringSubmatch(inst.ExtraArgs); m != nil && m[1] != "" {
		p, err := strconv.Atoi(m[1])
		if err == nil {
			if p == 0 {
				return "", false // OS-chosen port; unknown until process output
			}
			return fmt.Sprintf("http://127.0.0.1:%d", p), true
		}
	}
	return "http://127.0.0.1:3080", true
}

// probeService reports whether url answers an HTTP request. Any response
// (200/302/404…) counts — we only care that the DSH service is actually up.
// Localhost is dialed directly, never through the configured proxy.
func probeService(url string) bool {
	client := &http.Client{
		Timeout: 1200 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: nil, // 127.0.0.1 direct only
		},
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// probeAllServices probes every configured instance and emits "dsh:service"
// only when a state changed, so the frontend isn't spammed every tick.
func (a *App) probeAllServices() {
	for _, inst := range a.store.list() {
		url, ok := instanceServiceURL(&inst)
		st := ServiceState{InstanceID: inst.ID, URL: url}
		if ok {
			st.Reachable = probeService(url)
		}
		a.svcMu.Lock()
		prev, seen := a.svcKnown[inst.ID]
		if !seen || prev != st {
			a.svcKnown[inst.ID] = st
			a.svcMu.Unlock()
			a.emit("dsh:service", st)
		} else {
			a.svcMu.Unlock()
		}
	}
}

// serviceProbeLoop re-checks reachability on a low-frequency fallback ticker
// (covers DSH being killed externally / crashing without a launcher event) and
// immediately whenever triggerServiceProbe is called (process start/stop,
// save, remove).
func (a *App) serviceProbeLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.probeAllServices()
		case <-a.svcTrigger:
			a.probeAllServices()
		}
	}
}

// triggerServiceProbe wakes the probe loop without blocking callers.
func (a *App) triggerServiceProbe() {
	select {
	case a.svcTrigger <- struct{}{}:
	default:
	}
}

// ProbeServices returns the current service reachability for all instances.
// Called by the frontend after subscribing to "dsh:service" so the initial
// snapshot is never missed (Wails events emitted before subscription are
// dropped).
func (a *App) ProbeServices() []ServiceState {
	a.probeAllServices()
	a.svcMu.Lock()
	defer a.svcMu.Unlock()
	out := make([]ServiceState, 0, len(a.svcKnown))
	for _, s := range a.svcKnown {
		out = append(out, s)
	}
	return out
}
