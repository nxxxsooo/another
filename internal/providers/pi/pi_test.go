package pi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
)

// writeSession lays out a pi session exactly as pi does: one directory per
// working directory, named by replacing separators with dashes.
func writeSession(t *testing.T, root, cwd, name string, lines []string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")+"--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// pi stores reasoning and tool traffic in the same message stream. Only user
// and assistant text may cross a migration boundary.
func TestLoadKeepsOnlyUserAndAssistantText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	lines := []string{
		`{"type":"session","version":3,"id":"01a065a5-edfc-73df-89b3-f14b9b01f243","timestamp":"2026-09-03T05:02:48.316Z","cwd":"/home/user/proj"}`,
		`{"type":"model_change","id":"0908ee4a","parentId":null,"timestamp":"2026-09-03T05:02:48.400Z","provider":"openai-fast","modelId":"gpt-5.6-sol"}`,
		`{"type":"session_info","id":"445b3f12","parentId":"0908ee4a","timestamp":"2026-09-03T05:02:48.500Z","name":"real title"}`,
		`{"type":"message","id":"0e639209","parentId":"445b3f12","timestamp":"2026-09-03T05:02:49.000Z","message":{"role":"user","content":[{"type":"text","text":"real question"}],"timestamp":1788399673595}}`,
		`{"type":"message","id":"32f3eff6","parentId":"0e639209","timestamp":"2026-09-03T05:02:50.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"real answer"}]}}`,
		`{"type":"message","id":"fdc5263e","parentId":"32f3eff6","timestamp":"2026-09-03T05:02:51.000Z","message":{"role":"toolResult","toolCallId":"toolu_1","toolName":"read","content":[{"type":"text","text":"file body"}]}}`,
	}
	path := writeSession(t, root, "/home/user/proj", "2026-09-03T05-02-48-316Z_01a065a5-edfc-73df-89b3-f14b9b01f243.jsonl", lines)

	p := pi.New()
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "01a065a5-edfc-73df-89b3-f14b9b01f243", StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (thinking blocks and toolResult dropped)", len(conv.Messages))
	}
	if conv.Messages[0].Content != "real question" || conv.Messages[1].Content != "real answer" {
		t.Fatalf("unexpected messages: %+v", conv.Messages)
	}
	if strings.Contains(conv.Messages[1].Content, "private reasoning") {
		t.Fatal("assistant reasoning must not survive a migration")
	}
	if conv.ProjectPath != "/home/user/proj" {
		t.Fatalf("project = %q, want /home/user/proj", conv.ProjectPath)
	}
	if conv.Title != "real title" {
		t.Fatalf("title = %q, want the session_info name", conv.Title)
	}
}

