package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

// Update routes every message to one handler. Async results are matched by
// type, key presses go through updateKey, and anything else feeds the
// component that currently owns the screen.
func (m modelState) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.onWindowSize(msg)
	case sizeProbeMsg:
		return m, onSizeProbe(msg)
	case sessionsPageMsg:
		return m.onSessionsPage(msg)
	case previewLoadedMsg:
		return m.onPreviewLoaded(msg)
	case archiveDoneMsg:
		return m.onArchiveDone(msg)
	case titleSuggestionMsg:
		return m.onTitleSuggestion(msg)
	case renameDoneMsg:
		return m.onRenameDone(msg)
	case batchReadyMsg:
		return m.onBatchReady(msg)
	case batchResultMsg:
		return m.onBatchResult(msg)
	case batchFinishedMsg:
		return m.onBatchFinished(msg)
	case batchModelsMsg:
		return m.onBatchModels(msg)
	case batchAppliedMsg:
		return m.onBatchApplied(msg)
	case deleteDoneMsg:
		return m.onDeleteDone(msg)
	case restoreDoneMsg:
		return m.onRestoreDone(msg)
	case relocateDoneMsg:
		return m.onRelocateDone(msg)
	case migrateDoneMsg:
		return m.onMigrateDone(msg)
	case indexRefreshedMsg:
		return m.onIndexRefreshed(msg)
	case contentIndexedMsg:
		return m.onContentIndexed(msg)
	case searchResultsMsg:
		return m.onSearchResults(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m.updateComponents(msg)
}

func (m modelState) onWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	// A probe that confirms the size already on screen is not a resize, and
	// repainting for it would flicker the list under the user's hands.
	if msg.Width == m.width && msg.Height == m.height {
		return m, nil
	}
	m.width, m.height = msg.Width, msg.Height
	m.layout()
	// Bubbletea only erases the tail of a line it believes is shorter than
	// the terminal, so a frame drawn for the old size can survive under the
	// new one: an old border column, a count from a row that has moved.
	// Clearing on resize costs one frame and makes the screen the only
	// thing on screen.
	return m, tea.ClearScreen
}

func (m modelState) onSessionsPage(msg sessionsPageMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.pageGen {
		return m, nil
	}
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.setSessionItems(msg.items)
	m.totalSessions = msg.total
	m.scopeProjects = msg.scopeProjects
	m.updateSourceCounts(msg.counts)
	m.layout()
	return m, nil
}

func (m modelState) onPreviewLoaded(msg previewLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.previewContent = msg.content
	m.overlay = overlayPreview
	m.preview.GotoTop()
	m.layout()
	return m, nil
}

