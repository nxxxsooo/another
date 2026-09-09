package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
	"github.com/nxxxsooo/another/internal/util"
)

type sessionsPageMsg struct {
	items  []list.Item
	total  int
	counts map[string]int
	// scopeProjects is how many directories the scope holds, counted without
	// the source filter so selecting an agent cannot make the directory
	// column appear or vanish under the reader.
	scopeProjects int
	provider      string
	gen           uint64
	err           error
}

type previewLoadedMsg struct {
	content string
	err     error
}

type migrateDoneMsg struct {
	res      *migrate.Result
	targetID string
	err      error
}

type deleteDoneMsg struct {
	providerID string
	title      string
	counts     map[string]int
	err        error
	// restore is non-nil only when the provider owns the session's bytes and
	// can put back the same session. It is offered for one keypress, then
	// dropped; another keeps no trash of its own.
	restore provider.SessionRestore
}

type restoreDoneMsg struct {
	title  string
	counts map[string]int
	err    error
}

type renameDoneMsg struct {
	providerID string
	title      string
	err        error
	// caveat carries a rename that landed but could not reach one of the
	// agent's surfaces, so the row still updates and the reason is still said.
	caveat error
}

// titleSuggestionMsg carries an AI-proposed title back to the rename overlay.
// sessionID is what makes a late arrival safe to drop: by the time a model
// answers, the user may have closed the box or moved to another row.
type titleSuggestionMsg struct {
	sessionID string
	title     string
	err       error
}

type archiveDoneMsg struct {
	summary  model.Summary
	archived bool
	err      error
}

type relocateDoneMsg struct {
	providerID string
	directory  string
	resume     string
	moved      bool
	err        error
}

type indexRefreshedMsg struct {
	counts     map[string]int
	project    *util.ProjectScope
	err        error
	updated    int
	reloadPage bool
}

type contentIndexedMsg struct {
	status index.ContentIndexStatus
	err    error
}

type searchResultsMsg struct {
	items  []list.Item
	query  string
	counts map[string]int
	status index.ContentIndexStatus
	err    error
}

func dispatchPageLoad(m modelState) (modelState, tea.Cmd) {
	m.pageGen++
	m.loading = true
	return m, tea.Batch(m.spinner.Tick, loadSessionsPageCmd(m, m.pageGen))
}

func listOptsFor(m modelState) index.ListOpts {
	opts := index.ListOpts{
		Provider:         m.sourceID(),
		Limit:            maxShowAllPage,
		IncludeSubagents: false,
	}
	if m.projectOnly {
		applyProjectScope(&opts, m.projectScope)
	}
	return opts
}

// resolveNestedRepos fills in the repositories a non-Git scope must not absorb.
// A Git scope already names its trees exactly, and a failed lookup leaves the
// scope as it was: showing a folder's whole subtree is the previous behaviour,
// not a reason to refuse to draw a list.
func resolveNestedRepos(idx *index.Store, scope *util.ProjectScope) {
	scope.Excluded = nil
	if scope == nil || scope.Git || scope.CWD == "" || idx == nil {
		return
	}
	paths, err := idx.ProjectPathsUnder(scope.CWD)
	if err != nil {
		return
	}
	scope.Excluded = util.NestedRepoRoots(scope.CWD, paths)
}

func applyProjectScope(opts *index.ListOpts, scope util.ProjectScope) {
	if scope.Git && len(scope.Worktrees) > 0 {
		opts.ProjectRoots = append([]string(nil), scope.Worktrees...)
		return
	}
	// Outside Git a directory means that directory and what is under it. An
	// exact match hid entire trees: opening another in ~/Documents/sync/Work/
	// huatu showed nothing at all while 79 sessions sat in its subfolders,
	// because agents record the directory they ran in, not its parent.
	//
	// Descendants that are their own repository are subtracted. Without that,
	// a folder holding several checkouts reports all of them as one project:
	// opening another in ~/Documents/sync claimed 476 sessions spanning a
	// dozen unrelated repos.
	if scope.CWD != "" {
		opts.ProjectRoots = []string{scope.CWD}
		opts.ExcludeRoots = append([]string(nil), scope.Excluded...)
	}
}

