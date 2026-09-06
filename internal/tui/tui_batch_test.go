package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/titler"
)

var errBatchTest = errors.New("pi: 生成超时")

func ctrlTKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlT} }

func escKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

func batchTestModel() modelState {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.titleCfg = titler.Config{Provider: "pi"}
	m.ctx = context.Background()
	m.layout()
	return m
}

func TestBatchRequiresTitleModel(t *testing.T) {
	m := layoutTestModel()
	m.width, m.height = 100, 30
	m.marked["session"] = true
	m.layout()

	updated, cmd := m.Update(ctrlTKey())
	got := updated.(modelState)
	if got.overlay == overlayBatchTitle {
		t.Fatal("batch opened without a configured title model")
	}
	if got.batchRunning || cmd != nil {
		t.Fatalf("batch started processes without a title model: running=%v cmd=%v", got.batchRunning, cmd)
	}
	if !strings.Contains(got.status, "设置") {
		t.Fatalf("missing setup hint: %q", got.status)
	}
}

func TestBatchRequiresMarks(t *testing.T) {
	m := batchTestModel()

	updated, cmd := m.Update(ctrlTKey())
	got := updated.(modelState)
	if got.overlay == overlayBatchTitle {
		t.Fatal("batch opened with nothing marked")
	}
	if cmd != nil {
		t.Fatal("batch started work with nothing marked")
	}
	if !strings.Contains(got.status, "标记") {
		t.Fatalf("missing mark hint: %q", got.status)
	}
}

func TestBatchPrepareFreezesWithoutModelCalls(t *testing.T) {
	m := batchTestModel()
	m.marked["session"] = true

	opened, cmd := m.Update(ctrlTKey())
	got := opened.(modelState)
	if got.overlay != overlayBatchTitle {
		t.Fatalf("batch did not open: overlay=%d status=%q", got.overlay, got.status)
	}
	if cmd == nil {
		t.Fatal("batch opened but scheduled no preparation")
	}
	// The test registry holds no providers, so the row freezes on lookup
	// without any agent CLI ever starting.
	msg := cmd()
	ready, ok := msg.(batchReadyMsg)
	if !ok {
		t.Fatalf("prepare returned %T, want batchReadyMsg", msg)
	}
	if len(ready.items) != 0 {
		t.Fatalf("unresolvable row reached the engine: %+v", ready.items)
	}

	updated, _ := got.Update(ready)
	done := updated.(modelState)
	if done.batchRunning {
		t.Fatal("an all-frozen batch must land on review, not progress")
	}
	if len(done.batchResults) != 1 {
		t.Fatalf("results = %d, want 1 frozen row", len(done.batchResults))
	}
	view := done.View()
	if !strings.Contains(view, "冻结 1") {
		t.Fatalf("review must fold the frozen row into counts:\n%s", view)
	}
}

