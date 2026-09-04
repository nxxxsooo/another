package tui

import (
	"context"
	"encoding/json"
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
)

type sessionItem struct {
	summary     model.Summary
	providerLbl string
	snippet     string
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
	return strings.ReplaceAll(title, "\n", " ")
}

func (i sessionItem) FilterValue() string {
	return i.summary.ID + " " + i.summary.Title + " " + i.summary.ProjectPath + " " + i.summary.Provider
}

// sessionDelegate renders one session per line so the title, the only thing
// that identifies a session, gets every column the terminal can spare.
type sessionDelegate struct{ spacing int }

func (sessionDelegate) Height() int                         { return 1 }
func (d sessionDelegate) Spacing() int                      { return d.spacing }
func (sessionDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d sessionDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(sessionItem)
	if !ok {
		return
	}
	width := max(20, m.Width())
	cursor := "  "
	if index == m.Index() {
		cursor = selectedRow.Render("› ")
	}

	rel := util.FormatRelative(it.summary.UpdatedAt)
	lbl := it.providerLbl
	if lbl == "" {
		lbl = it.summary.Provider
	}
	msgs := ""
	if it.summary.MessageCount > 0 {
		msgs = fmt.Sprintf("%d条", it.summary.MessageCount)
	}

	const (
		timeW = 10
		provW = 12
		msgW  = 7
	)
	// The project column is what tells two similarly named sessions apart, but
	// the title matters more; it only appears once the title still has room.
	projW := 0
	if width >= 92 {
		projW = min(28, width/4)
	}
	fixed := 2 + timeW + provW + msgW + 3
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
	provText := padRight(ansi.Truncate(lbl, provW, ""), provW)
	if color, ok := providerColors[it.summary.Provider]; ok {
		provText = lipgloss.NewStyle().Foreground(color).Render(provText)
	}

	row := cursor +
		mutedStyle.Render(padRight(ansi.Truncate(rel, timeW, ""), timeW)) + " " +
		provText + " " + title + " "
	if projW > 0 {
		row += mutedStyle.Render(padRight(truncateLeft(util.TildePath(it.summary.ProjectPath), projW), projW)) + " "
	}
	row += mutedStyle.Render(padLeft(msgs, msgW))
	fmt.Fprint(w, ansi.Truncate(row, width, ""))
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
	if color, ok := providerColors[it.id]; ok && index != m.Index() {
		name = lipgloss.NewStyle().Foreground(color).Render(name)
	}
	fmt.Fprint(w, ansi.Truncate(cursor+name, max(4, m.Width()), ""))
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
	} else if color, ok := providerColors[it.id]; ok {
		name = lipgloss.NewStyle().Foreground(color).Render(name)
	}
	count := mutedStyle.Render(fmt.Sprintf("%d", it.count))
	line := cursor + padRight(name, max(4, m.Width()-ansi.StringWidth(count)-3)) + " " + count
	fmt.Fprint(w, ansi.Truncate(line, max(4, m.Width()), ""))
}

