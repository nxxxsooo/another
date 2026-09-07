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

// The opening frame has to show all three readings at once: ink only the
// source agent holds, ink only the target holds, and the overlap they share.
// If any one of them is missing, the goodbye never states the thing it then
// spends the whole animation resolving — that the session stood in two places.
func TestOpeningMarkShowsBothAgentsAndTheirOverlap(t *testing.T) {
	seen := map[inkState]int{}
	for sub := range faceSubRows {
		for col := range logoWidth() {
			seen[stateAt(sub, col, goodbyeScript[0].gap)]++
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
			t.Errorf("opening mark has no %s sub-pixels", s.name)
		}
	}
}

// Every sub-pixel of both copies must survive the overlay. The pair is centred
// in the reserved width, and the tear pulls individual rows off the frame's
// gap, so it is the gap each row is actually drawn at that has to stay inside
// the reserve — otherwise a torn band is clipped mid-stroke.
func TestGhostIsNeverClipped(t *testing.T) {
	// A row of the mark is two sub-rows of the bitmap, and the tear moves
	// whole rows, so both halves of a cell are always at the same gap.
	sourceInRow := make([]int, markHeight())
	for sub := range faceSubRows {
		for col := range markWidth {
			if faceInk(sub, col) {
				sourceInRow[sub/2]++
			}
		}
	}
	for i, beat := range goodbyeScript {
		for row := range markHeight() {
			gap := rowGap(row, beat.gap, beat.tear)
			if gap < 0 || gap > ghostShift {
				t.Fatalf("beat %d row %d is drawn at gap %d, outside the %d reserved",
					i, row, gap, ghostShift)
			}
			var target int
			for _, sub := range []int{2 * row, 2*row + 1} {
				for col := range logoWidth() {
					switch stateAt(sub, col, gap) {
					case targetOnly, shared:
						target++
					}
				}
			}
			if target != sourceInRow[row] {
				t.Errorf("beat %d row %d at gap %d lost ink: target shows %d sub-pixels, source has %d",
					i, row, gap, target, sourceInRow[row])
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
		frame := renderFrame(f.gap, f.merge, f.tear)
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

// The story is: the session standing in two places, drawing together, ending
// as one word. It has to open split and close merged, because the goodbye
// prints after the migration already happened and the merged frame is what
// stays in scrollback.
func TestScriptOpensSplitAndClosesMerged(t *testing.T) {
	first, last := goodbyeScript[0], goodbyeScript[len(goodbyeScript)-1]
	if first.gap != ghostShift || first.merge != 0 || first.tear != 0 {
		t.Errorf("script opens at %+v, want the two agents fully apart and whole {gap:%d merge:0 tear:0}", first, ghostShift)
	}
	if last.gap != 0 || last.merge != 1 || last.tear != 0 {
		t.Errorf("script closes at %+v, want the merged mark {gap:0 merge:1 tear:0}", last)
	}
}

// The tear is something the goodbye passes through, not something it leaves
// behind. It has to actually happen — a table of zeroes would quietly turn the
// close back into a clean dissolve — and it has to be gone by the end, because
// a torn frame is exactly what must not survive in scrollback.
func TestTearHappensAndIsGoneByTheEnd(t *testing.T) {
	var torn int
	for _, beat := range goodbyeScript {
		if beat.tear != 0 {
			torn++
		}
	}
	if torn == 0 {
		t.Fatal("no beat tears; the close is a clean dissolve")
	}

	// A tear every row answers the same way is a shift, not a tear.
	seen := map[int]bool{}
	for row := range markHeight() {
		seen[rowGap(row, ghostClose, 2)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("every torn row lands on the same gap %v", seen)
	}
}

// The gap only ever closes. A copy that drifted back apart would turn one
// merge into a wobble, and the mark would read as undecided rather than as a
// session arriving somewhere.
func TestScriptOnlyEverClosesTheGap(t *testing.T) {
	for i := 1; i < len(goodbyeScript); i++ {
		if goodbyeScript[i].gap > goodbyeScript[i-1].gap {
			t.Fatalf("beat %d reopens the gap: %+v after %+v",
				i, goodbyeScript[i], goodbyeScript[i-1])
		}
		if goodbyeScript[i].merge < goodbyeScript[i-1].merge {
			t.Fatalf("beat %d walks the agents back: %+v after %+v",
				i, goodbyeScript[i], goodbyeScript[i-1])
		}
	}
	var merges int
	for i := 1; i < len(goodbyeScript); i++ {
		if goodbyeScript[i].gap == 0 && goodbyeScript[i-1].gap > 0 {
			merges++
		}
	}
	if merges != 1 {
		t.Errorf("script merges %d times, want exactly one", merges)
	}
}

// The last beat is what renderFarewell prints for everyone who never sees the
// animation, so the two must agree exactly.
func TestRestFrameIsTheScriptsFinalBeat(t *testing.T) {
	last := goodbyeScript[len(goodbyeScript)-1]
	if restFrame() != renderFrame(last.gap, last.merge, last.tear) {
		t.Error("restFrame() and the script's final beat render differently")
	}
}

// Merge walks each agent to the colour of the session. At zero both copies are
// their own agent's; at one both have arrived at the white the overlap has
// carried since the first frame.
func TestMergeWalksBothAgentsToTheSession(t *testing.T) {
	if got := mix(twinTheme.source, twinTheme.text, 0); got != twinTheme.source {
		t.Errorf("source at merge 0 = %s, want %s", got, twinTheme.source)
	}
	if got := mix(twinTheme.target, twinTheme.text, 0); got != twinTheme.target {
		t.Errorf("target at merge 0 = %s, want %s", got, twinTheme.target)
	}
	if got := mix(twinTheme.source, twinTheme.text, 1); got != twinTheme.text {
		t.Errorf("source at merge 1 = %s, want %s", got, twinTheme.text)
	}
	if got := mix(twinTheme.target, twinTheme.text, 1); got != twinTheme.text {
		t.Errorf("target at merge 1 = %s, want %s", got, twinTheme.text)
	}
}

// In register there is no seam left to colour: every inked sub-pixel belongs to
// both copies, so the closing frame is the session's own white and nothing
// else. This is the frame that stays in scrollback.
func TestMergedFrameIsOneColour(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for sub := range faceSubRows {
		for col := range logoWidth() {
			if s := stateAt(sub, col, 0); s != bare && s != shared {
				t.Fatalf("sub-pixel (%d,%d) is %v in register, want bare or shared", sub, col, s)
			}
		}
	}
	frame := restFrame()
	for _, agent := range []lipgloss.Color{twinTheme.source, twinTheme.target} {
		if strings.Contains(frame, colorOf(t, agent)) {
			t.Errorf("the merged mark still carries %s", agent)
		}
	}
	if !strings.Contains(frame, colorOf(t, twinTheme.text)) {
		t.Error("the merged mark is not drawn in the session's colour")
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
			cells := []rune(ansi.Strip(markRow(row, logoWidth(), rowGap(row, beat.gap, beat.tear), previewInk)))
			if len(cells) != logoWidth() {
				t.Fatalf("gap=%d row %d renders %d cells, want %d",
					beat.gap, row, len(cells), logoWidth())
			}
			for col, got := range cells {
				top := stateAt(2*row, col, rowGap(row, beat.gap, beat.tear))
				bottom := stateAt(2*row+1, col, rowGap(row, beat.gap, beat.tear))

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
				top := stateAt(2*row, col, rowGap(row, beat.gap, beat.tear))
				bottom := stateAt(2*row+1, col, rowGap(row, beat.gap, beat.tear))
				if top != bare && bottom != bare && top != bottom {
					wantBG++
				}
			}
			line := markRow(row, logoWidth(), rowGap(row, beat.gap, beat.tear), previewInk)
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
		// ghostClose is the worst case: overlapping but not merged is where the
		// most cells carry two different agents' ink, and so a background.
		line := markRow(row, logoWidth(), ghostClose, previewInk)
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
