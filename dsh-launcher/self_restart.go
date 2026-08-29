package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Self-managed restart ("dsh-restart") contract between an opted-in project
// and the launcher.
//
// Opt-in (double gate, both must hold):
//   - the shared web profile has the plugin `dsh-self-mcp` installed, AND
//   - the instance directory (the project) contains the checked-in marker
//     `.dsh-self-restart.optin`.
//
// Only then does the launcher (a) generate a temporary `--patch` overlay that
// mounts the plugin, and (b) inject DSH_LAUNCHER=1 / DSH_INSTANCE_ID=<id> so
// the plugin knows it is supervised. Other projects/dirs never mount it → no
// global residue: a machine-launched DSH outside this project has no row and
// the tool does not exist.
//
// Restart request: the plugin writes <dir>/.dsh-self-mcp/restart-request.json
// and exits cleanly (exit 0). The launcher's exit reconcile sees the request
// and relaunches the same instance.
const (
	selfRestartPluginName    = "dsh-self-mcp"
	selfRestartOptInMarker   = ".dsh-self-restart.optin"
	selfRestartStateDir      = ".dsh-self-mcp"
	selfRestartRequestFile   = "restart-request.json"
	selfRestartOverlayPrefix = ".dsh-self-restart-"
)

// selfRestartEnabled reports whether the launcher should mount the restart
// plugin for an instance rooted at dir. Both gates must pass; a missing
// profile or marker simply disables (never an error).
func selfRestartEnabled(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	installed, err := readInstalledPlugins()
	if err != nil {
		return false
	}
	if _, ok := installed[selfRestartPluginName]; !ok {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, selfRestartOptInMarker)); err != nil {
		return false
	}
	return true
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
// the self-restart plugin for one launch, mirroring writeMaskOverlay
// semantics: a YAML entry list read once at dsh boot, safe to delete once the
// process has booted. Returns the RELATIVE file name.
//
// The row MUST be an `insert:` block, not a bare `- id/name` row: the loader's
// patch semantics treat a bare row as an override of an EXISTING entry (looked
// up by id; a miss warns and skips), while `insert:` pushes a NEW entry into
// the composed list — the same form the profile's cordis.patch.yml uses for
// mcp-* rows.
func writeSelfRestartOverlay(instanceID, dir string) (string, error) {
	rel := selfRestartRelName(instanceID)
	content := "# DSH 自管理重启覆盖层（--patch overlay，仅本次启动生效）\n" +
		"- insert:\n" +
		"    - id: self-restart\n" +
		"      name: '" + selfRestartPluginName + "'\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// cleanupSelfRestartOverlay removes an instance's temporary self-restart
// overlay from its directory (best-effort).
func cleanupSelfRestartOverlay(instanceID, dir string) {
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, selfRestartRelName(instanceID)))
}

// restartRequestPath is where the plugin signals a relaunch request.
func restartRequestPath(dir string) string {
	return filepath.Join(dir, selfRestartStateDir, selfRestartRequestFile)
}

// consumeRestartRequest reads and removes one restart-request file. It
// returns true only when a non-empty request was actually present, so the
// exit reconcile relaunches at most once per request (removing the file
// breaks any crash loop).
func consumeRestartRequest(dir string) bool {
	if dir == "" {
		return false
	}
	path := restartRequestPath(dir)
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return false
	}
	_ = os.Remove(path)
	return true
}
