package main

// env_boot.go — PATH bootstrap for GUI launches on macOS/Linux.
//
// A process started by double-clicking (file manager / D-Bus activation /
// desktop launcher) inherits the *session* environment, and that chain never
// sources ~/.bashrc. nvm / fnm / volta / asdf / pnpm all append their bin
// directory from an **interactive** rc file, so inside a double-clicked
// launcher `sh -c "npx ..."` (procattr_unix.go) cannot find npx/npm/pnpm at
// all — while starting the very same binary from a terminal works, because the
// shell already fixed PATH for it.
//
// What this does, once, before anything can spawn a child process (main.go):
//
//  1. Do nothing unless a node CLI is really missing: when npx/npm/pnpm all
//     resolve, PATH is left byte-for-byte alone, so a terminal launch behaves
//     exactly as before.
//  2. Probe the known node install locations on disk — no shell involved, so
//     this cannot hang — and prepend only directories that really hold a node
//     CLI, and only when that actually rescues one of the missing tools.
//  3. Only if that failed, ask the login shell once (`$SHELL -lic '…; env -0'`,
//     bounded by a timeout and a variable whitelist) and import the result.
//
// Everything is best-effort (fail-open): when no probe works nothing changes
// and nothing is blocked. The outcome is visible in 设置 → 前置环境 (npm/pnpm
// flip from 未找到 to a version) and in <config>/DSHLauncher/logs/app.log.
//
// Why mutate the *process* environment (os.Setenv) instead of each child's
// cmd.Env: applyProxyToCmd rebuilds cmd.Env from os.Environ() (proxy.go), so a
// child-level PATH would be wiped right there. Fixing the process environment
// covers shellCommand, exec.LookPath and os.Environ in one place.
//
// The file carries no build tag on purpose: only the runtime behaviour is
// platform-specific (the entry point returns early on Windows), so all of the
// ordering logic stays unit-testable on any OS.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// envDumpMarker separates the login shell's rc chatter from the `env -0` dump.
// Only what follows the last occurrence is parsed.
const envDumpMarker = "__DSH_LAUNCHER_ENV_DUMP__"

// loginShellBudget bounds the WHOLE shell-probing phase — every candidate shell
// times every argument form — so a pathological rc cannot stall startup even
// when each single attempt stays inside loginShellAttemptCap. A terminal that
// hangs on its own rc is a real possibility, not a hypothetical.
const loginShellBudget = 8 * time.Second

// loginShellAttemptCap bounds one single shell invocation inside that budget.
const loginShellAttemptCap = 4 * time.Second

// nodeToolNames are the CLIs that justify touching PATH. A directory that holds
// none of them is skipped: guessing would risk shadowing a working node with a
// broken one.
var nodeToolNames = []string{"node", "npm", "npx", "pnpm"}

// loginShellEnvAllow lists the shell variables worth importing. Deliberately
// narrow: node tool resolution only (PATH plus what the nvm/fnm/volta/pnpm
// shims need). Proxy variables are NOT imported — the launcher has its own
// proxy setting, and silently re-routing traffic because a terminal happened to
// export HTTPS_PROXY would be a surprising change of behaviour.
var loginShellEnvAllow = []string{
	"PATH",
	"NVM_DIR", "NVM_BIN", "NVM_INC",
	"PNPM_HOME",
	"VOLTA_HOME",
	"FNM_DIR", "FNM_MULTISHELL_PATH",
	"ASDF_DATA_DIR",
}

// envBootstrapNotes carries the one-shot bootstrap's log lines from main() to
// startup(), which is the first place a.logs exists.
var envBootstrapNotes []string

