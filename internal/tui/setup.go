package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/integrations"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

// Setup runs as three pages: which agents another manages, which of those
// agents writes title suggestions, then which model it writes them with. Each
// page can only offer what the previous one kept, so they are built on the way
// in rather than up front.
const (
	setupPageAgents = iota
	setupPageTitle
	setupPageModel
)

type setupItem struct {
	id, name  string
	command   string
	sessions  int
	data, cli bool
	available bool
	// adapter marks the second tier: agents another keeps working but does not
	// test end to end every release. They sit behind a fold so ten rows do not
	// bury the six that are.
	adapter bool
}

// foldRow is the row index sentinel for the fold itself, which belongs to no
// agent. Page one is a list of agents plus this one control, and the cursor
// walks all of them.
const foldRow = -1

// titleOption is one row on the title page. The zero row disables the feature.
type titleOption struct {
	id, name, command string
}

// SetupPlugin describes the adapter another can install into OpenCode 2, as it
// exists before setup runs. Setup asks about it rather than acting on
// detection: the files land in an agent's own configuration directory, which
// another may write to only because a person said so on this page.
type SetupPlugin struct {
	// Supported is false when there is no OpenCode 2 to install into, which
	// hides the row entirely.
	Supported bool
	Dir       string
	State     integrations.State
}

// Known reports whether the adapter has been looked up yet. Finding it means
// asking OpenCode 2 where its configuration lives, which is a subprocess and
// occasionally a service start, so setup draws before the answer arrives and
// the row says so until it does.
func (p SetupPlugin) Known() bool { return p.State != "" }

// pluginStatusMsg carries one finished lookup.
type pluginStatusMsg struct{ plugin SetupPlugin }

func pluginStatusCmd(probe func() SetupPlugin) tea.Cmd {
	if probe == nil {
		return nil
	}
	return func() tea.Msg { return pluginStatusMsg{plugin: probe()} }
}

type setupModel struct {
	items     []setupItem
	selected  map[string]bool
	cursor    int
	width     int
	height    int
	done      bool
	cancelled bool
	err       string
	spinner   spinner.Model

	// showAdapters opens the second tier. It starts open when one of those
	// agents is already selected: a setting that cannot be seen cannot be
	// turned off.
	showAdapters bool

	page        int
	titleOpts   []titleOption
	titleCursor int
	modelInput  textinput.Model
	langCursor  int
	// plugin is the OpenCode 2 adapter row and whether it is switched on.
	// pluginTouched keeps a lookup that lands late from overriding an answer
	// the person has already given on the row.
	plugin        SetupPlugin
	pluginWanted  bool
	pluginTouched bool
	pluginProbe   func() SetupPlugin
	// pluginConsented is the saved answer from an earlier run, which decides
	// the row's default once the lookup arrives.
	pluginConsented bool
	// uiLangCursor is the interface language, which page one both sets and
	// immediately demonstrates: the page redraws in the language under the
	// cursor, so the choice is verified by making it.
	uiLangCursor int

	// model* is the picker page. modelOpts is what the agent CLI itself
	// reported; typing filters it, and the last row falls back to a name
	// typed by hand for CLIs that cannot list or models too new to appear.
	modelOpts    []string
	modelCursor  int
	modelFilter  string
	modelErr     string
	modelLoading bool
	modelTyping  bool
	// modelFor is the agent the current listing belongs to, so a slow
	// listing cannot land on a page that has since changed agents.
	modelFor string
}

// modelsLoadedMsg carries one finished listing.
type modelsLoadedMsg struct {
	provider string
	models   []string
	err      error
}

// listModelsCmd asks the agent CLI for its own model list. A failure is not
// fatal: the page falls back to typing a name.
func listModelsCmd(provider string) tea.Cmd {
	return func() tea.Msg {
		models, err := titler.ListModels(context.Background(), provider)
		return modelsLoadedMsg{provider: provider, models: models, err: err}
	}
}

// languages is the order the title page cycles through. Auto is the product
// default: titles follow the first meaningful user message unless overridden.
var languages = []titler.Language{titler.LangAuto, titler.LangEnglish, titler.LangChinese}

