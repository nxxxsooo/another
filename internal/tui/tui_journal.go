package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/journal"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// historyRevertedMsg carries the outcome of reversing one journal entry.
type historyRevertedMsg struct {
	entry journal.Entry
	err   error
}

// openJournal returns another's action-history store. Recording is best-effort:
// a store that cannot be opened leaves the action itself unharmed.
func openJournal() *journal.Store {
	st, err := journal.Open()
	if err != nil {
		return nil
	}
	return st
}

func providerRef(sm model.Summary) provider.SessionRef {
	return provider.SessionRef{
		ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
	}
}

func journalEntry(action journal.Action, sm model.Summary) journal.Entry {
	now := time.Now()
	return journal.Entry{
		ID: journal.NewID(now), At: now, Action: action,
		Provider: sm.Provider, Session: sm.ID, Title: sm.Title,
		Project: sm.ProjectPath, Storage: sm.StoragePath,
	}
}

// recordArchive notes an archive toggle after it has landed. Its inverse is the
// other side of the same native toggle, so it is always recoverable.
func recordArchive(st *journal.Store, sm model.Summary, archived bool) {
	if st == nil {
		return
	}
	action := journal.ActionArchive
	if !archived {
		action = journal.ActionUnarchive
	}
	e := journalEntry(action, sm)
	e.Archived = archived
	e.Revertible = true
	_ = st.Record(e)
}

// recordRename notes a rename, carrying the old title so the revert can put it
// back through the provider's own rename.
func recordRename(st *journal.Store, sm model.Summary, newTitle string) {
	if st == nil {
		return
	}
	e := journalEntry(journal.ActionRename, sm)
	e.Before = sm.Title
	e.After = newTitle
	e.Revertible = true
	_ = st.Record(e)
}

// snapshotForDelete captures the bytes of a session about to be removed, so the
// delete can be reversed after another exits. It returns the entry that will
// describe the delete; the caller records it only once the delete has landed,
// so a failed delete never leaves a claim it happened. A snapshot the provider
// supports but cannot produce aborts the delete: silently removing a session
// non-recoverably would defeat the reason the capture exists.
func snapshotForDelete(ctx context.Context, st *journal.Store, p provider.Provider, sm model.Summary) (journal.Entry, error) {
	e := journalEntry(journal.ActionDelete, sm)
	if st == nil {
		return e, nil
	}
	snapshotter, ok := p.(provider.SessionSnapshotter)
	if !ok {
		return e, nil
	}
	snap, err := snapshotter.SnapshotSession(ctx, providerRef(sm))
	if err != nil {
		return e, err
	}
	data, err := snap.Encode()
	if err != nil {
		return e, err
	}
	if err := st.SaveSnapshot(e.ID, data); err != nil {
		return e, err
	}
	e.Revertible = true
	return e, nil
}

func recordDelete(st *journal.Store, e journal.Entry) {
	if st == nil {
		return
	}
	_ = st.Record(e)
}

func dropDeleteSnapshot(st *journal.Store, e journal.Entry) {
	if st == nil || !e.Revertible {
		return
	}
	st.DropSnapshot(e.ID)
}

// recordDeleteUndo marks a journaled delete as reversed when the one-step undo
// put the session back, so the history does not keep claiming a delete that no
// longer stands.
func recordDeleteUndo(st *journal.Store, sm model.Summary, entryID string) {
	if st == nil || entryID == "" {
		return
	}
	now := time.Now()
	_ = st.Record(journal.Entry{
		ID: journal.NewID(now), At: now, Action: journal.ActionRevert,
		Provider: sm.Provider, Session: sm.ID, Title: sm.Title, Target: entryID, Result: "undo",
	})
}

// revertEntryCmd reverses one journal entry through the provider's native
// operation — never by replaying another's copy. A delete whose bytes are gone,
// or whose agent cannot restore, refuses with a reason.
func revertEntryCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, st *journal.Store, e journal.Entry) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(e.Provider)
		if err != nil {
			return historyRevertedMsg{entry: e, err: err}
		}
		ref := provider.SessionRef{ID: e.Session, Provider: e.Provider, StoragePath: e.Storage, ProjectPath: e.Project}
		switch e.Action {
		case journal.ActionArchive, journal.ActionUnarchive:
			archiver, ok := p.(provider.SessionArchiver)
			if !ok {
				return historyRevertedMsg{entry: e, err: errors.New(txt.historyRevertUnsupported)}
			}
			if err := archiver.ArchiveSession(ctx, ref, !e.Archived); err != nil {
				return historyRevertedMsg{entry: e, err: err}
			}
		case journal.ActionRename:
			renamer, ok := p.(provider.SessionRenamer)
			if !ok {
				return historyRevertedMsg{entry: e, err: errors.New(txt.historyRevertUnsupported)}
			}
			if err := renamer.RenameSession(ctx, ref, e.Before); err != nil {
				return historyRevertedMsg{entry: e, err: err}
			}
		case journal.ActionDelete:
			if st == nil {
				return historyRevertedMsg{entry: e, err: errors.New(txt.historyNoSnapshot)}
			}
			data, ok := st.LoadSnapshot(e.ID)
			if !ok {
				return historyRevertedMsg{entry: e, err: errors.New(txt.historyNoSnapshot)}
			}
			snap, err := provider.DecodeSnapshot(data)
			if err != nil {
				return historyRevertedMsg{entry: e, err: err}
			}
			snapshotter, ok := p.(provider.SessionSnapshotter)
			if !ok {
				return historyRevertedMsg{entry: e, err: errors.New(txt.historyRevertUnsupported)}
			}
			if err := snapshotter.RestoreSession(ctx, ref, snap); err != nil {
				return historyRevertedMsg{entry: e, err: err}
			}
		default:
			return historyRevertedMsg{entry: e, err: errors.New(txt.historyRevertUnsupported)}
		}
		if st != nil {
			now := time.Now()
			_ = st.Record(journal.Entry{
				ID: journal.NewID(now), At: now, Action: journal.ActionRevert,
				Provider: e.Provider, Session: e.Session, Title: e.Title, Target: e.ID, Result: "ok",
			})
		}
		if idx != nil {
			_, _ = index.UpdateIncremental(ctx, reg, idx, e.Provider)
		}
		return historyRevertedMsg{entry: e}
	}
}
