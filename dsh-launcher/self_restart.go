package main

import (
	"os"
	"path/filepath"
)

// Launcher ↔ instance bridge ("dsh-launcher-plugin") contract.
//
// Gate (mount + env injection, one condition):
//   - the shared web profile has the plugin `dsh-launcher-plugin` installed
//     (see self_restart_install.go for the built-in install/migration flow), AND
//   - the launcher's loopback bridge is up (launcherPluginGate).
//
// Then the launcher (a) generates a temporary `--patch` overlay that mounts
// the plugin, and (b) injects DSH_LAUNCHER=1 / DSH_INSTANCE_ID=<id> /
// DSH_LAUNCH_ID / DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN. There is no
// instance-level checkbox anymore: mounting follows the installation.
//
// One bridge line carries all three features (theme sync, dsh-restart,
// capability handshake) over a loopback WebSocket — no state files are written by
// the plugin, so there is no restart-request.json / pending.json /
// capabilities.json channel left to reconcile. The restart flag lives in
// launcher memory (launcher_bridge), consumed once by the exit reconcile.
const (
	selfRestartPluginName = "dsh-launcher-plugin"
	// legacySelfRestartPluginName — the predecessor (file-channel plugin).
	// Still referenced by the startup migration that swaps it for the new one.
	legacySelfRestartPluginName = "dsh-self-mcp"
	// Temporary overlay file name for one launch, in the INSTANCE directory
	// (quote/space-free relative-name contract, same as the plugin mask).
	selfRestartOverlayPrefix = ".dsh-self-restart-"
)

// launcherPluginGate reports whether the bridge plugin must be mounted for a
// launch: installed in the shared profile AND the loopback bridge is up.
// Either missing → mounted=false (never an error, never a launch failure;
// capabilities fall back to "unknown" and the frontend stays fail-open).
// installed tells the caller whether a non-mount is worth logging (a plain
// profile without the plugin is the common case — stay silent there).
func (a *App) launcherPluginGate() (mounted bool, installed bool, detail string) {
	installedMap, err := readInstalledPlugins()
	if err != nil {
		return false, false, "读取已安装插件失败：" + err.Error()
	}
	_, installed = installedMap[selfRestartPluginName]
	if !installed {
		return false, false, "全局未安装插件 " + selfRestartPluginName
	}
	if a.bridge == nil || a.bridge.url == "" {
		return false, true, "启动器桥接未就绪（loopback WebSocket 服务没起来）"
	}
	return true, true, "已安装 " + selfRestartPluginName + " 且桥接就绪，本次启动已挂载"
}

// selfRestartRelName is the temporary overlay file name for an instance,
// written into the INSTANCE directory (the dsh process cwd) — same
// quote/space-free relative-name contract as the plugin mask, so the launch
// command stays free of `"` (Go's exec escaping would corrupt an absolute
// path through cmd.exe).
func selfRestartRelName(instanceID string) string {
	return selfRestartOverlayPrefix + instanceID + ".yml"
}

// writeSelfRestartOverlay writes the temporary `--patch` overlay that mounts
// the bridge plugin for one launch, mirroring writeMaskOverlay semantics:
// a YAML entry list read once at dsh boot, safe to delete once the process
// has booted. Returns the RELATIVE file name.
//
// The row MUST be an `insert:` block, not a bare `- id/name` row: the loader's
// patch semantics treat a bare row as an override of an EXISTING entry (looked
// up by id; a miss warns and skips), while `insert:` pushes a NEW entry into
// the composed list — the same form the profile's cordis.patch.yml uses for
// mcp-* rows.
func writeSelfRestartOverlay(instanceID, dir string) (string, error) {
	rel := selfRestartRelName(instanceID)
	content := "# DSH launcher 桥接插件覆盖层（--patch overlay，仅本次启动生效）\n" +
		"- insert:\n" +
		"    - id: launcher-plugin\n" +
		"      name: '" + selfRestartPluginName + "'\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// cleanupSelfRestartOverlay removes an instance's temporary overlay from its
// directory (best-effort).
func cleanupSelfRestartOverlay(instanceID, dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, selfRestartRelName(instanceID)))
}
