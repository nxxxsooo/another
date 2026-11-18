package tui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

type startupRefreshProvider struct {
	provider.Provider
	rows []model.Summary
}

func (*startupRefreshProvider) ID() string          { return "test-startup" }
func (*startupRefreshProvider) DisplayName() string { return "Startup fixture" }
func (*startupRefreshProvider) Installed() bool     { return true }
func (p *startupRefreshProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return p.rows, nil
}

func TestStartupRescansEvenWhenCacheIsFresh(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	p := &startupRefreshProvider{}
	p.rows = []model.Summary{{ID: "previously-active", Provider: p.ID(), StoragePath: filepath.Join(dir, "session"), ProjectPath: dir}}
	reg := registry.NewWith(p)
	if _, err := index.UpdateIncremental(ctx, reg, idx, ""); err != nil {
		t.Fatal(err)
	}
	p.rows = nil // Archive outside another, after the last scan.
	if index.NeedsIncrementalIndex(reg, idx, 5*time.Minute) {
		t.Fatal("test setup did not produce a fresh cache")
	}
	m := layoutTestModel()
	m.ctx, m.reg, m.idx, m.scopeMode = ctx, reg, idx, scopeModeAll
	if initial := loadSessionsPageCmd(m, m.pageGen)().(sessionsPageMsg); len(initial.items) != 1 || initial.err != nil {
		t.Fatalf("cached startup page = %+v", initial)
	}
	// Init appends the native scan after the terminal and cache-read commands.
	cmds := m.Init()().(tea.BatchMsg)
	refreshed, ok := cmds[len(cmds)-1]().(indexRefreshedMsg)
	if !ok || refreshed.err != nil {
		t.Fatalf("startup did not rescan: %+v", refreshed)
	}
	_, cmd := m.Update(refreshed)
	if final, ok := firstMarkedMsg[sessionsPageMsg](t, cmd); !ok || len(final.items) != 0 || final.err != nil {
		t.Fatalf("reloaded startup page = %+v", final)
	}
}

func TestPartialRefreshStillReloadsPage(t *testing.T) {
	m := layoutTestModel()
	err := &index.ScanFailure{Errors: []error{errors.New("one provider failed")}}
	res, cmd := m.Update(indexRefreshedMsg{err: err, reloadPage: true})
	got := res.(modelState)
	if cmd == nil || got.pageGen != m.pageGen+1 || got.err != err.Error() {
		t.Fatalf("partial refresh did not warn and reload: generation=%d err=%q", got.pageGen, got.err)
	}
}

func TestCompletedRefreshClearsIndexing(t *testing.T) {
	m := modelState{indexing: true, reg: registry.New(), sourceList: newSourceList(nil)}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 3})
	if res.(modelState).indexing {
		t.Fatal("completed refresh msg must clear indexing")
	}
}

func TestRefreshReplacesDiscoveredProjectScope(t *testing.T) {
	m := modelState{reg: registry.New(), sourceList: newSourceList(nil),
		projectScope: util.ProjectScope{CWD: "/old", Root: "/old"}}
	want := util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true, Worktrees: []string{"/repo", "/tmp/feature"}}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, project: &want})
	got := res.(modelState).projectScope
	if got.Root != want.Root || len(got.Worktrees) != 2 || got.Worktrees[1] != want.Worktrees[1] {
		t.Fatalf("project scope = %+v, want %+v", got, want)
	}
}

// A refresh rebuilds the source row, so a stale selection index must not point
// past the end of the new chips.
func TestRefreshKeepsSourceSelectionInRange(t *testing.T) {
	m := modelState{
		reg:        registry.New(),
		sources:    []sourceChip{{id: ""}, {id: "codex"}, {id: "pi"}},
		sourceList: newSourceList(nil),
		sourceIdx:  2,
	}
	res, _ := m.Update(indexRefreshedMsg{counts: map[string]int{}, updated: 1})
	got := res.(modelState)
	if got.sourceIdx >= len(got.sources) {
		t.Fatalf("sourceIdx %d out of range for %d chips", got.sourceIdx, len(got.sources))
	}
}

func TestContentCompletionRefreshesActiveSearch(t *testing.T) {
	m := modelState{searchQuery: "needle", reg: registry.New()}
	_, cmd := m.Update(contentIndexedMsg{})
	if cmd == nil {
		t.Fatal("content completion did not refresh active search")
	}
}
