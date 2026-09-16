import assert from "node:assert/strict"
import test from "node:test"
import plugin from "./index.ts"

type TitleEvent = { sessionID: string; model: { providerID: string; id: string }; result?: string }

async function harness(answer: string, created: number, parentID?: string) {
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
        return { text: answer }
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
  const fixture = await harness("研究｜OpenCode标题日期", Date.parse("2026-09-15T23:30:00Z"))
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await fixture.title(event)
    assert.equal(event.result, "0916｜研究｜OpenCode标题日期")
    assert.equal(fixture.calls(), 1)
  } finally {
    await fixture.cleanup()
  }
})

test("invalid model answer gets a dated fallback; child sessions retain native titles", async () => {
  const created = Date.parse("2026-09-15T23:30:00Z")
  const invalid = await harness("not a title", created)
  try {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await invalid.title(event)
    assert.equal(event.result, "0916｜Explore｜Untitled session")
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
