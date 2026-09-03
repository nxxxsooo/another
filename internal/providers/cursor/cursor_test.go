package cursor

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/util"
)

func TestDiscoverSeesWALOnlyStoreChange(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(root, "chats", "project", "session", "store.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
PRAGMA journal_mode=WAL;
PRAGMA wal_autocheckpoint=0;
CREATE TABLE meta (key INTEGER, value BLOB);
CREATE TABLE blobs (data BLOB);
INSERT INTO meta VALUES (0, '{"title":"WAL session"}');
INSERT INTO blobs VALUES ('{"role":"user","content":"first"}');
PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
		t.Fatal(err)
	}
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	before, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(before) != 1 {
		t.Fatalf("initial discover: summaries=%d err=%v", len(before), err)
	}
	mainBefore, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.Stat(storePath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO blobs VALUES ('{"role":"assistant","content":"second"}')`); err != nil {
		t.Fatal(err)
	}
	mainAfter, err := os.Stat(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if mainAfter.Size() != mainBefore.Size() || !mainAfter.ModTime().Equal(mainBefore.ModTime()) {
		t.Fatal("test setup checkpointed the WAL into store.db")
	}
	walAfter, err := os.Stat(storePath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if walAfter.Size() == walBefore.Size() && walAfter.ModTime().Equal(walBefore.ModTime()) {
		t.Fatal("test setup did not change store.db-wal")
	}

	stamp := before[0]
	callbackCalls := 0
	after, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(path string, mtime, size int64) bool {
		callbackCalls++
		return path == stamp.StoragePath && mtime == stamp.SourceMtime && size == stamp.SourceSize
	}})
	if err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 1 || len(after) != 1 {
		t.Fatalf("WAL change was skipped: callbacks=%d summaries=%d", callbackCalls, len(after))
	}
	if after[0].SourceMtime == stamp.SourceMtime && after[0].SourceSize == stamp.SourceSize {
		t.Fatal("WAL change did not alter source stamp")
	}
}

func TestDiscoverUsesCursorCLISidecarsForWorkspaceTimeAndKind(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "ssh")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	rootWrite, err := p.Write(context.Background(), &model.Conversation{
		ID: "root-origin", Provider: "codex", ProjectPath: project, Title: "SSH Tunnel Helper",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	subWrite, err := p.Write(context.Background(), &model.Conversation{
		ID: "sub-origin", Provider: "codex", ProjectPath: project, Title: "New Agent",
		Messages: []model.Message{{Role: model.RoleUser, Content: "child"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 6, 24, 17, 46, 7, 0, time.Local)
	updated := time.Date(2026, 8, 25, 4, 40, 52, 0, time.Local)
	writeMeta := func(path string, meta cursorSidecar) {
		t.Helper()
		data, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(cursorSidecarPath(path), data, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	writeMeta(rootWrite.StoragePath, cursorSidecar{
		SchemaVersion: 1, CreatedAtMillis: created.UnixMilli(), UpdatedAtMillis: updated.UnixMilli(),
		HasConversation: true, Title: "SSH Tunnel Helper", CWD: project,
	})
	writeMeta(subWrite.StoragePath, cursorSidecar{
		SchemaVersion: 1, CreatedAtMillis: created.UnixMilli(), UpdatedAtMillis: updated.UnixMilli(),
		HasConversation: true, Title: "New Agent", IsSubagent: true,
	})
	transcript := filepath.Join(p.projectsRoot, strings.TrimPrefix(util.EncodeClaudeProjectPath(project), "-"),
		"agent-transcripts", rootWrite.SessionID, rootWrite.SessionID+".jsonl")
	conflicting, _ := json.Marshal(map[string]any{
		"role": "user", "cwd": filepath.Join(root, "wrong"),
		"message": map[string]any{"role": "user", "content": "wrong transcript hint"},
	})
	if err := os.WriteFile(transcript, append(conflicting, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	stores := make(map[string]model.Summary)
	for _, summary := range summaries {
		if strings.HasSuffix(summary.StoragePath, "store.db") {
			stores[summary.ID] = summary
		}
	}
	// Summaries report the canonical project path. On macOS the temp dir sits
	// under /var, a symlink to /private/var, so the raw fixture path and the
	// resolved one differ; every summary must still land on the same string.
	wantProject := util.NormalizeProjectPath(project)
	rootSummary := stores[rootWrite.SessionID]
	if rootSummary.ProjectPath != wantProject || rootSummary.Title != "SSH Tunnel Helper" ||
		!rootSummary.CreatedAt.Equal(created) || !rootSummary.UpdatedAt.Equal(updated) || rootSummary.Kind != model.SessionKindRoot {
		t.Fatalf("root summary: %+v", rootSummary)
	}
	subSummary := stores[subWrite.SessionID]
	if subSummary.ProjectPath != wantProject || subSummary.Kind != model.SessionKindSubagent {
		t.Fatalf("subagent summary: %+v", subSummary)
	}
	if rootSummary.ProjectPath != subSummary.ProjectPath {
		t.Fatalf("one project rendered two ways: root %q vs subagent %q", rootSummary.ProjectPath, subSummary.ProjectPath)
	}
}

func TestDiscoverRefreshesWhenCursorSidecarChanges(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	write, err := p.Write(context.Background(), &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: filepath.Join(root, "project"), Title: "Before",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(write.StoragePath, now.Add(2*time.Hour), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cursorSidecarPath(write.StoragePath), now, now); err != nil {
		t.Fatal(err)
	}
	before, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var stamp model.Summary
	for _, summary := range before {
		if summary.StoragePath == write.StoragePath {
			stamp = summary
		}
	}
	meta, err := readCursorSidecar(write.StoragePath)
	if err != nil || meta == nil {
		t.Fatalf("sidecar: %+v err=%v", meta, err)
	}
	oldData, err := os.ReadFile(cursorSidecarPath(write.StoragePath))
	if err != nil {
		t.Fatal(err)
	}
	meta.Title = "After!"
	meta.UpdatedAtMillis += int64(time.Hour / time.Millisecond)
	data, _ := json.Marshal(meta)
	if len(data) != len(oldData) {
		t.Fatalf("test requires same-size metadata: before=%d after=%d", len(oldData), len(data))
	}
	if err := os.WriteFile(cursorSidecarPath(write.StoragePath), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cursorSidecarPath(write.StoragePath), now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(path string, mtime, size int64) bool {
		return path == stamp.StoragePath && mtime == stamp.SourceMtime && size == stamp.SourceSize
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range after {
		if summary.StoragePath == write.StoragePath && summary.Title == meta.Title {
			return
		}
	}
	t.Fatalf("sidecar-only change was skipped: %+v", after)
}

func TestDiscoverNeverUsesWorkspaceHashAsProject(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	write, err := p.Write(context.Background(), &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: filepath.Join(root, "project"), Title: "Legacy",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorSidecarPath(write.StoragePath), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", write.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='0'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	meta := decodeCursorMeta(raw)
	delete(meta, "projectPath")
	delete(meta, "workspaceUri")
	plain, _ := json.Marshal(meta)
	if _, err := db.Exec(`UPDATE meta SET value=? WHERE key='0'`, hex.EncodeToString(plain)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p.projectsRoot); err != nil {
		t.Fatal(err)
	}
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if summary.StoragePath == write.StoragePath {
			if summary.ProjectPath != "" {
				t.Fatalf("invented project path from workspace hash: %q", summary.ProjectPath)
			}
			return
		}
	}
	t.Fatal("store summary not found")
}

func TestCursorWorkspaceStorageFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux workspaceStorage layout")
	}
	home := t.TempDir()
	workspace := "workspace-hash"
	project := filepath.Join(home, "project with space")
	path := filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage", workspace, "workspace.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"folder": "file://" + project})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cursorWorkspaceStorageProject(home, workspace); got != project {
		t.Fatalf("workspace project = %q, want %q", got, project)
	}
}

func TestLoadPreviewUsesBoundedRecentMessagesAndSidecar(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	created := time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC)
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	write, err := p.Write(context.Background(), &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: project, Title: "Preview title", CreatedAt: created,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "one", Timestamp: created},
			{Role: model.RoleAssistant, Content: "two", Timestamp: created.Add(time.Second)},
			{Role: model.RoleUser, Content: "three", Timestamp: created.Add(2 * time.Second)},
		},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := p.LoadPreview(context.Background(), provider.SessionRef{
		ID: write.SessionID, StoragePath: write.StoragePath, ProjectPath: project,
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 || conv.Title != "Preview title" || conv.ProjectPath != project {
		t.Fatalf("preview: %+v", conv)
	}
	if conv.Messages[0].Content != "two" || conv.Messages[1].Content != "three" {
		t.Fatalf("recent messages: %+v", conv.Messages)
	}
}

func TestLoadStoreRefPrefersMatchingProjectTranscript(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	id := "session-id"
	encoded := strings.TrimPrefix(util.EncodeClaudeProjectPath(project), "-")
	path := filepath.Join(p.projectsRoot, encoded, "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"user","message":{"content":"from transcript"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{
		ID: id, ProjectPath: project, StoragePath: filepath.Join(root, "missing", "store.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 1 || conv.Messages[0].Content != "from transcript" {
		t.Fatalf("conversation = %+v", conv)
	}
}

func TestStoredMessageRawHexAndProtobuf(t *testing.T) {
	raw := []byte(`{"role":"assistant","content":[{"type":"text","text":"one"},{"type":"tool_use"},{"type":"text","text":"[REDACTED]"},{"type":"text","text":"two"}]}`)
	for name, data := range map[string][]byte{
		"raw":      raw,
		"hex":      []byte(hex.EncodeToString(raw)),
		"protobuf": append([]byte{0x0a, byte(len(raw))}, raw...),
	} {
		t.Run(name, func(t *testing.T) {
			role, text, _ := cursorStoredMessage(data)
			if role != "assistant" || text != "one\ntwo" {
				t.Fatalf("got role=%q text=%q", role, text)
			}
		})
	}
}

func TestExtractCursorMessageDropsRedactedToolPlaceholder(t *testing.T) {
	row := map[string]any{"message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "[REDACTED]"},
		map[string]any{"type": "tool_use", "name": "shell"},
	}}}
	if got := extractCursorMessage(row); got != "" {
		t.Fatalf("redacted placeholder = %q", got)
	}
}

func TestExtractCursorMessageKeepsAnswerButDropsRedactedBlock(t *testing.T) {
	row := map[string]any{"message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "real answer"},
		map[string]any{"type": "text", "text": "[REDACTED]"},
		map[string]any{"type": "tool_use", "name": "shell"},
	}}}
	if got := extractCursorMessage(row); got != "real answer" {
		t.Fatalf("message = %q", got)
	}
	row = map[string]any{"message": map[string]any{"content": " [REDACTED] "}}
	if got := extractCursorMessage(row); got != "" {
		t.Fatalf("string placeholder = %q", got)
	}
	row = map[string]any{"message": map[string]any{"content": "real answer\n\n[REDACTED]"}}
	if got := extractCursorMessage(row); got != "real answer" {
		t.Fatalf("inline placeholder = %q", got)
	}
}

func TestLoadStoreTraversesActiveGraphInReferenceOrder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "store.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value BLOB); CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	message := func(role, text string) []byte {
		data, _ := json.Marshal(map[string]any{"role": role, "content": text, "timestamp": "2026-08-17T01:02:03.456789Z"})
		return data
	}
	raw := message("user", "first")
	hexMessage := []byte(hex.EncodeToString(message("assistant", "second")))
	wrappedJSON := message("user", "third")
	wrapped := append([]byte{0x12, byte(len(wrappedJSON))}, wrappedJSON...)
	stale := message("assistant", "unreachable stale")
	ids := []string{strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64)}
	rootData := appendProtoBytes(nil, 1, mustDecodeHex(t, ids[1]))
	rootData = appendProtoBytes(rootData, 1, mustDecodeHex(t, ids[0]))
	rootData = appendProtoBytes(rootData, 1, mustDecodeHex(t, ids[2]))
	rootData = appendProtoBytes(rootData, 18, mustDecodeHex(t, ids[3]))
	rootID := strings.Repeat("a", 64)
	for i, data := range [][]byte{raw, hexMessage, wrapped, stale} {
		if _, err := db.Exec(`INSERT INTO blobs VALUES (?, ?)`, ids[i], data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO blobs VALUES (?, ?)`, rootID, rootData); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"agentId": "session", "latestRootBlobId": rootID})
	if _, err := db.Exec(`INSERT INTO meta VALUES ('0', ?)`, hex.EncodeToString(meta)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := (&Provider{}).loadStore(path, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 3 || loaded.Messages[0].Content != "second" || loaded.Messages[1].Content != "first" || loaded.Messages[2].Content != "third" {
		t.Fatalf("active messages = %+v", loaded.Messages)
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestDiscoverEnrichesStoreFromMatchingTranscript(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	conv := &model.Conversation{ID: "origin", Provider: "codex", ProjectPath: "/real/project", Messages: []model.Message{{Role: model.RoleUser, Content: "Real transcript title"}}}
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", write.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='0'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	meta := decodeCursorMeta(raw)
	delete(meta, "projectPath")
	delete(meta, "workspaceUri")
	delete(meta, "name")
	plain, _ := json.Marshal(meta)
	if _, err := db.Exec(`UPDATE meta SET value=? WHERE key='0'`, hex.EncodeToString(plain)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cursorSidecarPath(write.StoragePath)); err != nil {
		t.Fatal(err)
	}
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sm := range summaries {
		if sm.StoragePath == write.StoragePath {
			if sm.ProjectPath != conv.ProjectPath || sm.Title != "Real transcript title" {
				t.Fatalf("store was not enriched: %+v", sm)
			}
			return
		}
	}
	t.Fatal("store summary not found")
}

func TestWriteCreatesNativeStoreAndTranscriptRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	created := time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)
	conv := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: filepath.Join(root, "project with space"),
		Title: "Native migration", CreatedAt: created,
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "  repeat\r\n", Timestamp: created},
			{Role: model.RoleAssistant, Content: "\tanswer  ", Timestamp: created.Add(time.Second)},
			{Role: model.RoleUser, Content: "repeat", Timestamp: created.Add(2 * time.Second)},
		},
	}
	write, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(write.StoragePath) != "store.db" {
		t.Fatalf("storage path = %q", write.StoragePath)
	}
	if _, err := os.Stat(write.StoragePath); err != nil {
		t.Fatal(err)
	}
	sidecar, err := readCursorSidecar(write.StoragePath)
	if err != nil || sidecar == nil {
		t.Fatalf("sidecar: %+v err=%v", sidecar, err)
	}
	if sidecar.SchemaVersion != 1 || !sidecar.HasConversation || sidecar.CWD != conv.ProjectPath || sidecar.Title != conv.Title {
		t.Fatalf("sidecar metadata: %+v", sidecar)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: write.SessionID, StoragePath: write.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if model.ContentDigest(loaded) != model.ContentDigest(conv) {
		t.Fatalf("round trip mismatch: %#v", loaded.Messages)
	}
	if loaded.Messages[0].Content != conv.Messages[0].Content || loaded.Messages[1].Content != conv.Messages[1].Content {
		t.Fatalf("store whitespace changed: %#v", loaded.Messages)
	}
	if !loaded.Messages[0].Timestamp.Equal(created) || !loaded.Messages[2].Timestamp.Equal(created.Add(2*time.Second)) {
		t.Fatalf("timestamps: %#v", loaded.Messages)
	}
	if loaded.ProjectPath != conv.ProjectPath || loaded.Title != conv.Title || !loaded.CreatedAt.Equal(created) {
		t.Fatalf("metadata mismatch: %+v", loaded)
	}
	if loaded.Migration == nil || loaded.Migration.OriginID != conv.ID {
		t.Fatalf("migration metadata: %+v", loaded.Migration)
	}
	transcriptPath := filepath.Join(p.projectsRoot, strings.TrimPrefix(util.EncodeClaudeProjectPath(conv.ProjectPath), "-"), "agent-transcripts", write.SessionID, write.SessionID+".jsonl")
	transcript, err := p.Load(context.Background(), provider.SessionRef{ID: write.SessionID, StoragePath: transcriptPath})
	if err != nil || model.ContentDigest(transcript) != model.ContentDigest(conv) {
		t.Fatalf("transcript round trip: %+v err=%v", transcript, err)
	}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var stores, transcripts int
	for _, sum := range sums {
		if sum.ID != write.SessionID {
			continue
		}
		if strings.HasSuffix(sum.StoragePath, "store.db") {
			stores++
		} else if strings.HasSuffix(sum.StoragePath, ".jsonl") {
			transcripts++
		}
	}
	if stores != 1 || transcripts != 1 {
		t.Fatalf("representations: store=%d transcript=%d summaries=%+v", stores, transcripts, sums)
	}
	if err := p.CleanupWrite(context.Background(), *write); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(write.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("store remains: %v", err)
	}
	if _, err := os.Stat(cursorSidecarPath(write.StoragePath)); !os.IsNotExist(err) {
		t.Fatalf("sidecar remains: %v", err)
	}
	if _, err := os.Stat(transcriptPath); !os.IsNotExist(err) {
		t.Fatalf("transcript remains: %v", err)
	}
}

