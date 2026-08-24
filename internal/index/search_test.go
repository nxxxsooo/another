package index_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/model"
)

func TestFullTextSearchAndSubagentDefault(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	root := model.Summary{ID: "root", Provider: "codex", Title: "boring", Kind: model.SessionKindRoot,
		UpdatedAt: now, StoragePath: "/root.jsonl", SourceMtime: 1}
	child := model.Summary{ID: "child", Provider: "codex", Title: "hidden keyword", Kind: model.SessionKindSubagent,
		ParentID: "root", UpdatedAt: now.Add(time.Second), StoragePath: "/child.jsonl", SourceMtime: 1}
	for _, sm := range []model.Summary{root, child} {
		if err := store.Upsert(sm); err != nil {
			t.Fatal(err)
		}
	}
	conv := &model.Conversation{Messages: []model.Message{
		{Role: model.RoleSystem, Content: "secret-system-only"},
		{Role: model.RoleUser, Content: "Find the NeedleWord in this session"},
		{Role: model.RoleAssistant, Content: "Unicode jawaban tersedia"},
		{Role: model.RoleTool, Content: "secret-tool-only"},
	}}
	if err := store.IndexConversation(root, conv); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search(index.SearchOpts{Query: "needleword"})
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "root" {
		t.Fatalf("content search: hits=%+v err=%v", hits, err)
	}
	hits, err = store.Search(index.SearchOpts{Query: "secret-system-only"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("system text must not be indexed: hits=%+v err=%v", hits, err)
	}
	if n, err := store.Count(index.ListOpts{}); err != nil || n != 1 {
		t.Fatalf("root default count=%d err=%v", n, err)
	}
	if n, err := store.Count(index.ListOpts{IncludeSubagents: true}); err != nil || n != 2 {
		t.Fatalf("subagent opt-in count=%d err=%v", n, err)
	}
}

func TestOpenPreservesExistingCustomParentPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "index.db")
	store, err := index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetMeta("permission-test", "1"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path string
		perm os.FileMode
	}{{dir, 0o755}, {path, 0o600}, {path + "-wal", 0o600}, {path + "-shm", 0o600}} {
		info, err := os.Stat(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != item.perm {
			t.Fatalf("%s permissions = %o, want %o", item.path, got, item.perm)
		}
	}
}

func TestOpenSecuresOwnedAndCreatedDirectories(t *testing.T) {
	t.Run("app cache", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", root)
		dir := filepath.Join(root, "agenthop")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		store, err := index.Open("")
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertMode(t, dir, 0o700)
	})

	t.Run("created custom parent", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "new", "private")
		store, err := index.Open(filepath.Join(dir, "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertMode(t, dir, 0o700)
	})
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions = %o, want %o", path, got, want)
	}
}

func TestSearchRejectsConcurrentStaleCanonicalContent(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old := model.Summary{
		ID: "same", Provider: "cursor", Title: "plain", StoragePath: "/old.jsonl",
		UpdatedAt: time.Unix(100, 0), MessageCount: 1, SourceMtime: 10, SourceSize: 20,
	}
	current := old
	current.StoragePath = "/current.db"
	current.UpdatedAt = old.UpdatedAt.Add(time.Second)
	current.SourceMtime++
	current.SourceSize++
	if err := store.Upsert(old); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(current); err != nil {
		t.Fatal(err)
	}
	// Simulate an old load completing after the canonical source changed.
	if err := store.IndexConversation(old, &model.Conversation{Messages: []model.Message{{
		Role: model.RoleUser, Content: "concurrent stale needle",
	}}}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search(index.SearchOpts{Query: "concurrent stale needle"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale canonical content returned: hits=%+v err=%v", hits, err)
	}
}

func TestSearchFiltersBeforePagination(t *testing.T) {
	store, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	indexBody := func(sm model.Summary) {
		t.Helper()
		if err := store.Upsert(sm); err != nil {
			t.Fatal(err)
		}
		if err := store.IndexConversation(sm, &model.Conversation{Messages: []model.Message{{Role: model.RoleUser, Content: "paginationneedle"}}}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Unix(1000, 0)
	for i := 0; i < 25; i++ {
		indexBody(model.Summary{
			ID: fmt.Sprintf("distractor-%02d", i), Provider: "cursor", ProjectPath: "/other",
			Title: "plain", UpdatedAt: base.Add(time.Duration(100-i) * time.Second),
			StoragePath: fmt.Sprintf("/other/%02d", i), SourceMtime: int64(i + 1),
		})
	}
	for i := 0; i < 3; i++ {
		indexBody(model.Summary{
			ID: fmt.Sprintf("wanted-%d", i), Provider: "codex", ProjectPath: "/wanted/project",
			Title: "plain", UpdatedAt: base.Add(time.Duration(3-i) * time.Second),
			StoragePath: fmt.Sprintf("/wanted/%d", i), SourceMtime: int64(i + 1),
		})
	}
	indexBody(model.Summary{
		ID: "wanted-child", Provider: "codex", ProjectPath: "/wanted/project", Kind: model.SessionKindSubagent,
		UpdatedAt: base.Add(10 * time.Second), StoragePath: "/wanted/child", SourceMtime: 10,
	})

	opts := index.SearchOpts{Query: "paginationneedle", Provider: "codex", ProjectFilter: "/wanted", Limit: 1, Offset: 1}
	hits, err := store.Search(opts)
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "wanted-1" {
		t.Fatalf("filtered later page: hits=%+v err=%v", hits, err)
	}
	opts.Offset = -10
	hits, err = store.Search(opts)
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "wanted-0" {
		t.Fatalf("negative offset: hits=%+v err=%v", hits, err)
	}
}