func TestBatchReviewListsChangedOnly(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchResults = []titler.BatchResult{
		{SessionID: "a", Current: "old talk", Title: "0903｜修复｜快捷键冲突"},
		{SessionID: "b", Current: "other talk", Frozen: "缺少创建时间"},
		{SessionID: "c", Current: "third talk", Err: errBatchTest},
		{SessionID: "d", Current: "0903｜修复｜旧标题", Title: "0903｜修复｜旧标题"},
	}

	view := m.View()
	if !strings.Contains(view, "0903｜修复｜快捷键冲突") {
		t.Fatalf("review must show the changed row:\n%s", view)
	}
	if strings.Contains(view, "缺少创建时间") {
		t.Fatalf("review must fold frozen rows by default:\n%s", view)
	}
	if !strings.Contains(view, "可应用 1 条") || !strings.Contains(view, "冻结 1") ||
		!strings.Contains(view, "失败 1") || !strings.Contains(view, "无变化 1") {
		t.Fatalf("review must count every outcome:\n%s", view)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	expanded := updated.(modelState).View()
	if !strings.Contains(expanded, "缺少创建时间") {
		t.Fatalf("e must expand the folded rows:\n%s", expanded)
	}
}

func TestBatchFinalizeFreezesDuplicateProposals(t *testing.T) {
	m := batchTestModel()
	sm1 := model.Summary{ID: "a", Title: "first"}
	sm2 := model.Summary{ID: "b", Title: "second"}
	m.batchItems = []model.Summary{sm1, sm2}
	m.batchResults = []titler.BatchResult{
		{SessionID: "a", Current: "first", Title: "0903｜修复｜同一标题"},
		{SessionID: "b", Current: "second", Title: "0903｜修复｜同一标题"},
	}

	m.finalizeBatch()
	for _, r := range m.batchResults {
		if r.Changed() {
			t.Fatalf("duplicate proposal stayed applicable: %+v", r)
		}
		if r.Frozen == "" {
			t.Fatalf("duplicate proposal missing a freeze reason: %+v", r)
		}
	}
	// Mark order survives the out-of-order arrival.
	if m.batchResults[0].SessionID != "a" || m.batchResults[1].SessionID != "b" {
		t.Fatalf("review order broke: %+v", m.batchResults)
	}
}

func TestEscCancelsARunningBatch(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.batchCancel = cancel
	m.batchRunning = true
	m.batchItems = []model.Summary{{ID: "session", Title: "A useful title"}}
	m.batchTotal = 1

	updated, _ := m.Update(escKey())
	got := updated.(modelState)
	if ctx.Err() == nil {
		t.Fatal("esc did not cancel the batch context")
	}
	if got.overlay != overlayBatchTitle || !got.batchRunning {
		t.Fatalf("esc must keep the overlay open while rows drain: overlay=%d running=%v", got.overlay, got.batchRunning)
	}
	if !got.batchCancelling {
		t.Fatal("overlay must show the cancelling state")
	}
}

func TestEscOnReviewClosesAndKeepsMarks(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.marked["session"] = true
	m.batchResults = []titler.BatchResult{{SessionID: "session", Current: "x", Frozen: "缺少创建时间"}}

	updated, _ := m.Update(escKey())
	got := updated.(modelState)
	if got.overlay != overlayNone {
		t.Fatalf("esc did not close the review: overlay=%d", got.overlay)
	}
	if !got.marked["session"] {
		t.Fatal("closing the review must keep the marks for a retry")
	}
	if got.batchResults != nil {
		t.Fatal("batch state must reset on close")
	}
}

func TestEnterWithoutChangesAppliesNothing(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchResults = []titler.BatchResult{{SessionID: "session", Current: "x", Frozen: "缺少创建时间"}}

	updated, cmd := m.Update(enterKey())
	got := updated.(modelState)
	if cmd != nil {
		t.Fatal("enter must not start an apply with no changed rows")
	}
	if !strings.Contains(got.status, "没有可应用") {
		t.Fatalf("missing no-change hint: %q", got.status)
	}
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func typeRunes(m modelState, s string) modelState {
	for _, r := range s {
		updated, _ := m.Update(runeKey(r))
		m = updated.(modelState)
	}
	return m
}

// The review list cannot show which model wrote the titles, so the overlay
// must name the agent and the model it ran on.
func TestBatchReviewNamesAgentAndModel(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchResults = []titler.BatchResult{{SessionID: "a", Current: "old talk", Title: "0903｜修复｜快捷键冲突"}}

	if view := m.View(); !strings.Contains(view, "pi") || !strings.Contains(view, "默认模型") {
		t.Fatalf("review must name the agent and the default model:\n%s", view)
	}

	m.batchCfg = titler.Config{Provider: "pi", Model: "claude-sonnet-4-5"}
	if view := m.View(); !strings.Contains(view, "claude-sonnet-4-5") || !strings.Contains(view, "本次临时") {
		t.Fatalf("review must name the overridden model as temporary:\n%s", view)
	}
}

// The override is per batch: it changes what this run calls and never touches
// the configured default.
func TestBatchModelOverrideRerunsWithoutTouchingConfig(t *testing.T) {
	m := batchTestModel()
	m.marked["session"] = true
	opened, _ := m.Update(ctrlTKey())
	m = opened.(modelState)
	m.batchItems = []model.Summary{{ID: "session", Title: "A useful title"}}
	startGen := m.batchGen

	edited, _ := m.Update(runeKey('m'))
	m = edited.(modelState)
	if !m.batchModelEditing {
		t.Fatal("m did not open the model override")
	}
	m = typeRunes(m, "haiku")
	if view := m.View(); !strings.Contains(view, "haiku") {
		t.Fatalf("the override field must show what was typed:\n%s", view)
	}

	committed, cmd := m.Update(enterKey())
	got := committed.(modelState)
	if got.batchModelEditing {
		t.Fatal("enter must close the override field")
	}
	if got.batchConfig().Model != "haiku" {
		t.Fatalf("batch model = %q, want haiku", got.batchConfig().Model)
	}
	if got.titleCfg.Model != "" {
		t.Fatalf("the configured default must stay untouched, got %q", got.titleCfg.Model)
	}
	if got.batchGen == startGen {
		t.Fatal("a model change must supersede the previous run")
	}
	if cmd == nil || got.batchResults != nil {
		t.Fatalf("enter must re-run the batch: cmd=%v results=%+v", cmd, got.batchResults)
	}
}

// Results from a superseded run must not land in the new list: they were
// written by the model the person just replaced.
func TestBatchDropsResultsFromSupersededRun(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchGen = 7
	m.batchTotal = 2
	m.batchRunning = true

	stale, _ := m.Update(batchResultMsg{gen: 6, res: titler.BatchResult{SessionID: "a", Title: "旧模型的标题"}})
	got := stale.(modelState)
	if len(got.batchResults) != 0 {
		t.Fatalf("stale result landed in the new list: %+v", got.batchResults)
	}

	done, _ := got.Update(batchFinishedMsg{gen: 6})
	if !done.(modelState).batchRunning {
		t.Fatal("a stale finish must not end the current run")
	}
}

// The override field is drawn inside the same modal as the review list, so it
// has to survive the narrow terminals the rest of the overlays are tested at.
func TestBatchModelOverrideFitsNarrowTerminals(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {100, 30}} {
		m := batchTestModel()
		m.overlay = overlayBatchTitle
		m.batchModelInput = newBatchModelInput()
		m.batchModelEditing = true
		m.batchModelInput.SetValue("claude-sonnet-4-5")
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := updated.(modelState).View()
		if got := lipgloss.Width(view); got > size[0] {
			t.Errorf("%dx%d override width = %d", size[0], size[1], got)
		}
		if got := lipgloss.Height(view); got > size[1] {
			t.Errorf("%dx%d override height = %d", size[0], size[1], got)
		}
	}
}

// A retry must cost only the rows that failed: the suggestions that landed
// were a model call each, and re-running them invites a different answer for
// a row the person already reviewed.
func TestRetryRerunsOnlyFailedRows(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchItems = []model.Summary{
		{ID: "ok", Title: "old ok"},
		{ID: "bad", Title: "old bad"},
		{ID: "cancelled", Title: "old cancelled"},
		{ID: "frozen", Title: "old frozen"},
	}
	m.batchByID = map[string]model.Summary{}
	for _, sm := range m.batchItems {
		m.batchByID[sm.ID] = sm
	}
	m.batchResults = []titler.BatchResult{
		{SessionID: "ok", Current: "old ok", Title: "0903｜修复｜已有结果"},
		{SessionID: "bad", Current: "old bad", Err: errBatchTest},
		{SessionID: "cancelled", Current: "old cancelled", Frozen: "已取消"},
		{SessionID: "frozen", Current: "old frozen", Frozen: "缺少创建时间"},
	}
	startGen := m.batchGen

	if view := m.View(); !strings.Contains(view, "r 重试 2 行") {
		t.Fatalf("review must offer a retry for the failed and cancelled rows:\n%s", view)
	}

	updated, cmd := m.Update(runeKey('r'))
	got := updated.(modelState)
	if cmd == nil {
		t.Fatal("r scheduled no retry")
	}
	if got.batchGen == startGen {
		t.Fatal("the retry must supersede the previous run")
	}
	kept := map[string]bool{}
	for _, r := range got.batchResults {
		kept[r.SessionID] = true
	}
	if !kept["ok"] || !kept["frozen"] {
		t.Fatalf("a retry threw away rows it did not re-run: %+v", got.batchResults)
	}
	if kept["bad"] || kept["cancelled"] {
		t.Fatalf("retried rows must leave the list until they land again: %+v", got.batchResults)
	}

	// The re-run covers exactly the retried rows, and its results merge back
	// into the kept ones.
	ready, ok := cmd().(batchReadyMsg)
	if !ok {
		t.Fatalf("retry produced %T, want batchReadyMsg", cmd())
	}
	if ready.gen != got.batchGen {
		t.Fatalf("retry generation = %d, want %d", ready.gen, got.batchGen)
	}
	merged, _ := got.Update(ready)
	final := merged.(modelState)
	if len(final.batchResults) != 4 {
		t.Fatalf("retry lost rows: %+v", final.batchResults)
	}
}

func TestRetryWithNothingToRetrySaysSo(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchResults = []titler.BatchResult{{SessionID: "a", Current: "x", Frozen: "缺少创建时间"}}

	updated, cmd := m.Update(runeKey('r'))
	got := updated.(modelState)
	if cmd != nil {
		t.Fatal("a frozen row must not be retried")
	}
	if !strings.Contains(got.status, "没有可重试") {
		t.Fatalf("missing hint: %q", got.status)
	}
}

// Swapping models mid-run would leave the list half-written by each model.
func TestBatchModelOverrideRefusedWhileRunning(t *testing.T) {
	m := batchTestModel()
	m.overlay = overlayBatchTitle
	m.batchRunning = true

	updated, _ := m.Update(runeKey('m'))
	got := updated.(modelState)
	if got.batchModelEditing {
		t.Fatal("the override must wait for the run to stop")
	}
	if !strings.Contains(got.status, "esc") {
		t.Fatalf("missing cancel-first hint: %q", got.status)
	}
}

func TestBatchChangedSelectsOnlyChangedRows(t *testing.T) {
	results := []titler.BatchResult{
		{SessionID: "a", Current: "old", Title: "0903｜修复｜新标题"},
		{SessionID: "b", Current: "same", Title: "same"},
		{SessionID: "c", Current: "old", Title: ""},
		{SessionID: "d", Current: "old", Frozen: "缺少创建时间"},
		{SessionID: "e", Current: "old", Title: "0903｜修复｜另一标题", Err: errBatchTest},
	}
	got := batchChanged(results)
	if len(got) != 1 || got[0].SessionID != "a" {
		t.Fatalf("changed = %+v, want only row a", got)
	}
}

// fakeRenamer is an in-memory agent for apply tests. failIDs fail every
// rename; renamed records what got through.
type fakeRenamer struct {
	failIDs map[string]bool
	renamed map[string]string
}

func (f *fakeRenamer) ID() string          { return "fake" }
func (f *fakeRenamer) DisplayName() string { return "Fake" }
func (f *fakeRenamer) DefaultPaths() []provider.PathSpec {
	return nil
}
func (f *fakeRenamer) Installed() bool { return true }
func (f *fakeRenamer) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return nil, nil
}
func (f *fakeRenamer) Load(context.Context, provider.SessionRef) (*model.Conversation, error) {
	return nil, nil
}
func (f *fakeRenamer) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, nil
}
func (f *fakeRenamer) SupportsResume() bool { return false }
func (f *fakeRenamer) ResumeCommand(provider.WriteResult) string {
	return ""
}
func (f *fakeRenamer) RenameSession(_ context.Context, ref provider.SessionRef, title string) error {
	if f.failIDs[ref.ID] {
		return errBatchTest
	}
	if f.renamed == nil {
		f.renamed = map[string]string{}
	}
	f.renamed[ref.ID] = title
	return nil
}

