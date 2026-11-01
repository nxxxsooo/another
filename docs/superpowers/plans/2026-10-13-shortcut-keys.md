# Shortcut Keys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify single/batch session verbs on one key each (`r` rename, `d` delete), move refresh to `s`, narrow `X` to viewport-only, and complete the `?` overlay.

**Architecture:** Key dispatch stays in `updateList` (`internal/tui/tui_update.go`); the `?` overlay stays capability-filtered in `keyHelpColumns` (`internal/tui/tui_view.go`) with all copy in `strings.go`. Old `ctrl+r`/`ctrl+d`/`ctrl+t` remain as hidden aliases, never advertised.

**Tech Stack:** Go, Bubble Tea (`bubbletea`, `bubbles/list`), existing `modelState.marked map[string]bool`.

**Spec:** In-chat key table approved 2026-10-13 (Browse / This session / Several at once listing; `r` replaces `ctrl+r`+`ctrl+t`, `d` replaces `ctrl+d`, `s` replaces `r` refresh, `X` viewport-only, `c`/`u`/`esc`/`?` listed, unsupported session rows grey not hidden).

## Global Constraints

- Every `uiText` field added in English must have a Chinese translation in the same change; verbs/order of `Fmt` fields must match across languages.
- Session verb keys are dual-mode: `len(marked) > 0` acts on the marked set, else on the current row (rename unaffected when cursor is on a folded band header).
- Hidden aliases (`ctrl+r`, `ctrl+d`, `ctrl+t`, and `r`-as-refresh) keep working but appear nowhere in `?`, footer, or status hints.
- Marks are keyed by session ID and survive filtering/paging; `esc` in the list clears them.

## Review Focus

- Pressing `r` with no marks on a folded band header must do nothing (no selection to rename).
- Pressing `d` with a marked set containing the currently-running session must skip it with the existing cannot-delete-running error path, not delete the rest silently.
- `X` on a grouped list must mark only session rows on the current paginator page, never group headers.
- Chinese `?` overlay must not overflow the modal at 40x12; truncation with `…` is expected, a torn border is not.
- Old muscle memory `ctrl+t` with marks must still open the batch flow; `ctrl+t` with no marks must still hint `x`-to-mark.

---

### Task 1: Verb keys `r` / `d` / `s` with hidden aliases

**Files:**
- Modify: `internal/tui/tui_update.go` (`updateList`, cases `r`, `ctrl+r`, `ctrl+d`, `ctrl+t`)
- Test: `internal/tui/` (extend `marked_test.go` or add `shortcut_keys_test.go`)

**Interfaces:**
- Consumes: `m.startBatch()`, `m.deleteConfirm()`, single-rename box setup, `refreshIndexCmd`
- Produces: new `case "r"` (rename single-or-marked), `case "d"` (delete single-or-marked), `case "s"` (refresh); old cases kept as aliases delegating to the same branches

- [ ] **Step 1: Write the failing test**

```go
func TestRenameKeyOpensBatchWhenMarked(t *testing.T) {
    // marked non-empty + key "r" -> overlayBatchTitle
    // marked empty + key "r" -> overlayRename
    // key "d" with marks -> overlayDelete with deleteBatch set
    // key "s" -> indexing && loading true (refresh), key "r" no longer refreshes
    // keys "ctrl+r"/"ctrl+d"/"ctrl+t" still reach the same overlays
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestRenameKeyOpensBatchWhenMarked -v`
Expected: FAIL (cases `r`/`d`/`s` do not exist yet)

- [ ] **Step 3: Implement key changes in `internal/tui/tui_update.go`**