// uiLanguages is the same order for the interface, where auto means the
// terminal's locale rather than the session's content.
var uiLanguages = []i18n.Lang{i18n.LangAuto, i18n.LangEnglish, i18n.LangChinese}

// RunSetup lets a person choose which agents another should index and expose,
// plus the two language settings. It takes and returns whole settings so a
// preference this page does not touch survives being edited here. The caller
// persists the result only after the program exits cleanly.
func RunSetup(reg *registry.Registry, counts map[string]int, initial config.Settings, plugin SetupPlugin, probe func() SetupPlugin) (config.Settings, bool, error) {
	initialTitle := initial.TitleModel
	initialPolicy := initial.TitlePolicy
	chosen := initialSetupSelection(initial.EnabledProviders)
	var items []setupItem
	for _, p := range reg.All() {
		data := p.Installed()
		cli := registry.CLIAvailable(p.ID())
		item := setupItem{
			id: p.ID(), name: p.DisplayName(), command: registry.CLICommand(p.ID()),
			sessions: counts[p.ID()], data: data, cli: cli, available: data || cli,
			adapter: registry.IsCompatibilityAdapter(p.ID()),
		}
		items = append(items, item)
	}
	// The fold needs the two tiers contiguous; a saved display order is kept
	// inside each tier but cannot interleave them.
	sort.SliceStable(items, func(i, j int) bool { return !items[i].adapter && items[j].adapter })
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle
	modelInput := textinput.New()
	modelInput.Prompt = ""
	modelInput.Placeholder = txt.modelPlaceholder
	modelInput.CharLimit = 120
	start := setupModel{items: items, selected: chosen, spinner: sp, modelInput: modelInput, plugin: plugin}
	start.pluginProbe = probe
	start.pluginConsented = initial.Integrations.OpenCode2TitlePolicy
	start.pluginWanted = pluginDefault(plugin, start.pluginConsented)
	start.showAdapters = anyAdapterSelected(items, chosen)
	start.langCursor = languageCursor(titler.Language(initialPolicy.Language))
	start.uiLangCursor = uiLanguageCursor(i18n.Lang(initial.UI.Language))
	if initialTitle != nil {
		start.modelInput.SetValue(initialTitle.Model)
		start.titleCursor = -1 // resolved once the option list exists
		start.titleOpts = []titleOption{{id: initialTitle.Provider}}
		start.langCursor = languageCursor(titler.Language(initialTitle.Language))
	}
	// The page renders in the language it is about to offer to change, so the
	// interface it opens in is the one already configured.
	restoreLanguage := applyLanguage(i18n.Lang(initial.UI.Language))
	program := tea.NewProgram(start, tea.WithAltScreen())
	restoreInputSource := temporarilyUseASCIIInputSource()
	defer restoreInputSource()
	final, err := program.Run()
	restoreInputSource()
	// Setup either hands over to the browser, which sets its own title, or
	// returns to the shell; neither should inherit the setup screen's title.
	restoreWindowTitle(os.Stdout)
	if err != nil {
		applyLanguage(restoreLanguage)
		return config.Settings{}, false, err
	}
	model, ok := final.(setupModel)
	if !ok || model.cancelled {
		// A cancelled run changes nothing, including the language the rest of
		// this process speaks.
		applyLanguage(restoreLanguage)
		return config.Settings{}, false, nil
	}
	var enabled []string
	for _, item := range model.items {
		if model.selected[item.id] {
			enabled = append(enabled, item.id)
		}
	}
	saved := initial
	saved.EnabledProviders = enabled
	saved.TitleModel = model.titleModel()
	saved.TitlePolicy = config.TitlePolicy{Language: string(model.language())}
	saved.UI = config.UI{Language: string(model.uiLanguage())}
	saved.Integrations.OpenCode2TitlePolicy = model.wantsPlugin()
	return saved, model.done, nil
}

// anyAdapterSelected reports whether the saved configuration already enables a
// second-tier agent, which is what decides if the fold starts open.
func anyAdapterSelected(items []setupItem, selected map[string]bool) bool {
	for _, item := range items {
		if item.adapter && selected[item.id] {
			return true
		}
	}
	return false
}

