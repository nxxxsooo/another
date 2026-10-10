package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
)

// batchFailure is one session a marked-set action could not finish. The id is
// what keeps the row marked for a retry; the error is what the report says.
type batchFailure struct {
	id  string
	err error
}

// archiveBatchDoneMsg, deleteBatchDoneMsg, and copyBatchDoneMsg report the
// three actions that act on the marked set without a preview step. done and
// failed are disjoint, and failed sessions stay marked.
type archiveBatchDoneMsg struct {
	archived bool
	done     []string
	failed   []batchFailure
}

type deleteBatchDoneMsg struct {
	done   []string
	failed []batchFailure
}

type copyBatchDoneMsg struct {
	count int
	err   error
}

// markedEligible is the marked set prepared for one action: the sessions it can
// act on, and how many it cannot. A session that cannot be acted on is counted
// rather than dropped silently — the reader marked it deliberately.
func (m modelState) markedEligible(can func(model.Summary) bool) []model.Summary {
	summaries, _ := m.markedSummaries()
	todo := make([]model.Summary, 0, len(summaries))
	for _, sm := range summaries {
		if isCurrentSession(sm) || !can(sm) {
			continue
		}
		todo = append(todo, sm)
	}
	return todo
}

// canArchive and canDelete ask the source agent, which is the only one that can
// answer: capability is a property of the agent that owns the session.
func (m modelState) canArchive(sm model.Summary) bool {
	p, err := m.reg.Get(sm.Provider)
	if err != nil {
		return false
	}
	return capabilitiesFor(p, sm).Archive
}

func (m modelState) canDelete(sm model.Summary) bool {
	p, err := m.reg.Get(sm.Provider)
	if err != nil {
		return false
	}
	return capabilitiesFor(p, sm).Delete
}

// archiveMarked archives the marked sessions the way the single-session key
// does, one native call each. One failure does not stop the rest, and the index
// is refreshed once per agent afterwards.
func (m modelState) archiveMarked() (tea.Model, tea.Cmd) {
	todo := m.markedEligible(m.canArchive)
	if len(todo) == 0 {
		m.err = txt.archiveNoneMarked
		return m, nil
	}
	m.loading = true
	m.err = ""
	return m, tea.Batch(m.spinner.Tick, archiveMarkedCmd(m.ctx, m.reg, m.idx, todo, true))
}

// deleteConfirm is the batch delete's decision point: the same overlay as a
// single delete, carrying how many sessions are about to go.
func (m modelState) deleteConfirm() (tea.Model, tea.Cmd) {
	todo := m.markedEligible(m.canDelete)
	if len(todo) == 0 {
		m.err = txt.deleteNoneMarked
		return m, nil
	}
	m.deleteBatch = todo
	m.selected = nil
	m.deleteChoice = 0
	m.overlay = overlayDelete
	m.layout()
	return m, nil
}

// copyMarked puts one resume command per marked session on the clipboard, in
// the order the list shows them. It is the one action on the set that changes
// nothing, so it needs no confirmation and no result report beyond a count.
func (m modelState) copyMarked() (tea.Model, tea.Cmd) {
	summaries, _ := m.markedSummaries()
	if len(summaries) == 0 {
		m.err = txt.copyNoneMarked
		return m, nil
	}
	commands := make([]string, 0, len(summaries))
	for _, sm := range summaries {
		p, err := m.reg.Get(sm.Provider)
		if err != nil {
			continue
		}
		commands = append(commands, p.ResumeCommand(provider.WriteResult{
			SessionID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
		}))
	}
	if len(commands) == 0 {
		m.err = txt.copyNoneMarked
		return m, nil
	}
	count := len(commands)
	return m, copyBatchCmd(strings.Join(commands, "\n"), count)
}

