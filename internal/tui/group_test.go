package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/util"
)

const (
	groupRoot     = "/repo"
	groupLayout   = "/repo/.worktrees/list-layout"
	groupBugfix   = "/repo/.worktrees/bugfix"
	groupOutsider = "/tmp/another-hotfix"
)

func groupWorktrees() []string {
	return []string{groupRoot, groupLayout, groupBugfix, groupOutsider}
}

// groupModel is a browser scoped to one repository with four registered trees,
// which is the state grouping exists for.
func groupModel(rows ...list.Item) modelState {
	m := layoutTestModel()
	m.cwd = groupRoot
	m.projectOnly = true
	m.projectScope = util.ProjectScope{
		CWD: groupRoot, Root: groupRoot, Git: true, Worktrees: groupWorktrees(),
	}
	m.groupMode = groupTree
	m.setSessionItems(rows)
	return m
}

// groupRow builds a session that started in dir, minutes old. Age is what puts
// the trees in order, so every fixture states it.
func groupRow(id, dir string, minutes int) list.Item {
	return sessionItem{summary: model.Summary{
		ID:           id,
		Provider:     "codex",
		Title:        "title " + id,
		ProjectPath:  dir,
		UpdatedAt:    time.Now().Add(-time.Duration(minutes) * time.Minute),
		MessageCount: 10,
	}}
}

func groupLabels(items []list.Item) []string {
	var out []string
	for _, item := range items {
		if header, ok := item.(groupHeader); ok {
			out = append(out, fmt.Sprintf("%s:%d", header.path, header.count))
		}
	}
	return out
}

func sessionIDs(items []list.Item) []string {
	var out []string
	for _, item := range items {
		if row, ok := item.(sessionItem); ok {
			out = append(out, row.summary.ID)
		}
	}
	return out
}

