package tui

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/util"
)

// groupHeader names one tree and stands above the sessions that started in it.
// It is a row in the same list as the sessions, which is what keeps scrolling,
// pagination, and the delegate's measured columns working unchanged — but it is
// not a session: it cannot be selected, marked, opened, or acted on.
type groupHeader struct {
	// path is the group's own root, the directory every session under this
	// header belongs to. It is what gives the band its color, so a header and
	// the rows beneath it carry the same hue.
	path string
	// base is what the label is read against, the same base the project column
	// uses, so a header says ".worktrees/list-layout" rather than repeating the
	// project root every band already shares.
	base string
	// label names a band that is not a directory — the date bands, where the
	// heading is "Today" rather than a path. When it is set the header carries
	// no colour of its own: a hue in this list means a project, and a band of
	// time borrowing one would claim to be a place.
	label string
	count int
	// folded is set while the sessions under this band are hidden. The band
	// stays, saying how many it holds, and says that it is closed.
	folded bool
}

func (h groupHeader) FilterValue() string { return h.path }

// foldKey is what folding remembers about a band, and it has to be the same
// across a regroup: a tree band is its directory, a date band its own name.
func (h groupHeader) foldKey() string {
	if h.path != "" {
		return h.path
	}
	return h.label
}

// groupKeyFor is the tree a session belongs to. Sessions are recorded in the
// directory the agent ran in, which inside a worktree is often a subdirectory of
// it; grouping on the raw path would split one tree into a band per package.
// The longest matching root wins, so a worktree nested under another project's
// root still claims its own sessions.
//
// A path under none of the roots is its own group. That is the ordinary case
// outside a Git project, where "by tree" can only mean "by directory".
func groupKeyFor(path string, roots []string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	best := ""
	for _, root := range roots {
		root = filepath.Clean(root)
		if root == "" || root == "." {
			continue
		}
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(best) {
			best = root
		}
	}
	if best != "" {
		return best
	}
	return clean
}

// groupSessions clusters rows by tree without reordering the trees themselves.
// Groups come out in the order their first session appears, and the incoming
// list is sorted by recency, so the tree holding the newest session leads and
// the top of the list still shows the work just left behind. Inside a group the
// original recency order is untouched.
//
// A single group is returned ungrouped: one band over the whole list names
// something every row already shares, and costs a line to say it.
func groupSessions(items []list.Item, roots []string, base string) []list.Item {
	return groupSessionsBy(items,
		func(row sessionItem) string { return groupKeyFor(row.summary.ProjectPath, roots) },
		func(key string, count int) groupHeader {
			return groupHeader{path: key, base: base, count: count}
		})
}

// groupSessionsBy is the banding both modes are built from: it clusters rows by
// a key without reordering the groups themselves, so whatever order the page
// arrived in survives into the bands.
//
// A single group is returned ungrouped: one band over the whole list names
// something every row already shares, and costs a line to say it.
func groupSessionsBy(items []list.Item, key func(sessionItem) string, head func(string, int) groupHeader) []list.Item {
	order := make([]string, 0, 8)
	members := make(map[string][]list.Item, 8)
	for _, item := range items {
		row, ok := item.(sessionItem)
		if !ok {
			// Headers from a previous grouping are dropped rather than nested:
			// regrouping an already grouped list must produce the same list.
			continue
		}
		k := key(row)
		if _, seen := members[k]; !seen {
			order = append(order, k)
		}
		members[k] = append(members[k], item)
	}
	if len(order) < 2 {
		return items
	}
	out := make([]list.Item, 0, len(items)+len(order))
	for _, k := range order {
		rows := members[k]
		out = append(out, head(k, len(rows)))
		out = append(out, rows...)
	}
	return out
}

// hasGroupHeaders reports whether bands are actually drawn. Grouping can be on
// while the list holds one tree, and everything that reacts to a grouped list —
// the project column, the cursor's header skipping — has to follow what is on
// screen rather than what the mode says.
func hasGroupHeaders(items []list.Item) bool {
	for _, item := range items {
		if _, ok := item.(groupHeader); ok {
			return true
		}
	}
	return false
}

// projectsBelowGroups reports whether any row sits below its own group root —
// a monorepo's subtrees, or an agent started in a package rather than at the
// top of the worktree. When none do, the project column repeats what the band
// above it already said, and the cells belong to the title instead.
func projectsBelowGroups(items []list.Item, roots []string) bool {
	for _, item := range items {
		row, ok := item.(sessionItem)
		if !ok || row.summary.ProjectPath == "" {
			continue
		}
		if filepath.Clean(row.summary.ProjectPath) != groupKeyFor(row.summary.ProjectPath, roots) {
			return true
		}
	}
	return false
}

