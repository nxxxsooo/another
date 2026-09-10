package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func knownAgentIDs() []string {
	return []string{
		"claude-code", "codex", "cursor", "opencode", "opencode2",
		"commandcode", "codem", "hermes", "pi", "qwen", "agy",
	}
}

// The chip exists to make every agent the same size. An agent whose chip is a
// cell wider or narrower would push its row's title out of the column the rest
// of the list shares, which is the exact defect the chip replaced.
func TestAgentChipIsOneWidthForEveryAgent(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for _, id := range append(knownAgentIDs(), "a-provider-this-build-does-not-know", "") {
		if got := ansi.StringWidth(renderAgentChip(id)); got != agentChipWidth {
			t.Errorf("chip for %q is %d cells, want %d", id, got, agentChipWidth)
		}
	}
	if got := ansi.StringWidth(renderNeutralChip(allChipLabel)); got != agentChipWidth {
		t.Errorf("neutral chip is %d cells, want %d", got, agentChipWidth)
	}
}

// Codes are read instead of names, so two agents sharing one code would make
// two rows claim the same agent.
func TestAgentCodesAreDistinctAndFitTheChip(t *testing.T) {
	seen := map[string]string{}
	for _, id := range knownAgentIDs() {
		code := agentCode(id)
		if len(code) == 0 || len(code) > agentCodeWidth {
			t.Errorf("code for %s is %q, want 1..%d characters", id, code, agentCodeWidth)
		}
		if code != strings.ToUpper(code) {
			t.Errorf("code for %s is %q, want upper case", id, code)
		}
		if other, ok := seen[code]; ok {
			t.Errorf("agents %s and %s share code %s", other, id, code)
		}
		seen[code] = id
	}
}

// An agent this build does not know still gets a code, for the same reason it
// still gets a color: a blank chip in a column of chips reads as a broken row.
func TestUnknownAgentStillGetsACode(t *testing.T) {
	if got := agentCode("some-future-agent"); got != "SOM" {
		t.Fatalf("code = %q, want SOM", got)
	}
	if got := agentCode("!!!"); got != "?" {
		t.Fatalf("code for an unusable ID = %q, want ?", got)
	}
}

// The code is written in the agent's own color over a tint of that same color,
// which is legible for a bright agent and not for a dark one. Every chip has to
// clear the contrast floor without a hand-tuned pair.
func TestChipColorsKeepEveryAgentLegible(t *testing.T) {
	for _, id := range knownAgentIDs() {
		ink, tint := chipColors(providerColor(id))
		got := contrastRatio(relativeLuminance(string(ink)), relativeLuminance(string(tint)))
		if got < minChipContrast {
			t.Errorf("%s chip contrast = %.2f, want >= %.1f", id, got, minChipContrast)
		}
	}
	ink, tint := chipColors(twinTheme.textSubtle)
	if ink == tint {
		t.Fatal("a chip must not write its code in its own background")
	}
}

// The tint is what keeps a column of chips from taking the row away from the
// titles. If it ever approached the agent's full color, the list would be a
// column of blocks with titles beside them.
func TestChipTintStaysBehindTheTitle(t *testing.T) {
	for _, id := range knownAgentIDs() {
		color := providerColor(id)
		_, tint := chipColors(color)
		if tint == color {
			t.Errorf("%s chip is filled with its own color", id)
		}
		against := contrastRatio(relativeLuminance(string(tint)), relativeLuminance(string(twinTheme.surface)))
		if against > 2 {
			t.Errorf("%s chip tint is %.2f against the surface, want a quiet block", id, against)
		}
	}
}

// Luminance drives the ink choice, so a wrong reading is a silently unreadable
// chip rather than a crash. Pin the two ends and one known mid tone.
func TestRelativeLuminanceReadsThemeColors(t *testing.T) {
	tests := []struct {
		hex  string
		want float64
	}{
		{hex: "#000000", want: 0},
		{hex: "#FFFFFF", want: 1},
		{hex: "not a color", want: 0.5},
	}
	for _, tt := range tests {
		if got := relativeLuminance(tt.hex); got < tt.want-0.001 || got > tt.want+0.001 {
			t.Errorf("luminance(%s) = %.4f, want %.1f", tt.hex, got, tt.want)
		}
	}
}
