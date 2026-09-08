package opencode2_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/opencode2"
	_ "modernc.org/sqlite"
)

// apiStub records every `opencode2 api` invocation and answers fork with a
// session id, the way the real CLI prints the server's JSON.
func apiStub(t *testing.T, dbPath, forkID string) string {
	t.Helper()
	capture := filepath.Join(t.TempDir(), "calls")
	script := filepath.Join(t.TempDir(), "opencode2")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$CAPTURE\"\n" +
		"case \"$*\" in\n" +
		"  *fork*) printf '{\"data\":{\"id\":\"%s\"}}\\n' \"$FORK_ID\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", dbPath)
	t.Setenv("OPENCODE2_COMMAND", script)
	t.Setenv("CAPTURE", capture)
	t.Setenv("FORK_ID", forkID)
	return capture
}

func calls(t *testing.T, capture string) []string {
	t.Helper()
	data, err := os.ReadFile(capture)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// stageFork stands in for the server persisting a fork, including the copied
// messages and the directory a following move would land it in.
func stageFork(t *testing.T, path, forkID, directory string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO session_v2
		(id,project_id,parent_id,slug,directory,title,version,metadata,agent,model,time_created,time_updated)
		VALUES (?,'project','ses_fixture','fork',?,'OpenCode 2 title','2.0',NULL,'build','{}',1000,2000)`,
		forkID, directory); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES
		('msg_fork_user',?,'user',4,1100,1100,'{"time":{"created":1100},"text":"question"}')`, forkID); err != nil {
		t.Fatal(err)
	}
}

func storeDirectory(t *testing.T, path, sessionID, directory string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE session_v2 SET directory = ? WHERE id = ?`, directory, sessionID); err != nil {
		t.Fatal(err)
	}
}

func readDirectory(t *testing.T, path, sessionID string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var directory string
	if err := db.QueryRow(`SELECT directory FROM session_v2 WHERE id = ?`, sessionID).Scan(&directory); err != nil {
		t.Fatal(err)
	}
	return directory
}

// Fork is the server's own copy followed by the server's own move. another
// never re-renders the conversation, which is what keeps tool calls alive.
func TestRelocateForkUsesOfficialAPIs(t *testing.T) {
	path := fixtureDB(t)
	capture := apiStub(t, path, "ses_fork")
	// The stub cannot write the database, so the applied fork and move are
	// staged the way a server would leave them.
	stageFork(t, path, "ses_fork", "/tmp/worktree")

	res, err := opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateFork})
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "ses_fork" || res.Moved || res.ProjectPath != "/tmp/worktree" {
		t.Fatalf("relocate result = %+v", res)
	}
	got := calls(t, capture)
	if len(got) != 2 {
		t.Fatalf("expected a fork and a move, got %v", got)
	}
	if !strings.Contains(got[0], "api POST /api/session/ses_fixture/fork --data") ||
		!strings.Contains(got[0], `"type":"through"`) {
		t.Fatalf("fork call = %q", got[0])
	}
	if !strings.Contains(got[1], "api POST /api/session/ses_fork/move --data") ||
		!strings.Contains(got[1], `"directory":"/tmp/worktree"`) {
		t.Fatalf("move call = %q", got[1])
	}
	// The source is sacred.
	if dir := readDirectory(t, path, "ses_fixture"); dir != "/tmp/project" {
		t.Fatalf("fork moved the source session to %s", dir)
	}
}

func TestRelocateMoveUsesOfficialAPI(t *testing.T) {
	path := fixtureDB(t)
	capture := apiStub(t, path, "")
	storeDirectory(t, path, "ses_fixture", "/tmp/worktree")

	res, err := opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateMove})
	if err != nil {
		t.Fatal(err)
	}
	// A move is the same session in a new place, so it keeps its id.
	if res.SessionID != "ses_fixture" || !res.Moved {
		t.Fatalf("relocate result = %+v", res)
	}
	got := calls(t, capture)
	if len(got) != 1 || !strings.Contains(got[0], "api POST /api/session/ses_fixture/move --data") {
		t.Fatalf("move calls = %v", got)
	}
}

// `opencode2 api` exits 0 on an HTTP 500. A move the server refused must not be
// reported as a relocated session, or the person is told their session is in a
// directory it never reached.
func TestRelocateReportsARefusalTheCLIHides(t *testing.T) {
	path := fixtureDB(t)
	script := filepath.Join(t.TempDir(), "opencode2")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", path)
	t.Setenv("OPENCODE2_COMMAND", script)

	_, err := opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateMove})
	if err == nil {
		t.Fatal("a move that never landed was reported as success")
	}
	// The message has to name the directory the session is still in.
	if !strings.Contains(err.Error(), "/tmp/project") {
		t.Fatalf("error does not show the surviving directory: %v", err)
	}
}

// A fork that cannot be moved is not what was asked for, and left alone it
// looks like an accidental duplicate in the source directory.
func TestRelocateRollsBackAForkItCannotMove(t *testing.T) {
	path := fixtureDB(t)
	// The fork id the stub returns is never staged, so the move cannot land.
	capture := apiStub(t, path, "ses_ghost")

	_, err := opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateFork})
	if err == nil {
		t.Fatal("a fork that was never moved was reported as success")
	}
	got := calls(t, capture)
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "api DELETE /api/session/ses_ghost") {
		t.Fatalf("the orphaned fork was not cleaned up: %v", got)
	}
}

// A fork the server reported but did not fill is an empty session. Reporting it
// as relocated would hand the person a directory with nothing in it.
func TestRelocateRefusesAForkThatCarriedNothing(t *testing.T) {
	path := fixtureDB(t)
	apiStub(t, path, "ses_empty")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_v2
		(id,project_id,parent_id,slug,directory,title,version,metadata,agent,model,time_created,time_updated)
		VALUES ('ses_empty','project','ses_fixture','fork','/tmp/worktree','t','2.0',NULL,'build','{}',1,2)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateFork})
	if err == nil || !strings.Contains(err.Error(), "carried no messages") {
		t.Fatalf("an empty fork was reported as %v", err)
	}
}

func TestRelocateDryRunCallsNothing(t *testing.T) {
	path := fixtureDB(t)
	capture := apiStub(t, path, "ses_fork")
	res, err := opencode2.New().RelocateSession(context.Background(),
		provider.SessionRef{ID: "ses_fixture", ProjectPath: "/tmp/project"},
		provider.RelocateOpts{Directory: "/tmp/worktree", Mode: provider.RelocateMove, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProjectPath != "/tmp/worktree" {
		t.Fatalf("dry run result = %+v", res)
	}
	if got := calls(t, capture); len(got) != 0 {
		t.Fatalf("a dry run called the API: %v", got)
	}
	if dir := readDirectory(t, path, "ses_fixture"); dir != "/tmp/project" {
		t.Fatalf("a dry run moved the session to %s", dir)
	}
}
