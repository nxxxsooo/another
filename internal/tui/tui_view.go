package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

// markStatus describes the batch selection, and yields the status line back to
// ordinary messages once nothing is marked.
func (m modelState) markStatus() string {
	if len(m.marked) == 0 {
		return ""
	}
	return mutedStyle.Render(fmt.Sprintf(txt.markedFmt, len(m.marked)))
}

// The band is how wide the browser draws itself, however wide the terminal is.
// Every column on a session row is bounded except the title, which takes
// whatever is left and then pads it — so on an ultrawide terminal the row
// became a title on the left, a path on the right, and a hundred and thirty
// cells of nothing between them.
//
// Up to contentBandFloor the browser is the terminal, which is every ordinary
// window and the layout that was already reviewed there. Past it the band
// takes contentBandPercent of the width instead of all of it: a proportion
// keeps the browser growing with the screen — a wider terminal is still a
// wider list — while handing the rest back as margin. A fixed cap would stop
// growing entirely and make a 300-column terminal show exactly what a
// 133-column one does.
//
// The band is centred, so the cells it gives up become margin on both sides,
// which reads as space around a panel rather than as a gap torn open inside
// every record.
var (
	contentBandFloor   = 132
	contentBandPercent = 80
)

// bandWidth is the width every rendered thing is measured against. It is what
// m.width used to mean everywhere below; m.width itself stays the terminal,
// and is still what decides whether the terminal is too small at all.
func (m modelState) bandWidth() int {
	width := max(1, m.width)
	if width <= contentBandFloor {
		return width
	}
	// The proportion is what the terminal offers; naturalRowWidth is what the
	// columns can still use. The browser stops growing at the point where more
	// width would only be padding, so a 400-column terminal shows a full row
	// and a wide margin rather than a stretched one.
	return min(max(contentBandFloor, width*contentBandPercent/100), max(contentBandFloor, naturalRowWidth()))
}

// bandLeft is the left margin that centres the band. Odd leftovers go to the
// right, which is where a truncation would already have taken them.
func (m modelState) bandLeft() int { return max(0, (m.width-m.bandWidth())/2) }

func (m *modelState) layout() {
	if m.width < 40 || m.height < 12 {
		return
	}
	width := m.bandWidth()
	m.searchInput.Width = max(8, width-4)
	m.renameInput.Width = max(18, min(60, width-20))
	// A path is longer than the box that holds it, so the field scrolls
	// rather than widening the modal past the terminal. bubbles recomputes
	// that scrolling window only when the cursor moves, so a width set after
	// the value was filled in would render the whole path and tear the modal
	// open until the next keystroke.
	m.relocateInput.Width = max(18, min(60, width-20))
	m.relocateInput.SetCursor(m.relocateInput.Position())
	m.batchModelInput.Width = max(12, min(40, width-24))
	frameW, frameH := paneStyle.GetHorizontalFrameSize(), paneStyle.GetVerticalFrameSize()
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	paneOuterH := max(frameH+1, m.height-headerH-footerH)
	contentH := max(1, paneOuterH-frameH)
	// A short terminal spends every line on sessions; a tall one can afford
	// the blank line that keeps fifty rows from reading as one block.
	m.sessionSpacing = 0
	if width >= 80 && contentH >= 14 {
		m.sessionSpacing = 1
	}
	m.applySessionDelegate()
	m.sessions.SetSize(max(1, width-frameW), contentH)

	modalInnerW := modalInnerWidth(width)
	modalListH := max(1, min(10, max(1, contentH-8)))
	m.sourceList.SetSize(modalInnerW, min(len(m.sourceList.Items()), modalListH))
	// The target box is the one modal with a width of its own, and lipgloss
	// counts padding inside it. Sizing the list to the box would let a row wrap
	// and make the modal a line taller than the pane it sits in.
	m.targets.SetSize(max(1, targetModalWidth(width)-modalStyle.GetHorizontalPadding()),
		min(len(m.targets.Items()), modalListH))

	previewH := max(3, contentH-4)
	m.preview.Width = max(10, width-modalStyle.GetHorizontalFrameSize())
	m.preview.Height = previewH
	y := m.preview.YOffset
	m.preview.SetContent(ansi.Hardwrap(m.previewContent, max(1, m.preview.Width), false))
	m.preview.SetYOffset(y)
}

