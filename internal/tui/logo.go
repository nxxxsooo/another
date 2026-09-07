package tui

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// The goodbye wordmark is rasterized from a real typeface into
// internal/tui/logo_face.go by scripts/render-logo-face.py. It is drawn with
// half-block characters so the sub-pixel grid is square: a terminal cell is
// about 1:2, and a whole-block face on that grid produces a mark twice as tall
// as it should be.
//
// The mark is printed twice, offset horizontally, in the two brand states: the
// agent the session came from and the agent it went to. Where the two copies
// overlap the cell is lit near-white, which is the part of the session both
// agents hold. This is the identity PRODUCT.md already resolved — two
// near-coincident forms that read as one thing which cannot shed its extra
// presence — and the same channel-split language the README's hero banner uses,
// so the goodbye and the landing image read as one mark.
//
// The overlap is twinTheme.text rather than twinTheme.intersection, even though
// the palette names the latter for exactly this job. The brand asks the
// intersection to bloom brighter than both forms, and #68FFD6 only blooms
// against the master artwork's deeper green. Against Julep it is barely a shade
// apart, so the overlap collapses into the target fringe and the mark loses its
// third reading. Text white is the one entry in the palette that still reads as
// a bloom over both agents' colours.
//
// Painting a background here is safe and load-bearing: a cell whose two halves
// need different colours is drawn as a half block with one colour in the
// foreground and the other in the background. The rule recorded in theme.go is
// narrower than it sounds — it forbids an *inherited* background behind nested
// styled content, because the nested content emits ANSI resets that clear the
// fill and leave black rectangles in Ghostty. Every cell here is a leaf style
// over plain runes with nothing nested inside it, exactly like the agent chips
// in agent_chip.go, which have painted their own backgrounds all along.
const (
	// logoWord is the string scripts/render-logo-face.py rasterizes. Changing
	// it here does nothing until the face is regenerated.
	logoWord    = "another"
	logoTagline = "Keep the session. Change the agent."

	// ghostShift is the furthest the target copy ever travels from the source
	// copy, and so the extra width every frame reserves. ghostRest is where it
	// settles: one cell is half a stem, which leaves the word legible with a
	// coloured fringe down each edge, while the wider overshoot only happens in
	// passing.
	ghostShift = 2
	ghostRest  = 1
)

// inkState is what a single sub-pixel belongs to once the two offset copies of
// the mark are laid over each other.
type inkState uint8

const (
	bare inkState = iota
	sourceOnly
	targetOnly
	shared
)

// logoVersion is set once by the CLI so the ldflags-stamped version stays in
// internal/cli and the TUI keeps no build metadata of its own.
var logoVersion string

// SetVersion hands the running binary's version to the goodbye logo.
func SetVersion(v string) { logoVersion = v }

func markHeight() int { return len(faceBits) / 2 }

// logoWidth includes the room the target copy needs when it slides clear.
func logoWidth() int { return markWidth + ghostShift }

// logoHeight counts the mark and the caption beneath it.
func logoHeight() int { return markHeight() + 1 }

func rgbOf(c lipgloss.Color) (r, g, b int) {
	hex := strings.TrimPrefix(string(c), "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0, 0, 0
	}
	return int(v>>16) & 0xFF, int(v>>8) & 0xFF, int(v) & 0xFF
}

