package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/i18n"
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
	item := sessionItem{summary: sm}
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
		preview: viewport.New(1, 1), searchInput: textinput.New(), renameInput: textinput.New(),
		relocateInput: textinput.New(), selected: &item, spinner: newWaitSpinner(),
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
}

// Every screen is measured in both languages. An English sentence is longer
// than the Chinese one that means the same thing, and CJK text is twice as
// wide per character; a layout verified in one language says nothing about
// the other.
func TestViewsFitTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
			for _, ov := range []int{overlayNone, overlaySource, overlayTarget, overlayPreview, overlayDelete, overlayRename, overlayRelocate, overlayBatchTitle} {
				m := layoutTestModel()
				m.overlay = ov
				if ov == overlayRelocate {
					// The relocate box carries a path, which is the longest
					// single token any modal has to hold.
					m.relocateInput.SetValue("/Users/someone/Documents/sync/GitHub/another/.worktrees/relocate")
					m.relocateCanMove = true
				}
				updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				view := updated.(modelState).View()
				if got := lipgloss.Width(view); got > size[0] {
					t.Errorf("%s %dx%d overlay %d width = %d", lang, size[0], size[1], ov, got)
				}
				if got := lipgloss.Height(view); got > size[1] {
					t.Errorf("%s %dx%d overlay %d height = %d", lang, size[0], size[1], ov, got)
				}
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
		summary: model.Summary{ID: "x", Provider: "pi", Title: long},
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
	if !strings.Contains(header, txt.scopeThis) || !strings.Contains(header, "/repo") {
		t.Fatalf("header hides project scope: %q", header)
	}
	m.sessions.SetItems(nil)
	if empty := ansi.Strip(m.emptySessionsView()); !strings.Contains(empty, txt.emptyProject) {
		t.Fatalf("empty view = %q", empty)
	}
}

// helpActions is every action the ? overlay would offer for the selected
// session. It reads the groups rather than the rendered box because the
// question here is which actions are advertised at all, and a short terminal
// scrolls the panel rather than dropping one.
func helpActions(m modelState) string {
	left, right := m.keyHelpColumns()
	var b strings.Builder
	for _, set := range [][]keyGroup{left, right} {
		for _, g := range set {
			for _, row := range g.rows {
				b.WriteString(row.key + "\t" + row.label + "\n")
			}
		}
	}
	return b.String()
}

// The keymap advertises an action only where the selected agent implements it
// natively. This moved out of the footer when the footer became a fixed line,
// but it is the same contract: another never offers a lifecycle action it
// would have to emulate.
func TestHelpShowsOnlySelectedAgentCapabilities(t *testing.T) {
	m := layoutTestModel()
	if help := helpActions(m); !strings.Contains(help, txt.helpListRename) || !strings.Contains(help, txt.helpListArchive) || !strings.Contains(help, txt.helpListDelete) {
		t.Fatalf("Codex keys hide supported actions: %q", help)
	}

	it := m.sessions.SelectedItem().(sessionItem)
	it.summary.Provider = "agy"
	m.sessions.SetItems([]list.Item{it})
	if help := helpActions(m); strings.Contains(help, txt.helpListArchive) {
		t.Fatalf("Antigravity keys advertise an archive it keeps no state for: %q", help)
	} else if !strings.Contains(help, txt.helpListRename) || !strings.Contains(help, txt.helpListDelete) {
		t.Fatalf("Antigravity keys hide supported rename and delete: %q", help)
	}

	it.summary.Provider = "qwen"
	m.sessions.SetItems([]list.Item{it})
	if help := helpActions(m); strings.Contains(help, txt.helpListRelocate) {
		t.Fatalf("Qwen Code keys advertise a relocate it has no native move for: %q", help)
	} else if !strings.Contains(help, txt.helpListRename) || !strings.Contains(help, txt.helpListArchive) || !strings.Contains(help, txt.helpListDelete) {
		t.Fatalf("Qwen Code keys hide supported rename, archive and delete: %q", help)
	}
}

// Grouping empties the path column — every row under a band shares the band's
// directory — which is the largest the leftover width ever gets. The margin is
// measured from the band, so what no column can use ends the row early instead
// of starting it late, and the bands line up with the sessions beneath them.
func TestGroupedRowsStartAtTheLeftEdge(t *testing.T) {
	m := sampleModel(t, 132, 30)
	m.groupMode = groupTree
	m.ungrouped = sampleSessions()
	m.sessions.SetItems(m.groupedItems())
	m.layout()
	for _, row := range sessionRows(m.View()) {
		if row == "" {
			continue
		}
		if indent := len(row) - len(strings.TrimLeft(row, " ")); indent > rowGutterWidth+2 {
			t.Fatalf("a grouped row starts %d cells in: %q", indent, row)
		}
	}
}

