package codex_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/codex"
	_ "modernc.org/sqlite"
)

func TestLoadFixture(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample.jsonl")
	conv, err := p.Load(context.Background(), provider.SessionRef{
		ID: "test-session-001", StoragePath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d", len(conv.Messages))
	}
	if conv.Messages[0].PlainText() != "Hello from codex fixture" {
		t.Fatalf("user msg = %q", conv.Messages[0].PlainText())
	}
}

func TestWriteCleansRolloutWhenThreadRegistrationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p := codex.New()
	conv := &model.Conversation{ID: "src", Provider: "claude-code", Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	if _, err := p.Write(context.Background(), conv, provider.WriteOpts{}); err == nil {
		t.Fatal("expected thread registration failure")
	}
	var rollouts []string
	_ = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			rollouts = append(rollouts, path)
		}
		return err
	})
	if len(rollouts) != 0 {
		t.Fatalf("orphan rollouts after registration failure: %v", rollouts)
	}
}

func TestFirstSessionMetaWinsAndRepeatedTurnsSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-01-01T00-00-00-019d4f95-9605-7851-89dc-bc55c6d8080b.jsonl")
	data := strings.Join([]string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"019d4f95-9605-7851-89dc-bc55c6d8080b","cwd":"/child","source":{"subagent":{"thread_spawn":{"parent_thread_id":"019d3c80-0000-7000-8000-000000000000"}}}}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"repeat me"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"repeat me"}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"repeat me"}]}}`,
		`{"timestamp":"2026-01-01T00:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"repeat me"}]}}`,
		`{"timestamp":"2026-01-01T00:00:05Z","type":"session_meta","payload":{"id":"019d3c80-0000-7000-8000-000000000000","cwd":"/parent"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	p := codex.New()
	sum, err := p.SummarizeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ID != "019d4f95-9605-7851-89dc-bc55c6d8080b" || sum.ProjectPath != "/child" || sum.Kind != model.SessionKindSubagent {
		t.Fatalf("wrong authoritative metadata: %+v", sum)
	}
	if sum.ParentID != "019d3c80-0000-7000-8000-000000000000" {
		t.Fatalf("parent id = %q", sum.ParentID)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: sum.ID, StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 4 || conv.ProjectPath != "/child" {
		t.Fatalf("wire duplicate/repeated turn handling: project=%q messages=%+v", conv.ProjectPath, conv.Messages)
	}
}

func TestLoadV2Fixture(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample-v2.jsonl")
	conv, err := p.Load(context.Background(), provider.SessionRef{
		StoragePath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID != "019d0304-afe0-7001-b42e-69d2028e34d1" {
		t.Fatalf("id = %q", conv.ID)
	}
	if conv.ProjectPath != "/home/cyrus/Documents/demo" {
		t.Fatalf("project = %q", conv.ProjectPath)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d, want mirrored user record removed", len(conv.Messages))
	}
	if conv.Title != "Fix the auth bug in login handler" {
		t.Fatalf("title = %q", conv.Title)
	}
}

func TestSummarizeV2Fixture(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample-v2.jsonl")
	sum, err := p.SummarizeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Title != "Fix the auth bug in login handler" {
		t.Fatalf("title = %q", sum.Title)
	}
	if sum.ProjectPath != "/home/cyrus/Documents/demo" {
		t.Fatalf("project = %q", sum.ProjectPath)
	}
}

// A Codex Desktop subagent or forked thread records assistant turns only as
// event_msg/agent_message: it has no response_item assistant record, and its
// only user-role record is injected transport. Reading just the response_item
// half left every one of those sessions unopenable.
func TestLoadSubagentForkFixture(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample-subagent-fork.jsonl")
	conv, err := p.Load(context.Background(), provider.SessionRef{
		ID: "019d0400-1111-7000-8000-000000000001", StoragePath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %+v, want both agent_message turns", conv.Messages)
	}
	for i, m := range conv.Messages {
		if m.Role != model.RoleAssistant {
			t.Fatalf("message %d role = %q", i, m.Role)
		}
	}
	if conv.Messages[0].PlainText() != "I inventoried every exported API definition in the backend crate." {
		t.Fatalf("first message = %q", conv.Messages[0].PlainText())
	}
	if conv.ProjectPath != "/home/cyrus/Documents/demo" {
		t.Fatalf("project = %q", conv.ProjectPath)
	}
	if conv.Title != "api_definitions · Wegener the 9th" {
		t.Fatalf("title = %q, want the subagent identity", conv.Title)
	}
}

func TestSummarizeSubagentForkFixture(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample-subagent-fork.jsonl")
	sum, err := p.SummarizeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sum.MessageCount != 2 {
		t.Fatalf("message count = %d, want the agent_message turns", sum.MessageCount)
	}
	if sum.Kind != model.SessionKindSubagent || sum.ParentID != "019d0400-0000-7000-8000-000000000000" {
		t.Fatalf("kind = %q parent = %q", sum.Kind, sum.ParentID)
	}
	if sum.Title != "api_definitions · Wegener the 9th" {
		t.Fatalf("title = %q, want the subagent identity", sum.Title)
	}
}

// An indexed message count is shown next to a session and decides whether the
// index considers a row current, so it has to mean what Load returns rather
// than counting Codex's mirrored event_msg/response_item pair twice.
func TestSummarizeMessageCountMatchesLoad(t *testing.T) {
	p := codex.New()
	for _, name := range []string{"sample.jsonl", "sample-v2.jsonl", "sample-subagent-fork.jsonl"} {
		path := filepath.Join("..", "..", "..", "testdata", "codex", name)
		sum, err := p.SummarizeFile(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		conv, err := p.Load(context.Background(), provider.SessionRef{ID: sum.ID, StoragePath: path})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if sum.MessageCount != len(conv.Messages) {
			t.Fatalf("%s: summarized %d messages, loaded %d", name, sum.MessageCount, len(conv.Messages))
		}
	}
}

func TestSummarizeEmptyUsesProjectPath(t *testing.T) {
	p := codex.New()
	path := filepath.Join("..", "..", "..", "testdata", "codex", "sample-empty.jsonl")
	sum, err := p.SummarizeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sum.Title, "demo") {
		t.Fatalf("title = %q, want project path fallback", sum.Title)
	}
}
