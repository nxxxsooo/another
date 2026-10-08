package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/model"
)

func typeKeys(m modelState, text string) modelState {
	for _, r := range text {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(modelState)
	}
	return m
}

func relocateReadyModel(t *testing.T, providerID string) modelState {
	t.Helper()
	m := layoutTestModel()
	m.width, m.height = 100, 30
	it := m.sessions.SelectedItem().(sessionItem)
	it.summary = model.Summary{
		ID: "session", Provider: providerID, Title: "A useful title", ProjectPath: "/tmp/project",
	}
	m.sessions.SetItems([]list.Item{it})
	m.layout()
	return m
}

// The keymap must never advertise an action the selected agent cannot do.
func TestHelpOffersRelocateOnlyWhereItIsNative(t *testing.T) {
	if help := helpActions(relocateReadyModel(t, "pi")); !strings.Contains(help, txt.helpListRelocate) {
		t.Fatalf("pi keys hide relocate: %q", help)
	}
	if help := helpActions(relocateReadyModel(t, "codex")); strings.Contains(help, txt.helpListRelocate) {
		t.Fatalf("Codex keys advertise a relocate it cannot do: %q", help)
	}
}

func TestRelocateKeyIsRefusedForProvidersWithoutIt(t *testing.T) {
	m := relocateReadyModel(t, "codex")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.overlay == overlayRelocate {
		t.Fatal("the relocate box opened for an agent that cannot relocate")
	}
	if !strings.Contains(m.err, "Codex") {
		t.Fatalf("error does not name the agent: %q", m.err)
	}
}

// Fork is the default reading of the action, and the destructive one has to be
// chosen deliberately every time the box opens.
func TestRelocateOpensOnForkAndTogglesToMove(t *testing.T) {
	m := relocateReadyModel(t, "pi")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.overlay != overlayRelocate {
		t.Fatalf("m did not open the relocate box: overlay = %d", m.overlay)
	}
	if m.relocateMove {
		t.Fatal("the box opened on move")
	}
	if !m.relocateCanMove {
		t.Fatal("pi owns a native move but the box hid it")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(modelState)
	if !m.relocateMove {
		t.Fatal("tab did not switch to move")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(modelState)
	if m.relocateMove {
		t.Fatal("tab did not switch back to fork")
	}
	// Reopening resets to fork rather than remembering the destructive mode.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(modelState)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(modelState)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.relocateMove {
		t.Fatal("the box remembered move from the previous time it was open")
	}
}

// The box is a text field: keys that are list shortcuts outside it must reach
// the input instead of acting on the list underneath.
func TestRelocateBoxOwnsItsKeys(t *testing.T) {
	m := relocateReadyModel(t, "pi")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	m.relocateInput.SetValue("")
	m = typeKeys(m, "/tmp/x")
	if got := m.relocateInput.Value(); got != "/tmp/x" {
		t.Fatalf("input = %q, want /tmp/x", got)
	}
	if m.overlay != overlayRelocate {
		t.Fatal("typing closed the box")
	}
}

func TestRelocateRefusesADirectoryThatIsNotThere(t *testing.T) {
	m := relocateReadyModel(t, "pi")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	m.relocateInput.SetValue("/definitely/not/here")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd != nil {
		t.Fatal("a missing directory started a relocate")
	}
	if m.overlay != overlayRelocate || !strings.Contains(m.err, "does not exist") {
		t.Fatalf("overlay = %d, err = %q", m.overlay, m.err)
	}
}

func TestRelocateRefusesTheDirectoryItIsAlreadyIn(t *testing.T) {
	dir := t.TempDir()
	m := relocateReadyModel(t, "pi")
	it := m.sessions.SelectedItem().(sessionItem)
	it.summary.ProjectPath = dir
	m.sessions.SetItems([]list.Item{it})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	m.relocateInput.SetValue(dir)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd != nil || m.err != txt.relocateSameDirectory {
		t.Fatalf("cmd = %v, err = %q", cmd, m.err)
	}
}

// A relocate that landed has to leave the person holding the command that
// opens the session where it now lives.
func TestRelocateResultShowsTheResumeCommand(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		useLanguage(t, lang)
		m := relocateReadyModel(t, "pi")
		m.overlay = overlayRelocate
		updated, _ := m.Update(relocateDoneMsg{
			providerID: "pi", directory: "/tmp/worktree",
			resume: "cd '/tmp/worktree' && pi --session '/x.jsonl'",
		})
		m = updated.(modelState)
		if m.overlay != overlayNone {
			t.Fatalf("%s: box stayed open: overlay = %d", lang, m.overlay)
		}
		if m.lastResume == "" || m.launchProject != "/tmp/worktree" || m.launchTarget != "pi" {
			t.Fatalf("%s: resume = %q project = %q target = %q", lang, m.lastResume, m.launchProject, m.launchTarget)
		}
		if status := ansi.Strip(m.status); !strings.Contains(status, "/tmp/worktree") {
			t.Fatalf("%s: status hides the new directory: %q", lang, status)
		}
	}
}

