//go:build windows

package main

import (
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

// TestTrayLoopStaysOnLockedOSThread guards the fix for the tray icon silently
// going dead after the app has been in the tray for a while: left-click and
// right-click both stop responding while the main window still works.
//
// Root cause: fyne.io/systray's Win32 hidden-window message loop only receives
// messages on the OS thread that created the window. Running systray.Run on an
// unlocked goroutine lets the Go scheduler migrate the loop to another thread,
// after which GetMessage blocks on the wrong thread's queue and the tray goes
// unresponsive. startTray therefore wraps the loop in runSystrayLoop, which
// pins it to one OS thread via runtime.LockOSThread.
func TestTrayLoopStaysOnLockedOSThread(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs multiple OS threads to exercise scheduler migration")
	}

	started := make(chan struct{})
	finished := make(chan struct{})
	var firstThread uint32
	stable := true

	// Noisy goroutines keep the scheduler churning so a yielded goroutine is
	// likely to be stolen onto another OS thread (which would freeze systray).
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200000; j++ {
				runtime.Gosched()
			}
		}()
	}

	go runSystrayLoop(func() {
		firstThread = windows.GetCurrentThreadId()
		close(started)
		for i := 0; i < 2000; i++ {
			runtime.Gosched()
			if windows.GetCurrentThreadId() != firstThread {
				stable = false
				break
			}
		}
		close(finished)
	})

	<-started
	<-finished
	if !stable {
		t.Fatal("tray loop goroutine migrated to a different OS thread; the systray Win32 message pump would freeze and the tray icon would go unresponsive")
	}
}
