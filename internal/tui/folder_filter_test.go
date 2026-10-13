package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

func TestParseFolderQuery(t *testing.T) {
	frag, text := parseFolderQuery("path:packages/api timeout bug")
	if frag != "packages/api" || text != "timeout bug" {
		t.Fatalf("got frag=%q text=%q", frag, text)
	}
	frag, text = parseFolderQuery("timeout path:docs bug")
	if frag != "docs" || text != "timeout bug" {
		t.Fatalf("got frag=%q text=%q", frag, text)
	}
	frag, text = parseFolderQuery("path:docs/api")
	if frag != "docs/api" || text != "" {
		t.Fatalf("got frag=%q text=%q", frag, text)
	}
	frag, text = parseFolderQuery("plain query")
	if frag != "" || text != "plain query" {
		t.Fatalf("got frag=%q text=%q", frag, text)
	}
	frag, text = parseFolderQuery("path:")
	if frag != "" || text != "" {
		t.Fatalf("bare prefix should be no filter, got frag=%q text=%q", frag, text)
	}
}

// gitFixture builds a Git-style scope from real temporary directories: a
// repository, one linked worktree, and a sibling sharing a name prefix.
// Fixtures stay native (t.TempDir + filepath.Join) so the tests run wherever
// Go runs; production normalizes both sides before comparing.
func gitFixture(t *testing.T) (scope util.ProjectScope, repo, edge, sibling string) {
	t.Helper()
	base := t.TempDir()
	repo = filepath.Join(base, "repo")
	edge = filepath.Join(base, "worktrees", "edge")
	sibling = filepath.Join(base, "repo2")
	for _, d := range []string{repo, edge, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Normalize once, the way production scope roots arrive (DiscoverProjectScope
	// normalizes): a raw TempDir may spell a symlinked prefix differently from
	// its resolved form, and both sides must compare the same spelling.
	repo = util.NormalizeProjectPath(repo)
	edge = util.NormalizeProjectPath(edge)
	sibling = util.NormalizeProjectPath(sibling)
	scope = util.ProjectScope{CWD: repo, Root: repo, Worktrees: []string{repo, edge}, Git: true}
	return scope, repo, edge, sibling
}

func TestResolveFolderRelativeFansOut(t *testing.T) {
	scope, repo, edge, _ := gitFixture(t)
	got := resolveFolderRoots(scope, scopeModeExact, filepath.ToSlash("packages/api"), repo)
	want := []string{
		util.NormalizeProjectPath(filepath.Join(repo, "packages", "api")),
		util.NormalizeProjectPath(filepath.Join(edge, "packages", "api")),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFolderWorktreeFirst(t *testing.T) {
	scope, _, edge, _ := gitFixture(t)
	edgeBase := filepath.Base(edge)
	got := resolveFolderRoots(scope, scopeModeExact, edgeBase+filepath.ToSlash("/internal/tui"), scope.CWD)
	want := []string{util.NormalizeProjectPath(filepath.Join(edge, "internal", "tui"))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	got = resolveFolderRoots(scope, scopeModeExact, edgeBase, scope.CWD)
	want = []string{util.NormalizeProjectPath(edge)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFolderAbsoluteContainment(t *testing.T) {
	scope, repo, _, sibling := gitFixture(t)
	inside := filepath.Join(repo, "packages", "api")
	// Absolute fragments arrive in stored (normalized) spelling; the target
	// exists so normalization is stable on both sides.
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	got := resolveFolderRoots(scope, scopeModeExact, inside, repo)
	want := []string{util.NormalizeProjectPath(inside)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := resolveFolderRoots(scope, scopeModeExact, sibling, repo); len(got) != 0 {
		t.Fatalf("outside scope should be empty, got %#v", got)
	}
}

func TestResolveFolderSiblingPrefixAndTraversal(t *testing.T) {
	scope, repo, _, _ := gitFixture(t)
	// repo2 shares repo's name prefix but is not under it; climbing out of
	// every root must resolve to nothing rather than a neighboring tree.
	if got := resolveFolderRoots(scope, scopeModeExact, filepath.ToSlash("../repo2/x"), repo); len(got) != 0 {
		t.Fatalf("sibling-prefix escape should be empty, got %#v", got)
	}
	if got := resolveFolderRoots(scope, scopeModeExact, "..", repo); len(got) != 0 {
		t.Fatalf("traversal above the roots should be empty, got %#v", got)
	}
}

func TestResolveFolderNonGit(t *testing.T) {
	base := t.TempDir()
	rawNotes := filepath.Join(base, "notes")
	if err := os.MkdirAll(filepath.Join(rawNotes, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := util.NormalizeProjectPath(rawNotes)
	scope := util.ProjectScope{CWD: notes, Root: notes}
	got := resolveFolderRoots(scope, scopeModeTree, filepath.ToSlash("sub/deep"), notes)
	want := []string{util.NormalizeProjectPath(filepath.Join(notes, "sub", "deep"))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := resolveFolderRoots(scope, scopeModeTree, "..", notes); len(got) != 0 {
		t.Fatalf("traversal escape should be empty, got %#v", got)
	}
}

func TestResolveFolderAllModeReadsCWD(t *testing.T) {
	scope, repo, _, _ := gitFixture(t)
	got := resolveFolderRoots(scope, scopeModeAll, filepath.ToSlash("sub"), repo)
	want := []string{util.NormalizeProjectPath(filepath.Join(repo, "sub"))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestFolderFilterNarrowsListOpts(t *testing.T) {
	scope, repo, _, sibling := gitFixture(t)
	m := modelState{
		projectScope:  scope,
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(scope, scopeModeExact, "packages/api", repo),
	}
	opts := listOptsFor(m)
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
	m.folderDisplay = "nope"
	m.folderRoots = resolveFolderRoots(scope, scopeModeExact, sibling, repo)
	opts = listOptsFor(m)
	if len(opts.ProjectRoots) != 0 || opts.ProjectExact != folderNoMatch {
		t.Fatalf("no-match should be empty roots + sentinel, got %#v / %q", opts.ProjectRoots, opts.ProjectExact)
	}
}

func TestSearchOptsForRespectsFolderFilter(t *testing.T) {
	scope, repo, _, _ := gitFixture(t)
	m := modelState{
		projectScope:  scope,
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(scope, scopeModeExact, "packages/api", repo),
	}
	opts := searchOptsFor(m, "needle")
	if opts.Query != "needle" {
		t.Fatalf("Query = %q, want needle", opts.Query)
	}
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
}
