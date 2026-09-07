package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

// maxShowAllPage caps one fetch. The list scrolls, so there is no user-facing
// pagination; this only keeps a pathological index from being loaded whole.
const maxShowAllPage = 200

// Overlays are transient panels drawn on top of the session list. They are not
// stages: closing one returns to exactly the row the user was on.
const (
	overlayNone = iota
	overlaySource
	overlayTarget
	overlayPreview
	overlayDelete
	overlayRename
	overlayBatchTitle
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
	// showProject is off while the browser is scoped to one project, where
	// every row would repeat the same path. The column only earns its width
	// once the list can hold more than one project.
	showProject bool
}

func (sessionDelegate) Height() int                         { return 1 }
func (d sessionDelegate) Spacing() int                      { return d.spacing }
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
		row += renderProjectCellState(it.summary.ProjectPath, projW, it.missingDir) + " "
	}
	row += mutedStyle.Render(padLeft(msgs, msgW))
	fmt.Fprint(w, ansi.Truncate(row, width, ""))
}

// projectBar is the project column's leading mark. It must stay one cell wide;
// the column's width math assumes it. The quarter block is deliberate: the
// column identifies a project, it does not rank one, so the mark should be
// found when looked for and ignored otherwise.
const projectBar = "▎"

// renderProjectCell draws the project column in exactly width cells: a colored
// bar keyed to the path, then the path with its last segment lifted out of the
// dim. The bar is one cell of foreground, not a filled chip, so it survives the
// nested ANSI resets that make background-painted columns tear in Ghostty.
//
// A directory that no longer exists keeps its path — it is still the best
// name for where the work happened — and loses its color instead. Color in
// this column means "a project you can go to", so a moved, renamed, or
// deleted directory reads as gone without a symbol having to say it.
func renderProjectCell(path string, width int) string {
	return renderProjectCellState(path, width, false)
}

func renderProjectCellState(path string, width int, missing bool) string {
	if width <= 0 {
		return ""
	}
	if path == "" {
		return strings.Repeat(" ", width)
	}
	barStyle := lipgloss.NewStyle().Foreground(projectColor(path))
	leafStyle := projectLeafStyle
	if missing {
		barStyle, leafStyle = missingProjectStyle, projectParentStyle
	}
	if width < 3 {
		return barStyle.Render(projectBar) + strings.Repeat(" ", width-1)
	}

	shown := util.SanitizeDisplay(util.TildePath(path))
	textW := width - 2
	leaf := shown
	parent := ""
	if idx := strings.LastIndex(shown, "/"); idx >= 0 {
		leaf, parent = shown[idx+1:], shown[:idx+1]
	}

	var text string
	// The last segment is what the eye is actually looking for, so it keeps the
	// column whenever the whole path cannot; the parent gives way first.
	if parentW := textW - ansi.StringWidth(leaf); parentW > 0 && parent != "" {
		text = projectParentStyle.Render(truncateLeft(parent, parentW)) +
			leafStyle.Render(leaf)
	} else {
		text = leafStyle.Render(truncateLeft(leaf, textW))
	}

	return padRight(barStyle.Render(projectBar)+" "+text, width)
}

type targetItem struct{ id, name string }

func (i targetItem) FilterValue() string { return i.id + " " + i.name }

// targetDelegate is the compact one-line renderer used inside the overlay.
type targetDelegate struct{}

func (targetDelegate) Height() int                         { return 1 }
func (targetDelegate) Spacing() int                        { return 0 }
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

func (sourceDelegate) Height() int                         { return 1 }
func (sourceDelegate) Spacing() int                        { return 0 }
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

type sessionsPageMsg struct {
	items    []list.Item
	total    int
	counts   map[string]int
	provider string
	gen      uint64
	err      error
}
type previewLoadedMsg struct {
	content string
	err     error
}
type migrateDoneMsg struct {
	res      *migrate.Result
	targetID string
	err      error
}
type deleteDoneMsg struct {
	providerID string
	title      string
	counts     map[string]int
	err        error
}
type renameDoneMsg struct {
	providerID string
	title      string
	err        error
	// caveat carries a rename that landed but could not reach one of the
	// agent's surfaces, so the row still updates and the reason is still said.
	caveat error
}

// titleSuggestionMsg carries an AI-proposed title back to the rename overlay.
// sessionID is what makes a late arrival safe to drop: by the time a model
// answers, the user may have closed the box or moved to another row.
type titleSuggestionMsg struct {
	sessionID string
	title     string
	err       error
}
type archiveDoneMsg struct {
	summary  model.Summary
	archived bool
	err      error
}
type indexRefreshedMsg struct {
	counts     map[string]int
	project    *util.ProjectScope
	err        error
	updated    int
	reloadPage bool
}
type contentIndexedMsg struct {
	status index.ContentIndexStatus
	err    error
}
type searchResultsMsg struct {
	items  []list.Item
	query  string
	counts map[string]int
	status index.ContentIndexStatus
	err    error
}

type modelState struct {
	reg    *registry.Registry
	idx    *index.Store
	engine *migrate.Engine

	sessions    list.Model
	sourceList  list.Model
	targets     list.Model
	preview     viewport.Model
	searchInput textinput.Model
	renameInput textinput.Model
	spinner     spinner.Model

	sources      []sourceChip
	sourceIdx    int
	overlay      int
	deleteChoice int // 0 cancel, 1 delete

	// titleCfg is empty unless setup picked an agent to write suggestions.
	titleCfg   titler.Config
	suggesting bool
	suggestion string
	suggestErr string
	suggestFor string

	// marked holds the session IDs chosen for a batch action, keyed by ID
	// rather than list index: filtering and page reloads rebuild the rows, and
	// an index-keyed selection would silently follow a different session.
	marked map[string]bool

	// batch* carries the bulk-rename flow. Items keep mark order, results
	// stream in out of order, and total counts both from the moment the
	// engine starts. The channel and cancel func are nil outside a run.
	batchItems      []model.Summary
	batchByID       map[string]model.Summary
	batchMissing    []titler.BatchResult
	batchResults    []titler.BatchResult
	batchTotal      int
	batchCh         <-chan titler.BatchResult
	batchCancel     context.CancelFunc
	batchRunning    bool
	batchCancelling bool
	batchExpanded   bool
	// batchCfg is titleCfg plus any model chosen for this batch alone. The
	// override is deliberately not persisted: a cheap model for forty old
	// sessions should not become the default for the next single rename.
	batchCfg          titler.Config
	batchModelInput   textinput.Model
	batchModelEditing bool
	// batchModel* is the same picker setup uses, pulled from the agent's own
	// CLI so a temporary override cannot be a name that CLI would reject.
	batchModelPicking bool
	batchModelLoading bool
	batchModelOpts    []string
	batchModelCursor  int
	batchModelFilter  string
	batchModelErr     string
	// batchGen orphans a superseded run. Re-running on a different model
	// leaves the previous engine draining in the background, and its results
	// must not land in the new list.
	batchGen uint64

	selected        *sessionItem
	loading         bool
	indexing        bool
	contentIndexing bool
	searching       bool
	searchQuery     string
	totalSessions   int
	cwd             string
	projectScope    util.ProjectScope
	projectOnly     bool
	sessionSpacing  int
	pageGen         uint64
	lastResume      string
	lastArchived    *model.Summary
	contextMode     migrate.ContextMode
	err             string
	status          string
	// launch is the resume command the caller should exec after the program
	// exits. Running it from inside bubbletea would fight over the terminal.
	launch         string
	launchTarget   string
	launchProject  string
	width          int
	height         int
	ctx            context.Context
	cancel         context.CancelFunc
	previewContent string
}

