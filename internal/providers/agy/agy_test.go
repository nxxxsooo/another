package agy_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/agy"
	_ "modernc.org/sqlite"
)

func TestProviderBasics(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	p := agy.New()
	if p.ID() != "agy" {
		t.Fatalf("ID = %q, want 'agy'", p.ID())
	}
	if p.DisplayName() != "Antigravity" {
		t.Fatalf("DisplayName = %q, want 'Antigravity'", p.DisplayName())
	}
	if !p.SupportsResume() {
		t.Fatal("SupportsResume must be true")
	}

	paths := p.DefaultPaths()
	if len(paths) < 3 {
		t.Fatalf("expected at least 3 DefaultPaths, got %d", len(paths))
	}

	res := provider.WriteResult{
		SessionID:   "session-123",
		ProjectPath: "/some/project",
	}
	cmd := p.ResumeCommand(res)
	if cmd != "cd '/some/project' && agy --conversation 'session-123'" {
		t.Fatalf("unexpected ResumeCommand: %q", cmd)
	}

	resNoProj := provider.WriteResult{
		SessionID: "session-456",
	}
	cmdNoProj := p.ResumeCommand(resNoProj)
	if cmdNoProj != "agy --conversation 'session-456'" {
		t.Fatalf("unexpected ResumeCommand without project: %q", cmdNoProj)
	}
}

