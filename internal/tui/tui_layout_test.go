package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/migrate"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/providers/pi"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
)

func layoutTestModel() modelState {
	sm := model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project", MessageCount: 12}
	item := sessionItem{summary: sm, providerLbl: "Codex"}
	marked := map[string]bool{}
	return modelState{
		reg:      registry.New(),
		marked:   marked,
		sessions: newSessionList([]list.Item{item}, marked),
		targets:  newTargetList([]list.Item{targetItem{id: "claude-code", name: "Claude Code"}}),
		sources: []sourceChip{
			{id: "", name: "all", count: 3},
			{id: "codex", name: "Codex", count: 2},
			{id: "pi", name: "pi", count: 1},
		},
		sourceList: newSourceList([]list.Item{
			sourceChip{id: "", name: "all", count: 3},
			sourceChip{id: "codex", name: "Codex", count: 2},
			sourceChip{id: "pi", name: "pi", count: 1},
		}),
		preview: viewport.New(1, 1), searchInput: textinput.New(), renameInput: textinput.New(), selected: &item,
		previewContent: strings.Repeat("full preview content ", 100), status: "ready",
	}
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		for _, ov := range []int{overlayNone, overlaySource, overlayTarget, overlayPreview, overlayDelete, overlayBatchTitle} {
			m := layoutTestModel()
			m.overlay = ov
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := updated.(modelState).View()
			if got := lipgloss.Width(view); got > size[0] {
				t.Errorf("%dx%d overlay %d width = %d", size[0], size[1], ov, got)
			}
			if got := lipgloss.Height(view); got > size[1] {
				t.Errorf("%dx%d overlay %d height = %d", size[0], size[1], ov, got)
			}
		}
	}
}

func TestNarrowPreviewAndResizePreserveContent(t *testing.T) {
	m := layoutTestModel()
	m.overlay = overlayPreview
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	m = updated.(modelState)
	if !strings.Contains(m.preview.View(), "full preview content") {
		t.Fatal("narrow preview lost raw content")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(modelState)
	if !strings.Contains(m.preview.View(), "full preview content") {
		t.Fatal("resized preview lost raw content")
	}
}

func TestTooSmallView(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 39, 11
	if got := m.View(); !strings.Contains(got, "Terminal too small") {
		t.Fatalf("view = %q", got)
	}
}

// The session list is the whole screen, so a row must stay on one line however
// long the title is — otherwise the visible session count halves.
func TestSessionRowStaysOneLine(t *testing.T) {
	m := layoutTestModel()
	long := strings.Repeat("很长的中文标题", 20)
	m.sessions.SetItems([]list.Item{sessionItem{
		summary:     model.Summary{ID: "x", Provider: "pi", Title: long},
		providerLbl: "pi",
	}})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(modelState)
	for _, line := range strings.Split(m.sessions.View(), "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("row wider than terminal: %d", lipgloss.Width(line))
		}
	}
	if n := len(strings.Split(strings.TrimRight(m.sessions.View(), "\n"), "\n")); n > m.sessions.Height() {
		t.Fatalf("one item rendered %d lines", n)
	}
}

func TestLeftOpensSourceDrawerAndAppliesSelection(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(modelState)
	if m.overlay != overlaySource || cmd == nil {
		t.Fatalf("left did not open source drawer and hide the cursor: overlay=%d cmd=%v", m.overlay, cmd)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(modelState)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	if m.overlay != overlayNone || m.sourceIdx != 1 || cmd == nil {
		t.Fatalf("right did not apply source: overlay=%d idx=%d cmd=%v", m.overlay, m.sourceIdx, cmd)
	}
}

func TestSearchKeyFocusesInput(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(modelState)
	if !m.searching || !m.searchInput.Focused() {
		t.Fatal("/ did not focus search")
	}
}

func TestEscapeClearsAppliedSearch(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.searchQuery = "needle"
	m.searchInput.SetValue("needle")
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(modelState)
	if m.searchQuery != "" || m.searchInput.Value() != "" || cmd == nil {
		t.Fatalf("escape did not clear search: query=%q input=%q cmd=%v", m.searchQuery, m.searchInput.Value(), cmd)
	}
}

func TestRightOpensTargetOverlay(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := updated.(modelState).overlay; got != overlayTarget {
		t.Fatalf("right did not open target overlay: overlay=%d", got)
	}
}

func TestEnterDirectlyResumesSourceSession(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(modelState)
	if cmd == nil || got.launchTarget != "codex" || !strings.Contains(got.launch, "codex resume") {
		t.Fatalf("enter did not launch source session: target=%q launch=%q cmd=%v", got.launchTarget, got.launch, cmd)
	}
	if got.overlay != overlayNone {
		t.Fatalf("enter opened an overlay instead of resuming: %d", got.overlay)
	}
}

func TestPickersUseStableCenteredModals(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	base := m.View()

	m.overlay = overlaySource
	source := m.View()
	m.overlay = overlayTarget
	target := m.View()
	if !strings.Contains(source, "选择来源") || !strings.Contains(target, "选择去向") {
		t.Fatal("pickers lost their purpose labels")
	}
	if source == base || target == base || source == target {
		t.Fatal("source and target pickers must be distinct overlays")
	}
	for _, view := range []string{source, target} {
		var modalLine string
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "选择来源") || strings.Contains(line, "选择去向") {
				modalLine = line
				break
			}
		}
		if lipgloss.Width(modalLine) < 40 {
			t.Fatalf("picker is still a tiny drifting tooltip: %q", modalLine)
		}
	}
}

