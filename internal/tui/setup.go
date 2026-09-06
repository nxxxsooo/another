package tui

import (
	"context"
	"fmt"
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
}

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
	chosen := make(map[string]bool, len(initial))
	for _, id := range initial {
		chosen[registry.NormalizeID(id)] = true
	}
	firstRun := len(initial) == 0
	var items []setupItem
	for _, p := range reg.All() {
		data := p.Installed()
		cli := registry.CLIAvailable(p.ID())
		item := setupItem{
			id: p.ID(), name: p.DisplayName(), command: registry.CLICommand(p.ID()),
			sessions: counts[p.ID()], data: data, cli: cli, available: data || cli,
		}
		items = append(items, item)
		if firstRun && item.available {
			chosen[item.id] = true
		}
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle
	modelInput := textinput.New()
	modelInput.Prompt = ""
	modelInput.Placeholder = "留空用该 CLI 的默认模型"
	modelInput.CharLimit = 120
	start := setupModel{items: items, selected: chosen, spinner: sp, modelInput: modelInput}
	start.langCursor = languageCursor(titler.Language(initialPolicy.Language))
	if initialTitle != nil {
		start.modelInput.SetValue(initialTitle.Model)
		start.titleCursor = -1 // resolved once the option list exists
		start.titleOpts = []titleOption{{id: initialTitle.Provider}}
		start.langCursor = languageCursor(titler.Language(initialTitle.Language))
	}
	program := tea.NewProgram(start, tea.WithAltScreen())
	final, err := program.Run()
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

func (m setupModel) Init() tea.Cmd { return tea.HideCursor }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
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
			if m.cursor > 0 && m.cursor < len(m.items) {
				m.items[m.cursor-1], m.items[m.cursor] = m.items[m.cursor], m.items[m.cursor-1]
				m.cursor--
			}
		case "shift+down":
			if m.cursor >= 0 && m.cursor < len(m.items)-1 {
				m.items[m.cursor], m.items[m.cursor+1] = m.items[m.cursor+1], m.items[m.cursor]
				m.cursor++
			}
		case "up", "k":
			if len(m.items) > 0 {
				m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
			}
		case "down", "j":
			if len(m.items) > 0 {
				m.cursor = (m.cursor + 1) % len(m.items)
			}
		case " ":
			if len(m.items) == 0 {
				return m, nil
			}
			item := m.items[m.cursor]
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
	width := min(72, m.width-8)
	switch m.page {
	case setupPageTitle:
		panel := modalStyle.Width(width - modalStyle.GetHorizontalFrameSize()).Render(m.titlePageBody(width))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
	case setupPageModel:
		panel := modalStyle.Width(width - modalStyle.GetHorizontalFrameSize()).Render(m.modelPageBody(width))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
	}
	var body strings.Builder
	body.WriteString(accentStyle.Render("another setup") + "\n")
	body.WriteString(titleStyle.Render("选择你使用的 agent") + "  " + mutedStyle.Render(fmt.Sprintf("%d / %d", selectedCount(m.selected), len(m.items))) + "\n")
	body.WriteString(mutedStyle.Render("Space 开关 agent；Shift+↑↓ 调整显示顺序。") + "\n\n")
	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "› "
		}
		mark := mutedStyle.Render("○")
		if m.selected[item.id] {
			mark = okStyle.Render("●")
		}
		name := padRight(item.name, 16)
		if color, ok := providerColors[item.id]; ok {
			name = lipgloss.NewStyle().Foreground(color).Bold(i == m.cursor).Render(name)
		}
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
	body.WriteString("\n")
	if m.err != "" {
		body.WriteString(errStyle.Render("✗ "+m.err) + "\n")
	}
	body.WriteString(mutedStyle.Render("↑↓ 移动  ·  space 开关  ·  shift+↑↓ 排序  ·  enter 下一步  ·  esc 取消"))
	panel := modalStyle.Width(width - modalStyle.GetHorizontalFrameSize()).Render(body.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m setupModel) titlePageBody(width int) string {
	var body strings.Builder
	body.WriteString(accentStyle.Render("another setup") + "\n")
	body.WriteString(titleStyle.Render("重命名时的 AI 标题建议") + "\n")
	body.WriteString(mutedStyle.Render("按 ctrl+r 时调用哪个已装 agent 生成候选标题。") + "\n\n")

	if len(m.titleOpts) <= 1 {
		body.WriteString(mutedStyle.Render("已选的 agent 里没有能生成标题的 CLI，此功能保持关闭。") + "\n\n")
		body.WriteString(mutedStyle.Render("enter 保存  ·  esc 返回"))
		return body.String()
	}

	for i, opt := range m.titleOpts {
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
		line := cursor + mark + " " + name
		if opt.command != "" {
			line += "  " + mutedStyle.Render(opt.command)
		}
		body.WriteString(ansi.Truncate(line, width, "…") + "\n")
	}
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render("语言") + "  " + m.languageRow() + "\n")
	if m.titleCursor <= 0 {
		body.WriteString(mutedStyle.Render("建议模型关闭；语言仍供 O2／Pi 原生命名共用。") + "\n")
	}
	body.WriteString("\n")
	if m.err != "" {
		body.WriteString(errStyle.Render("✗ "+m.err) + "\n")
	}
	if m.titleCursor > 0 {
		body.WriteString(mutedStyle.Render("↑↓ 选 agent  ·  ←→ 选语言  ·  enter 选模型  ·  esc 返回"))
	} else {
		body.WriteString(mutedStyle.Render("↑↓ 选 agent  ·  ←→ 选语言  ·  enter 保存  ·  esc 返回"))
	}
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
