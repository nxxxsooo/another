package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nxxxsooo/another/internal/i18n"
)

// Rename and delete share one key for one row and for the marked set: r
// renames, d deletes, s refreshes. ctrl+r / ctrl+d / ctrl+t stay as hidden
// aliases and must never be advertised again.

func TestRenameKeyOpensSingleRenameWithoutMarks(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(runeKey('r'))
	got := updated.(modelState)
	if got.overlay != overlayRename || got.renameInput.Value() != "A useful title" {
		t.Fatalf("r did not open prefilled rename: overlay=%d value=%q", got.overlay, got.renameInput.Value())
	}
	if got.indexing {
		t.Fatal("r refreshed the index instead of renaming")
	}
}

func TestRenameKeyOpensBatchWhenMarked(t *testing.T) {
	m := batchTestModel()
	m.marked["session"] = true
	updated, cmd := m.Update(runeKey('r'))
	got := updated.(modelState)
	if got.overlay != overlayBatchTitle || cmd == nil {
		t.Fatalf("r with marks did not open batch: overlay=%d cmd=%v status=%q", got.overlay, cmd, got.status)
	}
}

func TestMarkAllOnlyMarksViewport(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	first := m.sessions.SelectedItem().(sessionItem)
	var items []list.Item
	for i := 0; i < 60; i++ {
		row := first
		row.summary.ID = fmt.Sprintf("s%02d", i)
		items = append(items, row)
	}
	m.sessions.SetItems(items)
	m.layout()
	visible := m.sessions.VisibleItems()
	start, end := m.sessions.Paginator.GetSliceBounds(len(visible))
	if end-start >= len(visible) {
		t.Fatalf("test setup shows everything on one page: bounds=[%d,%d) of %d", start, end, len(visible))
	}
	updated, _ := m.Update(runeKey('X'))
	got := updated.(modelState)
	want := map[string]bool{}
	for _, li := range visible[start:end] {
		want[li.(sessionItem).summary.ID] = true
	}
	if len(got.marked) != len(want) {
		t.Fatalf("X marked %d sessions, want the %d on this page", len(got.marked), len(want))
	}
	for id := range want {
		if !got.marked[id] {
			t.Fatalf("X missed a visible session: %s", id)
		}
	}
	// A second X clears the page again.
	updated, _ = got.Update(runeKey('X'))
	if len(updated.(modelState).marked) != 0 {
		t.Fatalf("second X left marks: %v", updated.(modelState).marked)
	}
}

func TestMarkAllSkipsGroupHeaders(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.groupMode = groupTree
	first := m.sessions.SelectedItem().(sessionItem)
	var items []list.Item
	for i := 0; i < 60; i++ {
		row := first
		row.summary.ID = fmt.Sprintf("g%02d", i)
		if i%2 == 0 {
			row.summary.ProjectPath = "/tmp/alpha"
		} else {
			row.summary.ProjectPath = "/tmp/beta"
		}
		items = append(items, row)
	}
	m.setSessionItems(items)
	m.layout()
	if !hasGroupHeaders(m.sessions.Items()) {
		t.Fatal("test setup drew no group headers")
	}
	visible := m.sessions.VisibleItems()
	start, end := m.sessions.Paginator.GetSliceBounds(len(visible))
	updated, _ := m.Update(runeKey('X'))
	got := updated.(modelState)
	want := 0
	for _, li := range visible[start:end] {
		if row, ok := li.(sessionItem); ok {
			want++
			if !got.marked[row.summary.ID] {
				t.Fatalf("X missed a visible session: %s", row.summary.ID)
			}
		}
	}
	if len(got.marked) != want {
		t.Fatalf("X marked %d, want the %d sessions on this page", len(got.marked), want)
	}
}

func TestHelpListsNewKeys(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		help := helpActions(layoutTestModel())
		keys := map[string]bool{}
		for _, line := range strings.Split(help, "\n") {
			if k, _, found := strings.Cut(line, "\t"); found {
				keys[k] = true
			}
		}
		for _, k := range []string{"r", "d", "s", "X", "c", "u", "esc", "?"} {
			if !keys[k] {
				t.Fatalf("%s: ? overlay hides %q:\n%s", lang, k, help)
			}
		}
		if strings.Contains(help, "ctrl+r") || strings.Contains(help, "ctrl+d") || strings.Contains(help, "ctrl+t") {
			t.Fatalf("%s: ? overlay still advertises a retired ctrl key:\n%s", lang, help)
		}
		for _, s := range []string{txt.markedFmt, txt.batchNeedsMarks, txt.batchPartialFmt, txt.batchAllFailedFmt} {
			if strings.Contains(s, "ctrl+t") || strings.Contains(s, "ctrl+d") {
				t.Fatalf("%s: status copy still names %q", lang, s)
			}
		}
	}
}

