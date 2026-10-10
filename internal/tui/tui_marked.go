package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
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
	skip     batchSkip
}

type deleteBatchDoneMsg struct {
	done   []string
	failed []batchFailure
	skip   batchSkip
}

type copyBatchDoneMsg struct {
	count int
	err   error
}

// markedSplit is the marked set prepared for one action: the sessions it can act
// on, and the ones it cannot. Nothing is dropped in silence — the reader marked
// those rows deliberately, so the report has to say they were passed over and
// who passed them over.
type markedSplit struct {
	todo    []model.Summary
	skipped []model.Summary
}

// batchSkip is what a marked-set action could not take, carried into the run
// and back out again so the report can say so.
type batchSkip struct {
	count  int
	agents string
}

func (s batchSkip) text() string {
	if s.count == 0 {
		return ""
	}
	return fmt.Sprintf(txt.batchSkippedSuffixFmt, s.count, s.agents)
}

func (m modelState) markedSplit(can func(model.Summary) bool) markedSplit {
	summaries, _ := m.markedSummaries()
	var split markedSplit
	for _, sm := range summaries {
		if isCurrentSession(sm) || !can(sm) {
			split.skipped = append(split.skipped, sm)
			continue
		}
		split.todo = append(split.todo, sm)
	}
	return split
}

// skipOf is the skip a report needs: how many, and which agents could not do it.
func (m modelState) skipOf(skipped []model.Summary) batchSkip {
	if len(skipped) == 0 {
		return batchSkip{}
	}
	seen := map[string]bool{}
	agents := make([]string, 0, 4)
	for _, sm := range skipped {
		name := registry.DisplayName(m.reg, sm.Provider)
		if seen[name] {
			continue
		}
		seen[name] = true
		agents = append(agents, name)
	}
	return batchSkip{count: len(skipped), agents: strings.Join(agents, ", ")}
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
	split := m.markedSplit(m.canArchive)
	if len(split.todo) == 0 {
		// Naming the agent is the difference between "it cannot be archived"
		// and knowing which store to look at: OpenCode archives its legacy
		// V1 sessions and has no archive for the current store.
		m.err = fmt.Sprintf(txt.noneEligibleFmt, txt.batchVerbArchive, m.skipOf(split.skipped).agents)
		return m, nil
	}
	m.loading = true
	m.err = ""
	return m, tea.Batch(m.spinner.Tick,
		archiveMarkedCmd(m.ctx, m.reg, m.idx, split.todo, true, m.skipOf(split.skipped)))
}

// deleteConfirm is the batch delete's decision point: the same overlay as a
// single delete, carrying how many sessions are about to go.
func (m modelState) deleteConfirm() (tea.Model, tea.Cmd) {
	split := m.markedSplit(m.canDelete)
	if len(split.todo) == 0 {
		m.err = fmt.Sprintf(txt.noneEligibleFmt, txt.batchVerbDelete, m.skipOf(split.skipped).agents)
		return m, nil
	}
	m.deleteBatch = split.todo
	m.deleteBatchSkip = m.skipOf(split.skipped)
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
func archiveMarkedCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sessions []model.Summary, archived bool, skip batchSkip) tea.Cmd {
	return func() tea.Msg {
		done := archiveBatchDoneMsg{archived: archived, skip: skip}
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
func deleteMarkedCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sessions []model.Summary, skip batchSkip) tea.Cmd {
	return func() tea.Msg {
		done := deleteBatchDoneMsg{skip: skip}
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
		m.err = fmt.Sprintf(txt.batchSetNoneFmt, verb, len(msg.failed), batchFailureText(msg.failed))
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.batchDoneFmt, verb, len(msg.done)))
	if len(msg.failed) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.batchFailedSuffixFmt, len(msg.failed), batchFailureText(msg.failed)))
	}
	m.status += mutedStyle.Render(msg.skip.text())
	return m, nil
}

func (m modelState) onDeleteBatchDone(msg deleteBatchDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.deleteBatch, m.deleteBatchSkip = nil, batchSkip{}
	m.lastDeleted, m.restoreDeleted = nil, nil
	for _, id := range msg.done {
		delete(m.marked, id)
	}
	if len(msg.done) == 0 {
		m.err = fmt.Sprintf(txt.batchSetNoneFmt, txt.batchVerbDeleted, len(msg.failed), batchFailureText(msg.failed))
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.batchDoneFmt, txt.batchVerbDeleted, len(msg.done)))
	if len(msg.failed) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.batchFailedSuffixFmt, len(msg.failed), batchFailureText(msg.failed)))
	}
	m.status += mutedStyle.Render(msg.skip.text())
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

// migrateBatchDoneMsg reports a marked-set carry. done and failed are disjoint,
// and the sessions that failed stay marked.
type migrateBatchDoneMsg struct {
	targetID string
	done     []string
	failed   []batchFailure
	skip     batchSkip
}

// migrateMarked carries each marked session to one target the way the single
// session flow does. A session already in that agent is not a failure of the
// carry but it is not carried either, so it is reported rather than passed
// over: silence would read as success.
func migrateMarkedCmd(ctx context.Context, engine *migrate.Engine, sessions []model.Summary, to string, contextMode migrate.ContextMode, skip batchSkip) tea.Cmd {
	return func() tea.Msg {
		done := migrateBatchDoneMsg{targetID: to, skip: skip}
		for _, sm := range sessions {
			if sm.Provider == to {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: errors.New(txt.migrateSameAgent)})
				continue
			}
			res, err := engine.Run(ctx, migrate.Options{
				SessionID: sm.ID, FromProvider: sm.Provider, ToProvider: to,
				ContextMode: contextMode,
			})
			if err != nil {
				done.failed = append(done.failed, batchFailure{id: sm.ID, err: err})
				continue
			}
			_ = res
			done.done = append(done.done, sm.ID)
		}
		return done
	}
}

func (m modelState) onMigrateBatchDone(msg migrateBatchDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.migrateBatch = nil
	for _, id := range msg.done {
		delete(m.marked, id)
	}
	target := registry.DisplayName(m.reg, msg.targetID)
	if len(msg.done) == 0 {
		m.err = fmt.Sprintf(txt.batchSetNoneFmt, txt.migratedPrefix, len(msg.failed), batchFailureText(msg.failed))
		return m, nil
	}
	m.status = okStyle.Render(fmt.Sprintf(txt.batchDoneFmt, txt.migratedPrefix+target, len(msg.done)))
	if len(msg.failed) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.batchFailedSuffixFmt, len(msg.failed), batchFailureText(msg.failed)))
	}
	m.status += mutedStyle.Render(msg.skip.text())
	return m, nil
}
