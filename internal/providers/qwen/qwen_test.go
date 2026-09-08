package qwen_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/qwen"
)

func TestWriteLoadDiscoverRenameAndCleanup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	p := qwen.New()
	source := &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/workspace/demo", Title: "Native title",
		Messages: []model.Message{
			{Role: model.RoleUser, Content: "first prompt", Timestamp: time.Date(2026, 9, 4, 10, 0, 0, 123000000, time.UTC)},
			{Role: model.RoleAssistant, Content: "first answer", Timestamp: time.Date(2026, 9, 4, 10, 0, 1, 456000000, time.UTC)},
		},
	}
	written, err := p.Write(context.Background(), source, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "projects", "-workspace-demo", "chats"); filepath.Dir(written.StoragePath) != want {
		t.Fatalf("storage dir = %q, want %q", filepath.Dir(written.StoragePath), want)
	}
	loaded, err := p.Load(context.Background(), provider.SessionRef{ID: written.SessionID, ProjectPath: written.ProjectPath})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != source.Title || model.ContentDigest(loaded) != model.ContentDigest(source) {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.Migration == nil || loaded.Migration.OriginID != "source" || loaded.Migration.OriginSource != "codex" {
		t.Fatalf("migration marker = %+v", loaded.Migration)
	}
	summaries, err := p.Discover(context.Background(), provider.DiscoverOpts{ProjectFilter: "workspace"})
	if err != nil || len(summaries) != 1 || summaries[0].ID != written.SessionID {
		t.Fatalf("Discover = %+v, %v", summaries, err)
	}
	ref := provider.SessionRef{ID: written.SessionID, StoragePath: written.StoragePath, ProjectPath: written.ProjectPath}
	if err := p.RenameSession(context.Background(), ref, "Renamed"); err != nil {
		t.Fatal(err)
	}
	renamed, err := p.Load(context.Background(), ref)
	if err != nil || renamed.Title != "Renamed" || model.ContentDigest(renamed) != model.ContentDigest(source) {
		t.Fatalf("renamed = %+v, %v", renamed, err)
	}
	if err := p.CleanupWrite(context.Background(), *written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(written.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("session survived cleanup: %v", err)
	}
}

func TestLoadUsesActiveParentChainAndDisplayText(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	path := filepath.Join(root, "projects", "-p", "chats", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"uuid":"u1","parentUuid":null,"sessionId":"s1","timestamp":"2026-09-04T10:00:00Z","type":"user","cwd":"/p","version":"0.23.0","message":{"role":"user","parts":[{"text":"model-facing prompt"}]},"systemPayload":{"displayText":"visible prompt"}}`,
		`{"uuid":"a1","parentUuid":"u1","sessionId":"s1","timestamp":"2026-09-04T10:00:01Z","type":"assistant","cwd":"/p","version":"0.23.0","message":{"role":"model","parts":[{"thought":"private"},{"text":"dead answer"}]}}`,
		`{"uuid":"u2","parentUuid":"u1","sessionId":"s1","timestamp":"2026-09-04T10:00:02Z","type":"user","cwd":"/p","version":"0.23.0","message":{"role":"user","parts":[{"text":"replacement"}]}}`,
		`{"uuid":"a2","parentUuid":"u2","sessionId":"s1","timestamp":"2026-09-04T10:00:03Z","type":"assistant","cwd":"/p","version":"0.23.0","message":{"role":"model","parts":[{"thought":"hidden"},{"text":"live answer"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv, err := qwen.New().Load(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Messages) != 3 || conv.Messages[0].Content != "visible prompt" || conv.Messages[1].Content != "replacement" || conv.Messages[2].Content != "live answer" {
		t.Fatalf("active projection = %+v", conv.Messages)
	}
}

func TestProviderBasics(t *testing.T) {
	t.Setenv("QWEN_HOME", t.TempDir())
	p := qwen.New()
	if p.ID() != "qwen" || p.DisplayName() != "Qwen Code" || !p.SupportsResume() {
		t.Fatalf("provider basics: %q %q %v", p.ID(), p.DisplayName(), p.SupportsResume())
	}
	got := p.ResumeCommand(provider.WriteResult{SessionID: "abc", ProjectPath: "/some/project"})
	if got != "cd '/some/project' && qwen --resume 'abc'" {
		t.Fatalf("resume command = %q", got)
	}
}

func TestCleanupRejectsOutsideStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	outside := filepath.Join(t.TempDir(), "victim.jsonl")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := qwen.New().CleanupWrite(context.Background(), provider.WriteResult{SessionID: "victim", StoragePath: outside})
	if err == nil {
		t.Fatal("cleanup accepted a path outside QWEN_HOME")
	}
}

// Archiving is a move between Qwen Code's two chat directories, so the whole
// session has to travel: the transcript the picker reads and the sidecars that
// only mean anything next to it. Unarchiving must land the same files back.
func TestArchiveCarriesTheWholeSessionBothWays(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	p := qwen.New()
	chats, id := filepath.Join(root, "projects", "-p", "chats"), sessionID
	writeSession(t, chats, id)
	for _, suffix := range []string{".worktree.json", ".pr.json", ".ledger.jsonl"} {
		writeFile(t, filepath.Join(chats, id+suffix), "{}")
	}
	ref := provider.SessionRef{ID: id, StoragePath: filepath.Join(chats, id+".jsonl"), ProjectPath: "/p"}

	if err := p.ArchiveSession(context.Background(), ref, true); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".jsonl", ".worktree.json", ".pr.json", ".ledger.jsonl"} {
		if _, err := os.Stat(filepath.Join(chats, "archive", id+suffix)); err != nil {
			t.Fatalf("%s did not reach the archive: %v", suffix, err)
		}
		if _, err := os.Stat(filepath.Join(chats, id+suffix)); !os.IsNotExist(err) {
			t.Fatalf("%s stayed behind: %v", suffix, err)
		}
	}
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("archived session still listed: %+v %v", sums, err)
	}

	// The undo path passes the summary as it was indexed, so the stale active
	// path must not stop another from finding the session where it now lives.
	if err := p.ArchiveSession(context.Background(), ref, false); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".jsonl", ".worktree.json", ".pr.json", ".ledger.jsonl"} {
		if _, err := os.Stat(filepath.Join(chats, id+suffix)); err != nil {
			t.Fatalf("%s did not come back: %v", suffix, err)
		}
	}
	if sums, err = p.Discover(context.Background(), provider.DiscoverOpts{}); err != nil || len(sums) != 1 {
		t.Fatalf("unarchived session not listed: %+v %v", sums, err)
	}
	// Archiving what is already archived is the state the caller asked for.
	if err := p.ArchiveSession(context.Background(), ref, false); err != nil {
		t.Fatalf("no-op archive returned %v", err)
	}
}

// A session a live Qwen Code is appending to must not be moved or removed from
// under it: the process holds the transcript open and would write the turn in
// flight into a file that is no longer the session.
func TestLifecycleRefusesASessionARunningQwenOwns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	p := qwen.New()
	chats, id := filepath.Join(root, "projects", "-p", "chats"), sessionID
	writeSession(t, chats, id)
	ref := provider.SessionRef{ID: id, StoragePath: filepath.Join(chats, id+".jsonl")}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(chats, id+".runtime.json")
	// pid 1 is alive on every machine this runs on, whether or not the test
	// user is allowed to signal it.
	writeFile(t, runtimePath, fmt.Sprintf(
		`{"schema_version":1,"pid":1,"session_id":%q,"work_dir":"/p","hostname":%q,"started_at":1.0,"qwen_version":"0.23.1"}`, id, host))
	if err := p.ArchiveSession(context.Background(), ref, true); err == nil {
		t.Fatal("archived a running session")
	}
	if err := p.DeleteSession(context.Background(), ref); err == nil {
		t.Fatal("deleted a running session")
	}

	// A sidecar written on another machine says nothing about a pid here, so
	// Qwen Code reads it as live and so does another.
	writeFile(t, runtimePath, fmt.Sprintf(
		`{"schema_version":1,"pid":%d,"session_id":%q,"work_dir":"/p","hostname":"somewhere-else","started_at":1.0,"qwen_version":"0.23.1"}`, deadPID(t), id))
	if err := p.DeleteSession(context.Background(), ref); err == nil {
		t.Fatal("deleted a session another machine has open")
	}

	// A sidecar left by a process that is gone is not a running session.
	writeFile(t, runtimePath, fmt.Sprintf(
		`{"schema_version":1,"pid":%d,"session_id":%q,"work_dir":"/p","hostname":%q,"started_at":1.0,"qwen_version":"0.23.1"}`, deadPID(t), id, host))
	if err := p.ArchiveSession(context.Background(), ref, true); err != nil {
		t.Fatalf("archive refused a session nobody is running: %v", err)
	}
}

