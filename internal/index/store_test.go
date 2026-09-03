package index_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/config"
	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/registry"
)

func TestStoreUpsertList(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	sm := model.Summary{
		ID: "abc-123", Provider: "codex", Title: "test session",
		UpdatedAt: now, MessageCount: 5, StoragePath: "/tmp/x.jsonl", SourceMtime: now.Unix(),
	}
	if err := store.Upsert(sm); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(index.ListOpts{Provider: "codex"})
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %d items err=%v", len(items), err)
	}
	if items[0].ID != "abc-123" {
		t.Fatalf("id = %q", items[0].ID)
	}
}

func TestOpenPropagatesSessionSourceBackfillFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken-upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE sessions (
  id TEXT NOT NULL, provider TEXT NOT NULL, project_path TEXT, title TEXT,
  created_at INTEGER, updated_at INTEGER, message_count INTEGER, storage_path TEXT NOT NULL,
  source_mtime INTEGER NOT NULL, kind TEXT NOT NULL DEFAULT 'root', parent_id TEXT,
  source_size INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (provider, id)
);
INSERT INTO sessions VALUES ('s1', 'codex', '/project', 'title', 1, 2, 1, '/session', 3, 'root', NULL, 4);
CREATE TABLE session_sources (provider TEXT NOT NULL, id TEXT NOT NULL);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(path)
	if store != nil {
		store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "storage_path") {
		t.Fatalf("Open error = %v, want backfill failure", err)
	}
}

func TestUpdateIncrementalPreservesFailedScanAndRemovesUninstalledProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, "rollout-2025-06-01T10-00-00-0197f8a1-2b3c-7d4e-8f90-abcdef123456.jsonl")
	line := `{"timestamp":"2025-06-01T10:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := registry.New()
	if n, err := index.Rebuild(context.Background(), reg, store, "codex"); err != nil || n != 1 {
		t.Fatalf("initial rebuild: n=%d err=%v", n, err)
	}
	items, err := store.List(index.ListOpts{Provider: "codex", IncludeSubagents: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("initial list: items=%d err=%v", len(items), err)
	}
	if err := store.IndexConversation(items[0], &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "indexed before removal"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-rollout.jsonl", path); err != nil {
		t.Fatal(err)
	}
	if _, err := index.UpdateIncremental(context.Background(), reg, store, "codex"); err == nil {
		t.Fatal("expected discovery failure")
	}
	if n, err := store.Count(index.ListOpts{Provider: "codex", IncludeSubagents: true}); err != nil || n != 1 {
		t.Fatalf("failed scan destroyed prior index: n=%d err=%v", n, err)
	}
	if hits, err := store.Search(index.SearchOpts{Query: "indexed before removal"}); err != nil || len(hits) != 1 {
		t.Fatalf("failed scan destroyed prior search index: hits=%d err=%v", len(hits), err)
	}
	if err := os.RemoveAll(sessions); err != nil {
		t.Fatal(err)
	}
	if _, err := index.UpdateIncremental(context.Background(), reg, store, "codex"); err != nil {
		t.Fatal(err)
	}
	if n, err := store.Count(index.ListOpts{Provider: "codex", IncludeSubagents: true}); err != nil || n != 0 {
		t.Fatalf("uninstalled provider rows remain: n=%d err=%v", n, err)
	}
	if hits, err := store.Search(index.SearchOpts{Query: "indexed before removal"}); err != nil || len(hits) != 0 {
		t.Fatalf("uninstalled provider search rows remain: hits=%d err=%v", len(hits), err)
	}
}

func TestRebuildPreservesOpenCodeIndexOnRowScanError(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	dir := filepath.Join(dataHome, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created, time_updated, parent_id TEXT);
CREATE TABLE message (id TEXT, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (message_id TEXT, data TEXT, time_created INTEGER);
INSERT INTO session VALUES
  ('keep', '/project', 'keep', 1000, 1000, NULL),
  ('drop', '/project', 'drop', 2000, 2000, NULL);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := registry.New()
	if n, err := index.Rebuild(context.Background(), reg, store, "opencode"); err != nil || n != 2 {
		t.Fatalf("initial rebuild: n=%d err=%v", n, err)
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session SET time_created='not-a-number' WHERE id='drop'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(context.Background(), reg, store, "opencode"); err == nil {
		t.Fatal("expected row scan error")
	}
	if n, err := store.Count(index.ListOpts{Provider: "opencode", IncludeSubagents: true}); err != nil || n != 2 {
		t.Fatalf("failed scan pruned index: n=%d err=%v", n, err)
	}
}

func TestRebuildPreservesHermesIndexOnRowScanError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HERMES_HOME", home)
	dbPath := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL,
  message_count INTEGER DEFAULT 0, title TEXT, cwd TEXT,
  parent_session_id TEXT, archived INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE messages (
  id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT,
  active INTEGER NOT NULL DEFAULT 1
);
INSERT INTO sessions (id, source, started_at, title) VALUES
  ('keep', 'cli', 1000, 'keep'),
  ('drop', 'cli', 2000, 'drop');`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := registry.New()
	if n, err := index.Rebuild(context.Background(), reg, store, "hermes"); err != nil || n != 2 {
		t.Fatalf("initial rebuild: n=%d err=%v", n, err)
	}
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET started_at='not-a-number' WHERE id='drop'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(context.Background(), reg, store, "hermes"); err == nil {
		t.Fatal("expected row scan error")
	}
	if n, err := store.Count(index.ListOpts{Provider: "hermes", IncludeSubagents: true}); err != nil || n != 2 {
		t.Fatalf("failed scan pruned index: n=%d err=%v", n, err)
	}
}

