package tui

import (
	"testing"

	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

func TestCompletedRefreshClearsIndexing(t *testing.T) {
	m := modelState{indexing: true, reg: registry.New(), sourceList: newSourceList(nil)}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 3})
	if res.(modelState).indexing {
		t.Fatal("completed refresh msg must clear indexing")
	}
}

func TestRefreshReplacesDiscoveredProjectScope(t *testing.T) {
	m := modelState{reg: registry.New(), sourceList: newSourceList(nil),
		projectScope: util.ProjectScope{CWD: "/old", Root: "/old"}}
	want := util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo", "/tmp/feature"}}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, project: &want})
	got := res.(modelState).projectScope
	if got.Root != want.Root || len(got.Worktrees) != 2 || got.Worktrees[1] != want.Worktrees[1] {
		t.Fatalf("project scope = %+v, want %+v", got, want)
	}
}

// A refresh rebuilds the source row, so a stale selection index must not point
// past the end of the new chips.
func TestRefreshKeepsSourceSelectionInRange(t *testing.T) {
	m := modelState{
		reg:        registry.New(),
		sources:    []sourceChip{{id: ""}, {id: "codex"}, {id: "pi"}},
		sourceList: newSourceList(nil),
		sourceIdx:  2,
	}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 1})
	got := res.(modelState)
	if got.sourceIdx >= len(got.sources) {
		t.Fatalf("sourceIdx %d out of range for %d chips", got.sourceIdx, len(got.sources))
	}
}

func TestContentCompletionRefreshesActiveSearch(t *testing.T) {
	m := modelState{searchQuery: "needle", reg: registry.New()}
	_, cmd := m.Update(contentIndexedMsg{})
	if cmd == nil {
		t.Fatal("content completion did not refresh active search")
	}
}