func Run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, contextMode migrate.ContextMode) error {
	return run(reg, idx, engine, nil, contextMode)
}

// RunMigrate opens the picker with a session preselected and its target overlay
// already up.
func RunMigrate(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, sessionID, from string, contextMode migrate.ContextMode) error {
	var selected *model.Summary
	if sessionID != "" {
		sm, _, err := migrate.ResolveSession(context.Background(), reg, idx, sessionID, from)
		if err != nil {
			return err
		}
		selected = sm
	}
	return run(reg, idx, engine, selected, contextMode)
}

func run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, initial *model.Summary, contextMode migrate.ContextMode) error {
	cwd, err := os.Getwd()
	if err == nil {
		cwd = util.NormalizeProjectPath(cwd)
	}
	projectScope := util.DiscoverProjectScope(context.Background(), cwd)
	initialOpts := index.ListOpts{IncludeSubagents: false}
	applyProjectScope(&initialOpts, projectScope)
	counts, _ := idx.CountByProviderFiltered(initialOpts)

	// One map instance is shared with the delegate; see sessionDelegate.
	marked := map[string]bool{}
	sessList := newSessionList(nil, marked)
	sources := sourceChips(reg, counts)
	sourceList := newSourceList(sourceItems(sources))
	targetList := newTargetList(nil)

	vp := viewport.New(64, 20)
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search titles and messages"
	rename := textinput.New()
	rename.Prompt = ""
	rename.Placeholder = txt.renamePlaceholder
	rename.CharLimit = 200
	sp := spinner.New()
	// OpenCode 2's compact braille spinner stays one cell wide, so the
	// progress counter and modal never shift between animation frames.
	sp.Spinner = spinner.MiniDot
	sp.Style = accentStyle

	// Settings are read here rather than threaded through every caller: the
	// suggestion agent is a TUI-only concern and an unreadable config simply
	// leaves the feature off.
	var titleCfg titler.Config
	if settings, err := config.LoadSettings(); err == nil && settings.TitleModel != nil {
		titleCfg = titler.Config{
			Provider: settings.TitleModel.Provider,
			Model:    settings.TitleModel.Model,
			Language: titler.NormalizeLanguage(titler.Language(settings.TitleModel.Language)),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := modelState{
		reg: reg, idx: idx, engine: engine,
		titleCfg: titleCfg,
		marked:   marked,
		sessions: sessList, sourceList: sourceList, targets: targetList,
		preview: vp, searchInput: search, renameInput: rename, spinner: sp,
		sources: sources, cwd: cwd, projectScope: projectScope, projectOnly: cwd != "",
		indexing: index.NeedsIncrementalIndex(reg, idx, 5*time.Minute), pageGen: 1,
		ctx: ctx, cancel: cancel, contextMode: contextMode,
	}
	if err != nil {
		m.err = txt.cwdUnreadable + err.Error()
	}
	m.contentIndexing = !m.indexing
	if initial != nil {
		sel := sessionItem{summary: *initial}
		m.selected = &sel
		m.sessions.SetItems([]list.Item{sel})
		m.targets.SetItems(targetItems(reg, initial.Provider))
		m.overlay = overlayTarget
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	restoreInputSource := temporarilyUseASCIIInputSource()
	defer restoreInputSource()
	final, runErr := p.Run()
	restoreInputSource()
	if runErr != nil {
		return runErr
	}
	done, ok := final.(modelState)
	if ok && done.launch != "" {
		// Handing the terminal to another agent: no goodbye screen, or it
		// lands as noise right before that agent paints its own startup. The
		// title goes with the terminal, so it names the agent taking over
		// rather than staying on another for that agent's whole session.
		setWindowTitle(os.Stdout, registry.DisplayName(reg, done.launchTarget))
		return launchResume(done.launch, done.launchTarget, done.launchProject)
	}
	restoreWindowTitle(os.Stdout)
	if ok {
		playFarewell(os.Stdout, done.width, done.height, true)
	}
	return nil
}

// launchResume replaces this process with the target agent. Claude Code is the
// one exception: accepting a project's first trust prompt records the decision
// and exits instead of resuming. For that exact transition we run it once more;
// a normal Claude exit never restarts.
func launchResume(command, target, project string) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	fmt.Fprintln(os.Stderr, command)
	if target != "claude-code" {
		return syscall.Exec(shell, []string{shell, "-c", command}, os.Environ())
	}

	configPath := filepath.Join(config.HomeDir(), ".claude.json")
	trustedBefore := claudeProjectTrusted(configPath, project)
	cmd := exec.Command(shell, "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if trustedBefore || !claudeProjectTrusted(configPath, project) {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Workspace trusted; resuming Claude Code…")
	return syscall.Exec(shell, []string{shell, "-c", command}, os.Environ())
}

func claudeProjectTrusted(configPath, project string) bool {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	var state struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &state) != nil {
		return false
	}
	if state.Projects[project].Trusted {
		return true
	}
	return state.Projects[util.NormalizeProjectPath(project)].Trusted
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

// markStatus describes the batch selection, and yields the status line back to
// ordinary messages once nothing is marked.
func (m modelState) markStatus() string {
	if len(m.marked) == 0 {
		return ""
	}
	return mutedStyle.Render(fmt.Sprintf(txt.markedFmt, len(m.marked)))
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

func (m modelState) sourceID() string {
	if m.sourceIdx < 0 || m.sourceIdx >= len(m.sources) {
		return ""
	}
	return m.sources[m.sourceIdx].id
}

func (m modelState) currentSource() sourceChip {
	if m.sourceIdx < 0 || m.sourceIdx >= len(m.sources) {
		return sourceChip{name: "all", count: m.totalSessions}
	}
	return m.sources[m.sourceIdx]
}

func (m *modelState) updateSourceCounts(counts map[string]int) {
	selectedID := m.sourceID()
	m.sources = sourceChips(m.reg, counts)
	m.sourceIdx = 0
	for i := range m.sources {
		if m.sources[i].id == selectedID {
			m.sourceIdx = i
			break
		}
	}
	m.sourceList.SetItems(sourceItems(m.sources))
	m.sourceList.Select(m.sourceIdx)
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

func (m modelState) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.HideCursor, tea.SetWindowTitle(windowTitle), m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen)}
	if m.indexing {
		cmds = append(cmds, backgroundIndexCmd(m.ctx, m.reg, m.idx))
	} else {
		cmds = append(cmds, contentIndexCmd(m.ctx, m.reg, m.idx))
	}
	return tea.Batch(cmds...)
}

func dispatchPageLoad(m modelState) (modelState, tea.Cmd) {
	m.pageGen++
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen))
}