// rows is what page one actually draws and what the cursor walks: every
// first-tier agent, the fold, and the second tier when it is open. A row holds
// an index into items, or foldRow for the control itself.
func (m setupModel) rows() []int {
	rows := make([]int, 0, len(m.items)+1)
	folded := 0
	for i, item := range m.items {
		if !item.adapter {
			rows = append(rows, i)
			continue
		}
		folded++
	}
	if folded == 0 {
		return rows
	}
	rows = append(rows, foldRow)
	if !m.showAdapters {
		return rows
	}
	for i, item := range m.items {
		if item.adapter {
			rows = append(rows, i)
		}
	}
	return rows
}

// foldedCount is how many agents the fold is currently holding back.
func (m setupModel) foldedCount() int {
	n := 0
	for _, item := range m.items {
		if item.adapter {
			n++
		}
	}
	return n
}

// currentItem resolves the cursor to an agent, or reports false on the fold.
func (m setupModel) currentItem() (int, bool) {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor] == foldRow {
		return 0, false
	}
	return rows[m.cursor], true
}

// initialSetupSelection preserves an existing explicit configuration. A first
// run starts empty: detecting an agent on disk is evidence that it is available,
// not that the person wants another to index and expose it.
func initialSetupSelection(initial []string) map[string]bool {
	chosen := make(map[string]bool, len(initial))
	for _, id := range initial {
		chosen[registry.NormalizeID(id)] = true
	}
	return chosen
}

// titleModel reads the chosen suggestion agent back out. Row 0 is "off", and
// so is any state where the page never offered a usable agent.
func (m setupModel) titleModel() *config.TitleModel {
	if m.titleCursor <= 0 || m.titleCursor >= len(m.titleOpts) {
		return nil
	}
	opt := m.titleOpts[m.titleCursor]
	if opt.id == "" {
		return nil
	}
	return &config.TitleModel{
		Provider: opt.id,
		Model:    m.selectedModel(),
		Language: string(m.language()),
	}
}

// setupOpenCode2 is the one agent whose native naming another can adapt today.
const setupOpenCode2 = "opencode2"

// pluginDefault decides where the row starts. A plugin another already
// maintains is already consented to, so it opens on; everything else starts
// off, including a hand-copied installation this run would only be adopting.
func pluginDefault(plugin SetupPlugin, consented bool) bool {
	if !plugin.Supported {
		return false
	}
	return consented || plugin.State == integrations.StateCurrent || plugin.State == integrations.StateOutdated
}

// pluginVisible reports whether the row belongs on the page. It is tied to the
// agent it writes into: someone who does not let another manage OpenCode 2 is
// not being asked about OpenCode 2's plugins.
func (m setupModel) pluginVisible() bool {
	return m.plugin.Supported && m.selected[setupOpenCode2]
}

// pluginToggleable reports whether the row is a choice rather than a report.
// Files another did not write are shown but never claimed from this page; the
// command line can force that, where the person has said what they mean.
func (m setupModel) pluginToggleable() bool {
	return m.pluginVisible() && m.plugin.Known() && !m.plugin.State.Blocked()
}

// wantsPlugin is the answer setup saves and acts on. A row the page could not
// offer keeps the answer the last run gave: saving quickly, before the lookup
// lands, or while files another did not write sit in the way, is not a way to
// withdraw a choice that was made deliberately.
func (m setupModel) wantsPlugin() bool {
	if !m.pluginVisible() {
		return false
	}
	if !m.pluginToggleable() {
		return m.pluginConsented
	}
	return m.pluginWanted
}

// pluginRow draws the adapter as one line: the switch, what it does, what is
// on disk now, and where that is. The path is on the row because this is the
// moment another asks to write outside its own configuration.
func (m setupModel) pluginRow(width int) string {
	mark := mutedStyle.Render("○")
	if m.wantsPlugin() {
		mark = okStyle.Render("●")
	}
	state := txt.setupPluginMissing
	switch m.plugin.State {
	case "":
		state = txt.setupPluginChecking
	case integrations.StateCurrent:
		state = txt.setupPluginCurrent
	case integrations.StateOutdated:
		state = txt.setupPluginOutdated
	case integrations.StateModified:
		state = txt.setupPluginModified
	case integrations.StateAdoptable:
		state = txt.setupPluginAdoptable
	case integrations.StateForeign:
		state = txt.setupPluginForeign
	}
	line := mark + " " + mutedStyle.Render(txt.setupPluginLabel+"  ·  "+state)
	if m.pluginToggleable() {
		line += mutedStyle.Render("  ·  " + txt.setupPluginToggle)
	}
	row := ansi.Truncate(line, width, "…")
	if m.plugin.Dir == "" {
		return row
	}
	return row + "\n" + ansi.Truncate(mutedStyle.Render("   "+util.TildePath(m.plugin.Dir)), width, "…")
}