// The header drops whole pieces rather than cutting one in half. It used to
// switch layouts at a fixed width, and the line it switched to was longer than
// that width in some languages — so the truncation landed on the target chip,
// which is the only thing in the header that names a key.
func TestHeaderDropsPiecesRatherThanTruncating(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		previous := applyLanguage(lang)
		for width := 40; width <= 200; width++ {
			m := sampleModel(t, width, 24)
			header := ansi.Strip(strings.Split(m.headerView(), "\n")[0])
			if got := ansi.StringWidth(header); got > m.bandWidth() {
				t.Fatalf("%s %d: header is %d cells, past the band at %d: %q",
					lang, width, got, m.bandWidth(), header)
			}
			// The target chip names the → key. Whatever else the header gives
			// up, it keeps that, whole.
			if !strings.Contains(header, ansi.Strip(txt.targetArrow)) {
				t.Fatalf("%s %d: header lost the target chip: %q", lang, width, header)
			}
		}
		applyLanguage(previous)
	}
}

// The position follows the cursor. A list of 361 with no idea where you are in
// it was the complaint; a number that does not move would be the same one.
func TestHeaderPositionFollowsTheCursor(t *testing.T) {
	m := sampleModel(t, 132, 32)
	first := ansi.Strip(m.headerView())
	if !strings.Contains(first, "1/") {
		t.Fatalf("header does not open at the first row: %q", first)
	}
	m.sessions.Select(4)
	fifth := ansi.Strip(m.headerView())
	if !strings.Contains(fifth, "5/") {
		t.Fatalf("header did not follow the cursor to row 5: %q", fifth)
	}
}

// One page is capped, so the header says both numbers when they differ. It
// used to print the total alone, which promised a list that could not be
// scrolled to.
func TestHeaderSaysWhenAPageIsCapped(t *testing.T) {
	m := sampleModel(t, 132, 32)
	m.totalSessions = 361
	if got := m.listPosition(); !strings.Contains(got, "361") || !strings.Contains(got, "10") {
		t.Fatalf("a capped page reads %q, want both the page and the total", got)
	}
	m.totalSessions = len(m.sessions.Items())
	if got := m.listPosition(); strings.Count(got, "10") != 1 {
		t.Fatalf("an uncapped page reads %q, want the total said once", got)
	}
}

// The keymap exists because keys were being hidden, so a keymap that runs off
// the bottom or the side of the screen is the same bug wearing a border. The
// browser supports terminals down to 40x12, where the full list cannot fit at
// any layout — there it must scroll, not overflow.
func TestHelpOverlayFitsEverySupportedTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		previous := applyLanguage(lang)
		for _, height := range []int{12, 14, 20, 24, 32, 50} {
			for _, width := range []int{40, 60, 80, 100, 132, 200} {
				m := sampleModel(t, width, height)
				m.overlay = overlayHelp
				m.layout()
				view := m.View()
				if got := lipgloss.Height(view); got > height {
					t.Errorf("%s %dx%d: the keymap is %d lines tall", lang, width, height, got)
				}
				for i, line := range strings.Split(view, "\n") {
					if got := ansi.StringWidth(line); got > width {
						t.Errorf("%s %dx%d: line %d is %d cells wide", lang, width, height, i, got)
					}
				}
			}
		}
		applyLanguage(previous)
	}
}

// Scrolling stops where the keymap ends. An offset that ran past it would show
// a panel that answers ↑ with nothing.
func TestHelpScrollStopsAtTheEnd(t *testing.T) {
	m := sampleModel(t, 80, 14)
	m.overlay = overlayHelp
	m.layout()
	limit := m.helpMaxScroll()
	if limit == 0 {
		t.Fatal("a 14-line terminal should not fit the whole keymap")
	}
	for i := 0; i < limit+10; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(modelState)
	}
	if m.helpOffset != limit {
		t.Fatalf("scrolled to %d, past the last line at %d", m.helpOffset, limit)
	}
	for i := 0; i < limit+10; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = updated.(modelState)
	}
	if m.helpOffset != 0 {
		t.Fatalf("scrolling back left the keymap at %d", m.helpOffset)
	}
}

// ? opens the keymap and closes it again, and the list is exactly where it was.
func TestHelpKeyTogglesTheOverlay(t *testing.T) {
	m := sampleModel(t, 100, 32)
	m.sessions.Select(3)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	opened := updated.(modelState)
	if opened.overlay != overlayHelp {
		t.Fatalf("? did not open the keymap: overlay %d", opened.overlay)
	}
	updated, _ = opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	closed := updated.(modelState)
	if closed.overlay != overlayNone {
		t.Fatalf("? did not close the keymap: overlay %d", closed.overlay)
	}
	if closed.sessions.Index() != 3 {
		t.Fatalf("the keymap moved the cursor to %d", closed.sessions.Index())
	}
}