// bootstrapProcessEnv performs the GUI-launch PATH fix-up and returns the lines
// to log (nil = nothing was missing, or nothing could be improved).
func bootstrapProcessEnv() []string {
	if runtime.GOOS == "windows" {
		return nil // Windows inherits the user PATH from the registry — nothing to fix.
	}
	missing := missingNodeTools()
	if len(missing) == 0 {
		return nil
	}
	sep := string(os.PathListSeparator)
	var notes []string

	// ---- 1) Filesystem probe: no shell, cannot hang, no network ----
	home, _ := os.UserHomeDir()
	candidates := candidateNodeDirs(home, os.Getenv, readDirNames, readFileTrimmed)
	if dirs := existingToolDirs(candidates, dirHasNodeTool); len(dirs) > 0 {
		cur := splitPathList(os.Getenv("PATH"), sep)
		added := pathListDiff(dirs, cur)
		// Never rewrite PATH for nothing: only keep it when one of the missing
		// tools is resolvable afterwards.
		if len(added) > 0 {
			merged := joinPathList(mergePathList(added, cur), sep)
			if resolvesAnyTool(merged, missing, sep, toolInDir) {
				if err := os.Setenv("PATH", merged); err == nil {
					notes = append(notes, "PATH 已补全（探测到本机 node 安装目录）: "+strings.Join(added, sep))
				}
			}
		}
	}
	if missing = missingNodeTools(); len(missing) == 0 {
		return notes
	}

	// ---- 2) Last resort: ask the user's login shell once ----
	deadline := time.Now().Add(loginShellBudget)
	for _, sh := range loginShellCandidates() {
		if time.Now().After(deadline) {
			break
		}
		vars, err := dumpLoginShellEnv(sh, time.Until(deadline))
		if err != nil || len(vars) == 0 {
			continue
		}
		changed := applyLoginShellEnv(vars, missing, sep, toolInDir)
		if len(changed) == 0 {
			continue
		}
		notes = append(notes, fmt.Sprintf("已从登录 shell %s 补全环境: %s", sh, strings.Join(changed, ", ")))
		break
	}
	if left := missingNodeTools(); len(left) > 0 {
		notes = append(notes, "PATH 里仍找不到 "+strings.Join(left, "/")+
			"（可在终端里启动本程序；或把 node 的 bin 目录写进 ~/.config/environment.d/）")
	}
	return notes
}

