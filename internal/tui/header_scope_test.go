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
	m.projectScope.Root = "/Users/example/Documents/sync/Docs"
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
