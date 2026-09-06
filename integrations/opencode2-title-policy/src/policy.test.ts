import assert from "node:assert/strict"
import test from "node:test"
import { finalizeTitle, loadLanguage, parsePartialTitle, titlePrompt } from "./policy.ts"

test("prompt is strict and language-specific", () => {
  assert.match(titlePrompt("en"), /Feature Design Fix Optimize Release Explore Docs Research/)
  assert.match(titlePrompt("zh"), /功能 设计 修复 优化 发布 探索 文档 研究/)
  assert.match(titlePrompt("auto"), /first user message contains any Han character/)
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

test("missing config defaults to auto", () => {
  assert.equal(loadLanguage({ HOME: "/definitely/missing" }), "auto")
})
