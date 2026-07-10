package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
)

func TestRolloutNeedsV2Rewrite(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.jsonl")
	v2 := filepath.Join(dir, "v2.jsonl")
	_ = os.WriteFile(legacy, []byte(`{"type":"session_meta","session_id":"x","cwd":"/tmp"}`+"\n"), 0o644)
	_ = os.WriteFile(v2, []byte(`{"type":"session_meta","payload":{"id":"x"}}`+"\n"), 0o644)
	if !rolloutNeedsV2Rewrite(legacy) {
		t.Fatal("legacy should need rewrite")
	}
	if rolloutNeedsV2Rewrite(v2) {
		t.Fatal("v2 should not need rewrite")
	}
}

func TestRolloutIsAgenthopMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	body := strings.Repeat(`{"type":"response_item","payload":{}}`+"\n", 10) +
		`{"type":"agenthop_migration","data":{"source_provider":"hermes"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !rolloutIsAgenthopMigration(path) {
		t.Fatal("expected agenthop migration marker")
	}
	plain := filepath.Join(dir, "plain.jsonl")
	_ = os.WriteFile(plain, []byte(`{"type":"session_meta","payload":{"id":"x"}}`+"\n"), 0o644)
	if rolloutIsAgenthopMigration(plain) {
		t.Fatal("plain v2 should not be agenthop migration")
	}
}

func TestRewriteRolloutFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	_ = os.WriteFile(path, []byte(`{"type":"session_meta","session_id":"abc"}`+"\n"), 0o644)
	p := &Provider{sessionsRoot: filepath.Join(dir, "sessions")}
	conv := &model.Conversation{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "hi"},
			{Role: model.RoleAssistant, Content: "hello"},
		},
		ProjectPath: "/tmp/p",
	}
	if err := p.rewriteRolloutFile(path, "abc", "/tmp/p", conv); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"payload"`) {
		t.Fatalf("expected v2 payload wrapper: %s", string(b[:min(120, len(b))]))
	}
}

func TestEnsureResumableDoesNotRewriteV2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	body := `{"type":"session_meta","payload":{"id":"abc"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"appended by codex after resume"}]}}` + "\n" +
		`{"type":"agenthop_migration","data":{"originId":"src-1"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provider{sessionsRoot: filepath.Join(dir, "sessions")}
	conv := &model.Conversation{
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	}
	ref := provider.WriteResult{SessionID: "abc", StoragePath: path, ProjectPath: "/tmp/p"}
	if err := p.EnsureResumable(conv, ref); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != body {
		t.Fatalf("v2 rollout was rewritten:\n%s", string(after))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
