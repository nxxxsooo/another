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
	wantBand := min(240*contentBandPercent/100, naturalRowWidth())
	if m.bandWidth() != wantBand {
		t.Fatalf("bandWidth = %d, want %d", m.bandWidth(), wantBand)
	}
	wantLeft := (240 - wantBand) / 2
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
	for _, width := range []int{100, contentBandFloor} {
		m := bandTestModel(t, width, 30)
		if m.bandWidth() != width {
			t.Fatalf("bandWidth = %d, want the terminal's own %d", m.bandWidth(), width)
		}
		if m.bandLeft() != 0 {
			t.Fatalf("bandLeft = %d at width %d, want no margin", m.bandLeft(), width)
		}
	}
}

// The band is a proportion between two flats. Below the floor it is the
// terminal; past the floor it takes its share of the window, so a bigger screen
// buys a bigger list instead of showing what the floor showed; and once the
// columns are full it stops, because every further cell would be padding.
//
// The three meet exactly, so the band never jumps — it only ever stops.
func TestBandGrowsWithTheTerminal(t *testing.T) {
	// The first terminal wide enough for the proportion to beat the floor,
	// found rather than derived: integer division decides it, and a formula
	// that rounds the other way would test a width the layout never sees.
	crossover := contentBandFloor
	for crossover*contentBandPercent/100 <= contentBandFloor {
		crossover++
	}
	ceiling := naturalRowWidth()
	previous := 0
	for width := 40; width <= 400; width++ {
		m := bandTestModel(t, width, 30)
		band := m.bandWidth()
		switch {
		case band < previous:
			t.Fatalf("terminal %d: band shrank from %d to %d", width, previous, band)
		case band > width:
			t.Fatalf("terminal %d: band %d is wider than the terminal", width, band)
		case band > ceiling:
			t.Fatalf("terminal %d: band %d is wider than a full row at %d", width, band, ceiling)
		case width >= crossover && band >= width:
			t.Fatalf("terminal %d: band %d kept the whole width instead of leaving margin", width, band)
		case width >= crossover && band <= contentBandFloor && band < ceiling:
			t.Fatalf("terminal %d: band stuck at the floor %d instead of growing", width, band)
		}
		previous = band
	}
	if got := bandTestModel(t, 400, 30).bandWidth(); got != ceiling {
		t.Fatalf("band at 400 = %d, want a full row at %d", got, ceiling)
	}
	// The middle of the range is the proportion's own, and it has to actually
	// be reached: a ceiling that undercut the floor would make the whole
	// proportional stretch unreachable and this test vacuous.
	middle := bandTestModel(t, 180, 30).bandWidth()
	if middle <= contentBandFloor || middle >= ceiling {
		t.Fatalf("band at 180 = %d, want the proportion between %d and %d", middle, contentBandFloor, ceiling)
	}
}

// The footer sits inside the band, under the rows it describes. It was once
// allowed to run past the band's right edge, because a keymap assembled from
// every supported action did not fit inside it — that keymap is the ? overlay
// now, and the line that replaced it lines up with the pane.
func TestFooterStaysInsideTheBand(t *testing.T) {
	m := bandTestModel(t, 240, 30)
	if got := m.bandWidth() + m.bandLeft(); got >= 240 {
		t.Fatalf("band already reaches the edge at %d; this test proves nothing", got)
	}
	for _, line := range strings.Split(ansi.Strip(m.footerView()), "\n") {
		if got := ansi.StringWidth(line); got > m.bandWidth() {
			t.Fatalf("footer line is %d cells, past the band at %d: %q", got, m.bandWidth(), line)
		}
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
