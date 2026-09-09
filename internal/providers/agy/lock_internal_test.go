//go:build darwin || linux

package agy

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The refusal has to name what is holding the conversation. A reader who
// closed the window an hour ago cannot act on "currently active": the process
// that outlived it is the thing to quit, and only the message can point there.
func TestActiveRefusalNamesTheLockHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presence", "held.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// Held on its own descriptor for the whole test: flock conflicts between
	// two open files even inside one process, which is what lets this stand in
	// for the AGY process that outlived its terminal.
	held, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not hold the presence lock: %v", err)
	}

	release, active, err := acquireConversationLock(path)
	if err != nil {
		t.Skipf("this platform cannot hold the presence lock: %v", err)
	}
	if !active {
		if release != nil {
			_ = release()
		}
		t.Skip("presence lock was not contended")
	}

	text := activeHolderText(path)
	if strings.Contains(text, "pid ") {
		return
	}
	// Without lsof there is no holder to name, and the message falls back to
	// the lock's path — still a place to look, which the bare id never was.
	if !strings.Contains(text, path) {
		t.Fatalf("refusal names neither a holder nor the lock: %q", text)
	}
}
