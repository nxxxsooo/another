package util

import (
	"strings"
	"unicode"
)

// SanitizeDisplay removes the runes whose width only the font knows.
//
// A session title is sometimes a captured shell prompt, and a Nerd Font prompt
// carries private-use glyphs: separators, folder icons, a clock. Every width
// table calls a private-use rune one cell wide because that is all it can say;
// the terminal draws several of them two cells wide. One such rune in a list
// row makes the row wider than the pane it was measured for, the terminal wraps
// it, and from that row down the renderer's idea of which line it is on is
// wrong — which is how a previous frame ends up left on screen.
//
// Control characters go for the same reason: they move the cursor or reset
// colors in the middle of a row that was measured as plain text.
func SanitizeDisplay(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || unicode.IsSpace(r):
			// Runs of whitespace collapse: dropping a glyph out of a prompt
			// otherwise leaves a hole where the icon used to be.
			space = b.Len() > 0
		case unicode.In(r, unicode.Co, unicode.Cs, unicode.Cf) || unicode.IsControl(r):
			// Dropped without a placeholder: a replacement character has a
			// width of its own and would be one more thing to measure.
		default:
			if space {
				b.WriteRune(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
