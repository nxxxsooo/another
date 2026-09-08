package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
	gen    uint64
	items  []titler.BatchItem
	frozen []titler.BatchResult
}

// batchResultMsg streams one finished suggestion. Results arrive out of order;
// the confirmation list is re-sorted into mark order at finalize time.
type batchResultMsg struct {
	gen uint64
	res titler.BatchResult
}

// batchFinishedMsg means the engine channel closed. On cancellation some rows
// may never have reported; finalize synthesizes them as cancelled.
type batchFinishedMsg struct{ gen uint64 }

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
		m.status = txt.batchNeedsModel
		return m, nil
	}
	if len(m.marked) == 0 {
		m.status = txt.batchNeedsMarks
		return m, nil
	}
	summaries, missing := m.markedSummaries()
	if len(summaries) == 0 && len(missing) == 0 {
		m.status = txt.batchMarksMissing
		return m, nil
	}
	byID := make(map[string]model.Summary, len(summaries))
	for _, sm := range summaries {
		byID[sm.ID] = sm
	}
	m.overlay = overlayBatchTitle
	m.batchItems = summaries
	m.batchByID = byID
	m.batchMissing = missing
	// The run starts from the configured agent and model, but the model can
	// be overridden for this batch alone; nothing here is written back to
	// config.json.
	m.batchCfg = m.titleCfg
	m.batchModelInput = newBatchModelInput()
	m.batchModelEditing = false
	m.batchResults = nil
	m.batchTotal = 0
	m.batchCh = nil
	m.batchCancel = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchExpanded = false
	m.batchGen++
	m.err = ""
	m.layout()
	return m, batchPrepareCmd(m.ctx, m.batchGen, m.reg, m.batchConfig(), summaries, missing)
}

// newBatchModelInput builds the per-batch model override field. It is created
// with the flow rather than with the program: it only ever exists while the
// batch overlay is open.
func newBatchModelInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = txt.modelPlaceholder
	in.CharLimit = 120
	in.Width = 32
	return in
}

// batchConfig is the agent and model this batch runs on. It falls back to the
// configured pair so a batch opened without going through startBatch still
// reports what would run.
func (m modelState) batchConfig() titler.Config {
	if m.batchCfg.Provider == "" {
		return m.titleCfg
	}
	return m.batchCfg
}

