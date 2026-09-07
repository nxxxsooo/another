package pi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
)

// relocateFixture is a session carrying exactly the records a migration would
// throw away: a model change, reasoning, and a tool result.
func relocateFixture(t *testing.T, root, cwd string) (path string, lines []string) {
	t.Helper()
	lines = []string{
		`{"type":"session","version":3,"id":"01a065a5-edfc-73df-89b3-f14b9b01f243","timestamp":"2026-09-03T05:02:48.316Z","cwd":"` + cwd + `"}`,
		`{"type":"model_change","id":"0908ee4a","parentId":null,"timestamp":"2026-09-03T05:02:48.400Z","provider":"openai-fast","modelId":"gpt-5.6-sol"}`,
		`{"type":"session_info","id":"445b3f12","parentId":"0908ee4a","timestamp":"2026-09-03T05:02:48.500Z","name":"real title"}`,
		`{"type":"message","id":"0e639209","parentId":"445b3f12","timestamp":"2026-09-03T05:02:49.000Z","message":{"role":"user","content":[{"type":"text","text":"real question"}],"timestamp":1788399673595}}`,
		`{"type":"message","id":"32f3eff6","parentId":"0e639209","timestamp":"2026-09-03T05:02:50.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"real answer"}]}}`,
		`{"type":"message","id":"fdc5263e","parentId":"32f3eff6","timestamp":"2026-09-03T05:02:51.000Z","message":{"role":"toolResult","toolCallId":"toolu_1","toolName":"read","content":[{"type":"text","text":"file body"}]}}`,
	}
	path = writeSession(t, root, cwd,
		"2026-09-03T05-02-48-316Z_01a065a5-edfc-73df-89b3-f14b9b01f243.jsonl", lines)
	return path, lines
}

// A relocate is not a migration. Everything a migration would drop — reasoning,
// tool results, model changes — has to arrive intact, because the point of the
// action is that the session itself continues somewhere else.
func TestRelocateForkCopiesEveryRecordVerbatim(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	target := t.TempDir()
	source, lines := relocateFixture(t, root, "/home/user/proj")

	p := pi.New()
	res, err := p.RelocateSession(context.Background(), provider.SessionRef{
		ID: "01a065a5-edfc-73df-89b3-f14b9b01f243", StoragePath: source, ProjectPath: "/home/user/proj",
	}, provider.RelocateOpts{Directory: target, Mode: provider.RelocateFork})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved {
		t.Fatal("a fork reported itself as a move")
	}
	if res.SessionID == "01a065a5-edfc-73df-89b3-f14b9b01f243" {
		t.Fatal("the fork kept the source session id; pi would find two files claiming it")
	}
	wantDir := filepath.Join(root, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(target, "/"), "/", "-")+"--")
	if filepath.Dir(res.StoragePath) != wantDir {
		t.Fatalf("fork landed in %s, want %s", filepath.Dir(res.StoragePath), wantDir)
	}
	if !strings.HasSuffix(res.StoragePath, "_"+res.SessionID+".jsonl") {
		t.Fatalf("file name %s does not carry the session id", res.StoragePath)
	}

	got := readLines(t, res.StoragePath)
	if len(got) != len(lines) {
		t.Fatalf("fork has %d lines, source had %d", len(got), len(lines))
	}
	// Only the header may differ, and only in id and cwd.
	for i := 1; i < len(lines); i++ {
		if got[i] != lines[i] {
			t.Fatalf("line %d was rewritten:\n got %s\nwant %s", i, got[i], lines[i])
		}
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(got[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header["cwd"] != target {
		t.Fatalf("header cwd = %v, want %s", header["cwd"], target)
	}
	if header["id"] != res.SessionID {
		t.Fatalf("header id = %v, want %s", header["id"], res.SessionID)
	}
	if header["version"] == nil || header["timestamp"] == nil {
		t.Fatalf("header lost its own fields: %v", header)
	}

	// The source is sacred: a fork must leave it exactly as it was.
	if source == res.StoragePath {
		t.Fatal("fork overwrote the source")
	}
	if srcLines := readLines(t, source); strings.Join(srcLines, "\n") != strings.Join(lines, "\n") {
		t.Fatal("fork modified the source session")
	}
}

func TestRelocateMoveKeepsTheIdentityAndRemovesTheSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	target := t.TempDir()
	source, lines := relocateFixture(t, root, "/home/user/proj")

	p := pi.New()
	res, err := p.RelocateSession(context.Background(), provider.SessionRef{
		ID: "01a065a5-edfc-73df-89b3-f14b9b01f243", StoragePath: source, ProjectPath: "/home/user/proj",
	}, provider.RelocateOpts{Directory: target, Mode: provider.RelocateMove})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatal("a move reported itself as a fork")
	}
	// A move is the same session in a new place, so it keeps its id.
	if res.SessionID != "01a065a5-edfc-73df-89b3-f14b9b01f243" {
		t.Fatalf("move changed the session id to %s", res.SessionID)
	}
	if filepath.Base(res.StoragePath) != filepath.Base(source) {
		t.Fatalf("move renamed the file to %s", filepath.Base(res.StoragePath))
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("move left the session in the old directory")
	}
	got := readLines(t, res.StoragePath)
	for i := 1; i < len(lines); i++ {
		if got[i] != lines[i] {
			t.Fatalf("line %d was rewritten:\n got %s\nwant %s", i, got[i], lines[i])
		}
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{StoragePath: res.StoragePath})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProjectPath != target {
		t.Fatalf("moved session still reports %s", conv.ProjectPath)
	}
}

