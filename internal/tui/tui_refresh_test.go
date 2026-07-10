package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"

	"github.com/CyrusSE/agenthop/internal/registry"
)

// Startup sends an initial cached-counts message; it must not clear the
// indexing flag — the always-running background refresh does that later.
func TestInitialRefreshMsgKeepsIndexing(t *testing.T) {
	m := modelState{
		indexing:  true,
		reg:       registry.New(),
		providers: list.New(nil, list.NewDefaultDelegate(), 0, 0),
	}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, initial: true})
	if !res.(modelState).indexing {
		t.Fatal("initial refresh msg must not clear indexing")
	}
	res, _ = m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 3})
	if res.(modelState).indexing {
		t.Fatal("completed refresh msg must clear indexing")
	}
}
