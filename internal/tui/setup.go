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
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
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

// RunSetup lets a person choose which agents another should index and expose.
// The caller persists the returned IDs only after the program exits cleanly.
func RunSetup(reg *registry.Registry, counts map[string]int, initial []string, initialTitle *config.TitleModel, initialPolicy config.TitlePolicy) ([]string, *config.TitleModel, config.TitlePolicy, bool, error) {
	chosen := initialSetupSelection(initial)
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
	modelInput.Placeholder = "留空用该 CLI 的默认模型"
	modelInput.CharLimit = 120
	start := setupModel{items: items, selected: chosen, spinner: sp, modelInput: modelInput}
	start.showAdapters = anyAdapterSelected(items, chosen)
	start.langCursor = languageCursor(titler.Language(initialPolicy.Language))
	if initialTitle != nil {
		start.modelInput.SetValue(initialTitle.Model)
		start.titleCursor = -1 // resolved once the option list exists
		start.titleOpts = []titleOption{{id: initialTitle.Provider}}
		start.langCursor = languageCursor(titler.Language(initialTitle.Language))
	}
	program := tea.NewProgram(start, tea.WithAltScreen())
	restoreInputSource := temporarilyUseASCIIInputSource()
	defer restoreInputSource()
	final, err := program.Run()
	restoreInputSource()
	// Setup either hands over to the browser, which sets its own title, or
	// returns to the shell; neither should inherit the setup screen's title.
	restoreWindowTitle(os.Stdout)
	if err != nil {
		return nil, nil, config.TitlePolicy{}, false, err
	}
	model, ok := final.(setupModel)
	if !ok || model.cancelled {
		return nil, nil, config.TitlePolicy{}, false, nil
	}
	var enabled []string
	for _, item := range model.items {
		if model.selected[item.id] {
			enabled = append(enabled, item.id)
		}
	}
	return enabled, model.titleModel(), config.TitlePolicy{Language: string(model.language())}, model.done, nil
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

// titleOptions offers only agents that survived page one and can actually run
// a one-shot prompt right now. Listing an agent whose CLI is missing would
// only produce a failure at rename time.
func titleOptions(items []setupItem, selected map[string]bool) []titleOption {
	opts := []titleOption{{name: "不启用"}}
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
	return tea.Batch(tea.HideCursor, tea.SetWindowTitle(setupWindowTitle))
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Setup is centred in the window, so a resize moves the whole panel and
		// leaves the old one behind unless the screen is cleared with it.
		return m, tea.ClearScreen
	case modelsLoadedMsg:
		if msg.provider != m.modelFor {
			return m, nil
		}
		m.modelLoading = false
		if msg.err != nil {
			// A CLI that cannot answer right now is not a dead end: the page
			// says why and falls back to typing a name.
			m.modelErr = msg.err.Error()
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
				m.err = item.name + " 未检测到 CLI 或会话数据"
				return m, nil
			}
			m.selected[item.id] = !m.selected[item.id]
			m.err = ""
		case "enter":
			if selectedCount(m.selected) == 0 {
				m.err = "至少选择一个 agent"
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
		return ansi.Truncate("Terminal too small — resize to at least 48x20", max(1, m.width), "")
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
		titleStyle.Render("选择你使用的 agent") + "  " +
		mutedStyle.Render(fmt.Sprintf("%d / %d", selectedCount(m.selected), len(m.items))) + "\n" +
		mutedStyle.Render("Space 开关 agent；Shift+↑↓ 调整显示顺序。") + "\n\n"
	foot := "\n"
	if m.err != "" {
		foot += errStyle.Render("✗ "+m.err) + "\n"
	}
	foot += mutedStyle.Render("↑↓ 移动  ·  space 开关  ·  shift+↑↓ 排序  ·  enter 下一步  ·  esc 取消")

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
		name := padRight(item.name, 16)
		if color, ok := providerColors[item.id]; ok {
			name = lipgloss.NewStyle().Foreground(color).Bold(row == m.cursor).Render(name)
		}
		// Setup is where an agent is met for the first time, so the chip the
		// session list will use is shown next to the name it stands for.
		name = renderAgentChip(item.id) + " " + name
		cli := "CLI 未安装"
		if item.cli {
			cli = "CLI 已安装"
		}
		data := fmt.Sprintf("%d 个会话", item.sessions)
		if !item.data && item.sessions == 0 {
			data = "无会话数据"
		}
		line := cursor + mark + " " + name + "  " + padRight(cli, 12) + "  " + data
		if !item.available {
			line = mutedStyle.Render(line)
		}
		body.WriteString(ansi.Truncate(line, width, "…") + "\n")
	}
	if n := len(rows) - (end - start); n > 0 {
		body.WriteString(ansi.Truncate(mutedStyle.Render(fmt.Sprintf("  + 还有 %d 个，↑↓ 滚动", n)), width, "…") + "\n")
	}
	body.WriteString(foot)
	panel := modalStyle.Width(panelW).Render(body.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// foldLine draws the one row that is not an agent. It says what is behind it
// and what it costs to look, because a fold that only shows a count reads as a
// truncated list rather than a choice.
func (m setupModel) foldLine(focused bool) string {
	sign, action := "+", "展开"
	if m.showAdapters {
		sign, action = "−", "收起"
	}
	label := fmt.Sprintf("%s 其他 %d 个兼容适配（非每次发布实测）", sign, m.foldedCount())
	if focused {
		label = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Render(label)
	} else {
		label = mutedStyle.Render(label)
	}
	return "  " + label + mutedStyle.Render("  ·  space "+action)
}

func (m setupModel) titlePageBody(panelW, width int) string {
	head := accentStyle.Render("another setup") + "\n" +
		titleStyle.Render("重命名时的 AI 标题建议") + "\n" +
		mutedStyle.Render("按 ctrl+r 时调用哪个已装 agent 生成候选标题。") + "\n\n"

	if len(m.titleOpts) <= 1 {
		return head + mutedStyle.Render("已选的 agent 里没有能生成标题的 CLI，此功能保持关闭。") + "\n\n" +
			mutedStyle.Render("enter 保存  ·  esc 返回")
	}

	foot := "\n" + mutedStyle.Render("语言") + "  " + m.languageRow() + "\n"
	if m.titleCursor <= 0 {
		foot += mutedStyle.Render("建议模型关闭；语言仍供 O2／Pi 原生命名共用。") + "\n"
	}
	foot += "\n"
	if m.err != "" {
		foot += errStyle.Render("✗ "+m.err) + "\n"
	}
	if m.titleCursor > 0 {
		foot += mutedStyle.Render("↑↓ 选 agent  ·  ←→ 选语言  ·  enter 选模型  ·  esc 返回")
	} else {
		foot += mutedStyle.Render("↑↓ 选 agent  ·  ←→ 选语言  ·  enter 保存  ·  esc 返回")
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
		name := padRight(opt.name, 16)
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
		body.WriteString(ansi.Truncate(mutedStyle.Render(fmt.Sprintf("  + 还有 %d 个，↑↓ 滚动", n)), width, "…") + "\n")
	}
	body.WriteString(foot)
	return body.String()
}

// languageRow shows the three choices at once. The list is short enough that
// hiding two of them behind a cycle would only make the setting harder to see.
func (m setupModel) languageRow() string {
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
