package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/nxxxsooo/another/internal/index"
	"github.com/nxxxsooo/another/internal/model"
	"github.com/nxxxsooo/another/internal/provider"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titler"
)

// maxBatchPreviewRows caps the confirmation list. A batch can in principle
// cover hundreds of rows; the overlay is a decision point, not a ledger, so
// anything past the cap folds into a "+N more" line.
const maxBatchPreviewRows = 12

// batchReadyMsg carries the prepared work: items still need a model, frozen
// rows never will. The overlay opens before this arrives, so a slow local
// preview load shows progress rather than a dead key.
type batchReadyMsg struct {
	items  []titler.BatchItem
	frozen []titler.BatchResult
}

// batchResultMsg streams one finished suggestion. Results arrive out of order;
// the confirmation list is re-sorted into mark order at finalize time.
type batchResultMsg struct{ res titler.BatchResult }

// batchFinishedMsg means the engine channel closed. On cancellation some rows
// may never have reported; finalize synthesizes them as cancelled.
type batchFinishedMsg struct{}

// batchAppliedMsg reports the confirmed rename pass. appliedIDs leave the
// batch selection; everything else stays marked for another attempt.
type batchAppliedMsg struct {
	appliedIDs []string
	applied    int
	failed     int
	detail     string
}

// startBatch opens the bulk-rename flow over the marked sessions. It only
// previews: nothing is renamed until the confirmation step fires batchApplyCmd.
func (m modelState) startBatch() (tea.Model, tea.Cmd) {
	if !m.titleCfg.Enabled() {
		m.status = "先在设置里配置标题模型，才能批量命名"
		return m, nil
	}
	if len(m.marked) == 0 {
		m.status = "x 标记会话后，再用 ctrl+t 批量命名"
		return m, nil
	}
	summaries, missing := m.markedSummaries()
	if len(summaries) == 0 && len(missing) == 0 {
		m.status = "标记的会话都不在当前列表中"
		return m, nil
	}
	byID := make(map[string]model.Summary, len(summaries))
	for _, sm := range summaries {
		byID[sm.ID] = sm
	}
	m.overlay = overlayBatchTitle
	m.batchItems = summaries
	m.batchByID = byID
	m.batchResults = nil
	m.batchTotal = 0
	m.batchCh = nil
	m.batchCancel = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchExpanded = false
	m.err = ""
	m.layout()
	return m, batchPrepareCmd(m.ctx, m.reg, summaries, missing)
}

// markedSummaries resolves the batch selection in visible order. Marks are
// keyed by session ID precisely so they survive filtering; rows that scrolled
// out of the current view resolve through the index instead.
func (m modelState) markedSummaries() ([]model.Summary, []titler.BatchResult) {
	var out []model.Summary
	var frozen []titler.BatchResult
	seen := map[string]bool{}
	for _, li := range m.sessions.Items() {
		it, ok := li.(sessionItem)
		if !ok || !m.marked[it.summary.ID] || seen[it.summary.ID] {
			continue
		}
		seen[it.summary.ID] = true
		out = append(out, it.summary)
	}
	for id := range m.marked {
		if seen[id] {
			continue
		}
		sm, err := m.findSummary(id)
		if err != nil || sm == nil {
			frozen = append(frozen, titler.BatchResult{SessionID: id, Frozen: "会话已不在索引中"})
			continue
		}
		out = append(out, *sm)
	}
	return out, frozen
}

func (m modelState) findSummary(id string) (*model.Summary, error) {
	if m.idx == nil {
		return nil, provider.ErrNotFound
	}
	return m.idx.FindByID(id)
}

// batchPrepareCmd loads the recent messages for every row and freezes the
// rows that must never reach a model. Preview loads are local reads, so they
// run up front; the slow model calls stream later through SuggestBatch.
func batchPrepareCmd(ctx context.Context, reg *registry.Registry, summaries []model.Summary, seed []titler.BatchResult) tea.Cmd {
	return func() tea.Msg {
		frozen := append([]titler.BatchResult{}, seed...)
		var items []titler.BatchItem
		freeze := func(sm model.Summary, reason string) {
			frozen = append(frozen, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Frozen: reason})
		}
		fail := func(sm model.Summary, err error) {
			frozen = append(frozen, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Err: err})
		}
		for _, sm := range summaries {
			switch {
			case isCurrentSession(sm):
				freeze(sm, "当前会话不改名")
			case sm.CreatedAt.Unix() <= 0:
				// The index stores a missing creation time as Unix 0, which
				// scans back as 1970 rather than a zero time. The titler
				// engine freezes zero times, but it never sees one through
				// this path, so the freeze happens here instead of handing
				// the model a date of 0101. Single rename has the same hole.
				freeze(sm, "缺少创建时间")
			case reg == nil:
				fail(sm, provider.ErrNotFound)
			default:
				p, err := reg.Get(sm.Provider)
				if err != nil {
					fail(sm, err)
					continue
				}
				if _, ok := p.(provider.SessionRenamer); !ok {
					freeze(sm, "该来源不支持重命名")
					continue
				}
				if !titler.Supports(sm.Provider) {
					freeze(sm, "该 agent 不支持标题建议")
					continue
				}
				ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
				var conv *model.Conversation
				if preview, ok := p.(provider.PreviewLoader); ok {
					conv, err = preview.LoadPreview(ctx, ref, 12)
				} else {
					conv, err = p.Load(ctx, ref)
				}
				if err != nil {
					fail(sm, err)
					continue
				}
				var msgs []model.Message
				if conv != nil {
					msgs = conv.Messages
				}
				items = append(items, titler.BatchItem{SessionID: sm.ID, Request: titler.Request{
					Title: sm.Title, ProjectPath: sm.ProjectPath, CreatedAt: sm.CreatedAt, Messages: msgs,
				}})
			}
		}
		return batchReadyMsg{items: items, frozen: frozen}
	}
}

