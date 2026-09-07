package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
)

var errListing = errors.New("pi 获取模型超时")

func TestSetupRenderProbe(t *testing.T) {
	if os.Getenv("RENDER_PROBE") == "" {
		t.Skip("set RENDER_PROBE=1 to print setup")
	}
	m := setupFixture()
	m.width, m.height = 88, 24
	fmt.Println("======== setup")
	fmt.Println(m.View())
}

func setupFixture() setupModel {
	return setupModel{
		items: []setupItem{
			{id: "pi", name: "pi", command: "pi", data: true, cli: true, available: true, sessions: 12},
			{id: "codex", name: "Codex", command: "codex", data: true, cli: true, available: true, sessions: 20},
			{id: "cursor", name: "Cursor", command: "cursor-agent", available: false, adapter: true},
		},
		selected:   map[string]bool{"pi": true},
		modelInput: textinput.New(),
		width:      80, height: 24,
	}
}

// onlyPIOnPath makes agent CLI detection deterministic: exactly one agent can
// generate titles, whatever the machine running the test has installed.
func onlyPIOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// titlePageFixture is the setup model as it looks after page one is accepted.
func titlePageFixture(t *testing.T) setupModel {
	t.Helper()
	onlyPIOnPath(t)
	m := setupFixture()
	m.selected = map[string]bool{"pi": true, "codex": true}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(setupModel)
}

func TestSetupSpaceTogglesAvailableAgent(t *testing.T) {
	m := setupFixture()
	m.cursor = 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	if !updated.(setupModel).selected["codex"] {
		t.Fatal("space did not select available agent")
	}
}

func TestFirstSetupRequiresManualAgentSelection(t *testing.T) {
	if selected := initialSetupSelection(nil); len(selected) != 0 {
		t.Fatalf("first setup preselected agents: %v", selected)
	}
	selected := initialSetupSelection([]string{"o2", "QWEN"})
	if !selected["opencode2"] || !selected["qwen"] || len(selected) != 2 {
		t.Fatalf("existing setup selection was not preserved: %v", selected)
	}
}

