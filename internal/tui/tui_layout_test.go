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
	sm := model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project", MessageCount: 12}
	item := sessionItem{summary: sm, providerLbl: "Codex"}
	return modelState{
		reg:      registry.New(),
		sessions: newSessionList([]list.Item{item}),
		targets:  newTargetList([]list.Item{targetItem{id: "claude-code", name: "Claude Code"}}),
		sources: []sourceChip{
			{id: "", name: "all", count: 3},
			{id: "codex", name: "Codex", count: 2},
			{id: "pi", name: "pi", count: 1},
		},
		preview: viewport.New(1, 1), searchInput: textinput.New(), selected: &item,
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		for _, ov := range []int{overlayNone, overlayTarget, overlayPreview} {
			m := layoutTestModel()
			m.overlay = ov
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := updated.(modelState).View()
			if got := lipgloss.Width(view); got > size[0] {
				t.Errorf("%dx%d overlay %d width = %d", size[0], size[1], ov, got)
			}
			if got := lipgloss.Height(view); got > size[1] {
				t.Errorf("%dx%d overlay %d height = %d", size[0], size[1], ov, got)
			}
		}
	}
}

func TestNarrowPreviewAndResizePreserveContent(t *testing.T) {
	m := layoutTestModel()
	m.overlay = overlayPreview
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

// The session list is the whole screen, so a row must stay on one line however
// long the title is — otherwise the visible session count halves.
func TestSessionRowStaysOneLine(t *testing.T) {
	m := layoutTestModel()
	long := strings.Repeat("很长的中文标题", 20)
	m.sessions.SetItems([]list.Item{sessionItem{
		summary:     model.Summary{ID: "x", Provider: "pi", Title: long},
		providerLbl: "pi",
	}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(modelState)
	for _, line := range strings.Split(m.sessions.View(), "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("row wider than terminal: %d", lipgloss.Width(line))
		}
	}
	if n := len(strings.Split(strings.TrimRight(m.sessions.View(), "\n"), "\n")); n > m.sessions.Height() {
		t.Fatalf("one item rendered %d lines", n)
	}
}

func TestLeftRightCyclesSource(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	if m.sourceIdx != 1 || cmd == nil {
		t.Fatalf("right did not move to the next source: idx=%d cmd=%v", m.sourceIdx, cmd)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := updated.(modelState).sourceIdx; got != 0 {
		t.Fatalf("left did not move back: idx=%d", got)
	}
	// Wrapping keeps the row navigable without an extra home/end key.
	m.sourceIdx = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := updated.(modelState).sourceIdx; got != len(m.sources)-1 {
		t.Fatalf("left at the first chip should wrap, got %d", got)
	}
}

func TestSearchKeyFocusesInput(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(modelState)
	if !m.searching || !m.searchInput.Focused() {
		t.Fatal("/ did not focus search")
	}
}

func TestEscapeClearsAppliedSearch(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.searchQuery = "needle"
	m.searchInput.SetValue("needle")
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(modelState)
	if m.searchQuery != "" || m.searchInput.Value() != "" || cmd == nil {
		t.Fatalf("escape did not clear search: query=%q input=%q cmd=%v", m.searchQuery, m.searchInput.Value(), cmd)
	}
}

func TestEnterOpensTargetOverlayAndEscapeReturns(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if m.overlay != overlayTarget {
		t.Fatalf("enter did not open the target overlay: overlay=%d", m.overlay)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(modelState).overlay; got != overlayNone {
		t.Fatalf("escape did not close the overlay: overlay=%d", got)
	}
}

// Picking a target runs the migration directly. The engine verifies its own
// write and rolls back, so a second confirmation only added a keystroke.
func TestEnterOnTargetStartsMigration(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.overlay = overlayTarget
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a target did not start a migration")
	}
	if !updated.(modelState).loading {
		t.Fatal("migration did not enter the loading state")
	}
}

func TestResumeLineReplacesSummaryAfterMigration(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	m.lastResume = "cd '/tmp/project' && codex resume 'abc'"
	if !strings.Contains(m.footerView(), "codex resume") {
		t.Fatal("footer did not surface the resume command")
	}
}
