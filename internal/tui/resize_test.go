package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// runCmd executes a command and flattens the batches it fans out into, so a
// test can say what a startup actually asks the terminal for.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, runCmd(c)...)
	}
	return msgs
}

func containsMsg(msgs []tea.Msg, want tea.Msg) bool {
	for _, msg := range msgs {
		if msg == want {
			return true
		}
	}
	return false
}

// Ghostty can report the pre-layout size of a window that is still settling,
// and no SIGWINCH follows once it has settled. The opening frame therefore has
// to be checked against the terminal that is really there.
func TestStartupRemeasuresTheTerminal(t *testing.T) {
	msgs := runCmd(onSizeProbe(sizeProbeMsg{step: 0}))
	if !containsMsg(msgs, tea.WindowSize()()) {
		t.Fatalf("the first probe did not re-measure the terminal: %v", msgs)
	}
	if !containsMsg(msgs, tea.ClearScreen()) {
		t.Fatalf("the first probe did not repaint the opening frame: %v", msgs)
	}
	if !containsMsg(msgs, sizeProbeMsg{step: 1}) {
		t.Fatalf("the first probe did not queue the next one: %v", msgs)
	}
}

// The probes stop. A program waiting on a keypress should not be woken for the
// rest of its life by a window that settled in the first second.
func TestProbingStopsOnceTheWindowHasSettled(t *testing.T) {
	last := len(sizeProbeDelays) - 1
	if probeSizeCmd(last) == nil {
		t.Fatal("the last delay never fires")
	}
	if probeSizeCmd(last+1) != nil {
		t.Fatal("the probes keep rescheduling past the last delay")
	}
	msgs := runCmd(onSizeProbe(sizeProbeMsg{step: last}))
	if !containsMsg(msgs, tea.WindowSize()()) {
		t.Fatalf("the last probe did not re-measure the terminal: %v", msgs)
	}
	for _, msg := range msgs {
		if _, ok := msg.(sizeProbeMsg); ok {
			t.Fatalf("the last probe queued another one: %v", msgs)
		}
	}
}

// A probe that confirms the size already on screen is not a resize. Clearing
// for it would flicker both screens a few times on every startup.
func TestAConfirmedSizeDoesNotRepaint(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if _, cmd := updated.Update(tea.WindowSizeMsg{Width: 120, Height: 40}); cmd != nil {
		t.Fatalf("the browser repainted for a size it was already drawn at: %T", cmd())
	}

	s := setupFixture()
	if _, cmd := s.Update(tea.WindowSizeMsg{Width: s.width, Height: s.height}); cmd != nil {
		t.Fatalf("setup repainted for a size it was already drawn at: %T", cmd())
	}
}

// The size another ends up with is the one the last probe reported, not the one
// the window opened at.
func TestALateSizeReplacesTheOpeningOne(t *testing.T) {
	m := layoutTestModel()
	opened, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	settled, cmd := opened.Update(tea.WindowSizeMsg{Width: 143, Height: 46})
	state, ok := settled.(modelState)
	if !ok {
		t.Fatalf("the browser returned %T", settled)
	}
	if state.width != 143 || state.height != 46 {
		t.Fatalf("the browser kept the opening size: %dx%d", state.width, state.height)
	}
	if cmd == nil || cmd() != tea.ClearScreen() {
		t.Fatal("the browser did not repaint from a clean screen for the settled size")
	}
}
