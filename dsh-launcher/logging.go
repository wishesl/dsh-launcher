package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// closeAll flushes and closes every open file (called on shutdown).
func (l *logStore) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, f := range l.files {
		_ = f.Close()
		delete(l.files, id)
	}
}

// logEvent is the single funnel for every log line shown in the UI: it emits
// dsh:log to the frontend AND persists the line to disk.
func (a *App) logEvent(e LogEvent) {
	a.emit("dsh:log", e)
	a.logs.append(e)
}