// batchNextCmd waits for one engine result. Each arrival re-arms the next
// wait, so progress streams instead of blocking until the slowest row lands.
func batchNextCmd(ch <-chan titler.BatchResult) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return batchFinishedMsg{}
		}
		return batchResultMsg{res: res}
	}
}

// batchChanged selects the only rows a confirmation may apply.
func batchChanged(results []titler.BatchResult) []titler.BatchResult {
	var out []titler.BatchResult
	for _, r := range results {
		if r.Changed() {
			out = append(out, r)
		}
	}
	return out
}

// finalizeBatch closes the suggestion phase: missing rows become cancelled,
// duplicates freeze together, and the review list returns to mark order.
func (m *modelState) finalizeBatch() {
	have := make(map[string]bool, len(m.batchResults))
	for _, r := range m.batchResults {
		have[r.SessionID] = true
	}
	for _, sm := range m.batchItems {
		if !have[sm.ID] {
			m.batchResults = append(m.batchResults, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Frozen: "已取消"})
		}
	}
	m.batchResults = titler.FreezeDuplicates(m.batchResults)
	pos := make(map[string]int, len(m.batchItems))
	for i, sm := range m.batchItems {
		pos[sm.ID] = i
	}
	sort.SliceStable(m.batchResults, func(i, j int) bool {
		pi, oki := pos[m.batchResults[i].SessionID]
		pj, okj := pos[m.batchResults[j].SessionID]
		if oki && okj {
			return pi < pj
		}
		return oki && !okj
	})
	m.batchRunning = false
	m.batchCancelling = false
	m.batchCancel = nil
	m.batchCh = nil
}

// resetBatch drops the flow but keeps the marks, so a cancelled or finished
// batch can be retried without re-marking.
func (m *modelState) resetBatch() {
	m.batchItems = nil
	m.batchByID = nil
	m.batchResults = nil
	m.batchTotal = 0
	m.batchCh = nil
	m.batchCancel = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchExpanded = false
}

func (m modelState) updateBatchOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.batchCancel != nil {
			m.batchCancel()
		}
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc":
		if m.batchRunning {
			if m.batchCancel != nil {
				m.batchCancel()
				m.batchCancel = nil
			}
			m.batchCancelling = true
			m.status = "正在取消剩余任务…"
			return m, nil
		}
		m.overlay = overlayNone
		m.resetBatch()
		m.layout()
		return m, nil
	case "e", "E":
		if !m.batchRunning {
			m.batchExpanded = !m.batchExpanded
		}
		return m, nil
	case "enter":
		if m.batchRunning || m.batchCancelling {
			return m, nil
		}
		changed := batchChanged(m.batchResults)
		if len(changed) == 0 {
			m.status = "没有可应用的标题变更"
			return m, nil
		}
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return m, batchApplyCmd(ctx, m.reg, m.idx, m.batchByID, changed)
	}
	return m, nil
}

// providerLookup is the seam that keeps apply tests off real agents.
// *registry.Registry satisfies it in production.
type providerLookup interface {
	Get(id string) (provider.Provider, error)
}

// summaryLookup rereads titles after a rename. *index.Store satisfies it in
// production.
type summaryLookup interface {
	FindByID(id string) (*model.Summary, error)
}

type appliedRename struct {
	id       string
	title    string
	provider string
}

type applyFailure struct {
	id     string
	reason string
}

// applyRenames runs the native rename for every row and isolates failures per
// row: one bad row never takes healthy rows with it.
func applyRenames(ctx context.Context, lookup providerLookup, byID map[string]model.Summary, rows []titler.BatchResult) (renamed []appliedRename, failed []applyFailure) {
	for _, r := range rows {
		sm, ok := byID[r.SessionID]
		if !ok {
			failed = append(failed, applyFailure{id: r.SessionID, reason: "会话已不在列表中"})
			continue
		}
		p, err := lookup.Get(sm.Provider)
		if err != nil {
			failed = append(failed, applyFailure{id: r.SessionID, reason: err.Error()})
			continue
		}
		renamer, ok := p.(provider.SessionRenamer)
		if !ok {
			failed = append(failed, applyFailure{id: r.SessionID, reason: "该来源不支持重命名"})
			continue
		}
		ref := provider.SessionRef{ID: sm.ID, Provider: sm.Provider, StoragePath: sm.StoragePath, ProjectPath: sm.ProjectPath}
		if err := renamer.RenameSession(ctx, ref, r.Title); err != nil {
			failed = append(failed, applyFailure{id: r.SessionID, reason: err.Error()})
			continue
		}
		renamed = append(renamed, appliedRename{id: sm.ID, title: r.Title, provider: sm.Provider})
	}
	return renamed, failed
}

