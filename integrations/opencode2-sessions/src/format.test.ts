import assert from "node:assert/strict"
import { test } from "node:test"
import { commentHeader, commentLayout, commentLineCount, directoryTail, providerName, scopeDirectory, sessionOption, textWidth, truncate } from "./format.ts"

test("directory tails retain three segments, handle native Windows separators, and cap CJK cells", () => {
  assert.equal(directoryTail("/Users/person/Documents/sync/GitHub/another"), "sync/GitHub/another")
  assert.equal(directoryTail("C:\\Users\\person\\Documents\\sync\\GitHub\\another"), "sync\\GitHub\\another")
  assert.equal(directoryTail("/one/two/three/four"), "two/three/four")
  const tail = directoryTail(`/Users/person/很长的工作区目录/项目资料/${"会话".repeat(20)}`)
  assert.ok(textWidth(tail) <= 28)
  assert.ok(tail.startsWith("…")); assert.ok(tail.endsWith("会话"))
  assert.equal(textWidth("会话 e\u0301"), 6)
  assert.ok(textWidth(truncate("会话标题".repeat(8), 17)) <= 17)
})

test("project footers omit current directory and use relative children/worktrees without prefix collisions", () => {
  assert.equal(scopeDirectory("/repo", "/repo", false), "")
  assert.equal(scopeDirectory("/repo/packages/api", "/repo", false), "packages/api")
  assert.equal(scopeDirectory("/repo-old", "/repo", false), "../repo-old")
  assert.equal(scopeDirectory("c:\\repo", "C:\\Repo", false), "")
  assert.equal(scopeDirectory("C:\\repo\\pkg\\api", "C:\\repo", false), "pkg\\api")
  assert.equal(scopeDirectory("/Users/p/sync/GitHub/another", "/repo", true), "sync/GitHub/another")
})

test("provider footer columns and marker slots are fixed-width and never appended descriptions", () => {
  const source = { id: "ses_one", provider: "opencode", title: "A very long title ".repeat(12) }
  for (const width of [100, 200]) {
    const first = sessionOption(source, "sync/GitHub/another", true, true, true, width)
    const second = sessionOption({ ...source, provider: "claude-code" }, "pkg/api", true, false, false, width)
    assert.equal(textWidth(first.footer), textWidth(second.footer))
    assert.equal(textWidth(first.footer), 40)
    assert.ok(first.footer.startsWith("[OpenCode]")); assert.ok(second.footer.startsWith("[Claude]"))
    assert.ok(first.title.startsWith("● 盯  "))
    assert.ok(second.title.startsWith("      "))
    assert.ok(first.title.endsWith("…")); assert.ok(second.title.endsWith("…"))
    assert.ok(textWidth(first.title) + textWidth(first.footer) <= Math.min(116, width - 2) - 12)
    assert.ok(!("description" in first))
  }
  const current = sessionOption({ ...source, title: "Title" }, "", false, false, false, 100)
  assert.equal(current.footer, "[OpenCode]")
  assert.equal(providerName("claude-code"), "Claude")
})

test("comment headers preserve provider/path and truncate both lines by display cells", () => {
  for (const width of [84, 48]) {
    const header = commentHeader("长标题".repeat(40), "Destination ".repeat(30), "opencode", "/Users/p/sync/GitHub/another", width)
    assert.ok(header.title.startsWith("Reference into "))
    assert.ok(header.title.endsWith("…"))
    assert.ok(textWidth(header.title) <= width)
    assert.ok(textWidth(header.source) <= width)
    assert.ok(header.source.endsWith(" · OpenCode · sync/GitHub/another"))
  }
  assert.equal(commentHeader("Source", "", "pi", "", 84).title, "Reference into New session")
})

test("comment field has a real five-row minimum, grows to twelve and respects terminal height", () => {
  for (const [columns, rows] of [[200, 120], [100, 40]]) {
    assert.equal(commentLayout(columns, rows).fieldRows, 5)
    assert.equal(commentLayout(columns, rows, 8).fieldRows, 8)
    assert.equal(commentLayout(columns, rows, 40).fieldRows, 12)
    assert.equal(commentLayout(columns, rows).width, 84)
  }
  assert.ok(commentLayout(40, 17, 20).fieldRows <= 3)
  assert.equal(commentLineCount(Array(8).fill("line").join("\n"), 82), 8)
  assert.equal(commentLineCount("会话".repeat(12), 12), 4)
  assert.equal(commentLayout(100, 40, commentLineCount(Array(8).fill("line").join("\n"), 82)).fieldRows, 8)
})