func modalInnerWidth(width int) int {
	return max(24, min(56, width-12)-modalStyle.GetHorizontalFrameSize())
}

// targetModalWidth keeps the box and the list inside it on one number: the
// picker names agents, so it never needs to be as wide as the source drawer.
func targetModalWidth(width int) int { return min(40, modalInnerWidth(width)) }

// modalSubtitle is the explanatory line under a modal's title. It disappears
// on a short terminal: the title and the control are what the modal is for,
// and a sentence that pushes the box past the bottom of the screen has taken
// the modal away to explain it. The length of that sentence depends on the
// language, so the decision is made on the height available, not on the words.
func (m modelState) modalSubtitle(text string) string {
	if m.height < 16 {
		return ""
	}
	return "\n" + mutedStyle.Render(text)
}

// textModalWidth bounds a modal whose content is prose. Sentence length is a
// property of the language, not of the layout, so a box sized by its text
// grows past the terminal in one language and not the other; past it, the
// right border is cut off and the modal stops looking like a modal. Bounding
// it instead makes the sentence wrap, which is what a sentence should do.
//
// The number is what lipgloss Width() means — content plus padding, without
// the border — while modalInnerWidth is the text area inside it.
func textModalWidth(width int) int {
	return modalInnerWidth(width) + modalStyle.GetHorizontalPadding()
}