func (m modelState) onArchiveDone(msg archiveDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	if msg.archived {
		summary := msg.summary
		m.lastArchived = &summary
		m.status = okStyle.Render(txt.archivedPrefix+truncateDisplay(msg.summary.Title, 48)) + mutedStyle.Render(txt.undoHint)
	} else {
		m.lastArchived = nil
		m.status = okStyle.Render(txt.unarchivedPrefix + truncateDisplay(msg.summary.Title, 48))
	}
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func (m modelState) onTitleSuggestion(msg titleSuggestionMsg) (tea.Model, tea.Cmd) {
	// A suggestion is only meaningful for the box that asked for it.
	if m.overlay != overlayRename || m.suggestFor == "" || m.suggestFor != msg.sessionID {
		return m, nil
	}
	m.suggesting = false
	switch {
	case msg.err != nil:
		m.suggestErr = suggestErrorText(msg.err)
	case msg.title == "":
		m.suggestErr = txt.noSuggestion
	default:
		m.suggestion = msg.title
	}
	return m, nil
}

func (m modelState) onRenameDone(msg renameDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.renameInput.Blur()
	m.clearSuggestion()
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.status = okStyle.Render(txt.renamedPrefix + truncateDisplay(msg.title, 56))
	if msg.caveat != nil {
		m.status += mutedStyle.Render("  ·  " + caveatText(msg.caveat))
	}
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func (m modelState) onBatchReady(msg batchReadyMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.batchGen {
		return m, nil
	}
	m.batchResults = append(m.batchResults, msg.frozen...)
	if len(msg.items) == 0 {
		m.finalizeBatch()
		return m, nil
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m.batchCancel = cancel
	m.batchTotal = len(m.batchResults) + len(msg.items)
	m.batchCh = titler.SuggestBatch(ctx, m.batchConfig(), msg.items, titler.DefaultConcurrency)
	m.batchRunning = true
	return m, tea.Batch(m.spinner.Tick, batchNextCmd(m.batchGen, m.batchCh))
}

func (m modelState) onBatchResult(msg batchResultMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.batchGen {
		return m, nil
	}
	m.batchResults = append(m.batchResults, msg.res)
	if m.batchCh != nil && len(m.batchResults) < m.batchTotal {
		return m, batchNextCmd(m.batchGen, m.batchCh)
	}
	m.finalizeBatch()
	return m, nil
}

func (m modelState) onBatchFinished(msg batchFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.batchGen || !m.batchRunning {
		return m, nil
	}
	m.finalizeBatch()
	return m, nil
}

func (m modelState) onBatchModels(msg batchModelsMsg) (tea.Model, tea.Cmd) {
	if m.overlay != overlayBatchTitle || msg.provider != m.batchConfig().Provider {
		return m, nil
	}
	m.batchModelLoading = false
	if msg.err != nil {
		// A CLI that cannot answer right now is not a dead end: the
		// overlay says why and falls back to typing a name.
		m.batchModelErr = listErrorText(msg.err)
		m.batchModelPicking = false
		m.batchModelEditing = true
		m.batchModelInput.Focus()
		return m, textinput.Blink
	}
	m.batchModelOpts = msg.models
	m.batchModelCursor = modelCursorFor(modelRowsFor(msg.models, ""), m.batchConfig().Model)
	return m, nil
}

func (m modelState) onBatchApplied(msg batchAppliedMsg) (tea.Model, tea.Cmd) {
	for _, id := range msg.appliedIDs {
		delete(m.marked, id)
	}
	m.overlay = overlayNone
	m.resetBatch()
	switch {
	case msg.applied > 0 && msg.failed > 0:
		detail := msg.detail
		if detail == "" {
			detail = txt.someRowsFailed
		}
		// Only the applied rows lose their mark, so ctrl+t reopens the
		// batch on exactly the rows that failed.
		m.err = fmt.Sprintf(txt.batchPartialFmt, msg.applied, msg.failed, detail)
	case msg.applied > 0:
		m.status = okStyle.Render(fmt.Sprintf(txt.batchRenamedFmt, msg.applied))
	case msg.failed > 0:
		detail := msg.detail
		if detail == "" {
			detail = txt.allRowsFailed
		}
		m.err = fmt.Sprintf(txt.batchAllFailedFmt, msg.failed, detail)
	default:
		m.status = txt.batchNoneApplied
	}
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func (m modelState) onDeleteDone(msg deleteDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.deleteChoice = 0
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	deleted := m.selected
	m.selected = nil
	m.lastResume = ""
	m.status = okStyle.Render(txt.deletedPrefix + truncateDisplay(msg.title, 48))
	if msg.restore != nil && deleted != nil {
		summary := deleted.summary
		m.lastDeleted = &summary
		m.restoreDeleted = msg.restore
		m.status += mutedStyle.Render(txt.undoDeleteHint)
	} else {
		m.lastDeleted = nil
		m.restoreDeleted = nil
	}
	m.sources = sourceChips(m.reg, msg.counts)
	if m.sourceIdx >= len(m.sources) {
		m.sourceIdx = 0
	}
	m.sourceList.SetItems(sourceItems(m.sources))
	m.sourceList.Select(m.sourceIdx)
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func (m modelState) onRestoreDone(msg restoreDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	// The offer is spent either way: a restore that failed will not start
	// working on a second press, and the reason belongs on screen.
	m.lastDeleted = nil
	m.restoreDeleted = nil
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.status = okStyle.Render(txt.restoredPrefix + truncateDisplay(msg.title, 48))
	if msg.counts != nil {
		m.sources = sourceChips(m.reg, msg.counts)
		if m.sourceIdx >= len(m.sources) {
			m.sourceIdx = 0
		}
		m.sourceList.SetItems(sourceItems(m.sources))
		m.sourceList.Select(m.sourceIdx)
	}
	var restoreCmd tea.Cmd
	m, restoreCmd = dispatchPageLoad(m)
	return m, restoreCmd
}

func (m modelState) onRelocateDone(msg relocateDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	m.relocateInput.Blur()
	if msg.err != nil && msg.resume == "" {
		m.err = msg.err.Error()
		return m, nil
	}
	verb := txt.forkedPrefix
	if msg.moved {
		verb = txt.movedPrefix
	}
	m.status = okStyle.Render(verb + truncateLeft(util.TildePath(msg.directory), 48))
	if msg.err != nil {
		m.status += mutedStyle.Render("  ·  " + msg.err.Error())
	}
	// The resume line points at the relocated session, which is the whole
	// point of the action: the next thing the user does is run it there.
	m.lastResume = msg.resume
	m.launchTarget = msg.providerID
	m.launchProject = msg.directory
	var cmd tea.Cmd
	m, cmd = dispatchPageLoad(m)
	return m, cmd
}

func (m modelState) onMigrateDone(msg migrateDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.overlay = overlayNone
	if msg.err != nil {
		m.err = msg.err.Error()
		m.lastResume = ""
		return m, nil
	}
	m.lastResume = msg.res.Resume
	m.launchTarget = msg.targetID
	if msg.res.Write != nil {
		m.launchProject = msg.res.Write.ProjectPath
	}
	target := msg.res.TargetName
	if target == "" {
		target = "target"
	}
	verb := txt.migratedPrefix
	if msg.res.AlreadyExists {
		verb = txt.alreadyPrefix
	}
	m.status = okStyle.Render(verb+target) + mutedStyle.Render(txt.copyHint)
	if len(msg.res.Warnings) > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(txt.warningsFmt, len(msg.res.Warnings)))
	}
	m.layout()
	return m, nil
}

func (m modelState) onIndexRefreshed(msg indexRefreshedMsg) (tea.Model, tea.Cmd) {
	m.indexing = false
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	if msg.project != nil {
		m.projectScope = *msg.project
	}
	m.updateSourceCounts(msg.counts)
	if msg.reloadPage {
		m.contentIndexing = true
		var cmd tea.Cmd
		m, cmd = dispatchPageLoad(m)
		return m, tea.Batch(cmd, contentIndexCmd(m.ctx, m.reg, m.idx))
	}
	m.status = fmt.Sprintf("Index updated (%d sessions)", msg.updated)
	m.layout()
	return m, nil
}

func (m modelState) onContentIndexed(msg contentIndexedMsg) (tea.Model, tea.Cmd) {
	m.contentIndexing = false
	if msg.err != nil && msg.err != context.Canceled {
		m.err = "content index: " + msg.err.Error()
	}
	// Index health is not news. Surface it only while work is outstanding.
	if msg.status.Pending > 0 {
		m.status = mutedStyle.Render(fmt.Sprintf("content indexing… %d pending", msg.status.Pending))
	}
	m.layout()
	if m.searchQuery != "" {
		return m, searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, m.searchQuery), providerCountOpts(m))
	}
	return m, nil
}

func (m modelState) onSearchResults(msg searchResultsMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.setSessionItems(msg.items)
	m.searchQuery = msg.query
	m.totalSessions = len(msg.items)
	m.updateSourceCounts(msg.counts)
	m.status = fmt.Sprintf("%d results for %q", len(msg.items), msg.query)
	if msg.status.Pending > 0 {
		m.status += mutedStyle.Render(fmt.Sprintf(" · %d sessions not indexed yet", msg.status.Pending))
	}
	m.layout()
	return m, nil
}

// updateKey dispatches a key press to the search box, the active overlay, or
// the session list, in that order of ownership.
func (m modelState) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		return m.updateSearching(msg)
	}
	if m.loading && !navigationKey(msg) {
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		return m, nil
	}
	if m.overlay != overlayNone {
		return m.updateOverlay(msg)
	}
	return m.updateList(msg)
}

