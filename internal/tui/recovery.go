package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const screenCheckInterval = 2 * time.Second

type screenCheckMsg struct{}

// screenRecovery is shared by the browser and setup. Terminal damage is not
// observable through the TTY: periodically discard the renderer's assumptions
// even when our model has not changed. Focus reports are an optional fast path.
type screenRecovery struct {
	blurred bool
}

func screenCheckCmd() tea.Cmd {
	return tea.Tick(screenCheckInterval, func(time.Time) tea.Msg {
		return screenCheckMsg{}
	})
}

func recoverScreenCmd() tea.Cmd {
	return tea.Batch(tea.WindowSize(), tea.HideCursor, tea.ClearScreen)
}

func (r *screenRecovery) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case screenCheckMsg:
		if r.blurred {
			return screenCheckCmd(), true
		}
		// Bubble Tea v1 invalidates BOTH frame and line caches on every
		// WindowSizeMsg, including an unchanged size. This re-emits the full
		// frame without blanking the screen first, while also recovering from
		// a missed resize signal. Do not replace this with a plain model tick:
		// an unchanged View would be suppressed by the renderer.
		return tea.Batch(tea.WindowSize(), screenCheckCmd()), true
	case tea.BlurMsg:
		r.blurred = true
		return nil, true
	case tea.FocusMsg, tea.ResumeMsg:
		r.blurred = false
		return recoverScreenCmd(), true
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlL {
			return recoverScreenCmd(), true
		}
	}
	return nil, false
}
