package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/registry"
)

// TestRenderProbe prints the real screen against the local index. An alt-screen
// TUI cannot be captured through a pipe, so this is how the layout gets looked
// at. Skipped unless RENDER_PROBE is set: it reads the developer's own index.
//
//	RENDER_PROBE=1 go test ./internal/tui/ -run TestRenderProbe -v
func TestRenderProbe(t *testing.T) {
	if os.Getenv("RENDER_PROBE") == "" {
		t.Skip("set RENDER_PROBE=1 to print the rendered screen")
	}
	reg := registry.New()
	idx, err := index.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	counts, _ := idx.CountByProvider()
	summaries, err := idx.List(index.ListOpts{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	var items []list.Item
	for _, s := range summaries {
		items = append(items, sessionItem{summary: s, providerLbl: registry.DisplayName(reg, s.Provider)})
	}
	sources := sourceChips(reg, counts)
	m := modelState{
		reg: reg, idx: idx,
		sessions:      newSessionList(items),
		sourceList:    newSourceList(sourceItems(sources)),
		targets:       newTargetList(targetItems(reg, "pi")),
		sources:       sources,
		totalSessions: sources[0].count,
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	shown := updated.(modelState)
	fmt.Println("======== list")
	fmt.Println(shown.View())
	shown.overlay = overlaySource
	shown.layout()
	fmt.Println("======== source picker")
	fmt.Println(shown.View())
	shown.overlay = overlayTarget
	shown.layout()
	fmt.Println("======== target picker")
	fmt.Println(shown.View())
	if item, ok := shown.sessions.SelectedItem().(sessionItem); ok {
		shown.selected = &item
	}
	shown.overlay = overlayDelete
	shown.deleteChoice = 0
	shown.layout()
	fmt.Println("======== delete confirmation")
	fmt.Println(shown.View())
}