// The footer is a fixed line now, and the whole point of fixing it is that it
// survives an ordinary terminal intact. Assembled from capabilities it reached
// 175 cells and was cut at 100 — losing search and batch, which are not
// discoverable anywhere else.
func TestFooterFitsAnOrdinaryTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		previous := applyLanguage(lang)
		m := layoutTestModel()
		m.width, m.height = 80, 24
		m.layout()
		footer := ansi.Strip(m.help())
		if got := ansi.StringWidth(footer); got > 80 {
			t.Errorf("%s footer is %d cells, past an 80-column terminal: %q", lang, got, footer)
		}
		for _, key := range []string{"/", "?"} {
			if !strings.Contains(footer, key) {
				t.Errorf("%s footer does not name %q: %q", lang, key, footer)
			}
		}
		applyLanguage(previous)
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
	if !strings.Contains(source, txt.sourceModalTitle) || !strings.Contains(target, txt.targetModalTitle) {
		t.Fatal("pickers lost their purpose labels")
	}
	if source == base || target == base || source == target {
		t.Fatal("source and target pickers must be distinct overlays")
	}
	for _, view := range []string{source, target} {
		var modalLine string
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, txt.sourceModalTitle) || strings.Contains(line, txt.targetModalTitle) {
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
	view := ansi.Strip(updated.(modelState).sessions.View())
	code := strings.Index(view, agentCode("codex"))
	if code < 0 {
		t.Fatalf("row does not name its agent at all: %q", view)
	}
	if code >= strings.Index(view, "A useful title") {
		t.Fatalf("source provider still trails the title: %q", view)
	}
}