func providerCountOpts(m modelState) index.ListOpts {
	opts := listOptsFor(m)
	opts.Provider = ""
	opts.Limit = 0
	opts.Offset = 0
	return opts
}

func loadSessionsPageCmd(m modelState, gen uint64) tea.Cmd {
	idx := m.idx
	opts := listOptsFor(m)
	providerFilter := opts.Provider
	countOpts := providerCountOpts(m)
	return func() tea.Msg {
		total, err := idx.Count(opts)
		if err != nil {
			return sessionsPageMsg{err: err, provider: providerFilter, gen: gen}
		}
		summaries, lerr := idx.List(opts)
		if err == nil {
			err = lerr
		}
		counts, countErr := idx.CountByProviderFiltered(countOpts)
		if err == nil {
			err = countErr
		}
		// Counted from the scope rather than from this page: a page holds one
		// agent's sessions, and whether the project spans directories is not
		// that agent's business.
		projects, projErr := idx.DistinctProjects(opts)
		if err == nil {
			err = projErr
		}
		return sessionsPageMsg{items: sessionItems(summaries), total: total, counts: counts, scopeProjects: projects, provider: providerFilter, gen: gen, err: err}
	}
}

func backgroundIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store) tea.Cmd {
	return func() tea.Msg {
		n, err := index.UpdateIncremental(ctx, reg, idx, "")
		counts, _ := idx.CountByProvider()
		return indexRefreshedMsg{counts: counts, err: err, updated: n, reloadPage: true}
	}
}

func contentIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store) tea.Cmd {
	return func() tea.Msg {
		_, _, err := idx.IndexPendingContent(ctx, reg, 0, false)
		status, statusErr := idx.ContentStatus()
		if err == nil {
			err = statusErr
		}
		return contentIndexedMsg{status: status, err: err}
	}
}

func searchOptsFor(m modelState, query string) index.SearchOpts {
	opts := index.SearchOpts{Query: query, Provider: m.sourceID(), Limit: maxShowAllPage}
	if m.projectOnly {
		listOpts := index.ListOpts{}
		applyProjectScope(&listOpts, m.projectScope)
		opts.ProjectExact = listOpts.ProjectExact
		opts.ProjectRoots = listOpts.ProjectRoots
	}
	return opts
}

func searchCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, opts index.SearchOpts, countOpts index.ListOpts) tea.Cmd {
	return func() tea.Msg {
		hits, err := idx.Search(opts)
		status, statusErr := idx.ContentStatus()
		if err == nil {
			err = statusErr
		}
		counts, countErr := idx.CountByProviderFiltered(countOpts)
		if err == nil {
			err = countErr
		}
		summaries := make([]model.Summary, 0, len(hits))
		for _, hit := range hits {
			summaries = append(summaries, hit.Session)
		}
		items := sessionItems(summaries)
		for i, hit := range hits {
			row := items[i].(sessionItem)
			row.snippet = hit.Snippet
			items[i] = row
		}
		return searchResultsMsg{items: items, query: opts.Query, counts: counts, status: status, err: err}
	}
}

func refreshIndexCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, providerFilter, scopeCWD string, reloadPage bool) tea.Cmd {
	return func() tea.Msg {
		n, err := index.UpdateIncremental(ctx, reg, idx, providerFilter)
		counts, _ := idx.CountByProvider()
		var project *util.ProjectScope
		if scopeCWD != "" {
			discovered := util.DiscoverProjectScope(ctx, scopeCWD)
			// A refresh is where a checkout created since startup appears, so
			// the nested set is recomputed rather than carried over.
			resolveNestedRepos(idx, &discovered)
			project = &discovered
		}
		return indexRefreshedMsg{counts: counts, project: project, err: err, updated: n, reloadPage: reloadPage}
	}
}

