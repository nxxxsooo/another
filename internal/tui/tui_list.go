package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

type sessionItem struct {
	summary model.Summary
	snippet string
	// missingDir records that the session's directory is gone. It is resolved
	// once per page rather than per frame: the row renderer runs on every
	// keystroke, and a filesystem check does not belong there.
	missingDir bool
}

// sessionItems turns summaries into rows, resolving each distinct directory
// once so a page of rows costs one check per project rather than one per row.
func sessionItems(summaries []model.Summary) []list.Item {
	known := make(map[string]bool, len(summaries))
	items := make([]list.Item, 0, len(summaries))
	for _, s := range summaries {
		missing := false
		if s.ProjectPath != "" {
			gone, seen := known[s.ProjectPath]
			if !seen {
				info, err := os.Stat(s.ProjectPath)
				gone = err != nil || !info.IsDir()
				known[s.ProjectPath] = gone
			}
			missing = gone
		}
		items = append(items, sessionItem{summary: s, missingDir: missing})
	}
	return items
}

func (i sessionItem) displayTitle() string {
	title := strings.TrimSpace(i.summary.Title)
	if title == "" || title == "(no title)" || title == "(transcript)" {
		if i.summary.ProjectPath != "" {
			title = util.TildePath(i.summary.ProjectPath)
		}
		if title == "" {
			title = "(untitled)"
		}
	}
	// A title can be a captured shell prompt, and the glyphs in one have a
	// width only the font knows. Measuring a row that contains them is what
	// puts the previous frame on screen; see util.SanitizeDisplay.
	return util.SanitizeDisplay(title)
}

func (i sessionItem) FilterValue() string {
	return i.summary.ID + " " + i.summary.Title + " " + i.summary.ProjectPath + " " + i.summary.Provider
}

// sessionDelegate renders one session per line so the title, the only thing
// that identifies a session, gets every column the terminal can spare.
// sessionDelegate carries the batch selection by reference so the browser can
// mutate it in place. The list is rebuilt on every page load and the delegate
// is not, so the map must never be reassigned after construction.
type sessionDelegate struct {
	marked  map[string]bool
	spacing int
	// showProject is off while every row would repeat the same path. The
	// column only earns its width once the list spans more than one
	// directory — which a project scope does whenever it covers Git
	// worktrees or a monorepo's subtrees.
	showProject bool
	// projectBase is the directory the column is read against. Inside a
	// project it is the project root, so a row shows the part of its path
	// that differs from its neighbours' instead of the prefix they share.
	projectBase string
	// projectW is what the loaded rows actually need, chip included. The
	// column is capped by the pane, but it is never wider than the longest
	// thing it has to say: cells the paths do not use belong to the title.
	// Zero means "unmeasured", which leaves the cap alone.
	projectW int
}

func (sessionDelegate) Height() int { return 1 }

func (d sessionDelegate) Spacing() int { return d.spacing }

func (sessionDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(sessionItem)
	if !ok {
		return
	}
	width := max(20, m.Width())
	cursor := " "
	if index == m.Index() {
		cursor = selectedRow.Render("›")
	}
	// The mark gets its own column rather than replacing the cursor, so a
	// marked row still shows where the cursor is, and so the row width never
	// changes when a batch selection starts or ends.
	mark := " "
	if d.marked[it.summary.ID] {
		mark = accentStyle.Render("✓")
	}
	gutter := cursor + mark + " "

	rel := util.FormatRelative(it.summary.UpdatedAt)
	msgs := ""
	if it.summary.MessageCount > 0 {
		msgs = fmt.Sprintf(txt.messageCountFmt, it.summary.MessageCount)
	}

	const (
		timeW = 10
		provW = agentChipWidth
	)
	// The message column is sized by the language rather than by a constant:
	// "128条" and "128 msg" are not the same number of cells, and a column cut
	// to the shorter of the two would truncate the count it exists to show.
	msgW := txt.messageCountWidth
	// The project column is what tells two similarly named sessions apart, but
	// the title matters more; it only appears once the title still has room.
	projW := 0
	if d.showProject && width >= 92 {
		projW = min(28, width/4)
		if d.projectW > 0 {
			projW = min(projW, d.projectW)
		}
	}
	fixed := 3 + timeW + provW + msgW + 3
	if projW > 0 {
		fixed += projW + 1
	}
	titleW := width - fixed
	if titleW < 8 {
		titleW = 8
	}
	title := padRight(ansi.Truncate(it.displayTitle(), titleW, "…"), titleW)
	if index == m.Index() {
		title = selectedRow.Render(title)
	}
	provText := renderAgentChip(it.summary.Provider)

	row := gutter +
		mutedStyle.Render(padRight(ansi.Truncate(rel, timeW, ""), timeW)) + " " +
		provText + " " + title + " "
	if projW > 0 {
		row += renderProjectCellState(it.summary.ProjectPath, d.projectBase, projW, it.missingDir) + " "
	}
	row += mutedStyle.Render(padLeft(msgs, msgW))
	fmt.Fprint(w, ansi.Truncate(row, width, ""))
}

