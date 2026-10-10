package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/util"
)

// A shortened path keeps its root and its tail. Cut from the left, every row
// in a list of sibling projects opened with the same "…" and spent its first
// cells on the middle of a word.
func TestElidedPathsKeepTheirRootAndTail(t *testing.T) {
	for _, tc := range []struct {
		path  string
		width int
		want  string
	}{
		// Untouched when it fits, then as much of the tail as the column can
		// hold — the root stays whatever happens.
		{"~/Documents/sync/Docs/health", 40, "~/Documents/sync/Docs/health"},
		{"~/Documents/sync/Docs/health", 20, "~/…/sync/Docs/health"},
		{"~/Documents/sync/Docs/health", 16, "~/…/Docs/health"},
		{"/Users/someone/Documents/sync/Work/fit", 22, "/…/sync/Work/fit"},
		{"~/Documents/sync/Work/huatu/projects/smart-note", 30, "~/…/huatu/projects/smart-note"},
		{"~/Documents/sync/Work/huatu/projects/smart-note", 24, "~/…/projects/smart-note"},
	} {
		got := elidePath(tc.path, tc.width)
		if got != tc.want {
			t.Errorf("elidePath(%q, %d) = %q, want %q", tc.path, tc.width, got, tc.want)
		}
		if w := ansi.StringWidth(got); w > tc.width {
			t.Errorf("elidePath(%q, %d) is %d cells wide", tc.path, tc.width, w)
		}
	}
}

// A path with nothing above it is read in a column of paths from unrelated
// trees, so it is shortened to one depth rather than to whatever the column
// happens to hold. Told at its own depth, "~/…/sync/GitHub/another" beside
// "~/…/projects/smart-note" made two projects look like two kinds of thing.
//
// The cut is not marked. Nearly every row in this column is cut, so a leading
// "…" is the same constant on all of them — the prefix that was just removed —
// and a shallow path still reads as one without it.
func TestGlobalPathsAreShortenedToAFixedDepth(t *testing.T) {
	for _, tc := range []struct {
		path  string
		width int
		want  string
	}{
		{"~/Documents/sync/GitHub/another", 27, "sync/GitHub/another"},
		{"~/Documents/sync/Tuning", 27, "Documents/sync/Tuning"},
		{"~/Documents/sync/Work/fit/projects/fit-infra", 27, "fit/projects/fit-infra"},
		{"~/Documents/sync/Work/huatu/projects/smart-note", 27, "huatu/projects/smart-note"},
		{"/opt/homebrew/var/log", 27, "homebrew/var/log"},
		// A path already at the fixed depth, or shorter, is its own tail and
		// keeps its own spelling.
		{"~/Documents", 27, "~/Documents"},
		{"~/Documents", 6, "…ments"},
		{"~", 27, "~"},
		{"/", 27, "/"},
		// Too narrow for the window: the bucket goes and the owner stays,
		// then the name alone.
		{"~/Documents/sync/Work/fit/projects/fit-infra", 20, "fit/fit-infra"},
		{"~/Documents/sync/Work/huatu/projects/smart-note", 20, "huatu/smart-note"},
		{"/Users/mingjian/Documents/apps/ht-canteen-miaoda", 24, "apps/ht-canteen-miaoda"},
		// Two ancestors of equal width: the parent stays, because it reads as
		// the name's own prefix.
		{"~/Documents/sync/Work/huatu", 10, "Work/huatu"},
		{"~/Documents/sync/Work/huatu", 8, "huatu"},
		{"~/Documents/sync/Work/fit/work", 10, "fit/work"},
		{"~/Documents/sync/Work/fit/projects/fit-infra", 12, "fit-infra"},
		{"~/Documents/sync/Work/fit/projects/fit-infra", 5, "…nfra"},
	} {
		got := elidePathTail(tc.path, tc.width, pathTailDepth)
		if got != tc.want {
			t.Errorf("elidePathTail(%q, %d) = %q, want %q", tc.path, tc.width, got, tc.want)
		}
		if w := ansi.StringWidth(got); w > tc.width {
			t.Errorf("elidePathTail(%q, %d) is %d cells wide", tc.path, tc.width, w)
		}
	}
}

