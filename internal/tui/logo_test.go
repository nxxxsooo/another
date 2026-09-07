package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/muesli/termenv"
)

// The generated bitmap must be an exact rectangle, or the wordmark shears and
// the caption's right-aligned version drifts off the block.
func TestGeneratedFaceIsRectangular(t *testing.T) {
	if len(faceBits) == 0 {
		t.Fatal("logo_face.go is empty; regenerate with scripts/render-logo-face.py")
	}
	for i, row := range faceBits {
		if got := len([]rune(row)); got != markWidth {
			t.Errorf("face row %d width = %d, want markWidth %d", i, got, markWidth)
		}
	}
}

// The face is stored unpacked, one character per sub-pixel, because the two
// offset copies have to be combined before sub-rows are folded into cells. A
// stray half block would mean the generator regressed to emitting packed rows.
func TestFaceIsAnUnpackedBitmap(t *testing.T) {
	if len(faceBits) != faceSubRows {
		t.Errorf("face has %d rows, want faceSubRows %d", len(faceBits), faceSubRows)
	}
	if faceSubRows%2 != 0 {
		t.Fatalf("faceSubRows = %d; sub-rows fold into cells in pairs", faceSubRows)
	}
	for _, r := range strings.Join(faceBits, "") {
		if r != '#' && r != ' ' {
			t.Fatalf("face contains %q; the bitmap holds only ink and bare", r)
		}
	}
}

// The brand rule is that the name is always lowercase.
func TestWordmarkIsLowercase(t *testing.T) {
	if logoWord != strings.ToLower(logoWord) {
		t.Errorf("logoWord = %q, must be lowercase", logoWord)
	}
}

// The whole point of the half-block grid is a short mark. Guard the height so a
// future regeneration at a taller size fails loudly.
func TestMarkStaysShort(t *testing.T) {
	if markHeight() > 5 {
		t.Errorf("mark is %d rows; the half-block face should need at most 5", markHeight())
	}
}

// At rest the mark has to show all three readings at once: ink only the source
// agent holds, ink only the target holds, and the overlap they share. If any
// one of them is missing the image is not saying that a session is in two
// places, which is the entire point of the goodbye.
func TestRestingMarkShowsBothAgentsAndTheirOverlap(t *testing.T) {
	seen := map[inkState]int{}
	for sub := range faceSubRows {
		for col := range logoWidth() {
			seen[stateAt(sub, col, ghostRest)]++
		}
	}
	for _, s := range []struct {
		state inkState
		name  string
	}{
		{sourceOnly, "source-only"},
		{targetOnly, "target-only"},
		{shared, "shared"},
	} {
		if seen[s.state] == 0 {
			t.Errorf("resting mark has no %s sub-pixels", s.name)
		}
	}
}

// Every sub-pixel of both copies must survive the overlay. The source copy sits
// at the origin and the target copy hangs to its right, so the reserved width
// is what keeps the target from being clipped mid-stroke.
func TestGhostIsNeverClipped(t *testing.T) {
	var source, target int
	for sub := range faceSubRows {
		for col := range markWidth {
			if faceInk(sub, col) {
				source++
			}
		}
	}
	for _, beat := range goodbyeScript {
		if beat.dx > ghostShift {
			t.Fatalf("script shifts %d cells, wider than the %d reserved", beat.dx, ghostShift)
		}
		target = 0
		for sub := range faceSubRows {
			for col := range logoWidth() {
				switch stateAt(sub, col, beat.dx) {
				case targetOnly, shared:
					target++
				}
			}
		}
		if target != source {
			t.Errorf("dx=%d lost ink: target copy shows %d sub-pixels, source has %d",
				beat.dx, target, source)
		}
	}
}

