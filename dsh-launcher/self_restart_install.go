package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Built-in installation of the dsh-launcher-plugin plugin ("内置到 launcher")。
//
// The plugin source is embedded in the launcher binary (embeddata.go).
// InstallSelfRestartPlugin materializes it into a STABLE location inside the
// shared profile — <profile>/.dsh-builtin/dsh-launcher-plugin — and installs it with
// the regular pnpm command pipeline (`pnpm add file:<dir>`, reusing
// runMarketCommand). A stable materialization path keeps the package.json
// `file:` spec valid across future pnpm rebuilds.
//
// Mounting is decided at launch (see launcherPluginGate in self_restart.go):
// installed + the bridge is up → the temporary --patch overlay mounts it.
// Installing the plugin alone never mounts it anywhere — no residue in an
// instance that launches without the bridge.

// selfRestartBuiltinRel is where the embedded source is materialized inside
// the profile directory.
var selfRestartBuiltinRel = filepath.Join(".dsh-builtin", "dsh-launcher-plugin")

// SelfRestartPluginInstalled reports whether dsh-launcher-plugin is installed in the
// shared profile.
func (a *App) SelfRestartPluginInstalled() bool {
	installed, err := readInstalledPlugins()
	if err != nil {
		return false
	}
	_, ok := installed[selfRestartPluginName]
	return ok
}

// embeddedSelfRestartRoot is the embed.FS root of the bundled plugin sources
// (the directory named in the //go:embed directive).
const embeddedSelfRestartRoot = "embed/dsh-launcher-plugin"

// extractEmbeddedSelfRestart materializes the embedded plugin into the
// profile's .dsh-builtin directory (idempotent overwrite) and returns the
// absolute materialized directory.
func extractEmbeddedSelfRestart(profileDir string) (string, error) {
	dest := filepath.Join(profileDir, selfRestartBuiltinRel)
	var walk func(rel string) error
	walk = func(rel string) error {
		entries, err := embeddedSelfRestart.ReadDir(rel)
		if err != nil {
			return err
		}
		for _, e := range entries {
			src := rel + "/" + e.Name()
			if e.IsDir() {
				if err := walk(src); err != nil {
					return err
				}
				continue
			}
			data, err := embeddedSelfRestart.ReadFile(src)
			if err != nil {
				return err
			}
			out := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(src, embeddedSelfRestartRoot+"/")))
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(embeddedSelfRestartRoot); err != nil {
		return "", err
	}
	return dest, nil
}

// installBundledSelfRestart materializes the embedded plugin and runs the
// regular pnpm install for it. Callers own the marketBusy single-flight flag
// and the stopped-instance preflight.
//
// Deliberately NOT a no-op when dsh-launcher-plugin is already installed: re-running
// it is the ONLY way an existing copy gets upgraded in place. A user who
// installed the plugin before `relaxEmbedAuth` existed would otherwise keep a
// copy without the embedded-view auth relax forever (内嵌视图 → 永久 401).
// The old code dead-ended here with `Already` and returned before the pnpm
// step, so `pnpm add` never re-linked the refreshed source.
func (a *App) installBundledSelfRestart(inst *Instance) (*MarketOpResult, error) {
	profile := marketProfileDir()
	dir, err := extractEmbeddedSelfRestart(profile)
	if err != nil {
		msg := "解出内置插件失败: " + err.Error()
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Target: selfRestartPluginName, Error: msg})
		return &MarketOpResult{OK: false, Error: msg}, nil
	}
	return a.runInstall(inst, "file:"+filepath.ToSlash(dir))
}

// InstallSelfRestartPlugin installs the launcher-bundled dsh-launcher-plugin into the
// shared profile using the regular pnpm pipeline. Re-running it refreshes an
// already-installed copy in place（幂等解出 + pnpm 重新链接），这正是内置插件
// 能「更新」的实现方式（见 market_update.go 的 builtin 分支与插件更新方案 §3.4/M3）。
func (a *App) InstallSelfRestartPlugin(instanceID string) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	inst, err := a.preflightMarketOp(instanceID)
	if err != nil {
		return nil, err
	}

	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "install", Target: selfRestartPluginName})
	a.emit("dsh:market-log", map[string]string{"line": "正在解出内置插件…"})
	if from, to := materializedBuiltinVersion(), embeddedBuiltinVersion(); from != "" && to != "" && from != to {
		a.emit("dsh:market-log", map[string]string{
			"line": fmt.Sprintf("检测到已装副本 v%s 与内置 v%s 不一致，将覆盖为内置版本", from, to),
		})
	}

	return a.installBundledSelfRestart(inst)
}

