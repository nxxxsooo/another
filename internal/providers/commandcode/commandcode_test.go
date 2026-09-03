package commandcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
)

func TestWriteRoundTripDoesNotMutateAndCleanup(t *testing.T) {
	p := &Provider{root: t.TempDir()}
	start := time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC)
	conv := &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: "/old", Title: "Preserved title", CreatedAt: start,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello", Timestamp: start},
			{Role: model.RoleAssistant, Content: "world"},
		},
	}
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: "/new path"})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Provider != "codex" || conv.ProjectPath != "/old" {
		t.Fatalf("source mutated: %+v", conv)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: write.SessionID, StoragePath: write.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "hello" || loaded.Messages[1].Content != "world" || loaded.ProjectPath != "/new path" || loaded.Title != conv.Title {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.Messages[1].Timestamp.IsZero() {
		t.Fatal("zero source timestamp was not replaced")
	}
	checkpoint := strings.TrimSuffix(write.StoragePath, ".jsonl") + ".checkpoints.jsonl"
	if err := os.WriteFile(checkpoint, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].ID != write.SessionID {
		t.Fatalf("checkpoint was indexed: %+v", sums)
	}
	if err := p.CleanupWrite(context.Background(), *write); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(write.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("session remains: %v", err)
	}
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("cleanup removed unowned checkpoint: %v", err)
	}
}

func TestCleanupRefusesPathsOutsideProjectsRoot(t *testing.T) {
	root := t.TempDir()
	p := &Provider{root: filepath.Join(root, "home")}
	outside := filepath.Join(root, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := p.CleanupWrite(context.Background(), provider.WriteResult{StoragePath: outside})
	if err == nil || !strings.Contains(err.Error(), "refusing cleanup") {
		t.Fatalf("cleanup error = %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path changed: %v", err)
	}
}

func TestMetadataWriteJoinsCleanupFailure(t *testing.T) {
	p := &Provider{root: t.TempDir()}
	original := writeCommandCodeFile
	t.Cleanup(func() { writeCommandCodeFile = original })
	calls := 0
	writeCommandCodeFile = func(path string, _ []byte, _ os.FileMode) error {
		calls++
		if calls == 2 {
			return errors.New("metadata failed")
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600)
	}
	_, err := p.Write(context.Background(), &model.Conversation{ID: "origin", Provider: "codex", ProjectPath: "/project", Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}, provider.WriteOpts{})
	if err == nil || !strings.Contains(err.Error(), "metadata failed") || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("joined metadata error = %v", err)
	}
}

func TestResumeCommandShellQuotes(t *testing.T) {
	p := &Provider{}
	got := p.ResumeCommand(provider.WriteResult{SessionID: "id; bad", ProjectPath: "/tmp/a b"})
	if got != "cd '/tmp/a b' && commandcode --resume 'id; bad'" {
		t.Fatalf("resume = %q", got)
	}
}

func TestDiscoverExcludesCheckpointOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-tmp-project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "only.checkpoints.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Provider{root: root}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("summaries=%+v err=%v", sums, err)
	}
}
