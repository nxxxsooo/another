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

func gitScope() util.ProjectScope {
	return util.ProjectScope{
		CWD:       "/repo",
		Root:      "/repo",
		Worktrees: []string{"/repo", "/worktrees/edge"},
		Git:       true,
	}
}

func TestResolveFolderRelativeFansOut(t *testing.T) {
	got := resolveFolderRoots(gitScope(), scopeModeExact, "packages/api", "/repo")
	want := []string{
		filepath.Join("/repo", "packages/api"),
		filepath.Join("/worktrees/edge", "packages/api"),
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
	got := resolveFolderRoots(gitScope(), scopeModeExact, "edge/internal/tui", "/repo")
	want := []string{util.NormalizeProjectPath("/worktrees/edge/internal/tui")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	got = resolveFolderRoots(gitScope(), scopeModeExact, "edge", "/repo")
	want = []string{util.NormalizeProjectPath("/worktrees/edge")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFolderAbsoluteContainment(t *testing.T) {
	got := resolveFolderRoots(gitScope(), scopeModeExact, "/repo/packages/api", "/repo")
	want := []string{util.NormalizeProjectPath("/repo/packages/api")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := resolveFolderRoots(gitScope(), scopeModeExact, "/other/dir", "/repo"); len(got) != 0 {
		t.Fatalf("outside scope should be empty, got %#v", got)
	}
}

func TestResolveFolderNonGitAndTraversal(t *testing.T) {
	scope := util.ProjectScope{CWD: "/notes", Root: "/notes"}
	got := resolveFolderRoots(scope, scopeModeTree, "sub/deep", "/notes")
	want := []string{util.NormalizeProjectPath("/notes/sub/deep")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := resolveFolderRoots(scope, scopeModeTree, "../escape", "/notes"); len(got) != 0 {
		t.Fatalf("traversal escape should be empty, got %#v", got)
	}
}

func TestFolderFilterNarrowsListOpts(t *testing.T) {
	m := modelState{
		projectScope:  gitScope(),
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(gitScope(), scopeModeExact, "packages/api", "/repo"),
	}
	opts := listOptsFor(m)
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
	m.folderDisplay = "nope"
	m.folderRoots = resolveFolderRoots(gitScope(), scopeModeExact, "/other/dir", "/repo")
	opts = listOptsFor(m)
	if len(opts.ProjectRoots) != 0 || opts.ProjectExact != folderNoMatch {
		t.Fatalf("no-match should be empty roots + sentinel, got %#v / %q", opts.ProjectRoots, opts.ProjectExact)
	}
}

func TestSearchOptsForRespectsFolderFilter(t *testing.T) {
	m := modelState{
		projectScope:  gitScope(),
		scopeMode:     scopeModeExact,
		folderDisplay: "packages/api",
		folderRoots:   resolveFolderRoots(gitScope(), scopeModeExact, "packages/api", "/repo"),
	}
	opts := searchOptsFor(m, "needle")
	if opts.Query != "needle" {
		t.Fatalf("Query = %q, want needle", opts.Query)
	}
	if len(opts.ProjectRoots) != 2 {
		t.Fatalf("ProjectRoots = %#v, want 2 narrowed roots", opts.ProjectRoots)
	}
}