func (m modelState) View() string {
	if m.width < 40 || m.height < 12 {
		return ansi.Truncate("Terminal too small — resize to at least 40x12", max(1, m.width), "")
	}
	width := m.bandWidth()
	header := m.headerView()
	footer := m.footerView()
	frameH := paneStyle.GetVerticalFrameSize()
	paneOuterH := max(frameH+1, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	contentH := max(1, paneOuterH-frameH)
	// lipgloss Width() covers content plus padding; the border adds two more
	// columns on top. Passing the full frame size here would shrink the content
	// area below the list width and wrap every row.
	paneW := max(1, width-paneStyle.GetHorizontalBorderSize())
	listView := m.sessions.View()
	if len(m.sessions.Items()) == 0 && !m.loading {
		listView = m.emptySessionsView()
	}
	pane := paneStyle.
		Width(paneW).Height(contentH).
		MaxWidth(width).MaxHeight(contentH + frameH).
		Render(listView)

	switch m.overlay {
	case overlaySource:
		box := sourceModalStyle.Render(accentStyle.Render(txt.sourceModalTitle) + m.modalSubtitle(txt.sourceModalHint) + "\n\n" + m.sourceList.View())
		pane = overlay(pane, box, width)
	case overlayTarget:
		box := targetModalStyle.Width(targetModalWidth(width)).Render(okStyle.Render(txt.targetModalTitle) + m.modalSubtitle(txt.targetModalHint) + "\n\n" + m.targets.View())
		pane = overlay(pane, box, width)
	case overlayPreview:
		box := modalStyle.Render(m.preview.View())
		pane = overlay(pane, box, width)
	case overlayDelete:
		box := modalStyle.Width(textModalWidth(width)).Render(m.deleteView())
		pane = overlay(pane, box, width)
	case overlayRename:
		box := modalStyle.Width(textModalWidth(width)).Render(titleStyle.Render(txt.renameModalTitle) +
			m.modalSubtitle(txt.renameModalHint) + "\n\n" + m.renameInput.View() +
			m.suggestionLine())
		pane = overlay(pane, box, width)
	case overlayRelocate:
		box := modalStyle.Width(textModalWidth(width)).Render(m.relocateView())
		pane = overlay(pane, box, width)
	case overlayBatchTitle:
		box := modalStyle.Render(m.batchView())
		pane = overlay(pane, box, width)
	}
	return centerBlock(lipgloss.JoinVertical(lipgloss.Left, header, pane, footer), m.bandLeft(), m.width)
}

// centerBlock puts a rendered block at column left and pads the row out to the
// full terminal width. The margins are plain spaces carrying no style, so the
// terminal's own backdrop shows through them — and writing the whole row means
// a resize repaints the cells the band used to occupy instead of leaving the
// previous frame's border standing in them.
func centerBlock(block string, left, total int) string {
	if left <= 0 && total <= 0 {
		return block
	}
	lines := strings.Split(block, "\n")
	pad := strings.Repeat(" ", max(0, left))
	for i, line := range lines {
		line = pad + line
		lines[i] = line + strings.Repeat(" ", max(0, total-ansi.StringWidth(line)))
	}
	return strings.Join(lines, "\n")
}

// overlay centres a box over the existing pane. Cutting each covered row with
// the ANSI-aware helper keeps the session context visible on both sides while
// replacing only the cells occupied by the modal itself.
func overlay(background, box string, width int) string {
	bgLines := strings.Split(background, "\n")
	boxLines := strings.Split(box, "\n")
	if len(boxLines) > len(bgLines) {
		return box
	}
	boxW := 0
	for _, line := range boxLines {
		boxW = max(boxW, ansi.StringWidth(line))
	}
	boxW = min(boxW, width)
	x := max(0, (width-boxW)/2)
	y := max(0, (len(bgLines)-len(boxLines))/2)
	for i, boxLine := range boxLines {
		row := y + i
		if row >= len(bgLines) {
			break
		}
		boxLine = fitLeft(boxLine, boxW)
		// ansi.Cut cannot split a double-width character, so a cut that lands
		// inside one comes back a cell short or a cell long. Left uncorrected
		// that shifts the modal and every background cell after it, and the
		// row tears — visibly at some terminal widths and not others, because
		// the width decides whether the cut lands mid-character at all.
		left := fitLeft(ansi.Cut(bgLines[row], 0, x), x)
		right := cutRight(bgLines[row], x+boxW, width)
		bgLines[row] = left + boxLine + right
	}
	return strings.Join(bgLines, "\n")
}

// fitLeft returns s in exactly cells columns, keeping its start. The padding
// goes where the lost half-character was: at the end.
func fitLeft(s string, cells int) string {
	if cells <= 0 {
		return ""
	}
	if w := ansi.StringWidth(s); w > cells {
		s = ansi.Truncate(s, cells, "")
	}
	return s + strings.Repeat(" ", max(0, cells-ansi.StringWidth(s)))
}

// cutRight returns columns from..to of line in exactly to-from columns. When
// the cut starts inside a double-width character ansi.Cut keeps the whole of
// it and hands back one column too many, so the slice is taken one column
// later and the character's lost half becomes padding. Padding on the left is
// what keeps the rest of the row in the columns it was drawn in.
func cutRight(line string, from, to int) string {
	cells := to - from
	if cells <= 0 {
		return ""
	}
	s := ansi.Cut(line, from, to)
	if ansi.StringWidth(s) > cells {
		s = ansi.Cut(line, from+1, to)
	}
	if w := ansi.StringWidth(s); w > cells {
		return fitLeft(s, cells)
	}
	return strings.Repeat(" ", max(0, cells-ansi.StringWidth(s))) + s
}

// suggestionLine renders at most one extra row under the rename input. It adds
// a single line and clips it: the rename box has to keep fitting inside the
// terminal it is drawn over, whatever a model returns.
func (m modelState) suggestionLine() string {
	var line string
	switch {
	case m.suggesting:
		// The same spinner the batch draws: one session or fifty, waiting on
		// an agent looks the same, and a still line reads as a hang.
		line = m.spinner.View() + " " + mutedStyle.Render(txt.suggestionLoading)
	case m.suggestion != "":
		line = okStyle.Render(txt.suggestionPrefix) + m.suggestion + mutedStyle.Render(txt.suggestionAccept)
	case m.suggestErr != "":
		line = mutedStyle.Render(txt.suggestionFailed + m.suggestErr)
	default:
		return ""
	}
	inner := modalInnerWidth(m.bandWidth())
	if inner < 8 {
		return ""
	}
	return "\n" + ansi.Truncate(line, inner, "…")
}

// field pads a label in the delete confirmation so the values line up. The
// labels are translated and "Directory" is not the width of "目录", so the
// column is measured rather than written into the strings themselves.
func field(label string) string {
	width := 0
	for _, l := range []string{txt.fieldSource, txt.fieldTitle, txt.fieldDirectory, "ID"} {
		width = max(width, ansi.StringWidth(l))
	}
	return padRight(label, width+2)
}

func (m modelState) deleteView() string {
	if m.selected == nil {
		return errStyle.Render(txt.noSessionSelected)
	}
	sm := m.selected.summary
	title := truncateDisplay(sm.Title, 64)
	project := truncateLeft(util.TildePath(sm.ProjectPath), 64)
	cancel := chipActive.Render(txt.choiceCancel)
	remove := chipMuted.Render(txt.choiceDelete)
	if m.deleteChoice == 1 {
		cancel = chipMuted.Render(txt.choiceCancel)
		remove = dangerChoice.Render(txt.choiceDelete)
	}
	if m.height < 18 {
		return errStyle.Render(txt.deleteConfirmTitle) + "\n" +
			title + "\n" + mutedStyle.Render(sm.ID) + "\n" + cancel + "   " + remove
	}
	// The two agents differ in what they promise, so the modal says which one
	// the person is standing in front of.
	body := txt.deleteConfirmBody
	if m.selectedDeleteIsReversible() {
		body = txt.deleteConfirmBodyUndo
	}
	return errStyle.Render(txt.deleteConfirmTitle) + "\n" +
		mutedStyle.Render(body) + "\n\n" +
		mutedStyle.Render(field(txt.fieldSource)) + registry.DisplayName(m.reg, sm.Provider) + "\n" +
		mutedStyle.Render(field(txt.fieldTitle)) + title + "\n" +
		mutedStyle.Render(field(txt.fieldDirectory)) + project + "\n" +
		mutedStyle.Render(field("ID")) + sm.ID + "\n\n" +
		cancel + "   " + remove
}

func (m modelState) relocateView() string {
	if m.selected == nil {
		return errStyle.Render(txt.noSessionSelected)
	}
	sm := m.selected.summary
	fork := chipActive.Render(txt.relocateChoiceFork)
	move := chipMuted.Render(txt.relocateChoiceMove)
	if m.relocateMove {
		fork = chipMuted.Render(txt.relocateChoiceFork)
		move = dangerChoice.Render(txt.relocateChoiceMove)
	}
	modes := fork + "   " + move
	if !m.relocateCanMove {
		// A provider that cannot move must not be shown a move it would only
		// refuse; the fork chip alone states what will happen.
		modes = fork + mutedStyle.Render("   "+fmt.Sprintf(txt.relocateMoveUnsupported, registry.DisplayName(m.reg, sm.Provider)))
	}
	hint := txt.relocateForkHint
	if m.relocateMove {
		hint = txt.relocateMoveHint
	}
	if m.height < 18 {
		return titleStyle.Render(txt.relocateModalTitle) + "\n" +
			m.relocateInput.View() + "\n" + modes
	}
	return titleStyle.Render(txt.relocateModalTitle) + "\n" +
		mutedStyle.Render(hint) + "\n\n" +
		mutedStyle.Render(field(txt.fieldTitle)) + truncateDisplay(sm.Title, 64) + "\n" +
		mutedStyle.Render(field(txt.fieldDirectory)) + truncateLeft(util.TildePath(sm.ProjectPath), 64) + "\n\n" +
		m.relocateInput.View() + "\n\n" + modes
}

func (m modelState) headerView() string {
	brand := accentStyle.Render(" another ")
	source := m.currentSource()
	sourceName := source.name
	if sourceName == "all" {
		sourceName = txt.scopeAll
	}
	left := brand + "  " + mutedStyle.Render(txt.sourceArrow) + sourceChipStyle.Render(sourceName)
	right := targetChipStyle.Render(txt.targetArrow)
	width := m.bandWidth()
	var first string
	if width >= 64 {
		header := left + mutedStyle.Render(fmt.Sprintf(txt.headerCountFmt, m.totalSessions)) + right +
			mutedStyle.Render("   │   ") + m.scopeView(true)
		first = ansi.Truncate(header, width, "…")
	} else {
		brand = sourceChipStyle.Render(txt.scopeProject)
		if !m.projectOnly {
			brand = sourceChipStyle.Render(txt.scopeAll)
		}
		left = brand + " " + mutedStyle.Render(txt.sourceArrow) + sourceChipStyle.Render(sourceName)
		left = ansi.Truncate(left, max(0, width-ansi.StringWidth(right)-2), "…")
		gap := max(2, width-ansi.StringWidth(left)-ansi.StringWidth(right))
		first = ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, "…")
	}
	lines := []string{first}
	if m.searching {
		lines = append(lines, m.searchInput.View())
	}
	return strings.Join(lines, "\n")
}