// suggestTitleCmd asks the configured agent for one title. It runs off the UI
// thread and reports failures inline: a missing suggestion must never block or
// disturb the manual rename that is already on screen.
func suggestTitleCmd(ctx context.Context, reg *registry.Registry, cfg titler.Config, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return titleSuggestionMsg{sessionID: sm.ID, err: err}
		}
		ref := provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		var conv *model.Conversation
		if preview, ok := p.(provider.PreviewLoader); ok {
			conv, err = preview.LoadPreview(ctx, ref, 12)
		} else {
			conv, err = p.Load(ctx, ref)
		}
		if err != nil {
			return titleSuggestionMsg{sessionID: sm.ID, err: err}
		}
		title, err := titler.Suggest(ctx, cfg, titler.Request{
			Title:       sm.Title,
			ProjectPath: sm.ProjectPath,
			CreatedAt:   sm.CreatedAt,
			Messages:    conv.Messages,
		})
		return titleSuggestionMsg{sessionID: sm.ID, title: title, err: err}
	}
}

func loadPreviewCmd(ctx context.Context, reg *registry.Registry, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return previewLoadedMsg{err: err}
		}
		ref := provider.SessionRef{ID: sm.ID, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		var conv *model.Conversation
		if preview, ok := p.(provider.PreviewLoader); ok {
			conv, err = preview.LoadPreview(ctx, ref, 40)
		} else {
			conv, err = p.Load(ctx, ref)
		}
		if err != nil {
			return previewLoadedMsg{err: err}
		}
		var b strings.Builder
		b.WriteString(titleStyle.Render(conv.Title) + "\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%s · %s · %d messages\n\n",
			registry.DisplayName(reg, conv.Provider), util.FormatRelative(conv.UpdatedAt), len(conv.Messages))))
		for _, msg := range conv.Messages {
			role := accentStyle.Render(string(msg.Role))
			text := msg.PlainText()
			if msg.Role == model.RoleUser {
				text = util.DisplayUserText(text)
			}
			b.WriteString(role + "\n" + text + "\n\n")
		}
		return previewLoadedMsg{content: b.String()}
	}
}

func archiveSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, archived bool) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return archiveDoneMsg{summary: sm, archived: archived, err: err}
		}
		archiver, ok := p.(provider.SessionArchiver)
		if !ok {
			return archiveDoneMsg{summary: sm, archived: archived, err: fmt.Errorf("%s does not support archive", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		if err := archiver.ArchiveSession(ctx, ref, archived); err != nil {
			return archiveDoneMsg{summary: sm, archived: archived, err: err}
		}
		_, err = index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		if err != nil {
			action := "archived"
			if !archived {
				action = "unarchived"
			}
			err = fmt.Errorf("session %s, but index refresh failed: %w", action, err)
		}
		return archiveDoneMsg{summary: sm, archived: archived, err: err}
	}
}

func renameSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, title string) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
		}
		renamer, ok := p.(provider.SessionRenamer)
		if !ok {
			return renameDoneMsg{providerID: sm.Provider, title: title, err: fmt.Errorf("%s does not support rename", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		// A caveat is not a failure: the agent's own store has the new title,
		// and some surface it also reads does not. Saying "rename failed" would
		// send the person to redo a rename that already happened.
		var caveat error
		if err := renamer.RenameSession(ctx, ref, title); err != nil {
			if !errors.Is(err, provider.ErrPartial) {
				return renameDoneMsg{providerID: sm.Provider, title: title, err: err}
			}
			caveat = err
		}
		if _, err := index.UpdateIncremental(ctx, reg, idx, sm.Provider); err != nil {
			return renameDoneMsg{providerID: sm.Provider, title: title,
				err: fmt.Errorf("session renamed, but index refresh failed: %w", err)}
		}
		return renameDoneMsg{providerID: sm.Provider, title: title, caveat: caveat}
	}
}

func deleteSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary) tea.Cmd {
	return func() tea.Msg {
		p, err := reg.Get(sm.Provider)
		if err != nil {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: err}
		}
		deleter, ok := p.(provider.SessionDeleter)
		if !ok {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: fmt.Errorf("%s does not support deletion", p.DisplayName())}
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		// Prefer the reversible path where the provider owns the session's
		// bytes. Where it does not, the delete stays exactly as final as the
		// confirmation said it was.
		var restore provider.SessionRestore
		if reversible, ok := p.(provider.ReversibleSessionDeleter); ok {
			restore, err = reversible.DeleteSessionReversibly(ctx, ref)
			if err != nil {
				return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: err}
			}
		} else if err := deleter.DeleteSession(ctx, ref); err != nil {
			return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, err: err}
		}
		_, err = index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		counts, _ := idx.CountByProvider()
		if err != nil {
			err = fmt.Errorf("session deleted, but index refresh failed: %w", err)
		}
		return deleteDoneMsg{providerID: sm.Provider, title: sm.Title, counts: counts, err: err, restore: restore}
	}
}

