package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/model"
)

// A reorganized workspace leaves sessions pointing at directories that are
// gone. The path still names where the work happened, so it stays; what it
// loses is the color, which in this column means a project you can go to.
func TestProjectCellWithdrawsColorFromAMissingDirectory(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	live := t.TempDir()
	gone := filepath.Join(live, "removed")

	present := renderProjectCellState(live, 24, false)
	missing := renderProjectCellState(gone, 24, true)

	if ansi.Strip(missing) == "" || !strings.Contains(ansi.Strip(missing), "removed") {
		t.Fatalf("a missing directory lost its path: %q", ansi.Strip(missing))
	}
	if ansi.StringWidth(missing) != 24 {
		t.Fatalf("width = %d, want the column width", ansi.StringWidth(missing))
	}
	if present == missing {
		t.Fatal("a missing directory renders the same as a live one")
	}
	if strings.Contains(missing, colorOf(t, projectColor(gone))) {
		t.Fatalf("a missing directory kept its project color: %q", missing)
	}
}

// colorOf renders one cell in a color so a test can look for that exact
// escape sequence rather than guessing at how lipgloss writes it.
func colorOf(t *testing.T, c lipgloss.TerminalColor) string {
	t.Helper()
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return strings.TrimSuffix(rendered, "x"+ansi.ResetStyle)
}

// The row renderer runs on every keystroke, so existence is resolved when a
// page is built, once per distinct directory.
func TestSessionItemsResolveEachDirectoryOnce(t *testing.T) {
	live := t.TempDir()
	gone := filepath.Join(live, "removed")
	file := filepath.Join(live, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	items := sessionItems([]model.Summary{
		{ID: "1", ProjectPath: live},
		{ID: "2", ProjectPath: gone},
		{ID: "3", ProjectPath: gone},
		{ID: "4", ProjectPath: ""},
		// A path that exists but is not a directory is not a project either.
		{ID: "5", ProjectPath: file},
	})
	if len(items) != 5 {
		t.Fatalf("built %d rows, want every summary", len(items))
	}
	want := map[string]bool{"1": false, "2": true, "3": true, "4": false, "5": true}
	for _, item := range items {
		row := item.(sessionItem)
		if row.missingDir != want[row.summary.ID] {
			t.Fatalf("session %s missingDir = %v", row.summary.ID, row.missingDir)
		}
	}
}
