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
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// The goodbye wordmark lives in logo_face.go, rasterized from a real typeface
// by scripts/render-logo-face.py. It is drawn with half-block characters so the
// sub-pixel grid is square: a terminal cell is about 1:2, and a whole-block face
// on that grid produces a mark twice as tall as it should be.
//
// Only foreground colour is used. Painting an inherited background here would
// reintroduce the Ghostty artifact documented in theme.go, where nested ANSI
// resets punch black rectangles through the fill.
const (
	// logoWord is the string scripts/render-logo-face.py rasterizes. Changing
	// it here does nothing until the face is regenerated.
	logoWord    = "another"
	logoTagline = "Keep the session. Change the agent."

	// tearSeam is the row the mark splits on when it ruptures. It sits below
	// the ascenders so t and h keep their stems, and on a cell boundary so no
	// single cell ever needs two colours at once.
	tearSeam  = 2
	tearShift = 2
)

// shades run from full ink to none. The rules above and below the mark are a
// halftone decay rather than a repeating hatch: the project's own direction
// asks for screen-print texture, and a tiled hatch is another product's mark.
var shades = []rune{'█', '▓', '▒', '░', ' '}

// logoVersion is set once by the CLI so the ldflags-stamped version stays in
// internal/cli and the TUI keeps no build metadata of its own.
var logoVersion string

// SetVersion hands the running binary's version to the goodbye logo.
func SetVersion(v string) { logoVersion = v }

func markHeight() int { return len(wordmarkRows) }

// logoWidth includes the room the upper half needs when it slips.
func logoWidth() int { return markWidth + tearShift }

// logoHeight counts the rule, the mark, the closing rule, and the caption.
func logoHeight() int { return markHeight() + 3 }

// inkRule draws a band of ink running out across the width.
func inkRule(width int, reversed bool) string {
	row := make([]rune, width)
	for i := range row {
		row[i] = shades[i*len(shades)/width]
	}
	if reversed {
		for i, j := 0, len(row)-1; i < j; i, j = i+1, j-1 {
			row[i], row[j] = row[j], row[i]
		}
	}
	return string(row)
}

func rgbOf(k charmtone.Key) (r, g, b int) {
	hex := strings.TrimPrefix(k.Hex(), "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0, 0, 0
	}
	return int(v>>16) & 0xFF, int(v>>8) & 0xFF, int(v) & 0xFF
}

// mix walks from one palette colour to another. The two states are the same
// identity, so they share a path rather than sitting as two unrelated hues.
func mix(from, to charmtone.Key, t float64) lipgloss.Color {
	fr, fg, fb := rgbOf(from)
	tr, tg, tb := rgbOf(to)
	at := func(a, b int) int { return a + int(math.Round(float64(b-a)*t)) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", at(fr, tr), at(fg, tg), at(fb, tb)))
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

// shiftRow moves one mark row right by off cells and pads it to width, so every
// frame is the same rectangle and an in-place redraw leaves no debris.
func shiftRow(row string, off, width int) string {
	out := make([]rune, width)
	for i := range out {
		out[i] = ' '
	}
	for i, r := range []rune(row) {
		if i+off < width {
			out[i+off] = r
		}
	}
	return string(out)
}

// renderFrame draws one moment of the goodbye. slip displaces the upper half by
// whole cells, because a terminal cannot slide half a column; tension separates
// the two states by colour, which terminals can interpolate.
func renderFrame(slip int, tension float64) string {
	width := logoWidth()
	inRegister := lipgloss.NewStyle().Foreground(charm(charmtone.Charple))
	displaced := lipgloss.NewStyle().Foreground(mix(charmtone.Charple, charmtone.Julep, tension))

	lines := make([]string, 0, logoHeight())
	// The top rule belongs to the displaced state, the bottom to the one still
	// in register, so the bands read as the two halves rather than decoration.
	lines = append(lines, displaced.Render(inkRule(width, false)))
	for r, row := range wordmarkRows {
		if r < tearSeam {
			lines = append(lines, displaced.Render(shiftRow(row, slip, width)))
			continue
		}
		lines = append(lines, inRegister.Render(shiftRow(row, 0, width)))
	}
	lines = append(lines, inRegister.Render(inkRule(width, true)), captionRow(width))
	return strings.Join(lines, "\n")
}

// restFrame is the mark in register and in one colour: the state the terminal
// keeps in its scrollback after the animation settles.
func restFrame() string { return renderFrame(0, 0) }

// goodbyeScript is the whole story: hold still, rupture once, hold the rupture,
// return to stillness. It is a single event, not a glitch loop.
var goodbyeScript = []struct {
	slip    int
	tension float64
}{
	{0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0}, {0, 0},
	{1, 0.55}, {2, 1},
	{2, 1}, {2, 1}, {2, 1}, {2, 1}, {2, 1},
	{1, 0.5}, {0, 0.15},
	{0, 0},
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
	fmt.Fprint(w, "\n"+renderFrame(goodbyeScript[0].slip, goodbyeScript[0].tension)+"\n")
	for _, f := range goodbyeScript[1:] {
		time.Sleep(frameDelay)
		// Up over the block, wipe what follows, repaint.
		fmt.Fprintf(w, "\x1b[%dA\r\x1b[0J", logoHeight())
		fmt.Fprint(w, renderFrame(f.slip, f.tension)+"\n")
	}
	fmt.Fprintln(w)
}
