package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// Stop at the native-operation boundary; no real agent store or index is used.
type archiveFlowProvider struct{ provider.Provider }

func (archiveFlowProvider) ID() string          { return "codex" }
func (archiveFlowProvider) DisplayName() string { return "Codex" }
func (archiveFlowProvider) ArchiveSession(context.Context, provider.SessionRef, bool) error {
	return errors.New("native archive boundary reached")
}

func archiveResult(t *testing.T, cmd tea.Cmd) archiveDoneMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("archive command missing")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("archive did not dispatch its command batch")
	}
	for _, child := range batch {
		if msg, ok := child().(archiveDoneMsg); ok {
			return msg
		}
	}
	t.Fatal("native archive command missing")
	return archiveDoneMsg{}
}

func TestArchiveCanRepeatThenUndoOnlyTheLatest(t *testing.T) {
	m := layoutTestModel()
	m.reg = registry.NewWith(archiveFlowProvider{})
	first := m.sessions.SelectedItem().(sessionItem)
	second := first
	second.summary.ID = "second"
	for _, item := range []sessionItem{first, second} {
		m.sessions.SetItems([]list.Item{item})
		m.loading = false
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		result := archiveResult(t, cmd)
		if result.summary.ID != item.summary.ID || !result.archived {
			t.Fatalf("a did not archive current row: %+v", result)
		}
		result.err = nil // Complete the UI transition after the native boundary.
		updated, _ = updated.(modelState).Update(result)
		m = updated.(modelState)
	}
	m.loading = false
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	result := archiveResult(t, cmd)
	if result.summary.ID != "second" || result.archived {
		t.Fatalf("u did not restore only the latest archive: %+v", result)
	}
	result.err = nil
	updated, _ := m.Update(result)
	m = updated.(modelState)
	m.loading = false
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}}); cmd != nil {
		t.Fatal("spent archive undo remained armed")
	}
}

func TestArchiveEscDismissesOfferDespiteOtherListState(t *testing.T) {
	for _, loading := range []bool{false, true} {
		m := layoutTestModel()
		summary := m.sessions.SelectedItem().(sessionItem).summary
		m.lastArchived = &summary
		m.marked[summary.ID] = true
		m.searchQuery = "title"
		m.loading = loading
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		got := updated.(modelState)
		if got.lastArchived != nil || got.help() != txt.helpListBase || got.status != "" {
			t.Fatalf("esc retained archive state (loading=%v): %q", loading, got.help())
		}
	}
}

func TestArchiveAndDeleteShareOnlyOneUndoOffer(t *testing.T) {
	m := layoutTestModel()
	summary := m.selected.summary
	restore := func(context.Context) error { return nil }
	m.lastDeleted, m.restoreDeleted = &summary, restore
	updated, _ := m.Update(archiveDoneMsg{summary: summary, archived: true})
	m = updated.(modelState)
	if m.lastDeleted != nil || m.restoreDeleted != nil {
		t.Fatal("archive kept an older delete undo")
	}
	updated, _ = m.Update(deleteDoneMsg{providerID: "codex", title: "deleted", restore: restore})
	m = updated.(modelState)
	if m.lastArchived != nil || m.lastDeleted == nil || m.help() != txt.helpDeleted {
		t.Fatal("delete kept an older archive undo or hid its own undo")
	}
}

func TestArchiveAllowsQuitDuringAndAfterRefresh(t *testing.T) {
	for _, loading := range []bool{false, true} {
		for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'q'}}, {Type: tea.KeyCtrlC}} {
			m := layoutTestModel()
			ctx, cancel := context.WithCancel(context.Background())
			m.ctx, m.cancel = ctx, cancel
			m.lastArchived = &m.selected.summary
			m.loading = loading
			_, cmd := m.Update(key)
			if cmd == nil {
				t.Fatal("quit command missing")
			}
			_, quits := cmd().(tea.QuitMsg)
			cancelled := ctx.Err() != nil
			cancel()
			if !quits || !cancelled {
				t.Fatalf("%s did not quit (loading=%v)", key.String(), loading)
			}
		}
	}
}
