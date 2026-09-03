package tui

import (
	"testing"

	"github.com/CyrusSE/agenthop/internal/index"
)

// The browser lists everywhere. Scoping to the working directory is a CLI
// concern; in the TUI it silently hid the sessions people came to find.
func TestListOptsBrowsesEverywhere(t *testing.T) {
	m := modelState{cwd: "/home/user/proj"}
	opts := listOptsFor(m)
	if opts.ProjectCWD != "" {
		t.Fatalf("ProjectCWD = %q, want empty so the browser spans every project", opts.ProjectCWD)
	}
	if opts.IncludeSubagents {
		t.Fatal("subagent sessions must stay out of the browser")
	}
	if opts.Limit != maxShowAllPage {
		t.Fatalf("Limit = %d, want the single-fetch cap %d", opts.Limit, maxShowAllPage)
	}
	var _ index.ListOpts = opts
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
