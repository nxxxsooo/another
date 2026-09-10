package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/titler"
)

// An idle batch closes on the first esc: there is nothing to wind down.
func TestBatchEscapesImmediatelyWhenIdle(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchResults = []titler.BatchResult{{SessionID: "s1", Current: "a", Title: "b"}}

	got, _ := m.Update(escKey())
	if left := got.(modelState); left.overlay != overlayNone {
		t.Fatalf("overlay = %d, want closed", left.overlay)
	}
}

// Closing the review keeps the marks, so ctrl+t reopens on the rows that
// failed — but it says so, and says how to put them down. Without the second
// step the only way out of a partial selection was X twice, which marks every
// row on the page before clearing them.
func TestEscLeavesTheReviewThenClearsTheMarks(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.marked["session"] = true
	m.batchResults = []titler.BatchResult{{SessionID: "session", Current: "x", Title: "y"}}

	closed, _ := m.Update(escKey())
	after := closed.(modelState)
	if after.overlay != overlayNone {
		t.Fatalf("esc did not close the review: overlay=%d", after.overlay)
	}
	if !after.marked["session"] {
		t.Fatal("closing the review dropped the marks a retry needs")
	}
	if !strings.Contains(ansi.Strip(after.status), "esc") {
		t.Fatalf("nothing says the marks are still there and how to clear them: %q", after.status)
	}

	cleared, _ := after.Update(escKey())
	last := cleared.(modelState)
	if len(last.marked) != 0 {
		t.Fatalf("esc in the list did not clear the marks: %v", last.marked)
	}
	if last.status != "" {
		t.Errorf("the mark status outlived the marks: %q", last.status)
	}
}

// esc puts down one thing at a time, so a search still outranks a selection:
// clearing marks first would leave the person looking at a filtered list they
// just asked to leave.
func TestEscClearsTheSearchBeforeTheMarks(t *testing.T) {
	m := batchTestModel()
	m.marked["session"] = true
	m.searchQuery = "anything"

	updated, _ := m.Update(escKey())
	after := updated.(modelState)
	if after.searchQuery != "" {
		t.Fatal("esc did not clear the search first")
	}
	if !after.marked["session"] {
		t.Fatal("esc took the marks with the search")
	}
}

// The first esc during a run asks it to stop and stays, so rows that already
// have a suggestion are still on screen while it winds down.
func TestBatchFirstEscapeCancelsWithoutClosing(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchRunning = true
	m.batchTotal = 3
	cancelled := false
	m.batchCancel = func() { cancelled = true }

	got, _ := m.Update(escKey())
	after := got.(modelState)
	if !cancelled {
		t.Error("first esc did not cancel the run")
	}
	if after.overlay != overlayBatchTitle {
		t.Errorf("overlay = %d, want the batch to stay open while cancelling", after.overlay)
	}
	if !after.batchCancelling {
		t.Error("first esc did not mark the run as cancelling")
	}
}

// A second esc leaves regardless of whether the engine has closed its channel.
// batchRunning only clears on batchFinishedMsg, which waits on an agent CLI; one
// that ignores its cancelled context used to hold the overlay open with no exit
// but quitting another entirely.
func TestBatchSecondEscapeLeavesAStuckRun(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchRunning = true
	m.batchTotal = 3
	m.batchCancel = func() {}

	first, _ := m.Update(escKey())
	second, _ := first.(modelState).Update(escKey())
	after := second.(modelState)

	if after.overlay != overlayNone {
		t.Fatalf("overlay = %d, want closed after a second esc", after.overlay)
	}
	if after.batchRunning || after.batchCancelling {
		t.Errorf("run state survived the close: running=%v cancelling=%v",
			after.batchRunning, after.batchCancelling)
	}
}

// Leaving a live run orphans its results: a suggestion that lands after the
// overlay closed belongs to a batch that no longer exists.
func TestBatchLeavingARunDiscardsLateResults(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchRunning = true
	m.batchTotal = 3
	m.batchCancel = func() {}
	gen := m.batchGen

	first, _ := m.Update(escKey())
	second, _ := first.(modelState).Update(escKey())
	closed := second.(modelState)

	late, _ := closed.Update(batchResultMsg{gen: gen, res: titler.BatchResult{
		SessionID: "s1", Current: "a", Title: "b",
	}})
	after := late.(modelState)

	if after.overlay != overlayNone {
		t.Errorf("a late result reopened the overlay: %d", after.overlay)
	}
	if len(after.batchResults) != 0 {
		t.Errorf("late result was kept: %+v", after.batchResults)
	}
}
