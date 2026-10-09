package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

// relocateFailure is one session a batch relocate could not carry.
type relocateFailure struct {
	id  string
	err error
}

// relocateBatchDoneMsg reports a marked-set relocate. done and failed are
// disjoint; resume is the command for the last session that landed.
type relocateBatchDoneMsg struct {
	providerID string
	directory  string
	resume     string
	moved      bool
	done       []string
	failed     []relocateFailure
	caveat     error
}

// openRelocateForMarked takes the marked set the way ctrl+t does for rename.
// Sessions the agent cannot relocate, and the one running right now, are
// dropped here and counted, so the box never promises more than it can do.
func (m modelState) openRelocateForMarked() (tea.Model, tea.Cmd) {
	summaries, _ := m.markedSummaries()
	var eligible []model.Summary
	canMove := true
	for _, sm := range summaries {
		if isCurrentSession(sm) {
			continue
		}
		p, err := m.reg.Get(sm.Provider)
		if err != nil {
			continue
		}
		if _, ok := p.(provider.SessionRelocator); !ok {
			continue
		}
		caps := capabilitiesFor(p, sm)
		if !caps.RelocateFork {
			continue
		}
		canMove = canMove && caps.RelocateMove
		eligible = append(eligible, sm)
	}
	if len(eligible) == 0 {
		m.err = txt.relocateNoneMarked
		return m, nil
	}
	sel := sessionItem{summary: eligible[0]}
	m.selected = &sel
	m.relocateBatch = eligible
	m.relocateSkipped = len(summaries) - len(eligible)
	return m.openRelocateBox(canMove, eligible[0].ProjectPath)
}

func (m modelState) openRelocateBox(canMove bool, fallbackStart string) (tea.Model, tea.Cmd) {
	m.relocateMove = canMove
	m.relocateCanMove = canMove
	m.relocateCreatePending = false
	start := m.cwd
	if start == "" {
		start = fallbackStart
	}
	m.relocateInput.SetValue(start)
	m.relocateInput.CursorEnd()
	m.relocateInput.Focus()
	m.relocatePrefill = start
	m.relocateRecent = nil
	if m.idx != nil {
		// A local read capped well above what the list shows; a failure
		// only means fewer suggestions.
		m.relocateRecent, _ = m.idx.RecentProjectPaths(200)
	}
	m.refreshRelocateSuggestions()
	m.overlay = overlayRelocate
	m.err = ""
	m.layout()
	return m, textinput.Blink
}

// refreshRelocateSuggestions recomputes the list for the current text and
// drops the highlight, so enter after any edit means what was typed.
func (m *modelState) refreshRelocateSuggestions() {
	typed := m.relocateInput.Value()
	if typed == m.relocatePrefill {
		typed = ""
	}
	src := suggestSources{
		home: util.HomeDir(), cwd: m.cwd,
		worktrees: m.projectScope.Worktrees, recent: m.relocateRecent,
	}
	if len(m.relocateBatch) == 0 && m.selected != nil {
		src.exclude = util.NormalizeProjectPath(m.selected.summary.ProjectPath)
	}
	m.relocateSuggest = suggestPaths(typed, src, relocateSuggestLimit)
	m.relocateCursor = -1
}

// fillRelocateSuggestion puts the highlighted row in the box with a trailing
// separator, so the list immediately offers its children.
func (m *modelState) fillRelocateSuggestion() {
	if m.relocateCursor < 0 || m.relocateCursor >= len(m.relocateSuggest) {
		return
	}
	path := util.TildePath(m.relocateSuggest[m.relocateCursor].path)
	m.relocateInput.SetValue(path + string(filepath.Separator))
	m.relocateInput.CursorEnd()
	m.relocateCreatePending = false
	m.err = ""
	m.refreshRelocateSuggestions()
}