func TestDiscoverFromSummariesDB(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	summariesDB := filepath.Join(root, "conversation_summaries.db")
	db, err := sql.Open("sqlite", summariesDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	schema := `
	CREATE TABLE conversation_summaries (
		conversation_id text PRIMARY KEY,
		title text NOT NULL DEFAULT "",
		preview text NOT NULL DEFAULT "",
		step_count integer NOT NULL DEFAULT 0,
		last_modified_time datetime NOT NULL,
		workspace_uris text NOT NULL,
		status text NOT NULL DEFAULT "",
		source text NOT NULL DEFAULT "",
		project_id text NOT NULL DEFAULT "",
		agent_name text NOT NULL DEFAULT "",
		parent_conversation_id text NOT NULL DEFAULT "",
		nesting_depth integer NOT NULL DEFAULT 0,
		battle_id text NOT NULL DEFAULT "",
		winning_conversation_id text NOT NULL DEFAULT "",
		not_fully_idle numeric NOT NULL DEFAULT false,
		killed numeric NOT NULL DEFAULT false,
		last_user_input_time datetime NOT NULL,
		last_user_input_step_index integer NOT NULL DEFAULT -1,
		app_data_dir text NOT NULL DEFAULT "",
		raw_summary BLOB
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	uris, _ := json.Marshal([]string{"file:///workspace/demo"})
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000+00:00")

	// Insert active session, empty session (0 steps), and killed session.
	_, err = db.Exec(`
		INSERT INTO conversation_summaries (conversation_id, title, preview, step_count, last_modified_time, workspace_uris, killed, last_user_input_time)
		VALUES
			('s1', 'My Session', 'preview 1', 5, ?, ?, 0, ?),
			('s2_empty', 'Empty Session', '', 0, ?, ?, 0, ?),
			('s3_killed', 'Killed Session', '', 10, ?, ?, 1, ?)
	`, now, string(uris), now, now, string(uris), now, now, string(uris), now)
	if err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(root, "brain", "s1", ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T09:00:00Z","content":"<USER_REQUEST>preview 1</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agy.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("expected 1 session discovered, got %d", len(sums))
	}
	if sums[0].ID != "s1" || sums[0].Title != "My Session" || sums[0].ProjectPath != "/workspace/demo" {
		t.Fatalf("unexpected summary: %+v", sums[0])
	}
}

func createSummariesDB(t *testing.T, root, id, title string, steps int, modified string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`
	CREATE TABLE conversation_summaries (
		conversation_id text PRIMARY KEY, title text NOT NULL DEFAULT '', preview text NOT NULL DEFAULT '',
		step_count integer NOT NULL DEFAULT 0, last_modified_time datetime NOT NULL, workspace_uris text NOT NULL,
		status text NOT NULL DEFAULT '', source text NOT NULL DEFAULT '', project_id text NOT NULL DEFAULT '',
		agent_name text NOT NULL DEFAULT '', parent_conversation_id text NOT NULL DEFAULT '', nesting_depth integer NOT NULL DEFAULT 0,
		battle_id text NOT NULL DEFAULT '', winning_conversation_id text NOT NULL DEFAULT '', not_fully_idle numeric NOT NULL DEFAULT false,
		killed numeric NOT NULL DEFAULT false, last_user_input_time datetime NOT NULL, last_user_input_step_index integer NOT NULL DEFAULT -1,
		app_data_dir text NOT NULL DEFAULT '', raw_summary BLOB
	);`); err != nil {
		t.Fatal(err)
	}
	uris, _ := json.Marshal([]string{"file://" + root})
	if _, err := db.Exec(`INSERT INTO conversation_summaries
		(conversation_id,title,preview,step_count,last_modified_time,workspace_uris,last_user_input_time,app_data_dir)
		VALUES(?,?,?,?,?,?,?,?)`, id, title, "preview", steps, modified, string(uris), modified, root); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverFallsBackWhenSummaryHasNotCountedActiveTranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	id := "active-summary-lag"
	createSummariesDB(t, root, id, "", 0, "2026-09-04 10:00:00.000000+00:00")
	logDir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:01Z","content":"<USER_REQUEST>active prompt</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	summaries, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != id || summaries[0].Title != "active prompt" {
		t.Fatalf("lagging native summary hid active transcript: %+v", summaries)
	}
}

func TestDiscoverFromBrainDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	convID := "brain-conv-1"
	logDir := filepath.Join(root, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:00Z","content":"<USER_REQUEST>\nFix database issue\n</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-09-04T10:00:05Z","content":"I fixed the database issue"}`,
	}
	logFile := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logFile, []byte(lines[0]+"\n"+lines[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agy.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("expected 1 session from brain, got %d", len(sums))
	}
	if sums[0].ID != convID || sums[0].Title != "Fix database issue" || sums[0].MessageCount != 2 {
		t.Fatalf("unexpected summary: %+v", sums[0])
	}
}

func TestLoadTranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	convID := "test-load-1"
	logDir := filepath.Join(root, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		`{"type":"another_migration","data":{"type":"another_migration","originId":"orig-1","originSource":"claude-code","originMessageCount":2}}`,
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:00Z","content":"<USER_REQUEST>\nHow to parse JSON in Go\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nLocal time: 10:00\n</ADDITIONAL_METADATA>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-09-04T10:00:05Z","content":"Use encoding/json package"}`,
	}
	logFile := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logFile, []byte(lines[0]+"\n"+lines[1]+"\n"+lines[2]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agy.New()
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: convID, StoragePath: logFile})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Migration == nil || conv.Migration.OriginID != "orig-1" {
		t.Fatalf("expected migration metadata, got %+v", conv.Migration)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "How to parse JSON in Go" {
		t.Fatalf("expected clean user prompt, got %q", conv.Messages[0].Content)
	}
	if conv.Messages[1].Content != "Use encoding/json package" {
		t.Fatalf("expected assistant response, got %q", conv.Messages[1].Content)
	}
}

func TestWriteAndVerifyRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	p := agy.New()
	source := &model.Conversation{
		ID:          "source-conv",
		Provider:    "codex",
		ProjectPath: "/home/user/code",
		Title:       "Sample Conversation",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "First prompt", Timestamp: time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)},
			{Role: model.RoleAssistant, Content: "First response", Timestamp: time.Date(2026, 9, 4, 10, 0, 5, 0, time.UTC)},
			{Role: model.RoleUser, Content: "Second prompt", Timestamp: time.Date(2026, 9, 4, 10, 1, 0, 0, time.UTC)},
			{Role: model.RoleAssistant, Content: "Second response", Timestamp: time.Date(2026, 9, 4, 10, 1, 10, 0, time.UTC)},
		},
	}

	writeRes, err := p.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if writeRes.SessionID == "" || writeRes.StoragePath == "" {
		t.Fatalf("invalid WriteResult: %+v", writeRes)
	}

	// Verify transcript was written
	if _, err := os.Stat(writeRes.StoragePath); err != nil {
		t.Fatalf("transcript not found at %s", writeRes.StoragePath)
	}

	// Load back
	loaded, err := p.Load(context.Background(), provider.SessionRef{
		ID:          writeRes.SessionID,
		StoragePath: writeRes.StoragePath,
		ProjectPath: writeRes.ProjectPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Messages) != len(source.Messages) {
		t.Fatalf("loaded message count = %d, want %d", len(loaded.Messages), len(source.Messages))
	}

	// Compare ContentDigest
	wantDigest := model.ContentDigest(source)
	gotDigest := model.ContentDigest(loaded)
	if wantDigest != gotDigest {
		t.Fatalf("content digest mismatch:\nwant %s\ngot  %s", wantDigest, gotDigest)
	}

	// Rename uses AGY's summary source of truth and reads it back.
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: writeRes.SessionID}, "Renamed Title"); err != nil {
		t.Fatal(err)
	}
	if got := pTitle(t, root, writeRes.SessionID); got != "Renamed Title" {
		t.Fatalf("renamed title = %q", got)
	}

	// CleanupWrite is the migration engine's exact rollback contract. It is
	// intentionally separate from user-facing deletion.
	if err := p.CleanupWrite(context.Background(), *writeRes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(writeRes.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("transcript still exists after rollback cleanup: %v", err)
	}
}

func TestLoadPrefersFullTranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	convID := "full-transcript"
	logDir := filepath.Join(root, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	compact := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:00Z","content":"<USER_REQUEST>truncated</USER_REQUEST>","truncated_fields":["content"]}` + "\n"
	full := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:00Z","content":"<USER_REQUEST>complete original prompt</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(compact), 0o644); err != nil {
		t.Fatal(err)
	}
	fullPath := filepath.Join(logDir, "transcript_full.jsonl")
	if err := os.WriteFile(fullPath, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}

	conv, err := agy.New().Load(context.Background(), provider.SessionRef{ID: convID})
	if err != nil {
		t.Fatal(err)
	}
	if conv.StoragePath != fullPath || len(conv.Messages) != 1 || conv.Messages[0].Content != "complete original prompt" {
		t.Fatalf("Load did not use the complete transcript: %+v", conv)
	}
}

