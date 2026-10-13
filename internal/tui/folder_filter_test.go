package tui

import (
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

func gitScope(t *testing.T) util.ProjectScope {
	t.Helper()
	base := util.NormalizeProjectPath(t.TempDir())
	root := filepath.Join(base, "repo")
	return util.ProjectScope{
		CWD:       root,
		Root:      root,
		Worktrees: []string{root, filepath.Join(base, "worktrees", "edge")},
		Git:       true,
	}
}

func TestResolveFolderRelativeFansOut(t *testing.T) {
	scope := gitScope(t)
	got := resolveFolderRoots(scope, scopeModeExact, "packages/api", scope.CWD)
	want := []string{
		filepath.Join(scope.Root, "packages", "api"),
		filepath.Join(scope.Worktrees[1], "packages", "api"),
	}
	// Normalize both sides for symlinked test dirs (e.g. /private/tmp on macOS).
	for i := range want {
		want[i] = util.NormalizeProjectPath(want[i])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFolderWorktreeFirst(t *testing.T) {
	scope := gitScope(t)
	got := resolveFolderRoots(scope, scopeModeExact, "edge/internal/tui", scope.CWD)
	want := []string{filepath.Join(scope.Worktrees[1], "internal", "tui")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	got = resolveFolderRoots(scope, scopeModeExact, "edge", scope.CWD)
	want = []string{scope.Worktrees[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFolderAbsoluteContainment(t *testing.T) {
	scope := gitScope(t)
	child := filepath.Join(scope.Root, "packages", "api")
	got := resolveFolderRoots(scope, scopeModeExact, child, scope.CWD)
	want := []string{child}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for _, outside := range []string{
		filepath.Join(filepath.Dir(scope.Root), "other", "dir"),
		scope.Root + "-sibling",
	} {
		if got := resolveFolderRoots(scope, scopeModeExact, outside, scope.CWD); len(got) != 0 {
			t.Fatalf("outside scope %q should be empty, got %#v", outside, got)
		}
	}
}

func TestResolveFolderNonGitAndTraversal(t *testing.T) {
	root := util.NormalizeProjectPath(t.TempDir())
	scope := util.ProjectScope{CWD: root, Root: root}
	got := resolveFolderRoots(scope, scopeModeTree, "sub/deep", scope.CWD)
	want := []string{filepath.Join(root, "sub", "deep")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := resolveFolderRoots(scope, scopeModeTree, "../escape", scope.CWD); len(got) != 0 {
		t.Fatalf("traversal escape should be empty, got %#v", got)
	}
}

func TestFolderFilterNarrowsListOpts(t *testing.T) {
	scope := gitScope(t)
	m := modelState{
		projectScope:  scope,
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(scope, scopeModeExact, "packages/api", scope.CWD),
	}
	opts := listOptsFor(m)
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
	m.folderDisplay = "nope"
	m.folderRoots = resolveFolderRoots(scope, scopeModeExact, scope.Root+"-sibling", scope.CWD)
	opts = listOptsFor(m)
	if len(opts.ProjectRoots) != 0 || opts.ProjectExact != folderNoMatch {
		t.Fatalf("no-match should be empty roots + sentinel, got %#v / %q", opts.ProjectRoots, opts.ProjectExact)
	}
}

func TestSearchOptsForRespectsFolderFilter(t *testing.T) {
	scope := gitScope(t)
	m := modelState{
		projectScope:  scope,
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(scope, scopeModeExact, "packages/api", scope.CWD),
	}
	opts := searchOptsFor(m, "needle")
	if opts.Query != "needle" {
		t.Fatalf("Query = %q, want needle", opts.Query)
	}
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
}
