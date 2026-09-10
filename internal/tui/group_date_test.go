package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/model"
)

// A band is a calendar day, not a duration. Ten past midnight and ten to
// midnight are twenty minutes apart and belong to different days, which is the
// distinction "yesterday" is asked about and a 24-hour window cannot make.
func TestDateBandsFollowTheCalendarNotTheClock(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 10, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want dateBand
	}{
		{"ten minutes ago, same day", now.Add(-10 * time.Minute), bandToday},
		{"twenty minutes ago, previous day", now.Add(-20 * time.Minute), bandYesterday},
		{"a clock running fast", now.Add(2 * time.Minute), bandToday},
		{"three days back", now.AddDate(0, 0, -3), bandThisWeek},
		{"a fortnight back", now.AddDate(0, 0, -14), bandThisMonth},
		{"a season back", now.AddDate(0, -3, 0), bandOlder},
		{"never recorded", time.Time{}, bandUnknown},
	} {
		if got := dateBandFor(tc.at, now); got != tc.want {
			t.Errorf("%s: band %d, want %d", tc.name, got, tc.want)
		}
	}
}

// The column answers what its band leaves open, so it never repeats the
// heading. Inside yesterday — a calendar day holding stamps both under and
// over 24 hours old — a duration would say nothing, so it gives the clock.
func TestCompactTimeAnswersWhatItsBandDoesNot(t *testing.T) {
	now := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), txt.compactNow},
		{now.Add(-7 * time.Minute), "7m"},
		{now.Add(-3 * time.Hour), "3h"},
		{time.Date(2026, 9, 9, 23, 40, 0, 0, time.UTC), "23:40"},
		{time.Date(2026, 9, 9, 13, 5, 0, 0, time.UTC), "13:05"},
		{now.AddDate(0, 0, -3), "Mon"},
		{time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC), "Aug 1"},
		{time.Date(2025, 8, 1, 9, 0, 0, 0, time.UTC), "Aug 25"},
	} {
		if got := compactTime(tc.at, now); got != tc.want {
			t.Errorf("compactTime(%s) = %q, want %q", tc.at.Format(time.RFC3339), got, tc.want)
		}
	}
}

// The point of the bands is the width they hand back: the heading carries the
// day, so the column stops carrying it too.
func TestDateBandsShrinkTheTimeColumn(t *testing.T) {
	now := time.Now()
	items := []list.Item{
		dateRow("a", now.Add(-7*time.Minute)),
		dateRow("b", now.Add(-3*time.Hour)),
		dateRow("c", now.AddDate(0, 0, -1)),
		dateRow("d", now.AddDate(0, 0, -40)),
	}
	flat := timeColumnWidth(items, false)
	banded := timeColumnWidth(items, true)
	if banded >= flat {
		t.Fatalf("date bands did not shrink the column: %d vs %d", banded, flat)
	}
	if banded > 5 {
		t.Errorf("the banded column is %d cells, wider than any stamp it holds", banded)
	}
}

// A date band says when, not where, so every row still owes its own directory.
// Only tree bands stand in for the path column.
func TestDateBandsKeepTheProjectColumn(t *testing.T) {
	m := sampleModel(t, 132, 32)
	m.groupMode = groupDate
	m.ungrouped = sampleSessions()
	m.sessions.SetItems(m.groupedItems())
	m.applySessionDelegate()
	m.layout()

	d := sessionDelegateFor(&m)
	if d.bands {
		t.Error("date bands were taken for tree bands")
	}
	if !d.dateBands {
		t.Fatal("the delegate does not know the bands are dates")
	}
	if !d.showProject {
		t.Fatal("date bands dropped the project column they do not replace")
	}
	if !strings.Contains(ansi.Strip(m.View()), "Docs/health") {
		t.Error("the rows lost their paths under date bands")
	}
}

// g walks off the end of the modes and back to a flat list rather than
// stopping on the last one.
func TestGroupKeyCyclesThroughEveryMode(t *testing.T) {
	m := sampleModel(t, 132, 32)
	m.ungrouped = sampleSessions()
	for _, want := range []int{groupDate, groupTree, groupNone, groupDate} {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
		m = updated.(modelState)
		if m.groupMode != want {
			t.Fatalf("g landed on mode %d, want %d", m.groupMode, want)
		}
		if m.status == "" {
			t.Error("g did not say which mode it landed on")
		}
	}
}

// Bands are labels. The cursor must not stop on one, whichever kind it is.
func TestDateBandsAreNotSelectable(t *testing.T) {
	m := sampleModel(t, 132, 32)
	m.groupMode = groupDate
	m.setSessionItems(sampleSessions())
	if _, isHeader := m.sessions.SelectedItem().(groupHeader); isHeader {
		t.Fatal("the cursor opened on a band")
	}
	for i := 0; i < len(m.sessions.Items())+2; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(modelState)
		if _, isHeader := m.sessions.SelectedItem().(groupHeader); isHeader {
			t.Fatalf("the cursor stopped on a band at step %d", i)
		}
	}
}

func dateRow(id string, at time.Time) list.Item {
	return sessionItem{summary: model.Summary{
		ID: id, Provider: "pi", Title: "A session", ProjectPath: "/tmp/project", UpdatedAt: at,
	}}
}
