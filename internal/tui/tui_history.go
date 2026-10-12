package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/journal"
)

// historyItem is one row of the action-history overlay: a journaled change and
// whether its inverse is still possible.
type historyItem struct {
	entry       journal.Entry
	recoverable bool
}

func (h historyItem) FilterValue() string { return h.entry.Title + " " + h.entry.Session }

type historyDelegate struct{}

func (historyDelegate) Height() int                         { return 2 }
func (historyDelegate) Spacing() int                        { return 0 }
func (historyDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (historyDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(historyItem)
	if !ok {
		return
	}
	e := it.entry
	cursor := "  "
	if index == m.Index() {
		cursor = "› "
	}
	when := e.At.Local().Format("01-02 15:04")
	action := historyActionLabel(e.Action)
	head := fmt.Sprintf("%s%s  %s  %s  %s", cursor,
		mutedStyle.Render(when), action, e.Provider, truncateDisplay(e.Title, 40))
	fmt.Fprint(w, ansi.Truncate(head, max(4, m.Width()), ""))
	fmt.Fprint(w, "\n    "+mutedStyle.Render(historyDetail(it)))
}

// historyDetail is the second line: what changed, and whether it can still be
// undone.
func historyDetail(it historyItem) string {
	e := it.entry
	switch e.Action {
	case journal.ActionRename:
		return fmt.Sprintf("%s → %s", truncateDisplay(e.Before, 32), truncateDisplay(e.After, 32))
	case journal.ActionDelete:
		if it.recoverable {
			return txt.historyRecoverable
		}
		return txt.historyPruned
	case journal.ActionRevert:
		return "→ " + e.Target
	default:
		if it.recoverable {
			return txt.historyRecoverable
		}
		return txt.historyPruned
	}
}

func historyActionLabel(a journal.Action) string {
	switch a {
	case journal.ActionArchive:
		return txt.historyActionArchived
	case journal.ActionUnarchive:
		return txt.historyActionUnarchived
	case journal.ActionRename:
		return txt.historyActionRenamed
	case journal.ActionDelete:
		return txt.historyActionDeleted
	case journal.ActionRevert:
		return txt.historyActionReverted
	}
	return string(a)
}

func newHistoryList(items []list.Item) list.Model {
	return newBareList(items, historyDelegate{}, 56, 10)
}

// loadHistoryItems rebuilds the overlay rows, newest first, from the journal.
func (m *modelState) loadHistoryItems() {
	st := openJournal()
	var rows []list.Item
	if st != nil {
		all, err := st.Entries()
		if err != nil {
			m.err = err.Error()
		}
		for i := len(all) - 1; i >= 0; i-- {
			rows = append(rows, historyItem{entry: all[i], recoverable: st.Recovery(all[i])})
		}
	}
	m.historyList.SetItems(rows)
}

func (m modelState) openHistory() (tea.Model, tea.Cmd) {
	m.loadHistoryItems()
	m.historyList.Select(0)
	m.overlay = overlayHistory
	m.err = ""
	if len(m.historyList.Items()) == 0 {
		m.status = mutedStyle.Render(txt.historyEmpty)
	}
	m.layout()
	return m, tea.Batch(tea.HideCursor, tea.ClearScreen)
}

func (m modelState) updateHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc", "left":
		m.overlay = overlayNone
		m.layout()
		return m, nil
	case "enter":
		it, ok := m.historyList.SelectedItem().(historyItem)
		if !ok {
			return m, nil
		}
		st := openJournal()
		if st == nil || !st.Recovery(it.entry) {
			m.err = txt.historyNotRecoverable
			return m, nil
		}
		m.loading = true
		m.err = ""
		return m, tea.Batch(m.spinner.Tick, revertEntryCmd(m.ctx, m.reg, m.idx, st, it.entry))
	}
	var cmd tea.Cmd
	m.historyList, cmd = m.historyList.Update(msg)
	return m, cmd
}

func (m modelState) onHistoryReverted(msg historyRevertedMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayHistory {
		return m, nil
	}
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		m.loadHistoryItems()
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.historyRevertedFmt, truncateDisplay(msg.entry.Title, 40)))
	m.loadHistoryItems()
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}
