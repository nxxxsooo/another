package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/charmtone"
)

// theme names colors by their job, not by a raw ANSI slot. The palette follows
// Crush's Charmtone Pantera direction: near-black structure with bright,
// deliberately sparse accents.
type theme struct {
	primary, secondary, accent       lipgloss.Color
	text, textSubtle, textMostSubtle lipgloss.Color
	border, borderFocus, surface     lipgloss.Color
	success, danger, warning, info   lipgloss.Color
}

func charm(key charmtone.Key) lipgloss.Color { return lipgloss.Color(key.Hex()) }

var punkTheme = theme{
	primary:        charm(charmtone.Charple),
	secondary:      charm(charmtone.Dolly),
	accent:         charm(charmtone.Bok),
	text:           charm(charmtone.Sash),
	textSubtle:     charm(charmtone.Smoke),
	textMostSubtle: charm(charmtone.Oyster),
	border:         charm(charmtone.Char),
	borderFocus:    charm(charmtone.Charple),
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
}

var (
	accentStyle  = lipgloss.NewStyle().Bold(true).Foreground(punkTheme.primary)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(punkTheme.secondary)
	mutedStyle   = lipgloss.NewStyle().Foreground(punkTheme.textMostSubtle)
	okStyle      = lipgloss.NewStyle().Foreground(punkTheme.success)
	errStyle     = lipgloss.NewStyle().Foreground(punkTheme.danger)
	paneStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(punkTheme.border).Padding(0, 1)
	modalStyle   = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(punkTheme.borderFocus).Background(punkTheme.surface).Padding(1, 3)
	footerStyle  = lipgloss.NewStyle().Foreground(punkTheme.textMostSubtle)
	chipActive   = lipgloss.NewStyle().Bold(true).Foreground(punkTheme.text).Background(punkTheme.primary).Padding(0, 1)
	chipMuted    = lipgloss.NewStyle().Foreground(punkTheme.textSubtle).Padding(0, 1)
	selectedRow  = lipgloss.NewStyle().Bold(true).Foreground(punkTheme.secondary)
	dangerChoice = lipgloss.NewStyle().Bold(true).Foreground(punkTheme.text).Background(punkTheme.danger).Padding(0, 1)
)