type sessionsPageMsg struct {
	items    []list.Item
	total    int
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

	selected        *sessionItem
	loading         bool
	indexing        bool
	contentIndexing bool
	searching       bool
	searchQuery     string
	totalSessions   int
	cwd             string
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

	counts, _ := idx.CountByProvider()

	sessList := newSessionList(nil)
	sources := sourceChips(reg, counts)
	sourceList := newSourceList(sourceItems(sources))
	targetList := newTargetList(nil)

	vp := viewport.New(64, 20)
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search titles and messages"
	rename := textinput.New()
	rename.Prompt = ""
	rename.Placeholder = "新的会话标题"
	rename.CharLimit = 200
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle

	// Settings are read here rather than threaded through every caller: the
	// suggestion agent is a TUI-only concern and an unreadable config simply
	// leaves the feature off.
	var titleCfg titler.Config
	if settings, err := config.LoadSettings(); err == nil && settings.TitleModel != nil {
		titleCfg = titler.Config{Provider: settings.TitleModel.Provider, Model: settings.TitleModel.Model}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := modelState{
		reg: reg, idx: idx, engine: engine,
		titleCfg: titleCfg,
		sessions: sessList, sourceList: sourceList, targets: targetList,
		preview: vp, searchInput: search, renameInput: rename, spinner: sp,
		sources: sources, cwd: cwd,
		indexing: index.NeedsIncrementalIndex(reg, idx, 5*time.Minute), pageGen: 1,
		ctx: ctx, cancel: cancel, contextMode: contextMode,
	}
	m.contentIndexing = !m.indexing
	if initial != nil {
		sel := sessionItem{summary: *initial, providerLbl: registry.DisplayName(reg, initial.Provider)}
		m.selected = &sel
		m.sessions.SetItems([]list.Item{sel})
		m.targets.SetItems(targetItems(reg, initial.Provider))
		m.overlay = overlayTarget
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, runErr := p.Run()
	if runErr != nil {
		return runErr
	}
	done, ok := final.(modelState)
	if ok && done.launch != "" {
		// Handing the terminal to another agent: no goodbye screen, or it
		// lands as noise right before that agent paints its own startup.
		return launchResume(done.launch, done.launchTarget, done.launchProject)
	}
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

func newSessionList(items []list.Item) list.Model {
	return newBareList(items, sessionDelegate{}, 72, 20)
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
	cmds := []tea.Cmd{tea.HideCursor, m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen)}
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

// listOptsFor browses everywhere by default. Scoping to the working directory
// is a CLI concern (`another list --cwd`); in the browser it only hid sessions
// people were looking for.
func listOptsFor(m modelState) index.ListOpts {
	return index.ListOpts{
		Provider:         m.sourceID(),
		Limit:            maxShowAllPage,
		IncludeSubagents: false,
	}
}

func loadSessionsPageCmd(m modelState, gen uint64) tea.Cmd {
	reg, idx := m.reg, m.idx
	opts := listOptsFor(m)
	providerFilter := opts.Provider
	return func() tea.Msg {
		total, err := idx.Count(opts)
		if err != nil {
			return sessionsPageMsg{err: err, provider: providerFilter, gen: gen}
		}
		summaries, lerr := idx.List(opts)
		if err == nil {
			err = lerr
		}
		var sitems []list.Item
		for _, s := range summaries {
			sitems = append(sitems, sessionItem{
				summary:     s,
				providerLbl: registry.DisplayName(reg, s.Provider),
			})
		}
		return sessionsPageMsg{items: sitems, total: total, provider: providerFilter, gen: gen, err: err}
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

func searchCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, query, providerFilter string) tea.Cmd {
	return func() tea.Msg {
		hits, err := idx.Search(index.SearchOpts{
			Query: query, Provider: providerFilter, Limit: maxShowAllPage,
		})
		status, statusErr := idx.ContentStatus()
		if err == nil {
			err = statusErr
		}
		items := make([]list.Item, 0, len(hits))
		for _, hit := range hits {
			items = append(items, sessionItem{summary: hit.Session, snippet: hit.Snippet,
				providerLbl: registry.DisplayName(reg, hit.Session.Provider)})
		}
		return searchResultsMsg{items: items, query: query, status: status, err: err}
	}
}

func refreshIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, providerFilter string, reloadPage bool) tea.Cmd {
	return func() tea.Msg {
		n, err := index.UpdateIncremental(ctx, reg, idx, providerFilter)
		counts, _ := idx.CountByProvider()
		return indexRefreshedMsg{counts: counts, err: err, updated: n, reloadPage: reloadPage}
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
		if err := renamer.RenameSession(ctx, ref, title); err != nil {
			return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
		}
		_, err = index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		if err != nil {
			err = fmt.Errorf("session renamed, but index refresh failed: %w", err)
		}
		return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
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
		return m, nil
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
			m.status = okStyle.Render("已归档 "+truncateDisplay(msg.summary.Title, 48)) + mutedStyle.Render("  ·  A 撤销")
		} else {
			m.lastArchived = nil
			m.status = okStyle.Render("已取消归档 " + truncateDisplay(msg.summary.Title, 48))
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
			m.suggestErr = msg.err.Error()
		case msg.title == "":
			m.suggestErr = "没有可用建议"
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
		m.status = okStyle.Render("已重命名为 " + truncateDisplay(msg.title, 56))
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
		m.status = okStyle.Render("已删除 " + truncateDisplay(msg.title, 48))
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
		verb := "Migrated to "
		if msg.res.AlreadyExists {
			verb = "Already on "
		}
		m.status = okStyle.Render(verb+target) + mutedStyle.Render("  ·  c 复制")
		if len(msg.res.Warnings) > 0 {
			m.status += mutedStyle.Render(fmt.Sprintf("  ·  %d warning(s)", len(msg.res.Warnings)))
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
		m.sources = sourceChips(m.reg, msg.counts)
		if m.sourceIdx >= len(m.sources) {
			m.sourceIdx = 0
		}
		m.sourceList.SetItems(sourceItems(m.sources))
		m.sourceList.Select(m.sourceIdx)
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
			return m, searchCmd(m.ctx, m.reg, m.idx, m.searchQuery, m.sourceID())
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
	if m.loading {
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
		return m, tea.Batch(m.spinner.Tick, searchCmd(m.ctx, m.reg, m.idx, query, m.sourceID()))
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m modelState) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
				m.err = "标题不能为空"
				return m, nil
			}
			if m.selected == nil {
				m.err = "没有选中的会话"
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
		m.err = p.DisplayName() + " 不支持直接进入"
		return m, nil
	}
	command := p.ResumeCommand(provider.WriteResult{
		SessionID: it.summary.ID, StoragePath: it.summary.StoragePath, ProjectPath: it.summary.ProjectPath,
	})
	if command == "" {
		m.err = p.DisplayName() + " 没有可用的 resume 命令"
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
	return m, tea.HideCursor
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
		return m, tea.HideCursor
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
			m.status = okStyle.Render("已复制 resume 命令")
		}
		return m, nil
	case "A":
		if m.lastArchived != nil {
			summary := *m.lastArchived
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, summary, false))
		}
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = "不能归档当前正在运行的会话"
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionArchiver); !ok {
				m.err = p.DisplayName() + " 不支持归档"
				return m, nil
			}
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, it.summary, true))
		}
		return m, nil
	case "ctrl+r":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = "不能重命名当前正在运行的会话"
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionRenamer); !ok {
				m.err = p.DisplayName() + " 不支持重命名"
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
	case "ctrl+d":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = "不能删除当前正在运行的会话"
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionDeleter); !ok {
				m.err = p.DisplayName() + " 不支持删除"
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
		return m, tea.Batch(m.spinner.Tick, refreshIndexCmd(m.ctx, m.reg, m.idx, m.sourceID(), true))
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

func (m *modelState) layout() {
	if m.width < 40 || m.height < 12 {
		return
	}
	m.searchInput.Width = max(8, m.width-4)
	m.renameInput.Width = max(18, min(60, m.width-20))
	frameW, frameH := paneStyle.GetHorizontalFrameSize(), paneStyle.GetVerticalFrameSize()
	headerH := lipgloss.Height(m.headerView())
	footerH := lipgloss.Height(m.footerView())
	paneOuterH := max(frameH+1, m.height-headerH-footerH)
	contentH := max(1, paneOuterH-frameH)
	sessionSpacing := 0
	if m.width >= 80 && contentH >= 14 {
		sessionSpacing = 1
	}
	m.sessions.SetDelegate(sessionDelegate{spacing: sessionSpacing})
	m.sessions.SetSize(max(1, m.width-frameW), contentH)

	modalInnerW := modalInnerWidth(m.width)
	modalListH := max(1, min(10, max(1, contentH-8)))
	m.sourceList.SetSize(modalInnerW, min(len(m.sourceList.Items()), modalListH))
	m.targets.SetSize(modalInnerW, min(len(m.targets.Items()), modalListH))

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
	pane := paneStyle.
		Width(paneW).Height(contentH).
		MaxWidth(m.width).MaxHeight(contentH + frameH).
		Render(m.sessions.View())

	switch m.overlay {
	case overlaySource:
		box := sourceModalStyle.Render(accentStyle.Render("选择来源") + "\n" + mutedStyle.Render("会话来自哪个 agent？") + "\n\n" + m.sourceList.View())
		pane = overlay(pane, box, m.width)
	case overlayTarget:
		box := targetModalStyle.Width(min(40, modalInnerWidth(m.width))).Render(okStyle.Render("选择去向") + "\n" + mutedStyle.Render("把这条会话带到哪个 agent？") + "\n\n" + m.targets.View())
		pane = overlay(pane, box, m.width)
	case overlayPreview:
		box := modalStyle.Render(m.preview.View())
		pane = overlay(pane, box, m.width)
	case overlayDelete:
		box := modalStyle.Render(m.deleteView())
		pane = overlay(pane, box, m.width)
	case overlayRename:
		box := modalStyle.Render(titleStyle.Render("重命名会话") + "\n" +
			mutedStyle.Render("写回来源 agent 的原生标题") + "\n\n" + m.renameInput.View() +
			m.suggestionLine())
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
		boxLine = ansi.Truncate(boxLine, boxW, "")
		boxLine += strings.Repeat(" ", max(0, boxW-ansi.StringWidth(boxLine)))
		left := ansi.Cut(bgLines[row], 0, x)
		right := ansi.Cut(bgLines[row], x+boxW, width)
		bgLines[row] = left + boxLine + right
	}
	return strings.Join(bgLines, "\n")
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
		line = mutedStyle.Render("AI 建议生成中…")
	case m.suggestion != "":
		line = okStyle.Render("建议 ") + m.suggestion + mutedStyle.Render("  · tab 接受")
	case m.suggestErr != "":
		line = mutedStyle.Render("建议不可用：" + m.suggestErr)
	default:
		return ""
	}
	inner := m.width - modalStyle.GetHorizontalFrameSize() - paneStyle.GetHorizontalBorderSize()
	if inner < 8 {
		return ""
	}
	return "\n" + ansi.Truncate(line, inner, "…")
}