// Depth is a preference (ui.path_depth), so the same path is asked for at
// several: the window widens with it, and what a narrow column spends is still
// the generic segments before the owner or the name.
func TestGlobalPathDepthIsAPreference(t *testing.T) {
	const path = "~/Documents/sync/Work/huatu/projects/smart-note"
	for _, tc := range []struct {
		width int
		depth int
		want  string
	}{
		{60, 1, "smart-note"},
		{60, 2, "projects/smart-note"},
		{60, 3, "huatu/projects/smart-note"},
		{60, 4, "Work/huatu/projects/smart-note"},
		{60, 5, "sync/Work/huatu/projects/smart-note"},
		{60, 6, "Documents/sync/Work/huatu/projects/smart-note"},
		{60, 7, "~/Documents/sync/Work/huatu/projects/smart-note"},
		{60, 99, "~/Documents/sync/Work/huatu/projects/smart-note"},
		// A depth the column cannot hold gives up its buckets, not its owner.
		{24, 4, "Work/huatu/smart-note"},
		// Narrow enough that a proper name is the widest thing left, which is
		// the point at which the choice stops mattering.
		{18, 4, "Work/smart-note"},
		{0, 4, ""},
		// Depth zero is not a spelling; the caller that forgot one still gets
		// the default rather than a blank column.
	} {
		got := elidePathTail(path, tc.width, tc.depth)
		if got != tc.want {
			t.Errorf("elidePathTail(%q, %d, depth %d) = %q, want %q", path, tc.width, tc.depth, got, tc.want)
		}
		if tc.width > 0 {
			if w := ansi.StringWidth(got); w > tc.width {
				t.Errorf("elidePathTail(%q, %d, depth %d) is %d cells wide", path, tc.width, tc.depth, w)
			}
		}
	}
	if got := pathDepthOr(0); got != pathTailDepth {
		t.Errorf("pathDepthOr(0) = %d, want the default %d", got, pathTailDepth)
	}
}

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
	if !strings.Contains(present, projectChipSeq(t, live)) {
		t.Fatalf("a live directory did not wear its project chip: %q", present)
	}
	if strings.Contains(missing, projectChipSeq(t, gone)) {
		t.Fatalf("a missing directory kept its project color: %q", missing)
	}
}