// mix walks from one theme colour to another. The two agents are the same
// session, so their colours share a path rather than sitting as two unrelated
// hues that happen to appear together.
func mix(from, to lipgloss.Color, t float64) lipgloss.Color {
	fr, fg, fb := rgbOf(from)
	tr, tg, tb := rgbOf(to)
	at := func(a, b int) int { return a + int(math.Round(float64(b-a)*t)) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", at(fr, tr), at(fg, tg), at(fb, tb)))
}

// faceInk reads one sub-pixel of the rasterized mark, treating everything off
// the bitmap as bare so a shifted copy can hang past either edge.
func faceInk(subRow, col int) bool {
	if subRow < 0 || subRow >= len(faceBits) || col < 0 || col >= markWidth {
		return false
	}
	return faceBits[subRow][col] == '#'
}

// stateAt overlays the source copy at rest with the target copy shifted right
// by dx and reports what the sub-pixel at (subRow, col) now belongs to.
func stateAt(subRow, col, dx int) inkState {
	src := faceInk(subRow, col)
	dst := faceInk(subRow, col-dx)
	switch {
	case src && dst:
		return shared
	case src:
		return sourceOnly
	case dst:
		return targetOnly
	default:
		return bare
	}
}

// markRow folds two sub-pixel rows into one row of cells. A cell whose halves
// disagree is a half block carrying one colour in the foreground and the other
// in the background, which is the only way a terminal cell holds two colours at
// once. Cells that share a colour pair are emitted as a single styled run so a
// frame costs a handful of escape sequences instead of one per column.
func markRow(cellRow, width, dx int, ink [4]lipgloss.Color) string {
	var (
		out    strings.Builder
		run    []rune
		curFG  lipgloss.Color
		curBG  lipgloss.Color
		hadFG  bool
		hadBG  bool
		opened bool
	)

	flush := func() {
		if len(run) == 0 {
			return
		}
		style := lipgloss.NewStyle()
		if hadFG {
			style = style.Foreground(curFG)
		}
		if hadBG {
			style = style.Background(curBG)
		}
		out.WriteString(style.Render(string(run)))
		run = run[:0]
	}

	for col := range width {
		top := stateAt(2*cellRow, col, dx)
		bottom := stateAt(2*cellRow+1, col, dx)

		var (
			glyph  rune
			fg, bg lipgloss.Color
			useFG  bool
			useBG  bool
		)
		switch {
		case top == bare && bottom == bare:
			glyph = ' '
		case top == bottom:
			glyph, fg, useFG = '█', ink[top], true
		case bottom == bare:
			glyph, fg, useFG = '▀', ink[top], true
		case top == bare:
			glyph, fg, useFG = '▄', ink[bottom], true
		default:
			glyph, fg, useFG = '▀', ink[top], true
			bg, useBG = ink[bottom], true
		}

		if !opened || useFG != hadFG || useBG != hadBG || fg != curFG || bg != curBG {
			flush()
			curFG, curBG, hadFG, hadBG, opened = fg, bg, useFG, useBG, true
		}
		run = append(run, glyph)
	}
	flush()
	return out.String()
}

// captionRow puts the tagline hard left and the version hard right.
func captionRow(width int) string {
	version := logoVersion
	if version != "" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	gap := width - lipgloss.Width(logoTagline) - lipgloss.Width(version)
	if gap < 1 {
		return mutedStyle.Render(pad(logoTagline, width))
	}
	return mutedStyle.Render(logoTagline) + strings.Repeat(" ", gap) + mutedStyle.Render(version)
}

func pad(s string, width int) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// renderFrame draws one moment of the goodbye. dx displaces the target copy by
// whole cells, because a terminal cannot slide half a column; tension separates
// the two states by colour, which terminals can interpolate. At tension zero
// every state resolves to the source colour, so the mark is one agent's, whole
// and single, however far the copies have already moved.
func renderFrame(dx int, tension float64) string {
	width := logoWidth()
	ink := [4]lipgloss.Color{
		bare:       lipgloss.Color(""),
		sourceOnly: twinTheme.source,
		targetOnly: mix(twinTheme.source, twinTheme.target, tension),
		shared:     mix(twinTheme.source, twinTheme.text, tension),
	}

	lines := make([]string, 0, logoHeight())
	for row := range markHeight() {
		lines = append(lines, markRow(row, width, dx, ink))
	}
	lines = append(lines, captionRow(width))
	return strings.Join(lines, "\n")
}

// restFrame is the settled mark: the session shown in both places at once. It
// is the frame the terminal keeps in its scrollback, so it is the one that has
// to carry the whole idea on its own.
func restFrame() string { return renderFrame(ghostRest, 1) }

// goodbyeScript is the whole story, and it is told in that order for a reason.
//
// While the copies are still in register every inked sub-pixel is shared, so
// tension alone drives the mark from the source agent's violet up to the plain
// white of the session itself: the handoff, before anything has moved. Only
// then do the copies separate, overshoot by a single frame, and settle a cell
// apart. The white core survives the split as the session both agents now hold,
// with one agent's colour fringing each edge.
//
// It ends split on purpose. The goodbye prints after a migration has already
// happened, and a mark that snapped back to one colour would say the opposite.
var goodbyeScript = []struct {
	dx      int
	tension float64
}{
	{0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0},
	{0, 0.45}, {0, 1},
	{1, 1}, {2, 1},
	{1, 1}, {1, 1}, {1, 1}, {1, 1},
	{ghostRest, 1},
}

const frameDelay = 42 * time.Millisecond

// renderFarewell is the still image, used when motion is unavailable or
// unwanted. It falls back to the compact framed banner when the terminal is too
// narrow for the mark, so the wordmark is never hard-wrapped mid-letter.
func renderFarewell(termWidth int) string {
	if termWidth > 0 && termWidth < logoWidth() {
		return "\n" + renderBanner() + "\n"
	}
	return "\n" + restFrame() + "\n"
}

// motionAllowed keeps the animation out of pipes, logs, CI, and terminals too
// short to redraw in without scrolling the frame apart. An unknown size counts
// as too short: redrawing in place without knowing the height smears the block.
func motionAllowed(termWidth, termHeight int) bool {
	if os.Getenv("ANOTHER_NO_MOTION") != "" || os.Getenv("NO_COLOR") != "" || os.Getenv("CI") != "" {
		return false
	}
	if !term.IsTerminal(os.Stdout.Fd()) || lipgloss.ColorProfile() == termenv.Ascii {
		return false
	}
	if termWidth > 0 && termWidth < logoWidth() {
		return false
	}
	// One spare line for the cursor, one so the first paint does not scroll the
	// block off its own starting row.
	return termHeight >= logoHeight()+2
}

// playFarewell prints the goodbye. It paints the first frame normally so the
// terminal finishes any scrolling, then redraws in place; whatever the terminal
// keeps in scrollback is the settled frame.
func playFarewell(w io.Writer, termWidth, termHeight int, animate bool) {
	if !animate || !motionAllowed(termWidth, termHeight) {
		fmt.Fprintln(w, renderFarewell(termWidth))
		return
	}
	fmt.Fprint(w, "\n"+renderFrame(goodbyeScript[0].dx, goodbyeScript[0].tension)+"\n")
	for _, f := range goodbyeScript[1:] {
		time.Sleep(frameDelay)
		// Up over the block, wipe what follows, repaint.
		fmt.Fprintf(w, "\x1b[%dA\r\x1b[0J", logoHeight())
		fmt.Fprint(w, renderFrame(f.dx, f.tension)+"\n")
	}
	fmt.Fprintln(w)
}
