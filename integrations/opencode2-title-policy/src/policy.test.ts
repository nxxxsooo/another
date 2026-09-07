import assert from "node:assert/strict"
import test from "node:test"
import { fallbackTitle, finalizeTitle, isRefusal, loadLanguage, parsePartialTitle, titlePrompt } from "./policy.ts"

test("prompt is strict and language-specific", () => {
  assert.match(titlePrompt("en"), /Feature Design Fix Optimize Release Explore Docs Research/)
  assert.match(titlePrompt("zh"), /功能 设计 修复 优化 发布 探索 文档 研究/)
  assert.match(titlePrompt("auto"), /first user message contains any Han character/)
})

// OpenCode 2 names the session with whatever the title agent returns. Offering
// the model an escape hatch it does not understand is how empty sessions ended
// up named KEEP, so the prompt must never hand out a word to fall back on.
test("prompt offers no refusal word", () => {
  for (const language of ["auto", "en", "zh"] as const) {
    assert.doesNotMatch(titlePrompt(language), /KEEP/)
    assert.match(titlePrompt(language), /Always output a title/)
  }
})

test("a refusal is recognized however it is written", () => {
  for (const value of ["KEEP", " keep ", "N/A", "none", "Untitled"]) assert.ok(isRefusal(value), value)
  for (const value of ["Fix｜KEEP alive header", "探索｜打招呼", "Keep-alive tuning"]) {
    assert.equal(isRefusal(value), false, value)
  }
})

test("a refused session is named, dated, and left alone afterwards", () => {
  const created = Date.parse("2026-09-04T23:30:00Z")
  assert.equal(fallbackTitle(created, "zh"), "0905｜探索｜未命名会话")
  assert.equal(fallbackTitle(created, "en"), "0905｜Explore｜Untitled session")
  assert.equal(fallbackTitle(created, "auto"), "0905｜Explore｜Untitled session")
  const fallback = fallbackTitle(created, "en")!
  assert.equal(isRefusal(fallback), false)
  assert.equal(parsePartialTitle(fallback), undefined)
})

test("finalizes English and Chinese with Shanghai creation date", () => {
  const created = Date.parse("2026-09-04T23:30:00Z")
  assert.equal(finalizeTitle("Fix｜Cancel batch naming", created, "en"), "0905｜Fix｜Cancel batch naming")
  assert.equal(finalizeTitle("修复｜取消批量命名", created, "zh"), "0905｜修复｜取消批量命名")
  assert.equal(finalizeTitle("修复｜取消批量命名", created, "en"), undefined)
})

test("rejects drift", () => {
  for (const value of ["Other｜Cleanup", "Fix|Cleanup", "KEEP", "Fix｜one two three four five six seven eight nine"]) {
    assert.equal(parsePartialTitle(value), undefined, value)
  }
})

// Titles that name real things mix scripts. Charging a Chinese budget for
// every letter of `Meeting Loop` is what used to leave sessions undated.
test("a mixed-script topic is measured in units of meaning", () => {
  for (const value of [
    "功能｜Meeting Loop会议待办总结",
    "功能｜隐藏重命名Agent的Session",
    "文档｜git cherry-pick 用途",
    "修复｜opencode2-title-policy 日期丢失",
  ]) {
    assert.ok(parsePartialTitle(value), value)
  }
  // A topic that genuinely rambles still fails, in either language.
  assert.equal(parsePartialTitle("修复｜这个标题一直在解释自己到底想说些什么内容完全停不下来"), undefined)
  assert.equal(parsePartialTitle("Fix｜one two three four five six seven eight nine"), undefined)
})

// The rename the plugin performs raises another rename event. A dated title
// must therefore read as nothing to do, or the plugin renames in a loop.
test("an already dated title is left alone", () => {
  const created = Date.parse("2026-09-04T23:30:00Z")
  assert.equal(parsePartialTitle("0905｜修复｜取消批量命名"), undefined)
  assert.equal(finalizeTitle("0905｜修复｜取消批量命名", created, "zh"), undefined)
})

test("missing config defaults to auto", () => {
  assert.equal(loadLanguage({ HOME: "/definitely/missing" }), "auto")
})