func TestDiscoverRefreshesWhenNativeSummaryChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	convID := "summary-refresh"
	logDir := filepath.Join(root, "brain", convID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fullPath := filepath.Join(logDir, "transcript_full.jsonl")
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-01T10:00:00Z","content":"<USER_REQUEST>original prompt</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(fullPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	createSummariesDB(t, root, convID, "old title", 1, "2026-09-04 10:00:00.000000+00:00")

	first, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(first) != 1 {
		t.Fatalf("initial Discover = %+v, %v", first, err)
	}
	if got := first[0].CreatedAt; !got.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreatedAt = %s, want first portable message timestamp", got)
	}
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE conversation_summaries SET title='new title', last_modified_time='2026-09-04 11:00:00.000000+00:00' WHERE conversation_id=?`, convID); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, mtime, size int64) bool {
		return mtime == first[0].SourceMtime && size == first[0].SourceSize
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Title != "new title" {
		t.Fatalf("native summary change was skipped: %+v", second)
	}
}

func TestDiscoverUsesNewestTranscriptActivity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	id := "newer-transcript"
	logDir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-05T10:00:00Z","content":"<USER_REQUEST>new activity</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "transcript_full.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	createSummariesDB(t, root, id, "title", 1, "2026-09-04 10:00:00.000000+00:00")

	summaries, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(summaries) != 1 {
		t.Fatalf("Discover = %+v, %v", summaries, err)
	}
	want := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	if !summaries[0].UpdatedAt.Equal(want) {
		t.Fatalf("UpdatedAt = %s, want transcript activity %s", summaries[0].UpdatedAt, want)
	}
}

func TestCompactOnlyTranscriptParticipatesInIncrementalRefresh(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	id := "compact-only"
	logDir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "transcript.jsonl")
	firstLine := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:00Z","content":"<USER_REQUEST>first title</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(path, []byte(firstLine), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(first) != 1 {
		t.Fatalf("first Discover = %+v, %v", first, err)
	}
	secondLine := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-04T10:00:01Z","content":"<USER_REQUEST>second and longer title</USER_REQUEST>"}` + "\n"
	if err := os.WriteFile(path, []byte(secondLine), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, mtime, size int64) bool {
		return mtime == first[0].SourceMtime && size == first[0].SourceSize
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Title != "second and longer title" || second[0].StoragePath != path {
		t.Fatalf("compact transcript change was skipped: %+v", second)
	}
}

func TestWriteRejectsIncompatibleSummarySchema(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversation_summaries (conversation_id text PRIMARY KEY, title text)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	conv := &model.Conversation{Provider: "pi", ProjectPath: root, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}
	res, err := agy.New().Write(context.Background(), conv, provider.WriteOpts{})
	if err == nil {
		t.Fatalf("Write accepted incompatible native schema: %+v", res)
	}
	if res != nil {
		t.Fatalf("failed Write returned an artifact without cleanup: %+v", res)
	}
}

func pTitle(t *testing.T, root, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var title string
	if err := db.QueryRow(`SELECT title FROM conversation_summaries WHERE conversation_id=?`, id).Scan(&title); err != nil {
		t.Fatal(err)
	}
	return title
}

func TestCleanupRejectsSymlinkBeforeChangingSummary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	p := agy.New()
	written, err := p.Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: root, Messages: []model.Message{{Role: model.RoleUser, Content: "keep"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	brainDir := filepath.Join(root, "brain", written.SessionID)
	if err := os.RemoveAll(brainDir); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, brainDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := p.CleanupWrite(context.Background(), *written); err == nil || !strings.Contains(err.Error(), "symlinked") {
		t.Fatalf("CleanupWrite error = %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries WHERE conversation_id=?`, written.SessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("unsafe delete changed the native summary before rejecting the symlink")
	}
}

func TestCleanupLeavesPostWriteAnnotationUntouched(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	p := agy.New()
	written, err := p.Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: root, Messages: []model.Message{{Role: model.RoleUser, Content: "keep"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	annotation := filepath.Join(root, "annotations", written.SessionID+".pbtxt")
	if err := os.MkdirAll(filepath.Dir(annotation), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(annotation, []byte("native annotation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupWrite(context.Background(), *written); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(annotation); err != nil || string(data) != "native annotation" {
		t.Fatalf("cleanup changed post-write annotation: data=%q err=%v", data, err)
	}
}

func TestActiveConversationCannotBeRenamed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	p := agy.New()
	written, err := p.Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: root, Messages: []model.Message{{Role: model.RoleUser, Content: "keep"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", written.SessionID)
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: written.SessionID}, "changed"); err == nil || !strings.Contains(err.Error(), "currently active") {
		t.Fatalf("active RenameSession error = %v", err)
	}
	if got := pTitle(t, root, written.SessionID); got == "changed" {
		t.Fatal("active conversation was renamed")
	}
}

func TestRenameMissingSessionReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	createSummariesDB(t, root, "existing", "old", 1, "2026-09-04 10:00:00.000000+00:00")
	err := agy.New().RenameSession(context.Background(), provider.SessionRef{ID: "00000000-0000-4000-8000-000000000099"}, "new")
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("RenameSession error = %v, want ErrNotFound", err)
	}
}

func TestWriteUsesPortableWindowsFileURI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	written, err := agy.New().Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: `C:\work tree`, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow(`SELECT workspace_uris FROM conversation_summaries WHERE conversation_id=?`, written.SessionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `["file:///C:/work%20tree"]` {
		t.Fatalf("workspace_uris = %q, want RFC 8089 Windows file URI", raw)
	}
}

func TestWriteUsesPortableUNCFileURI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	written, err := agy.New().Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: `\\server\share dir`, Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow(`SELECT workspace_uris FROM conversation_summaries WHERE conversation_id=?`, written.SessionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `["file://server/share%20dir"]` {
		t.Fatalf("workspace_uris = %q, want RFC 8089 UNC file URI", raw)
	}
}

func TestWritePreservesUnspecifiedTimestamps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	source := &model.Conversation{
		ID: "zero-time", Provider: "pi", ProjectPath: root,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "question"},
			{Role: model.RoleAssistant, Content: "answer"},
		},
	}
	p := agy.New()
	written, err := p.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: written.SessionID, StoragePath: written.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(source) != model.ContentDigest(loaded) {
		t.Fatalf("zero timestamps became specified: source=%s loaded=%s", model.ContentDigest(source), model.ContentDigest(loaded))
	}
}

func TestLoadPreview(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)

	p := agy.New()
	source := &model.Conversation{
		ID:          "source-preview",
		Provider:    "pi",
		ProjectPath: "/home/user/code",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "m1", Timestamp: time.Unix(1, 0)},
			{Role: model.RoleAssistant, Content: "m2", Timestamp: time.Unix(2, 0)},
			{Role: model.RoleUser, Content: "m3", Timestamp: time.Unix(3, 0)},
			{Role: model.RoleAssistant, Content: "m4", Timestamp: time.Unix(4, 0)},
		},
	}
	writeRes, err := p.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := p.LoadPreview(context.Background(), provider.SessionRef{
		ID:          writeRes.SessionID,
		StoragePath: writeRes.StoragePath,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Messages) != 2 {
		t.Fatalf("preview messages = %d, want 2", len(preview.Messages))
	}
	if preview.Messages[0].Content != "m3" || preview.Messages[1].Content != "m4" {
		t.Fatalf("unexpected preview messages: %+v", preview.Messages)
	}
}

