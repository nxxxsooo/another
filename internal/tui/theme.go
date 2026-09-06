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
	source:         lipgloss.Color("#6B50FF"),
	target:         lipgloss.Color("#00FFB2"),
	intersection:   lipgloss.Color("#68FFD6"),
	text:           lipgloss.Color("#F4F4F5"),
	textSubtle:     lipgloss.Color("#A1A1B3"),
	textMostSubtle: lipgloss.Color("#7E7E8F"),
	border:         lipgloss.Color("#2D2D38"),
	surface:        lipgloss.Color("#0A0A0A"),
	success:        lipgloss.Color("#00FFB2"),
	danger:         lipgloss.Color("#FF6B6B"),
	warning:        lipgloss.Color("#FF6B6B"),
	info:           lipgloss.Color("#62D8FF"),
}

// providerColors keeps every agent out of the hues this interface has already
// spent on meaning: brand purple is "source", mint is "target" and "done", red
// is "danger", pale cyan is "info". An agent painted in one of those reads as a
// state instead of a name. What is left is divided by hue rather than by brand
// fidelity, so ten labels stay apart in one dense column; a brand color is only
// used where it happens to land in a free hue. OpenCode and OpenCode 2 share a
// hue at two lightnesses because they are the same agent's two generations.
var providerColors = map[string]lipgloss.Color{
	"claude-code": charm(charmtone.Tang),    // orange
	"codex":       charm(charmtone.Smoke),   // neutral silver
	"cursor":      charm(charmtone.Thunder), // blue
	"opencode":    charm(charmtone.Pony),    // magenta
	"opencode2":   charm(charmtone.Cheeky),  // magenta, lighter
	"commandcode": charm(charmtone.Zest),    // lime
	"hermes":      charm(charmtone.Malibu),  // azure
	"pi":          charm(charmtone.Turtle),  // cyan
	"qwen":        charm(charmtone.Mauve),   // violet
	"agy":         charm(charmtone.Mustard), // yellow
}

// providerFallbacks colors an agent this build does not know about. Registry
// entries can outlive this map, and a single uncolored label in a colored
// column reads as a rendering fault rather than as a new provider.
var providerFallbacks = []lipgloss.Color{
	charm(charmtone.Yam), charm(charmtone.Salmon), charm(charmtone.Lichen),
	charm(charmtone.Anchovy), charm(charmtone.Orchid), charm(charmtone.Cumin),
}

// projectColors identifies a project by hue so repeated paths are recognized
// before they are read. These are only ever drawn as a one-cell bar, never as a
// fill, so they may sit closer together than the agent palette does.
var projectColors = []lipgloss.Color{
	charm(charmtone.Malibu), charm(charmtone.Guac), charm(charmtone.Tang),
	charm(charmtone.Cheeky), charm(charmtone.Citron), charm(charmtone.Violet),
	charm(charmtone.Sardine), charm(charmtone.Uni), charm(charmtone.Turtle),
	charm(charmtone.Hazy), charm(charmtone.Zest), charm(charmtone.Tuna),
}

// providerColor never fails, so no row can lose its agent color.
func providerColor(id string) lipgloss.Color {
	if c, ok := providerColors[id]; ok {
		return c
	}
	return providerFallbacks[int(hashKey(id)%uint32(len(providerFallbacks)))]
}

// projectColor is stable across runs and across processes: the same project
// keeps its bar color for as long as its path does.
func projectColor(path string) lipgloss.Color {
	return projectColors[int(hashKey(path)%uint32(len(projectColors)))]
}

// hashKey is FNV-1a, written out to keep this file free of a hash import for
// one call site and to pin the mapping to something that will not change under
// us the way a runtime map seed would.
func hashKey(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
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
	// The project cell is two readings at once: the bar answers "which project"
	// at a glance, the leaf answers "which directory" on a second look, and the
	// parent path stays behind both. A background fill would win the row from
	// the title and, per the note above, break on nested resets.
	projectLeafStyle   = lipgloss.NewStyle().Foreground(twinTheme.textSubtle)
	projectParentStyle = lipgloss.NewStyle().Foreground(twinTheme.textMostSubtle)
	dangerChoice       = lipgloss.NewStyle().Bold(true).Foreground(twinTheme.text).Background(twinTheme.danger).Padding(0, 1)
)
