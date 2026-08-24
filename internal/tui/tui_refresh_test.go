package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"

	"github.com/CyrusSE/agenthop/internal/registry"
)

func TestCompletedRefreshClearsIndexing(t *testing.T) {
	m := modelState{
		indexing:  true,
		reg:       registry.New(),
		providers: list.New(nil, list.NewDefaultDelegate(), 0, 0),
	}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 3})
	if res.(modelState).indexing {
		t.Fatal("completed refresh msg must clear indexing")
	}
}

func TestContentCompletionRefreshesActiveSearch(t *testing.T) {
	m := modelState{searchQuery: "needle", reg: registry.New()}
	_, cmd := m.Update(contentIndexedMsg{})
	if cmd == nil {
		t.Fatal("content completion did not refresh active search")
	}
}