// restoreSessionCmd undoes the delete another just performed. The restore was
// captured before the deletion, so this writes the agent's own session back
// rather than re-rendering it through the portable model.
func restoreSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, restore provider.SessionRestore) tea.Cmd {
	return func() tea.Msg {
		if restore == nil {
			return restoreDoneMsg{title: sm.Title, err: fmt.Errorf("nothing to restore")}
		}
		if err := restore(ctx); err != nil {
			return restoreDoneMsg{title: sm.Title, err: err}
		}
		_, err := index.UpdateIncremental(ctx, reg, idx, sm.Provider)
		counts, _ := idx.CountByProvider()
		if err != nil {
			err = fmt.Errorf("session restored, but index refresh failed: %w", err)
		}
		return restoreDoneMsg{title: sm.Title, counts: counts, err: err}
	}
}

func relocateSessionCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, sm model.Summary, directory string, mode provider.RelocateMode) tea.Cmd {
	return func() tea.Msg {
		done := relocateDoneMsg{providerID: sm.Provider, directory: directory, moved: mode == provider.RelocateMove}
		p, err := reg.Get(sm.Provider)
		if err != nil {
			done.err = err
			return done
		}
		relocator, ok := p.(provider.SessionRelocator)
		if !ok || !relocator.SupportsRelocate(mode) {
			done.err = fmt.Errorf(txt.relocateUnsupportedFmt, p.DisplayName())
			return done
		}
		res, err := relocator.RelocateSession(ctx, provider.SessionRef{
			ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath,
		}, provider.RelocateOpts{Directory: directory, Mode: mode})
		if err != nil {
			done.err = err
			return done
		}
		done.resume = p.ResumeCommand(provider.WriteResult{
			SessionID: res.SessionID, StoragePath: res.StoragePath, ProjectPath: res.ProjectPath,
		})
		if _, err := index.UpdateIncremental(ctx, reg, idx, sm.Provider); err != nil {
			// The session really did move. Saying so and naming the stale
			// index is more useful than reporting a failure that did not
			// happen.
			done.err = fmt.Errorf("relocated, but index refresh failed: %w", err)
		}
		return done
	}
}

func migrateCmd(ctx context.Context, engine *migrate.Engine, sm model.Summary, to string, contextMode migrate.ContextMode) tea.Cmd {
	return func() tea.Msg {
		res, err := engine.Run(ctx, migrate.Options{
			SessionID: sm.ID, FromProvider: sm.Provider, ToProvider: to,
			ContextMode: contextMode,
		})
		return migrateDoneMsg{res: res, targetID: to, err: err}
	}
}

func dispatchPageLoadModel(m modelState) (tea.Model, tea.Cmd) {
	m, cmd := dispatchPageLoad(m)
	return m, cmd
}
