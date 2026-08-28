//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// uniquePingMarker returns a distinct ping payload size used to identify OUR
// child processes, so assertions never collide with unrelated ping.exe.
// Must stay within ping's -l valid range (0..65500).
func uniquePingMarker() string {
	return strconv.Itoa(1000 + int(time.Now().UnixNano()%60000))
}

// pingAlive reports how many ping.exe processes carry the given payload size.
func pingAlive(marker string) int {
	out, err := exec.Command(
		"powershell", "-NoProfile", "-Command",
		fmt.Sprintf(
			"(Get-CimInstance Win32_Process -Filter \"Name='ping.exe'\") | Where-Object { $_.CommandLine -like '*%s*' } | Measure-Object | Select-Object -ExpandProperty Count",
			marker,
		),
	).Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return 0
}

func newShutdownTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store := &instanceStore{path: filepath.Join(dir, "instances.json")}
	ls := &logStore{dir: filepath.Join(dir, "logs"), files: map[string]*os.File{}, sizes: map[string]int64{}}
	app := &App{store: store, processes: map[string]*managedProcess{}, logs: ls}
	store.add(Instance{ID: "t1", Name: "t", Directory: ".", Version: "latest", PkgMgr: "local"})
	return app
}

// overrideCommand swaps the launch command for a harmless long-lived ping with
// a unique payload marker, so the full LaunchInstance/shutdown pipeline runs
// without npx and without touching unrelated processes.
func overrideCommand(t *testing.T, marker string) {
	t.Helper()
	old := buildCommandFn
	buildCommandFn = func(version, extraArgs, pkgMgr string) string {
		return fmt.Sprintf("ping -n 120 -l %s 127.0.0.1", marker)
	}
	t.Cleanup(func() { buildCommandFn = old })
}

// Regression: quitting while an auto-start attempt is still inside its
// stagger-sleep window used to leave DSH orphaned forever — the app exited
// before the child existed, so nothing ever killed it. shutdown must instead
// WAIT for the in-flight spawn and then kill the registered tree.
func TestShutdownWaitsForInFlightLaunchAndKills(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	marker := uniquePingMarker()
	overrideCommand(t, marker)
	app := newShutdownTestApp(t)

	// Simulate RunAutoStartInstances' protected staggered launch.
	stagger := 400 * time.Millisecond
	app.inFlight.Add(1)
	go func() {
		defer app.inFlight.Done()
		time.Sleep(stagger) // stagger window
		_ = app.LaunchInstance("t1")
	}()

	time.Sleep(50 * time.Millisecond) // shutdown races in BEFORE the spawn

	start := time.Now()
	shutdownDone := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not return within 10s")
	}
	elapsed := time.Since(start)
	// The fix means shutdown must have BLOCKED past the stagger window so the
	// child existed by the time it reaped it.
	if elapsed < stagger-100*time.Millisecond {
		t.Fatalf("shutdown returned in %v (< stagger %v): it did not wait for the in-flight spawn", elapsed, stagger)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if pingAlive(marker) == 0 {
			return // killed by shutdown: bug fixed
		}
		if time.Now().After(deadline) {
			t.Fatalf("ORPHAN REPRODUCED: ping(=%s) still alive after shutdown returned", marker)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitPingAlive polls until the marker'd ping appears, or times out.
func waitPingAlive(t *testing.T, marker string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if pingAlive(marker) > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Baseline: shutdown kills an already-running instance.
func TestShutdownKillsRunningInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	marker := uniquePingMarker()
	overrideCommand(t, marker)
	app := newShutdownTestApp(t)

	if err := app.LaunchInstance("t1"); err != nil {
		t.Fatalf("LaunchInstance: %v", err)
	}
	if !waitPingAlive(t, marker) {
		t.Fatal("process not observed running before shutdown")
	}

	app.shutdown(context.Background())

	deadline := time.Now().Add(5 * time.Second)
	for {
		if pingAlive(marker) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping(=%s) still alive after shutdown", marker)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