func TestRenameOnFoldedBandDoesNothing(t *testing.T) {
	m := sampleModel(t, 132, 32)
	m.groupMode = groupTree
	m.ungrouped = sampleSessions()
	m.sessions.SetItems(m.groupedItems())
	m.applySessionDelegate()
	m.layout()
	updated, _ := m.Update(runeKey('z'))
	m = updated.(modelState)
	m.sessions.Select(foldedHeaderIndex(t, m))
	if _, ok := m.sessions.SelectedItem().(sessionItem); ok {
		t.Fatal("test setup did not rest on a folded band")
	}
	updated, cmd := m.Update(runeKey('r'))
	got := updated.(modelState)
	if got.overlay != overlayNone || cmd != nil || got.indexing || got.err != "" {
		t.Fatalf("r on a folded band acted: overlay=%d cmd=%v indexing=%v err=%q", got.overlay, cmd, got.indexing, got.err)
	}
}

func TestDeleteSetSkipsTheRunningSession(t *testing.T) {
	m := markedModel(t, 3)
	t.Setenv("PI_SESSION_ID", "three")
	updated, _ := m.Update(runeKey('d'))
	got := updated.(modelState)
	if got.overlay != overlayDelete {
		t.Fatalf("d did not confirm the set: overlay=%d err=%q", got.overlay, got.err)
	}
	if len(got.deleteBatch) != 2 {
		t.Fatalf("deleteBatch holds %d sessions, want the 2 non-running", len(got.deleteBatch))
	}
	for _, sm := range got.deleteBatch {
		if sm.ID == "three" {
			t.Fatal("the running session entered the delete set")
		}
	}
	if got.deleteBatchSkip.count != 1 {
		t.Fatalf("skip=%+v, want the running session counted", got.deleteBatchSkip)
	}
}

func TestHelpOverlayFitsMinimumTerminal(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		m := layoutTestModel()
		m.width, m.height = 40, 12
		m.overlay = overlayHelp
		m.layout()
		view := m.View()
		if w := lipgloss.Width(view); w > 40 {
			t.Fatalf("%s: help view is %d cells wide", lang, w)
		}
		if h := lipgloss.Height(view); h > 12 {
			t.Fatalf("%s: help view is %d lines tall", lang, h)
		}
	}
}

func TestDeleteKeyAsksAboutTheMarkedSet(t *testing.T) {
	m := markedModel(t, 3)
	updated, _ := m.Update(runeKey('d'))
	got := updated.(modelState)
	if got.overlay != overlayDelete || len(got.deleteBatch) != 3 {
		t.Fatalf("d with marks did not confirm the set: overlay=%d batch=%d", got.overlay, len(got.deleteBatch))
	}
}

func TestDeleteKeyConfirmsASingleSession(t *testing.T) {
	m := markedModel(t, 0)
	updated, _ := m.Update(runeKey('d'))
	got := updated.(modelState)
	if got.overlay != overlayDelete || len(got.deleteBatch) != 0 {
		t.Fatalf("d without marks did not confirm the row: overlay=%d batch=%d", got.overlay, len(got.deleteBatch))
	}
}

func TestRefreshMovedToS(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(runeKey('s'))
	got := updated.(modelState)
	if !got.indexing || !got.loading || cmd == nil {
		t.Fatalf("s did not refresh: indexing=%v loading=%v cmd=%v", got.indexing, got.loading, cmd)
	}
}

func TestHiddenAliasesStillReachTheSameOverlays(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if got := updated.(modelState); got.overlay != overlayRename {
		t.Fatalf("ctrl+r lost the rename box: overlay=%d", got.overlay)
	}

	m2 := markedModel(t, 2)
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if got := updated.(modelState); got.overlay != overlayDelete || len(got.deleteBatch) != 2 {
		t.Fatalf("ctrl+d lost the set confirmation: overlay=%d batch=%d", got.overlay, len(got.deleteBatch))
	}

	m3 := batchTestModel()
	m3.marked["session"] = true
	updated, cmd := m3.Update(ctrlTKey())
	if got := updated.(modelState); got.overlay != overlayBatchTitle || cmd == nil {
		t.Fatalf("ctrl+t lost the batch flow: overlay=%d cmd=%v", got.overlay, cmd)
	}

	// Aliases are frozen on the old behavior: ctrl+r stays single-only even
	// with a set marked, ctrl+t stays batch-only without one.
	m4 := batchTestModel()
	m4.marked["session"] = true
	updated, _ = m4.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if got := updated.(modelState); got.overlay != overlayRename {
		t.Fatalf("ctrl+r with marks left single rename: overlay=%d", got.overlay)
	}
	m5 := batchTestModel()
	updated, _ = m5.Update(ctrlTKey())
	if got := updated.(modelState); got.overlay == overlayBatchTitle || got.status != txt.batchNeedsMarks {
		t.Fatalf("ctrl+t without marks lost the mark hint: overlay=%d status=%q", got.overlay, got.status)
	}
}
