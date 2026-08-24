package tui

import (
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func layoutTestModel() modelState {
	delegate := list.NewDefaultDelegate()
	sm := model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project"}
	item := sessionItem{summary: sm, providerLbl: "Codex"}
	m := modelState{
		reg: registry.New(), sessions: list.New([]list.Item{item}, delegate, 1, 1),
		providers: list.New([]list.Item{providerItem{name: "All agents"}}, delegate, 1, 1),
		actions:   list.New(actionItems(registry.New(), sm), delegate, 1, 1),
		targets:   list.New([]list.Item{targetItem{id: "claude-code", name: "Claude Code"}}, delegate, 1, 1),
		preview:   viewport.New(1, 1), searchInput: textinput.New(), selected: &item,
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
	return m
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		for _, stage := range []int{stageSessions, stageProviders, stageActions, stageMigrate, stagePreview, stageConfirm} {
			m := layoutTestModel()
			m.stage, m.confirmTarget = stage, "claude-code"
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := updated.(modelState).View()
			if got := lipgloss.Width(view); got > size[0] {
				t.Errorf("%dx%d stage %d width = %d", size[0], size[1], stage, got)
			}
			if got := lipgloss.Height(view); got > size[1] {
				t.Errorf("%dx%d stage %d height = %d", size[0], size[1], stage, got)
			}
		}
	}
}

func TestNarrowPreviewAndResizePreserveContent(t *testing.T) {
	m := layoutTestModel()
	m.stage = stagePreview
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	m = updated.(modelState)
	if !strings.Contains(m.preview.View(), "full preview content") {
		t.Fatal("narrow preview lost raw content")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(modelState)
	if !strings.Contains(m.preview.View(), "full preview content") {
		t.Fatal("resized preview lost raw content")
	}
}

func TestTooSmallView(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 39, 11
	if got := m.View(); !strings.Contains(got, "Terminal too small") {
		t.Fatalf("view = %q", got)
	}
}

func TestSearchAndSubagentKeys(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height, m.stage = 80, 24, stageSessions
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(modelState)
	if !m.searching || !m.searchInput.Focused() {
		t.Fatal("/ did not focus global search")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(modelState)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !updated.(modelState).includeSubagents {
		t.Fatal("s did not include subagents")
	}
}

func TestEscapeClearsAppliedSearch(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height, m.stage = 80, 24, stageSessions
	m.searchQuery = "needle"
	m.searchInput.SetValue("needle")
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(modelState)
	if m.searchQuery != "" || m.searchInput.Value() != "" || cmd == nil {
		t.Fatalf("escape did not clear search: query=%q input=%q cmd=%v", m.searchQuery, m.searchInput.Value(), cmd)
	}
}

func TestMigrationRequiresConfirmation(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height, m.stage = 80, 24, stageMigrate
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd != nil || m.stage != stageConfirm || m.confirmTarget != "claude-code" {
		t.Fatalf("enter migrated without confirmation: stage=%d target=%q", m.stage, m.confirmTarget)
	}
}
