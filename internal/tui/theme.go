package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/charmtone"
)

// theme names colors by their job, not by a raw ANSI slot. Neutral ink carries
// the dense interface; the brand pair only appears where direction matters.
type theme struct {
	source, target, intersection     lipgloss.Color
	text, textSubtle, textMostSubtle lipgloss.Color
	border, surface                  lipgloss.Color
	success, danger, warning, info   lipgloss.Color
}

func charm(key charmtone.Key) lipgloss.Color { return lipgloss.Color(key.Hex()) }

var twinTheme = theme{
	source:         charm(charmtone.Charple),
	target:         charm(charmtone.Julep),
	intersection:   charm(charmtone.Bok),
	text:           charm(charmtone.Sash),
	textSubtle:     charm(charmtone.Smoke),
	textMostSubtle: charm(charmtone.Oyster),
	border:         charm(charmtone.Char),
	surface:        charm(charmtone.BBQ),
	success:        charm(charmtone.Julep),
	danger:         charm(charmtone.Sriracha),
	warning:        charm(charmtone.Mustard),
	info:           charm(charmtone.Malibu),
}

var providerColors = map[string]lipgloss.Color{
	"claude-code": charm(charmtone.Coral),
	"codex":       charm(charmtone.Julep),
	"cursor":      charm(charmtone.Malibu),
	"opencode":    charm(charmtone.Charple),
	"opencode2":   charm(charmtone.Dolly),
	"commandcode": charm(charmtone.Tang),
	"hermes":      charm(charmtone.Guppy),
	"pi":          charm(charmtone.Blush),
	"agy":         charm(charmtone.Mustard),
}

var (
	accentStyle = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.source)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text)
	mutedStyle  = lipgloss.NewStyle().Foreground(twinTheme.textMostSubtle)
	okStyle     = lipgloss.NewStyle().Foreground(twinTheme.success)
	errStyle    = lipgloss.NewStyle().Foreground(twinTheme.danger)
	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(twinTheme.border).Padding(0, 1)
	// Do not paint one inherited background behind nested styled content.
	// Provider labels emit ANSI resets, which also reset the inherited background
	// and leave black rectangles in Ghostty. Direction comes from the frame and
	// labels, never from a large inherited fill.
	neutralModalStyle = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(twinTheme.border).Padding(1, 3)
	sourceModalStyle  = neutralModalStyle.BorderForeground(twinTheme.source)
	targetModalStyle  = neutralModalStyle.BorderForeground(twinTheme.target)
	modalStyle        = neutralModalStyle
	footerStyle       = lipgloss.NewStyle().Foreground(twinTheme.textMostSubtle)
	sourceChipStyle   = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Background(twinTheme.source).Padding(0, 1)
	targetChipStyle   = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.surface).Background(twinTheme.target).Padding(0, 1)
	chipActive        = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Background(twinTheme.border).Padding(0, 1)
	chipMuted         = lipgloss.NewStyle().Foreground(twinTheme.textSubtle).Padding(0, 1)
	selectedRow       = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.intersection)
	dangerChoice      = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Background(twinTheme.danger).Padding(0, 1)
)
