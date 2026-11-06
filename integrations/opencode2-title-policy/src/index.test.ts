import assert from "node:assert/strict"
import test, { after, before } from "node:test"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import plugin from "./index.ts"
import { mmdd, retryAttempts, type Mark } from "./policy.ts"

const configRoot = mkdtempSync(join(tmpdir(), "another-title-test-"))
const previous = { config: process.env.XDG_CONFIG_HOME, state: process.env.XDG_STATE_HOME }
before(() => {
  mkdirSync(join(configRoot, "another"))
  writeFileSync(join(configRoot, "another", "config.json"), JSON.stringify({ title_policy: { language: "en" } }))
  process.env.XDG_CONFIG_HOME = configRoot
  process.env.XDG_STATE_HOME = configRoot
})
after(() => {
  for (const [key, value] of [["XDG_CONFIG_HOME", previous.config], ["XDG_STATE_HOME", previous.state]]) {
    if (value === undefined) delete process.env[key!]
    else process.env[key!] = value
  }
  rmSync(configRoot, { recursive: true, force: true })
})

type Location = { directory: string; workspaceID?: string }
type Message = { id: string; type: string; text?: string; files?: { mime: string; name: string }[]; content?: { type: string; text: string }[] }
type Event = { type: string; location?: Location; data: { sessionID: string; title?: string } }
type TitleEvent = { sessionID: string; model: { providerID: string; id: string }; result?: string }
type Answer = string | Error | (() => Promise<string>)
const today = () => mmdd(Date.now())!
const yesterday = () => mmdd(Date.now() - 86400000)!
const user = (id: string, text = "Fix automatic session titles"): Message => ({ id, type: "user", text })
const pause = () => new Promise((resolve) => setTimeout(resolve, 15))
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

async function harness(options: {
  title?: string; answer?: Answer | Answer[]; created?: number; parentID?: string; mark?: Mark;
  titleModel?: { providerID: string; id: string; variant?: string };
  location?: Location; sessionLocation?: Location; messages?: Message[]; storage?: Map<string, unknown>;
} = {}) {
  const location = options.location ?? { directory: configRoot }
  const session = { title: options.title ?? "", location: options.sessionLocation ?? location,
    parentID: options.parentID, time: { created: options.created ?? Date.now() } }
  const state = { messages: options.messages ?? [user("msg_1")], answer: options.answer ?? "Fix｜Automatic title recovery" }
  const storage = options.storage ?? new Map<string, unknown>()
  if (options.mark) storage.set("title-mark:ses_test", options.mark)
  const updates: string[] = []
  const prompts: string[] = []
  const created: { id: string; agent: string; model: unknown; location: Location }[] = []
  const removed: string[] = []
  let titleHook!: (event: TitleEvent) => Promise<void>
  let reads = 0
  const queue: Event[] = []
  let wake: (() => void) | undefined
  const emit = (event: Partial<Event> = {}) => {
    queue.push({ type: "session.idle", location, data: { sessionID: "ses_test" }, ...event })
    wake?.()
  }
  const ctx = {
    location,
    model: { default: async () => ({ data: { providerID: "fallback", id: "default" } }) },
    agent: {
      transform: async () => ({ dispose: async () => {} }),
      list: async () => ({ data: options.titleModel ? [{ id: "title", model: options.titleModel }] : [] }),
    },
    session: {
      create: async (input: { agent: string; model: unknown; location: Location }) => {
        const temporary = { ...input, id: `ses_temporary_${created.length}` }
        created.push(temporary)
        return temporary
      },
      remove: async ({ sessionID }: { sessionID: string }) => { removed.push(sessionID) },
      generate: async ({ sessionID, prompt }: { sessionID: string; prompt: string }) => {
        assert.ok(created.some((session) => session.id === sessionID), "generation must use an isolated session")
        prompts.push(prompt)
        const answers = Array.isArray(state.answer) ? state.answer : [state.answer]
        const answer = answers[Math.min(prompts.length - 1, answers.length - 1)]
        if (answer instanceof Error) throw answer
        return { text: typeof answer === "function" ? await answer() : answer }
      },
      hook: async (_kind: string, callback: typeof titleHook) => { titleHook = callback; return { dispose: async () => {} } },
      get: async () => { reads++; return { ...session } },
      wait: async () => {},
      context: async () => state.messages,
      update: async ({ title }: { title: string }) => {
        session.title = title
        updates.push(title)
        emit({ type: "session.renamed", data: { sessionID: "ses_test", title } })
      },
    },
    storage: {
      get: async (key: string) => storage.get(key),
      set: async (key: string, value: unknown) => { storage.set(key, value) },
    },
    generate: {
      text: async () => { assert.fail("standalone generation bypasses authentication hooks") },
    },
    event: {
      subscribe: async function* ({ signal }: { signal: AbortSignal }) {
        while (!signal.aborted) {
          const event = queue.shift()
          if (event) { yield event; continue }
          await new Promise<void>((resolve) => {
            const done = () => { signal.removeEventListener("abort", done); resolve() }
            wake = done
            signal.addEventListener("abort", done, { once: true })
          })
        }
      },
    },
  }
  const cleanup = await plugin.setup(ctx as never)
  const title = async () => {
    const event: TitleEvent = { sessionID: "ses_test", model: { providerID: "test", id: "title" } }
    await titleHook(event)
    if (event.result !== undefined) {
      session.title = event.result
      emit({ type: "session.renamed", data: { sessionID: "ses_test", title: event.result } })
    }
    await pause()
    return event.result
  }
  const idle = async (event: Partial<Event> = {}) => { emit(event); await pause() }
  const mark = () => storage.get("title-mark:ses_test") as Mark | undefined
  const rename = async (title: string) => {
    session.title = title
    await idle({ type: "session.renamed", data: { sessionID: "ses_test", title } })
  }
  return { title, state, session, prompts, created, removed, updates, reads: () => reads, storage, mark, idle, emit, rename, cleanup }
}