func TestDiscoverReadsHeaderAndSessionInfo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	lines := []string{
		`{"type":"session","version":3,"id":"sess-1","timestamp":"2026-09-03T05:02:48.316Z","cwd":"/home/user/proj"}`,
		`{"type":"session_info","id":"a1","parentId":null,"name":"named by pi"}`,
		`{"type":"message","id":"b1","parentId":"a1","timestamp":"2026-09-03T05:02:49.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"message","id":"b2","parentId":"b1","timestamp":"2026-09-03T05:02:50.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	}
	writeSession(t, root, "/home/user/proj", "2026-09-03T05-02-48-316Z_sess-1.jsonl", lines)

	p := pi.New()
	got, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("summaries = %d, want 1", len(got))
	}
	s := got[0]
	if s.ID != "sess-1" {
		t.Fatalf("id = %q, want the session header id", s.ID)
	}
	if s.ProjectPath != "/home/user/proj" {
		t.Fatalf("project = %q", s.ProjectPath)
	}
	if s.Title != "named by pi" {
		t.Fatalf("title = %q, want session_info name", s.Title)
	}
	if s.MessageCount != 2 {
		t.Fatalf("message count = %d, want 2", s.MessageCount)
	}
	if s.Provider != "pi" {
		t.Fatalf("provider = %q", s.Provider)
	}
}

// A session with no session_info must still get a usable title from the first
// user message, the same fallback the other providers use.
func TestDiscoverFallsBackToFirstUserMessageTitle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	lines := []string{
		`{"type":"session","version":3,"id":"sess-2","timestamp":"2026-09-03T05:02:48.316Z","cwd":"/home/user/proj"}`,
		`{"type":"message","id":"b1","timestamp":"2026-09-03T05:02:49.000Z","message":{"role":"user","content":[{"type":"text","text":"migrate my session"}]}}`,
	}
	writeSession(t, root, "/home/user/proj", "2026-09-03T05-02-48-316Z_sess-2.jsonl", lines)

	got, err := pi.New().Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "migrate my session" {
		t.Fatalf("title fallback failed: %+v", got)
	}
}

func TestWriteMatchesPiResumeContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	p := pi.New()
	project := filepath.Join(root, "web_root", "a.b c")
	start := time.Date(2026, 8, 17, 1, 2, 3, 456000000, time.UTC)
	conv := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: project, Title: "carried title",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "  hello\r\n", Timestamp: start},
			{Role: model.RoleAssistant, Content: "\tworld  ", Timestamp: start.Add(time.Second)},
		},
	}
	res, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProjectPath != project {
		t.Fatalf("project = %q, want %q", res.ProjectPath, project)
	}
	data, err := os.ReadFile(res.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	var header map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header["type"] != "session" {
		t.Fatalf("first row type = %v, want session", header["type"])
	}
	if v, ok := header["version"].(float64); !ok || int(v) != 3 {
		t.Fatalf("version = %v, want 3", header["version"])
	}
	if header["id"] != res.SessionID {
		t.Fatalf("header id %v != write result %v", header["id"], res.SessionID)
	}
	if header["cwd"] != project {
		t.Fatalf("cwd = %v, want %q", header["cwd"], project)
	}
	if _, present := header["parentId"]; present {
		t.Fatal("session header must not carry parentId")
	}

	// Every row after the header forms a single parent chain; pi replays it in order.
	var (
		prevID     string
		sawInfo    bool
		userText   []string
		asstText   []string
		firstAfter = true
	)
	for _, row := range rows[1:] {
		var e map[string]any
		if err := json.Unmarshal([]byte(row), &e); err != nil {
			t.Fatalf("row is not json: %s", row)
		}
		id, _ := e["id"].(string)
		if id == "" {
			t.Fatalf("row without id: %s", row)
		}
		parent, _ := e["parentId"].(string)
		if firstAfter {
			if e["parentId"] != nil {
				t.Fatalf("first event must have null parentId, got %v", e["parentId"])
			}
			firstAfter = false
		} else if parent != prevID {
			t.Fatalf("broken parent chain: %s has parent %q, want %q", id, parent, prevID)
		}
		prevID = id
		if e["type"] == "session_info" {
			sawInfo = true
			if e["name"] != "carried title" {
				t.Fatalf("session name = %v, want the source title", e["name"])
			}
		}
		if e["type"] == "message" {
			m := e["message"].(map[string]any)
			blocks := m["content"].([]any)
			text := blocks[0].(map[string]any)["text"].(string)
			switch m["role"] {
			case "user":
				userText = append(userText, text)
			case "assistant":
				asstText = append(asstText, text)
			}
		}
	}
	if !sawInfo {
		t.Fatal("write must emit session_info so the session is pickable in pi -r")
	}
	if len(asstText) != 1 || asstText[0] != "\tworld  " {
		t.Fatalf("assistant text not preserved verbatim: %q", asstText)
	}
	if len(userText) == 0 || userText[0] != "  hello\r\n" {
		t.Fatalf("user text not preserved verbatim: %q", userText)
	}
}

// A migrated conversation ending on an assistant turn gets a synthetic
// trailing user turn naming the source agent (Anthropic-backed models
// reject a resume whose last turn is assistant, treating it as a prefill).
// Load must drop that synthetic turn again so the round trip is clean.
func TestWriteBridgesTrailingAssistantAndLoadDropsIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	p := pi.New()
	conv := &model.Conversation{
		ID: "source", Provider: "claude-code", ProjectPath: "/home/user/proj", Title: "carried title",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "question"},
			{Role: model.RoleAssistant, Content: "answer"},
		},
	}
	res, err := p.Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "上面是从 claude-code 迁移过来的历史上下文") {
		t.Fatalf("expected bridge turn naming the source agent, got: %s", data)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: res.SessionID, StoragePath: res.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (bridge turn must be dropped on load)", len(loaded.Messages))
	}
	if loaded.Messages[len(loaded.Messages)-1].Content != "answer" {
		t.Fatalf("last message = %q, want the real trailing assistant answer, not the bridge turn", loaded.Messages[len(loaded.Messages)-1].Content)
	}
}

// Pi's footer aggregates usage from every assistant event. A migrated turn has
// no provider billable usage, but it must still supply a complete zero-valued
// usage record so opening the session cannot crash the TUI.
func TestWriteGivesMigratedAssistantZeroUsage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	conv := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/home/user/proj",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "q"},
			{Role: model.RoleAssistant, Content: "a"},
		},
	}
	res, err := pi.New().Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(row), &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != "message" {
			continue
		}
		message, _ := event["message"].(map[string]any)
		if message["role"] != "assistant" {
			continue
		}
		for field, want := range map[string]string{
			"api": "pi-messages", "provider": "another", "model": "migration", "stopReason": "stop",
		} {
			if message[field] != want {
				t.Fatalf("assistant %s = %v, want %q", field, message[field], want)
			}
		}
		usage, ok := message["usage"].(map[string]any)
		if !ok {
			t.Fatalf("migrated assistant message has no usage: %#v", message)
		}
		for _, field := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
			if usage[field] != float64(0) {
				t.Fatalf("usage.%s = %v, want 0", field, usage[field])
			}
		}
		cost, ok := usage["cost"].(map[string]any)
		if !ok || cost["total"] != float64(0) {
			t.Fatalf("usage.cost = %#v, want zero-valued cost", usage["cost"])
		}
		return
	}
	t.Fatal("written session has no assistant message")
}

// pi rejects a resumed session whose last turn is an assistant message on some
// providers; codex2pi solved this with a trailing bridge user turn.
func TestWriteAppendsBridgeTurnWhenLastMessageIsAssistant(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	conv := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/home/user/proj",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "q"},
			{Role: model.RoleAssistant, Content: "a"},
		},
	}
	res, err := pi.New().Write(context.Background(), conv, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(res.StoragePath)
	rows := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var lastRole string
	for _, row := range rows {
		var e map[string]any
		_ = json.Unmarshal([]byte(row), &e)
		if e["type"] == "message" {
			lastRole, _ = e["message"].(map[string]any)["role"].(string)
		}
	}
	if lastRole != "user" {
		t.Fatalf("last message role = %q, want a trailing user bridge turn", lastRole)
	}
}

// pi names session files "<stamp>_<uuid>.jsonl" with real milliseconds; a
// dash-separated Go layout silently renders "000" instead.
func TestWriteFilenameCarriesRealMilliseconds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	conv := &model.Conversation{
		ID: "source", ProjectPath: "/home/user/proj",
		Messages: []model.Message{{Role: model.RoleUser, Content: "q"}},
	}
	seenNonZero := false
	for i := 0; i < 25; i++ {
		res, err := pi.New().Write(context.Background(), conv, provider.WriteOpts{})
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(res.StoragePath)
		stamp, _, ok := strings.Cut(base, "_")
		if !ok || !strings.HasSuffix(stamp, "Z") {
			t.Fatalf("filename %q does not match <stamp>_<uuid>.jsonl", base)
		}
		if ms := stamp[len(stamp)-4 : len(stamp)-1]; ms != "000" {
			seenNonZero = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !seenNonZero {
		t.Fatal("stamp milliseconds are always 000; the layout is emitting a literal")
	}
}

func TestWriteRejectsEmptySession(t *testing.T) {
	t.Setenv("PI_AGENT_DIR", t.TempDir())
	_, err := pi.New().Write(context.Background(), &model.Conversation{ID: "x"}, provider.WriteOpts{})
	if err != provider.ErrEmptySession {
		t.Fatalf("err = %v, want ErrEmptySession", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	conv := &model.Conversation{
		ID: "source", ProjectPath: "/home/user/proj",
		Messages: []model.Message{{Role: model.RoleUser, Content: "q"}},
	}
	res, err := pi.New().Write(context.Background(), conv, provider.WriteOpts{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", res.StoragePath)
	}
}

func TestResumeCommandUsesExactSessionFile(t *testing.T) {
	res := provider.WriteResult{SessionID: "s1", StoragePath: "/tmp/a b/s1.jsonl", ProjectPath: "/home/user/my proj"}
	got := pi.New().ResumeCommand(res)
	if !strings.Contains(got, "pi --session") {
		t.Fatalf("resume command = %q, want an exact --session invocation", got)
	}
	if !strings.Contains(got, "'/tmp/a b/s1.jsonl'") {
		t.Fatalf("resume command must quote the path: %q", got)
	}
}

// A failed verification must delete only the artifact Write produced.
func TestCleanupWriteRefusesOutsideSessionsRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	outside := filepath.Join(t.TempDir(), "victim.jsonl")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := pi.New().CleanupWrite(context.Background(), provider.WriteResult{StoragePath: outside})
	if err == nil {
		t.Fatal("cleanup outside the sessions root must fail")
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatal("cleanup deleted a file outside the sessions root")
	}
}

func TestRoundTripPreservesConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	p := pi.New()
	start := time.Date(2026, 8, 17, 1, 2, 3, 456000000, time.UTC)
	src := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/home/user/proj", Title: "t",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "question one", Timestamp: start},
			{Role: model.RoleAssistant, Content: "answer one", Timestamp: start.Add(time.Second)},
			{Role: model.RoleUser, Content: "question two", Timestamp: start.Add(2 * time.Second)},
		},
	}
	res, err := p.Write(context.Background(), src, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	back, err := p.Load(context.Background(), provider.SessionRef{ID: res.SessionID, StoragePath: res.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Messages) != len(src.Messages) {
		t.Fatalf("round trip changed message count: %d -> %d", len(src.Messages), len(back.Messages))
	}
	for i := range src.Messages {
		if back.Messages[i].Content != src.Messages[i].Content {
			t.Fatalf("message %d changed: %q -> %q", i, src.Messages[i].Content, back.Messages[i].Content)
		}
		if back.Messages[i].Role != src.Messages[i].Role {
			t.Fatalf("message %d role changed: %v -> %v", i, src.Messages[i].Role, back.Messages[i].Role)
		}
	}
}
