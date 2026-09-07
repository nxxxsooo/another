package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/registry"
)

// A rollout file does not change when another's reading of it changes, so the
// rows written before another could read event_msg-only Codex threads would
// keep their wrong title and count forever: every incremental scan skips a
// file whose mtime and size still match. The one-time re-summarize is what
// repairs them.
func TestCodexWireRebuildRepairsRowsAnIncrementalScanWouldSkip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, "rollout-2025-06-01T10-00-00-0197f8a1-2b3c-7d4e-8f90-abcdef123456.jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2025-06-01T10:00:00Z","type":"session_meta","payload":{"id":"0197f8a1-2b3c-7d4e-8f90-abcdef123456","cwd":"/demo","agent_nickname":"Wegener the 9th","agent_path":"/root/audit/api_definitions"}}`,
		`{"timestamp":"2025-06-01T10:00:01Z","type":"event_msg","payload":{"type":"agent_message","message":"Inventoried the API definitions."}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := registry.New()
	ctx := context.Background()
	if _, err := Rebuild(ctx, reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(ListOpts{Provider: "codex", IncludeSubagents: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("indexed rows = %d err=%v", len(items), err)
	}

	// Stand in for a row an older build wrote: the thread read as empty.
	stale := items[0]
	stale.MessageCount = 0
	stale.Title = "~/demo"
	if err := store.Upsert(stale); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta(codexWireSchemaKey, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta("last_update", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if !NeedsIncrementalIndex(reg, store, time.Hour) {
		t.Fatal("a fresh index still owing the codex re-summarize must ask for a scan")
	}

	if _, err := UpdateIncremental(ctx, reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(ListOpts{Provider: "codex", IncludeSubagents: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("rows after update = %d err=%v", len(items), err)
	}
	if items[0].MessageCount != 1 {
		t.Fatalf("message count = %d, want the agent_message turn", items[0].MessageCount)
	}
	if items[0].Title != "api_definitions · Wegener the 9th" {
		t.Fatalf("title = %q, want the subagent identity", items[0].Title)
	}
	// Settling the debt is what keeps the rebuild from repeating on every
	// command; the scan itself stays as cheap as it was.
	if codexRebuildOwed(store) {
		t.Fatal("re-summarize stayed owed after it ran")
	}
}
