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
	defer func() { _ = db.Close() }()
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

// storeTitle stands in for the server applying a rename to its own database.
func storeTitle(t *testing.T, path, title string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE session_v2 SET title = ? WHERE id = 'ses_fixture'`, title); err != nil {
		t.Fatal(err)
	}
}

func TestRenameUsesOfficialAPI(t *testing.T) {
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
	// A directory that exists keeps the lifecycle call from restoring one:
	// that path has its own tests, and this one is about the API call.
	storeDirectory(t, path, "ses_fixture", t.TempDir())
	// The stub cannot write the database, so the applied rename is staged the
	// way a server would leave it: visible in the session's own row.
	storeTitle(t, path, "new title")
	if err := opencode2.New().RenameSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}, "new title"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(capture)
	text := strings.TrimSpace(string(got))
	if !strings.Contains(text, "api POST /api/session/ses_fixture/rename --data") || !strings.Contains(text, `new title`) {
		t.Fatalf("rename command = %q", got)
	}
}

// `opencode2 api` exits 0 on an HTTP 500, which is how a refused rename used
// to be reported to the person as "已重命名" while nothing changed. OpenCode 2
// refuses sessions whose directory has been deleted, so this is reachable
// through ordinary use: an old worktree, a temporary checkout.
func TestRenameReportsARefusalTheCLIHides(t *testing.T) {
	path := fixtureDB(t)
	script := filepath.Join(t.TempDir(), "opencode2")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	err := opencode2.New().RenameSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}, "new title")
	if err == nil {
		t.Fatal("a rename that never landed was reported as success")
	}
	// The message has to name what the title still is, or the person cannot
	// tell a refusal from a stale list.
	if !strings.Contains(err.Error(), "OpenCode 2 title") {
		t.Fatalf("error does not show the surviving title: %v", err)
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
	storeDirectory(t, path, "ses_fixture", t.TempDir())
	// The stub cannot write the database, so the applied deletion is staged
	// the way a server would leave it: the row is gone.
	dropFixtureSession(t, path)
	if err := opencode2.New().DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(capture)
	if strings.TrimSpace(string(got)) != "api DELETE /api/session/ses_fixture" {
		t.Fatalf("delete command = %q", got)
	}
}

func dropFixtureSession(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`DELETE FROM session_v2 WHERE id = 'ses_fixture'`); err != nil {
		t.Fatal(err)
	}
}

// A deletion the server refused must not be reported as a cleaned-up session,
// for the same reason as rename: the CLI exits 0 on an HTTP 500.
func TestDeleteReportsARefusalTheCLIHides(t *testing.T) {
	path := fixtureDB(t)
	script := filepath.Join(t.TempDir(), "opencode2")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	if err := opencode2.New().DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}); err == nil {
		t.Fatal("a deletion that never happened was reported as success")
	}
}

// OpenCode 2's server opens a session's directory before it will delete it, so
// a session left behind by a merged worktree could not be removed at all.
// another restores the directory for the call and withdraws it afterwards.
func TestDeleteRestoresAMissingDirectoryAndWithdrawsItAgain(t *testing.T) {
	path := fixtureDB(t)
	root := t.TempDir()
	parent := filepath.Join(root, "gone")
	dir := filepath.Join(parent, "worktree")
	storeDirectory(t, path, "ses_fixture", dir)
	capture := filepath.Join(t.TempDir(), "seen")
	script := filepath.Join(t.TempDir(), "opencode2")
	body := "#!/bin/sh\n[ -d \"$SESSION_DIR\" ] && printf present > \"$CAPTURE\" || printf missing > \"$CAPTURE\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	t.Setenv("CAPTURE", capture)
	t.Setenv("SESSION_DIR", dir)
	// The stub cannot write the database, so the server's own delete is
	// staged: the row has to still be there when another reads the session's
	// directory, and gone by the time it confirms the deletion.
	go func() {
		time.Sleep(150 * time.Millisecond)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return
		}
		defer func() { _ = db.Close() }()
		_, _ = db.Exec(`DELETE FROM session_v2 WHERE id = 'ses_fixture'`)
	}()

	if err := opencode2.New().DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}); err != nil {
		t.Fatal(err)
	}
	seen, _ := os.ReadFile(capture)
	if string(seen) != "present" {
		t.Fatalf("directory during the call = %q, want present", seen)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("restored directory survived the call: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("restored parent survived the call: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("a directory another did not create was removed: %v", err)
	}
}

// A directory that was already there is the person's, not another's. It must
// survive the call untouched, and so must anything the operation left in a
// directory another did create.
func TestDeleteLeavesDirectoriesItDidNotCreate(t *testing.T) {
	path := fixtureDB(t)
	dir := filepath.Join(t.TempDir(), "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "file")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	storeDirectory(t, path, "ses_fixture", dir)
	script := filepath.Join(t.TempDir(), "opencode2")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)
	go func() {
		time.Sleep(150 * time.Millisecond)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return
		}
		defer func() { _ = db.Close() }()
		_, _ = db.Exec(`DELETE FROM session_v2 WHERE id = 'ses_fixture'`)
	}()

	if err := opencode2.New().DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("an existing directory was removed: %v", err)
	}
}

// OpenCode 2's server owns the deletion, and replaying a conversation back
// through the API would create a new session with a new ID. That is a copy, not
// an undo, so this provider must never claim the reversible contract — the
// confirmation another shows for it promises the delete is final.
func TestDeleteIsNotAdvertisedAsReversible(t *testing.T) {
	if _, ok := any(opencode2.New()).(provider.ReversibleSessionDeleter); ok {
		t.Fatal("OpenCode 2 offers an undo it cannot honour: a restore there is a new session, not the original")
	}
}
