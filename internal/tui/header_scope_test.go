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
			m.projectOnly = true
			project := column()
			m.projectOnly = false
			if all := column(); all != project {
				t.Errorf("%s width %d: target moved from %d to %d", lang, width, project, all)
			}
		}
		applyLanguage(previous)
	}
}
