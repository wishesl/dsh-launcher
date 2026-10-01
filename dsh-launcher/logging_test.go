package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 例行启动播报（正在启动 / 自管理重启覆盖层路径 / 已启动的命令）每次启动都出现，
// 真机反馈说面板上纯属噪声，所以改成 systemLogQuiet：只写实例日志文件、不推给界面。
// 它们仍是排障时要查的东西（跑了哪条命令、挂了哪层 patch），因此必须留在文件里。
//
// 这个用例跑真机同一条 LaunchInstance 管线（命令换成 ping），同时盯两条通道：
// 实例日志文件里必须有，dsh:log 事件里必须没有。
func TestStartupRoutineLinesAreFileOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	overrideCommand(t, uniquePingMarker())
	app := newShutdownTestApp(t)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	logFile := filepath.Join(app.logs.dir, "t1.log")

	var mu sync.Mutex
	var uiLines []string
	old := emitHook
	emitHook = func(event string, payload interface{}) {
		if event != "dsh:log" {
			return
		}
		e, ok := payload.(LogEvent)
		if !ok {
			return
		}
		mu.Lock()
		uiLines = append(uiLines, e.Line)
		mu.Unlock()
	}
	t.Cleanup(func() { emitHook = old })

	if err := app.LaunchInstance("t1"); err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}

	// 「进程已启动」落盘即说明两条例行行都已产生（正在启动在它之前）。
	var onDisk string
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(logFile); err == nil {
			onDisk = string(b)
		}
		if strings.Contains(onDisk, "进程已启动") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("实例日志里始终没有「进程已启动」行，日志内容:\n%s", onDisk)
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, needle := range []string{"正在启动 DSH", "进程已启动 PID="} {
		if !strings.Contains(onDisk, needle) {
			t.Errorf("实例日志应保留 %q（排障要用），实际日志:\n%s", needle, onDisk)
		}
		mu.Lock()
		for _, line := range uiLines {
			if strings.Contains(line, needle) {
				t.Errorf("面板不该收到例行启动行 %q，却收到了: %s", needle, line)
			}
		}
		mu.Unlock()
	}
}