func listOptsFor(m modelState) index.ListOpts {
	opts := index.ListOpts{
		Provider:         m.sourceID(),
		Limit:            maxShowAllPage,
		IncludeSubagents: false,
	}
	if m.projectOnly {
		applyProjectScope(&opts, m.projectScope)
	}
	return opts
}

func applyProjectScope(opts *index.ListOpts, scope util.ProjectScope) {
	if scope.Git && len(scope.Worktrees) > 0 {
		opts.ProjectRoots = append([]string(nil), scope.Worktrees...)
		return
	}
	// Outside Git a directory means that directory and what is under it. An
	// exact match hid entire trees: opening another in ~/Documents/sync/Work/
	// huatu showed nothing at all while 79 sessions sat in its subfolders,
	// because agents record the directory they ran in, not its parent.
	if scope.CWD != "" {
		opts.ProjectRoots = []string{scope.CWD}
	}
}

func providerCountOpts(m modelState) index.ListOpts {
	opts := listOptsFor(m)
	opts.Provider = ""
	opts.Limit = 0
	opts.Offset = 0
	return opts
}

func loadSessionsPageCmd(m modelState, gen uint64) tea.Cmd {
	idx := m.idx
	opts := listOptsFor(m)
	providerFilter := opts.Provider
	countOpts := providerCountOpts(m)
	return func() tea.Msg {
		total, err := idx.Count(opts)
		if err != nil {
			return sessionsPageMsg{err: err, provider: providerFilter, gen: gen}
		}
		summaries, lerr := idx.List(opts)
		if err == nil {
			err = lerr
		}
		counts, countErr := idx.CountByProviderFiltered(countOpts)
		if err == nil {
			err = countErr
		}
		return sessionsPageMsg{items: sessionItems(summaries), total: total, counts: counts, provider: providerFilter, gen: gen, err: err}
	}
}

func backgroundIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store) tea.Cmd {
	return func() tea.Msg {
		n, err := index.UpdateIncremental(ctx, reg, idx, "")
		counts, _ := idx.CountByProvider()
		return indexRefreshedMsg{counts: counts, err: err, updated: n, reloadPage: true}
	}
}

func contentIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store) tea.Cmd {
	return func() tea.Msg {
		_, _, err := idx.IndexPendingContent(ctx, reg, 0, false)
		status, statusErr := idx.ContentStatus()
		if err == nil {
			err = statusErr
		}
		return contentIndexedMsg{status: status, err: err}
	}
}

func searchOptsFor(m modelState, query string) index.SearchOpts {
	opts := index.SearchOpts{Query: query, Provider: m.sourceID(), Limit: maxShowAllPage}
	if m.projectOnly {
		listOpts := index.ListOpts{}
		applyProjectScope(&listOpts, m.projectScope)
		opts.ProjectExact = listOpts.ProjectExact
		opts.ProjectRoots = listOpts.ProjectRoots
	}
	return opts
}

func searchCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, opts index.SearchOpts, countOpts index.ListOpts) tea.Cmd {
	return func() tea.Msg {
		hits, err := idx.Search(opts)
		status, statusErr := idx.ContentStatus()
		if err == nil {
			err = statusErr
		}
		counts, countErr := idx.CountByProviderFiltered(countOpts)
		if err == nil {
			err = countErr
		}
		summaries := make([]model.Summary, 0, len(hits))
		for _, hit := range hits {
			summaries = append(summaries, hit.Session)
		}
		items := sessionItems(summaries)
		for i, hit := range hits {
			row := items[i].(sessionItem)
			row.snippet = hit.Snippet
			items[i] = row
		}
		return searchResultsMsg{items: items, query: opts.Query, counts: counts, status: status, err: err}
	}
}

func refreshIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, providerFilter, scopeCWD string, reloadPage bool) tea.Cmd {
	return func() tea.Msg {
		n, err := index.UpdateIncremental(ctx, reg, idx, providerFilter)
		counts, _ := idx.CountByProvider()
		var project *util.ProjectScope
		if scopeCWD != "" {
			discovered := util.DiscoverProjectScope(ctx, scopeCWD)
			project = &discovered
		}
		return indexRefreshedMsg{counts: counts, project: project, err: err, updated: n, reloadPage: reloadPage}
	}
}

// suggestTitleCmd asks the configured agent for one title. It runs off the UI
// thread and reports failures inline: a missing suggestion must never block or
// disturb the manual rename that is already on screen.
func suggestTitleCmd(ctx context.Context, reg *registry.Registry, cfg titler.Config, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return titleSuggestionMsg{sessionID: sm.ID, err: err}
		}
		ref := provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		var conv *model.Conversation
		if preview, ok := p.(provider.PreviewLoader); ok {
			conv, err = preview.LoadPreview(ctx, ref, 12)
		} else {
			conv, err = p.Load(ctx, ref)
		}
		if err != nil {
			return titleSuggestionMsg{sessionID: sm.ID, err: err}
		}
		title, err := titler.Suggest(ctx, cfg, titler.Request{
			Title:       sm.Title,
			ProjectPath: sm.ProjectPath,
			CreatedAt:   sm.CreatedAt,
			Messages:    conv.Messages,
		})
		return titleSuggestionMsg{sessionID: sm.ID, title: title, err: err}
	}
}

