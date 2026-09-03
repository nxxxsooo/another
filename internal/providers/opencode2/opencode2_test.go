package opencode2_test

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
	"github.com/nxxxsooo/another/internal/providers/opencode2"
	_ "modernc.org/sqlite"
)

func fixtureDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE session_v2 (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL,
			directory TEXT NOT NULL, title TEXT, version TEXT NOT NULL, metadata TEXT,
			agent TEXT, model TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE session_message (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL, seq INTEGER NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	meta := `{"another_migration":{"type":"another_migration","originId":"origin","originSource":"pi","originMessageCount":2}}`
	modelRef := `{"id":"test-model","providerID":"test","variant":"default"}`
	if _, err := db.Exec(`INSERT INTO session_v2
		(id,project_id,parent_id,slug,directory,title,version,metadata,agent,model,time_created,time_updated)
		VALUES ('ses_fixture','project',NULL,'fixture','/tmp/project','OpenCode 2 title','2.0',?,'build',?,1000,2000)`, meta, modelRef); err != nil {
		t.Fatal(err)
	}
	user := `{"time":{"created":1100},"text":"question","files":[],"agents":[]}`
	assistant := `{"time":{"created":1200,"completed":1300},"content":[{"type":"reasoning","text":"private"},{"type":"text","text":"answer"}]}`
	if _, err := db.Exec(`INSERT INTO session_message VALUES
		('msg_user','ses_fixture','user',4,1100,1100,?),
		('msg_assistant','ses_fixture','assistant',5,1200,1300,?)`, user, assistant); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverAndLoadV2Schema(t *testing.T) {
	path := fixtureDB(t)
	t.Setenv("OPENCODE2_DB_PATH", path)
	p := opencode2.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Title != "OpenCode 2 title" || sums[0].Provider != "opencode2" || sums[0].MessageCount != 2 {
		t.Fatalf("summary = %+v", sums)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "ses_fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Messages[0].Content != "question" || conv.Messages[1].Content != "answer" {
		t.Fatalf("messages = %+v", conv.Messages)
	}
	if strings.Contains(conv.Messages[1].Content, "private") {
		t.Fatal("reasoning crossed the provider boundary")
	}
	if conv.Migration == nil || conv.Migration.OriginID != "origin" {
		t.Fatalf("migration marker = %+v", conv.Migration)
	}
}

func TestWriteUsesOfficialImportContract(t *testing.T) {
	path := fixtureDB(t)
	capture := filepath.Join(t.TempDir(), "payload.json")
	script := filepath.Join(t.TempDir(), "opencode2")
	body := "#!/bin/sh\ncp \"$4\" \"$CAPTURE\"\nprintf 'Imported session: test\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	t.Setenv("CAPTURE", capture)
	p := opencode2.New()
	start := time.UnixMilli(10000)
	res, err := p.Write(context.Background(), &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/tmp/target", Title: "carried title",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "question", Timestamp: start},
			{Role: model.RoleAssistant, Content: "answer", Timestamp: start.Add(time.Second)},
		},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProjectPath != "/tmp/target" || !strings.HasPrefix(res.SessionID, "ses_") {
		t.Fatalf("write result = %+v", res)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Info struct {
			ID       string         `json:"id"`
			Title    string         `json:"title"`
			Metadata map[string]any `json:"metadata"`
		} `json:"info"`
		Messages []map[string]any `json:"messages"`
	}
	if json.Unmarshal(data, &payload) != nil {
		t.Fatal("captured import is not JSON")
	}
	if payload.Info.ID != res.SessionID || payload.Info.Title != "carried title" || len(payload.Messages) != 2 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Info.Metadata["another_migration"] == nil {
		t.Fatal("official import payload lost migration marker")
	}
}

func TestDeleteUsesOfficialAPI(t *testing.T) {
	path := fixtureDB(t)
	capture := filepath.Join(t.TempDir(), "args")
	script := filepath.Join(t.TempDir(), "opencode2")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$CAPTURE\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	t.Setenv("CAPTURE", capture)
	if err := opencode2.New().DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(capture)
	if strings.TrimSpace(string(got)) != "api DELETE /api/session/ses_fixture" {
		t.Fatalf("delete command = %q", got)
	}
}
