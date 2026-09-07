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
// The split is the goodbye's opening state, not its resting one. The animation
// closes the gap until the two copies are in register and the mark is only the
// white they shared all along; see goodbyeScript for why it runs that way. The
// channel-split reading therefore lives in the motion and in the README hero,
// while what stays in a terminal's scrollback is the merged word.
//
// The overlap is twinTheme.intersection, the entry the palette names for
// exactly this job. It sits close to Julep, so in the split frames the shared
// cells read as a lighter bloom inside the target's mint rather than as a third
// hue standing apart from it. That is the intended reading: what both agents
// hold is not a separate thing from where the session landed. The frame the
// scrollback keeps is therefore the mark in intersection cyan, not white.
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

	// ghostShift is the gap the two copies open at, and so the extra width
	// every frame reserves. Four cells is the narrowest gap that reads as two
	// copies: at two the stems of one land in the counters of the other and
	// the word thickens into a single two-toned blob, which says "bold"
	// rather than "twice".
	//
	// It is even because the copies close symmetrically. Each step halves the
	// gap into whole cells on both sides, so an odd gap would move one copy
	// and leave the other standing, and the pair would drift across the block
	// instead of meeting in it.
	//
	// ghostClose is the beat between: the two marks overlapping but not yet
	// one, which is where their colours start giving way.
	ghostShift = 4
	ghostClose = 2
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

// mix walks from one theme colour to another. Both agents walk toward the same
// destination, so their colours share a path rather than sitting as two
// unrelated hues that happen to appear together.
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

// pairOffsets places the two copies gap cells apart and centred in the width
// the block reserves. Centring is what makes the close symmetric: the source
// walks in from the left and the target from the right by the same number of
// cells, and they meet in the middle of the block rather than the target
// sliding across a stationary source.
func pairOffsets(gap int) (source, target int) {
	left := (logoWidth() - markWidth - gap) / 2
	return left, left + gap
}

// tearBands is how far each row of the mark leads or lags the gap the frame
// nominally has. It is a fixed pattern rather than noise because every frame
// has to be reproducible: the README GIF is rendered from this same table by
// scripts/render-goodbye-gif.py, and a goodbye that came out differently each
// time could not be checked against anything.
//
// The pattern is deliberately uneven. Strict alternation reads as a texture —
// a striped mark — while bands of unequal throw read as one word being pulled
// apart, which is the point.
var tearBands = [...]int{0, +1, -1, +2, -1}

// rowGap is the gap one row is drawn at. tear scales the bands, so tear zero
// is a clean frame and larger values rip the word further open.
//
// The result is clamped, which is load-bearing rather than defensive: outside
// this range a copy hangs past the reserved width and is clipped mid-stroke.
// Clamping also gives the tear its bite at the extremes — at a small gap the
// leading bands are still split while the rest have already merged.
func rowGap(cellRow, gap, tear int) int {
	if tear == 0 {
		return gap
	}
	return min(max(gap+tear*tearBands[cellRow%len(tearBands)], 0), ghostShift)
}

