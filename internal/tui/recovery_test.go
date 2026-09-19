package tui

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestScreenRecoveryPreservesEditingState(t *testing.T) {
	browser := layoutTestModel()
	browser.width, browser.height = 80, 24
	browser.overlay = overlayRename
	browser.renameInput.SetValue("未完成的标题")
	browser.renameInput.SetCursor(3)
	browser.marked["session"] = true
	browser.searchQuery = "active query"
	browser.layout()
	setup := setupFixture()
	setup.modelTyping = true
	setup.modelInput.SetValue("unfinished model")
	setup.modelInput.SetCursor(4)

	for name, initial := range map[string]tea.Model{"browser": browser, "setup": setup} {
		t.Run(name, func(t *testing.T) {
			for _, event := range []tea.Msg{tea.FocusMsg{}, tea.ResumeMsg{}, tea.KeyMsg{Type: tea.KeyCtrlL}} {
				updated, cmd := initial.Update(event)
				if !reflect.DeepEqual(recoveryState(initial), recoveryState(updated)) || initial.View() != updated.View() {
					t.Fatalf("%T changed application state", event)
				}
				msgs := runCmd(cmd)
				for _, want := range []tea.Msg{tea.WindowSize()(), tea.ClearScreen(), tea.HideCursor()} {
					if !containsMsg(msgs, want) {
						t.Fatalf("%T missing recovery action %T", event, want)
					}
				}
			}
			blurred, _ := initial.Update(tea.BlurMsg{})
			focused, _ := blurred.Update(tea.FocusMsg{})
			if !reflect.DeepEqual(recoveryState(initial), recoveryState(focused)) || initial.View() != focused.View() {
				t.Fatal("focus round trip changed application state")
			}
		})
	}
}

// Compare user-owned state, not Bubble components' function-valued key maps
// (functions are never DeepEqual, even when a model is entirely unchanged).
func recoveryState(m tea.Model) []any {
	switch m := m.(type) {
	case modelState:
		return []any{m.width, m.height, m.overlay, m.sessions.Index(), m.marked,
			m.searchQuery, m.renameInput.Value(), m.renameInput.Position(), m.recovery}
	case setupModel:
		return []any{m.width, m.height, m.page, m.cursor, m.selected,
			m.modelTyping, m.modelInput.Value(), m.modelInput.Position(), m.recovery}
	default:
		return nil
	}
}

func TestScreenChecksContinueWithoutFocusReportsAndPauseOnBlur(t *testing.T) {
	for _, blurred := range []bool{false, true} {
		r := screenRecovery{blurred: blurred}
		cmd, handled := r.update(screenCheckMsg{})
		if !handled {
			t.Fatal("screen check not handled")
		}
		msgs := runCmd(cmd)
		if !containsMsg(msgs, screenCheckMsg{}) {
			t.Fatal("screen check did not schedule its successor")
		}
		if containsMsg(msgs, tea.WindowSize()()) == blurred {
			t.Fatalf("dimension check with blurred=%v: %v", blurred, msgs)
		}
		if containsMsg(msgs, tea.ClearScreen()) {
			t.Fatal("periodic recovery must not blank the terminal")
		}
	}
}

// Pin the Bubble Tea behavior runtime recovery depends on. Model-only tests
// cannot prove a frame actually reaches the terminal when View is unchanged.
type recoveryRendererFixture struct{ recovery screenRecovery }

func (m recoveryRendererFixture) Init() tea.Cmd { return nil }
func (m recoveryRendererFixture) View() string {
	return "RECOVERY FIRST ROW\nunchanged middle row\nRECOVERY LAST ROW"
}
func (m recoveryRendererFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd, _ := m.recovery.update(msg)
	return m, cmd
}

type recoveryOutput struct{ frames chan string }

func (o recoveryOutput) Write(p []byte) (int, error) {
	// Capture only frame writes; mode changes and cursor commands are separate.
	if strings.Contains(string(p), "RECOVERY FIRST ROW") {
		o.frames <- string(p)
	}
	return len(p), nil
}

func TestRuntimeRecoveryReemitsUnchangedFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output := recoveryOutput{frames: make(chan string, 16)}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	query := tea.WindowSize()()
	p := tea.NewProgram(recoveryRendererFixture{}, tea.WithContext(ctx),
		tea.WithInput(reader), tea.WithOutput(output), tea.WithAltScreen(),
		tea.WithoutSignalHandler(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			// A pipe has no ioctl dimensions. Stand in for the TTY measurement,
			// leaving the actual event loop and renderer intact.
			if reflect.TypeOf(msg) == reflect.TypeOf(query) {
				return tea.WindowSizeMsg{Width: 80, Height: 24}
			}
			return msg
		}))
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	defer func() {
		p.Quit()
		if err := <-done; err != nil {
			t.Errorf("program failed: %v", err)
		}
	}()
	frame := func() string {
		t.Helper()
		select {
		case got := <-output.frames:
			if !strings.Contains(got, "RECOVERY LAST ROW") || !strings.Contains(got, "unchanged middle row") {
				t.Fatal("only part of the unchanged frame was repainted")
			}
			return got
		case <-ctx.Done():
			t.Fatal("renderer suppressed the unchanged frame")
			return ""
		}
	}
	frame()
	// Send a check directly to avoid waiting for its two-second timer. Both
	// checks measure 80x24, so the second proves same-size cache invalidation.
	for range 2 {
		p.Send(screenCheckMsg{})
		if strings.Contains(frame(), "\x1b[2J") {
			t.Fatal("periodic repaint cleared the screen")
		}
	}
}
