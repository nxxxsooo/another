import assert from "node:assert/strict"
import test from "node:test"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import {
  cleanGenerated,
  decideRefresh,
  fallbackTitle,
  firstTitle,
  finalizeTitle,
  isPolicyTitle,
  isRefusal,
  loadLanguage,
  parseMark,
  parsePartialTitle,
  redate,
  refreshPrompt,
  refreshTurns,
  repairPrompt,
  repairTitle,
  retryAttempts,
  sameLocation,
  titleContext,
  titleDay,
  titlePrompt,
  topicOf,
} from "./policy.ts"

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

test("a fallback has a recognizable dated shape", () => {
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

test("English titles reject Chinese prose even with an English category", () => {
  const created = Date.parse("2026-10-14T00:00:00+08:00")
  const mixed = "Fix｜Argon流量未转发至sub2api"
  assert.equal(finalizeTitle(mixed, created, "en"), undefined)
  assert.equal(repairTitle(mixed, created, "en"), undefined)
  assert.equal(firstTitle(mixed, created, "en"), "1014｜Explore｜Untitled session")
  assert.equal(finalizeTitle("Fix｜Argon traffic not forwarded to sub2api", created, "en"),
    "1014｜Fix｜Argon traffic not forwarded to sub2api")
  assert.equal(finalizeTitle("修复｜Argon流量未转发至sub2api", created, "zh"),
    "1014｜修复｜Argon流量未转发至sub2api")
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
  const root = mkdtempSync(join(tmpdir(), "another-missing-config-"))
  try { assert.equal(loadLanguage({ XDG_CONFIG_HOME: root }), "auto") }
  finally { rmSync(root, { recursive: true, force: true }) }
})

// The repair reads a model's answer directly instead of a rename event, so it
// has to survive the packaging a model puts around one line of text.
test("a generated answer is read through its packaging", () => {
  assert.equal(cleanGenerated("Fix｜Cancel batch naming"), "Fix｜Cancel batch naming")
  assert.equal(cleanGenerated('\n"Fix｜Cancel batch naming"\n'), "Fix｜Cancel batch naming")
  assert.equal(cleanGenerated("```\n修复｜取消批量命名\n```"), "修复｜取消批量命名")
  assert.equal(cleanGenerated("「修复｜取消批量命名」"), "修复｜取消批量命名")
  assert.equal(cleanGenerated("   \n\n"), "")
})

test("a repaired session is dated, and an unusable answer is declined", () => {
  const created = Date.parse("2026-09-04T23:30:00Z")
  assert.equal(repairTitle("Fix｜Cancel batch naming", created, "en"), "0905｜Fix｜Cancel batch naming")
  assert.equal(repairTitle(" KEEP ", created, "zh"), "0905｜探索｜未命名会话")
  // Declined, not forced: the session stays nameless and the next idle retries.
  assert.equal(repairTitle("Here is a good title for you!", created, "auto"), undefined)
  assert.equal(repairTitle("", created, "auto"), undefined)
  // The repair must not re-date a title that already carries a date.
  assert.equal(repairTitle("0905｜修复｜取消批量命名", created, "zh"), undefined)
})

test("first-title hook always supplies a dated title, even for invalid model output", () => {
  const created = Date.parse("2026-09-15T23:30:00Z")
  assert.equal(firstTitle("研究｜Open Code重命名插件日期缺失", created, "auto"), "0916｜研究｜Open Code重命名插件日期缺失")
  assert.equal(firstTitle("探索｜分支安装是否过多", created, "auto"), "0916｜探索｜分支安装是否过多")
  assert.equal(firstTitle("Here is a title", created, "zh"), "0916｜探索｜未命名会话")
  assert.equal(firstTitle("", created, "auto"), "0916｜Explore｜Untitled session")
  assert.equal(firstTitle("研究｜日期缺失", 0, "auto"), undefined)
})

test("the repair prompt carries the opening message, cut short", () => {
  const prompt = repairPrompt(`  ${"话".repeat(3000)}  `)
  assert.match(prompt, /Name the session that begins with this message/)
  assert.ok(prompt.endsWith("话".repeat(2000)), "the opening message is cut, not carried whole")
})

// OpenCode 2 stores the answer verbatim, so a dated request asks for the
// finished title and an undated one keeps the date out of the answer.
test("a dated prompt asks for the finished title", () => {
  const dated = titlePrompt("zh", "0916")
  assert.match(dated, /exact form 0916｜Type｜Topic, starting with the date 0916/)
  assert.match(dated, /such as 0916｜探索｜打招呼/)
  assert.doesNotMatch(dated, /without a date/)
  assert.match(titlePrompt("en"), /exact form Type｜Topic \(without a date\)/)
})

test("the refresh rule matches another rename --if-stale", () => {
  const now = Date.parse("2026-09-02T02:00:00Z") // 10:00 on 09-02 in Shanghai
  const owned = "0902｜修复｜Stripe重试"
  const mark = { title: owned, userTurns: 2, at: now }
  // null means "no mark"; undefined would select the default.
  const decide = (title: string, userTurns: number, withMark: typeof mark | null = mark) =>
    decideRefresh({ title, mark: withMark ?? undefined, userTurns, now, language: "auto" }).plan
  assert.equal(decide(owned, 2), "skip")
  assert.equal(decide(owned, 4), "skip")
  assert.equal(decide(owned, 5), "refresh")
  assert.equal(decide(owned, 1), "adopt")
  assert.equal(decide(owned, 9, null), "adopt")
  assert.equal(decide("0901｜修复｜Stripe重试", 2, { ...mark, title: "0901｜修复｜Stripe重试" }), "refresh")
  assert.equal(decide("0901｜修复｜Stripe重试", 2, null), "refresh")
  assert.equal(decide("0902｜功能｜我自己起的", 20), "skip")
  assert.equal(decide("my own name", 20, null), "skip")
  assert.equal(refreshTurns, 3)
})

test("redate keeps the topic and only moves a policy date", () => {
  const late = Date.parse("2026-09-01T17:30:00Z") // 01:30 on 09-02 in Shanghai
  assert.equal(redate("0901｜修复｜Stripe重试", late), "0902｜修复｜Stripe重试")
  assert.equal(redate("my own name", late), "my own name")
  assert.equal(topicOf("0902｜Fix｜Stripe retries"), "Fix｜Stripe retries")
  assert.equal(titleDay("09a1｜Fix｜x"), undefined)
  assert.equal(isPolicyTitle("0902｜修复｜Stripe重试", "en"), false)
  assert.equal(isPolicyTitle("0902｜Fix｜错误", "en"), false)
})

test("a stored mark is read only when it has the expected shape", () => {
  assert.deepEqual(parseMark({ title: "t", userTurns: 1, at: 2 }), { title: "t", userTurns: 1, at: 2 })
  for (const value of [undefined, null, "t", { title: "", userTurns: 1, at: 2 }, { title: "t", userTurns: "1", at: 2 }]) {
    assert.equal(parseMark(value), undefined)
  }
})

test("the refresh prompt weighs the latest messages and never offers a refusal", () => {
  const users = Array.from({ length: 10 }, (_, i) => `message ${i}`)
  const prompt = refreshPrompt("0901｜修复｜Stripe重试", users)
  assert.match(prompt, /output its Type｜Topic unchanged/)
  assert.match(prompt, /"修复｜Stripe重试"/)
  assert.match(prompt, /message 0/)
  assert.match(prompt, /message 6[\s\S]*message 9/)
  assert.doesNotMatch(prompt, /message 3/)
  assert.doesNotMatch(prompt, /KEEP/)
})

test("provisional ownership takes precedence over title conformance, but never a manual rename", () => {
  const now = Date.parse("2026-11-05T02:00:00Z")
  for (const title of ["1105｜Explore｜Untitled session", "1105｜not a title", "1105｜Fix｜错误"]) {
    const mark = { title, userTurns: 1, at: now, provisional: true, attempts: 0 }
    const input = { title, mark, userTurns: 1, now, language: "en" as const, turn: "msg_1" }
    assert.equal(decideRefresh(input).plan, "retry")
    assert.equal(decideRefresh({ ...input, title: "Human title" }).plan, "skip")
    assert.equal(decideRefresh({ ...input, mark: { ...mark, manual: true } }).plan, "skip")
    assert.equal(decideRefresh({ ...input, mark: { ...mark, lastAttempt: "msg_1" } }).plan, "skip")
    const exhausted = { ...mark, attempts: retryAttempts, lastAttempt: "msg_previous" }
    assert.equal(decideRefresh({ ...input, mark: exhausted }).reason, "retry budget exhausted")
    assert.equal(decideRefresh({ ...input, mark: exhausted, userTurns: 4 }).plan, "retry")
    assert.equal(decideRefresh({ ...input, mark: exhausted, now: now + 86400000 }).plan, "retry")
  }
})

test("invalid persisted retry state is rejected at the storage boundary", () => {
  const mark = { title: "t", userTurns: 1, at: 2, provisional: true, attempts: 1, lastAttempt: "msg_1" }
  assert.deepEqual(parseMark(mark), mark)
  for (const invalid of [{ userTurns: NaN }, { at: Infinity }, { attempts: -1 }, { attempts: 0.5 }, { lastAttempt: {} }, { manual: "yes" }]) {
    assert.equal(parseMark({ ...mark, ...invalid }), undefined)
  }
})

test("context is bounded and excludes image bytes, URIs, tools, and reasoning", () => {
  const context = titleContext([
    { id: "msg_1", type: "user", text: "", files: [{ name: "capture.png", mime: "image/png", description: "Ghostty settings", data: "SECRET_IMAGE_BYTES", source: { uri: "https://secret.invalid" } }] },
    { id: "msg_2", type: "assistant", content: [{ type: "reasoning", text: "SECRET_REASONING" }, { type: "tool", text: "SECRET_TOOL" }, { type: "text", text: "Visible explanation " + "x".repeat(4000) }] },
  ] as Parameters<typeof titleContext>[0])
  assert.equal(context.users.length, 1)
  assert.match(context.users[0], /capture.png.*Ghostty settings/)
  assert.equal(context.needsContext, false)
  assert.equal(context.assistant.length, 1000)
  assert.doesNotMatch(JSON.stringify(context), /SECRET_|secret.invalid/)
  assert.equal(context.turn, "msg_1")
  assert.equal(titleContext([{ id: "msg_1", type: "user", text: "", files: [{ mime: "image/png" }] }]).needsContext, true)
  assert.equal(titleContext([{ type: "user", text: "Inspect this bug", files: [{ mime: "image/png" }] }]).needsContext, false)
  for (const text of ["Image 1", "[Image 1]", '"Image 1"', '""']) {
    assert.equal(titleContext([{ type: "user", text, files: [{ mime: "image/png" }] }]).needsContext, true)
  }
})

test("location matching is exact after native normalization and includes workspace identity", () => {
  const root = join(tmpdir(), "another-location-test")
  const location = { directory: root }
  assert.equal(sameLocation(location, { directory: join(root, ".") }), true)
  assert.equal(sameLocation(location, { directory: join(root, "child") }), false)
  assert.equal(sameLocation(location, { directory: root + "-sibling" }), false)
  assert.equal(sameLocation(location, { directory: join(root, "..", "outside") }), false)
  assert.equal(sameLocation(location, { directory: root, workspaceID: "different" }), false)
  if (process.platform === "win32") assert.equal(sameLocation(location, { directory: root.toUpperCase() }), true)
})