// verifyRenames rereads every renamed title: a rename only counts if the
// source of truth confirms it.
func verifyRenames(lookup summaryLookup, renamed []appliedRename) (appliedIDs []string, failed []applyFailure) {
	for _, rn := range renamed {
		sm, err := lookup.FindByID(rn.id)
		if err != nil {
			failed = append(failed, applyFailure{id: rn.id, reason: err.Error()})
			continue
		}
		if strings.TrimSpace(sm.Title) != rn.title {
			failed = append(failed, applyFailure{id: rn.id, reason: "回读标题不一致"})
			continue
		}
		appliedIDs = append(appliedIDs, rn.id)
	}
	return appliedIDs, failed
}

// batchApplyCmd renames every confirmed row through the provider's native
// path, refreshes the index once per touched provider, and reads each title
// back: a rename only counts if the index confirms it.
func batchApplyCmd(ctx context.Context, reg *registry.Registry, idx *index.Store, byID map[string]model.Summary, rows []titler.BatchResult) tea.Cmd {
	return func() tea.Msg {
		renamed, failures := applyRenames(ctx, reg, byID, rows)
		touched := map[string]bool{}
		for _, rn := range renamed {
			touched[rn.provider] = true
		}
		var msg batchAppliedMsg
		for _, f := range failures {
			msg.failed++
			if msg.detail == "" {
				msg.detail = f.reason
			}
		}
		for providerID := range touched {
			if _, err := index.UpdateIncremental(ctx, reg, idx, providerID); err != nil && msg.detail == "" {
				msg.detail = err.Error()
			}
		}
		appliedIDs, verifyFailures := verifyRenames(idx, renamed)
		for _, f := range verifyFailures {
			msg.failed++
			if msg.detail == "" {
				msg.detail = f.reason
			}
		}
		msg.appliedIDs = appliedIDs
		msg.applied = len(appliedIDs)
		return msg
	}
}

// batchView renders progress while the engine runs and the confirmation list
// after it lands: changed rows first, everything else folded into counts
// unless expanded.
func (m modelState) batchView() string {
	inner := m.width - modalStyle.GetHorizontalFrameSize() - paneStyle.GetHorizontalBorderSize()
	if inner < 12 {
		inner = 12
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("批量命名会话") + "\n")
	if m.batchRunning {
		note := fmt.Sprintf("正在生成 %d/%d · esc 取消", len(m.batchResults), m.batchTotal)
		if m.batchCancelling {
			note = fmt.Sprintf("正在取消 %d/%d · 等待剩余任务退出", len(m.batchResults), m.batchTotal)
		}
		b.WriteString(mutedStyle.Render(note) + "\n\n")
		b.WriteString(mutedStyle.Render("每条都是独立的 agent 调用，慢是正常的。"))
		return b.String()
	}
	counts := titler.Summarize(m.batchResults)
	fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render(
		fmt.Sprintf("可应用 %d 条 · 冻结 %d · 失败 %d · 无变化 %d", counts.Changed, counts.Frozen, counts.Failed, counts.Unchanged)))
	changed := batchChanged(m.batchResults)
	listed := 0
	for _, r := range m.batchResults {
		if !r.Changed() {
			continue
		}
		if listed >= maxBatchPreviewRows {
			break
		}
		line := truncateDisplay(r.Current, inner/2-2) + "  →  " + r.Title
		b.WriteString(ansi.Truncate(line, inner, "…") + "\n")
		listed++
	}
	if len(changed) > listed {
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf("+ 还有 %d 条", len(changed)-listed)))
	}
	rest := counts.Frozen + counts.Failed + counts.Unchanged
	if m.batchExpanded {
		shown := 0
		for _, r := range m.batchResults {
			if r.Changed() {
				continue
			}
			if shown >= maxBatchPreviewRows {
				break
			}
			b.WriteString(ansi.Truncate(batchRestLine(r), inner, "…") + "\n")
			shown++
		}
		if rest > shown {
			fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf("+ 还有 %d 条", rest-shown)))
		}
	} else if rest > 0 {
		b.WriteString(mutedStyle.Render("e 展开其余行") + "\n")
	}
	return b.String()
}

func batchRestLine(r titler.BatchResult) string {
	name := truncateDisplay(r.Current, 40)
	if name == "" {
		name = r.SessionID
	}
	switch {
	case r.Err != nil:
		return mutedStyle.Render("失败 ") + name + mutedStyle.Render(" · "+r.Err.Error())
	case r.Frozen != "":
		return mutedStyle.Render("冻结 ") + name + mutedStyle.Render(" · "+r.Frozen)
	default:
		return mutedStyle.Render("无变化 ") + name
	}
}
