package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/charmtone"
)

// The generated face must be an exact rectangle, or the wordmark shears and
// the caption's right-aligned version drifts off the block.
func TestGeneratedFaceIsRectangular(t *testing.T) {
	if len(wordmarkRows) == 0 {
		t.Fatal("logo_face.go is empty; regenerate with scripts/render-logo-face.py")
	}
	for i, row := range wordmarkRows {
		if got := len([]rune(row)); got != markWidth {
			t.Errorf("face row %d width = %d, want markWidth %d", i, got, markWidth)
		}
	}
}

// The mark is drawn with half-blocks so the sub-pixel grid is square. A stray
// whole-block-only face would mean the generator regressed to the tall grid.
func TestFaceUsesHalfBlocks(t *testing.T) {
	joined := strings.Join(wordmarkRows, "")
	for _, r := range joined {
		if !strings.ContainsRune(" \u2588\u2580\u2584", r) {
			t.Fatalf("face contains %q, which is not a half-block cell", r)
		}
	}
	if !strings.ContainsAny(joined, "\u2580\u2584") {
		t.Error("face uses no half-blocks; it would render twice as tall as intended")
	}
}

// The brand rule is that the name is always lowercase.
func TestWordmarkIsLowercase(t *testing.T) {
	if logoWord != strings.ToLower(logoWord) {
		t.Errorf("logoWord = %q, must be lowercase", logoWord)
	}
}

// The tear must leave ink on both sides, or the rupture reads as the mark
// simply vanishing rather than as one identity in two places.
func TestTearSplitsTheMarkInTwo(t *testing.T) {
	if tearSeam < 1 || tearSeam >= markHeight() {
		t.Fatalf("tearSeam = %d, must land within rows 1..%d", tearSeam, markHeight()-1)
	}
	var above, below int
	for r, row := range wordmarkRows {
		n := len(strings.TrimSpace(row))
		if r < tearSeam {
			above += n
		} else {
			below += n
		}
	}
	if above == 0 || below == 0 {
		t.Fatalf("tear leaves an empty half: %d above, %d below", above, below)
	}
}

// The whole point of the rebuild was a shorter mark. Guard the height so a
// future regeneration at a taller size fails loudly.
func TestMarkStaysShort(t *testing.T) {
	if markHeight() > 5 {
		t.Errorf("mark is %d rows; the half-block face should need at most 5", markHeight())
	}
}

// The animation redraws in place with a cursor-up jump of exactly logoHeight()
// lines. If any frame differs in height the jump desyncs and the block smears;
// if any line differs in width the wipe leaves debris from the previous frame.
func TestEveryFrameHasIdenticalDimensions(t *testing.T) {
	SetVersion("1.27.1")
	t.Cleanup(func() { SetVersion("") })

	for _, f := range goodbyeScript {
		frame := renderFrame(f.slip, f.tension)
		lines := strings.Split(frame, "\n")
		if len(lines) != logoHeight() {
			t.Fatalf("frame slip=%d has %d lines, want %d", f.slip, len(lines), logoHeight())
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got != logoWidth() {
				t.Errorf("frame slip=%d line %d width = %d, want %d", f.slip, i, got, logoWidth())
			}
		}
	}
}

// A slipped upper half must never be clipped at the right edge, or letters lose
// strokes at the moment the mark ruptures.
func TestSlipNeverClipsTheMark(t *testing.T) {
	rest := ansi.Strip(renderFrame(0, 0))
	for _, f := range goodbyeScript {
		if f.slip > tearShift {
			t.Fatalf("script slips %d cells, wider than the %d reserved", f.slip, tearShift)
		}
		frame := ansi.Strip(renderFrame(f.slip, f.tension))
		if ink(frame) != ink(rest) {
			t.Errorf("slip=%d changed the inked cell count: %d vs %d", f.slip, ink(frame), ink(rest))
		}
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

// The story is stable, one rupture, back to stillness. It must start and end at
// rest, or the frame left in scrollback is a broken-looking mark.
func TestScriptOpensAndClosesAtRest(t *testing.T) {
	first, last := goodbyeScript[0], goodbyeScript[len(goodbyeScript)-1]
	if first.slip != 0 || first.tension != 0 {
		t.Errorf("script opens ruptured: %+v", first)
	}
	if last.slip != 0 || last.tension != 0 {
		t.Errorf("script closes ruptured: %+v", last)
	}
	var ruptures int
	for i := 1; i < len(goodbyeScript); i++ {
		if goodbyeScript[i].slip > 0 && goodbyeScript[i-1].slip == 0 {
			ruptures++
		}
	}
	if ruptures != 1 {
		t.Errorf("script ruptures %d times, want exactly one", ruptures)
	}
}

// Tension walks between the two stable brand states: source violet and
// destination mint. The rupture previews the migration rather than adding a
// third, unrelated campaign color.
func TestColourMixEndpointsAndMidpoint(t *testing.T) {
	if got := string(mix(charmtone.Charple, charmtone.Julep, 0)); got != "#6B50FF" {
		t.Errorf("tension 0 = %s, want Charple #6B50FF", got)
	}
	if got := string(mix(charmtone.Charple, charmtone.Julep, 1)); got != "#00FFB2" {
		t.Errorf("tension 1 = %s, want Julep #00FFB2", got)
	}
	if got := string(mix(charmtone.Charple, charmtone.Julep, 0.5)); got != "#35A8D8" {
		t.Errorf("tension 0.5 = %s, want the violet-mint midpoint #35A8D8", got)
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
