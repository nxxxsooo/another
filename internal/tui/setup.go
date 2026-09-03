package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/registry"
)

type setupItem struct {
	id, name  string
	command   string
	sessions  int
	data, cli bool
	available bool
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
}

// RunSetup lets a person choose which agents another should index and expose.
// The caller persists the returned IDs only after the program exits cleanly.
func RunSetup(reg *registry.Registry, counts map[string]int, initial []string) ([]string, bool, error) {
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
	program := tea.NewProgram(setupModel{items: items, selected: chosen, spinner: sp}, tea.WithAltScreen())
	final, err := program.Run()
	if err != nil {
		return nil, false, err
	}
	model, ok := final.(setupModel)
	if !ok || model.cancelled {
		return nil, false, nil
	}
	var enabled []string
	for _, item := range model.items {
		if model.selected[item.id] {
			enabled = append(enabled, item.id)
		}
	}
	return enabled, model.done, nil
}

func (m setupModel) Init() tea.Cmd { return tea.HideCursor }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
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
			m.done = true
			return m, tea.Quit
		}
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
	body.WriteString(mutedStyle.Render("↑↓ 移动  ·  space 选择  ·  enter 保存  ·  esc 取消"))
	panel := modalStyle.Width(width - modalStyle.GetHorizontalFrameSize()).Render(body.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}
