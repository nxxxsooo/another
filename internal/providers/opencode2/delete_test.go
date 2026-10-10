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
)

// deleteStub stands in for the agent CLI on a delete. mode "notfound" answers
// the server's no-such-session, "flaky" fails the first call and succeeds on
// the second, and the default succeeds.
func deleteStub(t *testing.T, dbPath, mode string) string {
	t.Helper()
	capture := filepath.Join(t.TempDir(), "calls")
	script := filepath.Join(t.TempDir(), "opencode2")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$CAPTURE\"\n" +
		"n=$(wc -l < \"$CAPTURE\")\n" +
		"if [ \"$MODE\" = notfound ]; then printf '{\"_tag\":\"SessionNotFoundError\",\"message\":\"Session not found\"}\\n'; exit 1; fi\n" +
		"if [ \"$MODE\" = flaky ] && [ \"$n\" -eq 1 ]; then printf '{\"_tag\":\"ServiceError\",\"message\":\"busy\"}\\n'; exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2_DB_PATH", dbPath)
	t.Setenv("OPENCODE2_COMMAND", script)
	t.Setenv("CAPTURE", capture)
	t.Setenv("MODE", mode)
	return capture
}

// dropSession removes the record, so the database agrees with a server that has
// let the session go.
func dropSession(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`DELETE FROM session_v2 WHERE id = 'ses_fixture'`); err != nil {
		t.Fatal(err)
	}
}

// A delete of a session the server no longer has is a delete with nothing left
// to do: a batch can hold a row the server already let go, and reporting that
// as a failure sends the reader to remove something that is not there.
func TestDeleteOfASessionTheServerHasLetGoSucceeds(t *testing.T) {
	requireShellStub(t)
	path := fixtureDB(t)
	dropSession(t, path)
	deleteStub(t, path, "notfound")

	p := opencode2.New()
	if err := p.DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture", Provider: "opencode2"}); err != nil {
		t.Fatalf("a session the server has let go reported a failure: %v", err)
	}
}

// A refusal another cannot explain is asked once more: a delete cannot do harm
// twice, and a server that was busy answers the second call.
func TestDeleteRetriesOnceAndThenReportsTheRefusal(t *testing.T) {
	requireShellStub(t)
	path := fixtureDB(t)
	dropSession(t, path)
	capture := deleteStub(t, path, "flaky")

	p := opencode2.New()
	if err := p.DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture", Provider: "opencode2"}); err != nil {
		t.Fatalf("the second call should have gone through: %v", err)
	}
	if got := len(calls(t, capture)); got != 2 {
		t.Fatalf("the CLI was called %d times, want a second attempt", got)
	}
}

// When the record is still there, the refusal is real and has to say so rather
// than claim a deletion that did not happen.
func TestDeleteStillReportsASessionTheDatabaseKeeps(t *testing.T) {
	requireShellStub(t)
	path := fixtureDB(t)
	deleteStub(t, path, "notfound")

	p := opencode2.New()
	err := p.DeleteSession(context.Background(), provider.SessionRef{ID: "ses_fixture", Provider: "opencode2"})
	if err == nil {
		t.Fatal("a session the agent kept was reported as deleted")
	}
	if !strings.Contains(err.Error(), "still in its database") {
		t.Fatalf("the refusal does not say what happened: %v", err)
	}
}
