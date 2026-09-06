package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

func layoutTestModel() modelState {
	sm := model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project", MessageCount: 12}
	item := sessionItem{summary: sm, providerLbl: "Codex"}
	marked := map[string]bool{}
	return modelState{
		reg:      registry.New(),
		marked:   marked,
		sessions: newSessionList([]list.Item{item}, marked),
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
		preview: viewport.New(1, 1), searchInput: textinput.New(), renameInput: textinput.New(), selected: &item,
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		for _, ov := range []int{overlayNone, overlaySource, overlayTarget, overlayPreview, overlayDelete, overlayBatchTitle} {
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
	if m.overlay != overlaySource || cmd == nil {
		t.Fatalf("left did not open source drawer and hide the cursor: overlay=%d cmd=%v", m.overlay, cmd)
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

func TestScopeKeyTogglesProjectFilter(t *testing.T) {
	m := layoutTestModel()
	m.cwd = "/repo"
	m.projectOnly = true
	m.projectScope = util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo", "/tmp/feature"}}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(modelState)
	if m.projectOnly || cmd == nil {
		t.Fatalf("scope did not switch to all: projectOnly=%v cmd=%v", m.projectOnly, cmd)
	}
	updated, _ = m.Update(sessionsPageMsg{gen: m.pageGen})
	m = updated.(modelState)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(modelState)
	if !m.projectOnly || cmd == nil {
		t.Fatalf("scope did not switch back to project: projectOnly=%v cmd=%v", m.projectOnly, cmd)
	}
}

func TestScopeKeyRerunsActiveSearch(t *testing.T) {
	m := layoutTestModel()
	m.cwd = "/repo"
	m.searchQuery = "needle"
	m.projectScope = util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo"}}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(modelState)
	if !m.projectOnly || !m.loading || cmd == nil {
		t.Fatalf("active search was not rerun: projectOnly=%v loading=%v cmd=%v", m.projectOnly, m.loading, cmd)
	}
}

func TestHeaderAndEmptyViewExposeProjectScope(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 24
	m.projectOnly = true
	m.projectScope = util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo"}}
	header := ansi.Strip(m.headerView())
	if !strings.Contains(header, "当前项目") || !strings.Contains(header, "/repo") {
		t.Fatalf("header hides project scope: %q", header)
	}
	m.sessions.SetItems(nil)
	if empty := ansi.Strip(m.emptySessionsView()); !strings.Contains(empty, "按 f 查看全部") {
		t.Fatalf("empty view = %q", empty)
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

func TestRightOpensTargetOverlay(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := updated.(modelState).overlay; got != overlayTarget {
		t.Fatalf("right did not open target overlay: overlay=%d", got)
	}
}

func TestOpeningPickersRequestsAFullRepaint(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight} {
		m := layoutTestModel()
		m.width, m.height = 87, 24
		m.layout()
		_, cmd := m.Update(tea.KeyMsg{Type: key})
		if !commandContainsMessage(cmd, "tea.clearScreenMsg") {
			t.Fatalf("opening picker with %v did not request a full repaint", key)
		}
	}
}

func TestEnterDirectlyResumesSourceSession(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(modelState)
	if cmd == nil || got.launchTarget != "codex" || !strings.Contains(got.launch, "codex resume") {
		t.Fatalf("enter did not launch source session: target=%q launch=%q cmd=%v", got.launchTarget, got.launch, cmd)
	}
	if got.overlay != overlayNone {
		t.Fatalf("enter opened an overlay instead of resuming: %d", got.overlay)
	}
}

func TestPickersUseStableCenteredModals(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	base := m.View()

	m.overlay = overlaySource
	source := m.View()
	m.overlay = overlayTarget
	target := m.View()
	if !strings.Contains(source, "选择来源") || !strings.Contains(target, "选择去向") {
		t.Fatal("pickers lost their purpose labels")
	}
	if source == base || target == base || source == target {
		t.Fatal("source and target pickers must be distinct overlays")
	}
	for _, view := range []string{source, target} {
		var modalLine string
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "选择来源") || strings.Contains(line, "选择去向") {
				modalLine = line
				break
			}
		}
		if lipgloss.Width(modalLine) < 40 {
			t.Fatalf("picker is still a tiny drifting tooltip: %q", modalLine)
		}
	}
}

func TestProviderColumnPrecedesTitle(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := updated.(modelState).sessions.View()
	if strings.Index(view, "Codex") >= strings.Index(view, "A useful title") {
		t.Fatalf("source provider still trails the title: %q", view)
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

func TestWideHeaderKeepsTargetBesideSessionCount(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 24
	header := ansi.Strip(m.headerView())
	if got := ansi.StringWidth(header); got > m.width {
		t.Fatalf("header width = %d, want <= %d: %q", got, m.width, header)
	}
	if !strings.Contains(header, "个会话   │    去向 →") {
		t.Fatalf("target action did not return beside the session count: %q", header)
	}
}

func TestNarrowHeaderKeepsBothDirectionControls(t *testing.T) {
	for _, source := range []sourceChip{
		{id: "", name: "all", count: 3},
		{id: "claude-code", name: "Claude Code", count: 2},
		{id: "commandcode", name: "CommandCode", count: 1},
	} {
		m := layoutTestModel()
		m.sources = []sourceChip{source}
		m.width, m.height = 40, 12
		header := ansi.Strip(m.headerView())
		if ansi.StringWidth(header) > m.width {
			t.Fatalf("source %q header width = %d, want <= %d", source.name, ansi.StringWidth(header), m.width)
		}
		if !strings.Contains(header, "来源") || !strings.Contains(header, "去向 →") {
			t.Fatalf("source %q lost a direction control: %q", source.name, header)
		}
	}
}

func TestWideSessionsBreatheButNarrowSessionsStayCompact(t *testing.T) {
	items := []list.Item{
		sessionItem{summary: model.Summary{ID: "one", Provider: "codex", Title: "First title"}, providerLbl: "Codex"},
		sessionItem{summary: model.Summary{ID: "two", Provider: "pi", Title: "Second title"}, providerLbl: "pi"},
	}
	m := layoutTestModel()
	m.sessions.SetItems(items)
	m.width, m.height = 100, 24
	m.layout()
	wide := strings.Split(ansi.Strip(m.sessions.View()), "\n")
	wideFirst, wideSecond := lineContaining(wide, "First title"), lineContaining(wide, "Second title")
	if wideFirst < 0 || wideSecond-wideFirst != 2 {
		t.Fatalf("wide list row distance = %d, want 2: %q", wideSecond-wideFirst, wide)
	}

	m.width, m.height = 60, 16
	m.layout()
	narrow := strings.Split(ansi.Strip(m.sessions.View()), "\n")
	narrowFirst, narrowSecond := lineContaining(narrow, "First title"), lineContaining(narrow, "Second title")
	if narrowFirst < 0 || narrowSecond-narrowFirst != 1 {
		t.Fatalf("narrow list row distance = %d, want 1: %q", narrowSecond-narrowFirst, narrow)
	}
}

func TestSelectedCursorUsesIntersectionInk(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	m := layoutTestModel()
	m.width, m.height = 100, 24
	m.layout()
	if !strings.Contains(m.sessions.View(), selectedRow.Render("›")) {
		t.Fatal("selected cursor is not visually joined to the selected title")
	}
}

func TestWideIdleFooterKeepsOnlyTheActiveControls(t *testing.T) {
	m := layoutTestModel()
	m.status = ""
	m.width, m.height = 100, 24
	m.layout()
	footer := ansi.Strip(m.footerView())
	if strings.Contains(footer, "/tmp/project") || strings.Contains(footer, "session") {
		t.Fatalf("wide footer repeats row metadata: %q", footer)
	}
	if !strings.Contains(footer, "enter") {
		t.Fatalf("wide footer lost active controls: %q", footer)
	}
}

func TestOverlayPreservesBackgroundOutsideItsOwnBounds(t *testing.T) {
	background := strings.Repeat(strings.Repeat("x", 40)+"\n", 9) + strings.Repeat("x", 40)
	got := ansi.Strip(overlay(background, "12345\nabcde", 40))
	lines := strings.Split(got, "\n")
	for _, row := range []int{4, 5} {
		if !strings.HasPrefix(lines[row], "x") || !strings.HasSuffix(lines[row], "x") {
			t.Fatalf("overlay erased background on row %d: %q", row, lines[row])
		}
	}
}

func TestOverlayKeepsModalAlignedAcrossWideCharacterCuts(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	backgroundLine := mutedStyle.Render("│" + strings.Repeat("飞", 19) + "│")
	background := strings.Join([]string{backgroundLine, backgroundLine, backgroundLine}, "\n")
	got := overlay(background, "┏━━┓\n┃中┃\n┗━━┛", 40)
	for row, line := range strings.Split(got, "\n") {
		if width := ansi.StringWidth(line); width != 40 {
			t.Fatalf("row %d width = %d, want 40 after a wide-character cut: %q", row, width, ansi.Strip(line))
		}
	}
}

func TestSourceModalRowsShareOneCellWidth(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 87, 24
	m.layout()
	box := sourceModalStyle.Render(accentStyle.Render("选择来源") + "\n" +
		mutedStyle.Render("会话来自哪个 agent？") + "\n\n" + m.sourceList.View())
	lines := strings.Split(box, "\n")
	want := ansi.StringWidth(lines[0])
	for row, line := range lines[1:] {
		if got := ansi.StringWidth(line); got != want {
			t.Fatalf("source modal row %d width = %d, want %d: %q", row+1, got, want, ansi.Strip(line))
		}
	}
}

func TestSourceModalBordersStayInTheSameColumnsOverSessionRows(t *testing.T) {
	m := layoutTestModel()
	var sessions []list.Item
	for i := 0; i < 20; i++ {
		sessions = append(sessions, sessionItem{
			summary: model.Summary{
				ID:           "session",
				Provider:     "opencode",
				Title:        "Claude Code v2.1.260 发布说明摘要",
				ProjectPath:  "/Users/mingjian/Documents/sync/GitHub/another",
				MessageCount: 20,
			},
			providerLbl: "OpenCode",
		})
	}
	m.sessions.SetItems(sessions)
	m.sourceList = newSourceList([]list.Item{
		sourceChip{name: "all", count: 724},
		sourceChip{id: "claude-code", name: "Claude Code", count: 107},
		sourceChip{id: "codex", name: "Codex", count: 303},
		sourceChip{id: "opencode", name: "OpenCode", count: 20},
		sourceChip{id: "opencode2", name: "OpenCode 2", count: 12},
		sourceChip{id: "pi", name: "pi", count: 256},
		sourceChip{id: "agy", name: "Antigravity", count: 26},
	})
	m.width, m.height, m.overlay = 87, 24, overlaySource
	m.layout()

	left, right := -1, -1
	for row, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		for _, pair := range [][2]string{{"┏", "┓"}, {"┃", "┃"}, {"┗", "┛"}} {
			first, last := strings.Index(plain, pair[0]), strings.LastIndex(plain, pair[1])
			if first < 0 || last <= first {
				continue
			}
			gotLeft := ansi.StringWidth(plain[:first])
			gotRight := ansi.StringWidth(plain[:last])
			if left < 0 {
				left, right = gotLeft, gotRight
			} else if gotLeft != left || gotRight != right {
				t.Fatalf("source modal border shifted on row %d: (%d,%d), want (%d,%d): %q", row, gotLeft, gotRight, left, right, plain)
			}
			break
		}
	}
}

func TestEqualHeightOverlayPreservesBackgroundOutsideItsOwnBounds(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	backgroundLine := mutedStyle.Render(strings.Repeat("x", 40))
	background := strings.Join([]string{backgroundLine, backgroundLine}, "\n")
	got := ansi.Strip(overlay(background, "12345\nabcde", 40))
	for row, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "x") || !strings.HasSuffix(line, "x") {
			t.Fatalf("equal-height overlay erased background on row %d: %q", row, line)
		}
	}
}

func TestTargetModalKeepsTheDesignedWideProportion(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.overlay = overlayTarget
	m.layout()
	view := ansi.Strip(m.View())
	for _, line := range strings.Split(view, "\n") {
		left, right := strings.Index(line, "┏"), strings.LastIndex(line, "┓")
		if left >= 0 && right > left {
			if got := ansi.StringWidth(line[left : right+len("┓")]); got < 42 {
				t.Fatalf("target modal is narrower than the approved comp: %d columns", got)
			}
			return
		}
	}
	t.Fatal("target modal border not rendered")
}

func lineContaining(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func commandContainsMessage(cmd tea.Cmd, typeName string) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if fmt.Sprintf("%T", msg) == typeName {
		return true
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return false
	}
	for _, child := range batch {
		if child != nil && fmt.Sprintf("%T", child()) == typeName {
			return true
		}
	}
	return false
}

func TestTargetOverlayEscapeReturns(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
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

func TestArchiveOffersOneStepUndo(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("A did not start native archive")
	}
	summary := m.sessions.SelectedItem().(sessionItem).summary
	updated, _ = m.Update(archiveDoneMsg{summary: summary, archived: true})
	got := updated.(modelState)
	if got.lastArchived == nil || !strings.Contains(got.help(), "撤销") {
		t.Fatalf("archive has no one-step undo: %+v", got.lastArchived)
	}
}

func TestCtrlROpensPrefilledNativeRename(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if got.overlay != overlayRename || got.renameInput.Value() != "A useful title" || !got.renameInput.Focused() || cmd == nil {
		t.Fatalf("ctrl+r did not open prefilled rename: overlay=%d value=%q focused=%v cmd=%v", got.overlay, got.renameInput.Value(), got.renameInput.Focused(), cmd)
	}
}

func TestCtrlRWithoutConfiguredAgentAsksForNoSuggestion(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if got.suggesting || got.suggestFor != "" {
		t.Fatalf("unconfigured another must not call a model: suggesting=%v for=%q", got.suggesting, got.suggestFor)
	}
	if strings.Contains(got.View(), "AI 建议") {
		t.Fatal("suggestion row shown while the feature is off")
	}
}

func TestSuggestedTitleIsAcceptedWithTab(t *testing.T) {
	m := layoutTestModel()
	m.titleCfg = titler.Config{Provider: "pi"}
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if !got.suggesting || got.suggestFor != "session" {
		t.Fatalf("ctrl+r did not request a suggestion: %+v", got.suggestFor)
	}
	if got.renameInput.Value() != "A useful title" {
		t.Fatal("the box must open on the original title, not wait for a model")
	}

	arrived, _ := got.Update(titleSuggestionMsg{sessionID: "session", title: "0903｜修复｜删除条目快捷键冲突"})
	got = arrived.(modelState)
	if got.suggesting || got.suggestion == "" || !strings.Contains(got.help(), "tab") {
		t.Fatalf("suggestion not offered: %+v", got.suggestion)
	}
	if got.renameInput.Value() != "A useful title" {
		t.Fatal("a suggestion must not overwrite the field on its own")
	}

	accepted, _ := got.Update(tea.KeyMsg{Type: tea.KeyTab})
	if value := accepted.(modelState).renameInput.Value(); value != "0903｜修复｜删除条目快捷键冲突" {
		t.Fatalf("tab did not accept the suggestion: %q", value)
	}
}

func TestStaleAndFailedSuggestionsStayOutOfTheWay(t *testing.T) {
	m := layoutTestModel()
	m.titleCfg = titler.Config{Provider: "pi"}
	m.width, m.height = 100, 30
	m.layout()
	opened, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := opened.(modelState)

	other, _ := got.Update(titleSuggestionMsg{sessionID: "a-different-session", title: "0903｜功能｜别的会话"})
	if s := other.(modelState); s.suggestion != "" {
		t.Fatalf("a suggestion for another session was shown: %q", s.suggestion)
	}

	failed, _ := got.Update(titleSuggestionMsg{sessionID: "session", err: errors.New("pi 未登录")})
	fail := failed.(modelState)
	if fail.err != "" {
		t.Fatalf("a failed suggestion leaked into the main error line: %q", fail.err)
	}
	if fail.suggesting || !strings.Contains(fail.View(), "建议不可用") {
		t.Fatal("a failed suggestion is not reported in the rename box")
	}

	closed, _ := fail.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s := closed.(modelState); s.suggestErr != "" || s.suggestFor != "" {
		t.Fatalf("suggestion state survived the overlay: %+v", s.suggestErr)
	}
}

func TestRenameOverlayWithSuggestionFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		m := layoutTestModel()
		m.overlay = overlayRename
		m.suggestion = "0903｜修复｜删除条目快捷键冲突"
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := updated.(modelState).View()
		if got := lipgloss.Width(view); got > size[0] {
			t.Errorf("%dx%d width = %d", size[0], size[1], got)
		}
		if got := lipgloss.Height(view); got > size[1] {
			t.Errorf("%dx%d height = %d", size[0], size[1], got)
		}
	}
}