// The chip is one flat style. A nested style would emit an ANSI reset inside
// the painted cell, which clears the background it was drawn on and leaves a
// black rectangle in Ghostty — the bug that kept this column a bar for so long.
func TestProjectChipPaintsOneUnbrokenStyle(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	root := "/Users/mingjian/Documents/sync/GitHub/another"
	cell := renderProjectCellState(root+"/.worktrees/delete-undo", root, 24, false)
	if got := strings.Count(cell, "\x1b[0m"); got != 1 {
		t.Fatalf("the chip resets %d times, want one reset at its end: %q", got, cell)
	}
	if idx := strings.Index(cell, "\x1b[0m"); idx >= 0 && strings.Contains(cell[idx+len("\x1b[0m"):], "\x1b[") {
		t.Fatalf("the chip restyles after its reset: %q", cell)
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
		// The root has nothing below itself to name, so it names itself
		// rather than standing in a column of identical marks.
		{"the project root itself", root, "another"},
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
	want := projectChipSeq(t, worktree)
	for _, base := range []string{root, ""} {
		cell := renderProjectCellState(worktree, base, 24, false)
		if !strings.Contains(cell, want) {
			t.Fatalf("the same directory hashes differently at base %q: %q", base, cell)
		}
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

// projectChipSeq is colorOf for a project chip, which writes its ink and its
// paint in one sequence: looking for either half on its own finds nothing.
func projectChipSeq(t *testing.T, path string) string {
	t.Helper()
	ink, tint := chipColors(projectColor(path))
	rendered := lipgloss.NewStyle().Foreground(ink).Background(tint).Render("x")
	idx := strings.Index(rendered, "x")
	if idx <= 0 {
		t.Fatalf("a chip rendered no escape sequence: %q", rendered)
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

// The global scope has no project root, so it is read against ui.path_base —
// the prefix every row would otherwise repeat. The base is spelling only: it
// never decides which sessions are listed, and a row it does not cover is
// still shown, written from home and marked with the root it kept.
func TestGlobalColumnReadsAgainstTheConfiguredBase(t *testing.T) {
	base := "/Users/mingjian/Documents/sync"
	m := modelState{scopeMode: scopeModeAll, pathBase: base, cwd: base + "/Work/huatu"}
	if got := m.projectBase(); got != base {
		t.Fatalf("projectBase = %q, want the configured base", got)
	}
	d := sessionDelegateFor(&m)
	if !d.global {
		t.Fatal("the global scope did not mark its column global")
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{base, "sync"},
		{base + "/Tuning", "Tuning"},
		{base + "/GitHub/another", "GitHub/another"},
		{base + "/Work/fit/projects/fit-infra", "fit/projects/fit-infra"},
		{base + "/Work/huatu/projects/smart-note", "huatu/projects/smart-note"},
		// Outside the base. Read at the default depth the window leaves the
		// root behind, and what it shows is the part of the path the base has
		// no spelling for; one segment deeper and the "~" comes back with it.
		{"/Users/mingjian/Documents/apps/ht-canteen-miaoda", "Documents/apps/ht-canteen-miaoda"},
		{"/tmp/project", "/tmp/project"},
	} {
		cell := ansi.Strip(renderProjectChipCell(tc.path, util.SanitizeDisplay(projectCellText(tc.path, base)), true, 34, false, true, pathTailDepth))
		if !strings.Contains(cell, tc.want) {
			t.Errorf("cell for %q = %q, want it to carry %q", tc.path, cell, tc.want)
		}
		if strings.Contains(cell, "Documents/sync") {
			t.Errorf("cell for %q spent its width on the base: %q", tc.path, cell)
		}
	}
	// At depth 4 the same row is still the deepest one in the column, and the
	// root it keeps is what says it is the one the base does not hold. The path
	// is built from this machine's home, so the test reads the same wherever it
	// runs: the "~" only appears for a directory that is really under home.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to build the outside path from")
	}
	out := filepath.Join(home, "Documents", "apps", "ht-canteen-miaoda")
	cell := ansi.Strip(renderProjectChipCell(out, util.SanitizeDisplay(projectCellText(out, base)), true, 34, false, true, 4))
	sep := string(filepath.Separator)
	want := "~" + sep + "apps" + sep + "ht-canteen-miaoda"
	if !strings.Contains(cell, want) {
		t.Errorf("cell at depth 4 = %q, want %q", cell, want)
	}
}

// A stored path carries the platform's separator, and the elisions read paths
// in segments on every platform: a Windows path is not one long word.
func TestPathElisionReadsBothSeparators(t *testing.T) {
	win := `C:\Users\mingjian\Documents\sync\Work\huatu\projects\smart-note`
	if got := elidePathTail(win, 40, pathTailDepth); got != `huatu\projects\smart-note` {
		t.Errorf("elidePathTail(%q) = %q, want the last three segments", win, got)
	}
	if got := elidePathTail(win, 12, pathTailDepth); got != `smart-note` {
		t.Errorf("elidePathTail(%q, 12) = %q, want the name", win, got)
	}
	got := elidePath(win, 24)
	if !strings.HasPrefix(got, `C:\…\`) || !strings.HasSuffix(got, `smart-note`) {
		t.Errorf("elidePath(%q, 24) = %q, want the root kept and the tail", win, got)
	}
	if w := ansi.StringWidth(got); w > 24 {
		t.Errorf("elidePath(%q, 24) is %d cells wide", win, w)
	}
}

// Without a base the column is read from home, and that is what an unset
// ui.path_base means: the spelling every path already starts at.
func TestGlobalColumnWithoutABaseKeepsReadingFromHome(t *testing.T) {
	m := modelState{scopeMode: scopeModeAll}
	if got := m.projectBase(); got != "" {
		t.Fatalf("projectBase = %q, want home (an empty base)", got)
	}
	cell := ansi.Strip(renderProjectChipCell(
		"/Users/mingjian/Documents/sync/Work/huatu/projects/smart-note",
		util.TildePath("/Users/mingjian/Documents/sync/Work/huatu/projects/smart-note"),
		true, 34, false, true, pathTailDepth))
	if !strings.Contains(cell, "huatu/projects/smart-note") {
		t.Fatalf("cell = %q, want the fixed tail from home", cell)
	}
}

// ui.path_base goes through the same resolution as any other directory a
// person types: a tilde expands, a value that is home or empty means home, and
// a value that cannot be resolved leaves the base unset instead of failing.
func TestConfiguredPathBaseResolvesOrFallsBackToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to resolve against")
	}
	if got := configuredPathBase(config.UI{PathBase: "~/Documents/sync"}); got != filepath.Join(home, "Documents", "sync") {
		t.Errorf("pathBase = %q, want the expanded directory", got)
	}
	for _, ui := range []config.UI{
		{},
		{PathBase: "~"},
		{PathBase: home},
		{PathBase: ""},
	} {
		if got := configuredPathBase(ui); got != "" {
			t.Errorf("pathBase for %+v = %q, want home (unset)", ui, got)
		}
	}
}

// ui.path_depth is a width preference, so a value outside any width a column
// could hold is clamped rather than refused: an unusable number should not take
// the screen down with it, and zero is not a spelling.
func TestConfiguredPathDepthClampsToWhatAColumnCouldHold(t *testing.T) {
	for _, tc := range []struct {
		ui   config.UI
		want int
	}{
		{config.UI{}, pathTailDepth},
		{config.UI{PathDepth: -1}, pathTailDepth},
		{config.UI{PathDepth: 1}, 1},
		{config.UI{PathDepth: 4}, 4},
		{config.UI{PathDepth: maxPathDepth + 1}, maxPathDepth},
	} {
		if got := configuredPathDepth(tc.ui); got != tc.want {
			t.Errorf("configuredPathDepth(%+v) = %d, want %d", tc.ui, got, tc.want)
		}
	}
}