func (m modelState) deleteView() string {
	if m.selected == nil {
		return errStyle.Render("没有选中的会话")
	}
	sm := m.selected.summary
	title := truncateDisplay(sm.Title, 64)
	project := truncateLeft(util.TildePath(sm.ProjectPath), 64)
	cancel := chipActive.Render("取消")
	remove := chipMuted.Render("删除")
	if m.deleteChoice == 1 {
		cancel = chipMuted.Render("取消")
		remove = dangerChoice.Render("删除")
	}
	if m.height < 18 {
		return errStyle.Render("删除会话？") + "\n" +
			title + "\n" + mutedStyle.Render(sm.ID) + "\n" + cancel + "   " + remove
	}
	return errStyle.Render("删除会话？") + "\n" +
		mutedStyle.Render("该操作会删除来源 agent 中的原始会话，无法撤销。") + "\n\n" +
		mutedStyle.Render("来源  ") + registry.DisplayName(m.reg, sm.Provider) + "\n" +
		mutedStyle.Render("标题  ") + title + "\n" +
		mutedStyle.Render("目录  ") + project + "\n" +
		mutedStyle.Render("ID    ") + sm.ID + "\n\n" +
		cancel + "   " + remove
}

func (m modelState) headerView() string {
	brand := accentStyle.Render(" another ")
	source := m.currentSource()
	sourceName := source.name
	if sourceName == "all" {
		sourceName = "全部"
	}
	left := brand + "  " + mutedStyle.Render("← 来源 ") + sourceChipStyle.Render(sourceName)
	if m.width >= 64 {
		left += mutedStyle.Render(fmt.Sprintf("   %d 个会话", m.totalSessions))
	}
	right := targetChipStyle.Render("去向 →")
	left = ansi.Truncate(left, max(0, m.width-ansi.StringWidth(right)-2), "…")
	gap := max(2, m.width-ansi.StringWidth(left)-ansi.StringWidth(right))
	header := left + strings.Repeat(" ", gap) + right
	lines := []string{ansi.Truncate(header, m.width, "…")}
	if m.searching {
		lines = append(lines, m.searchInput.View())
	}
	return strings.Join(lines, "\n")
}