func loadPreviewCmd(ctx context.Context, reg *registry.Registry, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return previewLoadedMsg{err: err}
		}
		ref := provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		var conv *model.Conversation
		if preview, ok := p.(provider.PreviewLoader); ok {
			conv, err = preview.LoadPreview(ctx, ref, 40)
		} else {
			conv, err = p.Load(ctx, ref)
		}
		if err != nil {
			return previewLoadedMsg{err: err}
		}
		var b strings.Builder
		b.WriteString(titleStyle.Render(conv.Title) + "\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%s · %s · %d messages\n\n",
			registry.DisplayName(reg, conv.Provider), util.FormatRelative(conv.UpdatedAt), len(conv.Messages))))
		for _, msg := range conv.Messages {
			role := accentStyle.Render(string(msg.Role))
			text := msg.PlainText()
			if msg.Role == model.RoleUser {
				text = util.DisplayUserText(text)
			}
			b.WriteString(role + "\n" + text + "\n\n")
		}
		return previewLoadedMsg{content: b.String()}
	}
}

func archiveSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, archived bool) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return archiveDoneMsg{summary: sm, archived: archived, err: err}
		}
		archiver, ok := p.(provider.SessionArchiver)
		if !ok {
			return archiveDoneMsg{summary: sm, archived: archived, err: fmt.Errorf("%s does not support archive", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		if err := archiver.ArchiveSession(ctx, ref, archived); err != nil {
			return archiveDoneMsg{summary: sm, archived: archived, err: err}
		}
		_, err = index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		if err != nil {
			action := "archived"
			if !archived {
				action = "unarchived"
			}
			err = fmt.Errorf("session %s, but index refresh failed: %w", action, err)
		}
		return archiveDoneMsg{summary: sm, archived: archived, err: err}
	}
}

func renameSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, title string) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
		}
		renamer, ok := p.(provider.SessionRenamer)
		if !ok {
			return renameDoneMsg{providerID: sm.Provider, title: title, err: fmt.Errorf("%s does not support rename", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		// A caveat is not a failure: the agent's own store has the new title,
		// and some surface it also reads does not. Saying "rename failed" would
		// send the person to redo a rename that already happened.
		var caveat error
		if err := renamer.RenameSession(ctx, ref, title); err != nil {
			if !errors.Is(err, provider.ErrPartial) {
				return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
			}
			caveat = err
		}
		if _, err := index.UpdateIncremental(ctx, reg, idx, sm.Provider); err != nil {
			return renameDoneMsg{providerID: sm.Provider, title: title,
				err: fmt.Errorf("session renamed, but index refresh failed: %w", err)}
		}
		return renameDoneMsg{providerID: sm.Provider, title: title, caveat: caveat}
	}
}

func deleteSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: err}
		}
		deleter, ok := p.(provider.SessionDeleter)
		if !ok {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: fmt.Errorf("%s does not support deletion", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		if err := deleter.DeleteSession(ctx, ref); err != nil {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: err}
		}
		_, err = index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		counts, _ := idx.CountByProvider()
		if err != nil {
			err = fmt.Errorf("session deleted, but index refresh failed: %w", err)
		}
		return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, counts: counts, err: err}
	}
}

func migrateCmd(ctx context.Context, engine *migrate.Engine, sm model.Summary, to string, contextMode migrate.ContextMode) tea.Cmd {
	return func() tea.Msg {
		res, err := engine.Run(ctx, migrate.Options{
			SessionID: sm.ID, FromProvider: sm.Provider, ToProvider: to,
			ContextMode: contextMode,
		})
		return migrateDoneMsg{res: res, targetID: to, err: err}
	}
}

