package tui

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
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
	// go test writes through a pipe, where lipgloss detects no terminal and
	// drops every color. A probe for reviewing color decisions has to force the
	// profile, or it prints the one thing it was run to show as plain text.
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	reg := registry.New()
	idx, err := index.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	cwd, _ := os.Getwd()
	project := util.DiscoverProjectScope(t.Context(), cwd)
	opts := index.ListOpts{Limit: 12}
	applyProjectScope(&opts, project)
	counts, _ := idx.CountByProviderFiltered(opts)
	summaries, err := idx.List(opts)
	if err != nil {
		t.Fatal(err)
	}
	var items []list.Item
	for _, s := range summaries {
		items = append(items, sessionItem{summary: s, providerLbl: registry.DisplayName(reg, s.Provider)})
	}
	sources := sourceChips(reg, counts)
	marked := map[string]bool{}
	m := modelState{
		reg: reg, idx: idx,
		marked:        marked,
		sessions:      newSessionList(items, marked),
		sourceList:    newSourceList(sourceItems(sources)),
		targets:       newTargetList(targetItems(reg, "pi")),
		sources:       sources,
		totalSessions: sources[0].count,
		cwd:           project.CWD,
		projectScope:  project,
		projectOnly:   true,
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: probeWidth(), Height: 20})
	shown := updated.(modelState)
	fmt.Println("======== list (project scope)")
	fmt.Println(shown.View())
	// The project column only exists in the global scope, so the probe has to
	// show both or it never shows that column at all.
	global := shown
	global.projectOnly = false
	global.layout()
	fmt.Println("======== list (all projects)")
	fmt.Println(global.View())
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

// probeWidth lets a reviewer reproduce a width-specific layout complaint:
// RENDER_PROBE_WIDTH=124 pins the terminal the screenshot came from.
func probeWidth() int {
	if n, err := strconv.Atoi(os.Getenv("RENDER_PROBE_WIDTH")); err == nil && n >= 40 {
		return n
	}
	return 100
}
