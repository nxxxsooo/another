package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

// suggestTree builds home/{Documents/{alpha,beta},Downloads,.hidden} and a
// worktree and a project outside it, all real directories.
func suggestTree(t *testing.T) (home string, src suggestSources) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	for _, d := range []string{"Documents/alpha", "Documents/beta", "Downloads", ".hidden", "wt/feature", "proj/api"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "Doc-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return home, suggestSources{
		home:      home,
		cwd:       home,
		worktrees: []string{filepath.Join(home, "wt/feature")},
		recent:    []string{filepath.Join(home, "proj/api"), filepath.Join(home, "gone")},
	}
}

func suggestionPaths(rows []pathSuggestion) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.path
	}
	return out
}

// An empty box offers where people go most: worktrees first, then the
// directories the index has seen, and only ones that still exist.
func TestSuggestEmptyOffersWorktreesThenProjects(t *testing.T) {
	home, src := suggestTree(t)
	got := suggestPaths("", src, 6)
	want := []string{filepath.Join(home, "wt/feature"), filepath.Join(home, "proj/api")}
	if !reflect.DeepEqual(suggestionPaths(got), want) {
		t.Fatalf("got %v, want %v", suggestionPaths(got), want)
	}
	if got[0].kind != suggestWorktree || got[1].kind != suggestProject {
		t.Fatalf("kinds = %v, %v", got[0].kind, got[1].kind)
	}
}

// Typing a path completes its last segment from the directory on disk. Files
// and hidden directories are not offered.
func TestSuggestCompletesDirectoriesUnderTilde(t *testing.T) {
	home, src := suggestTree(t)
	got := suggestionPaths(suggestPaths("~/Do", src, 6))
	want := []string{filepath.Join(home, "Documents"), filepath.Join(home, "Downloads")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = suggestionPaths(suggestPaths("~/Documents/", src, 6))
	want = []string{filepath.Join(home, "Documents/alpha"), filepath.Join(home, "Documents/beta")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
}

func TestSuggestHiddenOnlyWhenAskedFor(t *testing.T) {
	home, src := suggestTree(t)
	if got := suggestionPaths(suggestPaths(home+"/", src, 20)); contains(got, filepath.Join(home, ".hidden")) {
		t.Fatalf("hidden directory offered unasked: %v", got)
	}
	if got := suggestionPaths(suggestPaths(home+"/.h", src, 20)); !contains(got, filepath.Join(home, ".hidden")) {
		t.Fatalf("hidden directory not offered for a dot: %v", got)
	}
}

// Worktrees and projects match on any part of the path, so "feat" finds the
// worktree without typing where it lives.
func TestSuggestMatchesWorktreesAndProjectsBySubstring(t *testing.T) {
	home, src := suggestTree(t)
	got := suggestionPaths(suggestPaths("feat", src, 6))
	if !reflect.DeepEqual(got, []string{filepath.Join(home, "wt/feature")}) {
		t.Fatalf("got %v", got)
	}
	got = suggestionPaths(suggestPaths("API", src, 6))
	if !reflect.DeepEqual(got, []string{filepath.Join(home, "proj/api")}) {
		t.Fatalf("case-insensitive project match = %v", got)
	}
}

// One directory is one row, whichever source found it, and the session's own
// directory is never offered as a destination.
func TestSuggestDeduplicatesAndExcludesTheCurrentDirectory(t *testing.T) {
	home, src := suggestTree(t)
	src.recent = append(src.recent, filepath.Join(home, "wt/feature"))
	got := suggestionPaths(suggestPaths("", src, 6))
	if len(got) != 2 {
		t.Fatalf("duplicate rows: %v", got)
	}
	src.exclude = util.NormalizeProjectPath(filepath.Join(home, "wt/feature"))
	got = suggestionPaths(suggestPaths("", src, 6))
	if contains(got, filepath.Join(home, "wt/feature")) {
		t.Fatalf("current directory offered: %v", got)
	}
}

// A repository with many worktrees must not push every other destination off
// the list.
func TestSuggestLeavesRoomForProjectsBesideManyWorktrees(t *testing.T) {
	home, src := suggestTree(t)
	src.worktrees = nil
	for _, name := range []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7"} {
		p := filepath.Join(home, "wt", name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		src.worktrees = append(src.worktrees, p)
	}
	got := suggestPaths("", src, 6)
	projects := 0
	for _, r := range got {
		if r.kind == suggestProject {
			projects++
		}
	}
	// Leftover worktrees backfill rows the other sources leave empty.
	if len(got) != 6 || projects != 1 {
		t.Fatalf("rows = %+v, want 6 with the project among them", got)
	}
}

func TestSuggestRespectsTheLimit(t *testing.T) {
	_, src := suggestTree(t)
	if got := suggestPaths("~/", src, 2); len(got) != 2 {
		t.Fatalf("limit 2 gave %d rows", len(got))
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
