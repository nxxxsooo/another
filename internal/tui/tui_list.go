package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	marked map[string]bool
	// spacing is the blank line between rows. It is state rather than a
	// constant because it is only affordable on a terminal tall enough to
	// spend it: the rhythm it buys is worth a line per row when the list has
	// room, and worth nothing when it costs half the sessions.
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
	// bands is set while the list is drawn in groups. Under a band a row's
	// path is read against its own tree rather than against the project root,
	// and a row sitting exactly at that tree says nothing at all: the band
	// above it already did. groupRoots are the trees to read against, and are
	// empty outside Git, where every directory is its own group and the column
	// therefore falls silent entirely.
	bands bool
	// dateBands is set while the bands are stretches of time rather than
	// trees. The band then says which day a row is from, so the column beside
	// it only has to say where in that day — and a project path is still the
	// row's own, not something the heading already said.
	dateBands  bool
	groupRoots []string
}

func (sessionDelegate) Height() int { return 1 }

// Spacing separates one row from the next. Adjacent rows fit more sessions,
// but fifty of them in a bounded band read as a wall: every row carries the
// same weight, and nothing tells the eye where one record ends. The blank line
// is what makes a row a record rather than a line of a table.
func (d sessionDelegate) Spacing() int { return d.spacing }

func (sessionDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	width := max(20, m.Width())
	if header, ok := listItem.(groupHeader); ok {
		fmt.Fprint(w, d.renderGroupHeader(header, width))
		return
	}
	it, ok := listItem.(sessionItem)
	if !ok {
		return
	}
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
	if d.dateBands {
		rel = compactTime(it.summary.UpdatedAt, time.Now())
	}
	msgs := ""
	if it.summary.MessageCount > 0 {
		msgs = fmt.Sprintf(txt.messageCountFmt, it.summary.MessageCount)
	}

	c := d.columns(width)
	title := padRight(ansi.Truncate(it.displayTitle(), c.titleW, "…"), c.titleW)
	if index == m.Index() {
		title = selectedRow.Render(title)
	}
	provText := renderAgentChip(it.summary.Provider)

	row := c.leftInset + gutter +
		mutedStyle.Render(padRight(ansi.Truncate(rel, c.timeW, ""), c.timeW)) + c.gap +
		provText + c.gap + title + c.gap
	if c.projW > 0 {
		text, shown := projectCellShown(it.summary.ProjectPath, d.projectBase, d.bands, d.groupRoots)
		row += renderProjectChipCell(it.summary.ProjectPath, text, shown, c.projW, it.missingDir) + c.gap
	}
	row += mutedStyle.Render(padLeft(msgs, c.msgW)) + c.rightInset
	fmt.Fprint(w, ansi.Truncate(row, width, ""))
}

// rowColumns is the layout of one row at one width. Nothing in it depends on
// which row is being drawn: every column is measured once across the loaded
// page, which is what lets a group band be laid out from the same numbers and
// line up with the sessions under it.
type rowColumns struct {
	leftInset, rightInset, gap        string
	timeW, provW, msgW, titleW, projW int
}

// rowGutterWidth is the cursor, the mark, and the space after them. It is the
// indent everything on a row starts from, bands included.
const rowGutterWidth = 3

func (d sessionDelegate) columns(width int) rowColumns {
	// Every width here is a function of the pane and the mode, and of nothing
	// on screen. The columns used to be measured from the loaded rows so that
	// no cell was padded past its content — and every one of those
	// measurements moved when the content did. Pressing `f` swapped a scope
	// of short paths for one of long ones, and the message column, the path
	// column and the right edge all walked sideways under the eye. A column
	// that is where it was is worth more than a column with no air in it;
	// what does not fit is cut with an ellipsis, and what is short is short.
	const provW = agentChipWidth
	timeW := absoluteTimeWidth
	if d.dateBands {
		timeW = compactTimeWidth()
	}
	// The message column is sized by the language rather than by a constant:
	// "128条" and "128 msg" are not the same number of cells, and a column cut
	// to the shorter of the two would truncate the count it exists to show.
	msgW := txt.messageCountWidth
	// gaps are the spaces between columns: after the time, after the agent
	// chip, after the title, and after the project when it is drawn.
	gapCount := 3
	if d.showProject && width >= 92 {
		gapCount++
	}
	fixed := rowGutterWidth + timeW + provW + msgW
	// Spacing is bought before the text columns are, up to maxColumnGap, and
	// is one width for every gap: columns spaced unevenly read as groups.
	gapW := columnGap(max(0, width-fixed-gapCount-titleColumnCap/2), gapCount)
	room := max(8, width-fixed-gapCount*gapW)
	// The path gets the smaller share and the title the rest, up to the caps.
	// A share rather than a measurement: the same pane lays out the same way
	// whatever is loaded into it.
	projW := 0
	if gapCount == 4 {
		projW = min(projectColumnCap, room*2/5)
		room -= projW
	}
	titleW := min(titleColumnCap, room)
	room -= titleW
	// What is over the caps goes to the path first, which has more to say
	// whenever it is given the cells, and then becomes margin around the row.
	if projW > 0 {
		grow := min(room, projectColumnCap-projW)
		projW += grow
		room -= grow
	}
	// The row starts at the pane's left edge and whatever is over ends it. It
	// is over only when the pane is wider than a full row, which the band's
	// ceiling already prevents, so in practice this is nothing.
	rightInset := strings.Repeat(" ", max(0, room))
	gap := strings.Repeat(" ", gapW)
	return rowColumns{
		leftInset:  "",
		rightInset: rightInset,
		gap:        gap,
		timeW:      timeW,
		provW:      provW,
		msgW:       msgW,
		titleW:     titleW,
		projW:      projW,
	}
}