// UninstallSelfRestartPlugin removes dsh-launcher-plugin from the shared profile via
// the regular pnpm pipeline.
func (a *App) UninstallSelfRestartPlugin(instanceID string) (*MarketOpResult, error) {
	if !marketBusy.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("已有插件操作正在进行，请稍候或取消")
	}
	defer marketBusy.Store(false)

	inst, err := a.preflightMarketOp(instanceID)
	if err != nil {
		return nil, err
	}

	a.emitMarketStatus(MarketOpStatus{State: "running", Kind: "uninstall", Target: selfRestartPluginName})
	cmdStr := pluginCommand(*inst, "remove", selfRestartPluginName)
	a.emit("dsh:market-log", map[string]string{"line": "执行: " + cmdStr})

	output, cancelled, runErr := a.runMarketCommand(inst.Directory, cmdStr)
	if runErr != nil && !cancelled {
		msg := fmt.Sprintf("卸载失败: %v\n%s", runErr, tailLines(output, 8))
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "uninstall", Target: selfRestartPluginName, Error: msg})
		return &MarketOpResult{OK: false, Output: output, Error: msg}, nil
	}
	if cancelled {
		a.emitMarketStatus(MarketOpStatus{State: "cancelled", Kind: "uninstall", Target: selfRestartPluginName})
		return &MarketOpResult{OK: false, Cancelled: true, Output: output}, nil
	}
	a.emitMarketStatus(MarketOpStatus{State: "done", Kind: "uninstall", Target: selfRestartPluginName})
	return &MarketOpResult{OK: true, Output: output}, nil
}

// migrateLegacySelfRestart swaps the retired dsh-self-mcp for the bundled
// dsh-launcher-plugin in the shared profile. Best-effort, background, retried:
// at startup every instance is usually stopped, but auto-start may launch one
// seconds later and preflightMarketOp rejects running instances.
//
// Why an automatic swap: the old plugin is mounted ONLY by this launcher's
// overlay and the new gate never mounts it again — without the swap, an old
// profile keeps a dead dsh-self-mcp installed and gets neither theme sync nor
// self-restart. The swap runs through the regular pnpm pipeline (market add /
// market remove), so the profile's package.json stays consistent; the profile
// is never hand-edited.
//
// Nothing here is fatal: if the retry budget runs out or the removal fails,
// the leftover dsh-self-mcp is inert (nothing mounts it) — we just log it.
func (a *App) migrateLegacySelfRestart() {
	installed, err := readInstalledPlugins()
	if err != nil {
		a.logs.note("migrate legacy plugin: read installed failed: " + err.Error())
		return
	}
	if _, hasNew := installed[selfRestartPluginName]; hasNew {
		return // already on the new plugin
	}
	if _, hasLegacy := installed[legacySelfRestartPluginName]; !hasLegacy {
		return // nothing to do
	}

	const attempts = 12 // ~3 minutes of patience, then give up silently
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(15 * time.Second)
		}
		// Single-flight with user-driven market operations.
		if !marketBusy.CompareAndSwap(false, true) {
			continue
		}
		// Pick any stopped instance to run pnpm from (preflight re-checks).
		var stopped *Instance
		a.mu.Lock()
		for _, s := range a.store.list() {
			if _, running := a.processes[s.ID]; running {
				continue
			}
			c := s
			stopped = &c
			break
		}
		a.mu.Unlock()
		if stopped == nil {
			marketBusy.Store(false) // everything running; retry after the delay
			continue
		}
		inst, perr := a.preflightMarketOp(stopped.ID)
		if perr != nil {
			marketBusy.Store(false)
			continue
		}

		a.logs.note(fmt.Sprintf("migrate: installing %s to replace legacy %s", selfRestartPluginName, legacySelfRestartPluginName))
		res, ierr := a.installBundledSelfRestart(inst)
		if ierr != nil || res == nil || !res.OK {
			marketBusy.Store(false)
			if res != nil && res.Error != "" {
				a.logs.note("migrate: install failed, will retry: " + res.Error)
			}
			continue
		}

		// Remove the legacy copy (best-effort — a leftover is inert anyway).
		cmdStr := pluginCommand(*inst, "remove", legacySelfRestartPluginName)
		_, cancelled, rerr := a.runMarketCommand(inst.Directory, cmdStr)
		marketBusy.Store(false)

		msg := "已安装 " + selfRestartPluginName + " 替代旧插件 " + legacySelfRestartPluginName
		if rerr == nil && !cancelled {
			msg += "，并卸载了旧插件"
		} else {
			msg += "（旧插件卸载未完成，已不再挂载、不影响功能）"
		}
		a.logs.note(msg)
		a.systemLog(inst.ID, 0, msg)
		return
	}
	a.logs.note("migrate: legacy " + legacySelfRestartPluginName + " still present after retries; leaving it (it is not mounted anymore)")
}