func (m modelState) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		// Bubbletea only erases the tail of a line it believes is shorter than
		// the terminal, so a frame drawn for the old size can survive under the
		// new one: an old border column, a count from a row that has moved.
		// Clearing on resize costs one frame and makes the screen the only
		// thing on screen.
		return m, tea.ClearScreen
	case sessionsPageMsg:
		if msg.gen != m.pageGen {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.sessions.SetItems(msg.items)
		m.totalSessions = msg.total
		m.updateSourceCounts(msg.counts)
		m.layout()
		return m, nil
	case previewLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.previewContent = msg.content
		m.overlay = overlayPreview
		m.preview.GotoTop()
		m.layout()
		return m, nil
	case archiveDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if msg.archived {
			summary := msg.summary
			m.lastArchived = &summary
			m.status = okStyle.Render(txt.archivedPrefix+truncateDisplay(msg.summary.Title, 48)) + mutedStyle.Render(txt.undoHint)
		} else {
			m.lastArchived = nil
			m.status = okStyle.Render(txt.unarchivedPrefix + truncateDisplay(msg.summary.Title, 48))
		}
		var cmd tea.Cmd
		m, cmd = dispatchPageLoad(m)
		return m, cmd
	case titleSuggestionMsg:
		// A suggestion is only meaningful for the box that asked for it.
		if m.overlay != overlayRename || m.suggestFor == "" || m.suggestFor != msg.sessionID {
			return m, nil
		}
		m.suggesting = false
		switch {
		case msg.err != nil:
			m.suggestErr = suggestErrorText(msg.err)
		case msg.title == "":
			m.suggestErr = txt.noSuggestion
		default:
			m.suggestion = msg.title
		}
		return m, nil
	case renameDoneMsg:
		m.loading = false
		m.overlay = overlayNone
		m.renameInput.Blur()
		m.clearSuggestion()
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.status = okStyle.Render(txt.renamedPrefix + truncateDisplay(msg.title, 56))
		if msg.caveat != nil {
			m.status += mutedStyle.Render("  ·  " + caveatText(msg.caveat))
		}
		var cmd tea.Cmd
		m, cmd = dispatchPageLoad(m)
		return m, cmd
	case batchReadyMsg:
		if msg.gen != m.batchGen {
			return m, nil
		}
		m.batchResults = append(m.batchResults, msg.frozen...)
		if len(msg.items) == 0 {
			m.finalizeBatch()
			return m, nil
		}
		parent := m.ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithCancel(parent)
		m.batchCancel = cancel
		m.batchTotal = len(m.batchResults) + len(msg.items)
		m.batchCh = titler.SuggestBatch(ctx, m.batchConfig(), msg.items, titler.DefaultConcurrency)
		m.batchRunning = true
		return m, tea.Batch(m.spinner.Tick, batchNextCmd(m.batchGen, m.batchCh))
	case batchResultMsg:
		if msg.gen != m.batchGen {
			return m, nil
		}
		m.batchResults = append(m.batchResults, msg.res)
		if m.batchCh != nil && len(m.batchResults) < m.batchTotal {
			return m, batchNextCmd(m.batchGen, m.batchCh)
		}
		m.finalizeBatch()
		return m, nil
	case batchFinishedMsg:
		if msg.gen != m.batchGen || !m.batchRunning {
			return m, nil
		}
		m.finalizeBatch()
		return m, nil
	case batchModelsMsg:
		if m.overlay != overlayBatchTitle || msg.provider != m.batchConfig().Provider {
			return m, nil
		}
		m.batchModelLoading = false
		if msg.err != nil {
			// A CLI that cannot answer right now is not a dead end: the
			// overlay says why and falls back to typing a name.
			m.batchModelErr = listErrorText(msg.err)
			m.batchModelPicking = false
			m.batchModelEditing = true
			m.batchModelInput.Focus()
			return m, textinput.Blink
		}
		m.batchModelOpts = msg.models
		m.batchModelCursor = modelCursorFor(modelRowsFor(msg.models, ""), m.batchConfig().Model)
		return m, nil
	case batchAppliedMsg:
		for _, id := range msg.appliedIDs {
			delete(m.marked, id)
		}
		m.overlay = overlayNone
		m.resetBatch()
		switch {
		case msg.applied > 0 && msg.failed > 0:
			detail := msg.detail
			if detail == "" {
				detail = txt.someRowsFailed
			}
			// Only the applied rows lose their mark, so ctrl+t reopens the
			// batch on exactly the rows that failed.
			m.err = fmt.Sprintf(txt.batchPartialFmt, msg.applied, msg.failed, detail)
		case msg.applied > 0:
			m.status = okStyle.Render(fmt.Sprintf(txt.batchRenamedFmt, msg.applied))
		case msg.failed > 0:
			detail := msg.detail
			if detail == "" {
				detail = txt.allRowsFailed
			}
			m.err = fmt.Sprintf(txt.batchAllFailedFmt, msg.failed, detail)
		default:
			m.status = txt.batchNoneApplied
		}
		var cmd tea.Cmd
		m, cmd = dispatchPageLoad(m)
		return m, cmd
	case deleteDoneMsg:
		m.loading = false
		m.overlay = overlayNone
		m.deleteChoice = 0
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.selected = nil
		m.lastResume = ""
		m.status = okStyle.Render(txt.deletedPrefix + truncateDisplay(msg.title, 48))
		m.sources = sourceChips(m.reg, msg.counts)
		if m.sourceIdx >= len(m.sources) {
			m.sourceIdx = 0
		}
		m.sourceList.SetItems(sourceItems(m.sources))
		m.sourceList.Select(m.sourceIdx)
		var cmd tea.Cmd
		m, cmd = dispatchPageLoad(m)
		return m, cmd
	case migrateDoneMsg:
		m.loading = false
		m.overlay = overlayNone
		if msg.err != nil {
			m.err = msg.err.Error()
			m.lastResume = ""
			return m, nil
		}
		m.lastResume = msg.res.Resume
		m.launchTarget = msg.targetID
		if msg.res.Write != nil {
			m.launchProject = msg.res.Write.ProjectPath
		}
		target := msg.res.TargetName
		if target == "" {
			target = "target"
		}
		verb := txt.migratedPrefix
		if msg.res.AlreadyExists {
			verb = txt.alreadyPrefix
		}
		m.status = okStyle.Render(verb+target) + mutedStyle.Render(txt.copyHint)
		if len(msg.res.Warnings) > 0 {
			m.status += mutedStyle.Render(fmt.Sprintf(txt.warningsFmt, len(msg.res.Warnings)))
		}
		m.layout()
		return m, nil
	case indexRefreshedMsg:
		m.indexing = false
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if msg.project != nil {
			m.projectScope = *msg.project
		}
		m.updateSourceCounts(msg.counts)
		if msg.reloadPage {
			m.contentIndexing = true
			var cmd tea.Cmd
			m, cmd = dispatchPageLoad(m)
			return m, tea.Batch(cmd, contentIndexCmd(m.ctx, m.reg, m.idx))
		}
		m.status = fmt.Sprintf("Index updated (%d sessions)", msg.updated)
		m.layout()
		return m, nil
	case contentIndexedMsg:
		m.contentIndexing = false
		if msg.err != nil && msg.err != context.Canceled {
			m.err = "content index: " + msg.err.Error()
		}
		// Index health is not news. Surface it only while work is outstanding.
		if msg.status.Pending > 0 {
			m.status = mutedStyle.Render(fmt.Sprintf("content indexing… %d pending", msg.status.Pending))
		}
		m.layout()
		if m.searchQuery != "" {
			return m, searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, m.searchQuery), providerCountOpts(m))
		}
		return m, nil
	case searchResultsMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.sessions.SetItems(msg.items)
		m.searchQuery = msg.query
		m.totalSessions = len(msg.items)
		m.updateSourceCounts(msg.counts)
		m.status = fmt.Sprintf("%d results for %q", len(msg.items), msg.query)
		if msg.status.Pending > 0 {
			m.status += mutedStyle.Render(fmt.Sprintf(" · %d sessions not indexed yet", msg.status.Pending))
		}
		m.layout()
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.searching {
			return m.updateSearching(msg)
		}
		if m.loading && !navigationKey(msg) {
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			}
			return m, nil
		}
		if m.overlay != overlayNone {
			return m.updateOverlay(msg)
		}
		return m.updateList(msg)
	}

	var cmd tea.Cmd
	if m.loading || m.batchRunning {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	switch m.overlay {
	case overlaySource:
		m.sourceList, cmd = m.sourceList.Update(msg)
	case overlayTarget:
		m.targets, cmd = m.targets.Update(msg)
	case overlayPreview:
		m.preview, cmd = m.preview.Update(msg)
	case overlayRename:
		m.renameInput, cmd = m.renameInput.Update(msg)
	case overlayBatchTitle:
		// The batch overlay owns its keys; ticks and cursor blinks die here
		// instead of leaking into the session list underneath. The model
		// override field is the one part that needs its cursor to blink.
		if m.batchModelEditing {
			m.batchModelInput, cmd = m.batchModelInput.Update(msg)
			return m, cmd
		}
		return m, nil
	default:
		m.sessions, cmd = m.sessions.Update(msg)
	}
	return m, cmd
}