// An index that could not be refreshed is a caveat, not a failure: the session
// really did move, and reporting an error would send the person looking for it
// in the old place.
func TestRelocateReportsAStaleIndexAsACaveat(t *testing.T) {
	m := relocateReadyModel(t, "pi")
	m.overlay = overlayRelocate
	updated, _ := m.Update(relocateDoneMsg{
		providerID: "pi", directory: "/tmp/worktree", moved: true,
		resume: "cd '/tmp/worktree' && pi --session '/x.jsonl'",
		err:    errStaleIndex,
	})
	m = updated.(modelState)
	if m.err != "" {
		t.Fatalf("a completed move was reported as a failure: %q", m.err)
	}
	status := ansi.Strip(m.status)
	if !strings.Contains(status, "/tmp/worktree") || !strings.Contains(status, errStaleIndex.Error()) {
		t.Fatalf("status = %q", status)
	}
}

// A failure before anything landed has no resume command, and that is what
// separates it from the caveat above.
func TestRelocateFailureKeepsTheBoxClosedAndReportsIt(t *testing.T) {
	m := relocateReadyModel(t, "pi")
	m.overlay = overlayRelocate
	updated, _ := m.Update(relocateDoneMsg{providerID: "pi", directory: "/tmp/worktree", err: errStaleIndex})
	m = updated.(modelState)
	if m.err == "" {
		t.Fatal("a failed relocate was reported as success")
	}
	if m.lastResume != "" {
		t.Fatalf("a failed relocate offered a resume command: %q", m.lastResume)
	}
}

var errStaleIndex = errors.New("relocated, but index refresh failed: database is locked")

func relocateMarkedModel(t *testing.T, providers ...string) modelState {
	t.Helper()
	m := layoutTestModel()
	m.width, m.height = 100, 30
	items := make([]list.Item, 0, len(providers))
	for i, p := range providers {
		id := fmt.Sprintf("s%d", i)
		items = append(items, sessionItem{summary: model.Summary{ID: id, Provider: p, Title: "T " + id, ProjectPath: "/tmp/project"}})
		m.marked[id] = true
	}
	m.sessions.SetItems(items)
	m.layout()
	return m
}

// With sessions marked, m takes the marked set the way ctrl+t takes it for
// rename, rather than the row under the cursor.
func TestRelocateWithMarksTakesTheMarkedSet(t *testing.T) {
	m := relocateMarkedModel(t, "pi", "pi", "pi")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.overlay != overlayRelocate {
		t.Fatalf("overlay = %d", m.overlay)
	}
	if got := len(m.relocateBatch); got != 3 {
		t.Fatalf("batch = %d sessions, want 3", got)
	}
	if !m.relocateCanMove {
		t.Fatal("every marked agent owns a native move but the toggle was hidden")
	}
	if view := ansi.Strip(m.relocateView()); !strings.Contains(view, "3") {
		t.Fatalf("view does not show the count: %q", view)
	}
}

// Sessions whose agent cannot relocate are dropped up front and counted, so
// the person knows before confirming that the set shrank.
func TestRelocateWithMarksSkipsAgentsWithoutIt(t *testing.T) {
	m := relocateMarkedModel(t, "pi", "codex")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.overlay != overlayRelocate || len(m.relocateBatch) != 1 || m.relocateSkipped != 1 {
		t.Fatalf("overlay = %d batch = %d skipped = %d", m.overlay, len(m.relocateBatch), m.relocateSkipped)
	}
	m = relocateMarkedModel(t, "codex")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	if m.overlay == overlayRelocate || m.err == "" {
		t.Fatalf("a set with nothing relocatable opened the box: overlay = %d err = %q", m.overlay, m.err)
	}
}

// A missing directory is not a typo until the person says so: the first enter
// explains, the second creates.
func TestRelocateCreatesTheDirectoryOnSecondEnter(t *testing.T) {
	target := filepath.Join(t.TempDir(), "fresh")
	m := relocateReadyModel(t, "pi")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(modelState)
	m.relocateInput.SetValue(target)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd != nil || m.overlay != overlayRelocate || !m.relocateCreatePending {
		t.Fatalf("first enter: cmd = %v overlay = %d pending = %v", cmd, m.overlay, m.relocateCreatePending)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("first enter created the directory")
	}
	// Editing the path withdraws the offer; it must be made again for the
	// new text.
	m = typeKeys(m, "x")
	if m.relocateCreatePending {
		t.Fatal("pending survived an edit")
	}
	m.relocateInput.SetValue(target)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd == nil {
		t.Fatalf("second enter did not start the relocate: err = %q", m.err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
}

func relocateSuggestModel(t *testing.T) (modelState, string) {
	t.Helper()
	wt, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := relocateReadyModel(t, "pi")
	m.projectScope.Worktrees = []string{wt}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	return updated.(modelState), wt
}

func press(m modelState, k tea.KeyType) (modelState, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: k})
	return updated.(modelState), cmd
}

