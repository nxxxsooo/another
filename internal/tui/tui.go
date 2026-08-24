package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CyrusSE/agenthop/internal/index"
	"github.com/CyrusSE/agenthop/internal/migrate"
	"github.com/CyrusSE/agenthop/internal/model"
	"github.com/CyrusSE/agenthop/internal/provider"
	"github.com/CyrusSE/agenthop/internal/registry"
	"github.com/CyrusSE/agenthop/internal/util"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	accentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(lipgloss.Color("235")).Padding(0, 1)
	chipActive  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Background(lipgloss.Color("236")).Padding(0, 1)
	chipMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
)

const (
	minPageSize    = 50
	maxPageSize    = 200
	maxShowAllPage = 200
)

func pageSizeForHeight(h int) int {
	if h < 12 {
		return minPageSize
	}
	n := h - 6
	if n < minPageSize {
		return minPageSize
	}
	if n > maxPageSize {
		return maxPageSize
	}
	return n
}

const (
	stageSessions = iota
	stageActions
	stageProviders
	stagePreview
	stageMigrate
	stageConfirm
)

var providerColors = map[string]lipgloss.Color{
	"claude-code": lipgloss.Color("203"),
	"codex":       lipgloss.Color("42"),
	"cursor":      lipgloss.Color("39"),
	"opencode":    lipgloss.Color("141"),
	"commandcode": lipgloss.Color("214"),
	"hermes":      lipgloss.Color("177"),
}

type sessionItem struct {
	summary     model.Summary
	providerLbl string
	snippet     string
}

func (i sessionItem) Title() string {
	title := strings.TrimSpace(i.summary.Title)
	if title == "" || title == "(no title)" || title == "(transcript)" {
		if i.summary.ProjectPath != "" {
			title = util.FirstUserSnippet(util.TildePath(i.summary.ProjectPath), 40)
		}
		if title == "" {
			title = "(untitled)"
		}
	} else {
		title = truncate(title, 68)
	}
	rel := mutedStyle.Render(util.FormatRelative(i.summary.UpdatedAt))
	return rel + "  " + title
}

func (i sessionItem) Description() string {
	color, ok := providerColors[i.summary.Provider]
	lbl := i.providerLbl
	if lbl == "" {
		lbl = i.summary.Provider
	}
	if ok {
		lbl = lipgloss.NewStyle().Foreground(color).Render(lbl)
	}
	proj := util.TildePath(i.summary.ProjectPath)
	runes := []rune(proj)
	if len(runes) > 36 {
		proj = "…" + string(runes[len(runes)-35:])
	}
	desc := fmt.Sprintf("%s · %s · %s", lbl, i.summary.ShortID(), proj)
	if i.snippet != "" && i.snippet != i.summary.Title {
		desc += " · " + truncate(strings.ReplaceAll(i.snippet, "\n", " "), 72)
	}
	return desc
}

func (i sessionItem) FilterValue() string {
	return i.summary.ID + " " + i.summary.Title + " " + i.summary.ProjectPath + " " + i.summary.Provider
}

type providerItem struct {
	id, name string
	count    int
}

func (i providerItem) Title() string {
	return fmt.Sprintf("%s %s", i.name, mutedStyle.Render(fmt.Sprintf("(%d)", i.count)))
}
func (i providerItem) Description() string { return i.id }
func (i providerItem) FilterValue() string { return i.id + " " + i.name }

type targetItem struct{ id, name string }

func (i targetItem) Title() string       { return i.name }
func (i targetItem) Description() string { return mutedStyle.Render(i.id) }
func (i targetItem) FilterValue() string { return i.id + " " + i.name }

type actionItem struct{ id, title, desc string }

func (i actionItem) Title() string       { return i.title }
func (i actionItem) Description() string { return mutedStyle.Render(i.desc) }
func (i actionItem) FilterValue() string { return i.id + " " + i.title }