func TestBuildCursorBlobsMatchesObservedAgentTurnStepWire(t *testing.T) {
	conv := &model.Conversation{Messages: []model.Message{
		{Role: model.RoleUser, Content: "question"},
		{Role: model.RoleAssistant, Content: "answer"},
	}}
	blobs, _, err := buildCursorBlobs(conv, "/project")
	if err != nil {
		t.Fatal(err)
	}
	const wantStepHex = "0a06616e73776572"
	for _, blob := range blobs {
		if hex.EncodeToString(blob.data) == wantStepHex {
			return
		}
	}
	t.Fatalf("observed Cursor step wire %s not found", wantStepHex)
}

func TestCleanupRefusesPathsOutsideCursorRoots(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	outside := filepath.Join(root, "outside", "store.db")
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := p.CleanupWrite(context.Background(), provider.WriteResult{SessionID: "session", StoragePath: outside, ProjectPath: "/project"})
	if err == nil || !strings.Contains(err.Error(), "refusing cleanup") {
		t.Fatalf("cleanup error = %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path changed: %v", err)
	}
}

func TestWriteJoinsNativeStoreAndCleanupFailures(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	original := writeCursorNativeStore
	t.Cleanup(func() { writeCursorNativeStore = original })
	writeCursorNativeStore = func(path, _, _ string, _ *model.Conversation) error {
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			return err
		}
		return errors.New("native store failed")
	}
	_, err := p.Write(context.Background(), &model.Conversation{ID: "origin", Provider: "codex", ProjectPath: "/project", Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}}}, provider.WriteOpts{})
	if err == nil || !strings.Contains(err.Error(), "native store failed") || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("joined write error = %v", err)
	}
}