// The box opens with the worktrees on offer even though the input already
// holds the launch directory: nothing has been typed to filter by yet.
func TestRelocateBoxOpensWithSuggestions(t *testing.T) {
	m, wt := relocateSuggestModel(t)
	if len(m.relocateSuggest) == 0 || m.relocateSuggest[0].path != wt {
		t.Fatalf("suggestions = %+v, want the worktree first", m.relocateSuggest)
	}
	if m.relocateCursor != -1 {
		t.Fatalf("a row was highlighted before any arrow: %d", m.relocateCursor)
	}
	view := ansi.Strip(m.relocateView())
	if !strings.Contains(view, filepath.Base(wt)) || !strings.Contains(view, txt.relocateTagWorktree) {
		t.Fatalf("view hides the suggestion: %q", view)
	}
}

// Enter on a highlighted row fills the box and stops there; only a second
// enter relocates. A glance at a row never moves a session.
func TestRelocateEnterOnSuggestionFillsWithoutSubmitting(t *testing.T) {
	m, wt := relocateSuggestModel(t)
	m, _ = press(m, tea.KeyDown)
	if m.relocateCursor != 0 {
		t.Fatalf("down: cursor = %d", m.relocateCursor)
	}
	m, cmd := press(m, tea.KeyEnter)
	if cmd != nil && m.loading {
		t.Fatal("enter on a suggestion started a relocate")
	}
	if got, want := m.relocateInput.Value(), wt+string(filepath.Separator); got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
	if m.overlay != overlayRelocate || m.relocateCursor != -1 {
		t.Fatalf("overlay = %d cursor = %d", m.overlay, m.relocateCursor)
	}
	m, cmd = press(m, tea.KeyEnter)
	if cmd == nil || !m.loading {
		t.Fatalf("second enter did not relocate: err = %q", m.err)
	}
}

func TestRelocateArrowsMoveAndRightFills(t *testing.T) {
	m, wt := relocateSuggestModel(t)
	m, _ = press(m, tea.KeyDown)
	m, _ = press(m, tea.KeyUp)
	if m.relocateCursor != -1 {
		t.Fatalf("up from the first row should return to the input: %d", m.relocateCursor)
	}
	m, _ = press(m, tea.KeyDown)
	// Tab still toggles fork and move with a row highlighted.
	m, _ = press(m, tea.KeyTab)
	if !m.relocateMove {
		t.Fatal("tab stopped toggling move")
	}
	m, _ = press(m, tea.KeyRight)
	if got := m.relocateInput.Value(); got != wt+string(filepath.Separator) {
		t.Fatalf("right did not fill: %q", got)
	}
}

// Typing narrows the list and drops the highlight, so enter after typing
// means the typed text.
func TestRelocateTypingRefiltersAndClearsTheHighlight(t *testing.T) {
	m, _ := relocateSuggestModel(t)
	m, _ = press(m, tea.KeyDown)
	m.relocateInput.SetValue("")
	m = typeKeys(m, "zzz-nothing-matches")
	if m.relocateCursor != -1 || len(m.relocateSuggest) != 0 {
		t.Fatalf("cursor = %d suggestions = %+v", m.relocateCursor, m.relocateSuggest)
	}
}

// A batch result unmarks what landed and keeps the failures marked, the way
// batch rename does, so one more m retries exactly the rows that need it.
func TestRelocateBatchDoneKeepsFailuresMarked(t *testing.T) {
	m := relocateMarkedModel(t, "pi", "pi", "pi")
	m.overlay = overlayRelocate
	updated, _ := m.Update(relocateBatchDoneMsg{
		directory: "/tmp/worktree", moved: true,
		done:   []string{"s0", "s2"},
		failed: []relocateFailure{{id: "s1", err: errors.New("locked")}},
	})
	m = updated.(modelState)
	if m.overlay != overlayNone {
		t.Fatalf("box stayed open: overlay = %d", m.overlay)
	}
	if m.marked["s0"] || m.marked["s2"] || !m.marked["s1"] {
		t.Fatalf("marks = %v", m.marked)
	}
	status := ansi.Strip(m.status)
	if !strings.Contains(status, "2") || !strings.Contains(status, "s1") || !strings.Contains(status, "locked") {
		t.Fatalf("status = %q", status)
	}
	if m.err != "" {
		t.Fatalf("a partial success was reported as failure: %q", m.err)
	}

	updated, _ = m.Update(relocateBatchDoneMsg{directory: "/tmp/worktree", done: []string{"s1"}})
	m = updated.(modelState)
	if len(m.marked) != 0 {
		t.Fatalf("marks after full success = %v", m.marked)
	}
}
