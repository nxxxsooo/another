package migrate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// This crosses the real migration boundary: target Write, target Load digest
// verification, migration bookkeeping, provider discovery, and local indexing.
func TestImportIntoAgyUsesVerifiedNativePath(t *testing.T) {
	root := t.TempDir()
	agyHome := filepath.Join(root, "agy")
	if err := os.MkdirAll(agyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGY_HOME", agyHome)
	idx, err := index.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	reg := registry.NewEnabled([]string{"agy"})
	engine := &migrate.Engine{Registry: reg, Index: idx}
	start := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	source := &model.Conversation{
		ID: "source", Provider: "pi", ProjectPath: filepath.Join(root, "project"), Title: "native target",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "question", Timestamp: start},
			{Role: model.RoleAssistant, Content: "answer", Timestamp: start.Add(time.Second)},
		},
	}
	result, err := engine.Import(context.Background(), source, migrate.Options{ToProvider: "agy", ContextMode: migrate.ContextFull})
	if err != nil {
		t.Fatal(err)
	}
	if result.Write == nil || result.Write.SessionID == "" || result.AlreadyExists {
		t.Fatalf("unexpected migration result: %+v", result)
	}
	target, err := reg.Get("agy")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := target.Load(context.Background(), provider.SessionRef{
		ID: result.Write.SessionID, StoragePath: result.Write.StoragePath, ProjectPath: result.Write.ProjectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(source) != model.ContentDigest(loaded) {
		t.Fatal("verified engine path changed portable conversation content")
	}
	if _, err := idx.Get("agy", result.Write.SessionID); err != nil {
		t.Fatalf("verified target was not indexed: %v", err)
	}
	cleaner, ok := target.(provider.WriteCleaner)
	if !ok {
		t.Fatal("agy target has no exact rollback contract")
	}
	if err := cleaner.CleanupWrite(context.Background(), *result.Write); err != nil {
		t.Fatal(err)
	}
}