// missingNodeTools returns the node CLIs that do not resolve in the current
// process PATH.
func missingNodeTools() []string {
	var missing []string
	for _, t := range nodeToolNames {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	return missing
}

// candidateNodeDirs returns the ordered directories worth prepending, most
// specific first — mirroring what an interactive shell would put at the front
// of PATH. Nothing here touches the filesystem directly: reads go through the
// injected callbacks so the whole ordering stays unit-testable.
func candidateNodeDirs(home string, getenv func(string) string, readDir func(string) ([]string, error), readFile func(string) (string, error)) []string {
	var dirs []string
	add := func(p string) {
		if p != "" {
			dirs = append(dirs, p)
		}
	}

	// Version managers first.
	if nvm := firstNonEmpty(getenv("NVM_DIR"), pathIn(home, ".nvm")); nvm != "" {
		add(nvmBinDir(nvm, readDir, readFile))
	}
	add(getenv("FNM_MULTISHELL_PATH"))
	for _, base := range []string{getenv("FNM_DIR"), pathIn(home, ".local/share/fnm"), pathIn(home, ".fnm")} {
		if base != "" {
			add(filepath.Join(base, "aliases", "default", "bin"))
		}
	}
	if volta := firstNonEmpty(getenv("VOLTA_HOME"), pathIn(home, ".volta")); volta != "" {
		add(filepath.Join(volta, "bin"))
	}
	if asdf := firstNonEmpty(getenv("ASDF_DATA_DIR"), pathIn(home, ".asdf")); asdf != "" {
		add(filepath.Join(asdf, "shims"))
	}
	if nodenv := firstNonEmpty(getenv("NODENV_ROOT"), pathIn(home, ".nodenv")); nodenv != "" {
		add(filepath.Join(nodenv, "shims"))
	}
	if mise := firstNonEmpty(getenv("MISE_DATA_DIR"), pathIn(home, ".local/share/mise")); mise != "" {
		add(filepath.Join(mise, "shims"))
	}

	// pnpm / npm per-user installs.
	add(getenv("PNPM_HOME"))
	add(pathIn(home, ".local/share/pnpm"))
	add(pathIn(home, ".pnpm"))
	add(pathIn(home, ".npm-global/bin"))
	add(pathIn(home, ".local/bin"))

	// System locations last: they only matter when the GUI PATH lost them.
	add("/opt/homebrew/bin")
	add("/home/linuxbrew/.linuxbrew/bin")
	add("/usr/local/bin")
	add("/usr/bin")
	return dirs
}

// existingToolDirs keeps the candidate order, dropping duplicates and every
// directory that does not actually hold a node CLI. The probe is injected so
// the ordering rules stay testable without a real node install.
func existingToolDirs(dirs []string, hasTool func(dir string) bool) []string {
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if o == d {
				dup = true
				break
			}
		}
		if dup || !hasTool(d) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// dirHasNodeTool reports whether dir holds one of the node CLIs.
func dirHasNodeTool(dir string) bool {
	for _, t := range nodeToolNames {
		if toolInDir(dir, t) {
			return true
		}
	}
	return false
}

// toolInDir reports whether dir/<tool> exists as a file (npm/npx are symlinks
// to a .js file on unix, so os.Stat's follow-the-link behaviour is what we want).
func toolInDir(dir, tool string) bool {
	st, err := os.Stat(filepath.Join(dir, tool))
	return err == nil && !st.IsDir()
}

// nvmBinDir resolves the bin directory nvm itself would use: alias/default
// (following alias hops such as lts/* → v20.11.1) or, when that cannot be
// pinned down, the newest installed version.
func nvmBinDir(nvmDir string, readDir func(string) ([]string, error), readFile func(string) (string, error)) string {
	names, err := readDir(filepath.Join(nvmDir, "versions", "node"))
	if err != nil {
		return ""
	}
	var versions []string
	for _, n := range names {
		if len(versionFields(n)) > 0 {
			versions = append(versions, n)
		}
	}
	if len(versions) == 0 {
		return ""
	}
	pick := pickNodeVersion(versions, nvmDefaultAlias(nvmDir, readFile))
	if pick == "" {
		return ""
	}
	return filepath.Join(nvmDir, "versions", "node", pick, "bin")
}

// nvmDefaultAlias reads <nvmDir>/alias/default, following at most two further
// alias hops (lts/*, node, …) in search of a concrete version. Returns "" when
// it cannot be pinned down, so the caller falls back to the newest install.
func nvmDefaultAlias(nvmDir string, readFile func(string) (string, error)) string {
	seen := map[string]bool{}
	alias := "default"
	for hop := 0; hop < 3; hop++ {
		body, err := readFile(filepath.Join(nvmDir, "alias", alias))
		if err != nil {
			return ""
		}
		body = strings.TrimSpace(firstLine(body))
		if body == "" || body == alias || seen[body] {
			return ""
		}
		seen[body] = true
		if len(versionFields(body)) > 0 {
			return body
		}
		alias = body // lts/*, node, stable … keep following
	}
	return ""
}

// pickNodeVersion resolves a requested version/alias body against the installed
// versions: exact match first, then numeric-prefix match ("22" → newest 22.x),
// then the newest install. Anything non-numeric ("lts/*", "node") never matches
// a prefix, it just falls through to the newest install.
func pickNodeVersion(versions []string, def string) string {
	def = strings.TrimSpace(def)
	if def != "" {
		trimmed := strings.TrimPrefix(def, "v")
		for _, v := range versions {
			if v == def || v == trimmed || strings.TrimPrefix(v, "v") == trimmed {
				return v
			}
		}
		if want := versionFields(def); len(want) > 0 {
			best := ""
			for _, v := range versions {
				got := versionFields(v)
				if len(got) < len(want) || !equalInts(got[:len(want)], want) {
					continue
				}
				if best == "" || compareNodeVersions(v, best) > 0 {
					best = v
				}
			}
			if best != "" {
				return best
			}
		}
	}
	return latestNodeVersion(versions)
}

// latestNodeVersion returns the numerically newest version ("" when empty).
func latestNodeVersion(versions []string) string {
	best := ""
	for _, v := range versions {
		if best == "" || compareNodeVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

// compareNodeVersions orders two nvm-style version strings ("v22.14.0"). The
// leading "v" is not part of semver, so it is stripped and the repo's semver
// ordering does the rest (compareSemver, market_update.go): v9.10.0 is newer
// than v9.9.0, which a plain string compare gets backwards. Inputs that are not
// x.y.z fall back to that same string compare, which is fine here because nvm
// only ever names its directories with full versions.
func compareNodeVersions(a, b string) int {
	strip := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }
	return compareSemver(strip(a), strip(b))
}

// versionFields splits a version-ish string into its numeric fields:
// "v22.14.0" → [22 14 0], "22" → [22], "lts/*" → nil. Build metadata and
// prerelease suffixes are ignored ("v22.14.0-rc.1" → [22 14 0]).
func versionFields(s string) []int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return nil
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		d := leadingDigits(p)
		if d == "" {
			return nil
		}
		n, err := strconv.Atoi(d)
		if err != nil {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}

// splitPathList splits a PATH-style value, dropping empty entries.
func splitPathList(path, sep string) []string {
	var out []string
	for _, p := range strings.Split(path, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mergePathList concatenates primary and fallback, keeping the first occurrence
// of every entry: primary decides the ordering, fallback only adds what is not
// there yet (so GUI-only entries are never lost).
func mergePathList(primary, fallback []string) []string {
	out := make([]string, 0, len(primary)+len(fallback))
	seen := map[string]bool{}
	for _, list := range [][]string{primary, fallback} {
		for _, d := range list {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// joinPathList renders a PATH-style list.
func joinPathList(list []string, sep string) string { return strings.Join(list, sep) }

// pathListDiff returns the entries of primary that fallback does not contain,
// so the probe log only ever reports directories it really added.
func pathListDiff(primary, fallback []string) []string {
	have := make(map[string]bool, len(fallback))
	for _, d := range fallback {
		have[d] = true
	}
	var out []string
	for _, d := range primary {
		if d != "" && !have[d] {
			out = append(out, d)
		}
	}
	return out
}

// resolvesAnyTool reports whether any of the tools lives in one of the PATH
// entries, using the injected directory probe.
func resolvesAnyTool(path string, tools []string, sep string, hasTool func(dir, tool string) bool) bool {
	for _, dir := range splitPathList(path, sep) {
		for _, t := range tools {
			if hasTool(dir, t) {
				return true
			}
		}
	}
	return false
}

// loginShellCandidates lists the shells to ask, most authoritative first: the
// user's own $SHELL, then the usual absolute paths.
func loginShellCandidates() []string {
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			return
		}
		for _, o := range out {
			if o == p {
				return
			}
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return
		}
		out = append(out, p)
	}
	add(os.Getenv("SHELL"))
	for _, p := range []string{"/bin/bash", "/usr/bin/bash", "/bin/zsh", "/usr/bin/zsh", "/bin/sh"} {
		add(p)
	}
	return out
}

// loginShellArgs returns the argument sets to try for one shell, most faithful
// to "what the terminal does" first.
//
// On Linux a terminal starts an **interactive non-login** shell, which reads
// ~/.bashrc directly — and that is exactly where the nvm/fnm/volta installers
// append their PATH line. A login shell only reads ~/.bashrc when ~/.profile
// happens to source it (verified on a real system: with only ~/.bashrc present,
// `bash -lic` does NOT see the nvm entries while `bash -ic` does). macOS
// terminals default to a login shell, so the order is flipped there. The
// login-only form is the last resort, and shells whose -i reads $ENV instead
// (dash/ksh) keep -l first.
func loginShellArgs(shell, goos string) [][]string {
	dump := "printf '%s' " + envDumpMarker + "; env -0"
	interactive := []string{"-ic", dump}
	loginInteractive := []string{"-lic", dump}
	loginOnly := []string{"-lc", dump}
	switch filepath.Base(shell) {
	case "sh", "dash", "ksh", "mksh", "ash":
		return [][]string{loginOnly, interactive}
	}
	if goos == "darwin" {
		return [][]string{loginInteractive, interactive, loginOnly}
	}
	return [][]string{interactive, loginInteractive, loginOnly}
}

// dumpLoginShellEnv runs the login-shell probe and returns the whitelisted
// subset of its environment (nil when every attempt failed). budget is split
// across the argument forms so one blocking rc cannot eat the whole budget.
func dumpLoginShellEnv(shell string, budget time.Duration) (map[string]string, error) {
	sets := loginShellArgs(shell, runtime.GOOS)
	per := budget / time.Duration(len(sets))
	if per > loginShellAttemptCap {
		per = loginShellAttemptCap
	}
	if per < time.Second {
		per = time.Second
	}
	for _, args := range sets {
		if vars, err := runEnvDump(shell, args, per); err == nil && len(vars) > 0 {
			return vars, nil
		}
	}
	return nil, fmt.Errorf("登录 shell 环境探测失败: %s", shell)
}

// runEnvDump executes one shell invocation and parses its `env -0` dump.
func runEnvDump(shell string, args []string, timeout time.Duration) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, args...)
	// Own session (Setsid on unix) — the same idiom shellCommand uses. Without
	// it an interactive shell launched from a GUI has no controlling terminal
	// and blocks in its job-control handshake until the timeout kills it:
	// verified on Linux that `bash -ic … </dev/null` never returns while
	// `setsid bash -ic …` does, which would make this whole fallback useless.
	cmd.SysProcAttr = newSysProcAttr()
	cmd.Stdin = nil // an rc that tries to read gets EOF instead of hanging us
	// A grandchild holding the pipe open would otherwise keep Run() waiting
	// even after the shell itself was killed on timeout.
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard // "no job control in this shell" and friends
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return parseEnvDump(out.Bytes(), envDumpMarker, loginShellEnvAllow), nil
}

// parseEnvDump extracts the whitelisted KEY=VALUE pairs from a NUL-separated
// `env -0` dump that follows marker. Anything printed before the marker (rc
// greetings, prompts) is ignored. A dump without a marker or without PATH is a
// failed probe, not an empty environment, so it reports nil.
func parseEnvDump(out []byte, marker string, allow []string) map[string]string {
	i := bytes.LastIndex(out, []byte(marker))
	if i < 0 {
		return nil
	}
	want := make(map[string]bool, len(allow))
	for _, k := range allow {
		want[k] = true
	}
	vars := make(map[string]string, len(allow))
	for _, entry := range bytes.Split(out[i+len(marker):], []byte{0}) {
		j := bytes.IndexByte(entry, '=')
		if j <= 0 {
			continue
		}
		if k := string(entry[:j]); want[k] {
			vars[k] = string(entry[j+1:])
		}
	}
	if vars["PATH"] == "" {
		return nil
	}
	return vars
}

// applyLoginShellEnv imports the whitelisted variables, atomically per shell:
// PATH is merged (shell order first, current entries kept after it) and the
// whole import only happens when that merge really rescues one of the missing
// tools. A shell that did not help contributes nothing — not even its
// half-useful NVM_DIR — so the next candidate shell gets a clean try. Every
// other variable then only fills a gap: what the launcher already has (because
// it was started from a terminal, say) wins.
// Returns the names of the variables that actually changed.
func applyLoginShellEnv(vars map[string]string, want []string, sep string, hasTool func(dir, tool string) bool) []string {
	shellPath := vars["PATH"]
	if shellPath == "" {
		return nil
	}
	cur := os.Getenv("PATH")
	merged := joinPathList(mergePathList(splitPathList(shellPath, sep), splitPathList(cur, sep)), sep)
	if merged == cur || !resolvesAnyTool(merged, want, sep, hasTool) {
		return nil
	}
	if err := os.Setenv("PATH", merged); err != nil {
		return nil
	}
	changed := []string{"PATH"}
	for _, k := range loginShellEnvAllow {
		if k == "PATH" {
			continue
		}
		v := vars[k]
		if v == "" {
			continue
		}
		if cur, ok := os.LookupEnv(k); ok && cur != "" {
			continue
		}
		if err := os.Setenv(k, v); err == nil {
			changed = append(changed, k)
		}
	}
	return changed
}

// readDirNames is the os.ReadDir adapter used by the candidate probe.
func readDirNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

// readFileTrimmed is the os.ReadFile adapter used by the candidate probe.
func readFileTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// pathIn joins a relative path onto home, or returns "" when home is unknown.
func pathIn(home, rel string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, rel)
}

// firstNonEmpty returns the first trimmed non-empty value.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// firstLine returns everything before the first line break.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// leadingDigits returns the leading run of ASCII digits ("" when there is none).
func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// equalInts reports whether two int slices are equal.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
