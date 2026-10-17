package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logMaxBytes   = 5 << 20 // rotate a per-instance log at 5 MiB
	logKeptGens   = 1       // keep one rotated generation (<id>.log.1)
	logFlushWrite = true    // os.File writes are unbuffered; no explicit flush needed
)

// logStore persists instance logs under %APPDATA%\DSHLauncher\logs so output
// survives restarts and crashes can be diagnosed afterwards.
type logStore struct {
	mu    sync.Mutex
	dir   string
	files map[string]*os.File
	sizes map[string]int64
}

func newLogStore() *logStore {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return &logStore{
		dir:   filepath.Join(base, "DSHLauncher", "logs"),
		files: map[string]*os.File{},
		sizes: map[string]int64{},
	}
}

// append writes one event line as: time \t stream \t line.
// Rotation: when <id>.log exceeds logMaxBytes it becomes <id>.log.1
// (overwriting the previous generation).
func (l *logStore) append(e LogEvent) {
	if l == nil || e.InstanceID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.files[e.InstanceID]
	if !ok || l.sizes[e.InstanceID] > logMaxBytes {
		if f != nil {
			_ = f.Close()
			l.rotateLocked(e.InstanceID)
		}
		var err error
		f, err = l.openLocked(e.InstanceID)
		if err != nil {
			return // disk problems must never break the UI log stream
		}
	}
	var b strings.Builder
	b.WriteString(e.Time)
	b.WriteString("\t")
	b.WriteString(e.Stream)
	b.WriteString("\t")
	b.WriteString(strings.ReplaceAll(e.Line, "\r", " "))
	b.WriteString("\n")
	if n, err := f.WriteString(b.String()); err == nil {
		l.sizes[e.InstanceID] += int64(n)
	}
}

func (l *logStore) openLocked(id string) (*os.File, error) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(l.dir, id+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err == nil {
		l.sizes[id] = st.Size()
	} else {
		l.sizes[id] = 0
	}
	l.files[id] = f
	return f, nil
}

func (l *logStore) rotateLocked(id string) {
	src := filepath.Join(l.dir, id+".log")
	dst := filepath.Join(l.dir, id+".log.1")
	_ = os.Remove(dst) // Windows rename refuses to overwrite existing files
	_ = os.Rename(src, dst)
	delete(l.files, id)
	delete(l.sizes, id)
}

const (
	// runStartMarker 是启动器每次拉起实例前写的那行（见 dsh_process.go）。用它把
	// "本次运行"从历史日志里切出来：上一轮判过的结论不能算在这一轮头上 —— 否则
	// 用户修好了问题，面板还一直报红（AGENTS.md §9：误报会让整个面板失去可信度）。
	runStartMarker = "正在启动 DSH"
	// logRunScanBytes 把这次扫描限制在日志尾部：单实例日志按 5 MiB 轮转，
	// 面板会被反复打开，不值得每次把整份读进来。
	logRunScanBytes = 2 << 20
)

// currentRunTail 返回该实例日志里属于**本次运行**的那一段文本，以及是否找到了本次
// 运行的起点。找不到起点（日志刚轮转过、这一轮还没写）时 ok=false —— 调用方必须把
// 它当"没有结论"处理：宁可不说，也不要把上一轮的结论算进来。
func (l *logStore) currentRunTail(id string) (string, bool) {
	if l == nil || id == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(l.dir, id+".log"))
	if err != nil {
		return "", false
	}
	if len(data) > logRunScanBytes {
		data = data[len(data)-logRunScanBytes:]
	}
	text := string(data)
	idx := strings.LastIndex(text, runStartMarker)
	if idx < 0 {
		return text, false
	}
	return text[idx:], true
}

// closeAll flushes and closes every open file (called on shutdown).
func (l *logStore) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, f := range l.files {
		_ = f.Close()
		delete(l.files, id)
	}
}

// note appends an app-level diagnostic line to <dir>\app.log (best effort).
// Used to audit lifecycle events such as shutdown reaping, so a future
// "orphan process" report can be traced to the exact code path.
func (l *logStore) note(line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(l.dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(time.Now().Format(time.RFC3339) + "\t" + line + "\n")
	_ = f.Close()
}

// logEvent is the single funnel for every log line shown in the UI: it emits
// dsh:log to the frontend AND persists the line to disk.
func (a *App) logEvent(e LogEvent) {
	a.emit("dsh:log", e)
	a.logs.append(e)
}