// projectChipPad is what a chip costs beyond the text it holds: one cell of
// quiet on each side, the same shape the agent chip is cut to.
const projectChipPad = 2

// renderProjectCell draws the project column in exactly width cells, as one
// chip in the project's own color — the same object the agent column is made
// of, so a row reads as two tags and a title rather than as a title with two
// unrelated ornaments.
//
// The chip is a single flat style. Nested styles inside a painted background
// emit ANSI resets that also clear the background, which is what leaves black
// rectangles in Ghostty, so the path is written in one color rather than split
// into a dim parent and a lit leaf.
//
// A directory that no longer exists keeps its path — it is still the best
// name for where the work happened — and loses its color instead. Color in
// this column means "a project you can go to", so a moved, renamed, or
// deleted directory reads as gone without a symbol having to say it.
//
// With a base the path is read against it, which is what makes the column
// usable inside one project: cells spent on the prefix every row shares say
// nothing, and left-truncating that prefix cuts off the tail that does.
func renderProjectCell(path string, width int) string {
	return renderProjectCellState(path, "", width, false)
}

func renderProjectCellState(path, base string, width int, missing bool) string {
	if width <= 0 {
		return ""
	}
	// A row with no directory recorded is the only blank cell in the column,
	// which is what lets every other row be read as a chip on sight. A column
	// too narrow to hold one is blank for the same reason: half a chip reads
	// as damage.
	if path == "" || width < projectChipPad+1 {
		return strings.Repeat(" ", width)
	}
	// The chip hashes the absolute path, not the shown text, so a directory
	// keeps one hue whether the column is scoped to a project or not.
	ink, tint := chipColors(projectColor(path))
	if missing {
		ink, tint = twinTheme.textSubtle, twinTheme.border
	}
	shown := truncateLeft(util.SanitizeDisplay(projectCellText(path, base)), width-projectChipPad)
	return padRight(projectChip(shown, ink, tint), width)
}

// projectChip is the chip body. It is not bold: the agent code is three
// letters and has to survive being small, while a path is long enough to read
// on its own, and a column of bold paths would take the row from the title.
func projectChip(text string, ink, tint lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(ink).Background(tint).Render(" " + text + " ")
}

// projectColumnWidth is what the column needs to say everything it has to say:
// the widest chip among the rows that are loaded. Without it a list whose rows
// all sit at the project root still spent a quarter of the pane on one repeated
// name, and the title — the only thing that identifies a session — paid for it.
//
// It reads Items(), not VisibleItems(), for the same reason the column's own
// visibility does: a width that changed as a filter narrowed the list would
// move the title's right edge under the person reading it.
func projectColumnWidth(items []list.Item, base string) int {
	widest := 0
	for _, item := range items {
		row, ok := item.(sessionItem)
		if !ok || row.summary.ProjectPath == "" {
			continue
		}
		shown := util.SanitizeDisplay(projectCellText(row.summary.ProjectPath, base))
		widest = max(widest, ansi.StringWidth(shown)+projectChipPad)
	}
	return widest
}

// projectCellText is what the column says about a directory. Without a base
// that is the whole path; with one it is the part below the base, because
// inside a project the shared prefix is the one thing no row needs told.
//
// It works on cleaned strings only. This runs for every visible row on every
// keystroke, so it must not touch the filesystem; the index already stores
// normalized paths, and a path this cannot place keeps its full spelling
// rather than being guessed at.
func projectCellText(path, base string) string {
	if base == "" {
		return util.TildePath(path)
	}
	clean, cleanBase := filepath.Clean(path), filepath.Clean(base)
	if clean == cleanBase {
		// The root has nothing below itself to name, so it names itself. A
		// mark standing in for it left a column of identical dots beside the
		// rows that did carry a subtree, and told nobody which project this
		// was; its own last segment is short, true, and readable.
		if leaf := filepath.Base(cleanBase); leaf != "." && leaf != string(filepath.Separator) {
			return leaf
		}
		return util.TildePath(path)
	}
	rel, err := filepath.Rel(cleanBase, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// A worktree registered outside its repository root is still part of
		// the project, and its full path is the only honest thing to show.
		return util.TildePath(path)
	}
	return filepath.ToSlash(rel)
}

// projectsSpreadOut reports whether these rows started in more than one
// directory. It is what decides whether the column is worth its width: one
// directory repeated down the list says nothing, while a project holding
// worktrees or a monorepo's subtrees is exactly where the path is the only
// thing telling two similarly titled sessions apart.
func projectsSpreadOut(items []list.Item) bool {
	first := ""
	for _, item := range items {
		row, ok := item.(sessionItem)
		if !ok || row.summary.ProjectPath == "" {
			continue
		}
		if first == "" {
			first = row.summary.ProjectPath
			continue
		}
		if row.summary.ProjectPath != first {
			return true
		}
	}
	return false
}

type targetItem struct{ id, name string }

func (i targetItem) FilterValue() string { return i.id + " " + i.name }

// targetDelegate is the compact one-line renderer used inside the overlay.
type targetDelegate struct{}

func (targetDelegate) Height() int { return 1 }

func (targetDelegate) Spacing() int { return 0 }

