package tui

import (
	"errors"
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