func TestCtrlDOpensDeleteWithCancelSelected(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(modelState)
	if got.overlay != overlayDelete || got.deleteChoice != 0 {
		t.Fatalf("ctrl+d did not open safe delete state: overlay=%d choice=%d", got.overlay, got.deleteChoice)
	}
	if !strings.Contains(got.View(), "删除会话？") || !strings.Contains(got.View(), "默认") && !strings.Contains(got.View(), "取消") {
		t.Fatal("delete confirmation does not make cancellation visible")
	}
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(modelState).overlay != overlayNone || cmd != nil {
		t.Fatal("enter on the default cancel choice must not delete")
	}
}

func TestDeleteRequiresExplicitChoice(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.overlay = overlayDelete
	m.deleteChoice = 0
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	got := updated.(modelState)
	if got.deleteChoice != 1 {
		t.Fatal("right did not move focus to delete")
	}
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("explicit delete choice did not start deletion")
	}
}

func TestDeleteRemovesPiSourceAndIndexRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	p := pi.New()
	write, err := p.Write(context.Background(), &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/tmp/delete-fixture", Title: "delete me",
		Messages: []model.Message{{Role: model.RoleUser, Content: "temporary"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if _, err := index.UpdateIncremental(context.Background(), reg, idx, "pi"); err != nil {
		t.Fatal(err)
	}
	if n, _ := idx.Count(index.ListOpts{Provider: "pi"}); n != 1 {
		t.Fatalf("fixture was not indexed: count=%d", n)
	}

	cmd := deleteSessionCmd(context.Background(), reg, idx, model.Summary{
		ID: write.SessionID, Provider: "pi", ProjectPath: write.ProjectPath, StoragePath: write.StoragePath, Title: "delete me",
	})
	msg := cmd().(deleteDoneMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, err := os.Stat(write.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("source file still exists: %v", err)
	}
	if n, _ := idx.Count(index.ListOpts{Provider: "pi"}); n != 0 {
		t.Fatalf("index still contains deleted session: count=%d", n)
	}
}

func TestAgyConversationEnvironmentMarksCurrentSession(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "agy-current")
	if !isCurrentSession(model.Summary{ID: "agy-current", Provider: "agy"}) {
		t.Fatal("active Antigravity conversation was not recognized")
	}
	if isCurrentSession(model.Summary{ID: "other", Provider: "agy"}) {
		t.Fatal("unrelated Antigravity conversation was marked current")
	}
}

func TestCurrentPiSessionCannotBeDeleted(t *testing.T) {
	m := layoutTestModel()
	item := m.sessions.SelectedItem().(sessionItem)
	item.summary.Provider = "pi"
	item.summary.ID = "live-pi"
	item.summary.StoragePath = "/tmp/live-pi.jsonl"
	m.sessions.SetItems([]list.Item{item})
	t.Setenv("PI_SESSION_ID", "live-pi")
	t.Setenv("PI_SESSION_FILE", "/tmp/live-pi.jsonl")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(modelState)
	if got.overlay == overlayDelete || !strings.Contains(got.err, "当前") {
		t.Fatalf("current session was not protected: overlay=%d err=%q", got.overlay, got.err)
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

func markKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestXTogglesTheBatchMark(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	updated, _ := m.Update(markKey('x'))
	got := updated.(modelState)
	if !got.marked["session"] {
		t.Fatalf("x did not mark the row: %v", got.marked)
	}
	if !strings.Contains(got.View(), "✓") {
		t.Fatal("a marked row must render its mark")
	}

	updated, _ = got.Update(markKey('x'))
	if again := updated.(modelState); again.marked["session"] {
		t.Fatalf("x did not unmark the row: %v", again.marked)
	}
}

func TestSpaceStillOpensPreviewInsteadOfMarking(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(markKey(' '))
	got := updated.(modelState)
	if len(got.marked) != 0 {
		t.Fatalf("space must not mark; it is the preview key: %v", got.marked)
	}
	if !got.loading || cmd == nil {
		t.Fatalf("space no longer opens the preview: loading=%v cmd=%v", got.loading, cmd)
	}
}

func TestAMarksEveryVisibleRowAndClearsOnRepeat(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	updated, _ := m.Update(markKey('a'))
	got := updated.(modelState)
	if len(got.marked) != len(got.sessions.Items()) {
		t.Fatalf("a did not mark every visible row: %d of %d", len(got.marked), len(got.sessions.Items()))
	}

	updated, _ = got.Update(markKey('a'))
	if again := updated.(modelState); len(again.marked) != 0 {
		t.Fatalf("a did not clear a fully marked page: %v", again.marked)
	}
}

func TestMarksSurviveAPageReload(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(markKey('x'))
	got := updated.(modelState)

	// A filter change or an index refresh rebuilds the rows. An index-keyed
	// selection would follow whatever now sits in that position instead.
	sm := model.Summary{ID: "other", Provider: "pi", Title: "Another session", ProjectPath: "/tmp/project"}
	got.sessions.SetItems([]list.Item{
		sessionItem{summary: sm, providerLbl: "pi"},
		sessionItem{summary: model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project"}, providerLbl: "Codex"},
	})
	if !got.marked["session"] {
		t.Fatalf("the mark did not survive a page reload: %v", got.marked)
	}
	if got.marked["other"] {
		t.Fatal("the mark moved to a different session")
	}
}
