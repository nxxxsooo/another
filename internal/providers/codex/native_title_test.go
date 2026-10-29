package codex

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/provider"
)

const nativeTitleTestID = "0197f8a1-2b3c-7d4e-8f90-abcdef123456"

func nativeTitleFixture(t *testing.T) (*Provider, *sql.DB, provider.SessionRef) {
	t.Helper()
	t.Setenv("CODEX_SQLITE_HOME", "")
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, "rollout-2025-06-01T10-00-00-"+nativeTitleTestID+".jsonl")
	meta, err := json.Marshal(map[string]any{
		"timestamp": "2025-06-01T10:00:00Z", "type": "session_meta",
		"payload": map[string]string{"id": nativeTitleTestID, "cwd": t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := string(meta) + "\n" + `{"timestamp":"2025-06-01T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"native user prompt"}}` + "\n"
	for name, data := range map[string]string{
		path: content,
		filepath.Join(home, "session_index.jsonl"):      `{"id":"` + nativeTitleTestID + `","thread_name":"legacy catalog title"}` + "\n",
		filepath.Join(home, ".codex-global-state.json"): `{"electron-persisted-atom-state":{"thread-descriptions-v1":{"` + nativeTitleTestID + `":"legacy desktop title"}},"unrelated":1793238571123456789}`,
	} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, name TEXT, title TEXT, preview TEXT, updated_at INTEGER, rollout_path TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads VALUES (?, 'native name', 'legacy title', 'original preview', 123, ?)`, nativeTitleTestID, path); err != nil {
		t.Fatal(err)
	}
	p := &Provider{sessionsRoot: sessions}
	return p, db, provider.SessionRef{ID: nativeTitleTestID, StoragePath: path}
}

func TestNativeNameOverridesBothLegacyCatalogs(t *testing.T) {
	p, _, ref := nativeTitleFixture(t)
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(summaries) != 1 || summaries[0].Title != "native name" {
		t.Fatalf("discovery must use the client name: %v, %v", summaries, err)
	}
	conversation, err := p.Load(context.Background(), ref)
	if err != nil || conversation.Title != "native name" {
		t.Fatalf("load must use the client name: %v, %v", conversation, err)
	}
	if summaries[0].ID != ref.ID || conversation.ID != ref.ID {
		t.Fatal("title lookup changed the session identity")
	}
}

func TestClearedNativeNameDoesNotResurrectLegacyTitle(t *testing.T) {
	p, db, ref := nativeTitleFixture(t)
	if _, err := db.Exec(`UPDATE threads SET name = NULL`); err != nil {
		t.Fatal(err)
	}
	conversation, err := p.Load(context.Background(), ref)
	if err != nil || conversation.Title != "native user prompt" {
		t.Fatalf("cleared native name must discard stale catalogs: %v, %v", conversation, err)
	}
}

func TestNativeNameWALChangeInvalidatesDiscovery(t *testing.T) {
	p, db, _ := nativeTitleFixture(t)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; UPDATE threads SET name = 'before'`); err != nil {
		t.Fatal(err)
	}
	before, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(before) != 1 {
		t.Fatalf("initial discovery: %v, %v", before, err)
	}
	if _, err := db.Exec(`UPDATE threads SET updated_at = 456`); err != nil {
		t.Fatal(err)
	}
	unchanged, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, mtime, size int64) bool {
		return mtime == before[0].SourceMtime && size == before[0].SourceSize
	}})
	if err != nil || len(unchanged) != 0 {
		t.Fatalf("unrelated database writes invalidated a transcript: %v, %v", unchanged, err)
	}
	if _, err := db.Exec(`UPDATE threads SET name = 'after'`); err != nil {
		t.Fatal(err)
	}
	after, err := p.Discover(context.Background(), provider.DiscoverOpts{SkipSource: func(_ string, mtime, size int64) bool {
		return mtime == before[0].SourceMtime && size == before[0].SourceSize
	}})
	if err != nil || len(after) != 1 || after[0].Title != "after" {
		t.Fatalf("native-only rename was skipped: %v, %v", after, err)
	}
	if after[0].SourceMtime != before[0].SourceMtime {
		t.Fatal("native-only rename changed the rollout timestamp")
	}
}