test("native titles are dated and owned before persistence, with one successful call", async () => {
  const f = await harness({ answer: "Research｜OpenCode title dates", created: Date.parse("2026-09-15T23:30:00Z") })
  try {
    assert.equal(await f.title(), "0916｜Research｜OpenCode title dates")
    assert.equal(f.prompts.length, 1)
    assert.equal(f.created[0].agent, "title")
    assert.deepEqual(f.created[0].model, { providerID: "test", id: "title" })
    assert.deepEqual(f.created[0].location, f.session.location)
    assert.deepEqual(f.removed, f.created.map((session) => session.id))
    assert.equal(f.mark()?.title, f.session.title)
    assert.equal(f.mark()?.provisional, false)
    assert.equal(f.mark()?.manual, undefined)
  } finally { await f.cleanup() }
})

test("child sessions keep their native names on both title and idle paths", async () => {
  const f = await harness({ parentID: "ses_parent" })
  try {
    assert.equal(await f.title(), undefined)
    await f.idle()
    assert.equal(f.prompts.length, 0)
  } finally { await f.cleanup() }
})

for (const variant of [undefined, "high"]) test(`configured title model wins over native selection: ${variant ?? "default"}`, async () => {
  const model = { providerID: "gateway", id: "configured", ...(variant ? { variant } : {}) }
  const f = await harness({ titleModel: model })
  try {
    await f.title()
    assert.deepEqual(f.created[0].model, model)
    assert.deepEqual(f.removed, f.created.map((session) => session.id))
  } finally { await f.cleanup() }
})

for (const validAt of [1, 3]) test(`language correction succeeds on corrective retry ${validAt}`, async () => {
  const f = await harness({ answer: [...Array(validAt).fill("Fix｜Argon流量未转发至sub2api"), "Fix｜Argon traffic not forwarded to sub2api"] })
  try {
    assert.equal(await f.title(), `${today()}｜Fix｜Argon traffic not forwarded to sub2api`)
    assert.equal(f.prompts.length, validAt + 1)
    assert.equal(f.mark()?.provisional, false)
  } finally { await f.cleanup() }
})

for (const original of ["not a title", "Fix｜Argon流量未转发至sub2api"]) test(`retained original recovers on the next completed turn: ${original}`, async () => {
  const f = await harness({ answer: [original, "KEEP", "none", "N/A", "Fix｜Recovered title"] })
  try {
    assert.equal(await f.title(), `${today()}｜${original}`)
    assert.equal(f.prompts.length, 4)
    assert.equal(f.mark()?.provisional, true)
    await f.idle({ type: "session.execution.succeeded" })
    assert.equal(f.session.title, `${today()}｜Fix｜Recovered title`)
    assert.equal(f.prompts.length, 5)
    assert.equal(f.mark()?.provisional, false)
  } finally { await f.cleanup() }
})

