package qwen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/qwen"
)

func TestWriteLoadDiscoverRenameAndCleanup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	p := qwen.New()
	source := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/workspace/demo", Title: "Native title",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "first prompt", Timestamp: time.Date(2026, 9, 4, 10, 0, 0, 123000000, time.UTC)},
			{Role: model.RoleAssistant, Content: "first answer", Timestamp: time.Date(2026, 9, 4, 10, 0, 1, 456000000, time.UTC)},
		},
	}
	written, err := p.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "projects", "-workspace-demo", "chats"); filepath.Dir(written.StoragePath) != want {
		t.Fatalf("storage dir = %q, want %q", filepath.Dir(written.StoragePath), want)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: written.SessionID, ProjectPath: written.ProjectPath})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != source.Title || model.ContentDigest(loaded) != model.ContentDigest(source) {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.Migration == nil || loaded.Migration.OriginID != "source" || loaded.Migration.OriginSource != "codex" {
		t.Fatalf("migration marker = %+v", loaded.Migration)
	}
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{ProjectFilter: "workspace"})
	if err != nil || len(summaries) != 1 || summaries[0].ID != written.SessionID {
		t.Fatalf("Discover = %+v, %v", summaries, err)
	}
	ref := provider.SessionRef{ID: written.SessionID, StoragePath: written.StoragePath, ProjectPath: written.ProjectPath}
	if err := p.RenameSession(context.Background(), ref, "Renamed"); err != nil {
		t.Fatal(err)
	}
	renamed, err := p.Load(context.Background(), ref)
	if err != nil || renamed.Title != "Renamed" || model.ContentDigest(renamed) != model.ContentDigest(source) {
		t.Fatalf("renamed = %+v, %v", renamed, err)
	}
	if err := p.CleanupWrite(context.Background(), *written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(written.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("session survived cleanup: %v", err)
	}
}

func TestLoadUsesActiveParentChainAndDisplayText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	path := filepath.Join(root, "projects", "-p", "chats", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"uuid":"u1","parentUuid":null,"sessionId":"s1","timestamp":"2026-09-04T10:00:00Z","type":"user","cwd":"/p","version":"0.23.0","message":{"role":"user","parts":[{"text":"model-facing prompt"}]},"systemPayload":{"displayText":"visible prompt"}}`,
		`{"uuid":"a1","parentUuid":"u1","sessionId":"s1","timestamp":"2026-09-04T10:00:01Z","type":"assistant","cwd":"/p","version":"0.23.0","message":{"role":"model","parts":[{"thought":"private"},{"text":"dead answer"}]}}`,
		`{"uuid":"u2","parentUuid":"u1","sessionId":"s1","timestamp":"2026-09-04T10:00:02Z","type":"user","cwd":"/p","version":"0.23.0","message":{"role":"user","parts":[{"text":"replacement"}]}}`,
		`{"uuid":"a2","parentUuid":"u2","sessionId":"s1","timestamp":"2026-09-04T10:00:03Z","type":"assistant","cwd":"/p","version":"0.23.0","message":{"role":"model","parts":[{"thought":"hidden"},{"text":"live answer"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv, err := qwen.New().Load(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 3 || conv.Messages[0].Content != "visible prompt" || conv.Messages[1].Content != "replacement" || conv.Messages[2].Content != "live answer" {
		t.Fatalf("active projection = %+v", conv.Messages)
	}
}

func TestProviderBasics(t *testing.T) {
	t.Setenv("QWEN_HOME", t.TempDir())
	p := qwen.New()
	if p.ID() != "qwen" || p.DisplayName() != "Qwen Code" || !p.SupportsResume() {
		t.Fatalf("provider basics: %q %q %v", p.ID(), p.DisplayName(), p.SupportsResume())
	}
	got := p.ResumeCommand(provider.WriteResult{SessionID: "abc", ProjectPath: "/some/project"})
	if got != "cd '/some/project' && qwen --resume 'abc'" {
		t.Fatalf("resume command = %q", got)
	}
}

func TestCleanupRejectsOutsideStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	outside := filepath.Join(t.TempDir(), "victim.jsonl")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := qwen.New().CleanupWrite(context.Background(), provider.WriteResult{SessionID: "victim", StoragePath: outside})
	if err == nil {
		t.Fatal("cleanup accepted a path outside QWEN_HOME")
	}
}