func (m modelState) updateSearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.searchInput.SetValue("")
			return dispatchPageLoadModel(m)
		}
		m.layout()
		return m, nil
	case "enter":
		query := strings.TrimSpace(m.searchInput.Value())
		if query == "" {
			return m, nil
		}
		m.searching = false
		m.searchInput.Blur()
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, query), providerCountOpts(m)))
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m modelState) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayBatchTitle {
		return m.updateBatchOverlay(msg)
	}
	if m.overlay == overlayRename {
		switch msg.String() {
		case "ctrl+c", "q":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "esc":
			m.overlay = overlayNone
			m.renameInput.Blur()
			m.clearSuggestion()
			return m, tea.HideCursor
		case "tab":
			if m.suggestion == "" {
				return m, nil
			}
			m.renameInput.SetValue(m.suggestion)
			m.renameInput.CursorEnd()
			return m, nil
		case "enter":
			title := strings.TrimSpace(m.renameInput.Value())
			if title == "" {
				m.err = txt.titleEmpty
				return m, nil
			}
			if m.selected == nil {
				m.err = txt.noSessionSelected
				return m, nil
			}
			if title == strings.TrimSpace(m.selected.summary.Title) {
				m.overlay = overlayNone
				m.renameInput.Blur()
				m.clearSuggestion()
				return m, tea.HideCursor
			}
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick,
				renameSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary, title))
		}
		var cmd tea.Cmd
		m.renameInput, cmd = m.renameInput.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc":
		m.overlay = overlayNone
		m.err = ""
		m.layout()
		return m, nil
	case "left", "right":
		if m.overlay == overlayDelete {
			m.deleteChoice = 1 - m.deleteChoice
			return m, nil
		}
		if msg.String() == "left" && m.overlay == overlaySource {
			m.overlay = overlayNone
			m.layout()
			return m, nil
		}
		if msg.String() == "right" && m.overlay == overlaySource {
			return m.applySource()
		}
	case "enter":
		switch m.overlay {
		case overlaySource:
			return m.applySource()
		case overlayTarget:
			if m.selected != nil {
				if tgt, ok := m.targets.SelectedItem().(targetItem); ok {
					m.loading = true
					m.err = ""
					return m, tea.Batch(m.spinner.Tick,
						migrateCmd(m.ctx, m.engine, m.selected.summary, tgt.id, m.contextMode))
				}
			}
		case overlayDelete:
			if m.deleteChoice == 0 {
				m.overlay = overlayNone
				return m, nil
			}
			if m.selected != nil {
				m.loading = true
				m.err = ""
				return m, tea.Batch(m.spinner.Tick,
					deleteSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary))
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	switch m.overlay {
	case overlaySource:
		m.sourceList, cmd = m.sourceList.Update(msg)
	case overlayTarget:
		m.targets, cmd = m.targets.Update(msg)
	default:
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m modelState) openCurrentSession() (tea.Model, tea.Cmd) {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	p, err := m.reg.Get(it.summary.Provider)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	if !p.SupportsResume() {
		m.err = fmt.Sprintf(txt.resumeUnsupportedFmt, p.DisplayName())
		return m, nil
	}
	command := p.ResumeCommand(provider.WriteResult{
		SessionID: it.summary.ID, StoragePath: it.summary.StoragePath, ProjectPath: it.summary.ProjectPath,
	})
	if command == "" {
		m.err = fmt.Sprintf(txt.noResumeCommandFmt, p.DisplayName())
		return m, nil
	}
	m.launch = command
	m.launchTarget = it.summary.Provider
	m.launchProject = it.summary.ProjectPath
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
}

func (m modelState) openTargetDrawer() (tea.Model, tea.Cmd) {
	if m.lastResume != "" {
		m.launch = m.lastResume
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	sel := it
	m.selected = &sel
	m.targets.SetItems(targetItems(m.reg, it.summary.Provider))
	m.targets.Select(0)
	m.overlay = overlayTarget
	m.layout()
	return m, tea.Batch(tea.HideCursor, tea.ClearScreen)
}

func (m modelState) applySource() (tea.Model, tea.Cmd) {
	chip, ok := m.sourceList.SelectedItem().(sourceChip)
	if !ok {
		return m, nil
	}
	for i := range m.sources {
		if m.sources[i].id == chip.id {
			m.sourceIdx = i
			break
		}
	}
	m.overlay = overlayNone
	m.searchQuery = ""
	m.searchInput.SetValue("")
	m.lastResume = ""
	m.status = ""
	return dispatchPageLoadModel(m)
}

func (m modelState) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "left":
		if len(m.sources) == 0 {
			return m, nil
		}
		m.sourceList.SetItems(sourceItems(m.sources))
		m.sourceList.Select(m.sourceIdx)
		m.overlay = overlaySource
		m.layout()
		return m, tea.Batch(tea.HideCursor, tea.ClearScreen)
	case "right":
		return m.openTargetDrawer()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.layout()
		return m, textinput.Blink
	case "esc":
		m.err = ""
		if m.lastResume != "" {
			m.lastResume = ""
			m.launchTarget = ""
			m.launchProject = ""
			m.status = ""
			return m, nil
		}
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.searchInput.SetValue("")
			return dispatchPageLoadModel(m)
		}
		m.lastArchived = nil
		m.status = ""
		return m, nil
	case "f":
		if m.cwd == "" {
			m.err = txt.projectUnknown
			return m, nil
		}
		m.projectOnly = !m.projectOnly
		m.applySessionDelegate()
		m.err = ""
		m.status = ""
		m.lastResume = ""
		if m.searchQuery != "" {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick,
				searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, m.searchQuery), providerCountOpts(m)))
		}
		return dispatchPageLoadModel(m)
	case "enter":
		// Enter is the default action on the current object: resume it in its
		// native agent. Crossing to another agent is spatially mapped to right.
		if m.lastResume != "" {
			m.launch = m.lastResume
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		return m.openCurrentSession()
	case " ":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			sel := it
			m.selected = &sel
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, loadPreviewCmd(m.ctx, m.reg, sel.summary))
		}
		return m, nil
	case "c":
		if m.lastResume != "" {
			_ = clipboard.WriteAll(m.lastResume)
			m.status = okStyle.Render(txt.resumeCopied)
		}
		return m, nil
	// Shift means one thing in this list: the same action over the whole page.
	// Archive and select-all used to sit on a and A, two unrelated actions one
	// Shift apart, with only one of them in the footer.
	case "a":
		if m.lastArchived != nil {
			summary := *m.lastArchived
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, summary, false))
		}
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotArchiveRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionArchiver); !ok {
				m.err = fmt.Sprintf(txt.archiveUnsupportedFm, p.DisplayName())
				return m, nil
			}
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, it.summary, true))
		}
		return m, nil
	case "x":
		// x rather than space: space already opens the preview, which is the
		// primary browsing action and must keep it.
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if m.marked[it.summary.ID] {
				delete(m.marked, it.summary.ID)
			} else {
				m.marked[it.summary.ID] = true
			}
			m.status = m.markStatus()
		}
		return m, nil
	case "X":
		// Toggling on the whole visible page keeps one key for select-all and
		// clear-all, and it is x's own key with Shift: same verb, wider scope.
		items := m.sessions.Items()
		allMarked := len(items) > 0
		for _, li := range items {
			if it, ok := li.(sessionItem); ok && !m.marked[it.summary.ID] {
				allMarked = false
				break
			}
		}
		for _, li := range items {
			it, ok := li.(sessionItem)
			if !ok {
				continue
			}
			if allMarked {
				delete(m.marked, it.summary.ID)
			} else {
				m.marked[it.summary.ID] = true
			}
		}
		m.status = m.markStatus()
		return m, nil
	case "ctrl+r":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotRenameRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionRenamer); !ok {
				m.err = fmt.Sprintf(txt.renameUnsupportedFmt, p.DisplayName())
				return m, nil
			}
			sel := it
			m.selected = &sel
			m.renameInput.SetValue(it.summary.Title)
			m.renameInput.CursorEnd()
			m.renameInput.Focus()
			m.overlay = overlayRename
			m.layout()
			// The box opens on the original title immediately; the suggestion
			// lands later, or never, without ever blocking typing.
			m.suggestion, m.suggestErr, m.suggesting, m.suggestFor = "", "", false, ""
			if m.titleCfg.Enabled() {
				m.suggesting = true
				m.suggestFor = it.summary.ID
				return m, tea.Batch(textinput.Blink,
					suggestTitleCmd(m.ctx, m.reg, m.titleCfg, it.summary))
			}
			return m, textinput.Blink
		}
		return m, nil
	case "ctrl+t":
		// The batch flow previews first and renames only on confirmation, so
		// opening it never spends a model call by itself.
		return m.startBatch()
	case "ctrl+d":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotDeleteRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionDeleter); !ok {
				m.err = fmt.Sprintf(txt.deleteUnsupportedFmt, p.DisplayName())
				return m, nil
			}
			sel := it
			m.selected = &sel
			m.deleteChoice = 0
			m.overlay = overlayDelete
			m.layout()
		}
		return m, nil
	case "r":
		m.indexing = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, refreshIndexCmd(m.ctx, m.reg, m.idx, m.sourceID(), m.cwd, true))
	}
	var cmd tea.Cmd
	m.sessions, cmd = m.sessions.Update(msg)
	return m, cmd
}

