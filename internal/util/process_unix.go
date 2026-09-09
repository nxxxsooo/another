//go:build darwin || linux

package util

import (
	"errors"
	"os"
	"syscall"
)

// ProcessLiveness asks the kernel about a pid with signal 0, which runs the
// permission checks and then delivers nothing.
func ProcessLiveness(pid int) Liveness {
	if pid <= 0 {
		return ProcessGone
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return ProcessGone
	}
	switch err := proc.Signal(syscall.Signal(0)); {
	case err == nil:
		return ProcessRunning
	// Not being allowed to signal it is an answer too: it exists, it just is
	// not this user's to touch.
	case errors.Is(err, syscall.EPERM):
		return ProcessRunning
	case errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrProcessDone):
		return ProcessGone
	// Anything else is a failure to ask, not an answer.
	default:
		return ProcessUnknown
	}
}