// The animation redraws in place with a cursor-up jump of exactly logoHeight()
// lines. If any frame differs in height the jump desyncs and the block smears;
// if any line differs in width the wipe leaves debris from the previous frame.
func TestEveryFrameHasIdenticalDimensions(t *testing.T) {
	SetVersion("1.27.1")
	t.Cleanup(func() { SetVersion("") })

	for _, f := range goodbyeScript {
		frame := renderFrame(f.dx, f.tension)
		lines := strings.Split(frame, "\n")
		if len(lines) != logoHeight() {
			t.Fatalf("frame dx=%d has %d lines, want %d", f.dx, len(lines), logoHeight())
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got != logoWidth() {
				t.Errorf("frame dx=%d line %d width = %d, want %d", f.dx, i, got, logoWidth())
			}
		}
	}
}

// The story is: whole in one agent, separating once, settled in two. It has to
// open unified and close split, because the goodbye prints after the migration
// already happened and the split frame is what stays in scrollback.
func TestScriptOpensWholeAndClosesSplit(t *testing.T) {
	first, last := goodbyeScript[0], goodbyeScript[len(goodbyeScript)-1]
	if first.dx != 0 || first.tension != 0 {
		t.Errorf("script opens already split: %+v", first)
	}
	if last.dx != ghostRest || last.tension != 1 {
		t.Errorf("script closes at %+v, want the settled split {dx:%d tension:1}", last, ghostRest)
	}
	var separations int
	for i := 1; i < len(goodbyeScript); i++ {
		if goodbyeScript[i].dx > 0 && goodbyeScript[i-1].dx == 0 {
			separations++
		}
	}
	if separations != 1 {
		t.Errorf("script separates %d times, want exactly one", separations)
	}
}

// The last beat is what renderFarewell prints for everyone who never sees the
// animation, so the two must agree exactly.
func TestRestFrameIsTheScriptsFinalBeat(t *testing.T) {
	last := goodbyeScript[len(goodbyeScript)-1]
	if restFrame() != renderFrame(last.dx, last.tension) {
		t.Error("restFrame() and the script's final beat render differently")
	}
}

// Tension walks between the two stable brand states. At zero the mark is whole
// and entirely the source agent's colour however far the copies have moved; at
// one the target and the overlap have arrived at their own colours.
func TestTensionResolvesToOneColourWhenWhole(t *testing.T) {
	if got := mix(twinTheme.source, twinTheme.target, 0); got != twinTheme.source {
		t.Errorf("target at tension 0 = %s, want the source colour %s", got, twinTheme.source)
	}
	if got := mix(twinTheme.source, twinTheme.text, 0); got != twinTheme.source {
		t.Errorf("overlap at tension 0 = %s, want the source colour %s", got, twinTheme.source)
	}
	if got := mix(twinTheme.source, twinTheme.target, 1); got != twinTheme.target {
		t.Errorf("target at tension 1 = %s, want %s", got, twinTheme.target)
	}
	if got := mix(twinTheme.source, twinTheme.text, 1); got != twinTheme.text {
		t.Errorf("overlap at tension 1 = %s, want %s", got, twinTheme.text)
	}
}

// The two agent colours are the Charmtone pair the rest of the TUI uses, and
// the midpoint is the real interpolation between them rather than a third hue
// picked by eye.
func TestColourMixEndpointsAndMidpoint(t *testing.T) {
	if got := string(twinTheme.source); got != charmtone.Charple.Hex() {
		t.Errorf("source = %s, want Charple %s", got, charmtone.Charple.Hex())
	}
	if got := string(twinTheme.target); got != charmtone.Julep.Hex() {
		t.Errorf("target = %s, want Julep %s", got, charmtone.Julep.Hex())
	}
	if got := string(mix(twinTheme.source, twinTheme.target, 0.5)); got != "#35A8D8" {
		t.Errorf("tension 0.5 = %s, want the violet-mint midpoint #35A8D8", got)
	}
}

// previewInk is a palette whose four entries are distinguishable, so a test can
// tell which agent a rendered cell belongs to.
var previewInk = [4]lipgloss.Color{
	bare:       lipgloss.Color(""),
	sourceOnly: twinTheme.source,
	targetOnly: twinTheme.target,
	shared:     twinTheme.text,
}

