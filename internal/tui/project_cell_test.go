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
	"github.com/nxxxsooo/another/internal/util"
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

	present := renderProjectCellState(live, "", 24, false)
	missing := renderProjectCellState(gone, "", 24, true)

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

// A project is not one directory. Git worktrees and a monorepo's subtrees all
// belong to the project the browser is scoped to, and there the column has 28
// cells to spend on the part of the path that differs — never on the prefix
// every row shares.
func TestProjectCellReadsAPathAgainstTheProjectRoot(t *testing.T) {
	root := "/Users/mingjian/Documents/sync/GitHub/another"
	cases := []struct {
		name string
		path string
		want string
	}{
		{"a linked worktree under the root", root + "/.worktrees/delete-undo", ".worktrees/delete-undo"},
		{"a package in a monorepo", root + "/packages/api", "packages/api"},
		{"the project root itself", root, projectRootMark},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectCellText(tc.path, root); got != tc.want {
				t.Fatalf("projectCellText = %q, want %q", got, tc.want)
			}
			cell := ansi.Strip(renderProjectCellState(tc.path, root, 24, false))
			if !strings.Contains(cell, tc.want) {
				t.Fatalf("cell = %q, want it to carry %q", cell, tc.want)
			}
			if strings.Contains(cell, "GitHub") {
				t.Fatalf("cell spent its width on the shared prefix: %q", cell)
			}
		})
	}
}

// A worktree can be registered outside the repository it belongs to. It is
// still part of the project, and the only honest thing to show is where it
// actually is — not a relative path climbing out of the root.
func TestProjectCellFallsBackToTheFullPathOutsideTheRoot(t *testing.T) {
	root := "/Users/mingjian/Documents/sync/GitHub/another"
	outside := "/Users/mingjian/.worktrees/another-hotfix"
	if got := projectCellText(outside, root); got != util.TildePath(outside) {
		t.Fatalf("projectCellText = %q, want the full path", got)
	}
	if got := projectCellText(filepath.Dir(root), root); got != util.TildePath(filepath.Dir(root)) {
		t.Fatalf("the parent of the root rendered as a relative path: %q", got)
	}
}

// Color in this column identifies a project, so a directory must keep one hue
// whether the browser is scoped to its project or showing everything.
func TestProjectCellColorSurvivesTheProjectRoot(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	root := "/Users/mingjian/Documents/sync/GitHub/another"
	worktree := root + "/.worktrees/delete-undo"
	want := colorOf(t, projectColor(worktree))
	if !strings.Contains(renderProjectCellState(worktree, root, 24, false), want) {
		t.Fatal("a relative-rendered path lost its project color")
	}
	if !strings.Contains(renderProjectCellState(worktree, "", 24, false), want) {
		t.Fatal("the same directory hashes differently across scopes")
	}
}

// Whatever the column says, it says it in exactly the width it was given; a
// row wider than its pane is what leaves a previous frame on screen.
func TestProjectCellKeepsItsWidthAgainstARoot(t *testing.T) {
	root := "/Users/mingjian/Documents/sync/GitHub/another"
	paths := []string{
		root,
		root + "/.worktrees/session-directory-attribution",
		root + "/packages/一个很长的中文目录名称",
		"/Users/mingjian/.worktrees/another-hotfix",
		"",
	}
	for _, path := range paths {
		for width := 3; width <= 28; width++ {
			if got := ansi.StringWidth(renderProjectCellState(path, root, width, false)); got != width {
				t.Errorf("cell width for %q at %d = %d", path, width, got)
			}
		}
	}
}

// colorOf renders one cell in a color so a test can look for that exact
// escape sequence rather than guessing at how lipgloss writes it. Only the
// sequence before the cell is the color; what follows it is the reset, which
// every style ends with and which would match anything.
func colorOf(t *testing.T, c lipgloss.TerminalColor) string {
	t.Helper()
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	idx := strings.Index(rendered, "x")
	if idx <= 0 {
		t.Fatalf("a foreground color rendered no escape sequence: %q", rendered)
	}
	return rendered[:idx]
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