func actionItems(reg *registry.Registry, sm model.Summary) []list.Item {
	items := []list.Item{
		actionItem{id: "preview", title: "Preview messages", desc: "Read conversation in the right pane"},
		actionItem{id: "migrate", title: "Migrate to another agent", desc: "Hop this session to Codex, Cursor, etc."},
		actionItem{id: "copy-id", title: "Copy session ID", desc: sm.ID},
	}
	if p, err := reg.Get(sm.Provider); err == nil && p.SupportsResume() {
		cmd := p.ResumeCommand(provider.WriteResult{SessionID: sm.ID, ProjectPath: sm.ProjectPath})
		if cmd != "" {
			items = append(items, actionItem{id: "resume", title: "Copy resume command", desc: truncate(cmd, 48)})
		}
	}
	return items
}

type sessionsPageMsg struct {
	items    []list.Item
	total    int
	offset   int
	cwdMode  bool
	provider string
	gen      uint64
	err      error
}
type previewLoadedMsg struct {
	content string
	err     error
}
type migrateDoneMsg struct {
	res *migrate.Result
	err error
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
	reg              *registry.Registry
	idx              *index.Store
	engine           *migrate.Engine
	providers        list.Model
	sessions         list.Model
	actions          list.Model
	targets          list.Model
	preview          viewport.Model
	searchInput      textinput.Model
	spinner          spinner.Model
	stage            int
	backStage        int
	selected         *sessionItem
	loading          bool
	indexing         bool
	contentIndexing  bool
	cwdMode          bool
	pageOffset       int
	pageSize         int
	showAllOnPage    bool
	includeSubagents bool
	searching        bool
	searchQuery      string
	guided           bool
	confirmTarget    string
	totalSessions    int
	providerFilter   string
	cwd              string
	pageGen          uint64
	lastResume       string
	err              string
	status           string
	width            int
	height           int
	ctx              context.Context
	cancel           context.CancelFunc
	previewContent   string
}

func Run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine) error {
	return run(reg, idx, engine, nil, false)
}

// RunMigrate opens the migration picker, optionally preselecting a session.
func RunMigrate(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, sessionID, from string) error {
	var selected *model.Summary
	if sessionID != "" {
		sm, _, err := migrate.ResolveSession(context.Background(), reg, idx, sessionID, from)
		if err != nil {
			return err
		}
		selected = sm
	}
	return run(reg, idx, engine, selected, true)
}