// Deleting has to clear every place Qwen Code keys to the session, or the
// session comes back as a pin in the sidebar, a stale runtime entry in
// `qwen sessions ps`, or backups of files it edited that nothing can reach.
func TestDeleteRemovesEverySessionArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	project := filepath.Join(root, "projects", "-p")
	chats, id := filepath.Join(project, "chats"), sessionID
	writeSession(t, chats, id)
	for _, suffix := range []string{".worktree.json", ".pr.json", ".ledger.jsonl"} {
		writeFile(t, filepath.Join(chats, id+suffix), "{}")
	}
	writeFile(t, filepath.Join(chats, id+".runtime.json"), fmt.Sprintf(
		`{"schema_version":1,"pid":%d,"session_id":%q,"work_dir":"/p","hostname":"gone","started_at":1.0,"qwen_version":"0.23.1"}`, deadPID(t), "other-session"))
	writeFile(t, filepath.Join(root, "file-history", id, "backup.txt"), "old contents")
	writeFile(t, filepath.Join(project, "session-organization.v1.json"), fmt.Sprintf(
		`{"schemaVersion":1,"groups":[{"id":"g1","name":"work"}],"sessions":{%q:{"groupId":"g1"},"kept":{"groupId":null}},"future":"keep me"}`, id))

	err := qwen.New().DeleteSession(context.Background(), provider.SessionRef{ID: id, StoragePath: filepath.Join(chats, id+".jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".jsonl", ".worktree.json", ".pr.json", ".ledger.jsonl", ".runtime.json"} {
		if _, err := os.Stat(filepath.Join(chats, id+suffix)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the delete: %v", suffix, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "file-history", id)); !os.IsNotExist(err) {
		t.Fatalf("file history survived the delete: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, "session-organization.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var store struct {
		Groups   []map[string]any          `json:"groups"`
		Sessions map[string]map[string]any `json:"sessions"`
		Future   string                    `json:"future"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if _, gone := store.Sessions[id]; gone {
		t.Fatal("the deleted session kept its group membership")
	}
	if _, kept := store.Sessions["kept"]; !kept || len(store.Groups) != 1 || store.Future != "keep me" {
		t.Fatalf("the rest of the store did not survive: %+v", store)
	}
}

// An archived session is still a session: deleting one has to reach into the
// archive rather than report that there is nothing there.
func TestDeleteReachesTheArchive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	chats, id := filepath.Join(root, "projects", "-p", "chats"), sessionID
	writeSession(t, filepath.Join(chats, "archive"), id)
	err := qwen.New().DeleteSession(context.Background(),
		provider.SessionRef{ID: id, StoragePath: filepath.Join(chats, id+".jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(chats, "archive", id+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("archived session survived the delete: %v", err)
	}
}

// Every lifecycle path builds file names out of the id it is handed, so an id
// that is not one of Qwen Code's own session names never becomes a path.
func TestLifecycleRejectsAnIDThatIsNotASession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	p := qwen.New()
	victim := filepath.Join(root, "projects", "-p", "chats", "victim.jsonl")
	writeFile(t, victim, "keep")
	for _, id := range []string{"victim", "../../../etc/passwd", strings.Repeat("a", 40)} {
		ref := provider.SessionRef{ID: id, StoragePath: victim}
		if err := p.DeleteSession(context.Background(), ref); err == nil {
			t.Fatalf("delete accepted %q as a session id", id)
		}
		if err := p.ArchiveSession(context.Background(), ref, true); err == nil {
			t.Fatalf("archive accepted %q as a session id", id)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a refused id still removed a file: %v", err)
	}
}

// A session that is gone reports as much, so a caller does not read a silent
// success as proof that something was removed.
func TestLifecycleReportsAMissingSession(t *testing.T) {
	t.Setenv("QWEN_HOME", t.TempDir())
	ref := provider.SessionRef{ID: sessionID}
	if err := qwen.New().DeleteSession(context.Background(), ref); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("delete error = %v, want ErrNotFound", err)
	}
	if err := qwen.New().ArchiveSession(context.Background(), ref, true); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("archive error = %v, want ErrNotFound", err)
	}
}

const sessionID = "3f1c9a4e-2b7d-4c6a-9f80-15d3e7a4b8c2"

func writeSession(t *testing.T, chats, id string) {
	t.Helper()
	writeFile(t, filepath.Join(chats, id+".jsonl"), fmt.Sprintf(
		`{"uuid":"u1","parentUuid":null,"sessionId":%q,"timestamp":"2026-09-04T10:00:00Z","type":"user","cwd":"/p","message":{"role":"user","parts":[{"text":"a prompt"}]}}`, id)+"\n")
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadPID returns the pid of a process that has already exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// Qwen Code stamps cwd on every row, so an agent that moved into a temporary
// directory used to drag the whole session there. Attribution follows the
// first directory the session recorded.
func TestSessionKeepsItsStartingDirectoryAcrossLaterCwdChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("QWEN_HOME", root)
	path := filepath.Join(root, "projects", "-p", "chats", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"uuid":"u1","parentUuid":null,"sessionId":"s1","timestamp":"2026-09-04T10:00:00Z","type":"user","cwd":"/home/user/proj","version":"0.23.0","message":{"role":"user","parts":[{"text":"start here"}]}}`,
		`{"uuid":"a1","parentUuid":"u1","sessionId":"s1","timestamp":"2026-09-04T10:00:01Z","type":"assistant","cwd":"/home/user/proj/src","version":"0.23.0","message":{"role":"model","parts":[{"text":"answer"}]}}`,
		`{"uuid":"u2","parentUuid":"a1","sessionId":"s1","timestamp":"2026-09-04T10:00:02Z","type":"user","cwd":"/private/tmp","version":"0.23.0","message":{"role":"user","parts":[{"text":"one last check"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := qwen.New()
	sums, err := p.Discover(context.Background(), provider.DiscoverOpts{})
	if err != nil || len(sums) != 1 {
		t.Fatalf("summaries=%+v err=%v", sums, err)
	}
	if sums[0].ProjectPath != "/home/user/proj" {
		t.Fatalf("ProjectPath = %q, want /home/user/proj", sums[0].ProjectPath)
	}
	conv, err := p.Load(context.Background(), provider.SessionRef{ID: "s1", StoragePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ProjectPath != "/home/user/proj" {
		t.Fatalf("Load ProjectPath = %q, want /home/user/proj", conv.ProjectPath)
	}
}
