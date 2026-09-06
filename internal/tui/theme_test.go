package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/muesli/termenv"
)

func TestTwinThemeAssignsDistinctInteractionRoles(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "source", got: string(twinTheme.source), want: "#6B50FF"},
		{name: "target", got: string(twinTheme.target), want: "#00FFB2"},
		{name: "intersection", got: string(twinTheme.intersection), want: "#68FFD6"},
		{name: "text", got: string(twinTheme.text), want: "#F4F4F5"},
		{name: "text subtle", got: string(twinTheme.textSubtle), want: "#A1A1B3"},
		{name: "text most subtle", got: string(twinTheme.textMostSubtle), want: "#7E7E8F"},
		{name: "border", got: string(twinTheme.border), want: "#2D2D38"},
		{name: "surface", got: string(twinTheme.surface), want: "#0A0A0A"},
		{name: "success", got: string(twinTheme.success), want: "#00FFB2"},
		{name: "danger", got: string(twinTheme.danger), want: "#FF6B6B"},
		{name: "warning", got: string(twinTheme.warning), want: "#FF6B6B"},
		{name: "info", got: string(twinTheme.info), want: "#62D8FF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("role color = %s, want %s", tt.got, tt.want)
			}
		})
	}
	if twinTheme.source == twinTheme.target || twinTheme.source == twinTheme.intersection || twinTheme.target == twinTheme.intersection {
		t.Fatal("source, target, and intersection must remain visually distinct")
	}
}

// Agent colors name an agent. The theme's role colors name a state. An agent
// painted in a role color is read as that state, so the two sets must not
// overlap, and no two agents may share a color either.
func TestProviderColorsAreDistinctAndAvoidRoleColors(t *testing.T) {
	roles := map[string]string{
		"source":       string(twinTheme.source),
		"target":       string(twinTheme.target),
		"intersection": string(twinTheme.intersection),
		"danger":       string(twinTheme.danger),
		"info":         string(twinTheme.info),
		"success":      string(twinTheme.success),
	}
	seen := map[string]string{}
	for id, color := range providerColors {
		hex := fmt.Sprint(color)
		for role, roleHex := range roles {
			if hex == roleHex {
				t.Errorf("provider %s uses the %s role color %s", id, role, hex)
			}
		}
		if other, ok := seen[hex]; ok {
			t.Errorf("providers %s and %s share color %s", other, id, hex)
		}
		seen[hex] = id
	}
}

// Every registered agent carries a color; an uncolored label in a colored
// column reads as a rendering fault.
func TestProviderColorCoversEveryKnownAgent(t *testing.T) {
	for _, id := range []string{
		"claude-code", "codex", "cursor", "opencode", "opencode2",
		"commandcode", "hermes", "pi", "qwen", "agy",
	} {
		if _, ok := providerColors[id]; !ok {
			t.Errorf("provider %s has no explicit color", id)
		}
	}
	if providerColor("a-provider-this-build-does-not-know") == "" {
		t.Fatal("unknown provider fell through without a color")
	}
}

// The project bar must be stable across runs, or a project changes identity
// between two launches of the same browser.
func TestProjectColorIsStablePerPath(t *testing.T) {
	first := projectColor("/Users/mingjian/Documents/sync/GitHub/another")
	if second := projectColor("/Users/mingjian/Documents/sync/GitHub/another"); first != second {
		t.Fatalf("project color changed within a run: %s then %s", first, second)
	}
	if first == projectColor("/Users/mingjian/Documents/sync/GitHub/other-project") {
		t.Log("two sample projects collided; acceptable, palette is finite")
	}
}

func TestDirectionalStylesCarryTwinSemantics(t *testing.T) {
	if got := fmt.Sprint(sourceChipStyle.GetBackground()); got != charmtone.Charple.Hex() {
		t.Fatalf("source chip background = %s, want violet", got)
	}
	if got := fmt.Sprint(targetChipStyle.GetBackground()); got != charmtone.Julep.Hex() {
		t.Fatalf("target chip background = %s, want mint", got)
	}
	if got := fmt.Sprint(selectedRow.GetForeground()); got != charmtone.Bok.Hex() {
		t.Fatalf("selected row foreground = %s, want cyan-white intersection", got)
	}
	if got := fmt.Sprint(sourceModalStyle.GetBorderTopForeground()); got != charmtone.Charple.Hex() {
		t.Fatalf("source modal border = %s, want violet", got)
	}
	if got := fmt.Sprint(targetModalStyle.GetBorderTopForeground()); got != charmtone.Julep.Hex() {
		t.Fatalf("target modal border = %s, want mint", got)
	}
	if got := fmt.Sprint(neutralModalStyle.GetBorderTopForeground()); got != "#2D2D38" {
		t.Fatalf("neutral modal border = %s, want structural gray", got)
	}
	if got := fmt.Sprint(chipActive.GetBackground()); got != "#2D2D38" {
		t.Fatalf("neutral selected action background = %s, want structural gray", got)
	}
}

func TestDirectionalStylesAreWiredIntoViews(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	header := m.headerView()
	for name, want := range map[string]string{
		"source chip": sourceChipStyle.Render("全部"),
		"target chip": targetChipStyle.Render("去向 →"),
	} {
		if !strings.Contains(header, want) {
			t.Errorf("header does not use %s", name)
		}
	}

	m.overlay = overlaySource
	source := m.View()
	if !strings.Contains(source, accentStyle.Render("选择来源")) || !strings.Contains(source, selectedRow.Render("all")) {
		t.Error("source view does not use violet heading and intersection selection")
	}
	m.overlay = overlayTarget
	target := m.View()
	if !strings.Contains(target, okStyle.Render("选择去向")) || !strings.Contains(target, selectedRow.Render("Claude Code")) {
		t.Error("target view does not use mint heading and intersection selection")
	}
}

func TestDirectionalModalsNeverPaintInheritedBackground(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	boxes := []string{
		sourceModalStyle.Render(accentStyle.Render("选择来源") + "\n" + m.sourceList.View()),
		targetModalStyle.Render(okStyle.Render("选择去向") + "\n" + m.targets.View()),
	}
	for _, box := range boxes {
		if strings.Contains(box, "\x1b[48;") {
			t.Fatalf("directional modal emitted an inherited background SGR: %q", box)
		}
	}
}
