package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The agent is the one column that repeats on every row of the session list,
// and agent names have nothing in common: "Antigravity" fills eleven cells and
// "Pi" fills two. Set as words they leave a ragged edge, and a short name reads
// as a smaller agent for no reason other than its spelling. A chip removes the
// question: every agent is the same block, identified by color and a fixed
// three-letter code rather than by length.
//
// The block is tinted, not filled. A column of saturated blocks would take the
// row away from the title — the same reason the project column is a bar and not
// a fill — so the agent's color is laid over the surface at low strength and the
// code is written in the color itself.

// agentCodeWidth is how many cells a code occupies. Three is the shortest width
// that still lets ten agents keep a pronounceable code.
const agentCodeWidth = 3

// agentChipWidth is what one chip costs a row: the code and a cell of quiet on
// each side. It buys back seven columns from the twelve the spelled-out names
// held, and those columns go to the title.
const agentChipWidth = agentCodeWidth + 2

// chipTint is how much of the agent's color reaches the block. Enough to read
// as that agent's chip in a column of them, not enough to compete with the
// title beside it.
const chipTint = 0.18

// minChipContrast is the WCAG ratio a code must clear against its own tint.
const minChipContrast = 4.5

// agentCodes are read like tickers, so they follow how the name is said rather
// than how it is spelled: an agent should be recognizable from the code before
// its color is learned. The agents whose names all begin with C differ by their
// second letter, which is where a scanning eye lands next.
var agentCodes = map[string]string{
	"claude-code": "CLA",
	"codex":       "CDX",
	"cursor":      "CUR",
	"opencode":    "OPC",
	"opencode2":   "OC2",
	"commandcode": "CMD",
	"hermes":      "HRM",
	"pi":          "PI",
	"qwen":        "QWN",
	"agy":         "AGY",
}

// allChipLabel marks the row that is not an agent at all. It takes the same
// shape so the source list reads as one column of chips rather than one chip
// and a stray word.
const allChipLabel = "ALL"

// agentCode falls back to the provider ID for an agent this build does not
// know, for the same reason providerColor has a fallback: a blank chip in a
// column of chips reads as a rendering fault, not as a new agent.
func agentCode(id string) string {
	if code, ok := agentCodes[id]; ok {
		return code
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(id) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
		if b.Len() == agentCodeWidth {
			break
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}

// renderAgentChip draws one agent as a chip in its own color.
func renderAgentChip(id string) string {
	color := providerColor(id)
	ink, tint := chipColors(color)
	return chipBody(padCode(agentCode(id)), ink, tint)
}

// renderNeutralChip draws a chip that stands for no single agent. It borrows
// the border color, which is what this interface uses everywhere else for
// something present but not directional.
func renderNeutralChip(label string) string {
	return chipBody(padCode(label), twinTheme.textSubtle, twinTheme.border)
}

func chipBody(label string, ink, background lipgloss.Color) string {
	return lipgloss.NewStyle().Bold(true).
		Foreground(ink).Background(background).
		Render(" " + label + " ")
}

// padCode keeps every chip the same width whatever the code costs. Pi is the
// reason it exists, and the spare cell goes on the right: every code then
// starts at the same column, which is what a column of chips is scanned by.
func padCode(code string) string {
	if len(code) > agentCodeWidth {
		code = code[:agentCodeWidth]
	}
	return code + strings.Repeat(" ", agentCodeWidth-len(code))
}

// chipColors lays the agent's color over the surface for the block and writes
// the code in that same color. Two agents in this palette are dark enough that
// their own color would not clear the tint they produce, so the code is lifted
// toward the interface's paper white until it does — the hue survives, and the
// chip stays readable without ten hand-tuned pairs.
func chipColors(color lipgloss.Color) (ink, tint lipgloss.Color) {
	tint = blend(color, twinTheme.surface, chipTint)
	ink = color
	for i := 0; i < 4 && contrastRatio(relativeLuminance(string(ink)), relativeLuminance(string(tint))) < minChipContrast; i++ {
		ink = blend(twinTheme.text, ink, 0.25)
	}
	return ink, tint
}

// blend mixes strength of the front color into the back one.
func blend(front, back lipgloss.Color, strength float64) lipgloss.Color {
	f, okF := parseHex(string(front))
	b, okB := parseHex(string(back))
	if !okF || !okB {
		return front
	}
	var mixed [3]int
	for i := range mixed {
		mixed[i] = int(math.Round(f[i]*strength + b[i]*(1-strength)))
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", mixed[0], mixed[1], mixed[2]))
}

// contrastRatio is the WCAG ratio, order-independent so callers can pass a pair
// in whichever order reads better at the call site.
func contrastRatio(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// relativeLuminance reads the "#RRGGBB" colors this theme is written in. An
// unparseable color reports mid gray, which leaves the ink where it is instead
// of crashing a row.
func relativeLuminance(hex string) float64 {
	channels, ok := parseHex(hex)
	if !ok {
		return 0.5
	}
	return 0.2126*linearize(channels[0]/255) +
		0.7152*linearize(channels[1]/255) +
		0.0722*linearize(channels[2]/255)
}

// parseHex reads "#RRGGBB" into 0..255 channels.
func parseHex(hex string) ([3]float64, bool) {
	hex = strings.TrimPrefix(hex, "#")
	var channels [3]float64
	if len(hex) != 6 {
		return channels, false
	}
	for i := range channels {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return channels, false
		}
		channels[i] = float64(v)
	}
	return channels, true
}

// linearize undoes the sRGB transfer curve so the channels can be weighted.
func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
