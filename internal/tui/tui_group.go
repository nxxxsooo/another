package tui

import (
	"path/filepath"
	"strings"

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
	base  string
	count int
}

func (h groupHeader) FilterValue() string { return h.path }

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
	order := make([]string, 0, 8)
	members := make(map[string][]list.Item, 8)
	for _, item := range items {
		row, ok := item.(sessionItem)
		if !ok {
			// Headers from a previous grouping are dropped rather than nested:
			// regrouping an already grouped list must produce the same list.
			continue
		}
		key := groupKeyFor(row.summary.ProjectPath, roots)
		if _, seen := members[key]; !seen {
			order = append(order, key)
		}
		members[key] = append(members[key], item)
	}
	if len(order) < 2 {
		return items
	}
	out := make([]list.Item, 0, len(items)+len(order))
	for _, key := range order {
		rows := members[key]
		out = append(out, groupHeader{path: key, base: base, count: len(rows)})
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
	if m.projectOnly && m.projectScope.Git {
		return m.projectScope.Worktrees
	}
	return nil
}

// projectBase is the directory paths are shown relative to, for the project
// column and for a group's label alike. The two must agree: a band and the rows
// under it are the same path written once and once per row.
func (m modelState) projectBase() string {
	if !m.projectOnly {
		return ""
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
	if !m.grouped {
		return m.ungrouped
	}
	return groupSessions(m.ungrouped, m.groupRoots(), m.projectBase())
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

// skipGroupHeader moves the cursor to the nearest session in the direction the
// user was already travelling. A band is a label, not a destination: stopping
// on one would make ↑↓ pause on a row that enter, space, and x all ignore.
//
// A header is always followed by at least one session, so a downward scan ends
// on a row. An upward scan can run off the top — the first band — and turns
// around there rather than sitting on it.
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
	for i := idx; i >= 0 && i < len(items); i += step {
		if _, isHeader := items[i].(groupHeader); !isHeader {
			m.sessions.Select(i)
			return
		}
	}
	for i := range items {
		if _, isHeader := items[i].(groupHeader); !isHeader {
			m.sessions.Select(i)
			return
		}
	}
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
func (d sessionDelegate) renderGroupHeader(h groupHeader, width int) string {
	c := d.columns(width)
	label, ink, tint := txt.groupNoProject, twinTheme.textSubtle, twinTheme.border
	if h.path != "" {
		label = util.SanitizeDisplay(projectCellText(h.path, h.base))
		ink, tint = chipColors(projectColor(h.path))
	}
	room := min(projectColumnCap, width-rowGutterWidth-projectChipPad)
	if room < 1 {
		return ""
	}
	chip := projectChip(elidePath(label, room), ink, tint)
	head := c.leftInset + strings.Repeat(" ", rowGutterWidth) + chip
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
