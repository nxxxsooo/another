package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// markedFlowProvider stops at the native-operation boundary, the way the
// archive and delete flow tests do: the question here is what the set does, not
// what an agent's store does.
type markedFlowProvider struct{ provider.Provider }

func (markedFlowProvider) ID() string          { return "codex" }
func (markedFlowProvider) DisplayName() string { return "Codex" }
func (markedFlowProvider) ArchiveSession(context.Context, provider.SessionRef, bool) error {
	return nil
}
func (markedFlowProvider) DeleteSession(context.Context, provider.SessionRef) error { return nil }
func (markedFlowProvider) ResumeCommand(provider.WriteResult) string                { return "resume" }

// markedModel is a list of numbered sessions with the first n marked.
func markedModel(t *testing.T, n int) modelState {
	t.Helper()
	m := layoutTestModel()
	m.reg = registry.NewWith(markedFlowProvider{})
	first := m.sessions.SelectedItem().(sessionItem)
	items := make([]list.Item, 0, 3)
	for i, id := range []string{"one", "two", "three"} {
		row := first
		row.summary.ID = id
		row.summary.StoragePath = "/tmp/" + id
		if i < n {
			m.marked[id] = true
		}
		items = append(items, row)
	}
	m.sessions.SetItems(items)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(modelState)
	m.sessions.Select(0)
	m.applySessionDelegate()
	return m
}

// One action key, one set: a marked list is what archive acts on, and anything
// the agent cannot archive is counted rather than dropped in silence.
func TestArchiveActsOnTheMarkedSet(t *testing.T) {
	m := markedModel(t, 2)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(modelState)
	if cmd == nil {
		t.Fatal("archiving a marked set dispatched nothing")
	}
	done, ok := firstMarkedMsg[archiveBatchDoneMsg](t, cmd)
	if !ok {
		t.Fatal("the marked archive did not run its batch command")
	}
	if len(done.done) != 2 {
		t.Fatalf("archived %d sessions, want the two marked", len(done.done))
	}

	updated, _ = m.Update(done)
	m = updated.(modelState)
	if len(m.marked) != 0 {
		t.Fatalf("applied rows stayed marked: %v", m.marked)
	}
	if !strings.Contains(ansi.Strip(m.status), "2") {
		t.Fatalf("the report does not say how many landed: %q", ansi.Strip(m.status))
	}
}

// Delete asks about the set, not about the row the cursor happens to be on.
func TestDeleteAsksAboutTheWholeSet(t *testing.T) {
	m2 := markedModel(t, 3)
	updated, _ := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m2 = updated.(modelState)
	if m2.overlay != overlayDelete {
		t.Fatalf("ctrl+d did not open the confirmation: overlay=%v", m2.overlay)
	}
	if len(m2.deleteBatch) != 3 {
		t.Fatalf("the confirmation covers %d sessions, want the three marked", len(m2.deleteBatch))
	}
	if view := ansi.Strip(m2.View()); !strings.Contains(view, "3") {
		t.Fatalf("the confirmation does not say how many: %q", view)
	}

	// Confirming runs the set through the delete command: the choice is moved
	// to Delete and entered.
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyRight})
	m2 = updated.(modelState)
	updated, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = updated.(modelState)
	done, ok := firstMarkedMsg[deleteBatchDoneMsg](t, cmd)
	if !ok || len(done.done) != 3 {
		t.Fatalf("the confirmed batch deleted %d sessions", len(done.done))
	}
	// A batch keeps no undo: the offer restores one session, not a set.
	updated, _ = m2.Update(done)
	m2 = updated.(modelState)
	if m2.lastDeleted != nil {
		t.Fatal("a batch delete armed the one-session undo")
	}
}

// The report is what tells a reader which rows still need them.
func TestABatchReportKeepsFailedRowsMarked(t *testing.T) {
	m := markedModel(t, 2)
	updated, _ := m.Update(deleteBatchDoneMsg{
		done:   []string{"one"},
		failed: []batchFailure{{id: "two", err: errors.New("agent said no")}},
	})
	m = updated.(modelState)
	if m.marked["one"] {
		t.Fatal("a deleted session stayed marked")
	}
	if !m.marked["two"] {
		t.Fatal("a failed session lost its mark, so nothing can retry it")
	}
	status := ansi.Strip(m.status)
	if !strings.Contains(status, "1") || !strings.Contains(status, "agent said no") {
		t.Fatalf("the report does not name the failure: %q", status)
	}
}

// Copy changes nothing, so it needs no confirmation; it does have to build one
// command per marked session.
func TestCopyMarkedBuildsOneCommandPerRow(t *testing.T) {
	m := markedModel(t, 3)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("copying a marked set dispatched nothing")
	}
	done, ok := firstMarkedMsg[copyBatchDoneMsg](t, cmd)
	if !ok {
		t.Fatal("the marked copy did not run its command")
	}
	if done.count != 3 {
		t.Fatalf("copied %d commands, want the three marked", done.count)
	}
}

// firstMarkedMsg runs a command batch and returns the first message of the
// wanted type. The clipboard is absent in most test environments, so callers
// assert on the message rather than on the side effect.
func firstMarkedMsg[T any](t *testing.T, cmd tea.Cmd) (T, bool) {
	t.Helper()
	var zero T
	if cmd == nil {
		return zero, false
	}
	for _, msg := range flattenCmd(t, cmd) {
		if typed, ok := msg.(T); ok {
			return typed, true
		}
	}
	return zero, false
}

func flattenCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, child := range batch {
			if child == nil {
				continue
			}
			out = append(out, flattenCmd(t, child)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// Carrying a set is the same drawer as carrying one session, opened over the
// whole marked set, and the modal says how many are about to travel.
func TestCarryOpensTheTargetDrawerOverTheMarkedSet(t *testing.T) {
	m := markedModel(t, 3)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	if m.overlay != overlayTarget {
		t.Fatalf("right did not open the target drawer: overlay=%v", m.overlay)
	}
	if len(m.migrateBatch) != 3 {
		t.Fatalf("the drawer covers %d sessions, want the three marked", len(m.migrateBatch))
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "3") {
		t.Fatalf("the drawer does not say how many are travelling: %q", view)
	}
}

// A carry that lands clears the mark; one that did not keeps it, which is what
// makes the next attempt a matter of pressing right again.
func TestCarryReportsPerSessionAndKeepsFailuresMarked(t *testing.T) {
	m := markedModel(t, 3)
	updated, _ := m.Update(migrateBatchDoneMsg{
		targetID: "pi",
		done:     []string{"one", "two"},
		failed:   []batchFailure{{id: "three", err: errors.New("pi refused")}},
	})
	m = updated.(modelState)
	if m.marked["one"] || m.marked["two"] {
		t.Fatalf("carried sessions stayed marked: %v", m.marked)
	}
	if !m.marked["three"] {
		t.Fatal("a session that did not travel lost its mark")
	}
	status := ansi.Strip(m.status)
	if !strings.Contains(status, "2") || !strings.Contains(status, "pi refused") {
		t.Fatalf("the report does not name the outcome: %q", status)
	}
	if m.overlay != overlayNone {
		t.Fatal("the drawer stayed open after the carry finished")
	}
}
