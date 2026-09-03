package codex_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestDiscoverCancellationPrecedesSkip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, sessions, "rollout-2025-06-01T10-00-00-0197f8a1-2b3c-7d4e-8f90-abcdef123456.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	skipCalls := 0
	_, err := codex.New().Discover(ctx, provider.DiscoverOpts{SkipSource: func(string, int64, int64) bool {
		skipCalls++
		return true
	}})
	if !errors.Is(err, context.Canceled) || skipCalls != 0 {
		t.Fatalf("err=%v skip calls=%d", err, skipCalls)
	}
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

func TestDiscoverUsesDesktopTitleAndTreatsItAsUserVisible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "0197f8a1-2b3c-7d4e-8f90-abcdef123456"
	path := filepath.Join(sessions, "rollout-2025-06-01T10-00-00-"+id+".jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2025-06-01T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/project","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent"}}}}}`,
		`{"timestamp":"2025-06-01T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"<recommended_plugins> injected transport"}}`,
		`{"timestamp":"2025-06-01T10:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"real but long fallback title"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	indexRow := `{"id":"` + id + `","thread_name":"GUI short title","updated_at":"2025-06-01T11:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(indexRow), 0o600); err != nil {
		t.Fatal(err)
	}

	p := codex.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("summaries = %d, want 1", len(sums))
	}
	if sums[0].Title != "GUI short title" || sums[0].Kind != "root" || sums[0].ParentID != "" {
		t.Fatalf("desktop title was not authoritative: %+v", sums[0])
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: id, StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "GUI short title" {
		t.Fatalf("loaded title = %q, want GUI title", conv.Title)
	}
}

func TestDiscoverSkipUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeRollout(t, sessions, "rollout-2025-06-01T10-00-00-0197f8a1-2b3c-7d4e-8f90-abcdef123456.jsonl")
	p := codex.New()
	skipped := 0
	var gotMtime int64
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{
		SkipUnchanged: func(storagePath string, mtime int64) bool {
			skipped++
			gotMtime = mtime
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skip callback calls = %d, want 1", skipped)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMtime != info.ModTime().Unix() {
		t.Fatalf("skip mtime = %d, want Unix seconds %d", gotMtime, info.ModTime().Unix())
	}
	if len(sums) != 0 {
		t.Fatalf("summaries = %d, want 0 when all files skipped", len(sums))
	}
	var preciseMtime, preciseSize int64
	if _, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, mtime, size int64) bool {
		preciseMtime, preciseSize = mtime, size
		return true
	}}); err != nil {
		t.Fatal(err)
	}
	if preciseMtime != info.ModTime().UnixNano() || preciseSize != info.Size() {
		t.Fatalf("precise fingerprint = (%d, %d), want (%d, %d)",
			preciseMtime, preciseSize, info.ModTime().UnixNano(), info.Size())
	}
}
