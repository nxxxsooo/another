package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSetupRenderProbe(t *testing.T) {
	if os.Getenv("RENDER_PROBE") == "" {
		t.Skip("set RENDER_PROBE=1 to print setup")
	}
	m := setupFixture()
	m.width, m.height = 88, 24
	fmt.Println("======== setup")
	fmt.Println(m.View())
}

func setupFixture() setupModel {
	return setupModel{
		items: []setupItem{
			{id: "pi", name: "pi", command: "pi", data: true, cli: true, available: true, sessions: 12},
			{id: "codex", name: "Codex", command: "codex", data: true, cli: true, available: true, sessions: 20},
			{id: "cursor", name: "Cursor", command: "cursor-agent", available: false},
		},
		selected: map[string]bool{"pi": true},
		width:    80, height: 24,
	}
}

func TestSetupSpaceTogglesAvailableAgent(t *testing.T) {
	m := setupFixture()
	m.cursor = 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !updated.(setupModel).selected["codex"] {
		t.Fatal("space did not select available agent")
	}
}

func TestSetupRejectsUnavailableAgent(t *testing.T) {
	m := setupFixture()
	m.cursor = 2
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := updated.(setupModel)
	if got.selected["cursor"] || !strings.Contains(got.err, "未检测到") {
		t.Fatalf("unavailable agent selection = %+v", got)
	}
}

func TestSetupEnterCompletesWithSelection(t *testing.T) {
	m := setupFixture()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(setupModel)
	if !got.done || got.cancelled || cmd == nil {
		t.Fatalf("setup did not finish: done=%v cancelled=%v cmd=%v", got.done, got.cancelled, cmd)
	}
}

func TestSetupRequiresOneAgent(t *testing.T) {
	m := setupFixture()
	m.selected = map[string]bool{}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(setupModel)
	if got.done || cmd != nil || !strings.Contains(got.err, "至少") {
		t.Fatalf("empty setup was accepted: %+v", got)
	}
}

func TestSetupTooSmallView(t *testing.T) {
	m := setupFixture()
	m.width, m.height = 47, 19
	if view := m.View(); !strings.Contains(view, "Terminal too small") {
		t.Fatalf("small view = %q", view)
	}
}

func TestSetupViewFitsTerminal(t *testing.T) {
	m := setupFixture()
	for _, size := range [][2]int{{48, 20}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%dx%d rendered %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}
