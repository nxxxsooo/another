package claude_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/claude"
)

// Load must drop isMeta rows and tool_result-only rows (empty text), matching
// what summarizeFile counts — otherwise migrations carry injected junk.
func TestLoadSkipsMetaAndEmptyRows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	dir := filepath.Join(root, "projects", "-home-user-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s1","isMeta":true,"message":{"role":"user","content":"Caveat: injected meta line"}}`,
		`{"type":"user","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","content":"ignored"}]}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2025-06-01T10:00:00Z","message":{"role":"user","content":"real question"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2025-06-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"real answer"}]}}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := claude.New()
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (meta + empty rows dropped)", len(conv.Messages))
	}
	if conv.Messages[0].Content != "real question" || conv.Messages[1].Content != "real answer" {
		t.Fatalf("unexpected messages: %+v", conv.Messages)
	}
}

func TestRenameAppendsNativeCustomTitle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	dir := filepath.Join(root, "projects", "-home-user-proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","sessionId":"s1","cwd":"/home/user/proj","message":{"role":"user","content":"old prompt"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := claude.New()
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path}, "native name"); err != nil {
		t.Fatal(err)
	}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(sums) != 1 || sums[0].Title != "native name" {
		t.Fatalf("renamed summary=%+v err=%v", sums, err)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path})
	if err != nil || conv.Title != "native name" {
		t.Fatalf("renamed load title=%q err=%v", conv.Title, err)
	}
}

func TestWriteMatchesClaudeResumeContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	p := claude.New()
	project := filepath.Join(root, "web_root", "a.b c")
	start := time.Date(2026, 8, 17, 1, 2, 3, 456000000, time.UTC)
	conv := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: project,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "  hello\r\n", Timestamp: start},
			{Role: model.RoleAssistant, Content: "\tworld  ", Timestamp: start.Add(time.Second)},
		},
	}
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	wantBucket := filepath.Join(root, "projects", strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, project))
	if filepath.Dir(write.StoragePath) != wantBucket {
		t.Fatalf("bucket = %q want %q", filepath.Dir(write.StoragePath), wantBucket)
	}
	b, err := os.ReadFile(write.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	var user, assistant, progress map[string]any
	for i, dst := range []*map[string]any{&user, &assistant, &progress} {
		if err := json.Unmarshal([]byte(lines[i]), dst); err != nil {
			t.Fatal(err)
		}
	}
	if user["type"] != "user" || progress["type"] != "progress" {
		t.Fatalf("first/last types = %v/%v", user["type"], progress["type"])
	}
	for _, key := range []string{"cwd", "version", "gitBranch", "isSidechain", "userType"} {
		if _, ok := user[key]; !ok {
			t.Fatalf("user row missing %s: %v", key, user)
		}
	}
	message := assistant["message"].(map[string]any)
	if message["model"] == "" || assistant["parentUuid"] != user["uuid"] {
		t.Fatalf("assistant resume contract: %v", assistant)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: write.SessionID, StoragePath: write.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(loaded) != model.ContentDigest(conv) {
		t.Fatal("written Claude content did not round trip")
	}
	if loaded.Messages[0].Content != conv.Messages[0].Content || loaded.Messages[1].Content != conv.Messages[1].Content {
		t.Fatalf("written Claude whitespace changed: %#v", loaded.Messages)
	}
	if loaded.Migration == nil || loaded.Migration.OriginID != conv.ID || loaded.Migration.OriginSource != conv.Provider {
		t.Fatalf("loaded migration marker = %+v", loaded.Migration)
	}
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Migration == nil || summaries[0].Migration.OriginDigest != model.SnapshotDigest(conv) {
		t.Fatalf("migration marker not preserved for indexing: %+v", summaries)
	}
	if got := p.ResumeCommand(*write); !strings.Contains(got, "cd '") || !strings.Contains(got, "claude --resume '") {
		t.Fatalf("resume command is not shell quoted: %s", got)
	}
}

func TestDiscoverSubagentDoesNotOverwriteParentID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	path := filepath.Join(root, "projects", "-tmp-proj", "parent-session", "subagents", "agent-alpha.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"parent-session","cwd":"/tmp/proj","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"child request"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	p := claude.New()
	items, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "agent-alpha" || items[0].ParentID != "parent-session" || items[0].Kind != model.SessionKindSubagent {
		t.Fatalf("subagent summary = %+v", items)
	}
}
