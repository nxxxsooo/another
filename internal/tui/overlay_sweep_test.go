package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/model"
)

// The overlay cuts the session rows underneath it, and those rows are full of
// double-width characters. A cut that lands inside one of them has to be
// repaired, or the modal's border drifts a column and the rows around it tear.
// Whether a cut lands mid-character depends on the terminal width, so this
// sweeps widths instead of trusting one.
func TestOverlayBordersHoldAcrossEveryWidth(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })

	for width := 80; width <= 140; width++ {
		for _, ov := range []int{overlaySource, overlayTarget, overlayPreview, overlayDelete, overlayRename} {
			m := layoutTestModel()
			var sessions []list.Item
			for i := 0; i < 20; i++ {
				sessions = append(sessions, sessionItem{
					summary: model.Summary{
						ID: "session", Provider: "opencode",
						Title:       "Claude Code v2.1.260 发布说明摘要与后续动作",
						ProjectPath: "/Users/mingjian/Documents/sync/GitHub/another",
					},
					providerLbl: "OpenCode",
				})
			}
			m.sessions.SetItems(sessions)
			m.width, m.height, m.overlay = width, 24, ov
			m.layout()

			left, right, refRow := -1, -1, -1
			for row, line := range strings.Split(m.View(), "\n") {
				plain := ansi.Strip(line)
				if w := ansi.StringWidth(line); w != 0 && w != width {
					t.Fatalf("width %d overlay %d: row %d is %d cells wide: %q", width, ov, row, w, plain)
				}
				first, last := strings.IndexAny(plain, "┏┃┗"), strings.LastIndexAny(plain, "┓┃┛")
				if first < 0 || last <= first {
					continue
				}
				gotLeft, gotRight := ansi.StringWidth(plain[:first]), ansi.StringWidth(plain[:last])
				if left < 0 {
					left, right, refRow = gotLeft, gotRight, row
					continue
				}
				if gotLeft != left || gotRight != right {
					t.Fatalf("width %d overlay %d: border moved from (%d,%d) on row %d to (%d,%d) on row %d\n%q",
						width, ov, left, right, refRow, gotLeft, gotRight, row, plain)
				}
			}
			if left < 0 {
				t.Fatalf("width %d overlay %d: no modal border rendered", width, ov)
			}
		}
	}
}
