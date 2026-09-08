package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/titler"
)

func renamingModel(t *testing.T) modelState {
	t.Helper()
	m := layoutTestModel()
	m.titleCfg = titler.Config{Provider: "pi"}
	m.width, m.height = 100, 30
	m.layout()
	opened, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := opened.(modelState)
	if !got.suggesting || cmd == nil {
		t.Fatalf("ctrl+r did not start a suggestion: suggesting=%v cmd=%v", got.suggesting, cmd)
	}
	return got
}

// One session or fifty, waiting on an agent looks the same: the rename box
// draws the spinner ctrl+t draws, because a still line reads as a hang when the
// agent takes several seconds to answer.
func TestSuggestionWaitDrawsTheBatchSpinner(t *testing.T) {
	m := renamingModel(t)

	line := m.suggestionLine()
	if !strings.Contains(line, m.spinner.View()) {
		t.Fatalf("suggestion wait has no spinner: %q", line)
	}
	if !strings.Contains(line, txt.suggestionLoading) {
		t.Fatalf("suggestion wait lost its label: %q", line)
	}
	if !strings.Contains(m.View(), strings.TrimSpace(txt.suggestionLoading)) {
		t.Fatal("the wait is not on screen")
	}
}

// A spinner only moves while something re-arms its tick. The rename overlay
// hands non-key messages to the text input, so a suggestion in flight has to
// claim the ticks or the frame freezes on the first dot for the whole wait.
func TestSuggestionSpinnerAdvancesWhileTheBoxIsOpen(t *testing.T) {
	m := renamingModel(t)

	before := m.spinner.View()
	updated, next := m.Update(m.spinner.Tick())
	got := updated.(modelState)
	if got.spinner.View() == before || next == nil {
		t.Fatalf("suggestion spinner did not advance: before=%q after=%q next=%v",
			before, got.spinner.View(), next)
	}
	if !got.renameInput.Focused() || got.renameInput.Value() != "A useful title" {
		t.Fatalf("a tick disturbed the field: focused=%v value=%q",
			got.renameInput.Focused(), got.renameInput.Value())
	}
}

// The tick loop is only alive while a suggestion is: once the answer lands or
// the box closes, another must stop asking bubbletea to wake it up.
func TestSuggestionSpinnerStopsWhenTheWaitEnds(t *testing.T) {
	arrived, _ := renamingModel(t).Update(titleSuggestionMsg{sessionID: "session", title: "0908｜功能｜标题等待动画"})
	settled := arrived.(modelState)
	if _, next := settled.Update(settled.spinner.Tick()); next != nil {
		t.Fatal("the spinner kept ticking after the suggestion landed")
	}

	closed, _ := renamingModel(t).Update(tea.KeyMsg{Type: tea.KeyEsc})
	left := closed.(modelState)
	if _, next := left.Update(left.spinner.Tick()); next != nil {
		t.Fatal("the spinner kept ticking after the box closed")
	}
}