func TestMessageCountHasUnit(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := updated.(modelState).sessions.View()
	if !strings.Contains(view, fmt.Sprintf(txt.messageCountFmt, 12)) {
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
	if !strings.Contains(header, "│    "+txt.targetArrow) {
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
		if !strings.Contains(header, strings.TrimSpace(txt.sourceArrow)) || !strings.Contains(header, txt.targetArrow) {
			t.Fatalf("source %q lost a direction control: %q", source.name, header)
		}
	}
}

// Sessions are listed one to a line at every size. A wide terminal once put a
// blank line between them, which was worth its cost while a row was stretched
// across the whole window and the eye needed help staying on one; a row is now
// a compact band of columns, and the line only bought rhythm with half the
// sessions on screen.
// A row is one line, and whether a blank line follows it depends on how much
// height there is to spend. Fifty adjacent rows in a bounded band read as a
// wall — every row the same weight, nothing marking where a record ends — but
// on a short terminal that blank line costs half the sessions, which is the
// one thing this screen exists to show.
func TestRowSpacingIsSpentOnlyWhereThereIsHeight(t *testing.T) {
	items := []list.Item{
		sessionItem{summary: model.Summary{ID: "one", Provider: "codex", Title: "First title"}},
		sessionItem{summary: model.Summary{ID: "two", Provider: "pi", Title: "Second title"}},
	}
	cases := []struct {
		w, h     int
		distance int
		why      string
	}{
		{60, 16, 1, "a narrow terminal keeps rows adjacent"},
		{100, 24, 2, "a terminal with height to spare separates records"},
		{160, 40, 2, "so does a wide one"},
		{240, 50, 2, "and an ultrawide one"},
		{100, 13, 1, "a short terminal spends every line on sessions"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			m := layoutTestModel()
			m.sessions.SetItems(items)
			m.width, m.height = tc.w, tc.h
			m.layout()
			lines := strings.Split(ansi.Strip(m.sessions.View()), "\n")
			first, second := lineContaining(lines, "First title"), lineContaining(lines, "Second title")
			if first < 0 || second-first != tc.distance {
				t.Fatalf("%dx%d row distance = %d, want %d: %q", tc.w, tc.h, second-first, tc.distance, lines)
			}
		})
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
	// The row already shows the project and the session ID. A wide idle
	// footer that repeats them spends its one line on what is on screen
	// twice; the assertion is on the metadata, not on any word that happens
	// to appear in a key hint.
	want := ansi.Strip(ansi.Truncate(footerStyle.Render(m.help()), m.width, "…"))
	if footer != want {
		t.Fatalf("wide idle footer is not just the controls:\n got %q\nwant %q", footer, want)
	}
	if strings.Contains(footer, "/tmp/project") {
		t.Fatalf("wide footer repeats the row's project: %q", footer)
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
	box := sourceModalStyle.Render(accentStyle.Render(txt.sourceModalTitle) + "\n" +
		mutedStyle.Render(txt.sourceModalHint) + "\n\n" + m.sourceList.View())
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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("a did not start native archive")
	}
	summary := m.sessions.SelectedItem().(sessionItem).summary
	updated, _ = m.Update(archiveDoneMsg{summary: summary, archived: true})
	got := updated.(modelState)
	if got.lastArchived == nil || got.help() != txt.helpArchived {
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
	if strings.Contains(got.View(), txt.suggestionLoading) {
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
	if fail.suggesting || !strings.Contains(fail.View(), strings.TrimSpace(txt.suggestionFailed)) {
		t.Fatal("a failed suggestion is not reported in the rename box")
	}

	closed, _ := fail.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s := closed.(modelState); s.suggestErr != "" || s.suggestFor != "" {
		t.Fatalf("suggestion state survived the overlay: %+v", s.suggestErr)
	}
}

func TestRenameOverlayWithSuggestionFitsTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
			m := layoutTestModel()
			m.overlay = overlayRename
			m.suggestion = "0903｜修复｜删除条目快捷键冲突"
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := updated.(modelState).View()
			if got := lipgloss.Width(view); got > size[0] {
				t.Errorf("%s %dx%d width = %d", lang, size[0], size[1], got)
			}
			if got := lipgloss.Height(view); got > size[1] {
				t.Errorf("%s %dx%d height = %d", lang, size[0], size[1], got)
			}
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
	if !strings.Contains(got.View(), txt.deleteConfirmTitle) || !strings.Contains(got.View(), txt.choiceCancel) {
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
	defer func() { _ = idx.Close() }()
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
	if got.overlay == overlayDelete || got.err != txt.cannotDeleteRunning {
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

func TestShiftXMarksEveryVisibleRowAndClearsOnRepeat(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	updated, _ := m.Update(markKey('X'))
	got := updated.(modelState)
	if len(got.marked) != len(got.sessions.Items()) {
		t.Fatalf("X did not mark every visible row: %d of %d", len(got.marked), len(got.sessions.Items()))
	}

	updated, _ = got.Update(markKey('X'))
	if again := updated.(modelState); len(again.marked) != 0 {
		t.Fatalf("X did not clear a fully marked page: %v", again.marked)
	}
}

// Archive and select-all used to sit on a and A: two unrelated actions one
// Shift apart, and the footer named only the archive. Shift now means one
// thing, the same verb over the whole page, and the footer says so.
func TestShiftOnlyWidensTheSameVerb(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	help := helpActions(m)
	for _, key := range []string{txt.helpListArchive, "x\t", "X\t"} {
		if !strings.Contains(help, key) {
			t.Errorf("the keymap does not name %q: %q", key, help)
		}
	}

	// The freed key must do nothing at all: a row silently marked by a stray
	// capital is how the old pair went wrong in the first place.
	updated, cmd := m.Update(markKey('A'))
	if got := updated.(modelState); len(got.marked) != 0 || got.loading || cmd != nil {
		t.Fatalf("A still acts: marked=%v loading=%v cmd=%v", got.marked, got.loading, cmd)
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
		sessionItem{summary: sm},
		sessionItem{summary: model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project"}},
	})
	if !got.marked["session"] {
		t.Fatalf("the mark did not survive a page reload: %v", got.marked)
	}
	if got.marked["other"] {
		t.Fatal("the mark moved to a different session")
	}
}

// The project cell is composed from a bar, a gap, and two differently styled
// path halves, so its width is easy to get wrong by one cell — which would
// push every column after it out of alignment on that row only.
func TestProjectCellFillsExactlyItsColumn(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	paths := []string{
		"/Users/mingjian/Documents/sync/GitHub/another",
		"/tmp/p",
		"~/x",
		"/Users/mingjian/Documents/sync/GitHub/一个很长的中文项目名称目录",
		"",
	}
	for _, path := range paths {
		for width := 3; width <= 28; width++ {
			if got := ansi.StringWidth(renderProjectCell(path, width)); got != width {
				t.Errorf("project cell width for %q at %d = %d", path, width, got)
			}
		}
	}
}

// The last path segment is the part being read, so it must survive truncation
// even when the parent path cannot.
func TestProjectCellKeepsTheLastSegment(t *testing.T) {
	cell := ansi.Strip(renderProjectCell("/Users/mingjian/Documents/sync/GitHub/another", 20))
	if !strings.Contains(cell, "another") {
		t.Fatalf("project cell dropped its leaf: %q", cell)
	}
}

// When every row would repeat the same path, that width belongs to the title
// instead. Whether it would is decided in applySessionDelegate; this checks
// that the flag reaches the renderer.
func TestProjectColumnFollowsScope(t *testing.T) {
	render := func(showProject bool) string {
		marked := map[string]bool{}
		item := sessionItem{
			summary: model.Summary{
				ID: "session", Provider: "codex", Title: "A useful title",
				ProjectPath: "/tmp/scope-fixture", MessageCount: 3,
			},
		}
		d := sessionDelegate{marked: marked, showProject: showProject}
		l := newBareList([]list.Item{item}, d, 120, 4)
		var buf strings.Builder
		d.Render(&buf, l, 0, item)
		return ansi.Strip(buf.String())
	}
	if got := render(false); strings.Contains(got, "scope-fixture") {
		t.Fatalf("project-scoped row still shows the project column: %q", got)
	}
	if got := render(true); !strings.Contains(got, "scope-fixture") {
		t.Fatalf("global row lost the project column: %q", got)
	}
}

// A project scope covers one repository, not one directory: its registered
// worktrees and, outside Git, everything under the current directory all land
// in it. Hiding the path there left two sessions from two worktrees looking
// identical, so the column follows the rows rather than the scope flag.
func TestProjectColumnReturnsWhenAProjectSpansDirectories(t *testing.T) {
	root := "/tmp/another-scope-fixture"
	worktree := root + "/.worktrees/delete-undo"
	rowsIn := func(paths ...string) modelState {
		m := layoutTestModel()
		m.width, m.height = 120, 40
		m.cwd, m.projectScope = root, util.ProjectScope{CWD: root, Root: root, Git: true}
		m.projectOnly = true
		items := make([]list.Item, 0, len(paths))
		for i, path := range paths {
			items = append(items, sessionItem{summary: model.Summary{
				ID: fmt.Sprint(i), Provider: "codex", Title: "A useful title", ProjectPath: path,
			}})
		}
		m.sessions.SetItems(items)
		m.layout()
		return m
	}

	single := rowsIn(root, root)
	if sessionDelegateFor(&single).showProject {
		t.Fatal("a project living in one directory still spends width on the path")
	}

	multi := rowsIn(root, worktree)
	spread := sessionDelegateFor(&multi)
	if !spread.showProject {
		t.Fatal("a project spanning two worktrees hides the only thing telling them apart")
	}
	if spread.projectBase != root {
		t.Fatalf("projectBase = %q, want the project root", spread.projectBase)
	}

	// The global scope is unchanged: full paths, always shown.
	global := rowsIn(root, root)
	global.projectOnly = false
	if d := sessionDelegateFor(&global); !d.showProject || d.projectBase != "" {
		t.Fatalf("global scope changed: showProject=%v base=%q", d.showProject, d.projectBase)
	}
}

// The rendered row is what the user reads, so the two worktrees have to be
// distinguishable in it — and by the segment that differs, not by a prefix.
func TestProjectColumnShowsWorktreesApart(t *testing.T) {
	root := "/tmp/another-scope-fixture"
	marked := map[string]bool{}
	d := sessionDelegate{marked: marked, showProject: true, projectBase: root}
	render := func(path string) string {
		item := sessionItem{summary: model.Summary{
			ID: path, Provider: "codex", Title: "Same title in both trees", ProjectPath: path,
		}}
		l := newBareList([]list.Item{item}, d, 120, 4)
		var buf strings.Builder
		d.Render(&buf, l, 0, item)
		return ansi.Strip(buf.String())
	}
	atRoot, inWorktree := render(root), render(root+"/.worktrees/delete-undo")
	if atRoot == inWorktree {
		t.Fatal("two worktrees render the same row")
	}
	if !strings.Contains(inWorktree, ".worktrees/delete-undo") {
		t.Fatalf("the worktree row does not name its worktree: %q", inWorktree)
	}
	if strings.Contains(inWorktree, "another-scope-fixture") {
		t.Fatalf("the row repeats the shared project root: %q", inWorktree)
	}
	if !strings.Contains(atRoot, filepath.Base(root)) {
		t.Fatalf("the root row has nothing in the column: %q", atRoot)
	}
}

// A column sized to its cap spent a quarter of the pane on one repeated name
// while the title — the only thing that identifies a session — was cut. It is
// sized to the longest path it actually has to show instead.
func TestProjectColumnTakesOnlyTheWidthItsPathsNeed(t *testing.T) {
	root := "/tmp/another-scope-fixture"
	marked := map[string]bool{}
	// A title long enough to be cut at either column width, so how much of it
	// survives is the measure of what the column gave back.
	title := strings.Repeat("x", 120)
	items := []list.Item{
		sessionItem{summary: model.Summary{ID: "a", Provider: "codex", Title: title, ProjectPath: root}},
		sessionItem{summary: model.Summary{ID: "b", Provider: "codex", Title: title, ProjectPath: root + "/pkg"}},
	}
	want := ansi.StringWidth("another-scope-fixture") + projectChipPad
	if got := projectColumnWidth(items, root, false, nil); got != want {
		t.Fatalf("projectColumnWidth = %d, want %d", got, want)
	}

	render := func(projectW int) string {
		d := sessionDelegate{marked: marked, showProject: true, projectBase: root, projectW: projectW}
		l := newBareList(items, d, 120, 4)
		var buf strings.Builder
		d.Render(&buf, l, 0, items[0])
		return ansi.Strip(buf.String())
	}
	fitted, capped := render(want), render(0)
	if strings.Count(fitted, "x") <= strings.Count(capped, "x") {
		t.Fatalf("a fitted column did not give the title back any width:\n%q\n%q", fitted, capped)
	}
	if !strings.Contains(fitted, "another-scope-fixture") {
		t.Fatalf("the fitted column lost the path it was sized for: %q", fitted)
	}
}

// Bubbletea erases only the tail of a line it believes is shorter than the
// terminal, so a frame drawn for the old size can survive under the new one.
// Both screens ask for a clear when the size changes.
func TestResizeAsksForAClearScreen(t *testing.T) {
	m := layoutTestModel()
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if cmd == nil {
		t.Fatal("the browser did not repaint on resize")
	}
	if cmd() != tea.ClearScreen() {
		t.Fatalf("the browser repaint is not a clear: %T", cmd())
	}

	s := setupFixture()
	_, cmd = s.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if cmd == nil {
		t.Fatal("setup did not repaint on resize")
	}
	if cmd() != tea.ClearScreen() {
		t.Fatalf("the setup repaint is not a clear: %T", cmd())
	}
}

// nerdWidth models the terminal this has to survive: ambiguous characters stay
// narrow, which is what both Ghostty and the app's own width table assume, but
// a private-use glyph from a Nerd Font is drawn two cells wide while every
// width table can only call it one.
func nerdWidth(s string) int {
	narrow := runewidth.NewCondition()
	narrow.EastAsianWidth = false
	w := narrow.StringWidth(s)
	for _, r := range s {
		if unicode.In(r, unicode.Co) {
			w++
		}
	}
	return w
}

// The row that broke the browser in a folder holding several projects: a Qwen
// session whose title was a captured Nerd Font prompt. The glyphs measured one
// cell and drew two, so the row escaped its pane, the terminal wrapped it, and
// every frame after that landed one line off — the previous frame stayed on
// screen underneath.
func TestPromptGlyphsCannotWidenARow(t *testing.T) {
	marked := map[string]bool{}
	items := []list.Item{
		sessionItem{summary: model.Summary{
			ID: "prompt", Provider: "qwen", MessageCount: 134,
			Title:       "\ue0b6\U000f0035 mingjian \ue0b0 ~/\U000f0219 /sync \ue0b0\ue0b0\ue0b0 \uf43a 02:03 \ue0b4 \uf432 codex Error loading",
			ProjectPath: "/Users/mingjian/Documents/sync",
		}},
		sessionItem{summary: model.Summary{
			ID: "plain", Provider: "codex", MessageCount: 12,
			Title: "A useful title", ProjectPath: "/tmp/project",
		}},
	}

	for w := 40; w <= 200; w++ {
		for _, showProject := range []bool{false, true} {
			l := newBareList(items, sessionDelegate{marked: marked, showProject: showProject}, w, 4)
			for _, line := range strings.Split(l.View(), "\n") {
				if got := nerdWidth(ansi.Strip(line)); got > w {
					t.Fatalf("width %d project=%v: row draws %d cells: %q",
						w, showProject, got, ansi.Strip(line))
				}
			}
		}
	}
}

// A rename that reached the agent's own store but not one of the surfaces that
// agent reads is not a failed rename. Reporting it as one sends the person to
// redo something that already happened; hiding it leaves them staring at a
// sidebar that still shows the old name.
func TestPartialRenameReportsTheCaveatAndKeepsTheRename(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	caveat := fmt.Errorf("%w: Codex Desktop is running, so its sidebar keeps the old name until it restarts",
		provider.ErrPartial)
	updated, cmd := m.Update(renameDoneMsg{providerID: "codex", title: "0831｜文档｜月度PBC总结润色", caveat: caveat})
	got := updated.(modelState)
	if got.err != "" {
		t.Fatalf("a caveat was reported as a failure: %q", got.err)
	}
	if cmd == nil {
		t.Fatal("the list did not reload after a rename that landed")
	}
	status := ansi.Strip(got.status)
	if !strings.Contains(status, strings.TrimSpace(txt.renamedPrefix)) {
		t.Fatalf("the rename is not reported as done: %q", status)
	}
	if !strings.Contains(status, "Codex Desktop is running") {
		t.Fatalf("the caveat is not in the status line: %q", status)
	}
	if strings.Contains(status, provider.ErrPartial.Error()) {
		t.Fatalf("the sentinel leaked into the status line: %q", status)
	}
}

// The undo is offered only when the provider handed back a way to honour it.
// An offer another cannot keep is worse than no offer at all.
func TestDeleteOffersUndoOnlyWhenTheProviderCanHonourIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		restore  provider.SessionRestore
		wantUndo bool
	}{
		{name: "provider owns the bytes", restore: func(context.Context) error { return nil }, wantUndo: true},
		{name: "server owns the deletion", restore: nil, wantUndo: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := layoutTestModel()
			m.width, m.height = 100, 30
			m.layout()
			updated, _ := m.Update(deleteDoneMsg{providerID: "pi", title: "gone", restore: tc.restore})
			got := updated.(modelState)
			if tc.wantUndo {
				if got.lastDeleted == nil || got.restoreDeleted == nil {
					t.Fatal("a reversible delete did not offer the undo")
				}
				if got.help() != txt.helpDeleted || !strings.Contains(got.status, strings.TrimSpace(txt.undoDeleteHint)) {
					t.Fatalf("the undo is not visible: help=%q status=%q", got.help(), got.status)
				}
				return
			}
			if got.lastDeleted != nil || got.restoreDeleted != nil {
				t.Fatal("a delete another cannot take back was offered as undoable")
			}
			if got.help() == txt.helpDeleted {
				t.Fatal("the footer promises an undo the provider cannot honour")
			}
		})
	}
}

func TestUndoDeleteRestoresAndThenStopsOffering(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(deleteDoneMsg{providerID: "pi", title: "gone",
		restore: func(context.Context) error { return nil }})
	got := updated.(modelState)
	// The delete refreshes the list; the undo becomes pressable once it lands.
	got.loading = false

	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("u did not start the restore")
	}

	updated, _ = updated.(modelState).Update(restoreDoneMsg{title: "gone"})
	after := updated.(modelState)
	after.loading = false
	if after.lastDeleted != nil || after.restoreDeleted != nil {
		t.Fatal("the undo stayed armed after it was spent")
	}
	if !strings.Contains(after.status, strings.TrimSpace(txt.restoredPrefix)) {
		t.Fatalf("restore was not reported: %q", after.status)
	}
	// A second press must do nothing rather than replay the restore.
	_, cmd = after.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if cmd != nil {
		t.Fatal("u fired again after the undo was already used")
	}
}

// esc is how the person says "I meant it". The captured session is dropped and
// the delete becomes final, exactly like esc after an archive.
func TestEscKeepsTheDeleteAndDropsTheUndo(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(deleteDoneMsg{providerID: "pi", title: "gone",
		restore: func(context.Context) error { return nil }})
	armed := updated.(modelState)
	armed.loading = false
	updated, _ = armed.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(modelState)
	if got.lastDeleted != nil || got.restoreDeleted != nil {
		t.Fatal("esc left the undo armed")
	}
	if _, cmd := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}}); cmd != nil {
		t.Fatal("u restored a delete the person chose to keep")
	}
}

