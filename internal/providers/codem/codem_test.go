package codem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/util"
)

const (
	demoProject  = "/Users/example/work/demo"
	notesProject = "/Users/example/work/notes"
	demoID       = "20260817T013841-fda3cceed8fd4a4a8f0bf2f1eb7497a1"
	feishuID     = "sess_feishu_p2p_1838d9652f1b4dc8"
	startedID    = "20260825T054116-718a7380ca014b4398dc276ac2bb1eaf"
)

// key is the id another carries for a session: CodeM's id qualified by the
// hash of the directory it belongs to.
func key(project, id string) string { return sessionKey(projectHash(project), id) }

type fixture struct {
	file    string
	project string
	id      string
}

// store lays the fixtures out the way CodeM does: one directory per working
// directory, named by the hash of that directory, with the transcript inside.
func store(t *testing.T, fixtures ...fixture) *Provider {
	t.Helper()
	root := t.TempDir()
	for _, f := range fixtures {
		dir := filepath.Join(root, "sessions", projectHash(f.project))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join("testdata", f.file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f.id+".jsonl"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &Provider{root: root}
}

func summaries(t *testing.T, p *Provider, opts provider.DiscoverOpts) map[string]model.Summary {
	t.Helper()
	found, err := p.Discover(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]model.Summary, len(found))
	for _, sm := range found {
		out[sm.ID] = sm
	}
	return out
}

// The directory a session belongs to cannot be read off the path: CodeM names
// the directory with a one-way hash. It has to come out of the header.
func TestDiscoverAttributesSessionsToTheDirectoryInTheHeader(t *testing.T) {
	p := store(t,
		fixture{"session.jsonl", demoProject, demoID},
		fixture{"feishu.jsonl", notesProject, feishuID},
	)
	found := summaries(t, p, provider.DiscoverOpts{})
	if len(found) != 2 {
		t.Fatalf("discovered %d sessions, want 2: %+v", len(found), found)
	}
	demo := found[key(demoProject, demoID)]
	if demo.ProjectPath != demoProject {
		t.Errorf("project = %q, want %q", demo.ProjectPath, demoProject)
	}
	if demo.MessageCount != 5 {
		t.Errorf("message count = %d, want 5", demo.MessageCount)
	}
	if demo.Title != "检查一下构建脚本" {
		t.Errorf("title = %q, want the first real user message", demo.Title)
	}
	// Two headers, one for the original run and one for the resume. The
	// session started at the first of them.
	if want := time.Date(2026, 8, 17, 1, 38, 41, 870582000, time.UTC); !demo.CreatedAt.UTC().Equal(want) {
		t.Errorf("created = %s, want the earliest header start %s", demo.CreatedAt.UTC(), want)
	}
	if want := time.Date(2026, 8, 18, 2, 2, 20, 0, time.UTC); !demo.UpdatedAt.UTC().Equal(want) {
		t.Errorf("updated = %s, want the last message %s", demo.UpdatedAt.UTC(), want)
	}
	if demo.Kind != model.SessionKindRoot || demo.Provider != ProviderID {
		t.Errorf("summary identity = %q/%q", demo.Provider, demo.Kind)
	}
	if got := found[key(notesProject, feishuID)]; got.ProjectPath != notesProject {
		t.Errorf("feishu session project = %q, want %q", got.ProjectPath, notesProject)
	}
}

// A CodeM store holds three things that are not transcripts: the scratchpad
// directory named after a session, the Feishu client's loose session records,
// and sessions that were opened and never used.
func TestDiscoverSkipsScratchpadsRecordsAndUnusedSessions(t *testing.T) {
	p := store(t,
		fixture{"session.jsonl", demoProject, demoID},
		fixture{"started.jsonl", demoProject, startedID},
	)
	sessions := filepath.Join(p.sessionsRoot(), projectHash(demoProject))
	scratch := filepath.Join(sessions, demoID, "tool-results")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "call_1.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(p.sessionsRoot(), "sess_feishu_7dc6e0f8a1a606f7.json")
	if err := os.WriteFile(record, []byte(`{"id":"sess_feishu_7dc6e0f8a1a606f7"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	found := summaries(t, p, provider.DiscoverOpts{})
	if len(found) != 1 {
		t.Fatalf("discovered %d sessions, want only the one with messages: %+v", len(found), found)
	}
	if _, ok := found[key(demoProject, demoID)]; !ok {
		t.Fatalf("the real session is missing: %+v", found)
	}
}

// CodeM guarantees an id inside one directory and nowhere else: the Feishu
// client opens the same sess_feishu_p2p_<peer> id in every project. Two of
// those are two conversations, and the index keys on the id, so an
// unqualified id would drop one of them.
func TestOneCodeMIDInTwoDirectoriesStaysTwoSessions(t *testing.T) {
	p := store(t,
		fixture{"feishu.jsonl", notesProject, feishuID},
		fixture{"session.jsonl", demoProject, feishuID},
	)
	found := summaries(t, p, provider.DiscoverOpts{})
	if len(found) != 2 {
		t.Fatalf("one id in two directories collapsed to %d session(s): %+v", len(found), found)
	}
	notes, demo := found[key(notesProject, feishuID)], found[key(demoProject, feishuID)]
	if notes.ProjectPath != notesProject || demo.ProjectPath != demoProject {
		t.Fatalf("directories crossed: %q and %q", notes.ProjectPath, demo.ProjectPath)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: demo.ID})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProjectPath != demoProject {
		t.Errorf("loading by qualified id reached %q, want %q", conv.ProjectPath, demoProject)
	}
	// The hash is another's, not CodeM's: it must not reach the resume line.
	got := p.ResumeCommand(provider.WriteResult{SessionID: demo.ID, ProjectPath: demoProject})
	if got != "cd '"+demoProject+"' && codem --resume '"+feishuID+"'" {
		t.Fatalf("resume command = %q", got)
	}
}

func TestDiscoverHonorsSkipAndFilter(t *testing.T) {
	p := store(t,
		fixture{"session.jsonl", demoProject, demoID},
		fixture{"feishu.jsonl", notesProject, feishuID},
	)
	var asked []string
	found := summaries(t, p, provider.DiscoverOpts{SkipSource: func(path string, _, _ int64) bool {
		asked = append(asked, filepath.Base(path))
		return strings.HasPrefix(filepath.Base(path), "sess_feishu")
	}})
	if len(asked) != 2 {
		t.Fatalf("skip callback saw %v, want both transcripts", asked)
	}
	if _, ok := found[key(notesProject, feishuID)]; ok {
		t.Errorf("a skipped source was summarized anyway: %+v", found)
	}

	filtered := summaries(t, p, provider.DiscoverOpts{ProjectFilter: "notes"})
	if len(filtered) != 1 || filtered[key(notesProject, feishuID)].ID != key(notesProject, feishuID) {
		t.Fatalf("project filter = %+v, want only the notes session", filtered)
	}

	limited, err := p.Discover(context.Background(), provider.DiscoverOpts{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit = %d sessions, err=%v", len(limited), err)
	}
}

func TestLoadKeepsOnlyThePortableTurns(t *testing.T) {
	p := store(t, fixture{"session.jsonl", demoProject, demoID})
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: key(demoProject, demoID), ProjectPath: demoProject})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		role model.Role
		text string
	}{
		{model.RoleUser, "检查一下构建脚本"},
		{model.RoleAssistant, "我先看一下 Makefile。"},
		{model.RoleAssistant, "构建目标只有 `go build ./...`，没有测试步骤。"},
		{model.RoleUser, "补一个 test 目标"},
		{model.RoleAssistant, "已加上 `test: go test ./...`。"},
	}
	if len(conv.Messages) != len(want) {
		t.Fatalf("loaded %d messages, want %d: %+v", len(conv.Messages), len(want), conv.Messages)
	}
	for i, expected := range want {
		got := conv.Messages[i]
		if got.Role != expected.role || got.PlainText() != expected.text {
			t.Errorf("message %d = %s/%q, want %s/%q", i, got.Role, got.PlainText(), expected.role, expected.text)
		}
		if got.Timestamp.IsZero() {
			t.Errorf("message %d lost its timestamp", i)
		}
	}
	for _, text := range []string{"用户想让我检查构建脚本", "cat Makefile", "system-reminder"} {
		for _, msg := range conv.Messages {
			if strings.Contains(msg.PlainText(), text) {
				t.Errorf("a non-conversation record reached the portable model: %q", msg.PlainText())
			}
		}
	}
}

// Every CodeM transcript ends on a turn_end rather than on a message, and a
// ref from the index may carry an id and nothing else.
func TestLoadFindsASessionWithoutAStoredPath(t *testing.T) {
	p := store(t, fixture{"feishu.jsonl", notesProject, feishuID})
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: key(notesProject, feishuID)})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProjectPath != notesProject || len(conv.Messages) != 2 {
		t.Fatalf("load without a project = %q with %d messages", conv.ProjectPath, len(conv.Messages))
	}
	if _, err := p.Load(context.Background(), provider.SessionRef{ID: feishuID}); err != nil {
		t.Fatalf("an unqualified id did not resolve: %v", err)
	}
	if _, err := p.Load(context.Background(), provider.SessionRef{ID: "../../escape"}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("a traversing id resolved to %v", err)
	}
}

func TestLoadPreviewKeepsTheMostRecentMessages(t *testing.T) {
	p := store(t, fixture{"session.jsonl", demoProject, demoID})
	ref := provider.SessionRef{ID: key(demoProject, demoID), ProjectPath: demoProject}
	preview, err := p.LoadPreview(context.Background(), ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Messages) != 2 {
		t.Fatalf("preview held %d messages, want 2", len(preview.Messages))
	}
	if preview.Messages[0].PlainText() != "补一个 test 目标" || preview.Messages[1].PlainText() != "已加上 `test: go test ./...`。" {
		t.Errorf("preview held %+v, want the two newest messages", preview.Messages)
	}
	// A window over the tail must not move the session's own start or title.
	full, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CreatedAt.Equal(full.CreatedAt) || preview.Title != full.Title {
		t.Errorf("preview reported %s/%q, want %s/%q", preview.CreatedAt, preview.Title, full.CreatedAt, full.Title)
	}
}

func TestWriteRoundTripPreservesTheContentDigest(t *testing.T) {
	p := store(t, fixture{"session.jsonl", demoProject, demoID})
	source, err := p.Load(context.Background(), provider.SessionRef{ID: key(demoProject, demoID), ProjectPath: demoProject})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Write(context.Background(), source, provider.WriteOpts{ProjectPath: demoProject})
	if err != nil {
		t.Fatal(err)
	}
	written, err := p.Load(context.Background(), provider.SessionRef{ID: result.SessionID, StoragePath: result.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(written) != model.ContentDigest(source) {
		t.Fatalf("round trip changed the conversation:\n%+v\n%+v", source.Messages, written.Messages)
	}
	if written.Migration == nil || written.Migration.OriginID != source.ID {
		t.Fatalf("migration marker did not survive: %+v", written.Migration)
	}
	if written.ID != result.SessionID {
		t.Errorf("written session id = %q, want %q", written.ID, result.SessionID)
	}
}

// CodeM finds a session by hashing the directory it is run in, so a transcript
// that lands anywhere else cannot be resumed at all.
func TestWriteLandsUnderTheDirectoryHash(t *testing.T) {
	p := store(t)
	conv := &model.Conversation{
		ID: "origin", Provider: "pi", Messages: []model.Message{
			{Role: model.RoleUser, Content: "hello", Timestamp: time.Unix(1, 0).UTC()},
			{Role: model.RoleAssistant, Content: "hi", Timestamp: time.Unix(2, 0).UTC()},
		},
	}
	result, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: demoProject})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(p.sessionsRoot(), projectHash(demoProject))
	if filepath.Dir(result.StoragePath) != wantDir {
		t.Fatalf("wrote to %s, want %s", filepath.Dir(result.StoragePath), wantDir)
	}
	hash, id := splitSessionKey(result.SessionID)
	if hash != projectHash(demoProject) || len(id) != 32 {
		t.Errorf("session id %q is not a directory hash and CodeM's 32-character id", result.SessionID)
	}
	first, err := os.ReadFile(result.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Type    string `json:"type"`
		Version int    `json:"schema_version"`
		ID      string `json:"session_id"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(string(first), "\n", 2)[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Type != recordHeader || header.Version != schemaVersion || header.ID != id || header.CWD != demoProject {
		t.Fatalf("header = %+v", header)
	}
	if got := p.ResumeCommand(*result); got != "cd '"+demoProject+"' && codem --resume '"+id+"'" {
		t.Fatalf("resume command = %q", got)
	}
}

func TestWriteRefusesAnEmptySessionAndHonorsDryRun(t *testing.T) {
	p := store(t)
	empty := &model.Conversation{ID: "origin", Provider: "pi", Messages: []model.Message{
		{Role: model.RoleSystem, Content: "setup"},
	}}
	if _, err := p.Write(context.Background(), empty, provider.WriteOpts{ProjectPath: demoProject}); !errors.Is(err, provider.ErrEmptySession) {
		t.Fatalf("empty session write = %v", err)
	}
	conv := &model.Conversation{ID: "origin", Provider: "pi", Messages: []model.Message{
		{Role: model.RoleUser, Content: "hello"},
	}}
	result, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: demoProject, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("a dry run left %s behind", result.StoragePath)
	}
}

func TestCleanupWriteRemovesOnlyItsOwnArtifact(t *testing.T) {
	p := store(t, fixture{"session.jsonl", demoProject, demoID})
	conv := &model.Conversation{ID: "origin", Provider: "pi", Messages: []model.Message{
		{Role: model.RoleUser, Content: "hello"},
	}}
	result, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: demoProject})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupWrite(context.Background(), *result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s behind", result.StoragePath)
	}
	if _, err := os.Stat(p.transcriptPath(demoProject, demoID)); err != nil {
		t.Fatalf("cleanup touched an unrelated session: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = p.CleanupWrite(context.Background(), provider.WriteResult{
		SessionID: "elsewhere", StoragePath: outside, ProjectPath: demoProject,
	})
	if err == nil {
		t.Fatal("cleanup accepted a path outside the store")
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("a refused cleanup removed the file anyway: %v", statErr)
	}
}

func TestDeleteSessionRemovesTheTranscriptAndItsScratchpad(t *testing.T) {
	p := store(t,
		fixture{"session.jsonl", demoProject, demoID},
		fixture{"feishu.jsonl", notesProject, feishuID},
	)
	path := p.transcriptPath(demoProject, demoID)
	scratch := filepath.Join(filepath.Dir(path), demoID, "scratchpad")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	ref := provider.SessionRef{ID: key(demoProject, demoID), ProjectPath: demoProject, StoragePath: path}
	if err := p.DeleteSession(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the transcript survived the delete")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), demoID)); !os.IsNotExist(err) {
		t.Errorf("the scratchpad directory survived the delete")
	}
	if _, err := os.Stat(p.transcriptPath(notesProject, feishuID)); err != nil {
		t.Errorf("delete reached another session: %v", err)
	}
	if err := p.DeleteSession(context.Background(), ref); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("deleting a session twice = %v, want not found", err)
	}
}

