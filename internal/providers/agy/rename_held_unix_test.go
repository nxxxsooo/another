//go:build darwin || linux

package agy_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/agy"
)

// TestRenameSucceedsWhileAnotherProcessHoldsThePresenceLock is the refusal
// people actually hit: a live AGY — sometimes one that outlived the terminal
// that started it — holding the conversation's presence lock. That lock still
// stops a delete, because a delete removes files AGY is writing. It no longer
// stops a rename, which replaces an annotation AGY does not hold open.
func TestRenameSucceedsWhileAnotherProcessHoldsThePresenceLock(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000c1"
	writeFullConversation(t, root, id, "old name")
	holdPresenceLock(t, root, id)

	if err := agy.New().RenameSession(context.Background(), provider.SessionRef{ID: id}, "new name"); err != nil {
		t.Fatalf("RenameSession error = %v", err)
	}
	if got := pTitle(t, root, id); got != "new name" {
		t.Fatalf("title = %q, want %q", got, "new name")
	}
}

// TestDeleteStillRefusesAHeldConversation is the other half of the split: the
// same held lock that a rename now works beside keeps a delete out.
func TestDeleteStillRefusesAHeldConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000c2"
	path := writeFullConversation(t, root, id, "old name")
	holdPresenceLock(t, root, id)

	if err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: path}); err == nil {
		t.Fatal("DeleteSession removed a conversation another process holds open")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a refused delete removed the transcript: %v", err)
	}
}

// holdPresenceLock takes the conversation's presence lock on its own descriptor
// and keeps it for the rest of the test. flock conflicts between two open files
// even inside one process, so this stands in for the AGY process holding the
// conversation.
func holdPresenceLock(t *testing.T, root, id string) {
	t.Helper()
	path := filepath.Join(root, "presence", id+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Skipf("this platform cannot hold the presence lock: %v", err)
	}
}