// The confirmation must promise what that specific agent can deliver: pi owns
// its session file, OpenCode 2's server owns the deletion outright.
func TestDeleteModalPromisesUndoOnlyWhereItIsReal(t *testing.T) {
	for _, tc := range []struct {
		providerID string
		want       string
		reject     string
	}{
		{providerID: "pi", want: txt.deleteConfirmBodyUndo, reject: txt.deleteConfirmBody},
		{providerID: "opencode2", want: txt.deleteConfirmBody, reject: txt.deleteConfirmBodyUndo},
	} {
		t.Run(tc.providerID, func(t *testing.T) {
			m := layoutTestModel()
			item := m.sessions.SelectedItem().(sessionItem)
			item.summary.Provider = tc.providerID
			m.selected = &item
			m.overlay = overlayDelete
			m.width, m.height = 100, 30
			m.layout()
			view := m.View()
			if !strings.Contains(view, firstLine(tc.want)) {
				t.Fatalf("%s delete modal does not state its real promise", tc.providerID)
			}
			if strings.Contains(view, firstLine(tc.reject)) {
				t.Fatalf("%s delete modal states the wrong promise", tc.providerID)
			}
		})
	}
}

// The bodies wrap, so compare on the part that differs and survives wrapping.
func firstLine(body string) string {
	if i := strings.Index(body, "agent. "); i >= 0 {
		rest := body[i+len("agent. "):]
		if j := strings.IndexByte(rest, ' '); j > 0 {
			return rest[:j]
		}
		return rest
	}
	return body
}

