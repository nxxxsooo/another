package hermes

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	_ "modernc.org/sqlite"
)

func TestArchiveTogglesNativeFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, archived INTEGER NOT NULL DEFAULT 0); INSERT INTO sessions VALUES ('s1',0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	p := &Provider{dbPath: path}
	ref := provider.SessionRef{ID: "s1"}
	if err := p.ArchiveSession(context.Background(), ref, true); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", path)
	var archived int
	_ = db.QueryRow(`SELECT archived FROM sessions WHERE id='s1'`).Scan(&archived)
	_ = db.Close()
	if archived != 1 {
		t.Fatalf("archived=%d", archived)
	}
	if err := p.ArchiveSession(context.Background(), ref, false); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", path)
	defer db.Close()
	_ = db.QueryRow(`SELECT archived FROM sessions WHERE id='s1'`).Scan(&archived)
	if archived != 0 {
		t.Fatalf("unarchived=%d", archived)
	}
}

func TestDiscoverNullTitleSessions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    started_at REAL NOT NULL,
    message_count INTEGER DEFAULT 0,
    title TEXT,
    cwd TEXT,
	parent_session_id TEXT,
    archived INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT,
	timestamp REAL,
    active INTEGER NOT NULL DEFAULT 1
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at, message_count, title) VALUES
		('with-title', 'cli', 1000, 1, 'Has title'),
		('null-title', 'cli', 2000, 2, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET parent_session_id='with-title' WHERE id='null-title'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages (session_id, role, content, active, timestamp) VALUES
		('null-title', 'user', 'First user prompt', 1, 2050.25),
		('null-title', 'assistant', 'Reply', 1, 2100.5)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	p := &Provider{dbPath: dbPath}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sums))
	}
	byID := map[string]model.Summary{}
	for _, s := range sums {
		byID[s.ID] = s
	}
	if byID["with-title"].Title != "Has title" {
		t.Fatalf("with-title: %q", byID["with-title"].Title)
	}
	if byID["null-title"].Title != "First user prompt" {
		t.Fatalf("null-title: %q", byID["null-title"].Title)
	}
	if byID["null-title"].Kind != model.SessionKindSubagent || byID["null-title"].ParentID != "with-title" {
		t.Fatalf("child identity: %+v", byID["null-title"])
	}
	if got := byID["null-title"].UpdatedAt.UnixMilli(); got != 2100500 {
		t.Fatalf("updated time = %d, want 2100500", got)
	}

	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "null-title", StoragePath: dbPath + "#null-title"})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "First user prompt" {
		t.Fatalf("load title: %q", conv.Title)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages: %d", len(conv.Messages))
	}
}

func TestWriteRoundTripProjectTimestampsAndCleanup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE sessions (
 id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL, message_count INTEGER DEFAULT 0,
 title TEXT, cwd TEXT, parent_session_id TEXT, archived INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_sessions_title_unique ON sessions(title) WHERE title IS NOT NULL;
CREATE TABLE messages (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
 content TEXT, timestamp REAL NOT NULL, active INTEGER NOT NULL DEFAULT 1
);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: dbPath}
	start := time.Date(2026, 8, 17, 3, 0, 0, 125000000, time.UTC)
	conv := &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: "/old", Title: "Hermes migration", CreatedAt: start,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "first", Timestamp: start.Add(2 * time.Second)},
			{Role: model.RoleAssistant, Content: "equal", Timestamp: start.Add(2 * time.Second)},
			{Role: model.RoleUser, Content: "decreasing", Timestamp: start},
		},
	}
	before := *conv
	before.Messages = append([]model.Message(nil), conv.Messages...)
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: "/new path"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*conv, before) {
		t.Fatalf("write mutated input: before=%+v after=%+v", before, *conv)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: write.SessionID, StoragePath: write.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(loaded) != model.ContentDigest(conv) || loaded.ProjectPath != "/new path" {
		t.Fatalf("round trip: %+v", loaded)
	}
	for i := range conv.Messages {
		if !loaded.Messages[i].Timestamp.Equal(conv.Messages[i].Timestamp) {
			t.Fatalf("timestamp %d = %v, want %v", i, loaded.Messages[i].Timestamp, conv.Messages[i].Timestamp)
		}
	}
	if loaded.Migration == nil || loaded.Migration.OriginID != conv.ID {
		t.Fatalf("migration: %+v", loaded.Migration)
	}
	if err := p.CleanupWrite(context.Background(), *write); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", dbPath+"?mode=ro")
	defer db.Close()
	var sessions, messages int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, write.SessionID).Scan(&sessions)
	_ = db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id=?`, write.SessionID).Scan(&messages)
	if sessions != 0 || messages != 0 {
		t.Fatalf("cleanup sessions=%d messages=%d", sessions, messages)
	}
}

func TestResumeCommandUsesResumeAndQuotes(t *testing.T) {
	p := &Provider{}
	if got := p.ResumeCommand(provider.WriteResult{SessionID: "id; echo bad"}); !strings.HasSuffix(got, "--resume 'id; echo bad'") {
		t.Fatalf("resume = %q", got)
	}
}

func TestDiscoverSkipsArchived(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO sessions (id, source, started_at, message_count, title, archived) VALUES
		('live', 'cli', 1, 0, 'live', 0),
		('gone', 'cli', 2, 0, 'gone', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	p := &Provider{dbPath: dbPath}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].ID != "live" {
		t.Fatalf("got %+v", sums)
	}
}

func TestInstalled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	if err := os.WriteFile(dbPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: dbPath}
	if !p.Installed() {
		t.Fatal("expected installed")
	}
}

func TestDiscoverSeesWALOnlyDatabaseChange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA wal_autocheckpoint=0;
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, source TEXT NOT NULL, started_at REAL NOT NULL,
  message_count INTEGER DEFAULT 0, title TEXT, cwd TEXT,
  parent_session_id TEXT, archived INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE messages (
  id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT,
  timestamp REAL, active INTEGER NOT NULL DEFAULT 1
);
INSERT INTO sessions (id, source, started_at, message_count, title) VALUES ('s1', 'cli', 1000, 1, 'Stable title');
INSERT INTO messages VALUES (1, 's1', 'user', 'first', 2000, 1);
PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: dbPath}
	before, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(before) != 1 {
		t.Fatalf("initial discover: summaries=%d err=%v", len(before), err)
	}
	mainBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.Stat(dbPath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE messages SET content='second' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	mainAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if mainAfter.Size() != mainBefore.Size() || !mainAfter.ModTime().Equal(mainBefore.ModTime()) {
		t.Fatal("test setup checkpointed the WAL into state.db")
	}
	walAfter, err := os.Stat(dbPath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if walAfter.Size() == walBefore.Size() && walAfter.ModTime().Equal(walBefore.ModTime()) {
		t.Fatal("test setup did not change state.db-wal")
	}
	after, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(after) != 1 {
		t.Fatalf("updated discover: summaries=%d err=%v", len(after), err)
	}
	if after[0].SourceMtime == before[0].SourceMtime && after[0].SourceSize == before[0].SourceSize {
		t.Fatal("WAL-only edit did not alter source stamp")
	}
	if after[0].UpdatedAt != before[0].UpdatedAt || after[0].MessageCount != before[0].MessageCount {
		t.Fatal("test edit unexpectedly changed ordinary freshness fields")
	}
}