func (m modelState) footerView() string {
	var lines []string
	switch {
	case m.err != "":
		lines = append(lines, errStyle.Render("✗ "+m.err))
	case m.loading:
		lines = append(lines, m.spinner.View()+mutedStyle.Render(" working…"))
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
			return "indexing…"
		}
		return fmt.Sprintf("%d sessions", m.totalSessions)
	}
	proj := util.TildePath(it.summary.ProjectPath)
	return fmt.Sprintf(" %s · %s", proj, it.summary.ShortID())
}

func (m modelState) help() string {
	switch m.overlay {
	case overlaySource:
		return " ↑↓ 选来源 · →/enter 应用 · esc 取消"
	case overlayTarget:
		return " ↑↓ 选去向 · enter 迁移 · esc 取消"
	case overlayPreview:
		return " ↑↓ 滚动 · esc 关闭"
	case overlayDelete:
		return " ←→ 选择 · enter 确认 · esc 取消"
	case overlayRename:
		if m.suggestion != "" {
			return " 输入新标题 · tab 用建议 · enter 保存 · esc 取消"
		}
		return " 输入新标题 · enter 保存 · esc 取消"
	}
	if m.searching {
		return " enter 搜索 · esc 取消"
	}
	if m.lastResume != "" {
		return " enter 进入该 agent · c 复制命令 · esc 继续浏览 · q 退出"
	}
	if m.lastArchived != nil {
		return " A 撤销归档 · esc 放弃撤销 · ↑↓ 继续浏览"
	}
	return " ← 来源 · ↑↓ 选会话 · enter 进入 · → 跨 agent · space 预览 · ctrl+r 重命名 · A 归档 · ctrl+d 删除 · / 搜索"
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
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if s == "" {
		return "(untitled)"
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
