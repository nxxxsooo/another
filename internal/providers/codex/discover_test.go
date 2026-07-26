package codex_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/providers/codex"
)

func writeRollout(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := `{"timestamp":"2025-06-01T10:00:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"hello codex"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Filename UUIDs contain dashes; the session id must be the full UUID, not the
// last dash-separated chunk.
func TestSessionIDFromRolloutFilename(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "0197f8a1-2b3c-7d4e-8f90-abcdef123456"
	writeRollout(t, sessions, "rollout-2025-06-01T10-00-00-"+id+".jsonl")
	p := codex.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("summaries = %d, want 1", len(sums))
	}
	if sums[0].ID != id {
		t.Fatalf("id = %q, want %q", sums[0].ID, id)
	}
}

func TestDiscoverSkipUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, sessions, "rollout-2025-06-01T10-00-00-0197f8a1-2b3c-7d4e-8f90-abcdef123456.jsonl")
	p := codex.New()
	skipped := 0
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{
		SkipUnchanged: func(storagePath string, mtime int64) bool {
			skipped++
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skip callback calls = %d, want 1", skipped)
	}
	if len(sums) != 0 {
		t.Fatalf("summaries = %d, want 0 when all files skipped", len(sums))
	}
}