// columnGap is how wide each space between columns is, given the cells left
// over once every column has what it needs. Spacing is bought before anything
// else, because two columns that touch read as one — but only up to the point
// where it stops being spacing. Past maxColumnGap the eye has to travel the
// gap rather than take it in, which is the thing this layout is for.
func columnGap(spare, gaps int) int {
	if gaps <= 0 {
		return 1
	}
	return 1 + max(0, min(spare/gaps, maxColumnGap-1))
}

// maxColumnGap is the widest a space between two columns may be. Spacing is
// read as one thing separating two others; past a certain width it becomes a
// distance to cross instead, and the row stops being a single record. Slack
// beyond this is spent on the columns themselves, or given back as margin.
//
// A var rather than a const so the layout sample can render candidate values
// side by side, the same way it does for the band proportion. A spacing
// argument is settled by looking at the alternatives, not by picking a number.
//
// It was six. Four gaps at six cells spent 24 of a 132-cell row on emptiness
// between columns, and the row read as five islands rather than one record.
// Narrowing it does not close the padding inside the title column — that is a
// separate thing — but the cells it frees go to the path, which was truncated
// on every ordinary terminal and now usually is not.
var maxColumnGap = 3

const (
	// titleColumnCap and projectColumnCap are how much a column can use even
	// when the terminal could give it more. Both hold text that is read left
	// to right and truncated on an ordinary window, so a wide terminal should
	// widen them — but only until they say everything they have to say. Past
	// that the cells buy nothing, and a column padded beyond its content is
	// the emptiness this layout exists to remove.
	titleColumnCap   = 56
	projectColumnCap = 48
)

// absoluteTimeWidth is the widest the time column ever gets: a stamp past the
// relative range, such as "Sep 30, 2026".
const absoluteTimeWidth = 12

// naturalRowWidth is the widest a session row can be with every column full.
// It is the ceiling on the whole browser, because a band wider than this is
// asking the layout for space that no column has anything to put in.
//
// It is built from the caps rather than from the rows on screen on purpose: a
// width measured from content would move the pane border every time a page
// loaded, and a frame that resizes while it is being read is worse than the
// spacing it would be correcting.
func naturalRowWidth() int {
	return 3 + absoluteTimeWidth + agentChipWidth + txt.messageCountWidth +
		titleColumnCap + projectColumnCap + 4*maxColumnGap
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
	text, shown := projectCellShown(path, base, false, nil)
	return renderProjectChipCell(path, text, shown, width, missing)
}

// projectCellShown is what a row's project column says, and whether it says
// anything at all.
//
// Under a band it says only what the band did not. A row sitting exactly at its
// tree is already named by the heading above it, so its cell is blank rather
// than a copy; a row below its tree keeps the part that differs, which is the
// package the agent actually ran in. Outside Git every directory is its own
// group, so every cell is blank and the column disappears — which is what
// grouping by directory means.
func projectCellShown(path, base string, bands bool, roots []string) (string, bool) {
	if path == "" {
		return "", false
	}
	if !bands {
		return util.SanitizeDisplay(projectCellText(path, base)), true
	}
	key := groupKeyFor(path, roots)
	if filepath.Clean(path) == key {
		return "", false
	}
	return util.SanitizeDisplay(projectCellText(path, key)), true
}

func renderProjectChipCell(path, text string, shown bool, width int, missing bool) string {
	if width <= 0 {
		return ""
	}
	// A row with nothing to add is the only blank cell in the column, which is
	// what lets every other row be read as a chip on sight. A column too narrow
	// to hold one is blank for the same reason: half a chip reads as damage.
	if !shown || width < projectChipPad+1 {
		return strings.Repeat(" ", width)
	}
	// The chip hashes the absolute path, not the shown text, so a directory
	// keeps one hue whether the column is scoped to a project or not.
	ink, tint := chipColors(projectColor(path))
	if missing {
		ink, tint = twinTheme.textSubtle, twinTheme.border
	}
	return padRight(projectChip(elidePath(text, width-projectChipPad), ink, tint), width)
}

// projectChip is the chip body. It is not bold: the agent code is three
// letters and has to survive being small, while a path is long enough to read
// on its own, and a column of bold paths would take the row from the title.
func projectChip(text string, ink, tint lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(ink).Background(tint).Render(" " + text + " ")
}

// compactTimeWidth is the time column under date bands: "15:04", "Jan 06", and
// whatever the language calls the current minute.
func compactTimeWidth() int {
	return max(6, max(ansi.StringWidth(txt.compactNow), ansi.StringWidth(txt.bandUnknown)))
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
	items := m.sessions.Items()
	// The scope's own count decides the column; the loaded rows are only a
	// fallback for a page that arrived before the count did. Reading the rows
	// alone let a source filter take the column away: two Antigravity sessions
	// in one directory hid a column that the project's worktrees had earned.
	spread := m.scopeProjects > 1
	if m.scopeProjects == 0 {
		spread = projectsSpreadOut(items)
	}
	base := m.projectBase()
	// Grouped, the band already names the tree, so most rows have nothing left
	// to say and the column is only worth its width if some row sits below its
	// own tree — Lark Base hides the field it groups by for the same reason.
	headers := hasGroupHeaders(items)
	// Only tree bands stand in for the path column. A date band says when, so
	// every row still owes its own directory.
	bands := headers && m.groupMode == groupTree
	dateBands := headers && m.groupMode == groupDate
	roots := m.groupRoots()
	showProject := !m.projectOnly || spread
	if bands {
		showProject = projectsBelowGroups(items, roots)
	}
	return sessionDelegate{
		marked:      m.marked,
		showProject: showProject,
		projectBase: base,
		bands:       bands,
		dateBands:   dateBands,
		groupRoots:  roots,
		spacing:     m.sessionSpacing,
	}
}