func run(reg *registry.Registry, idx *index.Store, engine *migrate.Engine, initial *model.Summary, guided bool) error {
	cwd, err := os.Getwd()
	cwdMode := true
	if err != nil {
		cwdMode = false
	}
	cwd = util.NormalizeProjectPath(cwd)

	counts, _ := idx.CountByProvider()
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(lipgloss.Color("212"))

	provList := list.New(providerItems(reg, counts), delegate, 42, 22)
	provList.Title = "Filter by agent"
	provList.SetShowStatusBar(false)
	provList.DisableQuitKeybindings()

	sessList := list.New([]list.Item{}, delegate, 72, 22)
	sessList.Title = "Sessions"
	sessList.SetFilteringEnabled(false)
	sessList.SetShowStatusBar(true)
	sessList.DisableQuitKeybindings()

	targetList := list.New([]list.Item{}, delegate, 36, 14)
	targetList.Title = "Migrate to"
	targetList.DisableQuitKeybindings()

	actionList := list.New([]list.Item{}, delegate, 44, 12)
	actionList.Title = "Actions"
	actionList.SetShowStatusBar(false)
	actionList.DisableQuitKeybindings()

	vp := viewport.New(64, 20)
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "search titles and messages"
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := modelState{
		reg: reg, idx: idx, engine: engine,
		providers: provList, sessions: sessList, actions: actionList, targets: targetList,
		preview: vp, searchInput: search, spinner: sp,
		stage: stageSessions, cwdMode: cwdMode, cwd: cwd,
		indexing: index.NeedsIncrementalIndex(reg, idx, 5*time.Minute), pageGen: 1,
		pageSize: 200, guided: guided, ctx: ctx, cancel: cancel,
	}
	m.contentIndexing = !m.indexing
	if initial != nil {
		sel := sessionItem{summary: *initial, providerLbl: registry.DisplayName(reg, initial.Provider)}
		m.selected = &sel
		m.sessions.SetItems([]list.Item{sel})
		m.targets.SetItems(targetItems(reg, initial.Provider))
		m.stage = stageMigrate
		m.cwdMode = false
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, runErr := p.Run()
	return runErr
}

func providerItems(reg *registry.Registry, counts map[string]int) []list.Item {
	var allCount int
	for _, n := range counts {
		allCount += n
	}
	items := []list.Item{providerItem{id: "", name: "All agents", count: allCount}}
	for _, p := range reg.All() {
		if !p.Installed() {
			continue
		}
		items = append(items, providerItem{id: p.ID(), name: p.DisplayName(), count: counts[p.ID()]})
	}
	return items
}

func targetItems(reg *registry.Registry, exclude string) []list.Item {
	var items []list.Item
	for _, p := range reg.All() {
		if !p.Installed() || p.ID() == exclude {
			continue
		}
		items = append(items, targetItem{id: p.ID(), name: p.DisplayName()})
	}
	return items
}

func (m modelState) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen)}
	if m.indexing {
		cmds = append(cmds, backgroundIndexCmd(m.ctx, m.reg, m.idx))
	} else {
		cmds = append(cmds, contentIndexCmd(m.ctx, m.reg, m.idx))
	}
	return tea.Batch(cmds...)
}

func dispatchPageLoad(m modelState) (modelState, tea.Cmd) {
	if m.cwdMode {
		if wd, err := os.Getwd(); err == nil {
			m.cwd = util.NormalizeProjectPath(wd)
		}
	}
	m.pageGen++
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen))
}

func listOptsFor(m modelState) index.ListOpts {
	limit := m.pageSize
	offset := m.pageOffset
	if m.showAllOnPage {
		limit = maxShowAllPage
		offset = 0
	}
	opts := index.ListOpts{
		Provider:         m.providerFilter,
		Limit:            limit,
		Offset:           offset,
		IncludeSubagents: m.includeSubagents,
	}
	if m.cwdMode && m.cwd != "" {
		opts.ProjectCWD = m.cwd
	}
	return opts
}

