package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Built-in installation of the dsh-self-mcp plugin ("内置到 launcher")。
//
// The plugin source is embedded in the launcher binary (embed/embed.go).
// InstallSelfRestartPlugin materializes it into a STABLE location inside the
// shared profile — <profile>/.dsh-builtin/dsh-self-mcp — and installs it with
// the regular pnpm command pipeline (`pnpm add file:<dir>`, reusing
// runMarketCommand). A stable materialization path keeps the package.json
// `file:` spec valid across future pnpm rebuilds.
//
// Per-instance enablement is a separate step: the instance form checkbox
// (Instance.SelfRestart) decides at launch whether the plugin is mounted via
// the temporary --patch overlay (see self_restart.go). Installing the plugin
// alone never mounts it anywhere — no residue outside an opted-in instance.

// selfRestartBuiltinRel is where the embedded source is materialized inside
// the profile directory.
var selfRestartBuiltinRel = filepath.Join(".dsh-builtin", "dsh-self-mcp")

// SelfRestartPluginInstalled reports whether dsh-self-mcp is installed in the
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
const embeddedSelfRestartRoot = "embed/dsh-self-mcp"

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

// InstallSelfRestartPlugin installs the launcher-bundled dsh-self-mcp into the
// shared profile using the regular pnpm pipeline. Mirrors InstallPlugin's
// guards and status events.
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

	profile := marketProfileDir()
	dir, err := extractEmbeddedSelfRestart(profile)
	if err != nil {
		msg := "解出内置插件失败: " + err.Error()
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Target: selfRestartPluginName, Error: msg})
		return &MarketOpResult{OK: false, Error: msg}, nil
	}

	installed, _ := readInstalledPlugins()
	if _, ok := installed[selfRestartPluginName]; ok {
		msg := "该插件已安装（启用与否由各实例的「自管理重启」开关决定）"
		a.emitMarketStatus(MarketOpStatus{State: "failed", Kind: "install", Target: selfRestartPluginName, Error: msg})
		return &MarketOpResult{OK: false, Already: true, Error: msg}, nil
	}

	return a.runInstall(inst, "file:"+filepath.ToSlash(dir))
}

// UninstallSelfRestartPlugin removes dsh-self-mcp from the shared profile via
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
