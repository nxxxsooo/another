package tui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/nxxxsooo/another/internal/i18n"
	"github.com/nxxxsooo/another/internal/model"
)

// sampleSessions is a page of sessions shaped like a real one: several agents,
// Chinese titles of very different lengths beside English ones, paths that
// share a long prefix, and message counts from one digit to three.
//
// It is a fixture rather than the developer's own index because a layout
// argument has to be reproducible. TestRenderProbe reads the real index and
// shows what this machine has today; this shows the same page to everyone, so
// two people can disagree about the same screen.
// The ages are relative to the moment the sample is rendered, not to a date
// written down here. FormatRelative reads the real clock, so a fixture pinned
// to a calendar date drifted every day it was not looked at: a row written as
// seven minutes old printed "13h ago" the next morning and "3d ago" the next
// week, and the page two people were meant to disagree about was a different
// page for each of them.
func sampleSessions() []list.Item {
	now := time.Now()
	rows := []struct {
		provider, title, path string
		messages              int
		ago                   time.Duration
	}{
		{"opencode2", "0824｜功能｜替尔泊肽漏针核对与日历调整", "/Users/someone/Documents/sync/Docs/health", 160, 7 * time.Minute},
		{"opencode2", "0821｜优化｜独立仓迁移与代码解耦", "/Users/someone/Documents/sync/Work/huatu/projects/smart-note", 213, 13 * time.Minute},
		{"opencode2", "0909｜探索｜两个Google账号领取免费Pro至明年9月", "/Users/someone/Documents/sync/Docs", 2, 48 * time.Minute},
		{"opencode2", "0909｜探索｜跟进Cecilia与Cici任务及杨亮PPT", "/Users/someone/Documents/sync/Work/fit/projects/fit-landing", 78, 2 * time.Hour},
		{"agy", "Subtle API 菜单栏监控工具", "/Users/someone/Documents/sync/Tuning/menubar-quota", 7, 12 * time.Hour},
		{"opencode2", "0908｜功能｜agentic worktree 续开发", "/Users/someone/Documents/sync/Work/fit/projects/fit-onboarding", 120, 12 * time.Hour},
		{"qwen", "0909｜研究｜Qwen3.8套餐与发布时间", "/Users/someone/Documents/sync", 20, 13 * time.Hour},
		{"pi", "0908｜设计｜插件Monorepo合并方案", "/Users/someone/Documents/sync/GitHub", 9, 23 * time.Hour},
		{"claude-code", "Explore", "/Users/someone/Documents/sync/Work/fit", 2, 25 * time.Hour},
		{"codex", "0906｜文档｜会议待办总结与任务规划", "/Users/someone/Documents/sync/Work/fit", 186, 40 * 24 * time.Hour},
	}
	items := make([]list.Item, 0, len(rows))
	for i, row := range rows {
		items = append(items, sessionItem{summary: model.Summary{
			ID:           fmt.Sprintf("sample%02d", i),
			Provider:     row.provider,
			Title:        row.title,
			ProjectPath:  row.path,
			MessageCount: row.messages,
			UpdatedAt:    now.Add(-row.ago),
		}})
	}
	return items
}

// sampleModel is the browser showing sampleSessions at one terminal size.
func sampleModel(t *testing.T, width, height int) modelState {
	t.Helper()
	m := layoutTestModel()
	m.totalSessions = 361
	m.status = ""
	m.sessions.SetItems(sampleSessions())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(modelState)
}

// sampleOverlays are the panels the sample can be asked to draw over the list.
// A modal is sized against the terminal it is drawn in, and its worst case is
// the smallest one it still has to fit inside, so it needs looking at the same
// way a row does.
var sampleOverlays = map[string]int{
	"none":     overlayNone,
	"help":     overlayHelp,
	"source":   overlaySource,
	"target":   overlayTarget,
	"delete":   overlayDelete,
	"relocate": overlayRelocate,
}

// widestBlankRun is how far the eye has to travel across nothing to get from
// one thing on a row to the next. It is the number the whole layout argument
// is about, so it is measured rather than described.
//
// The margins around the row are trimmed first. Space outside the columns is
// what a wide terminal is supposed to turn into; only space between them is
// the thing that made the list hard to read.
func widestBlankRun(line string) int {
	widest, run := 0, 0
	for _, r := range strings.TrimSpace(ansi.Strip(line)) {
		if r == ' ' {
			run++
			widest = max(widest, run)
			continue
		}
		run = 0
	}
	return widest
}