// language reads the chosen title language back out.
func (m setupModel) language() titler.Language {
	if m.langCursor < 0 || m.langCursor >= len(languages) {
		return titler.LangChinese
	}
	return languages[m.langCursor]
}

// languageCursor puts the cursor back on a previously saved language.
func languageCursor(lang titler.Language) int {
	want := titler.NormalizeLanguage(lang)
	for i, l := range languages {
		if l == want {
			return i
		}
	}
	return 0
}

// uiLanguage reads the chosen interface language back out.
func (m setupModel) uiLanguage() i18n.Lang {
	if m.uiLangCursor < 0 || m.uiLangCursor >= len(uiLanguages) {
		return i18n.LangAuto
	}
	return uiLanguages[m.uiLangCursor]
}

// uiLanguageCursor puts the cursor back on a previously saved interface
// language. An unset preference lands on auto, which is where it started.
func uiLanguageCursor(lang i18n.Lang) int {
	want := i18n.Normalize(lang)
	for i, l := range uiLanguages {
		if l == want {
			return i
		}
	}
	return 0
}

// titleOptions offers only agents that survived page one and can actually run
// a one-shot prompt right now. Listing an agent whose CLI is missing would
// only produce a failure at rename time.
func titleOptions(items []setupItem, selected map[string]bool) []titleOption {
	opts := []titleOption{{name: txt.setupTitleOff}}
	for _, item := range items {
		if !selected[item.id] || !titler.Available(item.id) {
			continue
		}
		opts = append(opts, titleOption{id: item.id, name: item.name, command: titler.Command(item.id)})
	}
	return opts
}

// restoreTitleCursor puts the cursor back on a previously saved agent.
func restoreTitleCursor(opts []titleOption, previous []titleOption) int {
	if len(previous) != 1 {
		return 0
	}
	for i, opt := range opts {
		if opt.id != "" && opt.id == previous[0].id {
			return i
		}
	}
	return 0
}

func (m setupModel) Init() tea.Cmd {
	// The lookup starts with page one, which is where the time it takes is
	// free: the row it fills in belongs to page two.
	return tea.Batch(tea.HideCursor, tea.SetWindowTitle(setupWindowTitle), pluginStatusCmd(m.pluginProbe))
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Setup is centred in the window, so a resize moves the whole panel and
		// leaves the old one behind unless the screen is cleared with it.
		return m, tea.ClearScreen
	case pluginStatusMsg:
		m.plugin = msg.plugin
		if !m.pluginTouched {
			m.pluginWanted = pluginDefault(m.plugin, m.pluginConsented)
		}
		return m, nil
	case modelsLoadedMsg:
		if msg.provider != m.modelFor {
			return m, nil
		}
		m.modelLoading = false
		if msg.err != nil {
			// A CLI that cannot answer right now is not a dead end: the page
			// says why and falls back to typing a name.
			m.modelErr = listErrorText(msg.err)
			m.modelTyping = true
			m.modelInput.Focus()
			return m, textinput.Blink
		}
		m.modelOpts = msg.models
		m.modelCursor = modelCursorFor(m.modelRows(), m.modelInput.Value())
		return m, nil
	case tea.KeyMsg:
		switch m.page {
		case setupPageTitle:
			return m.updateTitlePage(msg)
		case setupPageModel:
			return m.updateModelPage(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "shift+up":
			// Order is only meaningful within a tier, and the fold marks that
			// boundary: an agent cannot be dragged across it.
			return m.reorder(-1), nil
		case "shift+down":
			return m.reorder(1), nil
		case "left", "right":
			// The interface language sits above the list rather than in it:
			// the cursor belongs to the agents, and arrows are free here
			// because page one never moves horizontally.
			step := 1
			if msg.String() == "left" {
				step = -1
			}
			m.uiLangCursor = (m.uiLangCursor + step + len(uiLanguages)) % len(uiLanguages)
			applyLanguage(m.uiLanguage())
			m.err = ""
			return m, nil
		case "up", "k":
			if n := len(m.rows()); n > 0 {
				m.cursor = (m.cursor - 1 + n) % n
			}
		case "down", "j":
			if n := len(m.rows()); n > 0 {
				m.cursor = (m.cursor + 1) % n
			}
		case " ":
			index, ok := m.currentItem()
			if !ok {
				// Space on the fold is the same gesture as space on an agent:
				// act on the row under the cursor.
				m.showAdapters = !m.showAdapters
				m.err = ""
				return m, nil
			}
			item := m.items[index]
			if !item.available {
				m.err = fmt.Sprintf(txt.setupUnavailable, item.name)
				return m, nil
			}
			m.selected[item.id] = !m.selected[item.id]
			m.err = ""
		case "enter":
			if selectedCount(m.selected) == 0 {
				m.err = txt.setupPickOne
				return m, nil
			}
			m.err = ""
			previous := m.titleOpts
			m.titleOpts = titleOptions(m.items, m.selected)
			m.titleCursor = restoreTitleCursor(m.titleOpts, previous)
			m.page = setupPageTitle
			m.modelInput.Focus()
			return m, textinput.Blink
		}
	}
	return m, nil
}