// The undo promise is a longer sentence than the final one, in both languages.
// It has to fit the same terminals the rest of the delete modal fits.
func TestReversibleDeleteModalFitsTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
			m := layoutTestModel()
			item := m.sessions.SelectedItem().(sessionItem)
			item.summary.Provider = "pi"
			m.selected = &item
			m.overlay = overlayDelete
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := updated.(modelState).View()
			if got := lipgloss.Width(view); got > size[0] {
				t.Errorf("%s %dx%d width = %d", lang, size[0], size[1], got)
			}
			if got := lipgloss.Height(view); got > size[1] {
				t.Errorf("%s %dx%d height = %d", lang, size[0], size[1], got)
			}
		}
	}
}

// A session older than the relative range prints an absolute date, and that
// date is wider than any relative stamp. The column was a fixed ten cells, so
// "Sep 30, 2026" arrived as "Sep 30, 20" and the year — the only part that
// matters once a session is this old — was the part that went.
func TestTimeColumnKeepsTheYearOfAnOldSession(t *testing.T) {
	old := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC).AddDate(-1, 0, 0)
	stamp := util.FormatRelative(old)
	if strings.HasSuffix(stamp, "ago") {
		t.Fatalf("fixture is not old enough to render an absolute date: %q", stamp)
	}

	m := layoutTestModel()
	m.width, m.height = 140, 40
	m.sessions.SetItems([]list.Item{
		sessionItem{summary: model.Summary{ID: "old", Provider: "codex", Title: "An old session", UpdatedAt: old}},
	})
	m.sessions.SetSize(m.width, m.height)

	rendered := sessionDelegateFor(&m)
	if got := rendered.timeW; got < ansi.StringWidth(stamp) {
		t.Fatalf("time column measured %d cells for a %d-cell stamp %q", got, ansi.StringWidth(stamp), stamp)
	}

	var buf strings.Builder
	rendered.Render(&buf, m.sessions, 0, m.sessions.Items()[0])
	if !strings.Contains(buf.String(), stamp) {
		t.Fatalf("row lost part of %q: %q", stamp, buf.String())
	}
}