func (m modelState) scopeView(showPath bool) string {
	path := m.projectScope.Root
	if path == "" {
		path = m.cwd
	}
	path = util.TildePath(path)
	var line string
	if m.projectOnly {
		line = sourceChipStyle.Render(txt.scopeThis)
	} else {
		line = sourceChipStyle.Render(txt.scopeAll)
	}
	if showPath && path != "" {
		line += mutedStyle.Render("  ·  " + path)
	}
	return line
}

// movedAwayDirectories finds directories that once held this project and no
// longer exist. It runs once, at startup, so an index full of dead paths never
// costs a redraw.
func movedAwayDirectories(idx *index.Store, roots []string) []string {
	dirs, err := idx.MissingDirectoriesFor(roots)
	if err != nil {
		return nil
	}
	var out []string
	for _, dir := range dirs {
		out = append(out, fmt.Sprintf("%s (%d)", util.TildePath(dir.Path), dir.Sessions))
		if len(out) == 3 {
			break
		}
	}
	return out
}

func (m modelState) emptySessionsView() string {
	if m.searchQuery != "" {
		return mutedStyle.Render(txt.emptySearch)
	}
	if m.projectOnly {
		body := txt.emptyProject
		// A project that was renamed or relocated looks empty here while its
		// sessions sit under the directory the agents recorded.
		if len(m.movedAway) > 0 {
			body += "\n" + fmt.Sprintf(txt.emptyProjectMoved, strings.Join(m.movedAway, ", "))
		}
		return mutedStyle.Render(body)
	}
	return mutedStyle.Render(txt.emptyAll)
}

