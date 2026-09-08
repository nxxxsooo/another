package tui

import (
	"reflect"
	"testing"

	"github.com/nxxxsooo/another/internal/util"
)

func TestListOptsDefaultsToCurrentGitProject(t *testing.T) {
	m := modelState{cwd: "/home/user/proj", projectOnly: true, projectScope: util.ProjectScope{
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

// A folder that is not a Git repository still owns the sessions beneath it.
// Agents record the directory they ran in, so a parent like Work/huatu never
// matches exactly and an exact filter showed an empty list.
func TestListOptsNonGitScopeCoversSubdirectoriesAndToggleShowsAll(t *testing.T) {
	m := modelState{cwd: "/tmp/notes", projectOnly: true,
		projectScope: util.ProjectScope{CWD: "/tmp/notes", Root: "/tmp/notes"}}
	opts := listOptsFor(m)
	if !reflect.DeepEqual(opts.ProjectRoots, []string{"/tmp/notes"}) {
		t.Fatalf("ProjectRoots = %#v, want the directory as a root", opts.ProjectRoots)
	}
	if opts.ProjectExact != "" {
		t.Fatalf("ProjectExact = %q, want the root filter instead", opts.ProjectExact)
	}
	m.projectOnly = false
	opts = listOptsFor(m)
	if opts.ProjectExact != "" || len(opts.ProjectRoots) != 0 {
		t.Fatalf("all-project opts still scoped: %+v", opts)
	}
}

func TestSearchOptsFollowActiveProjectScope(t *testing.T) {
	m := modelState{projectOnly: true, projectScope: util.ProjectScope{
		CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo", "/tmp/feature"},
	}}
	opts := searchOptsFor(m, "needle")
	if opts.Query != "needle" || !reflect.DeepEqual(opts.ProjectRoots, m.projectScope.Worktrees) {
		t.Fatalf("search opts = %+v", opts)
	}
	m.projectOnly = false
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
