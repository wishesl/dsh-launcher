package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LogEvent is streamed to the frontend on "dsh:log".
type LogEvent struct {
	InstanceID string `json:"instanceId"`
	PID        int    `json:"pid"`
	Line       string `json:"line"`
	Stream     string `json:"stream"` // "stdout" | "stderr" | "system"
	Time       string `json:"time"`
}

// StatusEvent is streamed to the frontend on "dsh:status".
// Status: "starting" | "running" | "ready" | "stopping" | "stopped" | "crashed".
// WebUrl is set (only) when Status == "ready": the URL captured from the
// process output and confirmed reachable via TCP probe.
// ExitCode is set (only) when Status == "crashed".
type StatusEvent struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	PID        int    `json:"pid"`
	WebUrl     string `json:"webUrl,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
}

// webURLRe matches the local web address DSH prints once it starts listening.
var webURLRe = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost|0\.0\.0\.0|\[::1\])(?::(\d{2,5}))?`)

// Source-mode default commands (源码启动). The user may override each in the
// form; the defaults mirror the standard DSH source workflow:
//
//	pnpm install → pnpm run build → pnpm dsh web
const (
	defaultSourceInitCmd  = "pnpm install"
	defaultSourceBuildCmd = "pnpm run build"
	defaultSourceStartCmd = "pnpm dsh web"
)

// sourceStartCommand returns the launch command for a 源码启动 instance: its
// user-editable 启动命令 (default "pnpm dsh web"), with any extra args appended.
func sourceStartCommand(inst Instance) string {
	cmd := strings.TrimSpace(inst.StartCmd)
	if cmd == "" {
		cmd = defaultSourceStartCmd
	}
	if extra := strings.TrimSpace(inst.ExtraArgs); extra != "" {
		cmd = cmd + " " + extra
	}
	return cmd
}

// effectiveSourceCmd returns cmd trimmed, or def when empty — the init/build
// commands are defaulted identically on save (app.go) and on run (install.go).
func effectiveSourceCmd(cmd, def string) string {
	if c := strings.TrimSpace(cmd); c != "" {
		return c
	}
	return def
}

// extractWebURL returns a normalized http://127.0.0.1:<port> URL from an
// output line, or "" if the line does not advertise one.
func extractWebURL(line string) string {
	m := webURLRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	port := m[1]
	if port == "" {
		return "" // scheme mention without a port is not usable
	}
	return "http://127.0.0.1:" + port
}

// managedProcess wraps a running DSH process for one instance.
type managedProcess struct {
	instanceID string
	pid        int
	cmd        *exec.Cmd
	job        *winJob // KILL_ON_JOB_CLOSE: kernel kills the tree even on hard app death

	done       chan struct{}
	once       sync.Once
	stopReq    atomic.Bool
	urlMu      sync.Mutex
	candidates []string // web URLs advertised by the process output, in order
}

func (p *managedProcess) requestStop() { p.stopReq.Store(true) }

func (p *managedProcess) stopRequested() bool { return p.stopReq.Load() }

func (p *managedProcess) addWebCandidate(u string) {
	p.urlMu.Lock()
	defer p.urlMu.Unlock()
	for _, c := range p.candidates {
		if c == u {
			return
		}
	}
	p.candidates = append(p.candidates, u)
}

// takeWebCandidates drains the candidate list.
func (p *managedProcess) takeWebCandidates() []string {
	p.urlMu.Lock()
	defer p.urlMu.Unlock()
	out := p.candidates
	p.candidates = nil
	return out
}

func (p *managedProcess) stop() {
	if p == nil {
		return
	}
	p.requestStop()
	p.once.Do(func() { close(p.done) })
	// Kernel-level kill first: closing the job handle terminates the whole
	// tree on Windows (KILL_ON_JOB_CLOSE); it is a no-op elsewhere.
	p.job.close()
	// killProcessTree reaps the whole npx -> node tree: taskkill /T /F on
	// Windows, the child's process group on macOS/Linux.
	killProcessTree(p.pid, p.cmd)
}

// validateVersion rejects version strings that would be interpolated into the
// shell command unless they are a clean semver (or the literal "latest").
// The "local" mode never interpolates the version, so it skips validation.
func validateVersion(pkgMgr, version string) error {
	if pkgMgr == "local" {
		return nil
	}
	v := strings.TrimSpace(version)
	if v == "" || v == "latest" {
		return nil
	}
	if _, err := parseVersion(v); err != nil {
		return fmt.Errorf("版本号不合法: %q（应为 x.y.z 或 x.y.z-预发布，如 0.1.1-rc.2）", v)
	}
	return nil
}

