package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/journal"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// histProvider records what the native operations were asked to do, so a revert
// can be checked where it lands rather than through an agent store.
type histProvider struct {
	provider.Provider
	archived bool
	title    string
	deleted  bool
	restored *provider.SessionSnapshot
	failSnap bool
}

func (h *histProvider) ID() string          { return "fake" }
func (h *histProvider) DisplayName() string { return "Fake" }

func (h *histProvider) ArchiveSession(_ context.Context, _ provider.SessionRef, archived bool) error {
	h.archived = archived
	return nil
}
func (h *histProvider) RenameSession(_ context.Context, _ provider.SessionRef, title string) error {
	h.title = title
	return nil
}
func (h *histProvider) DeleteSession(context.Context, provider.SessionRef) error {
	h.deleted = true
	return nil
}
func (h *histProvider) SnapshotSession(context.Context, provider.SessionRef) (*provider.SessionSnapshot, error) {
	if h.failSnap {
		return nil, errors.New("cannot read the session")
	}
	return &provider.SessionSnapshot{Data: []byte("session bytes"), StoragePath: "/tmp/sess", Mode: 0o600}, nil
}
func (h *histProvider) RestoreSession(_ context.Context, _ provider.SessionRef, snap *provider.SessionSnapshot) error {
	h.restored = snap
	return nil
}

func histSummary() model.Summary {
	return model.Summary{ID: "s1", Provider: "fake", Title: "old title", StoragePath: "/tmp/sess", ProjectPath: "/tmp"}
}

func TestArchiveAndRenameAreJournaled(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := openJournal()
	if st == nil {
		t.Fatal("journal store did not open")
	}
	sm := histSummary()
	recordArchive(st, sm, true)
	recordRename(st, sm, "new title")
	entries, err := st.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Action != journal.ActionArchive || !entries[0].Archived || !entries[0].Revertible {
		t.Fatalf("archive entry = %+v", entries[0])
	}
	if entries[1].Action != journal.ActionRename || entries[1].Before != "old title" || entries[1].After != "new title" {
		t.Fatalf("rename entry = %+v", entries[1])
	}
}

// A delete that a provider can snapshot is recoverable from the journal: the
// bytes are captured before the removal and put back by a later revert.
func TestDeleteIsRecoverableThroughTheJournal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hp := &histProvider{}
	reg := registry.NewWith(hp)
	st := openJournal()
	sm := histSummary()
	entry, err := snapshotForDelete(context.Background(), st, hp, sm)
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Revertible {
		t.Fatal("a snapshotted delete must be marked recoverable")
	}
	recordDelete(st, entry)

	msg := revertEntryCmd(context.Background(), reg, nil, st, entry)().(historyRevertedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if hp.restored == nil || string(hp.restored.Data) != "session bytes" {
		t.Fatalf("restore did not receive the captured bytes: %+v", hp.restored)
	}
	entries, _ := st.Entries()
	last := entries[len(entries)-1]
	if last.Action != journal.ActionRevert || last.Target != entry.ID {
		t.Fatalf("revert was not journaled: %+v", last)
	}
}

// A snapshot the provider advertises but cannot produce aborts the delete, so a
// session is never removed without the bytes that would bring it back.
func TestSnapshotFailureAbortsDelete(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hp := &histProvider{failSnap: true}
	st := openJournal()
	if _, err := snapshotForDelete(context.Background(), st, hp, histSummary()); err == nil {
		t.Fatal("a snapshot failure did not abort the delete")
	}
}

func TestRevertArchiveTogglesBack(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hp := &histProvider{archived: true}
	reg := registry.NewWith(hp)
	e := journal.Entry{ID: "e1", Action: journal.ActionArchive, Provider: "fake", Session: "s1", Archived: true, Revertible: true}
	if msg := revertEntryCmd(context.Background(), reg, nil, openJournal(), e)().(historyRevertedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	if hp.archived {
		t.Fatal("reverting an archive did not unarchive")
	}
}

func TestRevertRenamePutsTheOldTitleBack(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hp := &histProvider{}
	reg := registry.NewWith(hp)
	e := journal.Entry{ID: "e1", Action: journal.ActionRename, Provider: "fake", Session: "s1", Before: "old title", After: "new title", Revertible: true}
	if msg := revertEntryCmd(context.Background(), reg, nil, openJournal(), e)().(historyRevertedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	if hp.title != "old title" {
		t.Fatalf("revert renamed to %q, want the old title", hp.title)
	}
}

// A delete whose bytes are gone is refused with a reason, never faked by
// replaying another's copy.
func TestRevertDeleteWithoutSnapshotRefuses(t *testing.T) {
	hp := &histProvider{}
	reg := registry.NewWith(hp)
	e := journal.Entry{ID: "e1", Action: journal.ActionDelete, Provider: "fake", Session: "s1", Revertible: true}
	msg := revertEntryCmd(context.Background(), reg, nil, openJournal(), e)().(historyRevertedMsg)
	if msg.err == nil {
		t.Fatal("a delete with no captured bytes was reverted")
	}
}

// The h key opens the overlay and shows what the journal holds.
func TestHistoryKeyOpensTheOverlay(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	recordArchive(openJournal(), histSummary(), true)
	m := layoutTestModel()
	m.historyList = newHistoryList(nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got := updated.(modelState)
	if got.overlay != overlayHistory {
		t.Fatalf("h did not open the history overlay: %v", got.overlay)
	}
	if len(got.historyList.Items()) != 1 {
		t.Fatalf("overlay rows = %d, want 1", len(got.historyList.Items()))
	}
}