func loadSessionsPageCmd(m modelState, gen uint64) tea.Cmd {
	reg, idx := m.reg, m.idx
	opts := listOptsFor(m)
	cwdMode := m.cwdMode
	providerFilter := m.providerFilter
	showAll := m.showAllOnPage
	offset := opts.Offset
	return func() tea.Msg {
		total, err := idx.Count(opts)
		if err != nil {
			return sessionsPageMsg{err: err, cwdMode: cwdMode, provider: providerFilter, offset: offset, gen: gen}
		}
		if !showAll && offset >= total && total > 0 {
			ps := opts.Limit
			if ps <= 0 {
				ps = minPageSize
			}
			offset = (total - 1) / ps * ps
			opts.Offset = offset
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
		return sessionsPageMsg{
			items: sitems, total: total, offset: opts.Offset,
			cwdMode: cwdMode, provider: providerFilter, gen: gen, err: err,
		}
	}
}

func (m modelState) gotoStage(stage int) modelState {
	m.backStage = m.stage
	m.stage = stage
	m.layout()
	return m
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

func searchCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, query, providerFilter, cwd string, includeSubagents bool) tea.Cmd {
	return func() tea.Msg {
		hits, err := idx.Search(index.SearchOpts{
			Query: query, Provider: providerFilter, ProjectCWD: cwd,
			IncludeSubagents: includeSubagents, Limit: maxShowAllPage,
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

func loadPreviewCmd(ctx context.Context, reg *registry.Registry, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return previewLoadedMsg{err: err}
		}
		ref := provider.SessionRef{
			ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
		}
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
		b.WriteString(titleStyle.Render(truncate(conv.Title, 60)) + "\n")
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

func migrateCmd(ctx context.Context, engine *migrate.Engine, sm model.Summary, to string) tea.Cmd {
	return func() tea.Msg {
		res, err := engine.Run(ctx, migrate.Options{
			SessionID: sm.ID, FromProvider: sm.Provider, ToProvider: to,
		})
		return migrateDoneMsg{res: res, err: err}
	}
}

func (m modelState) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		newPS := pageSizeForHeight(m.height)
		if newPS != m.pageSize {
			m.pageSize = newPS
			m.pageOffset = 0
			m.layout()
			var cmd tea.Cmd
			m, cmd = dispatchPageLoad(m)
			return m, cmd
		}
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
		m.pageOffset = msg.offset
		if msg.total == 0 {
			m.pageOffset = 0
		}
		m.cwdMode = msg.cwdMode
		m.providerFilter = msg.provider
		m.updateStatusLine()
		m.layout()
		return m, nil
	case previewLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.previewContent = msg.content
		m.stage = stagePreview
		m.layout()
		return m, nil
	case migrateDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.lastResume = msg.res.Resume
		target := msg.res.TargetName
		if target == "" {
			target = "target"
		}
		if msg.res.AlreadyExists {
			m.status = okStyle.Render("Already migrated — resume command ready (press c to copy)")
		} else {
			m.status = okStyle.Render("Migration complete — resume command ready (press c to copy)")
		}
		if len(msg.res.Warnings) > 0 {
			m.status += fmt.Sprintf(" · %d warning(s)", len(msg.res.Warnings))
		}
		if m.backStage == stagePreview || m.backStage == stageMigrate {
			m.backStage = stageSessions
		}
		m.stage = stagePreview
		var b strings.Builder
		b.WriteString(titleStyle.Render("Migrated to "+target) + "\n\n")
		if m.selected != nil {
			b.WriteString(mutedStyle.Render("From: "+registry.DisplayName(m.reg, m.selected.summary.Provider)+" · "+m.selected.summary.ID) + "\n")
		}
		if msg.res.Write != nil {
			b.WriteString(mutedStyle.Render("Session: "+msg.res.Write.SessionID) + "\n")
			b.WriteString(mutedStyle.Render("Path: "+util.TildePath(msg.res.Write.StoragePath)) + "\n\n")
		}
		b.WriteString(accentStyle.Render("Resume command") + "\n")
		b.WriteString(m.lastResume + "\n\n")
		if len(msg.res.Warnings) > 0 {
			b.WriteString(accentStyle.Render("Warnings") + "\n")
			for _, warning := range msg.res.Warnings {
				b.WriteString("- " + warning + "\n")
			}
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render("Press c to copy · esc to go back"))
		m.previewContent = b.String()
		m.preview.SetContent(m.previewContent)
		m.preview.GotoTop()
		m.layout()
		return m, nil
	case indexRefreshedMsg:
		m.indexing = false
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.providers.SetItems(providerItems(m.reg, msg.counts))
		if msg.reloadPage {
			m.contentIndexing = true
			m.updateStatusLine()
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
		m.status = fmt.Sprintf("Content search: %d ready · %d pending · %d error",
			msg.status.Indexed, msg.status.Pending, msg.status.Failed)
		m.layout()
		if m.searchQuery != "" {
			project := ""
			if m.cwdMode {
				project = m.cwd
			}
			return m, searchCmd(m.ctx, m.reg, m.idx, m.searchQuery, m.providerFilter, project, m.includeSubagents)
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
		m.pageOffset = 0
		m.status = fmt.Sprintf("%d results for %q · content %d ready/%d pending/%d error",
			len(msg.items), msg.query, msg.status.Indexed, msg.status.Pending, msg.status.Failed)
		m.layout()
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.searching {
			switch msg.String() {
			case "esc":
				m.searching = false
				m.searchInput.Blur()
				if m.searchQuery != "" {
					m.searchQuery = ""
					m.searchInput.SetValue("")
					var cmd tea.Cmd
					m, cmd = dispatchPageLoad(m)
					return m, cmd
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
				project := ""
				if m.cwdMode {
					project = m.cwd
				}
				return m, tea.Batch(m.spinner.Tick, searchCmd(m.ctx, m.reg, m.idx, query, m.providerFilter, project, m.includeSubagents))
			}
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		if m.loading {
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "/":
			if m.stage == stageSessions {
				m.searching = true
				m.searchInput.Focus()
				m.layout()
				return m, textinput.Blink
			}
			return m, nil
		case "esc":
			m.err = ""
			prev := m.stage
			switch m.stage {
			case stageConfirm:
				m.stage = stageMigrate
				m.confirmTarget = ""
			case stageMigrate:
				m.stage = m.backStage
			case stagePreview:
				m.stage = m.backStage
				if m.backStage == stageSessions {
					m.preview.SetContent("")
				}
			case stageActions:
				m.stage = stageSessions
				m.selected = nil
			case stageProviders:
				m.stage = stageSessions
			default:
				if m.stage == stageSessions && m.searchQuery != "" {
					m.searchQuery = ""
					m.searchInput.SetValue("")
					m.pageOffset = 0
					var cmd tea.Cmd
					m, cmd = dispatchPageLoad(m)
					return m, cmd
				}
				m.status = ""
			}
			if m.stage != prev {
				m.layout()
			}
			return m, nil
		case "c":
			if m.lastResume != "" {
				_ = clipboard.WriteAll(m.lastResume)
				m.status = okStyle.Render("Copied resume command to clipboard")
			}
			return m, nil
		case "w":
			if wd, err := os.Getwd(); err == nil {
				m.cwd = util.NormalizeProjectPath(wd)
			}
			if !m.cwdMode {
				m.cwdMode = true
				m.pageOffset = 0
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			m.pageOffset = 0
			var cmd tea.Cmd
			m, cmd = dispatchPageLoad(m)
			return m, cmd
		case "a":
			if m.cwdMode {
				m.cwdMode = false
				m.pageOffset = 0
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			return m, nil
		case "0":
			if m.stage == stageSessions {
				m.showAllOnPage = !m.showAllOnPage
				m.pageOffset = 0
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			return m, nil
		case "[", "pgup":
			if m.stage == stageSessions && !m.showAllOnPage && m.pageOffset >= m.pageSize {
				m.pageOffset -= m.pageSize
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			return m, nil
		case "]", "pgdown":
			if m.stage == stageSessions && !m.showAllOnPage && m.pageOffset+m.pageSize < m.totalSessions {
				m.pageOffset += m.pageSize
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			return m, nil
		case "p":
			if m.stage == stageSessions {
				m.stage = stageProviders
				m.layout()
			}
			return m, nil
		case "r":
			m.indexing = true
			m.loading = true
			filter := m.providerFilter
			return m, tea.Batch(m.spinner.Tick, refreshIndexCmd(m.ctx, m.reg, m.idx, filter, true))
		case "s":
			if m.stage == stageSessions {
				m.includeSubagents = !m.includeSubagents
				m.pageOffset = 0
				m.searchQuery = ""
				m.searchInput.SetValue("")
				var cmd tea.Cmd
				m, cmd = dispatchPageLoad(m)
				return m, cmd
			}
			return m, nil
		case "y":
			if m.stage == stageConfirm && m.selected != nil && m.confirmTarget != "" {
				m.loading = true
				m.err = ""
				return m, tea.Batch(m.spinner.Tick, migrateCmd(m.ctx, m.engine, m.selected.summary, m.confirmTarget))
			}
			return m, nil
		case "n":
			if m.stage == stageConfirm {
				m.stage = stageMigrate
				m.confirmTarget = ""
				m.layout()
			}
			return m, nil
		case "enter":
			switch m.stage {
			case stageProviders:
				if it, ok := m.providers.SelectedItem().(providerItem); ok {
					m.providerFilter = it.id
					m.pageOffset = 0
					m.stage = stageSessions
					var cmd tea.Cmd
					m, cmd = dispatchPageLoad(m)
					return m, cmd
				}
			case stageSessions:
				if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
					sel := it
					m.selected = &sel
					if m.guided {
						m.targets.SetItems(targetItems(m.reg, it.summary.Provider))
						m = m.gotoStage(stageMigrate)
						return m, nil
					}
					m.backStage = stageSessions
					m.loading = true
					return m, tea.Batch(m.spinner.Tick, loadPreviewCmd(m.ctx, m.reg, m.selected.summary))
				}
			case stageActions:
				if m.selected == nil {
					return m, nil
				}
				if act, ok := m.actions.SelectedItem().(actionItem); ok {
					switch act.id {
					case "preview":
						m.backStage = stageActions
						m.loading = true
						return m, tea.Batch(m.spinner.Tick, loadPreviewCmd(m.ctx, m.reg, m.selected.summary))
					case "migrate":
						m.targets.SetItems(targetItems(m.reg, m.selected.summary.Provider))
						m = m.gotoStage(stageMigrate)
						return m, nil
					case "copy-id":
						_ = clipboard.WriteAll(m.selected.summary.ID)
						m.status = okStyle.Render("Copied session ID: " + m.selected.summary.ID)
						return m, nil
					case "resume":
						if p, err := m.reg.Get(m.selected.summary.Provider); err == nil {
							cmd := p.ResumeCommand(provider.WriteResult{
								SessionID: m.selected.summary.ID, ProjectPath: m.selected.summary.ProjectPath,
							})
							if cmd != "" {
								_ = clipboard.WriteAll(cmd)
								m.lastResume = cmd
								m.status = okStyle.Render("Copied resume command to clipboard")
							}
						}
						return m, nil
					}
				}
			case stageMigrate:
				if m.selected == nil {
					return m, nil
				}
				if tgt, ok := m.targets.SelectedItem().(targetItem); ok {
					m.confirmTarget = tgt.id
					m.stage = stageConfirm
					m.layout()
					return m, nil
				}
			}
			return m, nil
		case "m":
			if m.stage == stageSessions {
				if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
					sel := it
					m.selected = &sel
					m.targets.SetItems(targetItems(m.reg, it.summary.Provider))
					m = m.gotoStage(stageMigrate)
				}
			} else if m.stage == stagePreview && m.selected != nil {
				m.targets.SetItems(targetItems(m.reg, m.selected.summary.Provider))
				m.stage = stageMigrate
				m.layout()
			} else if m.stage == stageActions && m.selected != nil {
				m.targets.SetItems(targetItems(m.reg, m.selected.summary.Provider))
				m = m.gotoStage(stageMigrate)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.loading {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	switch m.stage {
	case stageProviders:
		m.providers, cmd = m.providers.Update(msg)
	case stageSessions:
		m.sessions, cmd = m.sessions.Update(msg)
	case stageActions:
		m.actions, cmd = m.actions.Update(msg)
	case stageMigrate:
		m.targets, cmd = m.targets.Update(msg)
	default:
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m *modelState) updateStatusLine() {
	start := m.pageOffset + 1
	end := m.pageOffset + len(m.sessions.Items())
	if end == 0 && m.totalSessions == 0 {
		m.status = "No sessions in index"
		if m.indexing {
			m.status += " · indexing…"
		}
		if m.contentIndexing {
			m.status += " · content indexing…"
		}
		return
	}
	if end == 0 {
		end = m.pageOffset
		start = 0
	}
	filter := "everywhere"
	if m.cwdMode {
		if util.HomeDir() != "" && m.cwd == util.HomeDir() {
			filter = "here (home projects)"
		} else {
			filter = "here"
		}
	}
	prov := "all agents"
	if m.providerFilter != "" {
		prov = registry.DisplayName(m.reg, m.providerFilter)
	}
	m.status = fmt.Sprintf("Showing %d–%d of %d sessions · filter %s · agent %s", start, end, m.totalSessions, filter, prov)
	if m.showAllOnPage {
		if m.totalSessions > len(m.sessions.Items()) {
			m.status += fmt.Sprintf(" · loaded %d (cap %d)", len(m.sessions.Items()), maxShowAllPage)
		} else {
			m.status += " · all on screen"
		}
	} else if m.totalSessions > m.pageSize {
		page := m.pageOffset/m.pageSize + 1
		pages := (m.totalSessions + m.pageSize - 1) / m.pageSize
		m.status += fmt.Sprintf(" · %d/page · page %d/%d · 0 all · [/] page", m.pageSize, page, pages)
	}
	if m.indexing {
		m.status += " · indexing…"
	}
	if m.contentIndexing {
		m.status += " · content indexing…"
	}
}

func (m *modelState) layout() {
	if m.width < 40 || m.height < 12 {
		return
	}
	frameW, frameH := paneStyle.GetHorizontalFrameSize(), paneStyle.GetVerticalFrameSize()
	m.searchInput.Width = max(8, m.width-4)
	for _, l := range []*list.Model{&m.sessions, &m.providers, &m.actions, &m.targets} {
		l.SetShowHelp(false)
		l.SetShowPagination(false)
		l.SetShowStatusBar(false)
	}
	headerH := lipgloss.Height(m.headerView())
	paneOuterH := max(frameH+1, m.height-headerH-3)
	h := max(1, paneOuterH-frameH)
	wide := m.width >= 100
	switch m.stage {
	case stageProviders:
		m.providers.SetSize(max(1, m.width-frameW), h)
	case stageSessions:
		m.sessions.SetSize(max(1, m.width-frameW), h)
	case stageActions, stageMigrate:
		left := 0
		if wide {
			left = m.width / 2
			m.sessions.SetSize(max(1, left-frameW), h)
		}
		right := m.width - left
		if m.stage == stageActions {
			m.actions.SetSize(max(1, right-frameW), h)
		} else {
			m.targets.SetSize(max(1, right-frameW), h)
		}
	case stagePreview, stageConfirm:
		left := 0
		if wide {
			left = m.width / 3
			m.sessions.SetSize(max(1, left-frameW), h)
		}
		pw := m.width - left
		m.preview.Width = max(1, pw-frameW)
		m.preview.Height = h
		if m.stage == stageConfirm {
			m.previewContent = m.confirmationView()
		}
		y := m.preview.YOffset
		m.preview.SetContent(ansi.Hardwrap(m.previewContent, m.preview.Width, false))
		m.preview.SetYOffset(y)
	}
}

func (m modelState) filterChips() string {
	here := chipMuted.Render("here")
	everywhere := chipMuted.Render("everywhere")
	if m.cwdMode {
		here = chipActive.Render("here")
	} else {
		everywhere = chipActive.Render("everywhere")
	}
	return here + " " + everywhere
}

func (m modelState) View() string {
	if m.width < 40 || m.height < 12 {
		return ansi.Truncate("Terminal too small — resize to at least 40x12", max(1, m.width), "")
	}
	header := m.headerView()
	frameW, frameH := paneStyle.GetHorizontalFrameSize(), paneStyle.GetVerticalFrameSize()
	paneOuterH := max(frameH+1, m.height-lipgloss.Height(header)-3)
	contentH := max(1, paneOuterH-frameH)
	renderPane := func(content string, outerW int) string {
		w := max(1, outerW-frameW)
		return paneStyle.Width(w).Height(contentH).MaxWidth(w).MaxHeight(contentH).Render(content)
	}
	wide := m.width >= 100
	var pane string
	switch m.stage {
	case stageProviders:
		pane = renderPane(m.providers.View(), m.width)
	case stageSessions:
		pane = renderPane(m.sessions.View(), m.width)
	case stageActions:
		if wide {
			left := m.width / 2
			pane = lipgloss.JoinHorizontal(lipgloss.Top, renderPane(m.sessions.View(), left), renderPane(m.actions.View(), m.width-left))
		} else {
			pane = renderPane(m.actions.View(), m.width)
		}
	case stageMigrate:
		if wide {
			left := m.width / 2
			pane = lipgloss.JoinHorizontal(lipgloss.Top, renderPane(m.sessions.View(), left), renderPane(m.targets.View(), m.width-left))
		} else {
			pane = renderPane(m.targets.View(), m.width)
		}
	case stagePreview, stageConfirm:
		if wide {
			left := m.width / 3
			pane = lipgloss.JoinHorizontal(lipgloss.Top, renderPane(m.sessions.View(), left), renderPane(m.preview.View(), m.width-left))
		} else {
			pane = renderPane(m.preview.View(), m.width)
		}
	}
	footer := footerStyle.Width(max(1, m.width-footerStyle.GetHorizontalFrameSize())).Render(
		ansi.Truncate(m.footerHelp(), max(1, m.width-footerStyle.GetHorizontalFrameSize()), "…"))
	return lipgloss.JoinVertical(lipgloss.Left, header, pane, footer)
}

func (m modelState) headerView() string {
	var lines []string
	if m.width >= 80 && m.height >= 24 {
		lines = append(lines, strings.TrimRight(renderBanner(), "\n"))
	}
	mode := "session browser"
	if m.guided {
		mode = "guided migration"
	}
	line := mutedStyle.Render("  "+mode) + "  " + m.filterChips()
	if m.includeSubagents {
		line += "  " + chipActive.Render("subagents")
	}
	lines = append(lines, ansi.Truncate(line, m.width, "…"))
	if m.cwdMode && m.cwd != "" {
		lines = append(lines, ansi.Truncate(mutedStyle.Render("  "+util.TildePath(m.cwd)), m.width, "…"))
	}
	if m.searching {
		lines = append(lines, m.searchInput.View())
	}
	if m.loading {
		lines = append(lines, m.spinner.View()+mutedStyle.Render(" working…"))
	}
	if m.status != "" {
		lines = append(lines, ansi.Truncate(m.status, m.width, "…"))
	}
	if m.err != "" {
		lines = append(lines, ansi.Truncate(errStyle.Render("✗ "+m.err), m.width, "…"))
	}
	return strings.Join(lines, "\n")
}

func (m modelState) confirmationView() string {
	name := registry.DisplayName(m.reg, m.confirmTarget)
	return titleStyle.Render("Confirm migration") + "\n\n" +
		fmt.Sprintf("Migrate %s to %s?\n\n", m.selected.summary.ShortID(), name) +
		accentStyle.Render("y") + " confirm  " + mutedStyle.Render("n/esc cancel")
}

func (m modelState) footerHelp() string {
	switch m.stage {
	case stageActions:
		return "↑↓ navigate · enter run action · m migrate · esc back · q quit"
	case stagePreview:
		return "↑↓ scroll preview · m migrate · c copy resume · esc back · q quit"
	case stageMigrate:
		return "↑↓ pick target · enter review · esc back · q quit"
	case stageConfirm:
		return "y confirm migration · n/esc cancel · q quit"
	case stageProviders:
		return "↑↓ pick agent · enter filter · esc back · q quit"
	default:
		return "↑↓ navigate · enter preview · / search · s subagents · w here · a all · [/] page · p agent · m migrate · r refresh · q quit"
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
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
