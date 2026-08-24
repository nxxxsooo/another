package migrate_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/providers/claude"
	"github.com/CyrusSE/agenthop/internal/registry"
)

func TestEngineVerifiesAndSnapshotsChangedSource(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	codexRoot := filepath.Join(root, "codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", codexRoot)
	if err := os.MkdirAll(filepath.Join(codexRoot, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	sourceProvider := claude.New()
	source := &model.Conversation{
		ID: "original", Provider: claude.ProviderID, ProjectPath: "/tmp/project",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello", Timestamp: time.Unix(1, 0)},
			{Role: model.RoleAssistant, Content: "world", Timestamp: time.Unix(2, 0)},
		},
	}
	written, err := sourceProvider.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := index.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Upsert(model.Summary{
		ID: written.SessionID, Provider: claude.ProviderID, ProjectPath: written.ProjectPath,
		StoragePath: written.StoragePath, Kind: model.SessionKindRoot,
	}); err != nil {
		t.Fatal(err)
	}
	engine := &migrate.Engine{Registry: registry.New(), Index: store}
	first, err := engine.Run(context.Background(), migrate.Options{SessionID: written.SessionID, ToProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AlreadyExists || len(first.Warnings) != 0 {
		t.Fatalf("first migration = %+v", first)
	}
	second, err := engine.Run(context.Background(), migrate.Options{SessionID: written.SessionID, ToProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadyExists || second.Write.SessionID != first.Write.SessionID {
		t.Fatalf("exact snapshot not reused: first=%+v second=%+v", first.Write, second.Write)
	}
	f, err := os.OpenFile(written.StoragePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintln(f, `{"type":"user","sessionId":"`+written.SessionID+`","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"new turn"}}`)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append=%v close=%v", err, closeErr)
	}
	changed, err := engine.Run(context.Background(), migrate.Options{SessionID: written.SessionID, ToProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.AlreadyExists || changed.Write.SessionID == first.Write.SessionID {
		t.Fatalf("changed source should create a new target: %+v", changed)
	}
}