// sessionRows returns the rendered session rows, without the pane border, the
// header or the footer.
func sessionRows(view string) []string {
	var rows []string
	for _, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		first, last := strings.IndexAny(plain, "│┃"), strings.LastIndexAny(plain, "│┃")
		if first < 0 || last <= first {
			continue
		}
		inner := plain[first+len("│") : last]
		if strings.TrimSpace(inner) == "" {
			continue
		}
		rows = append(rows, inner)
	}
	return rows
}

// This is the whole complaint, as a number: a row on a very wide terminal must
// not have more emptiness inside it than the same row on an ordinary one.
//
// Before the columns were bounded, the title took every cell the other columns
// did not and then padded it, so the blank space inside each record grew one
// for one with the window — a hundred and thirty-six cells at 231 columns.
// A row still has padding, because columns line up and titles differ in
// length; what it must not have is padding that scales with the screen.
func TestBlankSpaceInsideARowStopsGrowingWithTheTerminal(t *testing.T) {
	baseline := 0
	for _, row := range sessionRows(sampleModel(t, contentBandFloor, 40).View()) {
		baseline = max(baseline, widestBlankRun(row))
	}
	for _, width := range []int{160, 200, 231, 240, 320, 400} {
		widest := 0
		var worst string
		for _, row := range sessionRows(sampleModel(t, width, 40).View()) {
			if got := widestBlankRun(row); got > widest {
				widest, worst = got, row
			}
		}
		if widest > baseline {
			t.Errorf("terminal %d: %d blank cells inside a row, worse than the %d at %d columns\n%s",
				width, widest, baseline, contentBandFloor, worst)
		}
	}
}

// The columns are measured from the rows that are loaded, so a page of short
// titles must not leave the hole a page of long ones fills. This is the case
// the measurement exists for: one narrow page in a very wide terminal.
func TestShortTitlesDoNotStretchTheTitleColumn(t *testing.T) {
	m := sampleModel(t, 240, 40)
	m.sessions.SetItems([]list.Item{
		sessionItem{summary: model.Summary{ID: "a", Provider: "pi", Title: "Explore", ProjectPath: "/tmp/project", MessageCount: 2}},
		sessionItem{summary: model.Summary{ID: "b", Provider: "pi", Title: "Test", ProjectPath: "/tmp/project", MessageCount: 3}},
	})
	m.layout()
	for _, row := range sessionRows(m.View()) {
		if got := widestBlankRun(row); got > 4*maxColumnGap {
			t.Fatalf("short titles left %d blank cells in a row:\n%s", got, row)
		}
	}
}

// Spacing between columns is bought first but bounded: past maxColumnGap a gap
// stops separating two columns and starts being a distance between them. It is
// checked on the arithmetic rather than on a rendered row, because a blank run
// in a row is a gap plus whatever padding the column beside it happens to
// carry, and the bound is a claim about the gap alone.
func TestColumnGapsStayWithinTheirBound(t *testing.T) {
	previous := 0
	for spare := 0; spare <= 500; spare++ {
		gap := columnGap(spare, 4)
		switch {
		case gap < 1:
			t.Fatalf("spare %d: a gap of %d lets two columns touch", spare, gap)
		case gap > maxColumnGap:
			t.Fatalf("spare %d: a %d-cell gap, past the bound of %d", spare, gap, maxColumnGap)
		case gap < previous:
			t.Fatalf("spare %d: gap shrank from %d to %d as the row got wider", spare, previous, gap)
		}
		previous = gap
	}
	if got := columnGap(500, 0); got != 1 {
		t.Fatalf("a row with no gaps to space returned %d, want the single cell", got)
	}
}

