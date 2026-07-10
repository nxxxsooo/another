package hermes

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	_ "modernc.org/sqlite"
)

func TestLoadSkipsToolAndInjectedUserMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL,
		message_count INTEGER DEFAULT 0, title TEXT, cwd TEXT, archived INTEGER NOT NULL DEFAULT 0
	); CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
		content TEXT, active INTEGER NOT NULL DEFAULT 1
	);`); err != nil {
		t.Fatal(err)
	}
	sid := "sess-filter"
	if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at, message_count, title) VALUES (?, 'cli', 1, 0, '')`, sid); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ role, content string }{
		{"user", "[IMPORTANT: Background process completed"},
		{"tool", `{"output": "huge json dump", "exit_code": 0}`},
		{"tool", "[terminal] ran `curl` -> exit 0"},
		{"user", "real user prompt"},
		{"user", "Continue from the current coverage state and push deeper into branches"},
		{"assistant", "[CONTEXT COMPACTION — REFERENCE ONLY] summary"},
		{"assistant", "Here is the real answer"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO messages (session_id, role, content, active) VALUES (?, ?, ?, 1)`, sid, r.role, r.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	p := &Provider{dbPath: dbPath}
	conv, err := p.Load(context.Background(), providerSessionRef(sid, dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[0].Role != model.RoleUser || conv.Messages[0].PlainText() != "real user prompt" {
		t.Fatalf("user: %+v", conv.Messages[0])
	}
	if conv.Messages[1].Role != model.RoleAssistant || conv.Messages[1].PlainText() != "Here is the real answer" {
		t.Fatalf("assistant: %+v", conv.Messages[1])
	}
}

func TestHermesSkipStandingGoal(t *testing.T) {
	if !hermesSkipMessage(model.RoleUser, "Continue from the current coverage state and push deeper") {
		t.Fatal("expected standing goal user line to be skipped")
	}
}

func providerSessionRef(id, dbPath string) provider.SessionRef {
	return provider.SessionRef{ID: id, StoragePath: dbPath + "#" + id}
}