test("provider failure becomes an owned fallback and recovers without three new user turns", async () => {
  const f = await harness({ answer: new Error("gateway unavailable") })
  try {
    assert.equal(await f.title(), `${today()}｜Explore｜Untitled session`)
    assert.equal(f.mark()?.provisional, true)
    assert.deepEqual(f.created.map((session) => session.model), [{ providerID: "test", id: "title" }, { providerID: "fallback", id: "default" }])
    assert.deepEqual(f.removed, f.created.map((session) => session.id))
    f.state.answer = "Fix｜Gateway title recovery"
    await f.idle()
    assert.equal(f.session.title, `${today()}｜Fix｜Gateway title recovery`)
    assert.equal(f.mark()?.provisional, false)
    assert.match(readFileSync(join(configRoot, "another", "opencode-title-policy.log"), "utf8"), /generation_failed.*ses_test.*gateway unavailable/)
  } finally { await f.cleanup() }
})

test("duplicate idle/completion events and reloads respect the bounded retry budget", async () => {
  const f = await harness({ answer: new Error("offline") })
  try {
    await f.title()
    for (let i = 1; i <= retryAttempts; i++) {
      f.state.messages = Array.from({ length: i }, (_, index) => user(`msg_${index + 1}`))
      f.emit({ type: "session.execution.succeeded" })
      await f.idle()
      const calls = f.prompts.length
      await f.idle({ type: "session.execution.succeeded" })
      assert.equal(f.prompts.length, calls, "one recovery attempt per turn")
      assert.equal(f.mark()?.attempts, i)
    }
    await f.cleanup()
    const reloaded = await harness({ title: f.session.title, storage: f.storage, answer: "Fix｜Recovery after cooldown",
      messages: Array.from({ length: 4 }, (_, i) => user(`msg_${i + 1}`)) })
    try {
      await reloaded.idle()
      assert.equal(reloaded.prompts.length, 0)
      reloaded.state.messages = Array.from({ length: 6 }, (_, i) => user(`msg_${i + 1}`))
      await reloaded.idle()
      assert.equal(reloaded.session.title, `${today()}｜Fix｜Recovery after cooldown`)
      assert.equal(reloaded.mark()?.provisional, false)
    } finally { await reloaded.cleanup() }
  } finally { await f.cleanup() }
})

for (const partial of ["Explore｜Untitled session", "not a title"]) test(`legacy owned provisional titles also recover: ${partial}`, async () => {
  const title = `${today()}｜${partial}`
  const f = await harness({ title, mark: { title, userTurns: 1, at: Date.now() } })
  try { await f.idle(); assert.equal(f.prompts.length, 1); assert.equal(f.mark()?.provisional, false) }
  finally { await f.cleanup() }
})

test("normal titles are adopted, refreshed after three more turns, and redated on a later day", async () => {
  const title = `${today()}｜Fix｜Stripe retries`
  const f = await harness({ title })
  try {
    await f.idle()
    assert.equal(f.prompts.length, 0)
    f.state.messages.push(user("msg_2"), user("msg_3"))
    await f.idle()
    assert.equal(f.prompts.length, 0)
    f.state.messages.push(user("msg_4"))
    await f.idle()
    assert.equal(f.prompts.length, 1)
    assert.equal(f.session.title, `${today()}｜Fix｜Automatic title recovery`)
    assert.equal(f.mark()?.manual, undefined)
  } finally { await f.cleanup() }
  const old = `${yesterday()}｜Fix｜Stripe retries`
  const later = await harness({ title: old, answer: "Fix｜Stripe retries", mark: { title: old, userTurns: 1, at: Date.now() - 86400000 } })
  try { await later.idle(); assert.equal(later.session.title, title); assert.equal(later.prompts.length, 1) }
  finally { await later.cleanup() }
})

test("a failed periodic refresh can retry on the next turn", async () => {
  const title = `${yesterday()}｜Fix｜Stripe retries`
  const f = await harness({ title, answer: new Error("unavailable") })
  try {
    await f.idle()
    assert.equal(f.session.title, title)
    assert.equal(f.mark()?.provisional, true)
    f.state.answer = "Fix｜Stripe timeout"
    f.state.messages.push(user("msg_2"))
    await f.idle()
    assert.equal(f.session.title, `${today()}｜Fix｜Stripe timeout`)
  } finally { await f.cleanup() }
})

for (const incoming of ["My manual title", "Fix｜My chosen topic", `${today()}｜Explore｜Untitled session`]) {
  test(`manual rename cancels provisional ownership: ${incoming}`, async () => {
    const f = await harness({ answer: "not a title" })
    try {
      await f.title()
      await f.rename(incoming)
      const expected = f.session.title
      const calls = f.prompts.length
      assert.equal(f.mark()?.manual, true)
      await f.idle()
      assert.equal(f.prompts.length, calls)
      assert.equal(f.session.title, expected)
    } finally { await f.cleanup() }
  })
}

