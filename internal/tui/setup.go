package tui

import (
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

// Setup runs as two pages: which agents another manages, then which of those
// agents writes title suggestions. The second page can only offer agents the
// first one kept, so it is built on the way in rather than up front.
const (
	setupPageAgents = iota
	setupPageTitle
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
}

// RunSetup lets a person choose which agents another should index and expose.
// The caller persists the returned IDs only after the program exits cleanly.
func RunSetup(reg *registry.Registry, counts map[string]int, initial []string, initialTitle *config.TitleModel) ([]string, *config.TitleModel, bool, error) {
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
	if initialTitle != nil {
		start.modelInput.SetValue(initialTitle.Model)
		start.titleCursor = -1 // resolved once the option list exists
		start.titleOpts = []titleOption{{id: initialTitle.Provider}}
	}
	program := tea.NewProgram(start, tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return nil, nil, false, err
	}
	model, ok := final.(setupModel)
	if !ok || model.cancelled {
		return nil, nil, false, nil
	}
	var enabled []string
	for _, item := range model.items {
		if model.selected[item.id] {
			enabled = append(enabled, item.id)
		}
	}
	return enabled, model.titleModel(), model.done, nil
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
	return &config.TitleModel{Provider: opt.id, Model: strings.TrimSpace(m.modelInput.Value())}
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
	case tea.KeyMsg:
		if m.page == setupPageTitle {
			return m.updateTitlePage(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
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

// updateTitlePage keeps the model name field permanently focused so there is
// no focus to juggle: arrows pick the agent, everything else is typing.
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
	case "enter":
		m.done = true
		return m, tea.Quit
	}
	if m.titleCursor <= 0 {
		// Nothing to name a model for while the feature is off.
		return m, nil
	}
	var cmd tea.Cmd
	m.modelInput, cmd = m.modelInput.Update(msg)
	return m, cmd
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
	if m.page == setupPageTitle {
		panel := modalStyle.Width(width - modalStyle.GetHorizontalFrameSize()).Render(m.titlePageBody(width))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
	}
	var body strings.Builder
	body.WriteString(accentStyle.Render("another setup") + "\n")
	body.WriteString(titleStyle.Render("选择你使用的 agent") + "  " + mutedStyle.Render(fmt.Sprintf("%d / %d", selectedCount(m.selected), len(m.items))) + "\n")
	body.WriteString(mutedStyle.Render("这些选择决定索引范围、来源和迁移去向。") + "\n\n")
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
	body.WriteString(mutedStyle.Render("↑↓ 移动  ·  space 选择  ·  enter 下一步  ·  esc 取消"))
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
	if m.titleCursor > 0 {
		body.WriteString(mutedStyle.Render("模型") + "  " + m.modelInput.View() + "\n\n")
	} else {
		body.WriteString(mutedStyle.Render("重命名弹窗不会调用任何模型。") + "\n\n")
	}
	if m.err != "" {
		body.WriteString(errStyle.Render("✗ "+m.err) + "\n")
	}
	body.WriteString(mutedStyle.Render("↑↓ 选 agent  ·  输入模型名  ·  enter 保存  ·  esc 返回"))
	return body.String()
}
