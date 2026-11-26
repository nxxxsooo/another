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
	if markWidth != 82 || len(faceBits) != 14 {
		t.Fatalf("face is %d × %d sub-pixels, want 82 × 14", markWidth, len(faceBits))
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

func TestLogoReservesOnlyRoomForTheOffset(t *testing.T) {
	if logoWidth() != 87 || logoHeight() != 9 {
		t.Errorf("logo block is %d × %d cells, want 87 × 9", logoWidth(), logoHeight())
	}
}

// Every shifted shadow sub-pixel must fit, even in a torn band.
func TestGhostIsNeverClipped(t *testing.T) {
	for i, beat := range goodbyeScript {
		for sub, bits := range faceBits {
			y := sub + beat.drop
			gap := rowGap(y/2, beat.gap, beat.tear)
			for col, bit := range bits {
				x := col + gap
				if bit == '#' && (y < 0 || y >= 2*markHeight() || x < 0 || x >= logoWidth()) {
					t.Fatalf("beat %d clips shadow sub-pixel (%d,%d)", i, x, y)
				}
			}
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
		frame := renderFrame(f.gap, f.drop, f.tear)
		lines := strings.Split(frame, "\n")
		if len(lines) != logoHeight() {
			t.Fatalf("frame gap=%d has %d lines, want %d", f.gap, len(lines), logoHeight())
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got != logoWidth() {
				t.Errorf("frame gap=%d line %d width = %d, want %d", f.gap, i, got, logoWidth())
			}
		}
	}
}

func TestScriptOpensSplitAndSettlesAsOffsetLockup(t *testing.T) {
	first, last := goodbyeScript[0], goodbyeScript[len(goodbyeScript)-1]
	if first.gap != 5 || first.drop != 0 || first.tear != 0 {
		t.Errorf("script opens at %+v, want a clean five-column split", first)
	}
	if last.gap != 1 || last.drop != 1 || last.tear != 0 {
		t.Errorf("script closes at %+v, want a clean +1/+1 offset", last)
	}
	if last == goodbyeScript[len(goodbyeScript)-2] {
		t.Error("final beat must not hold")
	}
}

func TestTearOffsetsBandsDifferently(t *testing.T) {
	for _, beat := range goodbyeScript {
		if beat.tear == 0 {
			continue
		}
		for row := 1; row < markHeight(); row++ {
			if rowGap(row, beat.gap, beat.tear) != rowGap(0, beat.gap, beat.tear) {
				return
			}
		}
	}
	t.Fatal("no beat offsets rows by different amounts")
}

func TestScriptOnlyEverClosesTheGap(t *testing.T) {
	for i := 1; i < len(goodbyeScript); i++ {
		if goodbyeScript[i].gap > goodbyeScript[i-1].gap {
			t.Fatalf("beat %d reopens the gap: %+v after %+v",
				i, goodbyeScript[i], goodbyeScript[i-1])
		}
	}
}

// The last beat is what renderFarewell prints for everyone who never sees the
// animation, so the two must agree exactly.
func TestRestFrameIsTheScriptsFinalBeat(t *testing.T) {
	last := goodbyeScript[len(goodbyeScript)-1]
	if restFrame() != renderFrame(last.gap, last.drop, last.tear) {
		t.Error("restFrame() and the script's final beat render differently")
	}
}

func TestFaceCoversShadowAtRest(t *testing.T) {
	for sub := range 2 * markHeight() {
		for col := range logoWidth() {
			want := bare
			if faceInk(sub-1, col-1) {
				want = shadow
			}
			if faceInk(sub, col) {
				want = face
			}
			if got := stateAt(sub, col, restShift, restDrop); got != want {
				t.Fatalf("rest pixel (%d,%d) is %v, want %v", col, sub, got, want)
			}
		}
	}
}

func TestOpeningAndRestFramesUseVioletAndMint(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	first := goodbyeScript[0]
	for _, frame := range []string{renderFrame(first.gap, first.drop, first.tear), restFrame()} {
		for _, color := range []lipgloss.Color{twinTheme.source, twinTheme.target} {
			if !strings.Contains(frame, colorOf(t, color)) {
				t.Errorf("mark does not show %s", color)
			}
		}
		if strings.Contains(frame, colorOf(t, twinTheme.intersection)) {
			t.Error("mark should not show intersection cyan")
		}
	}
}

func TestWordmarkUsesBrandColours(t *testing.T) {
	if got := string(twinTheme.source); got != charmtone.Charple.Hex() {
		t.Errorf("source = %s, want Charple %s", got, charmtone.Charple.Hex())
	}
	if got := string(twinTheme.target); got != charmtone.Julep.Hex() {
		t.Errorf("target = %s, want Julep %s", got, charmtone.Julep.Hex())
	}
}

var previewInk = [3]lipgloss.Color{face: twinTheme.source, shadow: twinTheme.target}

// A terminal cell holds two sub-pixels, so the glyph has to say which halves
// carry ink and the colours have to say whose ink it is. This walks every cell
// of every beat and checks the packing against the states it came from.
func TestEveryCellPacksItsTwoSubPixels(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, beat := range goodbyeScript {
		for row := range markHeight() {
			gap := rowGap(row, beat.gap, beat.tear)
			cells := []rune(ansi.Strip(markRow(row, logoWidth(), gap, beat.drop, previewInk)))
			if len(cells) != logoWidth() {
				t.Fatalf("gap=%d row %d renders %d cells, want %d",
					beat.gap, row, len(cells), logoWidth())
			}
			for col, got := range cells {
				top := stateAt(2*row, col, gap, beat.drop)
				bottom := stateAt(2*row+1, col, gap, beat.drop)

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
					t.Fatalf("gap=%d cell (%d,%d) is %q, want %q for states %v/%v",
						beat.gap, row, col, got, want, top, bottom)
				}
			}
		}
	}
}

func TestPlainRestFrameKeepsBothHalvesOfTwoToneCells(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	lines := strings.Split(restFrame(), "\n")
	var found bool
	for row := range markHeight() {
		for col, glyph := range []rune(lines[row]) {
			top := stateAt(2*row, col, restShift, restDrop)
			bottom := stateAt(2*row+1, col, restShift, restDrop)
			if top != bare && bottom != bare && top != bottom {
				found = true
				if glyph != '█' {
					t.Errorf("plain two-tone cell (%d,%d) = %q, want full block", col, row, glyph)
				}
			}
		}
	}
	if !found {
		t.Fatal("no two-tone cells to check")
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
			gap := rowGap(row, beat.gap, beat.tear)
			var wantBG int
			for col := range logoWidth() {
				top := stateAt(2*row, col, gap, beat.drop)
				bottom := stateAt(2*row+1, col, gap, beat.drop)
				if top != bare && bottom != bare && top != bottom {
					wantBG++
				}
			}
			line := markRow(row, logoWidth(), gap, beat.drop, previewInk)
			gotBG := countBackgroundRuns(line)
			if wantBG == 0 && gotBG != 0 {
				t.Errorf("gap=%d row %d paints %d backgrounds but no cell needs one",
					beat.gap, row, gotBG)
			}
			if wantBG > 0 && gotBG == 0 {
				t.Errorf("gap=%d row %d needs a background on %d cells but paints none",
					beat.gap, row, wantBG)
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
		line := markRow(row, logoWidth(), restShift, restDrop, previewInk)
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
	wide := ansi.Strip(renderFarewell(logoWidth()))
	if ink(wide) == 0 {
		t.Error("wide terminal should get the block wordmark")
	}
	for _, width := range []int{logoWidth() - 1, 80, 40} {
		if got, want := renderFarewell(width), "\n"+renderBanner()+"\n"; got != want {
			t.Errorf("width %d should fall back to the compact banner", width)
		}
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
	if want := "\n" + restFrame() + "\n\n"; out != want {
		t.Error("still goodbye must print exactly the rest frame with no cursor movement")
	}
}