// confirmRelocate is enter in the box. A directory that does not exist is
// offered for creation on the first enter and created on the second, so a
// session can be carried into a worktree that is about to be made without a
// typo ever becoming a session that points at nothing.
func (m modelState) confirmRelocate() (tea.Model, tea.Cmd) {
	if m.selected == nil {
		m.err = txt.noSessionSelected
		return m, nil
	}
	typed := m.relocateInput.Value()
	create := m.relocateCreatePending && typed == m.relocateCreateFor
	directory, err := util.ResolveDir(typed, create)
	if errors.Is(err, util.ErrDirMissing) {
		m.relocateCreatePending = true
		m.relocateCreateFor = typed
		m.err = txt.relocateCreateOffer
		return m, nil
	}
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.relocateCreatePending = false
	mode := provider.RelocateFork
	if m.relocateMove {
		mode = provider.RelocateMove
	}
	if len(m.relocateBatch) == 0 {
		// Both sides are normalized: a stored path and a typed one can
		// name the same directory through different symlinks.
		if directory == util.NormalizeProjectPath(m.selected.summary.ProjectPath) {
			m.err = txt.relocateSameDirectory
			return m, nil
		}
		m.relocateInput.Blur()
		m.loading = true
		m.err = ""
		return m, tea.Batch(m.spinner.Tick,
			relocateSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary, directory, mode))
	}
	var todo []model.Summary
	for _, sm := range m.relocateBatch {
		if directory != util.NormalizeProjectPath(sm.ProjectPath) {
			todo = append(todo, sm)
		}
	}
	if len(todo) == 0 {
		m.err = txt.relocateSameDirectory
		return m, nil
	}
	m.relocateInput.Blur()
	m.loading = true
	m.err = ""
	return m, tea.Batch(m.spinner.Tick,
		relocateManyCmd(m.ctx, m.reg, m.idx, todo, directory, mode))
}

// relocateManyCmd carries each session in turn with the same native call the
// single flow uses. One failure does not stop the rest; the index is refreshed
// once per agent afterwards.
func relocateManyCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sessions []model.Summary, directory string, mode provider.RelocateMode) tea.Cmd {
	return func() tea.Msg {
		done := relocateBatchDoneMsg{directory: directory, moved: mode == provider.RelocateMove}
		touched := map[string]bool{}
		for _, sm := range sessions {
			p, err := reg.Get(sm.Provider)
			if err != nil {
				done.failed = append(done.failed, relocateFailure{id: sm.ID, err: err})
				continue
			}
			relocator, ok := p.(provider.SessionRelocator)
			if !ok || !relocator.SupportsRelocate(mode) {
				done.failed = append(done.failed, relocateFailure{id: sm.ID, err: fmt.Errorf(txt.relocateUnsupportedFmt, p.DisplayName())})
				continue
			}
			ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
			res, err := relocator.RelocateSession(ctx, ref, provider.RelocateOpts{Directory: directory, Mode: mode})
			if err != nil {
				done.failed = append(done.failed, relocateFailure{id: sm.ID, err: err})
				continue
			}
			touched[sm.Provider] = true
			done.done = append(done.done, sm.ID)
			done.providerID = sm.Provider
			done.resume = p.ResumeCommand(provider.WriteResult{
				SessionID: res.SessionID, StoragePath: res.StoragePath, ProjectPath: res.ProjectPath,
			})
		}
		for providerID := range touched {
			if _, err := index.UpdateIncremental(ctx, reg, idx, providerID); err != nil {
				done.caveat = fmt.Errorf("relocated, but index refresh failed: %w", err)
			}
		}
		return done
	}
}

func (m modelState) onRelocateBatchDone(msg relocateBatchDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.relocateInput.Blur()
	m.relocateBatch, m.relocateSkipped = nil, 0
	for _, id := range msg.done {
		delete(m.marked, id)
	}
	if len(msg.done) == 0 {
		m.err = fmt.Sprintf(txt.relocateBatchAllFailedFmt, len(msg.failed), relocateFailureText(msg.failed))
		return m, nil
	}
	verb := txt.relocateBatchForkedFmt
	if msg.moved {
		verb = txt.relocateBatchMovedFmt
	}
	m.status = okStyle.Render(fmt.Sprintf(verb, len(msg.done), elidePath(util.TildePath(msg.directory), 40)))
	if len(msg.failed) > 0 {
		m.status += errStyle.Render(fmt.Sprintf(txt.relocateBatchFailedFmt, len(msg.failed), relocateFailureText(msg.failed)))
	}
	if msg.caveat != nil {
		m.status += mutedStyle.Render("  ·  " + msg.caveat.Error())
	}
	m.lastResume = msg.resume
	m.launchTarget = msg.providerID
	m.launchProject = msg.directory
	m.err = ""
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func relocateFailureText(failed []relocateFailure) string {
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, f.id+" ("+f.err.Error()+")")
	}
	return strings.Join(parts, ", ")
}
