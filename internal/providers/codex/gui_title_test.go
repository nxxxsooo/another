package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxxxsooo/another/internal/provider"
)

// desktopStateFixture is the shape Codex Desktop actually keeps: the sidebar's
// title map nested in the Electron state, next to numbers that must survive a
// rewrite byte for byte.
const desktopStateFixture = `{
  "primary-runtime-update-jitter-ms": 1788762499810029483,
  "electron-persisted-atom-state": {
    "thread-descriptions-v1": {
      "01a031d4-74fa-7713-9f00-02cc43f4434b": "Review August Huatu PBC progress",
      "other": "Untouched"
    },
    "composer-auto-context-enabled": true
  },
  "active-workspace-roots": ["/Users/mingjian/Documents/sync"]
}`

func desktopFixture(t *testing.T) (*Provider, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, ".codex-global-state.json")
	if err := os.WriteFile(state, []byte(desktopStateFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CODEX_DESKTOP_STATE_DIR", t.TempDir()) // no singleton lock: Desktop is not running
	return New(), state
}

func TestDesktopTitleIsWrittenWhereTheSidebarReads(t *testing.T) {
	p, state := desktopFixture(t)
	if err := p.writeDesktopTitle("01a031d4-74fa-7713-9f00-02cc43f4434b", "0831｜文档｜月度PBC总结润色"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	titles := got["electron-persisted-atom-state"].(map[string]any)["thread-descriptions-v1"].(map[string]any)
	if titles["01a031d4-74fa-7713-9f00-02cc43f4434b"] != "0831｜文档｜月度PBC总结润色" {
		t.Fatalf("sidebar title = %v", titles["01a031d4-74fa-7713-9f00-02cc43f4434b"])
	}
	if titles["other"] != "Untouched" {
		t.Fatalf("a neighbouring title changed: %v", titles["other"])
	}
	// Desktop's own state travels through this rewrite. A timestamp that comes
	// back as 1.7887624998100295e+18 is a corrupted file, not a rename.
	if !strings.Contains(string(raw), "1788762499810029483") {
		t.Fatalf("a large number was rewritten as a float:\n%s", raw)
	}
	if _, ok := got["active-workspace-roots"]; !ok {
		t.Fatal("unrelated Desktop state was dropped")
	}
}

// Desktop rewrites this whole file from memory while it runs, so writing under
// it would either lose the rename or lose what Desktop has not flushed. The
// rename still happened in the CLI's own store, so this is a caveat.
func TestRunningDesktopIsReportedNotFought(t *testing.T) {
	p, state := desktopFixture(t)
	lockDir := t.TempDir()
	if err := os.Symlink("MingjianMax-1", filepath.Join(lockDir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_DESKTOP_STATE_DIR", lockDir)

	before, _ := os.ReadFile(state)
	err := p.writeDesktopTitle("01a031d4-74fa-7713-9f00-02cc43f4434b", "0831｜文档｜月度PBC总结润色")
	if err == nil {
		t.Fatal("a running Desktop was written underneath")
	}
	if !errorsIsPartial(err) {
		t.Fatalf("a running Desktop reads as a failed rename: %v", err)
	}
	if after, _ := os.ReadFile(state); string(after) != string(before) {
		t.Fatal("Desktop state was modified while Desktop was running")
	}
}

// Desktop writes the singleton lock when it starts, so a lock that is present
// but unreadable rules out the one thing another needs to know: that Desktop is
// closed. Treating an unparseable link as "not running" would rewrite Desktop's
// entire persisted state underneath a live app.
func TestUnreadableSingletonLockLeavesDesktopStateAlone(t *testing.T) {
	p, state := desktopFixture(t)
	lockDir := t.TempDir()
	if err := os.Symlink("no-pid-in-here", filepath.Join(lockDir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_DESKTOP_STATE_DIR", lockDir)

	before, _ := os.ReadFile(state)
	err := p.writeDesktopTitle("01a031d4-74fa-7713-9f00-02cc43f4434b", "0831｜文档｜月度PBC总结润色")
	if err == nil {
		t.Fatal("a lock another could not read was treated as Desktop being closed")
	}
	if !errorsIsPartial(err) {
		t.Fatalf("an unknown Desktop reads as a failed rename: %v", err)
	}
	if after, _ := os.ReadFile(state); string(after) != string(before) {
		t.Fatal("Desktop state was modified while its lock was held")
	}
}

func TestMissingDesktopStateIsNotAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CODEX_DESKTOP_STATE_DIR", t.TempDir())
	if err := New().writeDesktopTitle("id", "title"); err != nil {
		t.Fatalf("a machine without Codex Desktop reports an error: %v", err)
	}
}

func errorsIsPartial(err error) bool {
	for err != nil {
		if err == provider.ErrPartial {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