func TestNeedsRefresh(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Upsert(model.Summary{
		ID: "x", Provider: "codex", StoragePath: "/a.jsonl", SourceMtime: 100,
	})
	need, err := store.NeedsRefresh("codex", "/a.jsonl", 100)
	if err != nil || need {
		t.Fatalf("should not need refresh: need=%v err=%v", need, err)
	}
	need, _ = store.NeedsRefresh("codex", "/a.jsonl", 200)
	if !need {
		t.Fatal("should need refresh after mtime change")
	}
}

func TestFindByIDAmbiguousSuffix(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for _, id := range []string{"aaa-deadbeef", "bbb-deadbeef"} {
		if err := store.Upsert(model.Summary{
			ID: id, Provider: "codex", Title: id,
			UpdatedAt: now, StoragePath: "/tmp/" + id, SourceMtime: now.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = store.FindByID("deadbeef")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
}

func TestMigrationDedup(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordMigration("opencode", "digest-abc", "ses_123", "/db#ses_123", "src-1", "claude-code"); err != nil {
		t.Fatal(err)
	}
	sid, path, ok, err := store.FindMigration("opencode", "digest-abc")
	if err != nil || !ok || sid != "ses_123" || path != "/db#ses_123" {
		t.Fatalf("FindMigration = %q %q ok=%v err=%v", sid, path, ok, err)
	}
}
func TestGetAmbiguousSuffix(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for _, id := range []string{"aaa-deadbeef", "bbb-deadbeef"} {
		if err := store.Upsert(model.Summary{
			ID: id, Provider: "codex", Title: id,
			UpdatedAt: now, StoragePath: "/tmp/" + id, SourceMtime: now.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = store.Get("codex", "deadbeef")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
}

func TestListProjectCWD(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	for _, row := range []struct {
		id, provider, path string
	}{
		{"a", "codex", "/home/proj"},
		{"b", "claude-code", "/home/proj/sub"},
		{"c", "cursor", "/other"},
	} {
		if err := store.Upsert(model.Summary{
			ID: row.id, Provider: row.provider, ProjectPath: row.path,
			UpdatedAt: now, StoragePath: "/tmp/" + row.id, SourceMtime: now.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.Count(index.ListOpts{ProjectCWD: "/home/proj"})
	if err != nil || n != 1 {
		t.Fatalf("project cwd should be exact match only: n=%d err=%v", n, err)
	}
}

func TestFindByIDAmbiguousPrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.Upsert(model.Summary{
		ID: "prefix-old", Provider: "codex", Title: "old",
		UpdatedAt: now.Add(-48 * time.Hour), StoragePath: "/tmp/old", SourceMtime: now.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(model.Summary{
		ID: "prefix-new", Provider: "codex", Title: "new",
		UpdatedAt: now, StoragePath: "/tmp/new", SourceMtime: now.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindByID("prefix"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	sm, err := store.FindByID("…fix-new")
	if err != nil || sm.ID != "prefix-new" {
		t.Fatalf("legacy ellipsis suffix = %#v, %v", sm, err)
	}
}

func TestListPaginationAndProjectExact(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	paths := []string{"/proj/a", "/proj/b", "/other/c"}
	for i, p := range paths {
		if err := store.Upsert(model.Summary{
			ID: fmt.Sprintf("id-%d", i), Provider: "codex", Title: p,
			ProjectPath: p, UpdatedAt: now.Add(-time.Duration(i) * time.Hour),
			StoragePath: "/tmp/" + p, SourceMtime: now.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.Count(index.ListOpts{ProjectExact: "/proj/a"})
	if err != nil || n != 1 {
		t.Fatalf("count exact: n=%d err=%v", n, err)
	}

	items, err := store.List(index.ListOpts{Limit: 2, Offset: 0})
	if err != nil || len(items) != 2 {
		t.Fatalf("page 0: %d err=%v", len(items), err)
	}
	items2, err := store.List(index.ListOpts{Limit: 2, Offset: 2})
	if err != nil || len(items2) != 1 {
		t.Fatalf("page 1: %d err=%v", len(items2), err)
	}
}

func TestListProjectCWDAtHome(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	home := config.HomeDir()
	if home == "" {
		t.Skip("no home dir")
	}
	now := time.Now()
	for _, row := range []struct {
		id, path string
	}{
		{"home-only", home},
		{"home-sub", filepath.Join(home, "proj")},
		{"other", "/other"},
	} {
		if err := store.Upsert(model.Summary{
			ID: row.id, Provider: "codex", ProjectPath: row.path,
			UpdatedAt: now, StoragePath: "/tmp/" + row.id, SourceMtime: now.Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.Count(index.ListOpts{ProjectCWD: home})
	if err != nil || n != 1 {
		t.Fatalf("home cwd should match project subdirs only: n=%d err=%v", n, err)
	}
	sub := filepath.Join(home, "proj")
	n, err = store.Count(index.ListOpts{ProjectCWD: sub})
	if err != nil || n != 1 {
		t.Fatalf("project cwd should match project path: n=%d err=%v", n, err)
	}
}

func TestIndexBehindDiscoverAndStale(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, "codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	store, err := index.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	_ = store.Upsert(model.Summary{
		ID: "a", Provider: "codex", Title: "a",
		UpdatedAt: now, StoragePath: "/tmp/a", SourceMtime: now.Unix(),
	})
	_ = store.SetMeta("discover_unique:codex", "3")

	reg := registry.New()
	if !index.IndexBehindDiscover(reg, store) {
		t.Fatal("expected behind discover")
	}
	if !store.LastUpdateStale(time.Minute) {
		t.Fatal("missing last_update should be stale")
	}
	_ = store.SetMeta("last_update", time.Now().UTC().Format(time.RFC3339))
	if store.LastUpdateStale(time.Minute) {
		t.Fatal("fresh last_update should not be stale")
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cache")
	dbPath := filepath.Join(dir, "index.db")
	store, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
	_ = context.Background()
}

func TestOpenRefusesDefaultCacheSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, config.CacheDir()); err != nil {
		t.Fatal(err)
	}
	if store, err := index.Open(""); err == nil {
		store.Close()
		t.Fatal("expected default cache symlink to be refused")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("symlink target permissions = %o, want 755", got)
	}
}

func TestOpenRefusesSymlinkDatabaseFilesWithoutTouchingTargets(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "index.db")
			target := filepath.Join(t.TempDir(), "external")
			want := []byte("external data")
			if err := os.WriteFile(target, want, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, dbPath+suffix); err != nil {
				t.Fatal(err)
			}
			if store, err := index.Open(dbPath); err == nil {
				store.Close()
				t.Fatal("expected symlink index file to be refused")
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("external target changed: got %q", got)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o644 {
				t.Fatalf("external target permissions = %o, want 644", got)
			}
		})
	}
}
