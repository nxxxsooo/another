package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/i18n"
)

func TestScopeToggleKeepsTargetAtTheSameColumn(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		previous := applyLanguage(lang)
		for width := 40; width <= 260; width++ {
			m := sampleModel(t, width, 24)
			m.projectScope.Root = "/Users/example/Documents/sync/Docs"
			column := func() int {
				header := ansi.Strip(m.headerView())
				at := strings.Index(header, ansi.Strip(txt.targetArrow))
				if at < 0 {
					t.Fatalf("missing target: %q", header)
				}
				return ansi.StringWidth(header[:at])
			}
			m.scopeMode = scopeModeExact
			project := column()
			m.scopeMode = scopeModeTree
			if tree := column(); tree != project {
				t.Errorf("%s width %d: target moved from %d to %d in tree", lang, width, project, tree)
			}
			m.scopeMode = scopeModeAll
			if all := column(); all != project {
				t.Errorf("%s width %d: target moved from %d to %d in all", lang, width, project, all)
			}
		}
		applyLanguage(previous)
	}
}

func TestAllProjectsHeaderOmitsPathTail(t *testing.T) {
	m := sampleModel(t, 160, 24)
	// Short enough to fit beside the window chip, which is always drawn.
	m.projectScope.Root = "/srv/Docs"
	m.scopeMode = scopeModeAll
	header := ansi.Strip(m.headerView())
	if strings.Contains(header, "Docs") {
		t.Fatalf("global all projects header should not contain root path tail: %q", header)
	}
	if !strings.Contains(header, txt.scopeAll) {
		t.Fatalf("global all projects header missing %q: %q", txt.scopeAll, header)
	}

	// But exact mode should contain root path tail when wide enough
	m.scopeMode = scopeModeExact
	exactHeader := ansi.Strip(m.headerView())
	if !strings.Contains(exactHeader, "Docs") {
		t.Fatalf("exact scope header should contain root path tail when room allows: %q", exactHeader)
	}
}

// t steps the window through 90, 30, 7 days and everything. The header keeps
// one shape through all four: "all time" holds the chip's place, padded to
// the widest window, so nothing after it moves and the line never changes
// length at any terminal width.
func TestWindowKeyKeepsTheHeaderStill(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.LangEnglish, i18n.LangChinese} {
		previous := applyLanguage(lang)
		for width := 40; width <= 260; width++ {
			m := sampleModel(t, width, 24)
			m.projectScope.Root = "/Users/example/Documents/sync/Docs"
			m.scopeMode = scopeModeExact
			var want string
			for i, days := range recentWindowPresets {
				m.recentDays = days
				header := ansi.Strip(m.headerView())
				if i == 0 {
					want = header
				}
				if ansi.StringWidth(header) != ansi.StringWidth(want) ||
					strings.Index(header, ansi.Strip(txt.targetArrow)) != strings.Index(want, ansi.Strip(txt.targetArrow)) {
					t.Fatalf("%s width %d: the %d-day header moved against the 90-day one:\n%q\n%q",
						lang, width, days, header, want)
				}
				if days == 0 && width >= 120 && !strings.Contains(header, txt.recentWindowAll) {
					t.Errorf("%s width %d: no window shows no placeholder: %q", lang, width, header)
				}
			}
		}
		applyLanguage(previous)
	}
}