// writeConversation lays down the transcript Antigravity writes for a
// conversation, which is all a current build leaves behind: since the title
// moved to annotations, no summary row is created for it at all.
func writeConversation(t *testing.T, root, id, text string) string {
	t.Helper()
	logDir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-08T09:00:00Z","content":"<USER_REQUEST>` + text + `</USER_REQUEST>"}` + "\n"
	path := filepath.Join(logDir, "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func annotationTitle(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "annotations", id+".pbtxt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRenameWithoutSummaryRow is the shape every conversation a current
// Antigravity build creates has: a transcript, an annotation, and no row in
// conversation_summaries. Renaming through the table alone reported the
// conversation as missing.
func TestRenameWithoutSummaryRow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000a1"
	writeConversation(t, root, id, "hello there")
	if err := os.MkdirAll(filepath.Join(root, "annotations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "annotations", id+".pbtxt"), []byte(`title:"native name"`), 0o644); err != nil {
		t.Fatal(err)
	}

	p := agy.New()
	if err := p.RenameSession(context.Background(), provider.SessionRef{ID: id}, "0908｜修复｜标题存储迁移"); err != nil {
		t.Fatalf("RenameSession error = %v", err)
	}
	if got := annotationTitle(t, root, id); got != `title:"0908｜修复｜标题存储迁移"` {
		t.Fatalf("annotation = %q, want the renamed title", got)
	}

	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Title != "0908｜修复｜标题存储迁移" {
		t.Fatalf("Discover = %+v, want the annotated title", sums)
	}
}

// TestDiscoverPrefersAnnotationOverSummaryRow covers the conversations that
// have both: the row was written by an older build and stops being updated,
// so the annotation is the current name.
func TestDiscoverPrefersAnnotationOverSummaryRow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000a2"
	createSummariesDB(t, root, id, "stale row title", 3, "2026-09-06 15:10:32.000000+00:00")
	writeConversation(t, root, id, "hello there")
	if err := os.MkdirAll(filepath.Join(root, "annotations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "annotations", id+".pbtxt"), []byte(`title:"current name"`), 0o644); err != nil {
		t.Fatal(err)
	}
	sums, err := agy.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Title != "current name" {
		t.Fatalf("Discover = %+v, want the annotated title", sums)
	}
}

// TestRenameUpdatesBothTitleStores keeps a conversation an older build created
// readable by that build: the row it reads is renamed alongside the annotation.
func TestRenameUpdatesBothTitleStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000a3"
	createSummariesDB(t, root, id, "old", 3, "2026-09-06 15:10:32.000000+00:00")
	writeConversation(t, root, id, "hello there")
	if err := agy.New().RenameSession(context.Background(), provider.SessionRef{ID: id}, "new"); err != nil {
		t.Fatalf("RenameSession error = %v", err)
	}
	if got := pTitle(t, root, id); got != "new" {
		t.Fatalf("summary row title = %q, want %q", got, "new")
	}
	if got := annotationTitle(t, root, id); got != `title:"new"` {
		t.Fatalf("annotation = %q, want the renamed title", got)
	}
}

// TestRenameKeepsUnknownAnnotationFields protects fields another has never
// seen: the annotation belongs to Antigravity, and a rename edits one value.
func TestRenameKeepsUnknownAnnotationFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000a4"
	writeConversation(t, root, id, "hello there")
	if err := os.MkdirAll(filepath.Join(root, "annotations"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "pinned:true\ntitle:\"old\"\nlabels:{name:\"title\" value:\"not this one\"}"
	if err := os.WriteFile(filepath.Join(root, "annotations", id+".pbtxt"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agy.New().RenameSession(context.Background(), provider.SessionRef{ID: id}, `say "hi"\now`); err != nil {
		t.Fatalf("RenameSession error = %v", err)
	}
	want := "pinned:true\ntitle:\"say \\\"hi\\\"\\\\now\"\nlabels:{name:\"title\" value:\"not this one\"}"
	if got := annotationTitle(t, root, id); got != want {
		t.Fatalf("annotation =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteAnnotatesMigratedConversation covers migration into Antigravity: a
// current build takes the name from the annotation, so a conversation written
// with only a summary row arrives unnamed.
func TestWriteAnnotatesMigratedConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	p := agy.New()
	written, err := p.Write(context.Background(), &model.Conversation{
		Provider: "pi", ProjectPath: root, Title: "0908｜研究｜迁移标题",
		Messages: []model.Message{{Role: model.RoleUser, Content: "keep"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := annotationTitle(t, root, written.SessionID); got != `title:"0908｜研究｜迁移标题"` {
		t.Fatalf("annotation = %q, want the migrated title", got)
	}
	if err := p.CleanupWrite(context.Background(), *written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "annotations", written.SessionID+".pbtxt")); !os.IsNotExist(err) {
		t.Fatalf("cleanup left its own annotation behind: %v", err)
	}
}

// writeFullConversation lays out every store one conversation occupies, which
// is what a delete has to clear: transcript, trajectory database, annotation,
// summary row, and the presence file AGY leaves behind.
func writeFullConversation(t *testing.T, root, id, title string) string {
	t.Helper()
	path := writeConversation(t, root, id, "hello there")
	createSummariesDB(t, root, id, title, 1, "2026-09-06 15:10:32.000000+00:00")
	for dir, name := range map[string]string{
		"annotations":   id + ".pbtxt",
		"conversations": id + ".db",
		"presence":      id + ".lock",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte("")
		if dir == "annotations" {
			body = []byte(`title:"` + title + `"`)
		}
		if err := os.WriteFile(filepath.Join(root, dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestDeleteSessionClearsEveryStore is the whole capability: after a delete no
// store still names the conversation, so it cannot come back in a later listing
// through whichever store that listing happens to read.
func TestDeleteSessionClearsEveryStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000b1"
	path := writeFullConversation(t, root, id, "old name")

	p := agy.New()
	if err := p.DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: path}); err != nil {
		t.Fatalf("DeleteSession error = %v", err)
	}
	for _, gone := range []string{
		filepath.Join(root, "brain", id),
		filepath.Join(root, "conversations", id+".db"),
		filepath.Join(root, "annotations", id+".pbtxt"),
		filepath.Join(root, "presence", id+".lock"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the delete: %v", gone, err)
		}
	}
	if err := summaryRowGone(t, root, id); err != nil {
		t.Error(err)
	}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("Discover = %+v, want the deleted conversation gone", sums)
	}
}

// TestDeleteSessionRemovesWholeBrainDirectory separates a delete from the
// post-write rollback next to it. Rollback keeps anything AGY added, because it
// owns only what the migration wrote; a delete is the person removing their own
// session, and the uploads and scratch inside it go too.
func TestDeleteSessionRemovesWholeBrainDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000b2"
	path := writeFullConversation(t, root, id, "old name")
	upload := filepath.Join(root, "brain", id, ".user_uploaded", "screenshot.png")
	if err := os.MkdirAll(filepath.Dir(upload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upload, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: path}); err != nil {
		t.Fatalf("DeleteSession error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "brain", id)); !os.IsNotExist(err) {
		t.Fatalf("brain directory survived the delete: %v", err)
	}
}

// TestDeleteSessionWithoutSummaryRow is the shape a current Antigravity build
// creates: no row in the legacy table. Its absence is not a missing session.
func TestDeleteSessionWithoutSummaryRow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000b3"
	path := writeConversation(t, root, id, "hello there")
	if err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: path}); err != nil {
		t.Fatalf("DeleteSession error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "brain", id)); !os.IsNotExist(err) {
		t.Fatalf("brain directory survived the delete: %v", err)
	}
}

// TestDeleteMissingSession keeps a delete of something already gone
// distinguishable from a delete that failed.
func TestDeleteMissingSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000b4"
	err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: id})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("DeleteSession error = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(root, "presence", id+".lock")); !os.IsNotExist(err) {
		t.Fatalf("a delete that found nothing left a presence file: %v", err)
	}
}

// TestDeleteRefusesActiveConversation protects the conversation AGY has open:
// its files are still being written, so the delete is refused rather than
// pulled out from under the running CLI.
func TestDeleteRefusesActiveConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	const id = "00000000-0000-4000-8000-0000000000b5"
	path := writeFullConversation(t, root, id, "old name")
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", id)
	err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: path})
	if err == nil || !strings.Contains(err.Error(), "currently active") {
		t.Fatalf("DeleteSession error = %v, want the active conversation refused", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a refused delete removed the transcript: %v", err)
	}
}

// TestDeleteRejectsUnparseableID stops a ref from steering a delete outside the
// per-conversation layout: every path removed is built from this id.
func TestDeleteRejectsUnparseableID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGY_HOME", root)
	if err := agy.New().DeleteSession(context.Background(), provider.SessionRef{ID: "../.."}); err == nil {
		t.Fatal("DeleteSession accepted an id that is not a conversation")
	}
}

func summaryRowGone(t *testing.T, root, id string) error {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries WHERE conversation_id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		return errors.New("the summary row survived the delete")
	}
	return nil
}
