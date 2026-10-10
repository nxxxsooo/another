package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/config"
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

// The footer is the surface that says what this screen can do, so it has to
// follow the state it is in: a closed band offers to open it, and a marked list
// names the verbs that will act on all of it.
func TestTheFooterFollowsTheStateItIsIn(t *testing.T) {
	m := markedModel(t, 3)
	if got := ansi.Strip(m.markStatus()); !strings.Contains(got, "archive") || !strings.Contains(got, "move") {
		t.Fatalf("the marked footer does not name the set verbs: %q", got)
	}
	// The line is truncated with an ellipsis at the band's width, so it has to
	// stay inside what the rest of the footer is allowed to use.
	if w := ansi.StringWidth(ansi.Strip(m.markStatus())); w > 72 {
		t.Fatalf("the marked footer is %d cells, which truncates on ordinary terminals", w)
	}

	grouped := sampleModel(t, 132, 32)
	grouped.groupMode = groupTree
	grouped.ungrouped = sampleSessions()
	grouped.folded = map[string]bool{}
	for _, key := range bandKeys(grouped.groupedItems()) {
		grouped.folded[key] = true
	}
	grouped.sessions.SetItems(grouped.groupedItems())
	grouped.skipGroupHeader(true)
	grouped.applySessionDelegate()
	grouped.layout()
	if !grouped.selectedFoldedBand() {
		t.Fatal("the cursor is not on a closed band")
	}
	if got := grouped.help(); !strings.Contains(got, "z") {
		t.Fatalf("the footer on a closed band does not offer z: %q", got)
	}
}

// A set the agent cannot act on has to say which agent, because "its agent"
// hides the one fact that explains it: OpenCode archives its legacy V1 sessions
// and has no archive for the store its current sessions live in.
func TestABlanketRefusalNamesTheAgent(t *testing.T) {
	m := layoutTestModel()
	m.reg = registry.NewWith(noArchiveProvider{})
	first := m.sessions.SelectedItem().(sessionItem)
	row := first
	row.summary.ID = "only"
	row.summary.Provider = "codex"
	row.summary.StoragePath = "/tmp/only"
	m.marked["only"] = true
	m.sessions.SetItems([]list.Item{row})
	m.sessions.Select(0)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(modelState)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(modelState)
	if !strings.Contains(ansi.Strip(m.err), "Codex") {
		t.Fatalf("the refusal does not name the agent: %q", ansi.Strip(m.err))
	}
}

type noArchiveProvider struct{ provider.Provider }

func (noArchiveProvider) ID() string          { return "codex" }
func (noArchiveProvider) DisplayName() string { return "Codex" }

// The window narrows first, because looking further back is the rare direction,
// and it ends in no window at all. A configured number that is not a preset
// still steps down, so the key always means the same thing.
func TestTheWindowKeyNarrowsThenOpensEverything(t *testing.T) {
	for _, tc := range []struct{ from, want int }{
		{90, 30}, {30, 7}, {7, 0}, {0, 90},
		{180, 90}, {45, 30}, {1, 0},
	} {
		if got := nextRecentWindow(tc.from); got != tc.want {
			t.Errorf("nextRecentWindow(%d) = %d, want %d", tc.from, got, tc.want)
		}
	}
	if got := configuredRecentDays(config.UI{}); got != defaultRecentDays {
		t.Errorf("an unstated window = %d, want the %d-day default", got, defaultRecentDays)
	}
	if got := configuredRecentDays(config.UI{RecentDays: -1}); got != 0 {
		t.Errorf("a negative window = %d, want no window", got)
	}
	if got := configuredRecentDays(config.UI{RecentDays: 14}); got != 14 {
		t.Errorf("a stated window = %d, want it honored", got)
	}
}

// The window is a filter on the index, so it reaches the fetch, the count, and
// search the same way the scope does; and the header says which window is on,
// because it is what the count is a count of.
func TestTheWindowReachesTheQueryAndTheHeader(t *testing.T) {
	m := markedModel(t, 0)
	m.recentDays = 30
	cutoff := m.windowCutoff()
	if cutoff <= 0 {
		t.Fatal("a 30-day window produced no cutoff")
	}
	if got := listOptsFor(m).Since; got != cutoff {
		t.Fatalf("list opts cutoff = %d, want %d", got, cutoff)
	}
	if got := searchOptsFor(m, "needle").Since; got != cutoff {
		t.Fatalf("search opts cutoff = %d, want %d", got, cutoff)
	}
	if view := ansi.Strip(m.scopeView(true)); !strings.Contains(view, "30") {
		t.Fatalf("the header does not say which window is on: %q", view)
	}

	m.recentDays = 0
	if got := listOptsFor(m).Since; got != 0 {
		t.Fatalf("no window still filtered the fetch: %d", got)
	}
	if view := ansi.Strip(m.scopeView(true)); strings.Contains(view, "days") {
		t.Fatalf("the header claims a window that is off: %q", view)
	}
}

// The modal keeps saying what it is about to delete while the delete runs, and
// nothing else draws in its place: clearing the set at the confirm step left
// the single-session branch to render, which is where "No session selected"
// came from.
func TestTheBatchModalKeepsItsSubjectWhileTheWorkRuns(t *testing.T) {
	m := markedModel(t, 3)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = updated.(modelState)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(modelState)
	if cmd == nil {
		t.Fatal("confirming the batch dispatched nothing")
	}
	if len(m.deleteBatch) != 3 {
		t.Fatalf("the modal lost its subject while working: %d left", len(m.deleteBatch))
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, txt.noSessionSelected) {
		t.Fatalf("the modal drew the empty-session notice instead of its count:\n%s", view)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "3") {
		t.Fatalf("the modal does not say how many are going:\n%s", view)
	}

	// The report for a batch that landed nothing is a sentence, not a format
	// string: %s none: %d failed, %s.
	updated, _ = m.Update(deleteBatchDoneMsg{failed: []batchFailure{{id: "one", err: errors.New("agent said no")}}})
	m = updated.(modelState)
	report := ansi.Strip(m.err)
	if strings.Contains(report, "%!") || strings.Contains(report, "%s") {
		t.Fatalf("the report is unformatted: %q", report)
	}
	if !strings.Contains(report, "agent said no") || !strings.Contains(report, "1") {
		t.Fatalf("the report lost its reason or count: %q", report)
	}
	if m.overlay != overlayNone || len(m.deleteBatch) != 0 {
		t.Fatal("the modal outlived its run")
	}
}
