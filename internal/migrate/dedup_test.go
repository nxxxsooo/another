package migrate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/providers/claude"
)

func TestFindExistingMigration(t *testing.T) {
	dir := t.TempDir()
	conv := &model.Conversation{
		ID: "src-1", Provider: "claude-code",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello dedup"}},
	}
	meta := model.NewMigrationMeta(conv)
	path := filepath.Join(dir, "rollout-test-abc.jsonl")
	line := `{"type":"agenthop_migration","data":` + mustJSON(meta) + `}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := migrate.FindExistingMigration(path, meta.OriginDigest)
	if !ok || got != path {
		t.Fatalf("FindExistingMigration = %q ok=%v", got, ok)
	}
}

func TestFindDuplicateDoesNotScanProviderFolders(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sessions", "2026", "06", "24")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	conv := &model.Conversation{
		ID: "src-2", Provider: "claude-code",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "migrate me"},
			{Role: model.RoleAssistant, Content: "ok"},
		},
	}
	meta := model.NewMigrationMeta(conv)
	path := filepath.Join(sub, "rollout-2026-06-24-deadbeef.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"agenthop_migration","data":`+mustJSON(meta)+`}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if _, ok := migrate.FindDuplicate(nil, claude.New(), conv); ok {
		t.Fatal("dedup must use the index instead of scanning provider folders")
	}
}

func TestFindDuplicateIndex(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conv := &model.Conversation{
		ID: "src-3", Provider: "claude-code",
		Messages: []model.Message{{Role: model.RoleUser, Content: "indexed dedup"}},
	}
	digest := model.SnapshotDigest(conv)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	p := claude.New()
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.RecordMigrationSnapshot(p.ID(), digest, write.SessionID, write.StoragePath, "src-3", "claude-code", len(conv.Messages))
	wr, ok := migrate.FindDuplicate(store, p, conv)
	if !ok {
		t.Fatal("expected index duplicate")
	}
	if wr.SessionID != write.SessionID {
		t.Fatalf("session id = %q", wr.SessionID)
	}
	// A dedup record pointing at a deleted target must not count as a duplicate.
	conv2 := &model.Conversation{
		ID: "src-4", Provider: "claude-code",
		Messages: []model.Message{{Role: model.RoleUser, Content: "stale dedup"}},
	}
	_ = store.RecordMigrationSnapshot(p.ID(), model.SnapshotDigest(conv2), "gone", filepath.Join(dir, "missing.jsonl"), "src-4", "claude-code", 1)
	if _, ok := migrate.FindDuplicate(store, p, conv2); ok {
		t.Fatal("stale index record with deleted target must not dedup")
	}
}

func TestFindDuplicateDoesNotCollideAcrossSourceSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	dst := claude.New()
	first := &model.Conversation{
		ID: "source-a", Provider: "codex",
		Messages: []model.Message{{Role: model.RoleUser, Content: "identical"}},
	}
	write, err := dst.Write(context.Background(), first, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordMigrationSnapshot(dst.ID(), model.SnapshotDigest(first), write.SessionID, write.StoragePath, first.ID, first.Provider, 1); err != nil {
		t.Fatal(err)
	}
	second := &model.Conversation{
		ID: "source-b", Provider: "codex",
		Messages: first.Messages,
	}
	if _, ok := migrate.FindDuplicate(store, dst, second); ok {
		t.Fatal("distinct source sessions with identical text must not dedup")
	}
	if _, ok := migrate.FindDuplicate(store, dst, first); !ok {
		t.Fatal("unchanged source snapshot should dedup")
	}
}

func TestFindDuplicateRejectsLoadableTargetWithWrongMarker(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	dst := claude.New()
	wanted := &model.Conversation{
		ID: "wanted", Provider: "codex",
		Messages: []model.Message{{Role: model.RoleUser, Content: "same content"}},
	}
	other := &model.Conversation{
		ID: "other", Provider: "codex", Messages: wanted.Messages,
	}
	write, err := dst.Write(context.Background(), other, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordMigrationSnapshot(dst.ID(), model.SnapshotDigest(wanted), write.SessionID, write.StoragePath, wanted.ID, wanted.Provider, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := migrate.FindDuplicate(store, dst, wanted); ok {
		t.Fatal("loadable target with a different origin marker must not dedup")
	}
}

type missingRowProvider struct{ path string }

func (p missingRowProvider) ID() string                                { return "db-target" }
func (p missingRowProvider) DisplayName() string                       { return "DB Target" }
func (p missingRowProvider) DefaultPaths() []provider.PathSpec         { return nil }
func (p missingRowProvider) Installed() bool                           { return true }
func (p missingRowProvider) SupportsResume() bool                      { return false }
func (p missingRowProvider) ResumeCommand(provider.WriteResult) string { return "" }
func (p missingRowProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return nil, nil
}
func (p missingRowProvider) Load(context.Context, provider.SessionRef) (*model.Conversation, error) {
	return nil, provider.ErrNotFound
}
func (p missingRowProvider) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, provider.ErrNotInstalled
}

func TestFindDuplicateValidatesExactDatabaseRow(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dbPath := filepath.Join(dir, "target.db")
	if err := os.WriteFile(dbPath, []byte("database exists but row does not"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv := &model.Conversation{ID: "src", Provider: "codex", Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	p := missingRowProvider{path: dbPath}
	if err := store.RecordMigrationSnapshot(p.ID(), model.SnapshotDigest(conv), "missing-row", dbPath+"#missing-row", conv.ID, conv.Provider, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := migrate.FindDuplicate(store, p, conv); ok {
		t.Fatal("database file existence must not count as target row existence")
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