// plainProvider implements provider.Provider without SessionRenamer. It
// shares no structure with fakeRenamer: embedding would promote RenameSession
// and silently satisfy the assertion under test.
type plainProvider struct{}

func (p *plainProvider) ID() string          { return "plain" }
func (p *plainProvider) DisplayName() string { return "Plain" }
func (p *plainProvider) DefaultPaths() []provider.PathSpec {
	return nil
}
func (p *plainProvider) Installed() bool { return true }
func (p *plainProvider) Discover(context.Context, provider.DiscoverOpts) ([]model.Summary, error) {
	return nil, nil
}
func (p *plainProvider) Load(context.Context, provider.SessionRef) (*model.Conversation, error) {
	return nil, nil
}
func (p *plainProvider) Write(context.Context, *model.Conversation, provider.WriteOpts) (*provider.WriteResult, error) {
	return nil, nil
}
func (p *plainProvider) SupportsResume() bool { return false }
func (p *plainProvider) ResumeCommand(provider.WriteResult) string {
	return ""
}

type fakeLookup map[string]provider.Provider

func (l fakeLookup) Get(id string) (provider.Provider, error) {
	p, ok := l[id]
	if !ok {
		return nil, errBatchTest
	}
	return p, nil
}

func applyTestRows() ([]titler.BatchResult, map[string]model.Summary) {
	rows := []titler.BatchResult{
		{SessionID: "ok1", Current: "old", Title: "0903｜修复｜第一条"},
		{SessionID: "bad", Current: "old", Title: "0903｜修复｜第二条"},
		{SessionID: "ok2", Current: "old", Title: "0903｜修复｜第三条"},
	}
	byID := map[string]model.Summary{
		"ok1": {ID: "ok1", Provider: "fake", Title: "old"},
		"bad": {ID: "bad", Provider: "fake", Title: "old"},
		"ok2": {ID: "ok2", Provider: "fake", Title: "old"},
	}
	return rows, byID
}