// One session must not set the spacing of the whole list. The gaps were what
// the columns left over, and the title claimed the widest title in the list
// before they were measured — so a single un-renamed session carrying its own
// first message as a title (138 cells of it) collapsed every gap to one space.
// The same sessions, listed under a scope that did not happen to include that
// row, were laid out with room to breathe. Nothing about the sessions differed.
func TestOneLongTitleDoesNotCollapseTheSpacing(t *testing.T) {
	const width = 140
	ordinary := "0909｜Fix｜Read every agent's sessions on a first install"
	outlier := strings.Repeat("我", 69) // 138 cells, the real one from the index

	render := func(titles ...string) string {
		items := make([]list.Item, 0, len(titles))
		for i, title := range titles {
			items = append(items, sessionItem{summary: model.Summary{
				ID: fmt.Sprintf("s%d", i), Provider: "codex", Title: title,
			}})
		}
		d := sessionDelegate{marked: map[string]bool{}, titleW: titleColumnWidth(items)}
		l := newBareList(items, d, width, 8)
		var buf strings.Builder
		d.Render(&buf, l, 0, items[0])
		return ansi.Strip(buf.String())
	}

	alone := render(ordinary, ordinary)
	beside := render(ordinary, outlier)

	gapOf := func(row string) int {
		// The run of spaces between the agent chip and the title is the gap
		// every other column is spaced by too.
		idx := strings.Index(row, "CDX")
		if idx < 0 {
			t.Fatalf("no agent chip in %q", row)
		}
		rest := row[idx+len("CDX"):]
		return len(rest) - len(strings.TrimLeft(rest, " "))
	}

	if got, want := gapOf(beside), gapOf(alone); got != want {
		t.Fatalf("one long title changed the spacing of a row it is not in: gap = %d beside it, %d without it", got, want)
	}
	// And the spacing is the roomy one, not the collapsed one both could agree on.
	if got := gapOf(alone); got < 2 {
		t.Fatalf("gap = %d: a band this wide has room to space its columns", got)
	}
}