func TestWriteRollsBackStoreAndTranscriptWhenSidecarFails(t *testing.T) {
	root := t.TempDir()
	p := &Provider{chatsRoot: filepath.Join(root, "chats"), projectsRoot: filepath.Join(root, "projects")}
	original := writeCursorNativeSidecar
	t.Cleanup(func() { writeCursorNativeSidecar = original })
	writeCursorNativeSidecar = func(string, string, *model.Conversation) error {
		return errors.New("sidecar failed")
	}
	_, err := p.Write(context.Background(), &model.Conversation{
		ID: "origin", Provider: "codex", ProjectPath: filepath.Join(root, "project"),
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}, provider.WriteOpts{})
	if err == nil || !strings.Contains(err.Error(), "sidecar failed") {
		t.Fatalf("write error = %v", err)
	}
	for _, pattern := range []string{
		filepath.Join(p.chatsRoot, "*", "*", "store.db"),
		filepath.Join(p.projectsRoot, "*", "agent-transcripts", "*", "*.jsonl"),
	} {
		matches, globErr := filepath.Glob(pattern)
		if globErr != nil {
			t.Fatal(globErr)
		}
		if len(matches) != 0 {
			t.Fatalf("rollback left artifacts: %v", matches)
		}
	}
}

func TestResumeCommandQuotesSession(t *testing.T) {
	p := &Provider{}
	if got := p.ResumeCommand(provider.WriteResult{SessionID: "id; echo bad"}); got != "cursor-agent --resume 'id; echo bad'" {
		t.Fatalf("resume = %q", got)
	}
}