func (m modelState) footerView() string {
	var lines []string
	switch {
	case m.err != "":
		lines = append(lines, errStyle.Render("✗ "+m.err))
	case m.loading:
		lines = append(lines, m.spinner.View()+mutedStyle.Render(txt.working))
	case m.lastResume != "":
		lines = append(lines, accentStyle.Render(m.lastResume))
	case m.status != "":
		lines = append(lines, m.status)
	default:
		if m.bandWidth() < 92 {
			lines = append(lines, mutedStyle.Render(m.selectionSummary()))
		}
	}
	lines = append(lines, footerStyle.Render(m.help()))
	// The footer starts on the band's left edge but is allowed to run to the
	// right edge of the terminal. It is a run of words, not a column: nothing
	// lines up under it, and cutting the keymap short to respect a boundary
	// the rows need would hide the keys to pay for an alignment nobody reads.
	width := max(m.bandWidth(), m.width-m.bandLeft())
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m modelState) selectionSummary() string {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		if m.indexing {
			return txt.indexing
		}
		return sessionCountText(m.totalSessions)
	}
	proj := util.TildePath(it.summary.ProjectPath)
	return fmt.Sprintf(" %s · %s", proj, it.summary.ShortID())
}

func (m modelState) help() string {
	switch m.overlay {
	case overlaySource:
		return txt.helpSource
	case overlayTarget:
		return txt.helpTarget
	case overlayPreview:
		return txt.helpPreview
	case overlayDelete:
		return txt.helpDelete
	case overlayRename:
		if m.suggestion != "" {
			return txt.helpRenameSuggestion
		}
		return txt.helpRename
	case overlayRelocate:
		if m.relocateCanMove {
			return txt.helpRelocate
		}
		return txt.helpRelocateForkOnly
	case overlayBatchTitle:
		if m.batchModelPicking {
			return txt.helpBatchModelList
		}
		if m.batchModelEditing {
			return txt.helpBatchModelTyped
		}
		if m.batchRunning {
			return txt.helpBatchRunning
		}
		return txt.helpBatchReview
	}
	if m.searching {
		return txt.helpSearch
	}
	if m.lastResume != "" {
		return txt.helpResume
	}
	if m.lastArchived != nil {
		return txt.helpArchived
	}
	if m.lastDeleted != nil {
		return txt.helpDeleted
	}
	help := txt.helpListBase
	caps := m.selectedSessionCapabilities()
	if caps.rename {
		help += txt.helpListRename
	}
	if caps.archive {
		help += txt.helpListArchive
	}
	if caps.relocate {
		help += txt.helpListRelocate
	}
	if caps.delete {
		help += txt.helpListDelete
	}
	return help + txt.helpListTail
}

