package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/model"
)

// bandTestModel is a browser holding one ordinary session, sized by the caller.
func bandTestModel(t *testing.T, width, height int) modelState {
	t.Helper()
	m := layoutTestModel()
	m.sessions.SetItems([]list.Item{sessionItem{
		summary: model.Summary{ID: "abcdef01", Provider: "pi", Title: "0908｜修复｜标题存储迁移", ProjectPath: "/tmp/project"},
	}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(modelState)
}

// A terminal wider than the band gets margins, not a stretched row. Before the
// band, every column on a session row was bounded except the title, which took
// the leftover and padded it — so an ultrawide window put the title on the far
// left, the path on the far right, and a hundred cells of nothing between them.
func TestWideTerminalGetsMarginsNotAStretchedRow(t *testing.T) {
	m := bandTestModel(t, 240, 40)
	if m.bandWidth() != contentBandWidth {
		t.Fatalf("bandWidth = %d, want %d", m.bandWidth(), contentBandWidth)
	}
	wantLeft := (240 - contentBandWidth) / 2
	if m.bandLeft() != wantLeft {
		t.Fatalf("bandLeft = %d, want %d", m.bandLeft(), wantLeft)
	}
	for i, line := range strings.Split(m.View(), "\n") {
		stripped := ansi.Strip(line)
		if got := len(stripped) - len(strings.TrimLeft(stripped, " ")); got != 0 && got < wantLeft {
			t.Fatalf("line %d starts at column %d, inside the left margin of %d", i, got, wantLeft)
		}
	}
}

// Every row is written out to the terminal's full width. A frame that stopped
// at the band's right edge would leave the previous frame's cells standing
// there after a resize, which is the same stale-border class of bug the
// startup re-measure exists to clear.
func TestEveryRowIsPaintedToTheTerminalEdge(t *testing.T) {
	for _, width := range []int{80, 132, 200} {
		m := bandTestModel(t, width, 30)
		for i, line := range strings.Split(m.View(), "\n") {
			if got := ansi.StringWidth(line); got != width {
				t.Fatalf("terminal %d: line %d width = %d, want the full %d", width, i, got, width)
			}
		}
	}
}

// A terminal narrower than the band is the band. Nothing is centred, nothing is
// held back, and the browser fills the window exactly as it did before.
func TestNarrowTerminalIsTheBand(t *testing.T) {
	m := bandTestModel(t, 100, 30)
	if m.bandWidth() != 100 {
		t.Fatalf("bandWidth = %d, want the terminal's own 100", m.bandWidth())
	}
	if m.bandLeft() != 0 {
		t.Fatalf("bandLeft = %d, want no margin", m.bandLeft())
	}
}

// The rows stop at the band, but the footer is a run of words with nothing
// lining up under it. Cutting the keymap short to respect the rows' boundary
// would hide keys to buy an alignment no one reads, so it may run on to the
// terminal's right edge.
func TestFooterMayRunPastTheBand(t *testing.T) {
	m := bandTestModel(t, 240, 30)
	if got := m.bandWidth() + m.bandLeft(); got >= 240 {
		t.Fatalf("band already reaches the edge at %d; this test proves nothing", got)
	}
	footer := ansi.Strip(m.footerView())
	if ansi.StringWidth(footer) <= m.bandWidth() {
		t.Skip("this footer is short enough to fit the band; nothing to prove here")
	}
	if got := ansi.StringWidth(footer); got > 240-m.bandLeft() {
		t.Fatalf("footer width = %d, past the terminal's own right edge at %d", got, 240-m.bandLeft())
	}
}

// The too-small refusal is measured against the real terminal, not the band:
// it is the one thing that has to be readable when the window is smaller than
// anything the browser can draw.
func TestTooSmallStillMeasuresTheTerminal(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 39, 11
	view := m.View()
	if !strings.Contains(view, "Terminal too small") {
		t.Fatalf("view = %q", view)
	}
	if got := ansi.StringWidth(view); got > 39 {
		t.Fatalf("refusal width = %d, wider than the terminal at 39", got)
	}
}
