package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Bubbletea measures the terminal once when the program starts and then only
// when SIGWINCH arrives. Ghostty hands the pty its size before macOS has
// finished laying the window out, so that first measurement races the window's
// own resize, and the stale answer can be the one that lands last. Nothing
// follows it: the window is already where it wants to be, so no signal ever
// corrects the frame, and another keeps drawing for a terminal that is not
// there — a list wider than the window wraps every row, one narrower leaves the
// old border standing.
//
// So another asks again. Each probe re-measures the real terminal, and the
// window has settled long before the last one is due.
var sizeProbeDelays = []time.Duration{
	40 * time.Millisecond,
	200 * time.Millisecond,
	600 * time.Millisecond,
}

// sizeProbeMsg is the nth re-measure coming due.
type sizeProbeMsg struct{ step int }

// probeSizeCmd schedules one probe and returns nil once the window has had long
// enough to settle. Probing forever would keep waking a program that is only
// waiting for a keypress.
func probeSizeCmd(step int) tea.Cmd {
	if step < 0 || step >= len(sizeProbeDelays) {
		return nil
	}
	return tea.Tick(sizeProbeDelays[step], func(time.Time) tea.Msg {
		return sizeProbeMsg{step: step}
	})
}

// onSizeProbe re-measures the terminal and queues the next probe. The first one
// also repaints: another's opening frame goes up while the window is still
// moving, and whatever Ghostty left on the alternate screen underneath it
// survives until something clears it.
func onSizeProbe(msg sizeProbeMsg) tea.Cmd {
	cmds := []tea.Cmd{tea.WindowSize(), probeSizeCmd(msg.step + 1)}
	if msg.step == 0 {
		cmds = append(cmds, tea.ClearScreen)
	}
	return tea.Batch(cmds...)
}