func TestProviderColumnPrecedesTitle(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := updated.(modelState).sessions.View()
	if strings.Index(view, "Codex") >= strings.Index(view, "A useful title") {
		t.Fatalf("source provider still trails the title: %q", view)
	}
}

func TestMessageCountHasUnit(t *testing.T) {
	m := layoutTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := updated.(modelState).sessions.View()
	if !strings.Contains(view, "12条") {
		t.Fatalf("message count is still a bare number: %q", view)
	}
}

func TestTargetOverlayEscapeReturns(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(modelState)
	if m.overlay != overlayTarget {
		t.Fatalf("enter did not open the target overlay: overlay=%d", m.overlay)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := updated.(modelState).overlay; got != overlayNone {
		t.Fatalf("escape did not close the overlay: overlay=%d", got)
	}
}

// Picking a target runs the migration directly. The engine verifies its own
// write and rolls back, so a second confirmation only added a keystroke.
func TestEnterOnTargetStartsMigration(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.overlay = overlayTarget
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a target did not start a migration")
	}
	if !updated.(modelState).loading {
		t.Fatal("migration did not enter the loading state")
	}
}

func TestArchiveOffersOneStepUndo(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("A did not start native archive")
	}
	summary := m.sessions.SelectedItem().(sessionItem).summary
	updated, _ = m.Update(archiveDoneMsg{summary: summary, archived: true})
	got := updated.(modelState)
	if got.lastArchived == nil || !strings.Contains(got.help(), "撤销") {
		t.Fatalf("archive has no one-step undo: %+v", got.lastArchived)
	}
}

func TestCtrlROpensPrefilledNativeRename(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if got.overlay != overlayRename || got.renameInput.Value() != "A useful title" || !got.renameInput.Focused() || cmd == nil {
		t.Fatalf("ctrl+r did not open prefilled rename: overlay=%d value=%q focused=%v cmd=%v", got.overlay, got.renameInput.Value(), got.renameInput.Focused(), cmd)
	}
}

func TestCtrlRWithoutConfiguredAgentAsksForNoSuggestion(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if got.suggesting || got.suggestFor != "" {
		t.Fatalf("unconfigured another must not call a model: suggesting=%v for=%q", got.suggesting, got.suggestFor)
	}
	if strings.Contains(got.View(), "AI 建议") {
		t.Fatal("suggestion row shown while the feature is off")
	}
}

func TestSuggestedTitleIsAcceptedWithTab(t *testing.T) {
	m := layoutTestModel()
	m.titleCfg = titler.Config{Provider: "pi"}
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := updated.(modelState)
	if !got.suggesting || got.suggestFor != "session" {
		t.Fatalf("ctrl+r did not request a suggestion: %+v", got.suggestFor)
	}
	if got.renameInput.Value() != "A useful title" {
		t.Fatal("the box must open on the original title, not wait for a model")
	}

	arrived, _ := got.Update(titleSuggestionMsg{sessionID: "session", title: "0903｜修复｜删除条目快捷键冲突"})
	got = arrived.(modelState)
	if got.suggesting || got.suggestion == "" || !strings.Contains(got.help(), "tab") {
		t.Fatalf("suggestion not offered: %+v", got.suggestion)
	}
	if got.renameInput.Value() != "A useful title" {
		t.Fatal("a suggestion must not overwrite the field on its own")
	}

	accepted, _ := got.Update(tea.KeyMsg{Type: tea.KeyTab})
	if value := accepted.(modelState).renameInput.Value(); value != "0903｜修复｜删除条目快捷键冲突" {
		t.Fatalf("tab did not accept the suggestion: %q", value)
	}
}