// updateTitlePage picks the agent and the language. The model moved to its own
// page once it became a list pulled from the CLI rather than a typed string.
func (m setupModel) updateTitlePage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	case "esc":
		m.page = setupPageAgents
		m.err = ""
		m.modelInput.Blur()
		return m, nil
	case "up", "shift+tab":
		if n := len(m.titleOpts); n > 0 {
			m.titleCursor = (m.titleCursor - 1 + n) % n
		}
		return m, nil
	case "down", "tab":
		if n := len(m.titleOpts); n > 0 {
			m.titleCursor = (m.titleCursor + 1) % n
		}
		return m, nil
	case "left":
		// Arrows pick the language so the model field can keep every
		// printable key for typing.
		m.langCursor = (m.langCursor - 1 + len(languages)) % len(languages)
		return m, nil
	case "right":
		m.langCursor = (m.langCursor + 1) % len(languages)
		return m, nil
	case "t":
		// The model name is typed on its own page, so a letter is free here
		// and the switch does not need a row of its own in the list.
		if m.pluginToggleable() {
			m.pluginWanted = !m.pluginWanted
			m.pluginTouched = true
		}
		return m, nil
	case "enter":
		if m.titleCursor <= 0 {
			// Suggestions are off, so there is no model to choose.
			m.done = true
			return m, tea.Quit
		}
		next, cmd := m.openModelPage()
		return next, cmd
	}
	return m, nil
}

// reorder moves the agent under the cursor one row up or down, refusing to
// cross the fold or either end.
func (m setupModel) reorder(step int) setupModel {
	rows := m.rows()
	target := m.cursor + step
	if m.cursor < 0 || m.cursor >= len(rows) || target < 0 || target >= len(rows) {
		return m
	}
	from, to := rows[m.cursor], rows[target]
	if from == foldRow || to == foldRow {
		return m
	}
	m.items[from], m.items[to] = m.items[to], m.items[from]
	m.cursor = target
	return m
}

func selectedCount(selected map[string]bool) int {
	n := 0
	for _, yes := range selected {
		if yes {
			n++
		}
	}
	return n
}