func TestDeleteSessionRefusesALinkOutOfTheStore(t *testing.T) {
	p := store(t)
	target := filepath.Join(t.TempDir(), "precious.jsonl")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.sessionsRoot(), projectHash(demoProject))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := p.DeleteSession(context.Background(), provider.SessionRef{ID: "linked", ProjectPath: demoProject, StoragePath: link})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("delete followed a symlink: %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("the link target was removed: %v", statErr)
	}
}

// CodeM keeps no title of its own and has no operation that moves a session
// between directories, so another must not offer either. These are contracts,
// not gaps waiting to be filled with another-only state.
func TestUnsupportedLifecycleOperationsAreAbsent(t *testing.T) {
	var p any = New()
	if _, ok := p.(provider.SessionRenamer); ok {
		t.Error("codem claims a rename, but CodeM stores no title")
	}
	if _, ok := p.(provider.SessionArchiver); ok {
		t.Error("codem claims an archive state CodeM does not have")
	}
	if _, ok := p.(provider.SessionRelocator); ok {
		t.Error("codem claims a relocate, but the directory is a one-way hash")
	}
}

func TestWriteFailureLeavesNothingBehind(t *testing.T) {
	p := store(t)
	original := writeFile
	t.Cleanup(func() { writeFile = original })
	writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	conv := &model.Conversation{ID: "origin", Provider: "pi", Messages: []model.Message{
		{Role: model.RoleUser, Content: "hello"},
	}}
	if _, err := p.Write(context.Background(), conv, provider.WriteOpts{ProjectPath: demoProject}); err == nil {
		t.Fatal("a failed write reported success")
	}
	entries, err := os.ReadDir(filepath.Join(p.sessionsRoot(), projectHash(demoProject)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed write left %v", entries)
	}
}

func TestResumeCommandQuotesHostileValues(t *testing.T) {
	p := New()
	got := p.ResumeCommand(provider.WriteResult{SessionID: "id; rm -rf /", ProjectPath: "/tmp/a b"})
	if got != "cd '/tmp/a b' && codem --resume 'id; rm -rf /'" {
		t.Fatalf("resume command = %q", got)
	}
	if got := p.ResumeCommand(provider.WriteResult{SessionID: "abc"}); got != "codem --resume 'abc'" {
		t.Fatalf("resume command without a project = %q", got)
	}
}

func TestDefaultPathsAndInstallation(t *testing.T) {
	p := store(t)
	paths := p.DefaultPaths()
	if len(paths) != 1 || paths[0].Env != "CODEM_HOME" || paths[0].Path != p.sessionsRoot() {
		t.Fatalf("default paths = %+v", paths)
	}
	if err := os.MkdirAll(p.sessionsRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	if !p.Installed() {
		t.Error("a store on disk did not count as installed")
	}
}

// projectHash is the whole attribution contract: CodeM writes to the sha256 of
// the working directory, truncated. A drift here silently hides every session.
func TestProjectHashMatchesCodeM(t *testing.T) {
	if got := projectHash("/Users/mingjian"); got != "d471ecb5f5850404" {
		t.Fatalf("projectHash(/Users/mingjian) = %q, want the hash CodeM 0.1.205 uses", got)
	}
}

func TestSkipsCodeMInjectedUserTurns(t *testing.T) {
	if !skipUserMessage("<system-reminder>\n<skill-instructions name=\"x\">") {
		t.Error("an injected skill block was treated as a prompt")
	}
	if skipUserMessage("检查一下构建脚本") {
		t.Error("a real prompt was dropped")
	}
	if !skipUserMessage(util.DisplayUserText("<user_query>")) {
		t.Error("the shared injected-turn rules were not applied")
	}
}
