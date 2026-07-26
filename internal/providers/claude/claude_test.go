package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/providers/claude"
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
