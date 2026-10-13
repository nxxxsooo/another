package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