Move the existing single-rename block from `case "ctrl+r"` to `case "r"`; if `len(m.marked) > 0` call `m.startBatch()` instead (same rule as `m`/`a`/`→`). Move the `case "ctrl+d"` body to `case "d"`. Move the refresh body from `case "r"` to `case "s"`. Keep `case "ctrl+r", "ctrl+t"` → rename branch and `case "ctrl+d"` → delete branch as aliases, plus `case "R"`? No — do not add `R`. Batch-overlay-local `r` (retry) is untouched (different `updateBatchOverlay` switch).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/tui_update.go internal/tui/shortcut_keys_test.go
git commit -m "feat(tui): unify rename/delete on r/d, refresh on s"
```

### Task 2: `X` marks viewport only

**Files:**
- Modify: `internal/tui/tui_update.go` (`case "X"`)
- Test: `internal/tui/shortcut_keys_test.go` (same file as Task 1)

**Interfaces:**
- Consumes: `m.sessions.VisibleItems()` + `m.sessions.Paginator.GetSliceBounds()` for the current page slice; existing `m.markStatus()`
- Produces: `X` toggles marks for session rows on the current paginator page only (group headers skipped); repeated `X` on other pages accumulates; `esc` still clears all

- [ ] **Step 1: Write the failing test**

```go
func TestMarkAllOnlyMarksViewport(t *testing.T) {
    // list with more rows than fit one page; X -> only current-page session IDs marked
    // group headers on the page are never marked; second X clears the page again
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestMarkAllOnlyMarksViewport -v`
Expected: FAIL (X marks all `Items()`)

- [ ] **Step 3: Implement viewport-scoped `X` in `internal/tui/tui_update.go`**

Replace the `m.sessions.Items()` loop with the current-page slice (`GetSliceBounds(len(VisibleItems()))`); toggle semantics unchanged (all-marked-on-page → clear page, else mark page). Note in code comment: accumulation across pages via repeated `X` + scroll replaces the old load-all behavior.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/tui_update.go internal/tui/shortcut_keys_test.go
git commit -m "feat(tui): X marks viewport only"
```

### Task 3: `?` overlay, copy (EN/ZH), footer hints

**Files:**
- Modify: `internal/tui/tui_view.go` (`keyHelpColumns`, session group: always render rename/archive/relocate/delete rows, grey when unsupported)
- Modify: `internal/tui/strings.go` (`englishText`, `chineseText`: new/changed `helpKey*`, `helpList*`, `markedFmt`, batch `ctrl+t` retry hints → `r`)
- Test: `internal/tui/` golden/render test for `helpModalView` (both languages)

**Interfaces:**
- Consumes: `m.selectedSessionCapabilities()` for grey-out state
- Produces: `?` rows — Browse adds `s` refresh, `?`, `esc`, `u`, `c`; Session shows `r`/`a`/`m`/`d` with `(unsupported)` suffix instead of disappearing; Batch notes `X` viewport-only and `r` batch-rename
  (SUPERSEDED at implementation: capability-filtered hiding kept per repo
  AGENTS.md session-management rule + locking test
  TestHelpShowsOnlySelectedAgentCapabilities; see ledger Ruling.)

- [ ] **Step 1: Write the failing test**

```go
func TestHelpListsNewKeys(t *testing.T) {
    // English + Chinese helpBody() contains "r", "d", "s", "X", "c", "u", "esc"
    // and no longer advertises "ctrl+r"/"ctrl+d"/"ctrl+t"
    // unsupported provider still renders the row (grey), not hidden
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestHelpListsNewKeys -v`
Expected: FAIL

- [ ] **Step 3: Implement overlay + copy in `tui_view.go` / `strings.go`**

Exact new rows (EN): `s` refresh · `r` rename it (marked set if any) · `d` delete it (marked set if any) · `X` mark viewport shown · `c` copy resume command · `u` undo archive/delete · `esc` clear marks/close · `?` this keymap. ZH mirrors existing translations (`s` 刷新，`r` 重命名，`d` 删除，`X` 只标记当前屏，`c` 复制 resume 命令，`u` 撤销，`esc` 清除标记）. Update `markedFmt` and batch retry strings from `ctrl+t` to `r`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/tui_view.go internal/tui/strings.go internal/tui/shortcut_keys_test.go
git commit -m "feat(tui): complete keymap overlay and copy"
```