func TestSetupShiftArrowsReorderAgentsWithoutChangingSelection(t *testing.T) {
	m := setupFixture()
	m.cursor = 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	got := updated.(setupModel)
	if got.cursor != 0 || got.items[0].id != "codex" || got.items[1].id != "pi" {
		t.Fatalf("shift+up did not move the row: cursor=%d items=%+v", got.cursor, got.items)
	}
	if !got.selected["pi"] || got.selected["codex"] {
		t.Fatalf("reordering changed selection: %+v", got.selected)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	got = updated.(setupModel)
	if got.cursor != 1 || got.items[0].id != "pi" || got.items[1].id != "codex" {
		t.Fatalf("shift+down did not move the row back: cursor=%d items=%+v", got.cursor, got.items)
	}
}

func TestSetupShiftArrowsStopAtTheEnds(t *testing.T) {
	m := setupFixture()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	got := updated.(setupModel)
	if got.cursor != 0 || got.items[0].id != "pi" {
		t.Fatalf("shift+up wrapped the first row: cursor=%d items=%+v", got.cursor, got.items)
	}
	// The last row of the first tier: below it is the fold, which nothing may
	// be dragged across.
	got.cursor = 1
	last := got.items[1].id
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	got = updated.(setupModel)
	if got.cursor != 1 || got.items[1].id != last {
		t.Fatalf("shift+down crossed the fold: cursor=%d items=%+v", got.cursor, got.items)
	}
}

func TestSetupRejectsUnavailableAgent(t *testing.T) {
	m := setupFixture()
	m.showAdapters = true
	m.cursor = 3
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := updated.(setupModel)
	if got.selected["cursor"] || !strings.Contains(got.err, "未检测到") {
		t.Fatalf("unavailable agent selection = %+v", got)
	}
}

// Ten agents in one list buries the six another actually tests. The second
// tier is still reachable — hiding a provider outright would strand anyone
// already using it — but it costs one keystroke instead of four rows.
func TestSetupFoldsCompatibilityAdaptersUntilAsked(t *testing.T) {
	m := setupFixture()
	view := ansi.Strip(m.View())
	if strings.Contains(view, "Cursor") {
		t.Fatalf("a second-tier agent is on the page before the fold is opened:\n%s", view)
	}
	if !strings.Contains(view, "其他 1 个兼容适配") {
		t.Fatalf("the page does not say what it is holding back:\n%s", view)
	}

	// The fold is the last row, and space acts on the row under the cursor.
	m.cursor = len(m.rows()) - 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	opened := updated.(setupModel)
	if !opened.showAdapters || !strings.Contains(ansi.Strip(opened.View()), "Cursor") {
		t.Fatalf("space on the fold did not open the second tier:\n%s", opened.View())
	}
	if len(opened.selected) != len(m.selected) {
		t.Fatalf("opening the fold changed the selection: %v", opened.selected)
	}

	closed, _ := opened.Update(tea.KeyMsg{Type: tea.KeySpace})
	if strings.Contains(ansi.Strip(closed.(setupModel).View()), "Cursor") {
		t.Fatal("space on the fold did not close it again")
	}
}

// A setting that cannot be seen cannot be turned off, so an already enabled
// second-tier agent opens the fold on the way in.
func TestSetupOpensTheFoldForAnAlreadyEnabledAdapter(t *testing.T) {
	items := setupFixture().items
	if !anyAdapterSelected(items, map[string]bool{"cursor": true}) {
		t.Fatal("an enabled adapter did not open the fold")
	}
	if anyAdapterSelected(items, map[string]bool{"pi": true}) {
		t.Fatal("a first-tier selection opened the fold")
	}
}

func TestSetupEnterOpensTitlePageThenCompletes(t *testing.T) {
	got := titlePageFixture(t)
	if got.page != setupPageTitle || got.done {
		t.Fatalf("first enter should open the title page: page=%d done=%v", got.page, got.done)
	}
	final, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	done := final.(setupModel)
	if !done.done || done.cancelled || cmd == nil {
		t.Fatalf("setup did not finish: done=%v cancelled=%v cmd=%v", done.done, done.cancelled, cmd)
	}
}

func TestSetupTitlePageOffersOnlyInstalledCapableAgents(t *testing.T) {
	got := titlePageFixture(t)
	if len(got.titleOpts) != 2 {
		t.Fatalf("title options = %+v, want off + pi", got.titleOpts)
	}
	if got.titleOpts[0].id != "" || got.titleOpts[1].id != "pi" {
		t.Fatalf("title options = %+v", got.titleOpts)
	}
	if got.titleCursor != 0 || got.titleModel() != nil {
		t.Fatalf("title suggestions must default to off: cursor=%d model=%+v", got.titleCursor, got.titleModel())
	}
}

// modelPageFixture selects pi on the title page and lands on the model picker
// with a listing already delivered, without running any CLI.
func modelPageFixture(t *testing.T) setupModel {
	t.Helper()
	m := titlePageFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(setupModel)

	opened, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = opened.(setupModel)
	if m.page != setupPageModel {
		t.Fatalf("enter did not open the model page: page=%d", m.page)
	}
	if !m.modelLoading || cmd == nil {
		t.Fatalf("model page did not ask the CLI for a listing: loading=%v cmd=%v", m.modelLoading, cmd)
	}
	loaded, _ := m.Update(modelsLoadedMsg{
		provider: "pi",
		models:   []string{"anthropic/claude-sonnet-4-5", "anthropic/claude-haiku-4-5", "google/gemini-3-pro"},
	})
	return loaded.(setupModel)
}

// The model is chosen from what the agent CLI itself reports, so a typo can no
// longer reach the rename path as a model name.
func TestSetupModelPagePicksFromTheCliListing(t *testing.T) {
	m := modelPageFixture(t)
	if m.modelLoading {
		t.Fatal("listing arrived but the page still shows loading")
	}
	if cfg := m.titleModel(); cfg == nil || cfg.Model != "" {
		t.Fatalf("the picker must start on the CLI default: %+v", cfg)
	}
	if body := m.modelPageBody(72); !strings.Contains(body, "默认模型") || !strings.Contains(body, "claude-sonnet-4-5") {
		t.Fatalf("model page does not list what the CLI reported:\n%s", body)
	}

	down, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = down.(setupModel)
	if cfg := m.titleModel(); cfg == nil || cfg.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("cursor did not select the first listed model: %+v", cfg)
	}

	final, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	done := final.(setupModel)
	if !done.done || cmd == nil {
		t.Fatalf("enter did not finish setup: done=%v cmd=%v", done.done, cmd)
	}
	if cfg := done.titleModel(); cfg.Provider != "pi" || cfg.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("saved title model = %+v", cfg)
	}
}

