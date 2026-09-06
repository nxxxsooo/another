package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/titler"
)

// batchModelsMsg carries the listing for the batch overlay's model picker.
type batchModelsMsg struct {
	provider string
	models   []string
	err      error
}

// batchModelsCmd asks the agent CLI which models it can run.
func batchModelsCmd(provider string) tea.Cmd {
	return func() tea.Msg {
		models, err := titler.ListModels(context.Background(), provider)
		return batchModelsMsg{provider: provider, models: models, err: err}
	}
}

// openBatchModelPicker offers the same list setup pulls, so the temporary
// override cannot be a model name the CLI would reject. Agents that cannot
// list fall back to typing, which is the only honest option for them.
func (m modelState) openBatchModelPicker() (tea.Model, tea.Cmd) {
	cfg := m.batchConfig()
	if m.batchModelInput.Placeholder == "" {
		m.batchModelInput = newBatchModelInput()
	}
	m.batchModelInput.SetValue(cfg.Model)
	m.batchModelInput.CursorEnd()
	m.batchModelFilter = ""
	m.batchModelErr = ""
	m.status = ""
	if !titler.CanListModels(cfg.Provider) {
		m.batchModelPicking = false
		m.batchModelEditing = true
		m.batchModelErr = titler.Command(cfg.Provider) + " 不支持列出模型"
		m.batchModelInput.Focus()
		return m, textinput.Blink
	}
	m.batchModelPicking = true
	m.batchModelEditing = false
	if len(m.batchModelOpts) > 0 {
		m.batchModelCursor = modelCursorFor(modelRowsFor(m.batchModelOpts, ""), cfg.Model)
		return m, nil
	}
	m.batchModelLoading = true
	m.batchModelCursor = 0
	return m, batchModelsCmd(cfg.Provider)
}

func (m modelState) batchModelRows() []string {
	return modelRowsFor(m.batchModelOpts, m.batchModelFilter)
}

func (m modelState) batchCustomRow() int { return len(m.batchModelRows()) }

// updateBatchModelPicker owns the keyboard while the list is open. Typing
// filters; the last row hands over to the text input for a name the CLI has
// not listed.
func (m modelState) updateBatchModelPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.batchCancel != nil {
			m.batchCancel()
		}
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc":
		m.batchModelPicking = false
		m.batchModelFilter = ""
		return m, nil
	case "up", "shift+tab":
		if n := m.batchCustomRow() + 1; n > 0 {
			m.batchModelCursor = (m.batchModelCursor - 1 + n) % n
		}
		return m, nil
	case "down", "tab":
		if n := m.batchCustomRow() + 1; n > 0 {
			m.batchModelCursor = (m.batchModelCursor + 1) % n
		}
		return m, nil
	case "enter":
		if m.batchModelLoading {
			return m, nil
		}
		if m.batchModelCursor >= m.batchCustomRow() {
			m.batchModelPicking = false
			m.batchModelEditing = true
			m.batchModelInput.Focus()
			m.batchModelInput.CursorEnd()
			return m, textinput.Blink
		}
		rows := m.batchModelRows()
		chosen := ""
		if m.batchModelCursor > 0 && m.batchModelCursor < len(rows) {
			chosen = rows[m.batchModelCursor]
		}
		m.batchModelPicking = false
		m.batchModelFilter = ""
		m.batchModelInput.SetValue(chosen)
		return m.applyBatchModel(chosen)
	case "backspace":
		if m.batchModelFilter != "" {
			m.batchModelFilter = m.batchModelFilter[:len(m.batchModelFilter)-1]
			m.batchModelCursor = 0
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.batchModelFilter += string(msg.Runes)
		m.batchModelCursor = 0
	}
	return m, nil
}

// applyBatchModel takes the chosen model for this batch alone and re-runs the
// suggestions on it. Nothing is written back to config.
func (m modelState) applyBatchModel(chosen string) (tea.Model, tea.Cmd) {
	cfg := m.batchConfig()
	if chosen == cfg.Model {
		m.status = "模型未变，保留现有结果"
		return m, nil
	}
	cfg.Model = chosen
	m.batchCfg = cfg
	if len(m.batchItems) == 0 {
		return m, nil
	}
	return m.rerunBatch()
}

// batchModelView renders the picker, its loading state, and the typed
// fallback.
func (m modelState) batchModelView(inner int) string {
	var b strings.Builder
	if m.batchModelLoading {
		b.WriteString(mutedStyle.Render("正在向 "+titler.Command(m.batchConfig().Provider)+" 获取模型列表…") + "\n")
		b.WriteString(mutedStyle.Render("esc 返回"))
		return b.String()
	}
	rows := m.batchModelRows()
	start, end := modelWindow(m.batchModelCursor, len(rows), maxModelRows)
	for i := start; i < end; i++ {
		label := rows[i]
		if i == 0 {
			label = "默认模型"
		}
		b.WriteString(ansi.Truncate(modelRowLine(label, i == m.batchModelCursor), inner, "…") + "\n")
	}
	if hidden := len(rows) - end; hidden > 0 {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf("+ 还有 %d 个，继续输入可过滤", hidden)))
	}
	b.WriteString(ansi.Truncate(modelRowLine("自定义模型名", m.batchModelCursor >= m.batchCustomRow()), inner, "…") + "\n")
	if m.batchModelFilter != "" {
		b.WriteString(mutedStyle.Render("过滤："+m.batchModelFilter) + "\n")
	}
	b.WriteString(mutedStyle.Render("↑↓ 选模型  ·  输入过滤  ·  enter 换模型重跑  ·  esc 取消"))
	return b.String()
}
