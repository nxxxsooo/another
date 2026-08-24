package migrate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/providers/codex"
)

func TestFindDuplicateByOriginWhenDigestChanges(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("CODEX_HOME", dir)
	p := codex.New()
	origin := &model.Conversation{
		ID: "20260628_151621_cd7bdb", Provider: "hermes",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}
	write, err := p.Write(context.Background(), origin, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordMigrationSnapshot(p.ID(), model.SnapshotDigest(origin), write.SessionID, write.StoragePath, origin.ID, origin.Provider, 1); err != nil {
		t.Fatal(err)
	}

	changed := &model.Conversation{
		ID: "20260628_151621_cd7bdb", Provider: "hermes",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello"},
			{Role: model.RoleAssistant, Content: "filtered tool noise"},
		},
	}
	if _, ok := migrate.FindDuplicate(store, p, changed); ok {
		t.Fatal("changed source content must create a new snapshot")
	}
}

func TestLegacyOriginFallbackRequiresMatchingMessageCount(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("CODEX_HOME", dir)
	p := codex.New()
	origin := &model.Conversation{ID: "legacy-src", Provider: "hermes", Messages: []model.Message{{Role: model.RoleUser, Content: "old"}}}
	write, err := p.Write(context.Background(), origin, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	legacy := &model.MigrationMeta{Type: model.MigrationType, OriginID: origin.ID, OriginSource: origin.Provider, OriginMessageCount: 1}
	body, err := os.ReadFile(write.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	marker, err := json.Marshal(map[string]any{"type": model.MigrationType, "data": legacy})
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = string(marker)
	if err := os.WriteFile(write.StoragePath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(model.Summary{ID: write.SessionID, Provider: p.ID(), StoragePath: write.StoragePath, Migration: legacy}); err != nil {
		t.Fatal(err)
	}
	sameCount := &model.Conversation{ID: origin.ID, Provider: origin.Provider, Messages: []model.Message{{Role: model.RoleUser, Content: "changed but legacy cannot know"}}}
	if _, ok := migrate.FindDuplicate(store, p, sameCount); !ok {
		t.Fatal("legacy marker with matching message count should dedup")
	}
	more := &model.Conversation{ID: origin.ID, Provider: origin.Provider, Messages: []model.Message{{Role: model.RoleUser, Content: "old"}, {Role: model.RoleAssistant, Content: "new"}}}
	if _, ok := migrate.FindDuplicate(store, p, more); ok {
		t.Fatal("legacy marker with changed message count must create a new snapshot")
	}
}

func TestFindDuplicateDistinguishesContextMode(t *testing.T) {
	dir := t.TempDir()
	store, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("CODEX_HOME", dir)
	p := codex.New()
	origin := &model.Conversation{ID: "mode-source", Provider: "cursor", Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	recentMeta := model.NewMigrationMeta(origin)
	recentMeta.ContextMode = string(migrate.ContextRecent)
	recentMeta.OriginDigest = model.MigrationContextDigest(origin, string(migrate.ContextRecent))
	recent := *origin
	recent.WriteMigration = &recentMeta
	write, err := p.Write(context.Background(), &recent, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordMigrationSnapshot(p.ID(), recentMeta.OriginDigest, write.SessionID, write.StoragePath, origin.ID, origin.Provider, len(origin.Messages)); err != nil {
		t.Fatal(err)
	}
	if _, ok := migrate.FindDuplicate(store, p, &recent); !ok {
		t.Fatal("same context mode did not deduplicate")
	}
	fullMeta := model.NewMigrationMeta(origin)
	fullMeta.ContextMode = string(migrate.ContextFull)
	fullMeta.OriginDigest = model.MigrationContextDigest(origin, string(migrate.ContextFull))
	full := *origin
	full.WriteMigration = &fullMeta
	if _, ok := migrate.FindDuplicate(store, p, &full); ok {
		t.Fatal("full migration reused recent target")
	}
}