// groupRoots is the set of trees this list can group by. Git names them exactly,
// and only inside a project scope: with every session on screen there is no one
// repository whose worktrees they could be read against, so each directory is
// its own group.
func (m modelState) groupRoots() []string {
	if m.scopeMode == scopeModeExact && m.projectScope.Git {
		return m.projectScope.Worktrees
	}
	return nil
}

// projectBase is the directory paths are shown relative to, for the project
// column and for a group's label alike. The two must agree: a band and the rows
// under it are the same path written once and once per row.
//
// The global scope has no project to be read against, so it reads against
// ui.path_base instead — the prefix its rows would otherwise repeat — and home
// when that is unset. Base is spelling, never filtering: the scope still holds
// every session there is.
func (m modelState) projectBase() string {
	if m.scopeMode == scopeModeAll {
		return m.pathBase
	}
	if m.projectScope.Root != "" {
		return m.projectScope.Root
	}
	return m.cwd
}

// groupedItems is what the list should hold for the current mode. The ungrouped
// page is kept as it arrived so the toggle is free and, more importantly, so
// turning grouping off restores true recency order rather than the order the
// bands left behind.
func (m modelState) groupedItems() []list.Item {
	switch m.groupMode {
	case groupTree:
		return m.foldedBands(groupSessions(m.ungrouped, m.groupRoots(), m.projectBase()))
	case groupDate:
		return m.foldedBands(groupSessionsByDate(m.ungrouped, time.Now()))
	default:
		return m.ungrouped
	}
}

// foldedBands hides the sessions of every band the reader closed and marks
// those bands, so a heading keeps saying how many it holds. Every band may be
// closed — a list of headings is a legitimate summary of what is there — and
// the cursor then rests on one of them, where the key that opens it is aimed.
func (m modelState) foldedBands(items []list.Item) []list.Item {
	if len(m.folded) == 0 || !hasGroupHeaders(items) {
		return items
	}
	kept := make([]list.Item, 0, len(items))
	for i := 0; i < len(items); i++ {
		head, isHead := items[i].(groupHeader)
		if !isHead {
			kept = append(kept, items[i])
			continue
		}
		head.folded = m.folded[head.foldKey()]
		kept = append(kept, head)
		if !head.folded {
			continue
		}
		// A band's sessions run until the next heading.
		for i+1 < len(items) {
			if _, next := items[i+1].(groupHeader); next {
				break
			}
			i++
		}
	}
	return kept
}

// setSessionItems is the one way a page of sessions enters the list: it records
// the ungrouped order, applies the current mode, and takes the cursor off a
// header. Setting items directly would leave the cursor on a band.
func (m *modelState) setSessionItems(items []list.Item) {
	m.ungrouped = items
	m.sessions.SetItems(m.groupedItems())
	m.skipGroupHeader(true)
}

// selectSession puts the cursor back on a session by ID. Grouping moves rows
// under their bands, and a cursor left at its old index would land on a
// different session — or on a band — for no reason the user can see.
func (m *modelState) selectSession(id string) {
	if id != "" {
		for i, item := range m.sessions.Items() {
			if row, ok := item.(sessionItem); ok && row.summary.ID == id {
				m.sessions.Select(i)
				return
			}
		}
	}
	m.skipGroupHeader(true)
}

// skipGroupHeader moves the cursor to the nearest row it can act from in the
// direction the user was already travelling. An open band is a label, not a
// destination: stopping on one would make ↑↓ pause on a row that enter, space,
// and x all ignore.
//
// A closed band is the one exception, and it has to be: its sessions are not in
// the list, so resting on the band is the only way back to them, and z is aimed
// at what the cursor is on. Nothing is selected while it rests there — the
// resume command belongs to a session — so those keys do nothing rather than
// acting on a session the reader can no longer see.
func (m *modelState) skipGroupHeader(downward bool) {
	items := m.sessions.Items()
	if len(items) == 0 {
		return
	}
	idx := m.sessions.Index()
	step := 1
	if !downward {
		step = -1
	}
	rest := func(i int) {
		m.sessions.Select(i)
		if _, isHeader := items[i].(groupHeader); isHeader {
			m.lastResume = ""
		}
	}
	for i := idx; i >= 0 && i < len(items); i += step {
		if head, isHeader := items[i].(groupHeader); !isHeader || head.folded {
			rest(i)
			return
		}
	}
	for i := range items {
		if head, isHeader := items[i].(groupHeader); !isHeader || head.folded {
			rest(i)
			return
		}
	}
}

