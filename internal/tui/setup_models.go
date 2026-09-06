package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/titler"
)

// maxModelRows caps how much of a long catalog is drawn at once. pi alone
// reports dozens of models; the page scrolls around the cursor instead of
// growing past the terminal.
const maxModelRows = 8

// openModelPage moves to the model picker for the agent chosen on the title
// page. Agents whose CLI cannot list models skip straight to typing a name,
// which is the only honest option for them.
func (m setupModel) openModelPage() (setupModel, tea.Cmd) {
	opt := m.titleOpts[m.titleCursor]
	m.page = setupPageModel
	m.err = ""
	m.modelErr = ""
	m.modelFilter = ""
	m.modelCursor = 0
	m.modelTyping = false
	if m.modelFor != opt.id {
		m.modelOpts = nil
		m.modelFor = opt.id
	}
	if !titler.CanListModels(opt.id) {
		m.modelLoading = false
		m.modelErr = titler.Command(opt.id) + " 不支持列出模型"
		m.modelTyping = true
		m.modelInput.Focus()
		return m, textinput.Blink
	}
	if len(m.modelOpts) > 0 {
		// A listing already fetched for this agent is reused: it costs a
		// process launch and sometimes a network call.
		m.modelCursor = modelCursorFor(m.modelRows(), m.modelInput.Value())
		return m, nil
	}
	m.modelLoading = true
	return m, listModelsCmd(opt.id)
}

// modelRows is the pickable list: the CLI's own default first, then whatever
// survives the filter. The custom row lives at the end and is handled by
// index rather than stored here, so it can never be filtered away.
func (m setupModel) modelRows() []string {
	rows := []string{""}
	needle := strings.ToLower(strings.TrimSpace(m.modelFilter))
	for _, name := range m.modelOpts {
		if needle == "" || strings.Contains(strings.ToLower(name), needle) {
			rows = append(rows, name)
		}
	}
	return rows
}

// customRow is the index of the "type a name" row.
func (m setupModel) customRow() int { return len(m.modelRows()) }

// modelCursorFor puts the cursor on a previously saved model.
func modelCursorFor(rows []string, model string) int {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0
	}
	for i, name := range rows {
		if name == model {
			return i
		}
	}
	// A saved model the CLI no longer lists is still a valid answer: keep it
	// as the custom value rather than silently resetting to the default.
	return len(rows)
}

// selectedModel reads the picker back out. An empty string means the CLI's own
// default, which is a real choice and not a missing answer.
func (m setupModel) selectedModel() string {
	if m.modelTyping || m.modelCursor >= m.customRow() {
		return strings.TrimSpace(m.modelInput.Value())
	}
	rows := m.modelRows()
	if m.modelCursor <= 0 || m.modelCursor >= len(rows) {
		return ""
	}
	return rows[m.modelCursor]
}

// updateModelPage owns the keyboard on the picker. Typing filters the list;
// the custom row is where typing means the value itself.
func (m setupModel) updateModelPage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelTyping {
		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case "esc":
			if len(m.modelOpts) == 0 {
				// Nothing to go back to on this page.
				m.page = setupPageTitle
				m.modelInput.Blur()
				return m, nil
			}
			m.modelTyping = false
			m.modelInput.Blur()
			return m, nil
		case "enter":
			m.done = true
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.modelInput, cmd = m.modelInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	case "esc":
		m.page = setupPageTitle
		return m, nil
	case "up", "shift+tab":
		if n := m.customRow() + 1; n > 0 {
			m.modelCursor = (m.modelCursor - 1 + n) % n
		}
		return m, nil
	case "down", "tab":
		if n := m.customRow() + 1; n > 0 {
			m.modelCursor = (m.modelCursor + 1) % n
		}
		return m, nil
	case "enter":
		if m.modelLoading {
			return m, nil
		}
		if m.modelCursor >= m.customRow() {
			m.modelTyping = true
			m.modelInput.Focus()
			m.modelInput.CursorEnd()
			return m, textinput.Blink
		}
		m.done = true
		return m, tea.Quit
	case "backspace":
		if m.modelFilter != "" {
			m.modelFilter = m.modelFilter[:len(m.modelFilter)-1]
			m.modelCursor = 0
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.modelFilter += string(msg.Runes)
		m.modelCursor = 0
	}
	return m, nil
}

func (m setupModel) modelPageBody(width int) string {
	opt := m.titleOpts[m.titleCursor]
	var b strings.Builder
	b.WriteString(accentStyle.Render("another setup") + "\n")
	b.WriteString(titleStyle.Render("用哪个模型写标题") + "\n")
	b.WriteString(mutedStyle.Render(opt.name+" 报告的可用模型，留在默认即由该 CLI 自己决定。") + "\n\n")

	if m.modelLoading {
		b.WriteString(mutedStyle.Render("正在向 "+titler.Command(opt.id)+" 获取模型列表…") + "\n\n")
		b.WriteString(mutedStyle.Render("esc 返回"))
		return b.String()
	}
	if m.modelTyping {
		if m.modelErr != "" {
			b.WriteString(mutedStyle.Render(m.modelErr) + "\n\n")
		}
		b.WriteString(mutedStyle.Render("模型") + "  " + m.modelInput.View() + "\n\n")
		if len(m.modelOpts) == 0 {
			b.WriteString(mutedStyle.Render("enter 保存  ·  esc 返回上一步"))
		} else {
			b.WriteString(mutedStyle.Render("enter 保存  ·  esc 回到列表"))
		}
		return b.String()
	}

	rows := m.modelRows()
	start, end := modelWindow(m.modelCursor, len(rows), maxModelRows)
	for i := start; i < end; i++ {
		label := rows[i]
		if i == 0 {
			label = "默认模型"
		}
		b.WriteString(ansi.Truncate(modelRowLine(label, i == m.modelCursor), width, "…") + "\n")
	}
	if hidden := len(rows) - end; hidden > 0 {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf("+ 还有 %d 个，继续输入可过滤", hidden)))
	}
	custom := "自定义模型名"
	if value := strings.TrimSpace(m.modelInput.Value()); value != "" {
		custom += "：" + value
	}
	b.WriteString(ansi.Truncate(modelRowLine(custom, m.modelCursor >= m.customRow()), width, "…") + "\n\n")

	if m.modelErr != "" {
		b.WriteString(mutedStyle.Render(m.modelErr) + "\n")
	}
	if m.modelFilter != "" {
		b.WriteString(mutedStyle.Render("过滤："+m.modelFilter) + "\n")
	}
	b.WriteString(mutedStyle.Render("↑↓ 选模型  ·  输入过滤  ·  enter 保存  ·  esc 返回"))
	return b.String()
}

func modelRowLine(label string, selected bool) string {
	cursor, mark := "  ", mutedStyle.Render("○")
	if selected {
		cursor, mark = "› ", okStyle.Render("●")
		label = lipgloss.NewStyle().Bold(true).Render(label)
	}
	return cursor + mark + " " + label
}

// modelWindow scrolls a long catalog around the cursor instead of clipping it
// at the top, so the selected row is always on screen.
func modelWindow(cursor, total, size int) (int, int) {
	if total <= size {
		return 0, total
	}
	start := cursor - size/2
	if start < 0 {
		start = 0
	}
	if start+size > total {
		start = total - size
	}
	return start, start + size
}