func TestStaleAndFailedSuggestionsStayOutOfTheWay(t *testing.T) {
	m := layoutTestModel()
	m.titleCfg = titler.Config{Provider: "pi"}
	m.width, m.height = 100, 30
	m.layout()
	opened, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	got := opened.(modelState)

	other, _ := got.Update(titleSuggestionMsg{sessionID: "a-different-session", title: "0903｜功能｜别的会话"})
	if s := other.(modelState); s.suggestion != "" {
		t.Fatalf("a suggestion for another session was shown: %q", s.suggestion)
	}

	failed, _ := got.Update(titleSuggestionMsg{sessionID: "session", err: errors.New("pi 未登录")})
	fail := failed.(modelState)
	if fail.err != "" {
		t.Fatalf("a failed suggestion leaked into the main error line: %q", fail.err)
	}
	if fail.suggesting || !strings.Contains(fail.View(), "建议不可用") {
		t.Fatal("a failed suggestion is not reported in the rename box")
	}

	closed, _ := fail.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s := closed.(modelState); s.suggestErr != "" || s.suggestFor != "" {
		t.Fatalf("suggestion state survived the overlay: %+v", s.suggestErr)
	}
}

func TestRenameOverlayWithSuggestionFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}} {
		m := layoutTestModel()
		m.overlay = overlayRename
		m.suggestion = "0903｜修复｜删除条目快捷键冲突"
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := updated.(modelState).View()
		if got := lipgloss.Width(view); got > size[0] {
			t.Errorf("%dx%d width = %d", size[0], size[1], got)
		}
		if got := lipgloss.Height(view); got > size[1] {
			t.Errorf("%dx%d height = %d", size[0], size[1], got)
		}
	}
}

func TestCtrlDOpensDeleteWithCancelSelected(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(modelState)
	if got.overlay != overlayDelete || got.deleteChoice != 0 {
		t.Fatalf("ctrl+d did not open safe delete state: overlay=%d choice=%d", got.overlay, got.deleteChoice)
	}
	if !strings.Contains(got.View(), "删除会话？") || !strings.Contains(got.View(), "默认") && !strings.Contains(got.View(), "取消") {
		t.Fatal("delete confirmation does not make cancellation visible")
	}
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(modelState).overlay != overlayNone || cmd != nil {
		t.Fatal("enter on the default cancel choice must not delete")
	}
}

func TestDeleteRequiresExplicitChoice(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.overlay = overlayDelete
	m.deleteChoice = 0
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	got := updated.(modelState)
	if got.deleteChoice != 1 {
		t.Fatal("right did not move focus to delete")
	}
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !updated.(modelState).loading {
		t.Fatal("explicit delete choice did not start deletion")
	}
}

func TestDeleteRemovesPiSourceAndIndexRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_AGENT_DIR", root)
	p := pi.New()
	write, err := p.Write(context.Background(), &model.Conversation{
		ID: "source", Provider: "codex", ProjectPath: "/tmp/delete-fixture", Title: "delete me",
		Messages: []model.Message{{Role: model.RoleUser, Content: "temporary"}},
	}, provider.WriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if _, err := index.UpdateIncremental(context.Background(), reg, idx, "pi"); err != nil {
		t.Fatal(err)
	}
	if n, _ := idx.Count(index.ListOpts{Provider: "pi"}); n != 1 {
		t.Fatalf("fixture was not indexed: count=%d", n)
	}

	cmd := deleteSessionCmd(context.Background(), reg, idx, model.Summary{
		ID: write.SessionID, Provider: "pi", ProjectPath: write.ProjectPath, StoragePath: write.StoragePath, Title: "delete me",
	})
	msg := cmd().(deleteDoneMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, err := os.Stat(write.StoragePath); !os.IsNotExist(err) {
		t.Fatalf("source file still exists: %v", err)
	}
	if n, _ := idx.Count(index.ListOpts{Provider: "pi"}); n != 0 {
		t.Fatalf("index still contains deleted session: count=%d", n)
	}
}

func TestAgyConversationEnvironmentMarksCurrentSession(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "agy-current")
	if !isCurrentSession(model.Summary{ID: "agy-current", Provider: "agy"}) {
		t.Fatal("active Antigravity conversation was not recognized")
	}
	if isCurrentSession(model.Summary{ID: "other", Provider: "agy"}) {
		t.Fatal("unrelated Antigravity conversation was marked current")
	}
}

func TestCurrentPiSessionCannotBeDeleted(t *testing.T) {
	m := layoutTestModel()
	item := m.sessions.SelectedItem().(sessionItem)
	item.summary.Provider = "pi"
	item.summary.ID = "live-pi"
	item.summary.StoragePath = "/tmp/live-pi.jsonl"
	m.sessions.SetItems([]list.Item{item})
	t.Setenv("PI_SESSION_ID", "live-pi")
	t.Setenv("PI_SESSION_FILE", "/tmp/live-pi.jsonl")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := updated.(modelState)
	if got.overlay == overlayDelete || !strings.Contains(got.err, "当前") {
		t.Fatalf("current session was not protected: overlay=%d err=%q", got.overlay, got.err)
	}
}

func TestClaudeProjectTrustedReadsExactProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"projects":{"` + project + `":{"hasTrustDialogAccepted":true},"/other":{"hasTrustDialogAccepted":false}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if !claudeProjectTrusted(path, project) {
		t.Fatal("trusted project was not recognized")
	}
	if claudeProjectTrusted(path, "/other") {
		t.Fatal("untrusted project was reported trusted")
	}
	if claudeProjectTrusted(filepath.Join(t.TempDir(), "missing"), project) {
		t.Fatal("missing config must be untrusted")
	}
}

func TestMigrationResultRetainsLaunchIdentity(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	updated, _ := m.Update(migrateDoneMsg{
		targetID: "claude-code",
		res: &migrate.Result{
			Resume:     "cd '/tmp/project' && claude --resume 'abc'",
			TargetName: "Claude Code",
			Write:      &provider.WriteResult{ProjectPath: "/tmp/project"},
		},
	})
	got := updated.(modelState)
	if got.launchTarget != "claude-code" || got.launchProject != "/tmp/project" {
		t.Fatalf("launch identity lost: target=%q project=%q", got.launchTarget, got.launchProject)
	}
}

func TestResumeLineReplacesSummaryAfterMigration(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.layout()
	m.lastResume = "cd '/tmp/project' && codex resume 'abc'"
	if !strings.Contains(m.footerView(), "codex resume") {
		t.Fatal("footer did not surface the resume command")
	}
}

// After a migration the tool must be able to land the user in the other agent.
// Handing back a command to copy out of a full-screen UI is a dead end.
func TestEnterAfterMigrationLaunchesTarget(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.lastResume = "cd '/tmp/project' && codex resume 'abc'"
	m.layout()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(modelState)
	if got.launch != m.lastResume {
		t.Fatalf("launch = %q, want the resume command", got.launch)
	}
	if cmd == nil {
		t.Fatal("enter did not quit the program to hand over the terminal")
	}
	if got.overlay != overlayNone {
		t.Fatalf("enter reopened an overlay instead of launching: %d", got.overlay)
	}
}

// Escape keeps browsing without launching, so a finished migration does not
// trap the session on one screen.
func TestEscapeAfterMigrationResumesBrowsing(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 80, 24
	m.lastResume = "cd '/tmp' && codex resume 'abc'"
	m.layout()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(modelState)
	if got.lastResume != "" || got.launch != "" {
		t.Fatalf("escape did not return to browsing: resume=%q launch=%q", got.lastResume, got.launch)
	}
}

func markKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestXTogglesTheBatchMark(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	updated, _ := m.Update(markKey('x'))
	got := updated.(modelState)
	if !got.marked["session"] {
		t.Fatalf("x did not mark the row: %v", got.marked)
	}
	if !strings.Contains(got.View(), "✓") {
		t.Fatal("a marked row must render its mark")
	}

	updated, _ = got.Update(markKey('x'))
	if again := updated.(modelState); again.marked["session"] {
		t.Fatalf("x did not unmark the row: %v", again.marked)
	}
}

func TestSpaceStillOpensPreviewInsteadOfMarking(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, cmd := m.Update(markKey(' '))
	got := updated.(modelState)
	if len(got.marked) != 0 {
		t.Fatalf("space must not mark; it is the preview key: %v", got.marked)
	}
	if !got.loading || cmd == nil {
		t.Fatalf("space no longer opens the preview: loading=%v cmd=%v", got.loading, cmd)
	}
}

func TestAMarksEveryVisibleRowAndClearsOnRepeat(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()

	updated, _ := m.Update(markKey('a'))
	got := updated.(modelState)
	if len(got.marked) != len(got.sessions.Items()) {
		t.Fatalf("a did not mark every visible row: %d of %d", len(got.marked), len(got.sessions.Items()))
	}

	updated, _ = got.Update(markKey('a'))
	if again := updated.(modelState); len(again.marked) != 0 {
		t.Fatalf("a did not clear a fully marked page: %v", again.marked)
	}
}

func TestMarksSurviveAPageReload(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.layout()
	updated, _ := m.Update(markKey('x'))
	got := updated.(modelState)

	// A filter change or an index refresh rebuilds the rows. An index-keyed
	// selection would follow whatever now sits in that position instead.
	sm := model.Summary{ID: "other", Provider: "pi", Title: "Another session", ProjectPath: "/tmp/project"}
	got.sessions.SetItems([]list.Item{
		sessionItem{summary: sm, providerLbl: "pi"},
		sessionItem{summary: model.Summary{ID: "session", Provider: "codex", Title: "A useful title", ProjectPath: "/tmp/project"}, providerLbl: "Codex"},
	})
	if !got.marked["session"] {
		t.Fatalf("the mark did not survive a page reload: %v", got.marked)
	}
	if got.marked["other"] {
		t.Fatal("the mark moved to a different session")
	}
}
