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
		{name: "source", got: string(twinTheme.source), want: charmtone.Charple.Hex()},
		{name: "target", got: string(twinTheme.target), want: charmtone.Julep.Hex()},
		{name: "intersection", got: string(twinTheme.intersection), want: charmtone.Bok.Hex()},
		{name: "title", got: string(twinTheme.text), want: charmtone.Sash.Hex()},
		{name: "danger", got: string(twinTheme.danger), want: charmtone.Sriracha.Hex()},
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
	if got := fmt.Sprint(neutralModalStyle.GetBorderTopForeground()); got != charmtone.Char.Hex() {
		t.Fatalf("neutral modal border = %s, want structural gray", got)
	}
	if got := fmt.Sprint(chipActive.GetBackground()); got != charmtone.Char.Hex() {
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