// updateComponents forwards messages that are neither keys nor results, such
// as spinner ticks and cursor blinks, to whichever component owns the screen.
func (m modelState) updateComponents(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.loading || m.batchRunning {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	// The rename box animates and blinks at the same time. A tick belongs to
	// the suggestion spinner; everything else still reaches the text input, so
	// the cursor keeps blinking while the agent is being asked.
	if _, ok := msg.(spinner.TickMsg); ok && m.suggesting {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	switch m.overlay {
	case overlaySource:
		m.sourceList, cmd = m.sourceList.Update(msg)
	case overlayTarget:
		m.targets, cmd = m.targets.Update(msg)
	case overlayPreview:
		m.preview, cmd = m.preview.Update(msg)
	case overlayRename:
		m.renameInput, cmd = m.renameInput.Update(msg)
	case overlayRelocate:
		m.relocateInput, cmd = m.relocateInput.Update(msg)
	case overlayBatchTitle:
		// The batch overlay owns its keys; ticks and cursor blinks die here
		// instead of leaking into the session list underneath. The model
		// override field is the one part that needs its cursor to blink.
		if m.batchModelEditing {
			m.batchModelInput, cmd = m.batchModelInput.Update(msg)
			return m, cmd
		}
		return m, nil
	default:
		m.sessions, cmd = m.sessions.Update(msg)
	}
	return m, cmd
}

func (m modelState) updateSearching(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.searchInput.SetValue("")
			return dispatchPageLoadModel(m)
		}
		m.layout()
		return m, nil
	case "enter":
		query := strings.TrimSpace(m.searchInput.Value())
		if query == "" {
			return m, nil
		}
		m.searching = false
		m.searchInput.Blur()
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, query), providerCountOpts(m)))
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m modelState) updateOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayBatchTitle {
		return m.updateBatchOverlay(msg)
	}
	if m.overlay == overlayRename {
		switch msg.String() {
		case "ctrl+c", "q":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "esc":
			m.overlay = overlayNone
			m.renameInput.Blur()
			m.clearSuggestion()
			return m, tea.HideCursor
		case "tab":
			if m.suggestion == "" {
				return m, nil
			}
			m.renameInput.SetValue(m.suggestion)
			m.renameInput.CursorEnd()
			return m, nil
		case "enter":
			title := strings.TrimSpace(m.renameInput.Value())
			if title == "" {
				m.err = txt.titleEmpty
				return m, nil
			}
			if m.selected == nil {
				m.err = txt.noSessionSelected
				return m, nil
			}
			if title == strings.TrimSpace(m.selected.summary.Title) {
				m.overlay = overlayNone
				m.renameInput.Blur()
				m.clearSuggestion()
				return m, tea.HideCursor
			}
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick,
				renameSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary, title))
		}
		var cmd tea.Cmd
		m.renameInput, cmd = m.renameInput.Update(msg)
		return m, cmd
	}
	if m.overlay == overlayRelocate {
		switch msg.String() {
		case "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "esc":
			m.overlay = overlayNone
			m.relocateInput.Blur()
			return m, tea.HideCursor
		case "tab":
			// Fork and move differ only in whether the source survives, so
			// they share one box and one keystroke rather than two keys a
			// Shift apart.
			if m.relocateCanMove {
				m.relocateMove = !m.relocateMove
			}
			return m, nil
		case "enter":
			if m.selected == nil {
				m.err = txt.noSessionSelected
				return m, nil
			}
			directory, err := util.ResolveExistingDir(m.relocateInput.Value())
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			// Both sides are normalized: a stored path and a typed one can
			// name the same directory through different symlinks.
			if directory == util.NormalizeProjectPath(m.selected.summary.ProjectPath) {
				m.err = txt.relocateSameDirectory
				return m, nil
			}
			mode := provider.RelocateFork
			if m.relocateMove {
				mode = provider.RelocateMove
			}
			m.relocateInput.Blur()
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick,
				relocateSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary, directory, mode))
		}
		var cmd tea.Cmd
		m.relocateInput, cmd = m.relocateInput.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc":
		m.overlay = overlayNone
		m.err = ""
		m.layout()
		return m, nil
	case "left", "right":
		if m.overlay == overlayDelete {
			m.deleteChoice = 1 - m.deleteChoice
			return m, nil
		}
		if msg.String() == "left" && m.overlay == overlaySource {
			m.overlay = overlayNone
			m.layout()
			return m, nil
		}
		if msg.String() == "right" && m.overlay == overlaySource {
			return m.applySource()
		}
	case "enter":
		switch m.overlay {
		case overlaySource:
			return m.applySource()
		case overlayTarget:
			if m.selected != nil {
				if tgt, ok := m.targets.SelectedItem().(targetItem); ok {
					m.loading = true
					m.err = ""
					return m, tea.Batch(m.spinner.Tick,
						migrateCmd(m.ctx, m.engine, m.selected.summary, tgt.id, m.contextMode))
				}
			}
		case overlayDelete:
			if m.deleteChoice == 0 {
				m.overlay = overlayNone
				return m, nil
			}
			if m.selected != nil {
				m.loading = true
				m.err = ""
				return m, tea.Batch(m.spinner.Tick,
					deleteSessionCmd(m.ctx, m.reg, m.idx, m.selected.summary))
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	switch m.overlay {
	case overlaySource:
		m.sourceList, cmd = m.sourceList.Update(msg)
	case overlayTarget:
		m.targets, cmd = m.targets.Update(msg)
	default:
		m.preview, cmd = m.preview.Update(msg)
	}
	return m, cmd
}

func (m modelState) openCurrentSession() (tea.Model, tea.Cmd) {
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	p, err := m.reg.Get(it.summary.Provider)
	if err != nil {
		m.err = err.Error()
		return m, nil
	}
	if !p.SupportsResume() {
		m.err = fmt.Sprintf(txt.resumeUnsupportedFmt, p.DisplayName())
		return m, nil
	}
	command := p.ResumeCommand(provider.WriteResult{
		SessionID: it.summary.ID, StoragePath: it.summary.StoragePath, ProjectPath: it.summary.ProjectPath,
	})
	if command == "" {
		m.err = fmt.Sprintf(txt.noResumeCommandFmt, p.DisplayName())
		return m, nil
	}
	m.launch = command
	m.launchTarget = it.summary.Provider
	m.launchProject = it.summary.ProjectPath
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
}

func (m modelState) openTargetDrawer() (tea.Model, tea.Cmd) {
	if m.lastResume != "" {
		m.launch = m.lastResume
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}
	it, ok := m.sessions.SelectedItem().(sessionItem)
	if !ok {
		return m, nil
	}
	sel := it
	m.selected = &sel
	m.targets.SetItems(targetItems(m.reg, it.summary.Provider))
	m.targets.Select(0)
	m.overlay = overlayTarget
	m.layout()
	return m, tea.Batch(tea.HideCursor, tea.ClearScreen)
}

func (m modelState) applySource() (tea.Model, tea.Cmd) {
	chip, ok := m.sourceList.SelectedItem().(sourceChip)
	if !ok {
		return m, nil
	}
	for i := range m.sources {
		if m.sources[i].id == chip.id {
			m.sourceIdx = i
			break
		}
	}
	m.overlay = overlayNone
	m.searchQuery = ""
	m.searchInput.SetValue("")
	m.lastResume = ""
	m.status = ""
	return dispatchPageLoadModel(m)
}

func (m modelState) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "left":
		if len(m.sources) == 0 {
			return m, nil
		}
		m.sourceList.SetItems(sourceItems(m.sources))
		m.sourceList.Select(m.sourceIdx)
		m.overlay = overlaySource
		m.layout()
		return m, tea.Batch(tea.HideCursor, tea.ClearScreen)
	case "right":
		return m.openTargetDrawer()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.layout()
		return m, textinput.Blink
	case "esc":
		m.err = ""
		if m.lastResume != "" {
			m.lastResume = ""
			m.launchTarget = ""
			m.launchProject = ""
			m.status = ""
			return m, nil
		}
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.searchInput.SetValue("")
			return dispatchPageLoadModel(m)
		}
		m.lastArchived = nil
		m.lastDeleted = nil
		m.restoreDeleted = nil
		m.status = ""
		return m, nil
	case "u":
		// Undo the delete another just did. Unlike archive, this is not bound
		// to the key that caused it: ctrl+d again on a fresh row would arm a
		// second deletion instead of taking one back.
		if m.lastDeleted == nil || m.restoreDeleted == nil {
			return m, nil
		}
		summary, restore := *m.lastDeleted, m.restoreDeleted
		m.loading = true
		m.err = ""
		return m, tea.Batch(m.spinner.Tick, restoreSessionCmd(m.ctx, m.reg, m.idx, summary, restore))
	case "f":
		if m.cwd == "" {
			m.err = txt.projectUnknown
			return m, nil
		}
		m.projectOnly = !m.projectOnly
		m.applySessionDelegate()
		m.err = ""
		m.status = ""
		m.lastResume = ""
		if m.searchQuery != "" {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick,
				searchCmd(m.ctx, m.reg, m.idx, searchOptsFor(m, m.searchQuery), providerCountOpts(m)))
		}
		return dispatchPageLoadModel(m)
	case "g":
		// Grouping is a view of the page already loaded, not another query:
		// every session the list can show is in hand, so the bands appear
		// without a round trip and the cursor keeps the session it was on.
		m.grouped = !m.grouped
		selected, _ := m.sessions.SelectedItem().(sessionItem)
		m.sessions.SetItems(m.groupedItems())
		m.selectSession(selected.summary.ID)
		m.applySessionDelegate()
		return m, nil
	case "enter":
		// Enter is the default action on the current object: resume it in its
		// native agent. Crossing to another agent is spatially mapped to right.
		if m.lastResume != "" {
			m.launch = m.lastResume
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		return m.openCurrentSession()
	case " ":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			sel := it
			m.selected = &sel
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, loadPreviewCmd(m.ctx, m.reg, sel.summary))
		}
		return m, nil
	case "c":
		if m.lastResume != "" {
			_ = clipboard.WriteAll(m.lastResume)
			m.status = okStyle.Render(txt.resumeCopied)
		}
		return m, nil
	// Shift means one thing in this list: the same action over the whole page.
	// Archive and select-all used to sit on a and A, two unrelated actions one
	// Shift apart, with only one of them in the footer.
	case "a":
		if m.lastArchived != nil {
			summary := *m.lastArchived
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, summary, false))
		}
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotArchiveRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionArchiver); !ok {
				m.err = fmt.Sprintf(txt.archiveUnsupportedFm, p.DisplayName())
				return m, nil
			}
			m.loading = true
			m.err = ""
			return m, tea.Batch(m.spinner.Tick, archiveSessionCmd(m.ctx, m.reg, m.idx, it.summary, true))
		}
		return m, nil
	case "x":
		// x rather than space: space already opens the preview, which is the
		// primary browsing action and must keep it.
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if m.marked[it.summary.ID] {
				delete(m.marked, it.summary.ID)
			} else {
				m.marked[it.summary.ID] = true
			}
			m.status = m.markStatus()
		}
		return m, nil
	case "X":
		// Toggling on the whole visible page keeps one key for select-all and
		// clear-all, and it is x's own key with Shift: same verb, wider scope.
		items := m.sessions.Items()
		allMarked := len(items) > 0
		for _, li := range items {
			if it, ok := li.(sessionItem); ok && !m.marked[it.summary.ID] {
				allMarked = false
				break
			}
		}
		for _, li := range items {
			it, ok := li.(sessionItem)
			if !ok {
				continue
			}
			if allMarked {
				delete(m.marked, it.summary.ID)
			} else {
				m.marked[it.summary.ID] = true
			}
		}
		m.status = m.markStatus()
		return m, nil
	case "ctrl+r":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotRenameRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionRenamer); !ok {
				m.err = fmt.Sprintf(txt.renameUnsupportedFmt, p.DisplayName())
				return m, nil
			}
			sel := it
			m.selected = &sel
			m.renameInput.SetValue(it.summary.Title)
			m.renameInput.CursorEnd()
			m.renameInput.Focus()
			m.overlay = overlayRename
			m.layout()
			// The box opens on the original title immediately; the suggestion
			// lands later, or never, without ever blocking typing.
			m.suggestion, m.suggestErr, m.suggesting, m.suggestFor = "", "", false, ""
			if m.titleCfg.Enabled() {
				m.suggesting = true
				m.suggestFor = it.summary.ID
				return m, tea.Batch(textinput.Blink, m.spinner.Tick,
					suggestTitleCmd(m.ctx, m.reg, m.titleCfg, it.summary))
			}
			return m, textinput.Blink
		}
		return m, nil
	case "m":
		// Relocate is deliberately its own action rather than a migration to
		// the same agent: the provider moves or copies its own session, so
		// nothing is re-rendered and nothing is lost.
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotRelocateRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			relocator, ok := p.(provider.SessionRelocator)
			if !ok || !relocator.SupportsRelocate(provider.RelocateFork) {
				m.err = fmt.Sprintf(txt.relocateUnsupportedFmt, p.DisplayName())
				return m, nil
			}
			sel := it
			m.selected = &sel
			m.relocateMove = false
			m.relocateCanMove = relocator.SupportsRelocate(provider.RelocateMove)
			start := m.cwd
			if start == "" {
				start = it.summary.ProjectPath
			}
			m.relocateInput.SetValue(start)
			m.relocateInput.CursorEnd()
			m.relocateInput.Focus()
			m.overlay = overlayRelocate
			m.err = ""
			m.layout()
			return m, textinput.Blink
		}
		return m, nil
	case "ctrl+t":
		// The batch flow previews first and renames only on confirmation, so
		// opening it never spends a model call by itself.
		return m.startBatch()
	case "ctrl+d":
		if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
			if isCurrentSession(it.summary) {
				m.err = txt.cannotDeleteRunning
				return m, nil
			}
			p, err := m.reg.Get(it.summary.Provider)
			if err != nil {
				m.err = err.Error()
				return m, nil
			}
			if _, ok := p.(provider.SessionDeleter); !ok {
				m.err = fmt.Sprintf(txt.deleteUnsupportedFmt, p.DisplayName())
				return m, nil
			}
			sel := it
			m.selected = &sel
			m.deleteChoice = 0
			m.overlay = overlayDelete
			m.layout()
		}
		return m, nil
	case "r":
		m.indexing = true
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, refreshIndexCmd(m.ctx, m.reg, m.idx, m.sourceID(), m.cwd, true))
	}
	var cmd tea.Cmd
	m.sessions, cmd = m.sessions.Update(msg)
	// The list moved the cursor by rows and does not know a band is not one.
	m.skipGroupHeader(!headerSkipUpward(msg.String()))
	return m, cmd
}

// navigationKey stays live while a fetch is in flight. Swallowing arrows during
// a load makes a second press vanish, which reads as a dropped keystroke.
func navigationKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "up", "down", "left", "right", "pgup", "pgdown", "home", "end", "j", "k":
		return true
	}
	return false
}

// clearSuggestion drops suggestion state so a stale proposal cannot reappear
// over the next rename.
func (m *modelState) clearSuggestion() {
	m.suggesting = false
	m.suggestion = ""
	m.suggestErr = ""
	m.suggestFor = ""
}