// selectedDeleteIsReversible reports whether deleting the session in the open
// confirmation can be taken back. It asks the provider rather than assuming,
// so a provider that loses the capability stops promising an undo.
func (m modelState) selectedDeleteIsReversible() bool {
	if m.selected == nil || m.reg == nil {
		return false
	}
	p, err := m.reg.Get(m.selected.summary.Provider)
	if err != nil {
		return false
	}
	_, ok := p.(provider.ReversibleSessionDeleter)
	return ok
}

// sessionCapabilities is what the selected provider can actually do. The footer
// is assembled from it so an unsupported action is never advertised.
type sessionCapabilities struct {
	rename   bool
	archive  bool
	relocate bool
	delete   bool
}

func (m modelState) selectedSessionCapabilities() sessionCapabilities {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok || m.reg == nil {
		return sessionCapabilities{}
	}
	p, err := m.reg.Get(it.summary.Provider)
	if err != nil {
		return sessionCapabilities{}
	}
	var caps sessionCapabilities
	_, caps.rename = p.(provider.SessionRenamer)
	_, caps.archive = p.(provider.SessionArchiver)
	_, caps.delete = p.(provider.SessionDeleter)
	if relocator, ok := p.(provider.SessionRelocator); ok {
		caps.relocate = relocator.SupportsRelocate(provider.RelocateFork)
	}
	return caps
}

func isCurrentSession(sm model.Summary) bool { return provider.IsCurrentSession(sm) }

// caveatText strips the sentinel from a partial result so the status line reads
// as the one sentence that matters rather than as a wrapped error chain.
func caveatText(err error) string {
	text := err.Error()
	if _, rest, found := strings.Cut(text, provider.ErrPartial.Error()+": "); found {
		return rest
	}
	return text
}
