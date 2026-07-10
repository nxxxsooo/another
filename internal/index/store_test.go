package index_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestFindByIDPrefixPrefersNewest(t *testing.T) {
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
	sm, err := store.FindByID("prefix")
	if err != nil {
		t.Fatal(err)
	}
	if sm.ID != "prefix-new" {
		t.Fatalf("expected newest prefix match, got %q", sm.ID)
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
