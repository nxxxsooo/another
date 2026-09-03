package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
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
	mainBefore, _ := os.Stat(dbPath)
	walBefore, _ := os.Stat(dbPath + "-wal")
	if _, err := db.Exec(`UPDATE part SET data='{"type":"text","text":"second"}' WHERE message_id='m1'`); err != nil {
		t.Fatal(err)
	}
	mainAfter, _ := os.Stat(dbPath)
	walAfter, _ := os.Stat(dbPath + "-wal")
	if mainAfter.Size() != mainBefore.Size() || !mainAfter.ModTime().Equal(mainBefore.ModTime()) {
		t.Fatal("test setup checkpointed the WAL into opencode.db")
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
}

func TestWriteUsesOfficialImportWithNativeFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (
 id TEXT PRIMARY KEY, directory TEXT, title TEXT, version TEXT, agent TEXT, model TEXT, time_updated INTEGER);
INSERT INTO session VALUES ('native','/','native','9.8.7','build','{"id":"model-1","providerID":"provider-1","variant":"default"}',1);`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	captureDir := t.TempDir()
	capture := filepath.Join(captureDir, "payload.json")
	calls := filepath.Join(captureDir, "calls")
	script := filepath.Join(captureDir, "opencode")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$CALLS\"\n" +
		"if [ \"$1\" = import ]; then cp \"$2\" \"$CAPTURE\"; echo 'Imported session'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE", capture)
	t.Setenv("CALLS", calls)
	p := &Provider{dbPath: dbPath, command: script}
	start := time.UnixMilli(10000)
	conv := &model.Conversation{
		ID: "origin", Provider: "pi", ProjectPath: "/old", Title: "Portable",
		CreatedAt: start, UpdatedAt: start.Add(time.Second),
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "first", Timestamp: start},
			{Role: model.RoleAssistant, Content: "answer", Timestamp: start.Add(time.Second)},
		},
	}
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: "/new path"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Info struct {
			ID       string         `json:"id"`
			Title    string         `json:"title"`
			Agent    string         `json:"agent"`
			Model    map[string]any `json:"model"`
			Metadata map[string]any `json:"metadata"`
		} `json:"info"`
		Messages []struct {
			Info  map[string]any   `json:"info"`
			Parts []map[string]any `json:"parts"`
		} `json:"messages"`
	}
	if json.Unmarshal(data, &payload) != nil {
		t.Fatal("captured import is not JSON")
	}
	if payload.Info.ID != write.SessionID || payload.Info.Title != "Portable" || payload.Info.Agent != "build" || payload.Info.Model["id"] != "model-1" {
		t.Fatalf("native session fields missing: %+v", payload.Info)
	}
	if payload.Info.Metadata["another_migration"] == nil || len(payload.Messages) != 2 {
		t.Fatalf("migration payload incomplete: %+v", payload)
	}
	assistant := payload.Messages[1].Info
	if assistant["role"] != "assistant" || assistant["agent"] != "build" || assistant["modelID"] != "model-1" || assistant["providerID"] != "provider-1" || assistant["parentID"] == nil {
		t.Fatalf("assistant lacks native fields required by the TUI: %+v", assistant)
	}
	if err := p.CleanupWrite(context.Background(), *write); err != nil {
		t.Fatal(err)
	}
	callData, _ := os.ReadFile(calls)
	callText := string(callData)
	if !strings.Contains(callText, "import ") || !strings.Contains(callText, "session delete "+write.SessionID) {
		t.Fatalf("official CLI calls = %q", callText)
	}
}

func TestRenameUpdatesNativeSessionTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, time_updated INTEGER); INSERT INTO session VALUES ('s1','old',1)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	p := &Provider{dbPath: path}
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: "s1"}, "new title"); err != nil {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", path)
	defer db.Close()
	var title string
	if err := db.QueryRow(`SELECT title FROM session WHERE id='s1'`).Scan(&title); err != nil || title != "new title" {
		t.Fatalf("title=%q err=%v", title, err)
	}
}

func TestResumeCommandQuotesSession(t *testing.T) {
	p := &Provider{}
	if got := p.ResumeCommand(provider.WriteResult{SessionID: "id; echo bad"}); !strings.HasSuffix(got, "'id; echo bad'") {
		t.Fatalf("resume = %q", got)
	}
}
