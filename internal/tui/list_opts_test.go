package tui

import (
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/util"
)

func TestListOptsDefaultsToCurrentGitProject(t *testing.T) {
	m := modelState{cwd: "/home/user/proj", scopeMode: scopeModeExact, projectScope: util.ProjectScope{
		CWD: "/home/user/proj", Root: "/home/user/proj", Git: true,
		Worktrees: []string{"/home/user/proj", "/tmp/proj-feature"},
	}}
	opts := listOptsFor(m)
	if !reflect.DeepEqual(opts.ProjectRoots, m.projectScope.Worktrees) {
		t.Fatalf("ProjectRoots = %#v, want %#v", opts.ProjectRoots, m.projectScope.Worktrees)
	}
	if opts.IncludeSubagents {
		t.Fatal("subagent sessions must stay out of the browser")
	}
	if opts.Limit != maxShowAllPage {
		t.Fatalf("Limit = %d, want the single-fetch cap %d", opts.Limit, maxShowAllPage)
	}
}

// A plain directory has two scopes: its own tree, and everything. The tree is
// this directory and everything under it — agents record the directory they ran
// in, not its parent, so an exact match would hide whole trees — and the
// projects inside it are part of it: a workspace is read as one line of work.
func TestListOptsNonGitScopesAreTheTreeAndEverything(t *testing.T) {
	m := modelState{cwd: "/tmp/notes", scopeMode: scopeModeTree,
		projectScope: util.ProjectScope{CWD: "/tmp/notes", Root: "/tmp/notes"}}
	opts := listOptsFor(m)
	if !reflect.DeepEqual(opts.ProjectRoots, []string{"/tmp/notes"}) {
		t.Fatalf("ProjectRoots = %#v, want [/tmp/notes]", opts.ProjectRoots)
	}
	if opts.ProjectExact != "" {
		t.Fatalf("ProjectExact = %q, want no exact filter", opts.ProjectExact)
	}

	m.scopeMode = scopeModeAll
	opts = listOptsFor(m)
	if opts.ProjectExact != "" || len(opts.ProjectRoots) != 0 {
		t.Fatalf("all-project opts still scoped: %+v", opts)
	}
}

// The narrow scope is the directory's own shape: a repository and its
// worktrees, or a plain directory and everything under it.
func TestApplyProjectScopeGitAndNonGit(t *testing.T) {
	gitScope := util.ProjectScope{
		CWD: "/repo/sub", Root: "/repo", Git: true,
		Worktrees: []string{"/repo", "/repo-worktree"},
	}
	var gitOpts index.ListOpts
	applyProjectScope(&gitOpts, gitScope, scopeModeExact)
	if !reflect.DeepEqual(gitOpts.ProjectRoots, []string{"/repo", "/repo-worktree"}) {
		t.Fatalf("git ProjectRoots = %#v", gitOpts.ProjectRoots)
	}
	if gitOpts.ProjectExact != "" {
		t.Fatalf("git ProjectExact should be empty, got %q", gitOpts.ProjectExact)
	}

	nonGitScope := util.ProjectScope{
		CWD: "/Users/user/Documents/sync/Work/huatu", Root: "/Users/user/Documents/sync/Work/huatu",
	}
	var nonGitOpts index.ListOpts
	applyProjectScope(&nonGitOpts, nonGitScope, scopeModeTree)
	if nonGitOpts.ProjectExact != "" {
		t.Fatalf("nonGit ProjectExact should be empty, got %q", nonGitOpts.ProjectExact)
	}
	if !reflect.DeepEqual(nonGitOpts.ProjectRoots, []string{nonGitScope.CWD}) {
		t.Fatalf("nonGit ProjectRoots = %#v", nonGitOpts.ProjectRoots)
	}

	var allOpts index.ListOpts
	applyProjectScope(&allOpts, nonGitScope, scopeModeAll)
	if allOpts.ProjectExact != "" || len(allOpts.ProjectRoots) != 0 {
		t.Fatalf("all-project opts still scoped: %+v", allOpts)
	}
}

func TestSearchOptsFollowActiveProjectScope(t *testing.T) {
	m := modelState{scopeMode: scopeModeExact, projectScope: util.ProjectScope{
		CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo", "/tmp/feature"},
	}}
	opts := searchOptsFor(m, "needle")
	if opts.Query != "needle" || !reflect.DeepEqual(opts.ProjectRoots, m.projectScope.Worktrees) {
		t.Fatalf("search opts = %+v", opts)
	}
	m.scopeMode = scopeModeAll
	if got := searchOptsFor(m, "needle"); len(got.ProjectRoots) != 0 || got.ProjectExact != "" {
		t.Fatalf("global search is still scoped: %+v", got)
	}
}

func TestListOptsFollowsSelectedSource(t *testing.T) {
	m := modelState{
		sources:   []sourceChip{{id: "", name: "all"}, {id: "codex", name: "Codex"}},
		sourceIdx: 1,
	}
	if got := listOptsFor(m).Provider; got != "codex" {
		t.Fatalf("Provider = %q, want the selected source chip", got)
	}
	m.sourceIdx = 0
	if got := listOptsFor(m).Provider; got != "" {
		t.Fatalf("Provider = %q, want empty for the all chip", got)
	}
}

// The scope the browser opens on is the directory it was started in. Nothing is
// guessed from counts: an empty directory is a true answer, and a directory is
// either a project or a container, never both, so there are two states to walk.
func TestOpeningScopeIsTheDirectoryItStartedIn(t *testing.T) {
	project := util.ProjectScope{CWD: "/Users/mingjian/Documents/sync/Work/huatu"}
	container := util.ProjectScope{CWD: "/Users/mingjian/Documents"}
	git := util.ProjectScope{CWD: "/Users/mingjian/Documents/sync/GitHub/another", Git: true}
	for _, tc := range []struct {
		name  string
		scope util.ProjectScope
		want  scopeMode
	}{
		{"a plain directory opens on its tree", project, scopeModeTree},
		{"so does a container", container, scopeModeTree},
		{"a repository opens on the repository", git, scopeModeExact},
		{"a directory that is not known opens on everything", util.ProjectScope{}, scopeModeAll},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := openingScope(tc.scope); got != tc.want {
				t.Fatalf("openingScope = %v, want %v", got, tc.want)
			}
		})
	}
}

// Two states, and the narrow one is the directory's own shape: a repository is
// read as its worktrees, a plain directory as the tree below it. Anything else
// would offer the same list twice.
func TestScopeModesAreTwoAndTheNarrowOneFitsTheDirectory(t *testing.T) {
	git := scopeModes(util.ProjectScope{CWD: "/repo", Git: true})
	if len(git) != 2 || git[0] != scopeModeExact || git[1] != scopeModeAll {
		t.Fatalf("a repository walks %v, want the repository then everything", git)
	}
	plain := scopeModes(util.ProjectScope{CWD: "/plain"})
	if len(plain) != 2 || plain[0] != scopeModeTree || plain[1] != scopeModeAll {
		t.Fatalf("a plain directory walks %v, want its tree then everything", plain)
	}
}
