//go:build !windows

package main

import "errors"

var errNoJob = errors.New("job object unavailable")

// winJob is a no-op outside Windows: killProcessTree (a process-group kill,
// thanks to Setsid) is the child-reaping mechanism there.
type winJob struct{}

func newKillOnCloseJob() *winJob       { return nil }
func (j *winJob) assign(uintptr) error { return errNoJob }
func (j *winJob) close()               {}

// openProcessForJob is unsupported outside Windows.
func openProcessForJob(pid int) (uintptr, error) { return 0, errNoJob }
