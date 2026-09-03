package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
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
		sourceList: newSourceList([]list.Item{
			sourceChip{id: "", name: "all", count: 3},
			sourceChip{id: "codex", name: "Codex", count: 2},
			sourceChip{id: "pi", name: "pi", count: 1},
		}),
		preview: viewport.New(1, 1), searchInput: textinput.New(), selected: &item,
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		for _, ov := range []int{overlayNone, overlaySource, overlayTarget, overlayPreview} {
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

func TestLeftOpensSourceDrawerAndAppliesSelection(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(modelState)
	if m.overlay != overlaySource || cmd != nil {
		t.Fatalf("left did not open source drawer: overlay=%d cmd=%v", m.overlay, cmd)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(modelState)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	if m.overlay != overlayNone || m.sourceIdx != 1 || cmd == nil {
		t.Fatalf("right did not apply source: overlay=%d idx=%d cmd=%v", m.overlay, m.sourceIdx, cmd)
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

func TestRightAndEnterOpenTargetOverlay(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyRight, tea.KeyEnter} {
		m := layoutTestModel()
		m.width, m.height = 80, 24
		m.layout()
		updated, _ := m.Update(tea.KeyMsg{Type: key})
		if got := updated.(modelState).overlay; got != overlayTarget {
			t.Fatalf("key %v did not open target overlay: overlay=%d", key, got)
		}
	}
}

func TestDrawersOccupyTheirSemanticSides(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	base := m.View()

	m.overlay = overlaySource
	left := m.View()
	m.overlay = overlayTarget
	right := m.View()
	if !strings.Contains(left, "← 来源") || !strings.Contains(right, "去向 →") {
		t.Fatal("drawers lost their directional labels")
	}
	if left == base || right == base || left == right {
		t.Fatal("source and target drawers must be distinct overlays")
	}
	leftLine, rightLine := "", ""
	for _, line := range strings.Split(left, "\n") {
		if strings.Contains(line, "← 来源") {
			leftLine = line
			break
		}
	}
	for _, line := range strings.Split(right, "\n") {
		if strings.Contains(line, "去向 →") {
			rightLine = line
			break
		}
	}
	if strings.Index(leftLine, "← 来源") >= strings.Index(rightLine, "去向 →") {
		t.Fatalf("source drawer is not left of target drawer:\n%s\n%s", leftLine, rightLine)
	}
}

func TestMessageCountHasUnit(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := updated.(modelState).sessions.View()
	if !strings.Contains(view, "12条") {
		t.Fatalf("message count is still a bare number: %q", view)
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

func TestClaudeProjectTrustedReadsExactProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"projects":{"` + project + `":{"hasTrustDialogAccepted":true},"/other":{"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if !claudeProjectTrusted(path, project) {
		t.Fatal("trusted project was not recognized")
	}
	if claudeProjectTrusted(path, "/other") {
		t.Fatal("untrusted project was reported trusted")
	}
	if claudeProjectTrusted(filepath.Join(t.TempDir(), "missing"), project) {
		t.Fatal("missing config must be untrusted")
	}
}

func TestMigrationResultRetainsLaunchIdentity(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	updated, _ := m.Update(migrateDoneMsg{
		targetID: "claude-code",
		res: &migrate.Result{
			Resume:     "cd '/tmp/project' && claude --resume 'abc'",
			TargetName: "Claude Code",
			Write:      &provider.WriteResult{ProjectPath: "/tmp/project"},
		},
	})
	got := updated.(modelState)
	if got.launchTarget != "claude-code" || got.launchProject != "/tmp/project" {
		t.Fatalf("launch identity lost: target=%q project=%q", got.launchTarget, got.launchProject)
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

// After a migration the tool must be able to land the user in the other agent.
// Handing back a command to copy out of a full-screen UI is a dead end.
func TestEnterAfterMigrationLaunchesTarget(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.lastResume = "cd '/tmp/project' && codex resume 'abc'"
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(modelState)
	if got.launch != m.lastResume {
		t.Fatalf("launch = %q, want the resume command", got.launch)
	}
	if cmd == nil {
		t.Fatal("enter did not quit the program to hand over the terminal")
	}
	if got.overlay != overlayNone {
		t.Fatalf("enter reopened an overlay instead of launching: %d", got.overlay)
	}
}

// Escape keeps browsing without launching, so a finished migration does not
// trap the session on one screen.
func TestEscapeAfterMigrationResumesBrowsing(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.lastResume = "cd '/tmp' && codex resume 'abc'"
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(modelState)
	if got.lastResume != "" || got.launch != "" {
		t.Fatalf("escape did not return to browsing: resume=%q launch=%q", got.lastResume, got.launch)
	}
}