// stateAt overlays the two copies gap cells apart and reports what the
// sub-pixel at (subRow, col) now belongs to.
func stateAt(subRow, col, gap int) inkState {
	srcAt, dstAt := pairOffsets(gap)
	src := faceInk(subRow, col-srcAt)
	dst := faceInk(subRow, col-dstAt)
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
func markRow(cellRow, width, gap int, ink [4]lipgloss.Color) string {
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
		top := stateAt(2*cellRow, col, gap)
		bottom := stateAt(2*cellRow+1, col, gap)

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

// renderFrame draws one moment of the goodbye. gap is how far apart the two
// copies stand, in whole cells, because a terminal cannot slide half a column.
// merge is how far the two agents have given way to the session they both
// hold: at zero each copy is its own agent's colour, at one both have arrived
// at the intersection cyan the overlap has been all along. tear pulls the rows
// off that single gap so the word can come apart across scanlines on the way in.
//
// The overlap does not move. It is the part of the session both agents hold, so
// it carries the intersection colour from the first frame, and merging is the
// rest of the mark catching up to it rather than a third colour arriving from
// somewhere.
func renderFrame(gap int, merge float64, tear int) string {
	width := logoWidth()
	ink := [4]lipgloss.Color{
		bare:       lipgloss.Color(""),
		sourceOnly: mix(twinTheme.source, twinTheme.intersection, merge),
		targetOnly: mix(twinTheme.target, twinTheme.intersection, merge),
		shared:     twinTheme.intersection,
	}

	lines := make([]string, 0, logoHeight())
	for row := range markHeight() {
		lines = append(lines, markRow(row, width, rowGap(row, gap, tear), ink))
	}
	lines = append(lines, captionRow(width))
	return strings.Join(lines, "\n")
}

// restFrame is the settled mark: one word, in the colour of the session
// itself. It is the frame the terminal keeps in its scrollback, so it is the
// one that has to carry the whole idea on its own.
func restFrame() string { return renderFrame(0, 1, 0) }

// goodbyeScript is the whole story, and it is told in that order for a reason.
//
// It opens with the session standing in two places: two copies of the mark a
// clear gap apart, one in the source agent's violet and one in the target's
// mint. That is the state a migration leaves behind, and it is where the
// goodbye starts because it is what just happened.
//
// Then they close on each other, each walking the same distance toward the
// middle, and as they overlap their colours give way to the intersection they
// share.
// It does not close cleanly: the rows tear off the shared gap on the way in,
// so for a few frames the word is split in some bands and already merged in
// others. A migration is not a smooth dissolve, and the mark should not claim
// it was one.
//
// The last beat drops the tear and brings both copies into register, where
// every inked sub-pixel belongs to both and the mark is simply the session,
// whole, under a line that says Keep the session.
//
// It ends unified on purpose. Both agents held this conversation, and neither
// is what survives the handoff; a mark that stayed split would keep insisting
// on the seam after the point of the tool is that there isn't one.
//
// There is no hold on the final beat. The frame stays on screen once the
// animation stops, so repeating it would only be dead time before the prompt
// comes back — the whole goodbye has to be over before it is in the way.
var goodbyeScript = []struct {
	gap   int
	merge float64
	tear  int
}{
	{ghostShift, 0, 0}, {ghostShift, 0, 0},
	{ghostShift, 0, 2}, {ghostShift, 0, 1},
	{ghostClose, 0, 2}, {ghostClose, 0.2, 1},
	{ghostClose, 0.45, 2}, {ghostClose, 0.7, 1},
	{0, 0.85, 2}, {0, 1, 1},
	{0, 1, 0},
}

// frameDelay is a 50fps beat. The goodbye runs while the user is already on
// their way somewhere else, so it buys its expressiveness with more frames
// rather than with more of their time.
//
// It does not go below 20ms, and the floor comes from the README rather than
// from the terminal. GIF stores a frame delay in centiseconds, so anything
// under 20ms rounds to one, and browsers have clamped a one-centisecond delay
// to a tenth of a second since the days of animated under-construction
// banners. A 17ms goodbye would play correctly here and ten times too slow in
// the README; scripts/render-goodbye-gif.py refuses to render below this.
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
	fmt.Fprint(w, "\n"+renderFrame(goodbyeScript[0].gap, goodbyeScript[0].merge, goodbyeScript[0].tear)+"\n")
	for _, f := range goodbyeScript[1:] {
		time.Sleep(frameDelay)
		// Up over the block, wipe what follows, repaint.
		fmt.Fprintf(w, "\x1b[%dA\r\x1b[0J", logoHeight())
		fmt.Fprint(w, renderFrame(f.gap, f.merge, f.tear)+"\n")
	}
	fmt.Fprintln(w)
}