// rerunBatch throws away the current suggestions and asks the engine again,
// which is what a model change means: the old titles came from a model the
// person just rejected. Bumping the generation orphans the previous run's
// in-flight results instead of letting them land in the new list.
func (m modelState) rerunBatch() (tea.Model, tea.Cmd) {
	if m.batchCancel != nil {
		m.batchCancel()
		m.batchCancel = nil
	}
	m.batchResults = nil
	m.batchTotal = 0
	m.batchCh = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchExpanded = false
	m.batchGen++
	m.err = ""
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return m, batchPrepareCmd(ctx, m.batchGen, m.reg, m.batchConfig(), m.batchItems, m.batchMissing)
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
			frozen = append(frozen, titler.BatchResult{SessionID: id, Frozen: titler.FreezeNotIndexed})
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
func batchPrepareCmd(ctx context.Context, gen uint64, reg *registry.Registry, cfg titler.Config, summaries []model.Summary, seed []titler.BatchResult) tea.Cmd {
	return func() tea.Msg {
		frozen := append([]titler.BatchResult{}, seed...)
		var items []titler.BatchItem
		freeze := func(sm model.Summary, reason titler.FreezeReason) {
			frozen = append(frozen, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Frozen: reason})
		}
		fail := func(sm model.Summary, err error) {
			frozen = append(frozen, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Err: err})
		}
		// Whether a title can be suggested at all is a property of the agent
		// doing the suggesting, not of the agent each session came from. Asked
		// once, a misconfigured agent says so on every row for the right
		// reason, instead of every row failing separately inside the engine.
		if !titler.Supports(cfg.Provider) {
			for _, sm := range summaries {
				freeze(sm, titler.FreezeSuggestUnsupported)
			}
			return batchReadyMsg{gen: gen, frozen: frozen}
		}
		for _, sm := range summaries {
			switch {
			case isCurrentSession(sm):
				freeze(sm, titler.FreezeCurrentSession)
			case sm.CreatedAt.Unix() <= 0:
				// The index stores a missing creation time as Unix 0, which
				// scans back as 1970 rather than a zero time. The titler
				// engine freezes zero times, but it never sees one through
				// this path, so the freeze happens here instead of handing
				// the model a date of 0101. Single rename has the same hole.
				freeze(sm, titler.FreezeMissingCreatedAt)
			case reg == nil:
				fail(sm, provider.ErrNotFound)
			default:
				p, err := reg.Get(sm.Provider)
				if err != nil {
					fail(sm, err)
					continue
				}
				if _, ok := p.(provider.SessionRenamer); !ok {
					freeze(sm, titler.FreezeRenameUnsupported)
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
		return batchReadyMsg{gen: gen, items: items, frozen: frozen}
	}
}

// batchNextCmd waits for one engine result. Each arrival re-arms the next
// wait, so progress streams instead of blocking until the slowest row lands.
func batchNextCmd(gen uint64, ch <-chan titler.BatchResult) tea.Cmd {
	return func() tea.Msg {
		res, ok := <-ch
		if !ok {
			return batchFinishedMsg{gen: gen}
		}
		return batchResultMsg{gen: gen, res: res}
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
			m.batchResults = append(m.batchResults, titler.BatchResult{SessionID: sm.ID, Current: sm.Title, Frozen: titler.FreezeCancelled})
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
	// Closing is also how a run that has not stopped yet is left behind, so the
	// generation moves with it: results still in flight belong to a batch that
	// no longer exists and must not repopulate a closed overlay.
	m.batchGen++
	m.batchItems = nil
	m.batchByID = nil
	m.batchMissing = nil
	m.batchCfg = titler.Config{}
	m.batchModelEditing = false
	m.batchModelPicking = false
	m.batchModelFilter = ""
	m.batchModelErr = ""
	m.batchModelCursor = 0
	m.batchModelInput.Blur()
	m.batchResults = nil
	m.batchTotal = 0
	m.batchCh = nil
	m.batchCancel = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchExpanded = false
}

func (m modelState) updateBatchOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.batchModelEditing {
		return m.updateBatchModelInput(msg)
	}
	if m.batchModelPicking {
		return m.updateBatchModelPicker(msg)
	}
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
		// The first esc asks the run to stop and stays, so the rows that already
		// have a suggestion are still on screen when it winds down.
		if m.batchRunning && !m.batchCancelling {
			if m.batchCancel != nil {
				m.batchCancel()
				m.batchCancel = nil
			}
			m.batchCancelling = true
			m.status = txt.batchCancelling
			return m, nil
		}
		// A second esc leaves regardless. Waiting for the engine to close its
		// channel means waiting on an agent CLI, and one that ignores its
		// cancelled context would otherwise hold the overlay open with no way
		// out but quitting another entirely.
		if m.batchCancel != nil {
			m.batchCancel()
			m.batchCancel = nil
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
	case "r", "R":
		if m.batchRunning || m.batchCancelling {
			return m, nil
		}
		retry := batchRetryable(m.batchResults)
		if len(retry) == 0 {
			m.status = txt.batchNothingToRetry
			return m, nil
		}
		return m.retryBatch(retry)
	case "m", "M":
		// Changing the model mid-run would leave half the list written by
		// one model and half by another, so the override waits for the run
		// to stop. esc already cancels it.
		if m.batchRunning || m.batchCancelling {
			m.status = txt.batchCancelFirst
			return m, nil
		}
		return m.openBatchModelPicker()
	case "enter":
		if m.batchRunning || m.batchCancelling {
			return m, nil
		}
		changed := batchChanged(m.batchResults)
		if len(changed) == 0 {
			m.status = txt.batchNoChanges
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

// batchRetryable selects the rows a retry could still change. Failures and
// rows cut short by a cancel are worth another attempt; a frozen row was
// refused on a rule that a second run would apply identically, and an
// unchanged row already got its answer.
func batchRetryable(results []titler.BatchResult) map[string]bool {
	retry := map[string]bool{}
	for _, r := range results {
		if r.Err != nil || r.Frozen == titler.FreezeCancelled {
			retry[r.SessionID] = true
		}
	}
	return retry
}

// retryBatch runs the engine again over the failed rows only. Suggestions that
// already landed are kept: they cost a model call each, and re-running them
// would also invite a different title for a row the person already reviewed.
func (m modelState) retryBatch(retry map[string]bool) (tea.Model, tea.Cmd) {
	var again []model.Summary
	for _, sm := range m.batchItems {
		if retry[sm.ID] {
			again = append(again, sm)
		}
	}
	if len(again) == 0 {
		m.status = txt.batchRetryMissing
		return m, nil
	}
	kept := make([]titler.BatchResult, 0, len(m.batchResults))
	for _, r := range m.batchResults {
		if !retry[r.SessionID] {
			kept = append(kept, r)
		}
	}
	if m.batchCancel != nil {
		m.batchCancel()
		m.batchCancel = nil
	}
	m.batchResults = kept
	m.batchTotal = 0
	m.batchCh = nil
	m.batchRunning = false
	m.batchCancelling = false
	m.batchGen++
	m.err = ""
	m.status = ""
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return m, batchPrepareCmd(ctx, m.batchGen, m.reg, m.batchConfig(), again, nil)
}

// updateBatchModelInput owns the keyboard while the per-batch model override
// is open. Enter re-runs this batch on the new model; esc leaves the previous
// suggestions and the previous model untouched.
func (m modelState) updateBatchModelInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.batchCancel != nil {
			m.batchCancel()
		}
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	case "esc":
		m.batchModelEditing = false
		m.batchModelInput.Blur()
		// Going back to the list is only possible when there is one; for a
		// CLI that cannot list, esc leaves the override alone.
		m.batchModelPicking = len(m.batchModelOpts) > 0
		return m, nil
	case "enter":
		chosen := strings.TrimSpace(m.batchModelInput.Value())
		m.batchModelEditing = false
		m.batchModelInput.Blur()
		return m.applyBatchModel(chosen)
	}
	var cmd tea.Cmd
	m.batchModelInput, cmd = m.batchModelInput.Update(msg)
	return m, cmd
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
			failed = append(failed, applyFailure{id: r.SessionID, reason: txt.rowNotInList})
			continue
		}
		p, err := lookup.Get(sm.Provider)
		if err != nil {
			failed = append(failed, applyFailure{id: r.SessionID, reason: err.Error()})
			continue
		}
		renamer, ok := p.(provider.SessionRenamer)
		if !ok {
			failed = append(failed, applyFailure{id: r.SessionID, reason: txt.rowRenameUnsupported})
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
			failed = append(failed, applyFailure{id: rn.id, reason: txt.rowTitleMismatch})
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
	b.WriteString(titleStyle.Render(txt.batchTitle) + "\n")
	b.WriteString(ansi.Truncate(m.batchAgentLine(), inner, "…") + "\n")
	if m.batchModelPicking {
		b.WriteString(m.batchModelView(inner))
		return b.String()
	}
	if m.batchModelEditing {
		if m.batchModelErr != "" {
			b.WriteString(mutedStyle.Render(m.batchModelErr) + "\n")
		}
		b.WriteString(mutedStyle.Render(txt.batchModelLabel) + "  " + m.batchModelInput.View() + "\n")
		b.WriteString(mutedStyle.Render(txt.batchModelHelp))
		return b.String()
	}
	if m.batchRunning {
		note := fmt.Sprintf(txt.batchProgressFmt, len(m.batchResults), m.batchTotal)
		if m.batchCancelling {
			note = fmt.Sprintf(txt.batchCancelProgress, len(m.batchResults), m.batchTotal)
		}
		b.WriteString(accentStyle.Render(m.spinner.View()) + " " + mutedStyle.Render(note) + "\n\n")
		b.WriteString(mutedStyle.Render(txt.batchConcurrencyNote))
		return b.String()
	}
	counts := titler.Summarize(m.batchResults)
	fmt.Fprintf(&b, "%s\n\n", mutedStyle.Render(
		fmt.Sprintf(txt.batchCountsFmt, counts.Changed, counts.Frozen, counts.Failed, counts.Unchanged)))
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
		fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf(txt.batchMoreRowsFmt, len(changed)-listed)))
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
			fmt.Fprintf(&b, "%s\n", mutedStyle.Render(fmt.Sprintf(txt.batchMoreRowsFmt, rest-shown)))
		}
	}
	var hints []string
	if rest > 0 && !m.batchExpanded {
		hints = append(hints, txt.batchExpandHint)
	}
	if n := len(batchRetryable(m.batchResults)); n > 0 {
		hints = append(hints, fmt.Sprintf(txt.batchRetryHintFmt, n))
	}
	if len(hints) > 0 {
		b.WriteString(ansi.Truncate(mutedStyle.Render(strings.Join(hints, "  ·  ")), inner, "…") + "\n")
	}
	return b.String()
}

