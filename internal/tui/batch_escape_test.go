package tui

import (
	"testing"

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
