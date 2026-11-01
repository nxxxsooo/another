import assert from "node:assert/strict"
import test, { after, before } from "node:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import plugin from "./index.ts"

// Never let the developer's title-language setting select the test policy.
const configRoot = mkdtempSync(join(tmpdir(), "another-title-test-"))
const previousConfigRoot = process.env.XDG_CONFIG_HOME
before(() => {
  mkdirSync(join(configRoot, "another"))
  writeFileSync(join(configRoot, "another", "config.json"), JSON.stringify({ title_policy: { language: "en" } }))
  process.env.XDG_CONFIG_HOME = configRoot
})
after(() => {
  if (previousConfigRoot === undefined) delete process.env.XDG_CONFIG_HOME
  else process.env.XDG_CONFIG_HOME = previousConfigRoot
  rmSync(configRoot, { recursive: true, force: true })
})

type TitleEvent = { sessionID: string; model: { providerID: string; id: string }; result?: string }

async function harness(answer: string | (string | Error)[], created: number, parentID?: string) {
  let hook: ((event: TitleEvent) => Promise<void>) | undefined
  let calls = 0
  const ctx = {
    agent: {
      transform: async (edit: (editor: { update: (id: string, apply: (agent: { system: string }) => void) => void }) => void) => {
        edit({ update: (_id, apply) => apply({ system: "" }) })
        return { dispose: async () => {} }
      },
    },
    session: {
      hook: async (_kind: string, callback: (event: TitleEvent) => Promise<void>) => {
        hook = callback
        return { dispose: async () => {} }
      },
      get: async () => ({ parentID, title: "", time: { created } }),
      context: async () => [{ type: "user", text: "检查 OpenCode 标题日期" }],
    },
    generate: {
      text: async () => {
        calls++
        const value = Array.isArray(answer) ? answer[Math.min(calls - 1, answer.length - 1)] : answer
        if (value instanceof Error) throw value
        return { text: value }
      },
    },
    event: {
      subscribe: async function* ({ signal }: { signal: AbortSignal }) {
        await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }))
      },
    },
  }
  const cleanup = await plugin.setup(ctx as never)
  assert.ok(hook)
  return {
    title: hook,
    calls: () => calls,
    cleanup,
  }
}

test("first title is dated before native persistence without a second model call", async () => {
  const fixture = await harness("Research｜OpenCode title dates", Date.parse("2026-09-15T23:30:00Z"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "0916｜Research｜OpenCode title dates")
    assert.equal(fixture.calls(), 1)
  } finally {
    await fixture.cleanup()
  }
})

test("invalid model answer is retained after three retries; child sessions retain native titles", async () => {
  const created = Date.parse("2026-09-15T23:30:00Z")
  const invalid = await harness("not a title", created)
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await invalid.title(event)
    assert.equal(event.result, "0916｜not a title")
    assert.equal(invalid.calls(), 4)
  } finally {
    await invalid.cleanup()
  }
  const child = await harness("研究｜不应覆盖", created, "ses_parent")
  try {
    const event: TitleEvent = { sessionID: "ses_child", model: { providerID: "test", id: "title" } }
    await child.title(event)
    assert.equal(event.result, undefined)
    assert.equal(child.calls(), 0)
  } finally {
    await child.cleanup()
  }
})

test("language correction stops at the first valid retry", async () => {
  const fixture = await harness([
    "Fix｜Argon流量未转发至sub2api",
    "Fix｜Argon traffic not forwarded to sub2api",
  ], Date.parse("2026-10-14T00:00:00+08:00"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "1014｜Fix｜Argon traffic not forwarded to sub2api")
    assert.equal(fixture.calls(), 2)
  } finally {
    await fixture.cleanup()
  }
})

test("exhausted corrections retain the original answer rather than the last rejection", async () => {
  const fixture = await harness([
    "Fix｜Argon流量未转发至sub2api",
    "Fix｜仍然错误",
    "Fix｜再次错误",
    "Fix｜最后错误",
  ], Date.parse("2026-10-14T00:00:00+08:00"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "1014｜Fix｜Argon流量未转发至sub2api")
    assert.equal(fixture.calls(), 4)
  } finally {
    await fixture.cleanup()
  }
})

test("the third corrective retry can still supply the final title", async () => {
  const fixture = await harness([
    "Fix｜Argon流量未转发至sub2api",
    "Fix｜仍然错误",
    "Fix｜再次错误",
    "Fix｜Argon traffic not forwarded to sub2api",
  ], Date.parse("2026-10-14T00:00:00+08:00"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "1014｜Fix｜Argon traffic not forwarded to sub2api")
    assert.equal(fixture.calls(), 4)
  } finally {
    await fixture.cleanup()
  }
})

test("refused corrections preserve the original rejected title", async () => {
  const fixture = await harness([
    "Fix｜Argon流量未转发至sub2api", "KEEP", "none", "N/A",
  ], Date.parse("2026-10-14T00:00:00+08:00"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "1014｜Fix｜Argon流量未转发至sub2api")
    assert.equal(fixture.calls(), 4)
  } finally {
    await fixture.cleanup()
  }
})
