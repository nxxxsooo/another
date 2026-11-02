package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/registry"
)

func focusModel(focus string) modelState {
	m := modelState{
		reg:        registry.New(),
		marked:     map[string]bool{},
		folded:     map[string]bool{},
		sessions:   newSessionList(nil, map[string]bool{}),
		sourceList: newSourceList(nil),
		pageGen:    1,
	}
	if focus != "" {
		m.focus = &model.Summary{ID: focus, Provider: "codex", Title: "title " + focus}
	}
	return m
}

func cursorID(m modelState) string {
	if row, ok := m.sessions.SelectedItem().(sessionItem); ok {
		return row.summary.ID
	}
	return ""
}

// A deep link names a session the first page may not hold: outside the scope,
// past the recent window, or simply older than the page. It must still be the
// row the person lands on.
func TestFocusedSessionMissingFromPageIsAddedAndSelected(t *testing.T) {
	m := focusModel("s9")
	res, _ := m.Update(sessionsPageMsg{gen: 1, items: []list.Item{groupRow("s1", "/a", 1), groupRow("s2", "/a", 2)}})
	got := res.(modelState)
	if ids := sessionIDs(got.sessions.Items()); len(ids) != 3 || ids[0] != "s9" {
		t.Fatalf("rows = %v, want s9 added at the top", ids)
	}
	if cursorID(got) != "s9" {
		t.Fatalf("cursor on %q, want s9", cursorID(got))
	}
}

func TestFocusedSessionOnPageIsSelectedWithoutDuplicate(t *testing.T) {
	m := focusModel("s2")
	res, _ := m.Update(sessionsPageMsg{gen: 1, items: []list.Item{groupRow("s1", "/a", 1), groupRow("s2", "/a", 2), groupRow("s3", "/a", 3)}})
	got := res.(modelState)
	if ids := sessionIDs(got.sessions.Items()); len(ids) != 3 {
		t.Fatalf("rows = %v, want the page unchanged", ids)
	}
	if cursorID(got) != "s2" {
		t.Fatalf("cursor on %q, want s2", cursorID(got))
	}
}

// After the first key the person is driving: a reload follows their scope and
// no longer pins the linked session.
func TestKeyPressReleasesFocus(t *testing.T) {
	m := focusModel("s9")
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := res.(modelState)
	if got.focus != nil {
		t.Fatal("a key press must release the focus")
	}
	res, _ = got.Update(sessionsPageMsg{gen: got.pageGen, items: []list.Item{groupRow("s1", "/a", 1)}})
	if ids := sessionIDs(res.(modelState).sessions.Items()); len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("rows = %v, want only the page", ids)
	}
}