func TestApplyRenamesIsolatesRowFailures(t *testing.T) {
	rows, byID := applyTestRows()
	fake := &fakeRenamer{failIDs: map[string]bool{"bad": true}}

	renamed, failed := applyRenames(context.Background(), fakeLookup{"fake": fake}, byID, rows)

	if len(renamed) != 2 || len(failed) != 1 {
		t.Fatalf("renamed=%d failed=%d, want 2 and 1", len(renamed), len(failed))
	}
	if failed[0].id != "bad" {
		t.Fatalf("wrong row failed: %+v", failed)
	}
	if fake.renamed["ok1"] != "0903｜修复｜第一条" || fake.renamed["ok2"] != "0903｜修复｜第三条" {
		t.Fatalf("healthy rows did not reach the provider: %v", fake.renamed)
	}
	if _, ok := fake.renamed["bad"]; ok {
		t.Fatal("the failing row must not record a rename")
	}
}

func TestApplyRenamesRejectsUnknownRowsAndNonRenamers(t *testing.T) {
	rows := []titler.BatchResult{
		{SessionID: "ghost", Current: "old", Title: "0903｜修复｜幽灵行"},
		{SessionID: "plain", Current: "old", Title: "0903｜修复｜普通行"},
	}
	byID := map[string]model.Summary{
		"ghost": {ID: "ghost", Provider: "missing", Title: "old"},
		"plain": {ID: "plain", Provider: "plain", Title: "old"},
	}
	lookup := fakeLookup{"fake": &fakeRenamer{}, "plain": &plainProvider{}}

	renamed, failed := applyRenames(context.Background(), lookup, byID, rows)

	if len(renamed) != 0 || len(failed) != 2 {
		t.Fatalf("renamed=%d failed=%d, want 0 and 2", len(renamed), len(failed))
	}
	reasons := map[string]string{}
	for _, f := range failed {
		reasons[f.id] = f.reason
	}
	if reasons["plain"] != "该来源不支持重命名" {
		t.Fatalf("plain reason = %q", reasons["plain"])
	}
	// ghost is missing from byID... it is present; drop it to test the
	// unknown-row path instead.
	delete(byID, "ghost")
	_, failed = applyRenames(context.Background(), lookup, byID, rows[:1])
	if len(failed) != 1 || failed[0].reason != "会话已不在列表中" {
		t.Fatalf("unknown row failure = %+v", failed)
	}
}

type fakeSummaryStore map[string]string

func (s fakeSummaryStore) FindByID(id string) (*model.Summary, error) {
	title, ok := s[id]
	if !ok {
		return nil, errBatchTest
	}
	return &model.Summary{ID: id, Title: title}, nil
}

func TestVerifyRenamesRequiresRereadMatch(t *testing.T) {
	renamed := []appliedRename{
		{id: "ok", title: "0903｜修复｜新标题", provider: "fake"},
		{id: "drift", title: "0903｜修复｜新标题", provider: "fake"},
		{id: "gone", title: "0903｜修复｜新标题", provider: "fake"},
	}
	store := fakeSummaryStore{"ok": "0903｜修复｜新标题", "drift": "完全不一样的标题"}

	applied, failed := verifyRenames(store, renamed)

	if len(applied) != 1 || applied[0] != "ok" {
		t.Fatalf("applied = %v, want [ok]", applied)
	}
	if len(failed) != 2 {
		t.Fatalf("failed = %+v, want drift and gone", failed)
	}
	byID := map[string]string{}
	for _, f := range failed {
		byID[f.id] = f.reason
	}
	if byID["drift"] != "回读标题不一致" {
		t.Fatalf("drift reason = %q", byID["drift"])
	}
}