// Nothing irreversible may happen before the copy is verified, so a move that
// cannot be written must leave the original in place.
func TestRelocateMoveKeepsTheSourceWhenTheTargetIsTaken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	target := t.TempDir()
	source, _ := relocateFixture(t, root, "/home/user/proj")
	// A session with the same name is already there.
	writeSession(t, root, target,
		"2026-09-03T05-02-48-316Z_01a065a5-edfc-73df-89b3-f14b9b01f243.jsonl",
		[]string{`{"type":"session","version":3,"id":"01a065a5-edfc-73df-89b3-f14b9b01f243","cwd":"` + target + `"}`})

	p := pi.New()
	_, err := p.RelocateSession(context.Background(), provider.SessionRef{
		ID: "01a065a5-edfc-73df-89b3-f14b9b01f243", StoragePath: source, ProjectPath: "/home/user/proj",
	}, provider.RelocateOpts{Directory: target, Mode: provider.RelocateMove})
	if err == nil {
		t.Fatal("a move onto an existing session was reported as success")
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatal("a refused move deleted the original session")
	}
}

func TestRelocateDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	target := t.TempDir()
	source, _ := relocateFixture(t, root, "/home/user/proj")

	p := pi.New()
	res, err := p.RelocateSession(context.Background(), provider.SessionRef{
		ID: "01a065a5-edfc-73df-89b3-f14b9b01f243", StoragePath: source, ProjectPath: "/home/user/proj",
	}, provider.RelocateOpts{Directory: target, Mode: provider.RelocateFork, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(res.StoragePath); !os.IsNotExist(statErr) {
		t.Fatal("a dry run wrote a session file")
	}
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatal("a dry run touched the source")
	}
}

func TestRelocateRefusesFilesOutsideTheSessionsRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	outside := filepath.Join(t.TempDir(), "not-a-session.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session","id":"x","cwd":"/tmp"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := pi.New().RelocateSession(context.Background(), provider.SessionRef{
		ID: "x", StoragePath: outside, ProjectPath: "/tmp",
	}, provider.RelocateOpts{Directory: t.TempDir(), Mode: provider.RelocateFork})
	if err == nil {
		t.Fatal("relocating a file outside the sessions root must fail")
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatal("a refused relocate touched the file")
	}
}

func TestRelocateReportsBothModesSupported(t *testing.T) {
	p := pi.New()
	if !p.SupportsRelocate(provider.RelocateFork) || !p.SupportsRelocate(provider.RelocateMove) {
		t.Fatal("pi owns both relocate modes")
	}
	if p.SupportsRelocate(provider.RelocateMode("teleport")) {
		t.Fatal("an unknown mode was reported as supported")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}