// batchAgentLine names the agent and model this batch actually runs on. It is
// the one thing the review list cannot show by itself: two runs over the same
// sessions look identical until you know which model wrote the titles.
func (m modelState) batchAgentLine() string {
	cfg := m.batchConfig()
	name := registry.DisplayName(m.reg, cfg.Provider)
	if cmd := titler.Command(cfg.Provider); cmd != "" && !strings.EqualFold(cmd, name) {
		name += " (" + cmd + ")"
	}
	modelName := cfg.Model
	if modelName == "" {
		modelName = txt.defaultModel
	}
	line := mutedStyle.Render(txt.batchModelSource) + "  " + name + mutedStyle.Render(" · ") + modelName +
		mutedStyle.Render(" · ") + titler.LanguageLabel(cfg.Language)
	if cfg.Model != m.titleCfg.Model {
		line += mutedStyle.Render(txt.batchModelTemporary)
	}
	return line
}

func batchRestLine(r titler.BatchResult) string {
	name := truncateDisplay(r.Current, 40)
	if name == "" {
		name = r.SessionID
	}
	switch {
	case r.Err != nil:
		return mutedStyle.Render(txt.rowFailed) + name + mutedStyle.Render(" · "+suggestErrorText(r.Err))
	case r.Frozen != titler.FreezeNone:
		return mutedStyle.Render(txt.rowFrozen) + name + mutedStyle.Render(" · "+freezeText(r.Frozen))
	default:
		return mutedStyle.Render(txt.rowUnchanged) + name
	}
}
