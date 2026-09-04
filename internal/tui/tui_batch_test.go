package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxxxsooo/another/internal/model"
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
