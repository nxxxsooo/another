package tui

import (
	"testing"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/util"
)

// A remembered "project" scope still means the directory this browser opened
// in: the mode is derived fresh, so the same remembered value is exact under a
// repository and a tree under a plain directory.
func TestLastScopeProjectFollowsTheOpeningDirectory(t *testing.T) {
	last := &config.LastView{Scope: "project"}
	git := util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true}
	if got := lastScope(last, git); got != scopeModeExact {
		t.Fatalf("lastScope(project, git) = %v, want exact", got)
	}
	plain := util.ProjectScope{CWD: "/plain", Root: "/plain"}
	if got := lastScope(last, plain); got != scopeModeTree {
		t.Fatalf("lastScope(project, plain) = %v, want tree", got)
	}
}

// A directory another does not know has no project to be narrow about, so a
// remembered "project" cannot leave the browser on an empty scope.
func TestLastScopeProjectFallsBackToAllWithoutADirectory(t *testing.T) {
	last := &config.LastView{Scope: "project"}
	if got := lastScope(last, util.ProjectScope{}); got != scopeModeAll {
		t.Fatalf("lastScope(project, unknown dir) = %v, want all", got)
	}
}

// An absent view, or a scope this version does not know, leaves the opening
// scope to be derived from the directory, exactly as before this existed.
func TestLastScopeFallsBackToOpeningScope(t *testing.T) {
	git := util.ProjectScope{CWD: "/repo", Root: "/repo", Git: true}
	if got := lastScope(nil, git); got != openingScope(git) {
		t.Fatalf("lastScope(nil) = %v, want openingScope", got)
	}
	if got := lastScope(&config.LastView{Scope: "sideways"}, git); got != openingScope(git) {
		t.Fatalf("lastScope(unknown) = %v, want openingScope", got)
	}
}

// The window is written even when it is zero, because zero is the no-window
// stop on the t key; only the presence of a view says a window was ever
// chosen. ui.recent_days seeds the first launch instead.
func TestLastRecentDays(t *testing.T) {
	if got := lastRecentDays(&config.LastView{RecentDays: 0}, config.UI{RecentDays: 30}); got != 0 {
		t.Fatalf("remembered no-window = %d, want 0", got)
	}
	if got := lastRecentDays(&config.LastView{RecentDays: 7}, config.UI{}); got != 7 {
		t.Fatalf("remembered 7 = %d, want 7", got)
	}
	if got := lastRecentDays(nil, config.UI{RecentDays: 14}); got != 14 {
		t.Fatalf("unremembered = %d, want ui.recent_days 14", got)
	}
	if got := lastRecentDays(nil, config.UI{}); got != defaultRecentDays {
		t.Fatalf("unremembered default = %d, want %d", got, defaultRecentDays)
	}
}

// Grouping is clamped to the modes this version draws, so a value written by a
// future version cannot leave the list with no bands for it.
func TestLastGroupClampsToKnownModes(t *testing.T) {
	if got := lastGroup(nil); got != groupNone {
		t.Fatalf("lastGroup(nil) = %d, want off", got)
	}
	if got := lastGroup(&config.LastView{Group: groupDate}); got != groupDate {
		t.Fatalf("lastGroup(date) = %d, want date", got)
	}
	if got := lastGroup(&config.LastView{Group: groupModes + 1}); got != groupNone {
		t.Fatalf("lastGroup(out of range) = %d, want off", got)
	}
}

// The agent filter is matched by ID rather than index, so a provider added,
// removed, or reordered since does not move the filter to a different agent.
func TestLastSourceMatchesByID(t *testing.T) {
	sources := []sourceChip{{id: ""}, {id: "codex"}, {id: "pi"}}
	if got := lastSource(sources, &config.LastView{Source: "pi"}); got != 2 {
		t.Fatalf("lastSource(pi) = %d, want 2", got)
	}
	if got := lastSource(sources, &config.LastView{Source: "gone"}); got != 0 {
		t.Fatalf("lastSource(missing) = %d, want the all chip", got)
	}
	if got := lastSource(sources, nil); got != 0 {
		t.Fatalf("lastSource(nil) = %d, want the all chip", got)
	}
}

// The scope is stored as a direction, not a resolved mode: both narrower modes
// round-trip as "project".
func TestLastScopeName(t *testing.T) {
	if got := lastScopeName(scopeModeAll); got != "all" {
		t.Fatalf("lastScopeName(all) = %q", got)
	}
	for _, mode := range []scopeMode{scopeModeExact, scopeModeTree} {
		if got := lastScopeName(mode); got != "project" {
			t.Fatalf("lastScopeName(%v) = %q, want project", mode, got)
		}
	}
}

// Closing the browser records the view it closed with, over the rest of the
// config, so the next launch can open the same way.
func TestSaveLastViewRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveSettings(config.Settings{EnabledProviders: []string{"pi", "codex"}}); err != nil {
		t.Fatal(err)
	}
	m := modelState{
		sources:   []sourceChip{{id: ""}, {id: "codex"}},
		sourceIdx: 1, scopeMode: scopeModeTree, recentDays: 7, groupMode: groupDate,
	}
	saveLastView(m)

	got, err := config.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	want := &config.LastView{Source: "codex", Scope: "project", RecentDays: 7, Group: groupDate}
	if got.UI.Last == nil || *got.UI.Last != *want {
		t.Fatalf("saved view = %+v, want %+v", got.UI.Last, want)
	}
	if len(got.EnabledProviders) != 2 {
		t.Fatalf("saveLastView clobbered the rest of the config: %+v", got.EnabledProviders)
	}
}

// The restored agent filter has to survive the first page load: the fetch that
// opens the browser rebuilds the source row from fresh counts, and the
// selection must follow the provider rather than a stale index. This is the
// path a person takes every launch, so it is tested end to end here.
func TestRestoredSourceSurvivesTheFirstPageLoad(t *testing.T) {
	reg := registry.New()
	sources := sourceChips(reg, map[string]int{"codex": 3})
	m := modelState{reg: reg, sources: sources, sourceList: newSourceList(sourceItems(sources))}
	m.sourceIdx = lastSource(sources, &config.LastView{Source: "codex"})
	if got := m.sourceID(); got != "codex" {
		t.Fatalf("restored source = %q, want codex", got)
	}
	m.updateSourceCounts(map[string]int{"codex": 3, "pi": 1})
	if got := m.sourceID(); got != "codex" {
		t.Fatalf("after the first page load, source = %q, want codex", got)
	}
}

// A person who has never saved settings has nothing to write to, and losing
// the browser's exit over that is worse than forgetting the view.
func TestSaveLastViewIsBestEffortWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveLastView(modelState{sources: []sourceChip{{id: ""}}})
	if config.SettingsExist() {
		t.Fatal("saveLastView created a config from nothing")
	}
}