// LaunchInstance starts DSH in the instance's directory with its chosen
// version. Output is streamed to the frontend via the dsh:log event; once the
// process advertises a web address and that port accepts TCP connections, a
// "ready" status (with the working URL) is emitted.
func (a *App) LaunchInstance(id string) error {
	// Mark this attempt in-flight so shutdown() waits for it (and kills the
	// child once registered) instead of exiting mid-spawn.
	a.inFlight.Add(1)
	defer a.inFlight.Done()

	a.mu.Lock()
	inst := a.store.find(id)
	if inst == nil {
		a.mu.Unlock()
		return fmt.Errorf("实例不存在: %s", id)
	}
	if _, running := a.processes[id]; running {
		a.mu.Unlock()
		return fmt.Errorf("实例已在运行: %s", inst.Name)
	}

	if !inst.Source {
		if err := validateVersion(inst.PkgMgr, inst.Version); err != nil {
			a.mu.Unlock()
			return err
		}
	}

	inst.Status = "starting"
	inst.PID = 0
	snapshot := *inst
	a.mu.Unlock()

	a.emitStatus(snapshot.ID, "starting", 0)
	if snapshot.Source {
		a.systemLog(snapshot.ID, 0, fmt.Sprintf("正在启动 DSH（源码模式） (目录: %s)", snapshot.Directory))
		a.systemLog(snapshot.ID, 0, fmt.Sprintf("源码模式：启动命令「%s」。首次运行请先「安装到目录」执行初始化+构建。", sourceStartCommand(snapshot)))
	} else {
		a.systemLog(snapshot.ID, 0, fmt.Sprintf("正在启动 DSH %s (目录: %s)", versionLabel(snapshot.Version), snapshot.Directory))
	}
	if pl := a.proxyLogLine(); pl != "" {
		a.systemLog(snapshot.ID, 0, pl)
	}
	if !snapshot.Source && snapshot.PkgMgr == "local" && snapshot.LocalVersion == "" {
		a.systemLog(snapshot.ID, 0, "提示: 目录内未检测到本地副本，npx 可能回退到 registry 下载。建议先「安装到目录」。")
	}

	// 插件「适用」scope：启动前把不适用于本实例的插件屏蔽、适用的启用，
	// 保证该实例按配置加载插件（仅影响设置了适用实例的插件）。
	a.reconcilePluginScope(snapshot.ID)

	// buildCommandFn is a var (not a direct call) so tests can substitute the
	// command without spawning real npx.
	cmdStr := buildCommandFn(snapshot.Version, snapshot.ExtraArgs, snapshot.PkgMgr)
	if snapshot.Source {
		// 源码启动：直接执行用户配置的启动命令（默认 pnpm dsh web）。
		cmdStr = sourceStartCommand(snapshot)
	}
	cmd := shellCommand(context.Background(), cmdStr)
	cmd.Dir = snapshot.Directory
	if cmd.Dir == "" {
		cmd.Dir = "."
	}
	// Auto-answer prompts (e.g. pnpm asking whether to build native modules
	// like node-pty/koffi: "a" = select all, then "y" = confirm).
	cmd.Stdin = strings.NewReader("a\na\ny\ny\n")
	// First-run npx/pnpm-dlx downloads hit the registry too; route them
	// through the configured proxy (if any) so they don't hang like plugin
	// installs do on a restricted network.
	a.applyProxyToCmd(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.systemLog(snapshot.ID, 0, "创建输出管道失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.systemLog(snapshot.ID, 0, "创建错误管道失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return err
	}

	if err := cmd.Start(); err != nil {
		a.systemLog(snapshot.ID, 0, "启动失败: "+err.Error())
		a.setStopped(snapshot.ID)
		return fmt.Errorf("启动失败: %w", err)
	}

	mp := &managedProcess{
		instanceID: snapshot.ID,
		pid:        cmd.Process.Pid,
		cmd:        cmd,
		done:       make(chan struct{}),
	}
	// Assign the child to a KILL_ON_JOB_CLOSE job: if the launcher dies hard
	// (crash / Task Manager / power loss), the kernel reaps the whole tree.
	mp.job = newKillOnCloseJob()
	if h, err := openProcessForJob(cmd.Process.Pid); err == nil {
		if jerr := mp.job.assign(h); jerr != nil {
			a.systemLog(snapshot.ID, mp.pid, "提示: 进程未纳入 Job 托管（停止时将依赖平台进程树清理）")
		}
	} else {
		a.systemLog(snapshot.ID, mp.pid, "提示: 进程未纳入 Job 托管（停止时将依赖平台进程树清理）")
	}

	a.mu.Lock()
	// re-check instance still exists (user may have deleted meanwhile)
	if cur := a.store.find(id); cur != nil {
		cur.PID = cmd.Process.Pid
		cur.Status = "running"
	}
	a.processes[id] = mp
	a.mu.Unlock()

	a.systemLog(snapshot.ID, mp.pid, fmt.Sprintf("进程已启动 PID=%d，命令: %s", mp.pid, cmdStr))
	a.emitStatus(snapshot.ID, "running", mp.pid)
	// A process just started — ask the service probe to re-check its port so
	// the header/card flip to "已就绪" as soon as DSH answers.
	a.triggerServiceProbe()

	// Stream output lines to the frontend; capture advertised web URLs.
	stream := func(r *bufio.Scanner, tag string) {
		for r.Scan() {
			line := r.Text()
			if line == "" {
				continue
			}
			if u := extractWebURL(line); u != "" {
				mp.addWebCandidate(u)
			}
			a.logEvent(LogEvent{
				InstanceID: snapshot.ID,
				PID:        mp.pid,
				Line:       line,
				Stream:     tag,
				Time:       time.Now().Format(time.RFC3339),
			})
		}
	}
	go stream(bufio.NewScanner(stdout), "stdout")
	go stream(bufio.NewScanner(stderr), "stderr")

	// Probe advertised URLs until one accepts TCP connections, then flip the
	// instance to "ready". Exits early when the process dies.
	go a.probeReady(mp)

	// Wait for exit in the background, then reconcile state.
	go func() {
		waitErr := cmd.Wait()
		code, codeText := exitCodeOf(waitErr)
		crashed := waitErr != nil && !mp.stopRequested()
		if crashed {
			a.systemLog(snapshot.ID, mp.pid, "进程异常退出 "+codeText+"（非用户停止）")
		} else {
			a.systemLog(snapshot.ID, mp.pid, "进程已退出 "+codeText)
		}
		status := "stopped"
		if crashed {
			status = "crashed"
		}
		// Reconcile state for THIS process only. A quick stop→start may have
		// registered a new process under the same instance id by now; the old
		// goroutine must never clobber it (the stuck-"running" race): guard the
		// store write by PID and the map delete by identity.
		a.mu.Lock()
		wasCurrent := false
		if cur := a.store.find(id); cur != nil && cur.PID == mp.pid {
			cur.PID = 0
			cur.Status = status
			cur.WebUrl = ""
			wasCurrent = true
		}
		if curP, ok := a.processes[id]; ok && curP == mp {
			delete(a.processes, id)
			wasCurrent = true
		}
		a.mu.Unlock()
		if wasCurrent {
			switch {
			case crashed:
				// unexpected self-exit (bad args, port conflict, broken install...)
				a.emit("dsh:status", StatusEvent{InstanceID: snapshot.ID, Status: "crashed", PID: 0, ExitCode: code})
			case !mp.stopRequested():
				// clean self-exit: nothing else announced "stopped" for us
				a.emitStatus(snapshot.ID, "stopped", 0)
			default:
				// user-initiated stop: StopInstance already announced stopped
			}
		}
		// A process exited — the service it was serving may be gone (or a
		// replacement may be coming up): ask the service probe to re-check.
		a.triggerServiceProbe()
	}()

	return nil
}

// exitCodeOf maps a cmd.Wait error to an exit code and readable text.
func exitCodeOf(err error) (int, string) {
	if err == nil {
		return 0, "(exit 0)"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		c := ee.ExitCode()
		return c, fmt.Sprintf("(exit %d)", c)
	}
	return -1, "(exit ?): " + err.Error()
}

// probeReady watches the process's advertised web URLs and dials them until
// one accepts TCP connections, then emits status "ready" with the URL.
func (a *App) probeReady(mp *managedProcess) {
	deadline := time.Now().Add(3 * time.Minute)
	tried := map[string]bool{}
	for time.Now().Before(deadline) {
		select {
		case <-mp.done:
			return
		default:
		}
		for _, u := range mp.takeWebCandidates() {
			tried[u] = false
		}
		for u, reached := range tried {
			if reached {
				continue
			}
			if dialLocalWeb(u, 700*time.Millisecond) {
				tried[u] = true
				a.mu.Lock()
				if cur := a.store.find(mp.instanceID); cur != nil {
					cur.Status = "ready"
					cur.WebUrl = u
				}
				a.mu.Unlock()
				a.emit("dsh:status", StatusEvent{
					InstanceID: mp.instanceID,
					Status:     "ready",
					PID:        mp.pid,
					WebUrl:     u,
				})
				a.systemLog(mp.instanceID, mp.pid, "web 已就绪: "+u)
				return
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// dialLocalWeb checks that the host:port of u accepts TCP connections.
func dialLocalWeb(u string, timeout time.Duration) bool {
	idx := strings.LastIndex(u, ":")
	if idx < 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1"+u[idx:], timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// StopInstance stops a running instance (kills the process tree).
func (a *App) StopInstance(id string) error {
	a.mu.Lock()
	inst := a.store.find(id)
	mp := a.processes[id]
	if inst == nil {
		a.mu.Unlock()
		return fmt.Errorf("实例不存在: %s", id)
	}
	if mp == nil {
		// No launcher-managed process. If the instance still claims to be
		// running (e.g. a stale persisted/desynced state), correct the store
		// AND announce it — otherwise the frontend keeps showing 运行中 forever.
		changed := inst.Status != "stopped" && inst.Status != "crashed"
		if changed {
			inst.Status = "stopped"
			inst.PID = 0
			inst.WebUrl = ""
		}
		a.mu.Unlock()
		if changed {
			a.emitStatus(id, "stopped", 0)
		}
		a.triggerServiceProbe()
		return nil // not running, nothing to do
	}
	inst.Status = "stopping"
	pid := mp.pid
	a.mu.Unlock()

	a.emitStatus(id, "stopping", pid)
	a.systemLog(id, pid, "正在停止进程树...")
	mp.stop()

	a.mu.Lock()
	if cur := a.store.find(id); cur != nil {
		cur.Status = "stopped"
		cur.PID = 0
		cur.WebUrl = ""
	}
	delete(a.processes, id)
	a.mu.Unlock()
	a.emitStatus(id, "stopped", 0)
	a.triggerServiceProbe()
	return nil
}

func (a *App) setStopped(id string) {
	a.mu.Lock()
	if cur := a.store.find(id); cur != nil {
		cur.Status = "stopped"
		cur.PID = 0
		cur.WebUrl = ""
	}
	a.mu.Unlock()
	a.emitStatus(id, "stopped", 0)
	a.triggerServiceProbe()
}

func (a *App) emitStatus(id, status string, pid int) {
	a.emit("dsh:status", StatusEvent{InstanceID: id, Status: status, PID: pid})
}

func (a *App) systemLog(id string, pid int, line string) {
	a.logEvent(LogEvent{
		InstanceID: id,
		PID:        pid,
		Line:       line,
		Stream:     "system",
		Time:       time.Now().Format(time.RFC3339),
	})
}

// buildCommandFn indirection: tests override this to launch a harmless
// long-lived process instead of real npx.
var buildCommandFn = buildCommand

// buildCommand returns the shell command that launches DSH for a version.
// Examples:
//
//	pnpm dlx @deepseek-ai/dsh@0.1.1-rc.2 web --port 3081   (pkgMgr = "pnpm")
//	npx -y @deepseek-ai/dsh@0.1.1-rc.2 web                 (pkgMgr = "npx")
//	npx @deepseek-ai/dsh web --port 3081                   (pkgMgr = "local" — default & recommended:
//	                                                         uses the directory's node_modules copy,
//	                                                         the "official design" where the
//	                                                         workspace holds readable DSH source)
func buildCommand(version, extraArgs, pkgMgr string) string {
	extra := strings.TrimSpace(extraArgs)
	switch pkgMgr {
	case "local":
		parts := []string{"npx", "@deepseek-ai/dsh", "web"}
		if extra != "" {
			parts = append(parts, extra)
		}
		return strings.Join(parts, " ")
	case "npx":
		v := strings.TrimSpace(version)
		if v == "" {
			v = "latest"
		}
		parts := []string{"npx", "-y", "@deepseek-ai/dsh@" + v, "web"}
		if extra != "" {
			parts = append(parts, extra)
		}
		return strings.Join(parts, " ")
	}
	// default: pnpm dlx (reliable on machines where npm install hangs)
	v := strings.TrimSpace(version)
	if v == "" {
		v = "latest"
	}
	parts := []string{"pnpm", "dlx", "@deepseek-ai/dsh@" + v, "web"}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " ")
}

func versionLabel(version string) string {
	v := strings.TrimSpace(version)
	if v == "" || v == "latest" {
		return "@latest"
	}
	return "@" + v
}
