//go:build windows

package main

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNoJob = errors.New("job object unavailable")

// winJob wraps a Windows Job Object with KILL_ON_JOB_CLOSE. Every child the
// launcher spawns is assigned to one, so the whole process tree is killed by
// the kernel the moment the launcher's last handle goes away — covering hard
// deaths too: crash, Task Manager kill, power loss. This is the safety net
// behind the graceful taskkill path, not a replacement for it.
type winJob struct {
	mu     sync.Mutex
	handle windows.Handle
}

// newKillOnCloseJob creates the job; returns nil (degraded mode) when the API
// is unavailable — callers then fall back to taskkill alone.
func newKillOnCloseJob() *winJob {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil || h == 0 {
		return nil
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return nil
	}
	return &winJob{handle: h}
}

// assign puts a freshly started process into the job.
func (j *winJob) assign(processHandle uintptr) error {
	if j == nil {
		return errNoJob
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return errNoJob
	}
	return windows.AssignProcessToJobObject(j.handle, windows.Handle(processHandle))
}

// openProcessForJob opens a handle to a live process with the access rights
// AssignProcessToJobObject requires (PROCESS_SET_QUOTA | PROCESS_TERMINATE).
// The caller owns the handle.
func openProcessForJob(pid int) (uintptr, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return 0, err
	}
	return uintptr(h), nil
}

// close releases the job handle. Because the job was created with
// KILL_ON_JOB_CLOSE and still has assigned processes, the kernel terminates
// the entire tree at this point (used by the graceful-stop path as well).
func (j *winJob) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		_ = windows.CloseHandle(j.handle)
		j.handle = 0
	}
}
