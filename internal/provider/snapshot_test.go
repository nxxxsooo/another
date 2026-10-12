package provider_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/provider"
)

func TestFileSnapshotRestoresBytesModeAndTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	want := []byte("{\"session\":\"bytes\"}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2024, 3, 1, 8, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	snap, err := provider.SnapshotFileSession(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := provider.DecodeSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := provider.RestoreFileSession(decoded); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("restored bytes = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(when) {
		t.Fatalf("restored mtime = %v, want %v", info.ModTime(), when)
	}
}

// A restore must never clobber a session that is present again: the agent has
// moved on and the old bytes are no longer the truth there.
func TestRestoreRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess.jsonl")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := provider.SnapshotFileSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.RestoreFileSession(snap); err == nil {
		t.Fatal("restore overwrote an existing session")
	}
}