// A terminal cell holds two sub-pixels, so the glyph has to say which halves
// carry ink and the colours have to say whose ink it is. This walks every cell
// of every beat and checks the packing against the states it came from.
func TestEveryCellPacksItsTwoSubPixels(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, beat := range goodbyeScript {
		for row := range markHeight() {
			cells := []rune(ansi.Strip(markRow(row, logoWidth(), beat.dx, previewInk)))
			if len(cells) != logoWidth() {
				t.Fatalf("dx=%d row %d renders %d cells, want %d",
					beat.dx, row, len(cells), logoWidth())
			}
			for col, got := range cells {
				top := stateAt(2*row, col, beat.dx)
				bottom := stateAt(2*row+1, col, beat.dx)

				var want rune
				switch {
				case top == bare && bottom == bare:
					want = ' '
				case top != bare && bottom != bare:
					// Both halves inked. A full block can only carry one
					// colour, so halves that disagree must stay a half block
					// with the second colour behind it.
					if top == bottom {
						want = '█'
					} else {
						want = '▀'
					}
				case bottom == bare:
					want = '▀'
				default:
					want = '▄'
				}
				if got != want {
					t.Fatalf("dx=%d cell (%d,%d) is %q, want %q for states %v/%v",
						beat.dx, row, col, got, want, top, bottom)
				}
			}
		}
	}
}

// countBackgroundRuns walks the SGR parameter lists in a rendered line and
// counts the styles that set a background. Searching for a "48;" substring is
// not good enough: lipgloss merges foreground and background into one sequence,
// and a foreground whose red channel is 148 would contain "48;2;" by accident.
func countBackgroundRuns(line string) int {
	var n int
	for i := 0; i < len(line); {
		if !strings.HasPrefix(line[i:], "\x1b[") {
			i++
			continue
		}
		end := strings.Index(line[i:], "m")
		if end < 0 {
			break
		}
		params := strings.Split(line[i+2:i+end], ";")
		for p := 0; p < len(params); {
			kind := params[p]
			if kind != "38" && kind != "48" {
				p++
				continue
			}
			// 38;2;r;g;b and 38;5;n, and the same pair for 48.
			width := 1
			if p+1 < len(params) {
				switch params[p+1] {
				case "2":
					width = 5
				case "5":
					width = 3
				}
			}
			if kind == "48" {
				n++
			}
			p += width
		}
		i += end + 1
	}
	return n
}

// A background is only legitimate on a cell whose two halves carry different
// agents' ink. Anywhere else it would be a fill, which is what tears in
// Ghostty, and it would also paint colour into bare cells that should stay the
// terminal's own background.
func TestBackgroundsOnlyAppearWhereTwoAgentsShareACell(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, beat := range goodbyeScript {
		for row := range markHeight() {
			var wantBG int
			for col := range logoWidth() {
				top := stateAt(2*row, col, beat.dx)
				bottom := stateAt(2*row+1, col, beat.dx)
				if top != bare && bottom != bare && top != bottom {
					wantBG++
				}
			}
			line := markRow(row, logoWidth(), beat.dx, previewInk)
			gotBG := countBackgroundRuns(line)
			if wantBG == 0 && gotBG != 0 {
				t.Errorf("dx=%d row %d paints %d backgrounds but no cell needs one",
					beat.dx, row, gotBG)
			}
			if wantBG > 0 && gotBG == 0 {
				t.Errorf("dx=%d row %d needs a background on %d cells but paints none",
					beat.dx, row, wantBG)
			}
		}
	}
}

