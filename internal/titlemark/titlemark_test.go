package titlemark_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxxxsooo/another/internal/titlemark"
	"github.com/nxxxsooo/another/internal/titler"
)

func TestMarksRoundTripPerSession(t *testing.T) {
	store := titlemark.At(t.TempDir())
	if got := store.Load("pi", "s1"); got != nil {
		t.Fatalf("missing mark = %+v, want nil", got)
	}
	at := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	if err := store.Save("pi", "s1", titler.Mark{Title: "0901｜修复｜X", UserTurns: 3, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("pi", "s1", titler.Mark{Title: "0902｜修复｜X", UserTurns: 4, At: at}); err != nil {
		t.Fatal(err)
	}
	got := store.Load("pi", "s1")
	if got == nil || got.Title != "0902｜修复｜X" || got.UserTurns != 4 || !got.At.Equal(at) {
		t.Fatalf("mark = %+v", got)
	}
	if store.Load("agy", "s1") != nil || store.Load("pi", "s2") != nil {
		t.Fatal("a mark leaked to another provider or session")
	}
}

// Session ids come from agents. One carrying separators or a drive-like
// prefix must still land inside the store as a single file.
func TestMarksSurviveIDsThatAreNotFileNames(t *testing.T) {
	dir := t.TempDir()
	store := titlemark.At(dir)
	for _, id := range []string{`..\..\escape`, "../../escape", "C:conv", "a/b:c*?"} {
		if err := store.Save("claude-code", id, titler.Mark{Title: "t", UserTurns: 1}); err != nil {
			t.Fatalf("Save(%q): %v", id, err)
		}
		if got := store.Load("claude-code", id); got == nil {
			t.Fatalf("Load(%q) lost the mark", id)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "claude-code"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("store holds %d entries (%v), want 4 files in one directory", len(entries), err)
	}
}

func TestAnUnreadableMarkCountsAsNone(t *testing.T) {
	dir := t.TempDir()
	store := titlemark.At(dir)
	if err := store.Save("pi", "s1", titler.Mark{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "pi"))
	if err := os.WriteFile(filepath.Join(dir, "pi", entries[0].Name()), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := store.Load("pi", "s1"); got != nil {
		t.Fatalf("corrupt mark = %+v, want nil", got)
	}
}