func (targetDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (targetDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(targetItem)
	if !ok {
		return
	}
	cursor := "  "
	name := it.name
	if index == m.Index() {
		cursor = "› "
		name = selectedRow.Render(name)
	}
	if it.id != "" && index != m.Index() {
		name = lipgloss.NewStyle().Foreground(providerColor(it.id)).Render(name)
	}
	// The picker is where the chip and the spelled-out name are seen together,
	// which is the only reason a three-letter code on a session row is
	// readable at all. This is the legend.
	fmt.Fprint(w, ansi.Truncate(cursor+renderAgentChip(it.id)+" "+name, max(4, m.Width()), ""))
}

// sourceChip is one choice in the left source drawer: "all" plus every
// installed provider.
type sourceChip struct {
	id, name string
	count    int
}

func (i sourceChip) FilterValue() string { return i.id + " " + i.name }

type sourceDelegate struct{}

func (sourceDelegate) Height() int { return 1 }

func (sourceDelegate) Spacing() int { return 0 }

func (sourceDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (sourceDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(sourceChip)
	if !ok {
		return
	}
	cursor := "  "
	name := it.name
	if index == m.Index() {
		cursor = "› "
		name = selectedRow.Render(name)
	} else if it.id != "" {
		name = lipgloss.NewStyle().Foreground(providerColor(it.id)).Render(name)
	}
	// "all" is not an agent, but it takes a chip too: one column of blocks
	// reads as a column, one block among words reads as a mistake.
	chip := renderAgentChip(it.id)
	if it.id == "" {
		chip = renderNeutralChip(allChipLabel)
	}
	count := mutedStyle.Render(fmt.Sprintf("%d", it.count))
	nameW := max(4, m.Width()-ansi.StringWidth(count)-agentChipWidth-4)
	line := cursor + chip + " " + padRight(name, nameW) + " " + count
	fmt.Fprint(w, ansi.Truncate(line, max(4, m.Width()), ""))
}

// newBareList strips every chrome row bubbles adds by default. The browser
// draws its own header and footer, and an unstripped list silently costs seven
// rows — which is invisible until a modal is taller than the pane behind it.
func newBareList(items []list.Item, delegate list.ItemDelegate, w, h int) list.Model {
	l := list.New(items, delegate, w, h)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	return l
}

func newSessionList(items []list.Item, marked map[string]bool) list.Model {
	return newBareList(items, sessionDelegate{marked: marked}, 72, 20)
}

func newSourceList(items []list.Item) list.Model {
	return newBareList(items, sourceDelegate{}, 28, 8)
}

func newTargetList(items []list.Item) list.Model {
	return newBareList(items, targetDelegate{}, 30, 8)
}

func sourceItems(chips []sourceChip) []list.Item {
	items := make([]list.Item, len(chips))
	for i := range chips {
		items[i] = chips[i]
	}
	return items
}

func sourceChips(reg *registry.Registry, counts map[string]int) []sourceChip {
	var allCount int
	for _, n := range counts {
		allCount += n
	}
	chips := []sourceChip{{id: "", name: "all", count: allCount}}
	for _, p := range reg.All() {
		if !p.Installed() && counts[p.ID()] == 0 {
			continue
		}
		chips = append(chips, sourceChip{id: p.ID(), name: p.DisplayName(), count: counts[p.ID()]})
	}
	return chips
}

func targetItems(reg *registry.Registry, exclude string) []list.Item {
	var items []list.Item
	for _, p := range reg.All() {
		if !registry.CLIAvailable(p.ID()) || p.ID() == exclude {
			continue
		}
		items = append(items, targetItem{id: p.ID(), name: p.DisplayName()})
	}
	return items
}

// applySessionDelegate rebuilds the row renderer from current state. Scope can
// change without a resize, so this is called from the scope toggle as well as
// from layout; leaving it to layout alone would keep the project column visible
// for a project-scoped list until the next window change.
//
// The column is decided from the rows that are actually loaded rather than
// from the scope alone. A project scope is not one directory: Git worktrees
// and monorepo subtrees all land in it, and there the path is the only thing
// on the row that says which tree a session came from. Reading the loaded
// items keeps every frame self-consistent, including the one after the scope
// toggle where the rows on screen still belong to the previous scope.
func (m *modelState) applySessionDelegate() {
	m.sessions.SetDelegate(sessionDelegateFor(m))
}

// sessionDelegateFor decides how rows are drawn for the state the browser is
// in. It is separate from applying it so the decision can be read directly.
func sessionDelegateFor(m *modelState) sessionDelegate {
	// Items(), not VisibleItems(): a column that appeared and vanished as a
	// filter narrowed the list would move every row beside it.
	spread := projectsSpreadOut(m.sessions.Items())
	base := ""
	if m.projectOnly {
		base = m.projectScope.Root
		if base == "" {
			base = m.cwd
		}
	}
	return sessionDelegate{
		marked:      m.marked,
		spacing:     m.sessionSpacing,
		showProject: !m.projectOnly || spread,
		projectBase: base,
		projectW:    projectColumnWidth(m.sessions.Items(), base),
	}
}
