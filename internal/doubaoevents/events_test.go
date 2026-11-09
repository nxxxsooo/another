package doubaoevents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func desktopLine(conversation, turn string) string {
	return `[123:456:1109/123456.000000:INFO:FRONTEND(0)] [neotix] [office-api-logger] "[webview][unknown][flow-web][logifier-report]: {"version":1,"type":"event","level":"info","message":"on_message_session_end","eventName":"on_message_session_end","meta":{"agent_error":"0","cancel_source":"","chat_scene":"normal_conversation","conversation_type":"3","general_agent":"1","is_async_task":"0","receive_err":"","receive_success":"1","session_err":"","session_success":"1","send_success":"1","conversation_id":"` + conversation + `","receive_message_id":"` + turn + `","resumed":"1"}}", source:  (0)` + "\n"
}

// desktopFirstLine is the shape a work conversation's very first completed turn
// writes: type 5 and the start-page scene, before any resume observer repeats it.
func desktopFirstLine(conversation, turn string) string {
	return strings.NewReplacer(
		`"chat_scene":"normal_conversation"`, `"chat_scene":"generic_start_page_new_conversation"`,
		`"conversation_type":"3"`, `"conversation_type":"5"`,
		`"resumed":"1"`, `"resumed":"0"`,
	).Replace(desktopLine(conversation, turn))
}

func cliLine(conversation, turn string) string {
	return `[123:456:1109/123456.000000:INFO:aha/doubao_cli/runtime_console_delegate.cc:83] [JS worker=fixture] [info] [cli-chat] {"level":"info","eventName":"im_task_finished","meta":{"scope":"agent","agent_type":"main","root_session_id":"` + turn + `","conversation_id":"` + conversation + `","root_conversation_id":"` + conversation + `","session_id":"` + turn + `","has_error":false,"aborted":false,"duration":100}}` + "\n"
}

func TestParseNativeRecords(t *testing.T) {
	for _, tc := range []struct {
		source Source
		line   string
		ok     bool
	}{
		{Desktop, desktopLine("123", "456"), true},
		{Desktop, desktopFirstLine("123", "456"), true},
		{CLI, cliLine("123", "turn-1"), true},
		{Desktop, strings.ReplaceAll(desktopLine("123", "456"), `"chat_scene":"normal_conversation"`, `"chat_scene":"video_generation"`), false},
		{Desktop, strings.ReplaceAll(desktopLine("123", "456"), `"session_success":"1"`, `"session_success":"0"`), false},
		{Desktop, strings.ReplaceAll(desktopLine("123", "456"), `"agent_error":"0"`, `"agent_error":"1"`), false},
		{Desktop, strings.ReplaceAll(desktopLine("123", "456"), `"cancel_source":""`, `"cancel_source":"user"`), false},
		{Desktop, strings.ReplaceAll(desktopLine("123", "456"), `"conversation_type":"3"`, `"conversation_type":"1"`), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), `"agent_type":"main"`, `"agent_type":"sub"`), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), `"aborted":false`, `"aborted":true`), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), `"has_error":false`, `"has_error":true`), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), `"has_error":false,`, ``), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), `"root_conversation_id":"123"`, `"root_conversation_id":"999"`), false},
		{CLI, strings.ReplaceAll(cliLine("123", "turn-1"), "im_task_finished", "unknown"), false},
		{CLI, `{"eventName":"im_task_finished"}`, false},
		{Desktop, cliLine("123", "turn-1"), false},
		{CLI, cliLine("bad/id", "turn-1"), false},
		{CLI, "malformed", false},
	} {
		e, ok := parse([]byte(strings.TrimSuffix(tc.line, "\n")), tc.source)
		if ok != tc.ok {
			t.Fatalf("source %s: got %v, want %v", tc.source, ok, tc.ok)
		}
		if ok && (e.ConversationID != "123" || e.Source != tc.source || e.TurnID == "") {
			t.Fatalf("unexpected event: %+v", e)
		}
	}
}

func TestTailPartialBaselineLimitAndTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("past\nunfinished"); err != nil {
		t.Fatal(err)
	}
	tail := tail{f: f}
	if err := tail.baseline(); err != nil {
		t.Fatal(err)
	}
	var lines []string
	read := func() {
		t.Helper()
		if err := tail.read(func(b []byte) error { lines = append(lines, string(b)); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	write := func(s string) {
		t.Helper()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		read()
	}
	write("-ignored\nfirst\npart")
	if len(lines) != 1 || lines[0] != "first" {
		t.Fatalf("baseline/partial: %v", lines)
	}
	write("ial\n")
	if lines[1] != "partial" {
		t.Fatalf("partial: %v", lines)
	}
	write(strings.Repeat("x", maxLine+1) + "\nafter-limit\n")
	if len(lines) != 3 || lines[2] != "after-limit" || cap(tail.partial) > maxLine {
		t.Fatalf("line cap failed: %d", len(lines))
	}
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	write("new\n")
	if lines[3] != "new" {
		t.Fatalf("truncate: %v", lines)
	}
	// Truncate and regrow beyond the old offset before reading.
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	write("replacement-longer\n")
	if lines[4] != "replacement-longer" {
		t.Fatalf("truncate/regrow: %v", lines)
	}
}

func TestLedgerPersistenceAndCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "events.json")
	l, err := loadLedger(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxDedup {
		l.Keys = append(l.Keys, eventKey(Event{ConversationID: "c", TurnID: strings.Repeat("x", i)}))
	}
	key := eventKey(Event{ConversationID: "new", TurnID: "turn", Source: CLI})
	if err := l.save(p, key); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadLedger(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Keys) != maxDedup || !reloaded.contains(key) {
		t.Fatal("ledger cap/persistence")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("turn")) {
		t.Fatal("ledger must contain hashes only")
	}
	if !json.Valid(b) {
		t.Fatal("invalid JSON")
	}
}
