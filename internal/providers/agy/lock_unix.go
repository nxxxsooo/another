//go:build darwin || linux

package agy

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireConversationLock uses the same advisory presence lock AGY holds while
// a conversation is open. The caller keeps the returned release function until
// its entire lifecycle mutation has completed, closing the check/action race.
func acquireConversationLock(path string) (release func() error, active bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, false, nil
}
