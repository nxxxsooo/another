package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/model"
)

// narrowModel is the browser on a phone-width terminal, filtered to one agent
// and banded by date: the screen the narrow layout was written for.
func narrowModel(t *testing.T, width int, title string) modelState {
	t.Helper()
	useLanguage(t, i18n.LangEnglish)
	m := layoutTestModel()
	m.status = ""
	m.sourceIdx = 1 // codex
	m.groupMode = groupDate
	now := time.Now()
	m.ungrouped = []list.Item{
		sessionItem{summary: model.Summary{
			ID: "narrow01", Provider: "codex", Title: title, ProjectPath: "/tmp/project",
			MessageCount: 220, UpdatedAt: now,
		}},
		sessionItem{summary: model.Summary{
			ID: "narrow02", Provider: "codex", Title: "1001｜Fix｜An older session", ProjectPath: "/tmp/project",
			MessageCount: 3, UpdatedAt: now.AddDate(0, 0, -20),
		}},
	}
	m.sessions.SetItems(m.groupedItems())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	m = updated.(modelState)
	if _, ok := m.sessions.Items()[0].(groupHeader); !ok {
		t.Fatal("the fixture is not banded by date")
	}
	m.sessions.Select(1)
	return m
}

const narrowTitle = "1019｜Explore｜API Scope for the billing export and its retries"

func TestNarrowRowsGiveTheTitleTheCells(t *testing.T) {
	wide := sessionDelegate{dateBands: true}.columns(46)
	narrow := sessionDelegate{dateBands: true, narrow: true, hideAgent: true}.columns(46)
	if narrow.provW != 0 || narrow.msgW != narrowCountWidth {
		t.Fatalf("narrow columns kept the agent or the unit: %+v", narrow)
	}
	if narrow.titleW < wide.titleW+10 {
		t.Fatalf("narrow title is %d cells, want at least %d", narrow.titleW, wide.titleW+10)
	}
	// With more than one agent on screen the chip is what tells rows apart.
	if mixed := (sessionDelegate{narrow: true}).columns(46); mixed.provW != agentChipWidth {
		t.Fatalf("narrow layout dropped the agent chip with every agent shown: %+v", mixed)
	}
}

func TestNarrowBrowserShowsTheTitleNotItsChrome(t *testing.T) {
	m := narrowModel(t, 48, narrowTitle)
	d := sessionDelegateFor(&m)
	if !d.narrow || !d.hideAgent {
		t.Fatalf("a 48-column single-agent browser is not narrow: %+v", d)
	}
	rows := strings.Join(sessionRows(m.View()), "\n")
	if strings.Contains(rows, "CDX") {
		t.Error("the agent chip repeats the header's single agent on every row")
	}
	if strings.Contains(rows, "1019") || !strings.Contains(rows, "Explore｜API Scope") {
		t.Errorf("the row did not drop the date prefix under date bands:\n%s", rows)
	}
	if strings.Contains(rows, "220 msg") || !strings.Contains(rows, "220") {
		t.Errorf("the row kept the message unit:\n%s", rows)
	}
}

func TestNarrowFooterSpellsOutTheSelectedTitle(t *testing.T) {
	long := narrowModel(t, 48, narrowTitle)
	footer := ansi.Strip(long.footerView())
	flat := strings.Join(strings.Fields(footer), " ")
	if !strings.Contains(flat, "1019｜Explore｜API Scope for the billing") {
		t.Errorf("the footer does not spell out the selected title:\n%s", footer)
	}
	short := narrowModel(t, 48, "Short")
	if got, want := lipgloss.Height(short.footerView()), lipgloss.Height(long.footerView()); got != want {
		t.Fatalf("footer is %d lines for a short title and %d for a long one", got, want)
	}
	if !strings.Contains(ansi.Strip(short.footerView()), "/tmp/project") {
		t.Error("a title that fits one line did not leave the second to its directory")
	}
	for _, line := range strings.Split(long.View(), "\n") {
		if w := ansi.StringWidth(line); w > 48 {
			t.Fatalf("line is %d cells on a 48-column terminal: %q", w, ansi.Strip(line))
		}
	}
}

// The breakpoint is the band, and nothing at or past it changes.
func TestNarrowLayoutStopsAtTheBreakpoint(t *testing.T) {
	for _, width := range []int{narrowBandWidth, 80, 132} {
		m := narrowModel(t, width, narrowTitle)
		d := sessionDelegateFor(&m)
		if d.narrow || d.hideAgent {
			t.Fatalf("width %d took the narrow layout", width)
		}
		rows := strings.Join(sessionRows(m.View()), "\n")
		if !strings.Contains(rows, "CDX") || !strings.Contains(rows, "1019") {
			t.Errorf("width %d changed the wide row:\n%s", width, rows)
		}
		if strings.Contains(ansi.Strip(m.footerView()), "API Scope") {
			t.Errorf("width %d moved the title into the footer", width)
		}
	}
	if m := narrowModel(t, narrowBandWidth-1, narrowTitle); !sessionDelegateFor(&m).narrow {
		t.Fatalf("width %d did not take the narrow layout", narrowBandWidth-1)
	}
}

func TestNarrowMessageCountFitsItsColumn(t *testing.T) {
	for _, n := range []int{1, 220, 9999, 10000, 123456, 99999999} {
		if got := narrowMessageCount(n); ansi.StringWidth(got) > narrowCountWidth {
			t.Errorf("%d spelled %q, wider than %d cells", n, got, narrowCountWidth)
		}
	}
}