// navigationKey stays live while a fetch is in flight. Swallowing arrows during
// a load makes a second press vanish, which reads as a dropped keystroke.
func navigationKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "up", "down", "left", "right", "pgup", "pgdown", "home", "end", "j", "k":
		return true
	}
	return false
}

func dispatchPageLoadModel(m modelState) (tea.Model, tea.Cmd) {
	m, cmd := dispatchPageLoad(m)
	return m, cmd
}

// applySessionDelegate rebuilds the row renderer from current state. Scope can
// change without a resize, so this is called from the scope toggle as well as
// from layout; leaving it to layout alone would keep the project column visible
// for a project-scoped list until the next window change.
func (m *modelState) applySessionDelegate() {
	m.sessions.SetDelegate(sessionDelegate{
		marked:      m.marked,
		spacing:     m.sessionSpacing,
		showProject: !m.projectOnly,
	})
}

func (m *modelState) layout() {
	if m.width < 40 || m.height < 12 {
		return
	}
	m.searchInput.Width = max(8, m.width-4)
	m.renameInput.Width = max(18, min(60, m.width-20))
	m.batchModelInput.Width = max(12, min(40, m.width-24))
	frameW, frameH := paneStyle.GetHorizontalFrameSize(), paneStyle.GetVerticalFrameSize()
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	paneOuterH := max(frameH+1, m.height-headerH-footerH)
	contentH := max(1, paneOuterH-frameH)
	m.sessionSpacing = 0
	if m.width >= 80 && contentH >= 14 {
		m.sessionSpacing = 1
	}
	m.applySessionDelegate()
	m.sessions.SetSize(max(1, m.width-frameW), contentH)

	modalInnerW := modalInnerWidth(m.width)
	modalListH := max(1, min(10, max(1, contentH-8)))
	m.sourceList.SetSize(modalInnerW, min(len(m.sourceList.Items()), modalListH))
	// The target box is the one modal with a width of its own, and lipgloss
	// counts padding inside it. Sizing the list to the box would let a row wrap
	// and make the modal a line taller than the pane it sits in.
	m.targets.SetSize(max(1, targetModalWidth(m.width)-modalStyle.GetHorizontalPadding()),
		min(len(m.targets.Items()), modalListH))

	previewH := max(3, contentH-4)
	m.preview.Width = max(10, m.width-modalStyle.GetHorizontalFrameSize())
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
	header := m.headerView()
	footer := m.footerView()
	frameH := paneStyle.GetVerticalFrameSize()
	paneOuterH := max(frameH+1, m.height-lipgloss.Height(header)-lipgloss.Height(footer))
	contentH := max(1, paneOuterH-frameH)
	// lipgloss Width() covers content plus padding; the border adds two more
	// columns on top. Passing the full frame size here would shrink the content
	// area below the list width and wrap every row.
	paneW := max(1, m.width-paneStyle.GetHorizontalBorderSize())
	listView := m.sessions.View()
	if len(m.sessions.Items()) == 0 && !m.loading {
		listView = m.emptySessionsView()
	}
	pane := paneStyle.
		Width(paneW).Height(contentH).
		MaxWidth(m.width).MaxHeight(contentH + frameH).
		Render(listView)

	switch m.overlay {
	case overlaySource:
		box := sourceModalStyle.Render(accentStyle.Render(txt.sourceModalTitle) + m.modalSubtitle(txt.sourceModalHint) + "\n\n" + m.sourceList.View())
		pane = overlay(pane, box, m.width)
	case overlayTarget:
		box := targetModalStyle.Width(targetModalWidth(m.width)).Render(okStyle.Render(txt.targetModalTitle) + m.modalSubtitle(txt.targetModalHint) + "\n\n" + m.targets.View())
		pane = overlay(pane, box, m.width)
	case overlayPreview:
		box := modalStyle.Render(m.preview.View())
		pane = overlay(pane, box, m.width)
	case overlayDelete:
		box := modalStyle.Width(textModalWidth(m.width)).Render(m.deleteView())
		pane = overlay(pane, box, m.width)
	case overlayRename:
		box := modalStyle.Width(textModalWidth(m.width)).Render(titleStyle.Render(txt.renameModalTitle) +
			m.modalSubtitle(txt.renameModalHint) + "\n\n" + m.renameInput.View() +
			m.suggestionLine())
		pane = overlay(pane, box, m.width)
	case overlayBatchTitle:
		box := modalStyle.Render(m.batchView())
		pane = overlay(pane, box, m.width)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, pane, footer)
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

// clearSuggestion drops suggestion state so a stale proposal cannot reappear
// over the next rename.
func (m *modelState) clearSuggestion() {
	m.suggesting = false
	m.suggestion = ""
	m.suggestErr = ""
	m.suggestFor = ""
}

// suggestionLine renders at most one extra row under the rename input. It adds
// a single line and clips it: the rename box has to keep fitting inside the
// terminal it is drawn over, whatever a model returns.
func (m modelState) suggestionLine() string {
	var line string
	switch {
	case m.suggesting:
		line = mutedStyle.Render(txt.suggestionLoading)
	case m.suggestion != "":
		line = okStyle.Render(txt.suggestionPrefix) + m.suggestion + mutedStyle.Render(txt.suggestionAccept)
	case m.suggestErr != "":
		line = mutedStyle.Render(txt.suggestionFailed + m.suggestErr)
	default:
		return ""
	}
	inner := modalInnerWidth(m.width)
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
	return errStyle.Render(txt.deleteConfirmTitle) + "\n" +
		mutedStyle.Render(txt.deleteConfirmBody) + "\n\n" +
		mutedStyle.Render(field(txt.fieldSource)) + registry.DisplayName(m.reg, sm.Provider) + "\n" +
		mutedStyle.Render(field(txt.fieldTitle)) + title + "\n" +
		mutedStyle.Render(field(txt.fieldDirectory)) + project + "\n" +
		mutedStyle.Render(field("ID")) + sm.ID + "\n\n" +
		cancel + "   " + remove
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
	var first string
	if m.width >= 64 {
		header := left + mutedStyle.Render(fmt.Sprintf(txt.headerCountFmt, m.totalSessions)) + right +
			mutedStyle.Render("   │   ") + m.scopeView(true)
		first = ansi.Truncate(header, m.width, "…")
	} else {
		brand = sourceChipStyle.Render(txt.scopeProject)
		if !m.projectOnly {
			brand = sourceChipStyle.Render(txt.scopeAll)
		}
		left = brand + " " + mutedStyle.Render(txt.sourceArrow) + sourceChipStyle.Render(sourceName)
		left = ansi.Truncate(left, max(0, m.width-ansi.StringWidth(right)-2), "…")
		gap := max(2, m.width-ansi.StringWidth(left)-ansi.StringWidth(right))
		first = ansi.Truncate(left+strings.Repeat(" ", gap)+right, m.width, "…")
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

func (m modelState) emptySessionsView() string {
	if m.searchQuery != "" {
		return mutedStyle.Render(txt.emptySearch)
	}
	if m.projectOnly {
		return mutedStyle.Render(txt.emptyProject)
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
		if m.width < 92 {
			lines = append(lines, mutedStyle.Render(m.selectionSummary()))
		}
	}
	lines = append(lines, footerStyle.Render(m.help()))
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m modelState) selectionSummary() string {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		if m.indexing {
			return txt.indexing
		}
		return fmt.Sprintf(txt.sessionCountFmt, m.totalSessions)
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
	help := txt.helpListBase
	rename, archive, delete := m.selectedSessionCapabilities()
	if rename {
		help += txt.helpListRename
	}
	if archive {
		help += txt.helpListArchive
	}
	if delete {
		help += txt.helpListDelete
	}
	return help + txt.helpListTail
}

func (m modelState) selectedSessionCapabilities() (rename, archive, delete bool) {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok || m.reg == nil {
		return false, false, false
	}
	p, err := m.reg.Get(it.summary.Provider)
	if err != nil {
		return false, false, false
	}
	_, rename = p.(provider.SessionRenamer)
	_, archive = p.(provider.SessionArchiver)
	_, delete = p.(provider.SessionDeleter)
	return rename, archive, delete
}

// truncateLeft keeps the tail of a path. The leading directories repeat across
// projects; the last segments are what identify one.
func isCurrentSession(sm model.Summary) bool {
	ids := []string{
		os.Getenv("PI_SESSION_ID"),
		os.Getenv("CLAUDE_SESSION_ID"),
		os.Getenv("CODEX_THREAD_ID"),
		os.Getenv("OPENCODE_SESSION_ID"),
		os.Getenv("ANTIGRAVITY_CONVERSATION_ID"),
	}
	for _, id := range ids {
		if id != "" && sm.ID == id {
			return true
		}
	}
	return sm.StoragePath != "" && os.Getenv("PI_SESSION_FILE") == sm.StoragePath
}

func truncateDisplay(s string, n int) string {
	s = util.SanitizeDisplay(s)
	if s == "" {
		return txt.untitled
	}
	return ansi.Truncate(s, n, "…")
}

func truncateLeft(s string, n int) string {
	if ansi.StringWidth(s) <= n {
		return s
	}
	runes := []rune(s)
	for i := range runes {
		candidate := "…" + string(runes[i+1:])
		if ansi.StringWidth(candidate) <= n {
			return candidate
		}
	}
	return ansi.Truncate(s, n, "")
}

func padRight(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

func padLeft(s string, n int) string {
	if w := ansi.StringWidth(s); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// caveatText strips the sentinel from a partial result so the status line reads
// as the one sentence that matters rather than as a wrapped error chain.
func caveatText(err error) string {
	text := err.Error()
	if _, rest, found := strings.Cut(text, provider.ErrPartial.Error()+": "); found {
		return rest
	}
	return text
}
