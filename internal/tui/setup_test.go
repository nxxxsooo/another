package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
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
		selected:   map[string]bool{"pi": true},
		modelInput: textinput.New(),
		width:      80, height: 24,
	}
}

// onlyPIOnPath makes agent CLI detection deterministic: exactly one agent can
// generate titles, whatever the machine running the test has installed.
func onlyPIOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// titlePageFixture is the setup model as it looks after page one is accepted.
func titlePageFixture(t *testing.T) setupModel {
	t.Helper()
	onlyPIOnPath(t)
	m := setupFixture()
	m.selected = map[string]bool{"pi": true, "codex": true}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(setupModel)
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

func TestSetupEnterOpensTitlePageThenCompletes(t *testing.T) {
	got := titlePageFixture(t)
	if got.page != setupPageTitle || got.done {
		t.Fatalf("first enter should open the title page: page=%d done=%v", got.page, got.done)
	}
	final, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	done := final.(setupModel)
	if !done.done || done.cancelled || cmd == nil {
		t.Fatalf("setup did not finish: done=%v cancelled=%v cmd=%v", done.done, done.cancelled, cmd)
	}
}

func TestSetupTitlePageOffersOnlyInstalledCapableAgents(t *testing.T) {
	got := titlePageFixture(t)
	if len(got.titleOpts) != 2 {
		t.Fatalf("title options = %+v, want off + pi", got.titleOpts)
	}
	if got.titleOpts[0].id != "" || got.titleOpts[1].id != "pi" {
		t.Fatalf("title options = %+v", got.titleOpts)
	}
	if got.titleCursor != 0 || got.titleModel() != nil {
		t.Fatalf("title suggestions must default to off: cursor=%d model=%+v", got.titleCursor, got.titleModel())
	}
}

func TestSetupTitlePageRecordsAgentAndModel(t *testing.T) {
	m := titlePageFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(setupModel)
	for _, r := range "sonnet" {
		typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = typed.(setupModel)
	}
	cfg := m.titleModel()
	if cfg == nil || cfg.Provider != "pi" || cfg.Model != "sonnet" {
		t.Fatalf("title model = %+v", cfg)
	}
}

func TestSetupTitlePageIgnoresTypingWhileDisabled(t *testing.T) {
	m := titlePageFixture(t)
	typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := typed.(setupModel)
	if got.modelInput.Value() != "" || got.titleModel() != nil {
		t.Fatalf("typing while off leaked into config: %q %+v", got.modelInput.Value(), got.titleModel())
	}
}

func TestSetupTitlePageEscGoesBackWithoutCancelling(t *testing.T) {
	m := titlePageFixture(t)
	back, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := back.(setupModel)
	if got.page != setupPageAgents || got.cancelled || got.done {
		t.Fatalf("esc on title page = page:%d cancelled:%v done:%v", got.page, got.cancelled, got.done)
	}
}

func TestSetupTitlePageViewFitsTerminal(t *testing.T) {
	m := titlePageFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(setupModel)
	for _, size := range [][2]int{{48, 20}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%dx%d rendered %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
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