// A session is recorded in the directory the agent ran in, which inside a
// worktree is usually a package below it. Grouping on the raw path would split
// one tree into a band per package.
func TestGroupKeyFollowsTheWorktreeItRanIn(t *testing.T) {
	cases := []struct{ name, path, want string }{
		{"the tree root itself", groupLayout, groupLayout},
		{"a package inside a tree", groupLayout + "/internal/tui", groupLayout},
		{"the main checkout", groupRoot, groupRoot},
		{"a package in the main checkout", groupRoot + "/cmd/another", groupRoot},
		{"a tree registered outside the root", groupOutsider + "/pkg", groupOutsider},
		{"a directory in no tree", "/elsewhere/notes", "/elsewhere/notes"},
		{"nothing recorded", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := groupKeyFor(tc.path, groupWorktrees()); got != tc.want {
				t.Fatalf("groupKeyFor(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// The tree holding the newest session leads, so grouping never buries the work
// just left behind — and inside a band the rows stay in recency order.
func TestGroupSessionsOrdersTreesByTheirNewestSession(t *testing.T) {
	rows := []list.Item{
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
		groupRow("c", groupLayout+"/internal/tui", 9),
		groupRow("d", groupBugfix, 20),
		groupRow("e", groupRoot, 30),
	}
	got := groupSessions(rows, groupWorktrees(), groupRoot)

	wantBands := []string{groupLayout + ":2", groupRoot + ":2", groupBugfix + ":1"}
	if labels := groupLabels(got); strings.Join(labels, " ") != strings.Join(wantBands, " ") {
		t.Fatalf("bands = %v, want %v", labels, wantBands)
	}
	wantRows := "a c b e d"
	if ids := strings.Join(sessionIDs(got), " "); ids != wantRows {
		t.Fatalf("rows = %q, want %q", ids, wantRows)
	}
	if _, ok := got[0].(groupHeader); !ok {
		t.Fatal("the list does not open on a band")
	}
}

// One band over the whole list names something every row already shares, and
// spends a line saying it.
func TestGroupSessionsLeavesASingleTreeAlone(t *testing.T) {
	rows := []list.Item{
		groupRow("a", groupLayout, 1),
		groupRow("b", groupLayout+"/internal/tui", 4),
	}
	got := groupSessions(rows, groupWorktrees(), groupRoot)
	if hasGroupHeaders(got) || len(got) != len(rows) {
		t.Fatalf("a single tree was given bands: %v", groupLabels(got))
	}
}

// Regrouping an already grouped list must produce the same list; otherwise a
// refresh landing mid-session would nest bands inside bands.
func TestGroupSessionsIsStableOverItsOwnOutput(t *testing.T) {
	rows := []list.Item{
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
	}
	once := groupSessions(rows, groupWorktrees(), groupRoot)
	twice := groupSessions(once, groupWorktrees(), groupRoot)
	if len(once) != len(twice) ||
		strings.Join(groupLabels(once), " ") != strings.Join(groupLabels(twice), " ") ||
		strings.Join(sessionIDs(once), " ") != strings.Join(sessionIDs(twice), " ") {
		t.Fatalf("regrouping changed the list: %v then %v", groupLabels(once), groupLabels(twice))
	}
}

// A band is a label, not a destination: stopping on one would make ↑↓ pause on
// a row that enter, space, and x all ignore.
func TestCursorNeverRestsOnABand(t *testing.T) {
	m := groupModel(
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
		groupRow("c", groupRoot, 8),
		groupRow("d", groupBugfix, 20),
	)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(modelState)
	if !hasGroupHeaders(m.sessions.Items()) {
		t.Fatal("the fixture is not grouped")
	}
	assertOnSession := func(step string) {
		t.Helper()
		if _, ok := m.sessions.SelectedItem().(sessionItem); !ok {
			t.Fatalf("cursor sits on a band after %s (index %d)", step, m.sessions.Index())
		}
	}
	assertOnSession("the first paint")
	for _, key := range []string{"down", "down", "down", "down", "down", "up", "up", "up", "up", "home", "end"} {
		updated, _ = m.Update(keyFor(key))
		m = updated.(modelState)
		assertOnSession(key)
	}
}

func keyFor(name string) tea.KeyMsg {
	switch name {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// Grouping is a view of the page already in hand, so the toggle keeps the
// session under the cursor rather than whatever now sits at its old index.
func TestGroupToggleKeepsTheSelectedSession(t *testing.T) {
	m := groupModel()
	m.groupMode = groupNone
	m.setSessionItems([]list.Item{
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
		groupRow("c", groupLayout, 8),
		groupRow("d", groupRoot, 20),
	})
	m.sessions.Select(3) // "d", last by recency, first tree's second row once grouped
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(modelState)

	if m.groupMode != groupTree || !hasGroupHeaders(m.sessions.Items()) {
		t.Fatal("g did not group the list")
	}
	if row, ok := m.sessions.SelectedItem().(sessionItem); !ok || row.summary.ID != "d" {
		t.Fatalf("grouping moved the cursor off its session: %#v", m.sessions.SelectedItem())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(modelState)
	if m.groupMode != groupDate {
		t.Fatalf("g did not move on to the date bands: mode %d", m.groupMode)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updated.(modelState)
	if m.groupMode != groupNone || hasGroupHeaders(m.sessions.Items()) {
		t.Fatal("g did not come back round to an ungrouped list")
	}
	if ids := strings.Join(sessionIDs(m.sessions.Items()), " "); ids != "a b c d" {
		t.Fatalf("ungrouping did not restore recency order: %q", ids)
	}
	if row, ok := m.sessions.SelectedItem().(sessionItem); !ok || row.summary.ID != "d" {
		t.Fatalf("ungrouping moved the cursor off its session: %#v", m.sessions.SelectedItem())
	}
}

// The band names the tree, so a column repeating it down every row is padding.
// It comes back the moment a row has something the band did not say.
func TestGroupedRowsDropTheColumnTheBandReplaces(t *testing.T) {
	atRoots := groupModel(
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
	)
	if sessionDelegateFor(&atRoots).showProject {
		t.Fatal("grouped rows repeat the path their band already carries")
	}

	belowRoots := groupModel(
		groupRow("a", groupLayout+"/internal/tui", 1),
		groupRow("b", groupRoot, 5),
	)
	if !sessionDelegateFor(&belowRoots).showProject {
		t.Fatal("a session below its tree lost the only thing naming where it ran")
	}

	ungrouped := groupModel(
		groupRow("a", groupLayout, 1),
		groupRow("b", groupRoot, 5),
	)
	ungrouped.groupMode = groupNone
	ungrouped.sessions.SetItems(ungrouped.groupedItems())
	if !sessionDelegateFor(&ungrouped).showProject {
		t.Fatal("an ungrouped project spanning two trees hides what tells them apart")
	}
}

// Under a band the column says what the band did not, and nothing when the band
// said it all: the package the agent ran in, not the tree it ran in.
func TestGroupedColumnSaysOnlyWhatTheBandDidNot(t *testing.T) {
	m := groupModel(
		groupRow("a", groupLayout+"/internal/tui", 1),
		groupRow("b", groupLayout, 5),
		groupRow("c", groupRoot, 8),
	)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(modelState)
	d := sessionDelegateFor(&m)

	below, shown := projectCellShown(groupLayout+"/internal/tui", d.projectBase, d.bands, d.groupRoots)
	if !shown || below != "internal/tui" {
		t.Fatalf("a row below its tree says %q (shown %v), want the package alone", below, shown)
	}
	if _, shown := projectCellShown(groupLayout, d.projectBase, d.bands, d.groupRoots); shown {
		t.Fatal("a row at its tree repeats the band above it")
	}
	// The column is measured on what it will draw, so the tree name no row
	// prints buys no width.
	if got, want := d.projectW, ansi.StringWidth("internal/tui")+projectChipPad; got != want {
		t.Fatalf("grouped column measured %d cells, want %d", got, want)
	}
}

// The band has to read as a heading at every width the browser draws, in both
// languages: it carries a path, and a path is the longest thing in the list.
func TestGroupBandFitsAndNamesItsTree(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 120, 200} {
		m := groupModel(
			groupRow("a", groupLayout, 1),
			groupRow("b", groupRoot, 5),
			groupRow("c", groupRoot, 8),
		)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = updated.(modelState)
		for _, line := range strings.Split(m.sessions.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: row is %d wide: %q", width, got, ansi.Strip(line))
			}
		}
		view := ansi.Strip(m.sessions.View())
		if width >= 60 && !strings.Contains(view, ".worktrees/list-layout") {
			t.Fatalf("width %d: band does not name its tree: %q", width, view)
		}
		if width >= 60 && !strings.Contains(view, fmt.Sprintf(txt.sessionCountFmt, 2)) {
			t.Fatalf("width %d: band does not say how many sessions it holds: %q", width, view)
		}
	}
}

// Sessions with no directory on record are still sessions. Their band cannot
// be named after a path, and an unlabelled band reads as a rendering fault.
func TestBandWithoutADirectoryStillHasAName(t *testing.T) {
	m := groupModel(
		groupRow("a", groupLayout, 1),
		groupRow("b", "", 5),
	)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(modelState)
	if view := ansi.Strip(m.sessions.View()); !strings.Contains(view, txt.groupNoProject) {
		t.Fatalf("the directoryless band has no label: %q", view)
	}
}

// TestGroupedLayoutSample prints a page of one repository's worktrees, grouped,
// at a sweep of widths. A band is a layout claim — that it reads as a heading
// rather than as a broken row — and this project settles those by looking.
//
//	LAYOUT_SAMPLE=1 go test ./internal/tui/ -run TestGroupedLayoutSample -v
func TestGroupedLayoutSample(t *testing.T) {
	if os.Getenv("LAYOUT_SAMPLE") == "" {
		t.Skip("set LAYOUT_SAMPLE=1 to print the grouped layout")
	}
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	for _, width := range sampleNumbers("LAYOUT_SAMPLE_WIDTHS", []int{100, 132, 160, 200}) {
		for _, grouped := range []int{groupNone, groupTree, groupDate} {
			m := layoutTestModel()
			m.status = ""
			m.cwd = sampleRepo
			m.projectOnly = true
			m.projectScope = util.ProjectScope{
				CWD: sampleRepo, Root: sampleRepo, Git: true, Worktrees: sampleWorktreeRoots(),
			}
			m.groupMode = grouped
			m.setSessionItems(sampleWorktreeSessions())
			m.totalSessions = len(m.ungrouped)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 32})
			m = updated.(modelState)
			fmt.Printf("\n%s\nterminal %d · grouped %v\n%s\n%s\n",
				strings.Repeat("=", width), width, grouped, strings.Repeat("=", width), m.View())
		}
	}
}

const sampleRepo = "/Users/someone/Documents/sync/GitHub/another"

func sampleWorktreeRoots() []string {
	return []string{
		sampleRepo,
		sampleRepo + "/.worktrees/bugfix",
		sampleRepo + "/.worktrees/cc-title",
		sampleRepo + "/.worktrees/list-layout",
		sampleRepo + "/.worktrees/windows-support",
	}
}

// sampleWorktreeSessions is a real-shaped page: one repository, five trees, the
// same day's work spread across them in recency order.
func sampleWorktreeSessions() []list.Item {
	now := time.Date(2026, 9, 9, 18, 0, 0, 0, time.UTC)
	rows := []struct {
		provider, title, dir string
		messages             int
		ago                  time.Duration
	}{
		{"opencode2", "0908｜探索｜检查本地是否为最新版本", "", 379, time.Minute},
		{"opencode2", "0909｜Fix｜CLI installed but sessions not read", ".worktrees/bugfix", 132, 2 * time.Minute},
		{"opencode2", "0909｜Fix｜claude title command lacks model listing", ".worktrees/cc-title", 83, 2 * time.Minute},
		{"opencode2", "0909｜修复｜进程存活三态判断", ".worktrees/windows-support", 85, 9 * time.Minute},
		{"opencode2", "0909｜设计｜宽屏终端列表排版与间距", ".worktrees/list-layout", 132, 11 * time.Minute},
		{"opencode2", "0909｜Fix｜Homebrew cask install_steps run undefined method", "", 27, 44 * time.Minute},
		{"opencode2", "0909｜功能｜worktree 修复 agy 改名 bug", ".worktrees/list-layout", 125, 3 * time.Hour},
		{"claude-code", "0908｜Fix｜agy Rename Failure", "", 785, 25 * time.Hour},
		{"opencode2", "0909｜设计｜列表分组与树带", ".worktrees/list-layout/internal/tui", 44, 26 * time.Hour},
		{"pi", "0907｜Explore｜Clarify truncated user message", "", 5, 30 * time.Hour},
	}
	items := make([]list.Item, 0, len(rows))
	for i, row := range rows {
		dir := sampleRepo
		if row.dir != "" {
			dir += "/" + row.dir
		}
		items = append(items, sessionItem{summary: model.Summary{
			ID:           fmt.Sprintf("tree%02d", i),
			Provider:     row.provider,
			Title:        row.title,
			ProjectPath:  dir,
			MessageCount: row.messages,
			UpdatedAt:    now.Add(-row.ago),
		}})
	}
	return items
}

// Outside a Git project there is no repository whose worktrees the rows could
// be read against, so "by tree" can only mean "by directory".
func TestGroupingOutsideGitFallsBackToDirectories(t *testing.T) {
	m := layoutTestModel()
	m.cwd = "/notes"
	m.projectOnly = true
	m.projectScope = util.ProjectScope{CWD: "/notes", Root: "/notes"}
	m.groupMode = groupTree
	m.setSessionItems([]list.Item{
		groupRow("a", "/notes/one", 1),
		groupRow("b", "/notes/two", 5),
		groupRow("c", "/notes/one", 8),
	})
	wantBands := []string{"/notes/one:2", "/notes/two:1"}
	if labels := groupLabels(m.sessions.Items()); strings.Join(labels, " ") != strings.Join(wantBands, " ") {
		t.Fatalf("bands = %v, want %v", labels, wantBands)
	}
}