// Choosing a source must not take the directory column away. A project that
// spans worktrees has earned the column; filtering to an agent whose sessions
// all sit in the root leaves one directory on screen, and reading the column
// off those rows made it vanish — moving every column beside it while someone
// was reading, which is the thing the width rule already refuses to do.
func TestProjectColumnSurvivesASourceFilter(t *testing.T) {
	root := "/Users/mingjian/Documents/sync/GitHub/another"
	m := layoutTestModel()
	m.width, m.height = 140, 40
	m.projectOnly = true
	m.projectScope.Root = root
	// What the scope holds: the root plus a worktree under it.
	m.scopeProjects = 2
	// What this page holds after filtering to one agent: the root only.
	m.sessions.SetItems([]list.Item{
		sessionItem{summary: model.Summary{ID: "one", Provider: "agy", Title: "First", ProjectPath: root}},
		sessionItem{summary: model.Summary{ID: "two", Provider: "agy", Title: "Second", ProjectPath: root}},
	})
	m.layout()

	if got := sessionDelegateFor(&m); !got.showProject {
		t.Fatal("a source filter hid the directory column the project had earned")
	}

	// A project that really is one directory still keeps the column away.
	m.scopeProjects = 1
	if got := sessionDelegateFor(&m); got.showProject {
		t.Fatal("a single-directory project drew a column with nothing to say")
	}
}

// Changing scope must not slide the row sideways. The leftover width was split
// evenly around the row, so a scope whose paths are long and the same scope
// narrowed to one project — `~/Documents/sync/Docs` against `Docs` — differed
// by the whole width of the directory column, and half of that arrived as left
// margin. Pressing `f` moved every column on screen by fifteen cells.
func TestScopeToggleDoesNotSlideTheRow(t *testing.T) {
	const width = 231
	base := "/Users/mingjian/Documents/sync/Docs"
	title := "0909｜探索｜两个Google账号领取免费Pro至明年9月"

	agentColumnAt := func(paths []string, projectBase string) int {
		items := make([]list.Item, 0, len(paths))
		for i, p := range paths {
			items = append(items, sessionItem{summary: model.Summary{
				ID: fmt.Sprint(i), Provider: "codex", Title: title, ProjectPath: p,
			}})
		}
		d := sessionDelegate{
			marked: map[string]bool{}, showProject: true, projectBase: projectBase,
			projectW: projectColumnWidth(items, projectBase, false, nil),
			titleW:   titleColumnWidth(items),
		}
		l := newBareList(items, d, width, 8)
		var buf strings.Builder
		d.Render(&buf, l, 0, items[0])
		row := ansi.Strip(buf.String())
		at := strings.Index(row, "CDX")
		if at < 0 {
			t.Fatalf("no agent chip in %q", row)
		}
		return ansi.StringWidth(row[:at])
	}

	all := agentColumnAt([]string{
		"/Users/mingjian/Documents/sync/Work/huatu/projects/ai-pioneer",
		"/Users/mingjian/Documents/sync/Tuning",
	}, "")
	scoped := agentColumnAt([]string{base + "/health", base + "/finance/trading"}, base)

	if all != scoped {
		t.Fatalf("scope moved the agent column: %d in the whole index, %d in one project", all, scoped)
	}
}