// Every styled run is a leaf: one style opened over plain runes and closed
// again. The Ghostty artifact recorded in theme.go comes from nesting styled
// content inside a filled container, where the inner reset clears the outer
// fill, so the mark must never open a second style before closing the first.
func TestStyledRunsAreLeavesAndNeverNest(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for row := range markHeight() {
		line := markRow(row, logoWidth(), ghostRest, previewInk)
		var depth int
		for i := 0; i < len(line); {
			switch {
			case strings.HasPrefix(line[i:], "\x1b[0m"):
				if depth == 0 {
					t.Fatalf("row %d closes a style that was never opened: %q", row, line)
				}
				depth--
				i += len("\x1b[0m")
			case strings.HasPrefix(line[i:], "\x1b["):
				if depth != 0 {
					t.Fatalf("row %d opens a style inside another: %q", row, line)
				}
				depth++
				i += strings.Index(line[i:], "m") + 1
			default:
				i++
			}
		}
		if depth != 0 {
			t.Errorf("row %d leaves a style open: %q", row, line)
		}
	}
}

func TestCaptionCarriesTaglineAndVersion(t *testing.T) {
	SetVersion("1.27.1")
	t.Cleanup(func() { SetVersion("") })

	caption := ansi.Strip(captionRow(logoWidth()))
	if !strings.HasPrefix(caption, logoTagline) {
		t.Errorf("caption %q does not start with the tagline", caption)
	}
	if !strings.HasSuffix(caption, "v1.27.1") {
		t.Errorf("caption %q does not end with the version", caption)
	}
	if strings.Contains(caption, "™") {
		t.Errorf("caption %q must not claim a trademark", caption)
	}
}

// An unstamped dev build has no version; the caption must still fill its row.
func TestCaptionWithoutVersion(t *testing.T) {
	SetVersion("")
	if got := ansi.StringWidth(captionRow(logoWidth())); got != logoWidth() {
		t.Errorf("caption width = %d, want %d", got, logoWidth())
	}
}

func TestLogoFitsEightyColumns(t *testing.T) {
	if logoWidth() > 80 {
		t.Errorf("logo width %d exceeds an 80-column terminal", logoWidth())
	}
}

// ink counts every painted cell, half-blocks included.
func ink(s string) int {
	var n int
	for _, r := range s {
		if strings.ContainsRune("\u2588\u2580\u2584", r) {
			n++
		}
	}
	return n
}

// Narrow terminals fall back to the compact frame instead of wrapping a glyph
// row across two lines.
func TestFarewellFallsBackWhenNarrow(t *testing.T) {
	wide := ansi.Strip(renderFarewell(120))
	if ink(wide) == 0 {
		t.Error("wide terminal should get the block wordmark")
	}
	narrow := ansi.Strip(renderFarewell(40))
	if ink(narrow) != 0 {
		t.Error("narrow terminal should fall back to the compact banner")
	}
	if !strings.Contains(narrow, "another") {
		t.Error("compact fallback should still show the name")
	}
}

// Motion is opt-out, but it must never fire into a pipe, a log, or a terminal
// too short to redraw in.
func TestMotionIsSuppressed(t *testing.T) {
	for _, tc := range []struct{ name, env string }{
		{"opt out", "ANOTHER_NO_MOTION"},
		{"no colour", "NO_COLOR"},
		{"ci", "CI"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, "1")
			if motionAllowed(120, 40) {
				t.Errorf("%s should suppress motion", tc.env)
			}
		})
	}
	t.Run("short terminal", func(t *testing.T) {
		if motionAllowed(120, logoHeight()) {
			t.Error("a terminal with no room to redraw should suppress motion")
		}
	})
}

// With motion off, the goodbye is a single still frame and emits no cursor
// movement — otherwise redirecting output would litter the file with escapes.
func TestStillGoodbyeEmitsNoCursorMotion(t *testing.T) {
	var buf bytes.Buffer
	playFarewell(&buf, 120, 40, false)
	out := buf.String()
	if strings.Contains(out, "\x1b[") && strings.Contains(out, "A\r") {
		t.Error("still goodbye must not emit cursor-up sequences")
	}
	if ink(ansi.Strip(out)) == 0 {
		t.Error("still goodbye should draw the mark")
	}
}