// toggleFold closes or opens the band the cursor is in. Folding is aimed at a
// band but driven from where the reader already is: the cursor sits on a
// session inside the band, or on a closed band itself, and never has to be
// walked onto a heading that is open.
func (m modelState) toggleFold() modelState {
	key, ok := m.bandAtCursor()
	if !ok {
		return m
	}
	if m.folded == nil {
		// A model built without one still folds: the set is a view, and a view
		// that needs a constructor to exist is a trap for the next caller.
		m.folded = map[string]bool{}
	}
	selected, hadRow := m.sessions.SelectedItem().(sessionItem)
	if m.folded[key] {
		delete(m.folded, key)
	} else {
		m.folded[key] = true
	}
	m.sessions.SetItems(m.groupedItems())
	if hadRow {
		m.selectSession(selected.summary.ID)
	} else {
		m.sessions.Select(m.sessions.Index())
	}
	m.skipGroupHeader(true)
	m.applySessionDelegate()
	return m
}

// bandAtCursor is the band the cursor is in: the heading the cursor is on when
// it rests on a closed one, or the last heading it passed otherwise.
func (m modelState) bandAtCursor() (string, bool) {
	items := m.sessions.Items()
	idx := m.sessions.Index()
	if idx >= len(items) {
		return "", false
	}
	if head, ok := items[idx].(groupHeader); ok {
		return head.foldKey(), true
	}
	for i := idx - 1; i >= 0; i-- {
		if head, ok := items[i].(groupHeader); ok {
			return head.foldKey(), true
		}
	}
	return "", false
}

// headerSkipUpward reports which way the cursor was moving, so it leaves a band
// the way it entered it. Anything that is not an upward move settles downward,
// including the first paint of a page, where the top row is a band.
func headerSkipUpward(key string) bool {
	switch key {
	case "up", "k", "pgup", "home", "shift+tab":
		return true
	}
	return false
}

// renderGroupHeader draws one band: the tree's chip, a rule across the row, and
// how many sessions it holds. It is laid out from the same measured columns the
// session rows use, so the band starts where a row starts and ends where a row
// ends rather than floating over the list.
func (d sessionDelegate) renderGroupHeader(h groupHeader, width int, selected bool) string {
	c := d.columns(width)
	label, ink, tint := txt.groupNoProject, twinTheme.textSubtle, twinTheme.border
	switch {
	case h.label != "":
		// A date band is not a place. Hue in this list means a project, so a
		// stretch of time takes the neutral chip rather than borrowing one.
		label = h.label
	case h.path != "":
		label = util.SanitizeDisplay(projectCellText(h.path, h.base))
		ink, tint = chipColors(projectColor(h.path))
	}
	room := min(projectColumnCap, width-rowGutterWidth-projectChipPad)
	if room < 1 {
		return ""
	}
	// A path is shortened from the middle so its root survives; a band's name
	// is a phrase, and a phrase is cut at the end like any other sentence. In
	// the global scope the label is a whole path, or the part of one below
	// ui.path_base, and it takes the same fixed tail the rows below it do.
	shown := elidePath(label, room)
	if d.global {
		shown = elidePathTail(label, room, d.pathDepth)
	}
	if h.label != "" {
		shown = ansi.Truncate(label, room, "…")
	}
	chip := projectChip(shown, ink, tint)
	// The gutter is the same three cells a session row uses: the cursor, then
	// the mark column. A closed band rests its own cursor there — its sessions
	// are gone, so the band is the only place left to put it — and says it is
	// closed in the mark column, which no band can otherwise use.
	cursor := " "
	if selected {
		cursor = selectedRow.Render("›")
	}
	mark := " "
	if h.folded {
		mark = "▸"
	}
	head := c.leftInset + cursor + mark + " " + chip
	count := mutedStyle.Render(sessionCountText(h.count))
	// The rule is what makes a label a band. It is dropped rather than
	// squeezed: two dashes between a chip and a count read as damage, while a
	// chip and a count alone still read as a heading.
	tail := ansi.StringWidth(count) + ansi.StringWidth(c.rightInset) + 2
	fill := width - ansi.StringWidth(head) - tail
	if fill < 2 {
		return ansi.Truncate(head+"  "+count+c.rightInset, width, "")
	}
	rule := lipgloss.NewStyle().Foreground(twinTheme.border).Render(strings.Repeat("─", fill))
	return ansi.Truncate(head+" "+rule+" "+count+c.rightInset, width, "")
}