// TestLayoutSample prints the same page of sessions at a sweep of terminal
// widths. An alt-screen TUI cannot be captured through a pipe, and a layout
// judgement made at one width says nothing about another, so this is how a
// spacing decision gets looked at before it ships.
//
// Skipped unless LAYOUT_SAMPLE is set, because its output is a screen to read,
// not a result to assert.
//
//	LAYOUT_SAMPLE=1 go test ./internal/tui/ -run TestLayoutSample -v
//	LAYOUT_SAMPLE=1 LAYOUT_SAMPLE_WIDTHS=231 go test ./internal/tui/ -run TestLayoutSample -v
//	LAYOUT_SAMPLE=1 LAYOUT_SAMPLE_PERCENTS=70,80,90 go test ./internal/tui/ -run TestLayoutSample -v
//
// Comparing candidate settings is the point: LAYOUT_SAMPLE_PERCENTS renders the
// same page once per band proportion, so the choice is made by looking at the
// alternatives side by side rather than by arguing about a number.
func TestLayoutSample(t *testing.T) {
	if os.Getenv("LAYOUT_SAMPLE") == "" {
		t.Skip("set LAYOUT_SAMPLE=1 to print the sample layout")
	}
	// go test writes through a pipe, where lipgloss finds no terminal and
	// drops every color. A sample printed to review spacing has to force the
	// profile, or the chips it is meant to show arrive as plain text.
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)

	restoreBand, restoreGap := contentBandPercent, maxColumnGap
	defer func() { contentBandPercent, maxColumnGap = restoreBand, restoreGap }()

	// Column widths are measured per language — "128条" and "128 msg" are not
	// the same number of cells — so a layout reviewed in one language says
	// only that much about the other.
	if lang := os.Getenv("LAYOUT_SAMPLE_LANG"); lang != "" {
		defer applyLanguage(applyLanguage(i18n.Lang(lang)))
	}

	grouped := os.Getenv("LAYOUT_SAMPLE_GROUPED") != ""

	overlayName := os.Getenv("LAYOUT_SAMPLE_OVERLAY")
	if overlayName == "" {
		overlayName = "none"
	}
	which, ok := sampleOverlays[overlayName]
	if !ok {
		t.Fatalf("LAYOUT_SAMPLE_OVERLAY=%q is not one of %v", overlayName, sampleOverlayNames())
	}

	for _, percent := range sampleNumbers("LAYOUT_SAMPLE_PERCENTS", []int{contentBandPercent}) {
		contentBandPercent = percent
		for _, gap := range sampleNumbers("LAYOUT_SAMPLE_GAPS", []int{maxColumnGap}) {
			maxColumnGap = gap
			for _, width := range sampleNumbers("LAYOUT_SAMPLE_WIDTHS", []int{100, 132, 160, 200, 240}) {
				height := sampleNumbers("LAYOUT_SAMPLE_HEIGHT", []int{32})[0]
				m := sampleModel(t, width, height)
				if grouped {
					m.grouped = true
					m.ungrouped = sampleSessions()
					m.sessions.SetItems(m.groupedItems())
					m.layout()
				}
				if which != overlayNone {
					m = sampleWithOverlay(m, which)
				}
				widest := 0
				for _, row := range sessionRows(m.View()) {
					widest = max(widest, widestBlankRun(row))
				}
				view := m.View()
				fmt.Printf("\n%s\nterminal %dx%d · band %d · margin %d · gap %d · overlay %s · widest blank run in a row %d\n%s\n%s\n",
					strings.Repeat("=", width), width, height, m.bandWidth(), m.bandLeft(), gap, overlayName,
					widest, strings.Repeat("=", width), view)
			}
		}
	}
}

func sampleOverlayNames() []string {
	names := make([]string, 0, len(sampleOverlays))
	for name := range sampleOverlays {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sampleWithOverlay opens a panel over the sample list, filling in the state
// that panel reads so it draws the way it would in the running browser.
func sampleWithOverlay(m modelState, which int) modelState {
	if it, ok := m.sessions.SelectedItem().(sessionItem); ok {
		sel := it
		m.selected = &sel
		m.targets.SetItems(targetItems(m.reg, it.summary.Provider))
	}
	m.overlay = which
	m.layout()
	return m
}

// sampleNumbers reads a comma-separated list of widths or percentages from the
// environment, falling back to the sweep the sample is normally read at.
func sampleNumbers(key string, fallback []int) []int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	var out []int
	for _, field := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