func (m setupModel) View() string {
	if m.width < 48 || m.height < 20 {
		return ansi.Truncate(txt.terminalTooSmall, max(1, m.width), "")
	}
	// Two different widths, and using one for the other is what wraps rows.
	// lipgloss counts padding inside Width and the border outside it, so the
	// panel is asked for one number while text is cut to another: a row cut to
	// the panel width wraps inside the padding, and every wrapped row costs the
	// page a line it did not budget for.
	panelW := min(72, m.width-8) - modalStyle.GetHorizontalBorderSize()
	width := panelW - modalStyle.GetHorizontalPadding()
	switch m.page {
	case setupPageTitle:
		panel := modalStyle.Width(panelW).Render(m.titlePageBody(panelW, width))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
	case setupPageModel:
		panel := modalStyle.Width(panelW).Render(m.modelPageBody(width))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
	}
	head := accentStyle.Render("another setup") + "\n" +
		titleStyle.Render(txt.setupAgentsTitle) + "  " +
		mutedStyle.Render(fmt.Sprintf("%d / %d", selectedCount(m.selected), len(m.items))) + "\n" +
		mutedStyle.Render(txt.setupAgentsHint) + "\n" +
		mutedStyle.Render(txt.setupInterface) + "  " + m.uiLanguageRow() + "\n"
	foot := "\n"
	if m.err != "" {
		foot += errStyle.Render("✗ "+m.err) + "\n"
	}
	foot += mutedStyle.Render(txt.setupAgentsHelp)

	rows := m.rows()
	// The panel is centred in the terminal, so a body one line too tall does not
	// clip: the terminal scrolls, and from then on every frame lands lower than
	// the one before it. The list scrolls instead, and how many rows fit is
	// measured on the real panel rather than counted by hand, because the help
	// line wraps at narrow widths and the frame owns four more lines.
	budget := m.height - lipgloss.Height(modalStyle.Width(panelW).Render(head+foot))
	hidden := len(rows) - budget
	if hidden > 0 {
		budget-- // the "+ N more" line has to fit too
	}
	start, end := modelWindow(m.cursor, len(rows), max(1, budget))

	var body strings.Builder
	body.WriteString(head)
	for row := start; row < end; row++ {
		index := rows[row]
		cursor := "  "
		if row == m.cursor {
			cursor = "› "
		}
		if index == foldRow {
			body.WriteString(ansi.Truncate(cursor+m.foldLine(row == m.cursor), width, "…") + "\n")
			continue
		}
		item := m.items[index]
		mark := mutedStyle.Render("○")
		if m.selected[item.id] {
			mark = okStyle.Render("●")
		}
		name := padRight(item.name, txt.setupNameWidth)
		if color, ok := providerColors[item.id]; ok {
			name = lipgloss.NewStyle().Foreground(color).Bold(row == m.cursor).Render(name)
		}
		// Setup is where an agent is met for the first time, so the chip the
		// session list will use is shown next to the name it stands for.
		name = renderAgentChip(item.id) + " " + name
		cli := txt.setupCLIMissing
		if item.cli {
			cli = txt.setupCLIFound
		}
		data := fmt.Sprintf(txt.setupSessionsFmt, item.sessions)
		if !item.data && item.sessions == 0 {
			data = txt.setupNoData
		}
		line := cursor + mark + " " + name + "  " + padRight(cli, txt.setupCLIWidth) + "  " + data
		if !item.available {
			line = mutedStyle.Render(line)
		}
		body.WriteString(ansi.Truncate(line, width, "…") + "\n")
	}
	if n := len(rows) - (end - start); n > 0 {
		body.WriteString(ansi.Truncate(mutedStyle.Render(fmt.Sprintf(txt.setupMoreRowsFmt, n)), width, "…") + "\n")
	}
	body.WriteString(foot)
	panel := modalStyle.Width(panelW).Render(body.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// foldLine draws the one row that is not an agent. It says what is behind it
// and what it costs to look, because a fold that only shows a count reads as a
// truncated list rather than a choice.
func (m setupModel) foldLine(focused bool) string {
	sign, action := "+", txt.setupFoldExpand
	if m.showAdapters {
		sign, action = "−", txt.setupFoldCollapse
	}
	format := txt.setupFoldLabelFmt
	if m.foldedCount() == 1 {
		format = txt.setupFoldLabelOneFmt
	}
	label := fmt.Sprintf(format, sign, m.foldedCount())
	if focused {
		label = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Render(label)
	} else {
		label = mutedStyle.Render(label)
	}
	return "  " + label + mutedStyle.Render(fmt.Sprintf(txt.setupFoldHintFmt, action))
}

func (m setupModel) titlePageBody(panelW, width int) string {
	head := accentStyle.Render("another setup") + "\n" +
		titleStyle.Render(txt.setupTitleTitle) + "\n" +
		mutedStyle.Render(txt.setupTitleHint) + "\n\n"

	if len(m.titleOpts) <= 1 {
		body := head + mutedStyle.Render(txt.setupTitleNone) + "\n"
		if m.pluginVisible() {
			// The plugin is the one part of this page that still works when
			// no agent can suggest a title: OpenCode 2 writes its own.
			body += "\n" + m.pluginRow(width) + "\n"
		}
		return body + "\n" + mutedStyle.Render(txt.setupTitleNoneHelp)
	}

	foot := "\n" + mutedStyle.Render(txt.setupTitleLanguage) + "  " + m.titleLanguageRow() + "\n"
	// The policy line says the language is shared with OpenCode 2 and Pi. When
	// the OpenCode 2 row is on the page it says the same thing about a real
	// directory, so the abstract sentence gives up its lines to the concrete
	// one rather than both competing for a short terminal.
	if m.titleCursor <= 0 && !m.pluginVisible() {
		foot += mutedStyle.Render(txt.setupTitlePolicy) + "\n"
	}
	if m.pluginVisible() {
		foot += m.pluginRow(width) + "\n"
	}
	foot += "\n"
	if m.err != "" {
		foot += errStyle.Render("✗ "+m.err) + "\n"
	}
	if m.titleCursor > 0 {
		foot += mutedStyle.Render(txt.setupTitleHelpModel)
	} else {
		foot += mutedStyle.Render(txt.setupTitleHelpSave)
	}

	// Same budget as the agent page: this list is every agent that can write a
	// title, so it outgrows a short terminal for the same reason.
	budget := m.height - lipgloss.Height(modalStyle.Width(panelW).Render(head+foot))
	if len(m.titleOpts) > budget {
		budget--
	}
	start, end := modelWindow(m.titleCursor, len(m.titleOpts), max(1, budget))

	var body strings.Builder
	body.WriteString(head)
	for i := start; i < end; i++ {
		opt := m.titleOpts[i]
		cursor := "  "
		if i == m.titleCursor {
			cursor = "› "
		}
		mark := mutedStyle.Render("○")
		if i == m.titleCursor {
			mark = okStyle.Render("●")
		}
		name := padRight(opt.name, txt.setupNameWidth)
		if color, ok := providerColors[opt.id]; ok {
			name = lipgloss.NewStyle().Foreground(color).Bold(i == m.titleCursor).Render(name)
		}
		if opt.id != "" {
			name = renderAgentChip(opt.id) + " " + name
		} else {
			// "不启用" is a setting, not an agent; an empty chip-width gutter
			// keeps its name on the same column as the agents below it.
			name = strings.Repeat(" ", agentChipWidth+1) + name
		}
		line := cursor + mark + " " + name
		if opt.command != "" {
			line += "  " + mutedStyle.Render(opt.command)
		}
		body.WriteString(ansi.Truncate(line, width, "…") + "\n")
	}
	if n := len(m.titleOpts) - (end - start); n > 0 {
		body.WriteString(ansi.Truncate(mutedStyle.Render(fmt.Sprintf(txt.setupMoreRowsFmt, n)), width, "…") + "\n")
	}
	body.WriteString(foot)
	return body.String()
}

// uiLanguageRow shows the interface choices the same way the title row does.
// Its labels stay in the language they name, so the row is readable to someone
// who cannot read the page it sits on.
func (m setupModel) uiLanguageRow() string {
	labels := make([]string, 0, len(uiLanguages))
	for i, l := range uiLanguages {
		label := i18n.Label(l)
		if i == m.uiLangCursor {
			label = okStyle.Render("[" + label + "]")
		} else {
			label = mutedStyle.Render(" " + label + " ")
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, " ") + mutedStyle.Render("  ·  ←→")
}

// languageRow shows the three choices at once. The list is short enough that
// hiding two of them behind a cycle would only make the setting harder to see.
func (m setupModel) titleLanguageRow() string {
	labels := make([]string, 0, len(languages))
	for i, l := range languages {
		label := titler.LanguageLabel(l)
		if i == m.langCursor {
			label = okStyle.Render("[" + label + "]")
		} else {
			label = mutedStyle.Render(" " + label + " ")
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, " ")
}
