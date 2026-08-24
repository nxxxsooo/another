package opencode

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	_ "modernc.org/sqlite"
)

func TestDiscoverSeesWALOnlyDatabaseChange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA wal_autocheckpoint=0;
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER, parent_id TEXT);
CREATE TABLE message (id TEXT, session_id TEXT, data TEXT, time_created INTEGER);
CREATE TABLE part (message_id TEXT, data TEXT, time_created INTEGER);
INSERT INTO session VALUES ('s1', '/project', 'Stable title', 1000, 2000, NULL);
INSERT INTO message VALUES ('m1', 's1', '{"role":"user","time":{"created":1000}}', 1000);
INSERT INTO part VALUES ('m1', '{"type":"text","text":"first"}', 1000);
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
	if _, err := db.Exec(`UPDATE part SET data='{"type":"text","text":"second"}' WHERE message_id='m1'`); err != nil {
		t.Fatal(err)
	}
	mainAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if mainAfter.Size() != mainBefore.Size() || !mainAfter.ModTime().Equal(mainBefore.ModTime()) {
		t.Fatal("test setup checkpointed the WAL into opencode.db")
	}
	walAfter, err := os.Stat(dbPath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if walAfter.Size() == walBefore.Size() && walAfter.ModTime().Equal(walBefore.ModTime()) {
		t.Fatal("test setup did not change opencode.db-wal")
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

func TestWriteRoundTripUsesNativeVersionAndCleanup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, sandboxes TEXT NOT NULL);
CREATE TABLE session (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL, directory TEXT NOT NULL,
 title TEXT NOT NULL, version TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
 metadata TEXT, agent TEXT, model TEXT, cost REAL NOT NULL DEFAULT 0, tokens_input INTEGER NOT NULL DEFAULT 0,
 tokens_output INTEGER NOT NULL DEFAULT 0, tokens_reasoning INTEGER NOT NULL DEFAULT 0,
 tokens_cache_read INTEGER NOT NULL DEFAULT 0, tokens_cache_write INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
INSERT INTO project VALUES ('global', '/', 1, 1, '[]');
INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES ('native', 'global', 'native', '/', 'native', '9.8.7', 1, 1);
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: dbPath}
	start := time.Date(2026, 8, 17, 2, 0, 0, 0, time.UTC)
	conv := &model.Conversation{
		ID: "origin", Provider: "claude-code", ProjectPath: "/old", Title: "Portable",
		CreatedAt: start, UpdatedAt: start.Add(time.Minute),
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
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	for i := range conv.Messages {
		if !loaded.Messages[i].Timestamp.Equal(conv.Messages[i].Timestamp) {
			t.Fatalf("timestamp %d = %v, want %v", i, loaded.Messages[i].Timestamp, conv.Messages[i].Timestamp)
		}
	}
	db, err = sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var version string
	var agent, usedModel sql.NullString
	if err := db.QueryRow(`SELECT version, agent, model FROM session WHERE id=?`, write.SessionID).Scan(&version, &agent, &usedModel); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if version != "9.8.7" || agent.Valid || usedModel.Valid {
		t.Fatalf("defaults: version=%q agent=%v model=%v", version, agent, usedModel)
	}
	if err := p.CleanupWrite(context.Background(), *write); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", dbPath+"?mode=ro")
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session WHERE id=?`, write.SessionID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleanup count=%d err=%v", count, err)
	}
}

func TestResumeCommandQuotesSession(t *testing.T) {
	p := &Provider{}
	if got := p.ResumeCommand(provider.WriteResult{SessionID: "id; echo bad"}); !strings.HasSuffix(got, "'id; echo bad'") {
		t.Fatalf("resume = %q", got)
	}
}