// archiveMarkedCmd carries each session in turn with the same native call the
// single flow uses.
func archiveMarkedCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sessions []model.Summary, archived bool) tea.Cmd {
	return func() tea.Msg {
		done := archiveBatchDoneMsg{archived: archived}
		touched := map[string]bool{}
		for _, sm := range sessions {
			p, err := reg.Get(sm.Provider)
			if err != nil {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: err})
				continue
			}
			archiver, ok := p.(provider.SessionArchiver)
			if !ok {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: fmt.Errorf(txt.archiveUnsupportedFm, p.DisplayName())})
				continue
			}
			ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
			if err := archiver.ArchiveSession(ctx, ref, archived); err != nil {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: err})
				continue
			}
			touched[sm.Provider] = true
			done.done = append(done.done, sm.ID)
		}
		for providerID := range touched {
			if idx == nil {
				continue
			}
			_, _ = index.UpdateIncremental(ctx, reg, idx, providerID)
		}
		return done
	}
}

// deleteMarkedCmd deletes each session in turn. A batch keeps no undo: the
// one-step offer restores a single session, and a row that comes back without
// its neighbours is not the state the person had. The confirmation says so
// before anything is deleted.
func deleteMarkedCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sessions []model.Summary) tea.Cmd {
	return func() tea.Msg {
		done := deleteBatchDoneMsg{}
		touched := map[string]bool{}
		for _, sm := range sessions {
			p, err := reg.Get(sm.Provider)
			if err != nil {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: err})
				continue
			}
			deleter, ok := p.(provider.SessionDeleter)
			if !ok {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: fmt.Errorf(txt.deleteUnsupportedFmt, p.DisplayName())})
				continue
			}
			ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
			if err := deleter.DeleteSession(ctx, ref); err != nil {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: err})
				continue
			}
			touched[sm.Provider] = true
			done.done = append(done.done, sm.ID)
		}
		for providerID := range touched {
			if idx == nil {
				continue
			}
			_, _ = index.UpdateIncremental(ctx, reg, idx, providerID)
		}
		return done
	}
}

func copyBatchCmd(text string, count int) tea.Cmd {
	return func() tea.Msg {
		return copyBatchDoneMsg{count: count, err: clipboard.WriteAll(text)}
	}
}

// onArchiveBatchDone and onDeleteBatchDone finish a marked-set action: what
// landed leaves the selection, what did not stays for another attempt.
func (m modelState) onArchiveBatchDone(msg archiveBatchDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	for _, id := range msg.done {
		delete(m.marked, id)
	}
	verb := txt.archiveBatchVerbArchived
	if !msg.archived {
		verb = txt.archiveBatchVerbUnarchived
	}
	if len(msg.done) == 0 {
		m.err = fmt.Sprintf(txt.batchAllFailedFmt, verb, len(msg.failed), batchFailureText(msg.failed))
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.batchDoneFmt, verb, len(msg.done)))
	if len(msg.failed) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.batchFailedSuffixFmt, len(msg.failed), batchFailureText(msg.failed)))
	}
	return m, nil
}

func (m modelState) onDeleteBatchDone(msg deleteBatchDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.lastDeleted, m.restoreDeleted = nil, nil
	for _, id := range msg.done {
		delete(m.marked, id)
	}
	if len(msg.done) == 0 {
		m.err = fmt.Sprintf(txt.batchAllFailedFmt, txt.batchVerbDeleted, len(msg.failed), batchFailureText(msg.failed))
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.batchDoneFmt, txt.batchVerbDeleted, len(msg.done)))
	if len(msg.failed) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.batchFailedSuffixFmt, len(msg.failed), batchFailureText(msg.failed)))
	}
	return m, nil
}

func (m modelState) onCopyBatchDone(msg copyBatchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = errStyle.Render(txt.resumeCopyFailed)
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.copyBatchDoneFmt, msg.count))
	return m, nil
}

// batchFailureText names the first failure so the status line says something a
// reader can act on; the rest are counted.
func batchFailureText(failures []batchFailure) string {
	if len(failures) == 0 {
		return ""
	}
	return failures[0].err.Error()
}