// A long catalog is only usable if it can be narrowed down.
func TestSetupModelPageFiltersByTyping(t *testing.T) {
	m := modelPageFixture(t)
	for _, r := range "haiku" {
		typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = typed.(setupModel)
	}
	rows := m.modelRows()
	if len(rows) != 2 || rows[1] != "anthropic/claude-haiku-4-5" {
		t.Fatalf("filter did not narrow the list: %v", rows)
	}
	down, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cfg := down.(setupModel).titleModel(); cfg.Model != "anthropic/claude-haiku-4-5" {
		t.Fatalf("filtered selection = %+v", cfg)
	}
	back, _ := down.(setupModel).Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := back.(setupModel).modelFilter; got != "haik" {
		t.Fatalf("backspace did not edit the filter: %q", got)
	}
}

// A model too new to be listed, or a CLI that cannot list at all, still has to
// be reachable.
func TestSetupModelPageKeepsACustomName(t *testing.T) {
	m := modelPageFixture(t)
	m.modelCursor = m.customRow()
	typing, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = typing.(setupModel)
	if !m.modelTyping {
		t.Fatal("the custom row must open the input")
	}
	for _, r := range "some-new-model" {
		typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = typed.(setupModel)
	}
	final, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	done := final.(setupModel)
	if !done.done {
		t.Fatal("enter did not finish setup from the custom row")
	}
	if cfg := done.titleModel(); cfg.Model != "some-new-model" {
		t.Fatalf("custom model = %+v", cfg)
	}
}

// A CLI that cannot list must say so and let a name be typed, not dead-end.
func TestSetupModelPageFallsBackWhenListingFails(t *testing.T) {
	m := modelPageFixture(t)
	m.modelOpts = nil
	failed, _ := m.Update(modelsLoadedMsg{provider: "pi", err: errListing})
	got := failed.(setupModel)
	if !got.modelTyping || got.modelLoading {
		t.Fatalf("a failed listing must fall back to typing: typing=%v loading=%v", got.modelTyping, got.modelLoading)
	}
	if body := got.modelPageBody(72); !strings.Contains(body, "pi 获取模型超时") {
		t.Fatalf("the page must say why the listing failed:\n%s", body)
	}
}

// A listing that arrives after the agent changed must not populate the page.
func TestSetupModelPageIgnoresAStaleListing(t *testing.T) {
	m := modelPageFixture(t)
	stale, _ := m.Update(modelsLoadedMsg{provider: "codex", models: []string{"gpt-5"}})
	if rows := stale.(setupModel).modelRows(); len(rows) != 4 {
		t.Fatalf("a listing for another agent landed on the page: %v", rows)
	}
}

// An English session renamed into Chinese is harder to find again, so the
// language has to be a setting rather than a constant in the prompt.
func TestSetupTitlePageRecordsLanguage(t *testing.T) {
	m := titlePageFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(setupModel)

	if cfg := m.titleModel(); cfg == nil || cfg.Language != string(titler.LangAuto) {
		t.Fatalf("default language = %+v, want auto", cfg)
	}
	if body := m.titlePageBody(72, 64); !strings.Contains(body, "中文") || !strings.Contains(body, "Auto") {
		t.Fatalf("the title page must show the language choices:\n%s", body)
	}

	right, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = right.(setupModel)
	if cfg := m.titleModel(); cfg == nil || cfg.Language != string(titler.LangEnglish) {
		t.Fatalf("→ did not select English: %+v", cfg)
	}
	right, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = right.(setupModel)
	if cfg := m.titleModel(); cfg == nil || cfg.Language != string(titler.LangChinese) {
		t.Fatalf("→ did not select Chinese: %+v", cfg)
	}
	// The arrows must not leak into the model name.
	if m.modelInput.Value() != "" {
		t.Fatalf("language keys typed into the model field: %q", m.modelInput.Value())
	}
	left, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if cfg := left.(setupModel).titleModel(); cfg == nil || cfg.Language != string(titler.LangEnglish) {
		t.Fatalf("← did not go back to English: %+v", cfg)
	}
}

// Re-running setup must not silently rewrite a language that was already
// chosen.
func TestSetupRestoresSavedLanguage(t *testing.T) {
	start := setupFixture()
	start.langCursor = languageCursor(titler.Language("auto"))
	if got := start.language(); got != titler.LangAuto {
		t.Fatalf("saved language = %q, want auto", got)
	}
	if got := languageCursor(titler.Language("")); got != 0 {
		t.Fatalf("an unset language must fall back to Auto, got cursor %d", got)
	}
}

