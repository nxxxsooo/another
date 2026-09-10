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
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/i18n"
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
	// The probe exists to look at the real screen, so it resolves the
	// interface language the way the program does: from configuration, or
	// from the locale. ANOTHER_LANG forces one for a side-by-side look.
	//
	//	RENDER_PROBE=1 ANOTHER_LANG=zh go test ./internal/tui/ -run TestRenderProbe -v
	language := os.Getenv("ANOTHER_LANG")
	if language == "" {
		if settings, err := config.LoadSettings(); err == nil {
			language = settings.UI.Language
		}
	}
	useLanguage(t, i18n.Lang(language))
	reg := registry.New()
	idx, err := index.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
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
		items = append(items, sessionItem{summary: s})
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
	updated, _ := m.Update(tea.WindowSizeMsg{Width: probeWidth(), Height: probeHeight()})
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
	// Grouping is what this repository's own sessions look like sorted into
	// their worktrees, which is the case it was built for.
	grouped := shown
	grouped.grouped = true
	grouped.setSessionItems(items)
	grouped.layout()
	fmt.Println("======== list (grouped by tree)")
	fmt.Println(grouped.View())
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

// probeHeight is the other half of that: a grouped list needs more rows than a
// flat one to show a second heading at all, and a paging artefact is invisible
// at a height that never pages. RENDER_PROBE_HEIGHT=40 asks for the taller look.
func probeHeight() int {
	if n, err := strconv.Atoi(os.Getenv("RENDER_PROBE_HEIGHT")); err == nil && n >= 12 {
		return n
	}
	return 20
}
