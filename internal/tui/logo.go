package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// The landing-page wordmark is drawn with half blocks to preserve its square
// pixel grid. The violet face stays fixed; its mint copy slides in behind it.
const (
	logoTagline = "Keep the session. Change the agent."

	ghostShift = 5 // Maximum horizontal offset, in columns.
	restShift  = 1 // Horizontal offset of the settled shadow, in columns.
	restDrop   = 1 // Vertical offset of the settled shadow, in sub-rows.
)

type inkState uint8

const (
	bare inkState = iota
	face
	shadow
)

// logoVersion is set once by the CLI so the ldflags-stamped version stays in
// internal/cli and the TUI keeps no build metadata of its own.
var logoVersion string

// SetVersion hands the running binary's version to the goodbye logo.
func SetVersion(v string) { logoVersion = v }

func markHeight() int { return (faceSubRows + restDrop + 1) / 2 }

// logoWidth includes the room the target copy needs when it slides clear.
func logoWidth() int { return markWidth + ghostShift }

// logoHeight counts the mark and the caption beneath it.
func logoHeight() int { return markHeight() + 1 }

// faceInk treats everything outside the bitmap as bare.
func faceInk(subRow, col int) bool {
	if subRow < 0 || subRow >= len(faceBits) || col < 0 || col >= markWidth {
		return false
	}
	return faceBits[subRow][col] == '#'
}

// Uneven, fixed scanline offsets keep the tear reproducible in the README GIF.
var tearBands = [...]int{0, +1, -1, +2, -1}

// rowGap keeps each torn band inside the reserved width.
func rowGap(cellRow, gap, tear int) int {
	return min(max(gap+tear*tearBands[cellRow%len(tearBands)], restShift), ghostShift)
}

// stateAt draws the violet face in front of the offset mint copy.
func stateAt(subRow, col, gap, drop int) inkState {
	switch {
	case faceInk(subRow, col):
		return face
	case faceInk(subRow-drop, col-gap):
		return shadow
	default:
		return bare
	}
}

// markRow folds two sub-pixel rows into one row of cells. A cell whose halves
// differ in colour uses a foreground and background on a half block. Styles
// cover plain glyph runs only, so nested ANSI resets cannot clear a background.
func markRow(cellRow, width, gap, drop int, ink [3]lipgloss.Color) string {
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
		top := stateAt(2*cellRow, col, gap, drop)
		bottom := stateAt(2*cellRow+1, col, gap, drop)

		var (
			glyph  rune
			fg, bg lipgloss.Color
			useFG  bool
			useBG  bool
		)
		switch {
		case top == bare && bottom == bare:
			glyph = ' '
		case top != bare && bottom != bare && ink[top] == ink[bottom]:
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

// renderFrame offsets the mint copy by gap columns and drop sub-rows.
func renderFrame(gap, drop, tear int) string {
	width := logoWidth()
	ink := [3]lipgloss.Color{face: twinTheme.source, shadow: twinTheme.target}
	if lipgloss.ColorProfile() == termenv.Ascii {
		// A two-tone half block would lose its background in plain output.
		ink[shadow] = ink[face]
	}

	lines := make([]string, 0, logoHeight())
	for row := range markHeight() {
		lines = append(lines, markRow(row, width, rowGap(row, gap, tear), drop, ink))
	}
	lines = append(lines, captionRow(width))
	return strings.Join(lines, "\n")
}

// restFrame is the landing lockup that stays in scrollback.
func restFrame() string { return renderFrame(restShift, restDrop, 0) }

// The mint copy opens to the right, tears through uneven bands as it closes,
// then settles behind the face. The final beat has no hold.
var goodbyeScript = []struct {
	gap  int
	drop int
	tear int
}{
	{ghostShift, 0, 0}, {ghostShift, 0, 0},
	{ghostShift, 0, 2}, {4, 0, 2}, {4, 0, 1},
	{3, 0, 2}, {3, 0, 1}, {2, 0, 2},
	{2, restDrop, 1}, {restShift, restDrop, 1},
	{restShift, restDrop, 0},
}

// Browsers slow down GIFs with frame delays below 20ms.
const frameDelay = 20 * time.Millisecond

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
	fmt.Fprintln(w)
	for i, f := range goodbyeScript {
		if i > 0 {
			time.Sleep(frameDelay)
			fmt.Fprintf(w, "\x1b[%dA\r\x1b[0J", logoHeight())
		}
		fmt.Fprintln(w, renderFrame(f.gap, f.drop, f.tear))
	}
	fmt.Fprintln(w)
}