func TestPreNameDatabaseUsesLegacyCatalog(t *testing.T) {
	p, db, ref := nativeTitleFixture(t)
	if _, err := db.Exec(`DROP TABLE threads; CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	conversation, err := p.Load(context.Background(), ref)
	if err != nil || conversation.Title != "legacy catalog title" {
		t.Fatalf("pre-name catalog: %v, %v", conversation, err)
	}
}

func TestBrokenNativeTitleStoreIsReported(t *testing.T) {
	t.Setenv("CODEX_SQLITE_HOME", "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state_5.sqlite"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Provider{sessionsRoot: filepath.Join(home, "sessions")}
	if _, err := p.loadGUITitles(context.Background()); err == nil {
		t.Fatal("unreadable native metadata silently became a legacy title")
	}
}

func setNativeTitleHelper(t *testing.T, p *Provider, mode string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.appServerCommand = func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCodexNativeTitleServiceHelper$")
		cmd.Env = append(os.Environ(), "ANOTHER_CODEX_TITLE_HELPER="+mode)
		return cmd
	}
}

func TestNativeRenameVerifiesClientNameWithoutEditingLegacyState(t *testing.T) {
	p, db, ref := nativeTitleFixture(t)
	setNativeTitleHelper(t, p, "success")
	before := make(map[string]string)
	for _, name := range []string{"session_index.jsonl", ".codex-global-state.json"} {
		path := filepath.Join(filepath.Dir(p.sessionsRoot), name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(data)
	}
	const title = "1029｜Explore｜Native title verification"
	if err := p.RenameSession(context.Background(), ref, "  "+title+"  "); err != nil {
		t.Fatal(err)
	}
	var name, legacyTitle, preview string
	var updated int
	if err := db.QueryRow(`SELECT name, title, preview, updated_at FROM threads WHERE id = ?`, ref.ID).Scan(&name, &legacyTitle, &preview, &updated); err != nil {
		t.Fatal(err)
	}
	if name != title || legacyTitle != "legacy title" || preview != "original preview" || updated != 123 {
		t.Fatalf("wrong fields changed: name=%q title=%q preview=%q updated=%d", name, legacyTitle, preview, updated)
	}
	for path, expected := range before {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("legacy client state was modified: %s (%v)", path, err)
		}
	}
	conversation, err := p.Load(context.Background(), ref)
	if err != nil || conversation.Title != title {
		t.Fatalf("another and the native name disagree: %v, %v", conversation, err)
	}
}

func TestNativeRenameRequiresAcknowledgementAndMatchingReadback(t *testing.T) {
	for _, mode := range []string{"reject", "mismatch", "wrong-id", "missing-name", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			p, _, ref := nativeTitleFixture(t)
			setNativeTitleHelper(t, p, mode)
			if err := p.RenameSession(context.Background(), ref, "1029｜Fix｜Native title"); err == nil {
				t.Fatal("unverified native rename reported success")
			}
			data, err := os.ReadFile(filepath.Join(filepath.Dir(p.sessionsRoot), "session_index.jsonl"))
			if err != nil || strings.Contains(string(data), "Native title") {
				t.Fatalf("failed native request wrote a legacy alias: %s (%v)", data, err)
			}
		})
	}
}

func TestNativeRenameCancellationStopsItsService(t *testing.T) {
	p, _, ref := nativeTitleFixture(t)
	setNativeTitleHelper(t, p, "stall")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := p.RenameSession(ctx, ref, "new name"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled service did not honor cancellation: %v", err)
	}
}

func TestNativeRenameMissingCLILeavesStateAlone(t *testing.T) {
	p, _, ref := nativeTitleFixture(t)
	p.appServerCommand = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-codex"))
	}
	if err := p.RenameSession(context.Background(), ref, "new name"); err == nil {
		t.Fatal("rename succeeded without a native service")
	}
	conversation, err := p.Load(context.Background(), ref)
	if err != nil || conversation.Title != "native name" {
		t.Fatalf("missing CLI changed a title: %v, %v", conversation, err)
	}
}

// Re-exec the test executable for a platform-native stdio service. This tests
// process cancellation and RPC framing on Windows as well as Unix, without a
// shell fixture or a real Codex installation.
func TestCodexNativeTitleServiceHelper(t *testing.T) {
	mode := os.Getenv("ANOTHER_CODEX_TITLE_HELPER")
	if mode == "" {
		return
	}
	if mode == "stall" {
		time.Sleep(time.Minute)
		os.Exit(1)
	}
	db, err := sql.Open("sqlite", filepath.Join(os.Getenv("CODEX_HOME"), "state_5.sqlite"))
	if err != nil {
		os.Exit(2)
	}
	reader := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	initialized := false
	for reader.Scan() {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(reader.Bytes(), &request) != nil {
			os.Exit(3)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
		case "initialized":
			initialized = true
			continue
		case "thread/name/set":
			if !initialized {
				os.Exit(4)
			}
			var params struct{ ThreadID, Name string }
			if json.Unmarshal(request.Params, &params) != nil || params.ThreadID != nativeTitleTestID || params.Name == "" {
				os.Exit(5)
			}
			if mode == "reject" {
				_ = writer.Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32602, "message": "native rename rejected"}})
				continue
			}
			if mode == "malformed" {
				fmt.Println("invalid JSON")
				continue
			}
			if mode != "mismatch" {
				if _, err := db.Exec(`UPDATE threads SET name = ? WHERE id = ?`, params.Name, params.ThreadID); err != nil {
					os.Exit(6)
				}
			}
			_ = writer.Encode(map[string]any{"method": "thread/name/updated", "params": map[string]string{"threadId": params.ThreadID, "threadName": params.Name}})
		case "thread/read":
			var params struct {
				ThreadID     string `json:"threadId"`
				IncludeTurns bool   `json:"includeTurns"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.IncludeTurns || params.ThreadID != nativeTitleTestID {
				os.Exit(7)
			}
			var name string
			if err := db.QueryRow(`SELECT name FROM threads WHERE id = ?`, params.ThreadID).Scan(&name); err != nil {
				os.Exit(8)
			}
			thread := map[string]any{"id": params.ThreadID, "name": name}
			if mode == "wrong-id" {
				thread["id"] = "another-thread"
			}
			if mode == "missing-name" {
				delete(thread, "name")
			}
			result = map[string]any{"thread": thread}
		default:
			os.Exit(9)
		}
		if writer.Encode(map[string]any{"id": request.ID, "result": result}) != nil {
			os.Exit(10)
		}
	}
	_ = db.Close()
	os.Exit(0)
}