test("a policy-shaped manual title without an old mark remains manual", async () => {
  const f = await harness()
  try {
    await f.rename("Fix｜My chosen topic")
    await f.idle()
    assert.equal(f.session.title, `${today()}｜Fix｜My chosen topic`)
    assert.equal(f.mark()?.manual, true)
    assert.equal(f.prompts.length, 0)
  } finally { await f.cleanup() }
})

test("only the owning location handles global idle and rename events, even without event location", async () => {
  const a = { directory: join(configRoot, "project-a") }
  const b = { directory: join(configRoot, "project-b") }
  const title = `${yesterday()}｜Fix｜Stripe retries`
  const own = await harness({ title, location: a })
  const foreign = await harness({ title, location: b, sessionLocation: a })
  try {
    await Promise.all([own.idle({ location: a }), foreign.idle({ location: a })])
    assert.equal(own.prompts.length, 1)
    assert.equal(foreign.prompts.length, 0)
    assert.equal(foreign.reads(), 0, "foreign events are filtered before session IO")
    await foreign.idle({ location: undefined })
    foreign.session.title = "Fix｜Manual partial title"
    await foreign.idle({ location: undefined, type: "session.renamed", data: { sessionID: "ses_test", title: foreign.session.title } })
    assert.equal(foreign.prompts.length, 0)
    assert.deepEqual(foreign.updates, [])
  } finally { await own.cleanup(); await foreign.cleanup() }
})

test("workspace identity is part of location ownership", async () => {
  const f = await harness({ title: `${yesterday()}｜Fix｜Stripe retries`, location: { directory: configRoot, workspaceID: "workspace-a" },
    sessionLocation: { directory: configRoot, workspaceID: "workspace-b" } })
  try { await f.idle({ location: undefined }); assert.equal(f.prompts.length, 0) }
  finally { await f.cleanup() }
})

test("screenshot-only titles retry with the assistant explanation and attachment metadata", async () => {
  const f = await harness({ messages: [{ id: "msg_image", type: "user", text: '"Image 1"', files: [{ mime: "image/png", name: "screenshot.png" }] }],
    answer: "Explore｜Image 1" })
  try {
    await f.title()
    assert.equal(f.mark()?.provisional, true)
    assert.match(f.prompts[0], /screenshot.png \(image\/png\)/)
    f.state.messages.push({ id: "msg_reply", type: "assistant", content: [{ type: "text", text: "This screenshot shows a Ghostty fullscreen issue after unplugging the external monitor." }] })
    f.state.answer = "Fix｜Ghostty fullscreen after monitor disconnect"
    await f.idle({ type: "session.execution.succeeded" })
    assert.match(f.prompts.at(-1)!, /Ghostty fullscreen issue/)
    assert.equal(f.session.title, `${today()}｜Fix｜Ghostty fullscreen after monitor disconnect`)
    assert.equal(f.mark()?.provisional, false)
    await f.idle()
    assert.equal(f.prompts.length, 2)
  } finally { await f.cleanup() }
})

for (const action of ["rename", "move", "dispose"] as const) test(`late generation cannot overwrite a ${action}`, async () => {
  const result = deferred<string>()
  const started = deferred<void>()
  const f = await harness({ title: `${yesterday()}｜Fix｜Stripe retries`, answer: async () => { started.resolve(); return result.promise } })
  try {
    f.emit()
    await started.promise
    if (action === "rename") await f.rename("Human choice")
    if (action === "move") f.session.location = { directory: join(configRoot, "moved") }
    if (action === "dispose") await f.cleanup()
    result.resolve("Fix｜Late answer")
    await pause()
    assert.deepEqual(f.updates, [])
    assert.deepEqual(f.removed, f.created.map((session) => session.id), "late or aborted work still removes temporary sessions")
    assert.equal(f.session.title, action === "rename" ? "Human choice" : `${yesterday()}｜Fix｜Stripe retries`)
  } finally { await f.cleanup() }
})

test("completion waits for a slow native title before retrying its fallback", async () => {
  const native = deferred<string>()
  const started = deferred<void>()
  const f = await harness({ answer: async () => { started.resolve(); return native.promise } })
  try {
    const title = f.title()
    await started.promise
    f.emit({ type: "session.execution.succeeded" })
    await pause()
    assert.equal(f.prompts.length, 1)
    f.state.answer = "Fix｜Recovered slow title"
    native.resolve("Explore｜Untitled session")
    await title
    const deadline = Date.now() + 6000
    while (f.session.title !== `${today()}｜Fix｜Recovered slow title` && Date.now() < deadline) await pause()
    assert.equal(f.session.title, `${today()}｜Fix｜Recovered slow title`)
    assert.equal(f.prompts.length, 2)
  } finally { await f.cleanup() }
})
