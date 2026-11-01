import assert from "node:assert/strict"
import test, { after, before } from "node:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import plugin from "./index.ts"
import { mmdd } from "./policy.ts"

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

// refreshHarness drives the idle path: the test pushes idle events and reads
// what the plugin wrote to the session and to its own storage.
async function refreshHarness(options: { title: string; users: string[]; answer: string; mark?: unknown }) {
  const session = { parentID: undefined as string | undefined, title: options.title, time: { created: Date.now() } }
  const state = { users: options.users, answer: options.answer }
  const storage = new Map<string, unknown>()
  if (options.mark) storage.set("title-mark:ses_live", options.mark)
  const updates: string[] = []
  let calls = 0
  const queue: Array<{ type: string; data: { sessionID: string } }> = []
  let wake: (() => void) | undefined
  const ctx = {
    agent: {
      transform: async () => ({ dispose: async () => {} }),
      list: async () => ({ data: [] }),
    },
    session: {
      hook: async () => ({ dispose: async () => {} }),
      get: async () => ({ ...session }),
      context: async () => state.users.map((text) => ({ type: "user", text })),
      update: async ({ title }: { title: string }) => {
        session.title = title
        updates.push(title)
      },
    },
    storage: {
      get: async (key: string) => storage.get(key),
      set: async (key: string, value: unknown) => void storage.set(key, value),
    },
    generate: {
      text: async () => {
        calls++
        return { text: state.answer }
      },
    },
    event: {
      subscribe: async function* ({ signal }: { signal: AbortSignal }) {
        while (!signal.aborted) {
          const next = queue.shift()
          if (next) {
            yield next
            continue
          }
          await new Promise<void>((resolve) => {
            wake = resolve
            signal.addEventListener("abort", () => resolve(), { once: true })
          })
        }
      },
    },
  }
  const cleanup = await plugin.setup(ctx as never)
  const mark = () => storage.get("title-mark:ses_live") as { title: string; userTurns: number } | undefined
  // idle sends one idle event and waits until the plugin has settled on it.
  const idle = async (settled: () => boolean) => {
    queue.push({ type: "session.idle", data: { sessionID: "ses_live" } })
    wake?.()
    const deadline = Date.now() + 2000
    while (!settled()) {
      if (Date.now() > deadline) assert.fail("the plugin did not settle on the idle event")
      await new Promise((resolve) => setTimeout(resolve, 5))
    }
    // Let a write that would follow the awaited one surface before asserting.
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
  return { state, session, updates, calls: () => calls, mark, idle, cleanup }
}

const today = () => mmdd(Date.now())!
const yesterday = () => mmdd(Date.now() - 24 * 60 * 60 * 1000)!

test("a title the plugin wrote is adopted, then refreshed after three more user turns", async () => {
  const title = `${today()}｜Fix｜Stripe retries`
  const fixture = await refreshHarness({ title, users: ["fix the stripe retries", "ok"], answer: "Feature｜Dynamic session titles" })
  try {
    await fixture.idle(() => fixture.mark() !== undefined)
    assert.equal(fixture.mark()?.title, title)
    assert.equal(fixture.mark()?.userTurns, 2)
    assert.equal(fixture.calls(), 0)

    fixture.state.users = [...fixture.state.users, "two more turns"]
    await fixture.idle(() => true)
    assert.equal(fixture.calls(), 0, "two turns are not drift")

    fixture.state.users = [...fixture.state.users, "now the titles", "dynamic ones"]
    await fixture.idle(() => fixture.updates.length === 1)
    assert.deepEqual(fixture.updates, [`${today()}｜Feature｜Dynamic session titles`])
    assert.equal(fixture.mark()?.title, `${today()}｜Feature｜Dynamic session titles`)
    assert.equal(fixture.mark()?.userTurns, 5)
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

test("a session resumed on a later day is redated even when the topic is kept", async () => {
  const title = `${yesterday()}｜Fix｜Stripe retries`
  const fixture = await refreshHarness({
    title,
    users: ["fix the stripe retries", "continue"],
    answer: "Fix｜Stripe retries",
    mark: { title, userTurns: 1, at: Date.now() - 24 * 60 * 60 * 1000 },
  })
  try {
    await fixture.idle(() => fixture.updates.length === 1)
    assert.deepEqual(fixture.updates, [`${today()}｜Fix｜Stripe retries`])
    assert.equal(fixture.calls(), 1)
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

test("a title someone else set is never refreshed", async () => {
  const fixture = await refreshHarness({
    title: `${yesterday()}｜Feature｜My own name`,
    users: ["一", "二", "三", "四", "五"],
    answer: "Fix｜Must not appear",
    mark: { title: `${yesterday()}｜Fix｜Stripe retries`, userTurns: 1, at: Date.now() },
  })
  try {
    await fixture.idle(() => true)
    assert.equal(fixture.calls(), 0)
    assert.deepEqual(fixture.updates, [])
  } finally {
    await fixture.cleanup()
  }
})