func TestSetupTitlePageIgnoresTypingWhileDisabled(t *testing.T) {
	m := titlePageFixture(t)
	typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := typed.(setupModel)
	if got.modelInput.Value() != "" || got.titleModel() != nil {
		t.Fatalf("typing while off leaked into config: %q %+v", got.modelInput.Value(), got.titleModel())
	}
}

func TestSetupTitlePageEscGoesBackWithoutCancelling(t *testing.T) {
	m := titlePageFixture(t)
	back, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := back.(setupModel)
	if got.page != setupPageAgents || got.cancelled || got.done {
		t.Fatalf("esc on title page = page:%d cancelled:%v done:%v", got.page, got.cancelled, got.done)
	}
}

func TestSetupTitlePageViewFitsTerminal(t *testing.T) {
	m := titlePageFixture(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(setupModel)
	for _, size := range [][2]int{{48, 20}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%dx%d rendered %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}

func TestSetupRequiresOneAgent(t *testing.T) {
	m := setupFixture()
	m.selected = map[string]bool{}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(setupModel)
	if got.done || cmd != nil || !strings.Contains(got.err, "至少") {
		t.Fatalf("empty setup was accepted: %+v", got)
	}
}

func TestSetupTooSmallView(t *testing.T) {
	m := setupFixture()
	m.width, m.height = 47, 19
	if view := m.View(); !strings.Contains(view, "Terminal too small") {
		t.Fatalf("small view = %q", view)
	}
}

func TestSetupExplainsToggleAndSortControls(t *testing.T) {
	view := setupFixture().View()
	for _, want := range []string{"space 开关", "shift+↑↓ 排序"} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup does not explain %q: %q", want, view)
		}
	}
}

func TestSetupViewFitsTerminal(t *testing.T) {
	m := setupFixture()
	for _, size := range [][2]int{{48, 20}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("%dx%d rendered %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
	}
}

// setupWithEveryAgent is the page as a first run actually sees it: every agent
// another supports, which is what made the panel outgrow the terminal.
func setupWithEveryAgent() setupModel {
	reg := registry.NewOrdered(nil)
	var items []setupItem
	for _, p := range reg.All() {
		items = append(items, setupItem{
			id: p.ID(), name: p.DisplayName(), command: registry.CLICommand(p.ID()),
			data: true, cli: true, available: true, sessions: 116,
			adapter: registry.IsCompatibilityAdapter(p.ID()),
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return !items[i].adapter && items[j].adapter })
	return setupModel{items: items, selected: map[string]bool{"pi": true}, modelInput: textinput.New()}
}

// The panel is centred, so a body one line too tall does not clip: the terminal
// scrolls, and from then on every frame lands lower than the one before it —
// which is how the same agent ends up drawn twice with two cursors.
func TestSetupNeverOutgrowsTheTerminal(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for h := 20; h <= 60; h++ {
			for _, w := range []int{48, 60, 80, 100, 140, 200} {
				for _, page := range []int{setupPageAgents, setupPageTitle} {
					m := setupWithEveryAgent()
					m.showAdapters = folded
					m.width, m.height, m.page = w, h, page
					if page == setupPageTitle {
						m.titleOpts = titleOptions(m.items, map[string]bool{})
						for _, item := range m.items {
							m.titleOpts = append(m.titleOpts, titleOption{id: item.id, name: item.name, command: item.command})
						}
					}
					if rows := len(m.rows()); rows > 0 {
						m.cursor = min(rows-1, 10)
					}
					if got := lipgloss.Height(m.View()); got > h {
						t.Fatalf("page %d at %dx%d fold=%v: setup renders %d lines", page, w, h, folded, got)
					}
				}
			}
		}
	}
}

// Every row has to survive its own panel: a row cut to the panel width wraps
// inside the padding, and a wrapped row is a line the page never budgeted for.
func TestSetupRowsStayOnOneLine(t *testing.T) {
	m := setupWithEveryAgent()
	m.showAdapters = true
	for _, w := range []int{48, 56, 72, 92, 120, 200} {
		m.width, m.height = w, 40
		view := ansi.Strip(m.View())
		for _, line := range strings.Split(view, "\n") {
			if strings.Count(line, "○")+strings.Count(line, "●") > 1 {
				t.Fatalf("width %d: two agents share a line: %q", w, line)
			}
		}
		if got := strings.Count(view, "○") + strings.Count(view, "●"); got != len(m.items) {
			t.Fatalf("width %d: %d of %d agent rows drawn", w, got, len(m.items))
		}
	}
}
