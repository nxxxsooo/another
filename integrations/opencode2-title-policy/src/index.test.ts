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
type Part = { type: string; text: string }
type RequestEvent = { sessionID: string; system: Part[]; messages: { role: string; content: Part[] }[]; tools?: Record<string, unknown> }
// An Error is a provider failure: OpenCode 2's own title request swallows it,
// and session.generate throws it.
type Answer = string | Error | (() => Promise<string>)
const today = () => mmdd(Date.now())!
const yesterday = () => mmdd(Date.now() - 86400000)!
const user = (id: string, text = "Fix automatic session titles"): Message => ({ id, type: "user", text })
const pause = () => new Promise((resolve) => setTimeout(resolve, 15))
async function until(condition: () => boolean, ms = 8000) {
  const deadline = Date.now() + ms
  while (!condition() && Date.now() < deadline) await pause()
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

async function harness(options: {
  title?: string; answer?: Answer | Answer[]; created?: number; parentID?: string; mark?: Mark;
  location?: Location; sessionLocation?: Location; messages?: Message[]; storage?: Map<string, unknown>;
} = {}) {
  const location = options.location ?? { directory: configRoot }
  const session = { title: options.title ?? "", location: options.sessionLocation ?? location,
    parentID: options.parentID, time: { created: options.created ?? Date.now() } }
  const state = { messages: options.messages ?? [user("msg_1")], answer: options.answer ?? "Fix｜Automatic title recovery" }
  const storage = options.storage ?? new Map<string, unknown>()
  if (options.mark) storage.set("title-mark:ses_test", options.mark)
  // updates are the plugin's own renames; requests are its own generations;
  // prompts, systems, and sent are what every model request carried.
  const updates: string[] = []
  const prompts: string[] = []
  const systems: string[] = []
  const sent: RequestEvent[] = []
  let requests = 0
  const hooks: Record<string, (event: RequestEvent) => Promise<void>> = {}
  let reads = 0
  const queue: Event[] = []
  let wake: (() => void) | undefined
  const emit = (event: Partial<Event> = {}) => {
    queue.push({ type: "session.idle", location, data: { sessionID: "ses_test" }, ...event })
    wake?.()
  }
  const ask = async (kind: string, event: RequestEvent) => {
    await hooks[kind]?.(event)
    sent.push(event)
    prompts.push(event.messages.map((message) => message.content.map((part) => part.text).join("")).join("\n"))
    systems.push(event.system.map((part) => part.text).join("\n"))
    const answers = Array.isArray(state.answer) ? state.answer : [state.answer]
    return answers[Math.min(prompts.length - 1, answers.length - 1)]
  }
  // native mirrors OpenCode 2's title request: the hook may rewrite it, usage
  // is recorded on an answer, and the answer's first line is stored only when
  // the title did not change meanwhile.
  const native = async () => {
    const before = session.title
    const answer = await ask("title", { sessionID: "ses_test", system: [{ type: "text", text: "native system" }],
      messages: [{ role: "user", content: [{ type: "text", text: "native prompt" }] }] })
    if (answer instanceof Error) return
    const text = typeof answer === "function" ? await answer() : answer
    emit({ type: "session.usage.updated" })
    const line = text.split("\n").map((part) => part.trim()).find(Boolean)
    if (!line || session.title !== before || session.title === line) return
    session.title = line
    emit({ type: "session.renamed", data: { sessionID: "ses_test", title: line } })
  }
  const ctx = {
    location,
    agent: { transform: async () => ({ dispose: async () => {} }) },
    session: {
      hook: async (kind: string, callback: (event: RequestEvent) => Promise<void>) => { hooks[kind] = callback; return { dispose: async () => {} } },
      // session.generate carries the agent's whole context unless a hook trims it.
      generate: async ({ sessionID, prompt }: { sessionID: string; prompt: string }) => {
        requests++
        const answer = await ask("generate", { sessionID, system: [{ type: "text", text: "agent instructions" }],
          messages: [{ role: "user", content: [{ type: "text", text: "earlier conversation" }] },
            { role: "user", content: [{ type: "text", text: prompt }] }], tools: { shell: {} } })
        if (answer instanceof Error) throw answer
        return { text: typeof answer === "function" ? await answer() : answer }
      },
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
  // title is OpenCode's own first title request, made after the first prompt.
  const title = async () => { await native(); await pause(); await pause(); return session.title }
  const idle = async (event: Partial<Event> = {}) => { emit(event); await pause(); await pause() }
  const mark = () => storage.get("title-mark:ses_test") as Mark | undefined
  const rename = async (title: string) => {
    session.title = title
    await idle({ type: "session.renamed", data: { sessionID: "ses_test", title } })
  }
  return { title, native, state, session, prompts, systems, sent, updates, requests: () => requests, reads: () => reads,
    storage, mark, idle, emit, rename, cleanup }
}

test("the first title rewrites OpenCode's own request and is final as answered", async () => {
  const created = Date.parse("2026-09-15T23:30:00Z")
  const f = await harness({ answer: "0916｜Research｜OpenCode title dates", created })
  try {
    assert.equal(await f.title(), "0916｜Research｜OpenCode title dates")
    assert.equal(f.prompts.length, 1)
    assert.match(f.prompts[0], /Name the session that begins with this message[\s\S]*Fix automatic session titles/)
    assert.match(f.systems[0], /exact form 0916｜Type｜Topic/)
    assert.deepEqual(f.updates, [], "an answer that already follows the policy is not renamed again")
    assert.equal(f.requests(), 0)
    assert.equal(f.mark()?.title, f.session.title)
    assert.equal(f.mark()?.provisional, false)
    assert.equal(f.mark()?.manual, undefined)
  } finally { await f.cleanup() }
})

test("an undated first answer is dated and owned", async () => {
  const f = await harness({ answer: "Research｜OpenCode title dates", created: Date.parse("2026-09-15T23:30:00Z") })
  try {
    assert.equal(await f.title(), "0916｜Research｜OpenCode title dates")
    assert.deepEqual(f.updates, ["0916｜Research｜OpenCode title dates"])
    assert.equal(f.mark()?.manual, undefined)
  } finally { await f.cleanup() }
})

test("child sessions keep their native names on both title and idle paths", async () => {
  const f = await harness({ parentID: "ses_parent", answer: "Subagent task" })
  try {
    assert.equal(await f.title(), "Subagent task")
    assert.equal(f.prompts[0], "native prompt")
    await f.idle()
    assert.equal(f.requests(), 0)
    assert.deepEqual(f.updates, [])
  } finally { await f.cleanup() }
})

for (const validAt of [1, 3]) test(`language correction succeeds on corrective retry ${validAt}`, async () => {
  const f = await harness({ answer: [...Array(validAt).fill("Fix｜Argon流量未转发至sub2api"), "Fix｜Argon traffic not forwarded to sub2api"] })
  try {
    await f.title()
    await until(() => f.mark() !== undefined)
    assert.equal(f.session.title, `${today()}｜Fix｜Argon traffic not forwarded to sub2api`)
    assert.equal(f.prompts.length, validAt + 1)
    assert.match(f.prompts.at(-1)!, /did not satisfy the title policy/)
    assert.equal(f.mark()?.provisional, false)
  } finally { await f.cleanup() }
})

for (const original of ["not a title", "Fix｜Argon流量未转发至sub2api"]) test(`retained original recovers on the next completed turn: ${original}`, async () => {
  const f = await harness({ answer: [original, "KEEP", "none", "N/A", "Fix｜Recovered title"] })
  try {
    await f.title()
    await until(() => f.mark() !== undefined)
    assert.equal(f.session.title, `${today()}｜${original}`)
    assert.equal(f.prompts.length, 4)
    assert.equal(f.mark()?.provisional, true)
    await f.idle({ type: "session.execution.succeeded" })
    await until(() => f.mark()?.provisional === false)
    assert.equal(f.session.title, `${today()}｜Fix｜Recovered title`)
    assert.equal(f.prompts.length, 5)
  } finally { await f.cleanup() }
})

test("a first title that never arrives becomes an owned fallback and recovers", async () => {
  const f = await harness({ answer: new Error("gateway unavailable") })
  try {
    assert.equal(await f.title(), "")
    await f.idle()
    await until(() => f.mark() !== undefined)
    assert.equal(f.session.title, `${today()}｜Explore｜Untitled session`)
    assert.equal(f.mark()?.provisional, true)
    assert.equal(f.requests(), 1)
    f.state.answer = "Fix｜Gateway title recovery"
    f.state.messages.push(user("msg_2"))
    await f.idle()
    await until(() => f.mark()?.provisional === false)
    assert.equal(f.session.title, `${today()}｜Fix｜Gateway title recovery`)
    assert.match(readFileSync(join(configRoot, "another", "opencode-title-policy.log"), "utf8"), /generation_failed.*ses_test.*gateway unavailable/)
  } finally { await f.cleanup() }
})

test("duplicate idle/completion events and reloads respect the bounded retry budget", async () => {
  const fallback = `${today()}｜Explore｜Untitled session`
  const f = await harness({ title: fallback, answer: new Error("offline"),
    mark: { title: fallback, userTurns: 1, at: Date.now(), provisional: true, attempts: 0 } })
  try {
    for (let i = 1; i <= retryAttempts; i++) {
      f.state.messages = Array.from({ length: i }, (_, index) => user(`msg_${index + 1}`))
      f.emit({ type: "session.execution.succeeded" })
      await f.idle()
      await until(() => f.mark()?.attempts === i)
      const calls = f.requests()
      await f.idle({ type: "session.execution.succeeded" })
      assert.equal(f.requests(), calls, "one recovery attempt per turn")
      assert.equal(f.mark()?.attempts, i)
    }
    await f.cleanup()
    const reloaded = await harness({ title: f.session.title, storage: f.storage, answer: "Fix｜Recovery after cooldown",
      messages: Array.from({ length: 4 }, (_, i) => user(`msg_${i + 1}`)) })
    try {
      await reloaded.idle()
      assert.equal(reloaded.requests(), 0)
      reloaded.state.messages = Array.from({ length: 6 }, (_, i) => user(`msg_${i + 1}`))
      await reloaded.idle()
      await until(() => reloaded.mark()?.provisional === false)
      assert.equal(reloaded.session.title, `${today()}｜Fix｜Recovery after cooldown`)
    } finally { await reloaded.cleanup() }
  } finally { await f.cleanup() }
})

for (const partial of ["Explore｜Untitled session", "not a title"]) test(`legacy owned provisional titles also recover: ${partial}`, async () => {
  const title = `${today()}｜${partial}`
  const f = await harness({ title, mark: { title, userTurns: 1, at: Date.now() } })
  try {
    await f.idle()
    await until(() => f.mark()?.provisional === false)
    assert.equal(f.requests(), 1)
  } finally { await f.cleanup() }
})

test("normal titles are adopted, refreshed after three more turns, and redated on a later day", async () => {
  const title = `${today()}｜Fix｜Stripe retries`
  const f = await harness({ title })
  try {
    await f.idle()
    assert.equal(f.requests(), 0)
    f.state.messages.push(user("msg_2"), user("msg_3"))
    await f.idle()
    assert.equal(f.requests(), 0)
    f.state.messages.push(user("msg_4"))
    await f.idle()
    await until(() => f.session.title === `${today()}｜Fix｜Automatic title recovery`)
    assert.equal(f.requests(), 1)
    const [refresh] = f.sent
    assert.match(refresh.system[0].text, new RegExp(`exact form ${today()}｜Type｜Topic`))
    assert.equal(refresh.messages.length, 1, "no conversation history leaves with a title request")
    assert.match(refresh.messages[0].content[0].text, /The session has continued since its current title was written/)
    assert.deepEqual(refresh.tools, {})
    assert.equal(f.mark()?.manual, undefined)
  } finally { await f.cleanup() }
  const old = `${yesterday()}｜Fix｜Stripe retries`
  const later = await harness({ title: old, answer: "Fix｜Stripe retries", mark: { title: old, userTurns: 1, at: Date.now() - 86400000 } })
  try {
    await later.idle()
    await until(() => later.session.title === title)
    assert.equal(later.requests(), 1)
    assert.equal(later.mark()?.title, title)
  } finally { await later.cleanup() }
})

test("a failed periodic refresh can retry on the next turn", async () => {
  const title = `${yesterday()}｜Fix｜Stripe retries`
  const f = await harness({ title, answer: new Error("unavailable") })
  try {
    await f.idle()
    await until(() => f.mark() !== undefined)
    assert.equal(f.session.title, title)
    assert.equal(f.mark()?.provisional, true)
    f.state.answer = "Fix｜Stripe timeout"
    f.state.messages.push(user("msg_2"))
    await f.idle()
    await until(() => f.session.title === `${today()}｜Fix｜Stripe timeout`)
    assert.equal(f.session.title, `${today()}｜Fix｜Stripe timeout`)
  } finally { await f.cleanup() }
})

for (const incoming of ["My manual title", "Fix｜My chosen topic", `${today()}｜Explore｜Untitled session`]) {
  test(`manual rename cancels provisional ownership: ${incoming}`, async () => {
    const f = await harness({ answer: "not a title" })
    try {
      await f.title()
      await until(() => f.mark() !== undefined)
      await f.rename(incoming)
      const expected = f.session.title
      const calls = f.requests()
      assert.equal(f.mark()?.manual, true)
      await f.idle()
      assert.equal(f.requests(), calls)
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
    assert.equal(f.requests(), 0)
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
    await until(() => own.requests() === 1)
    assert.equal(own.requests(), 1)
    assert.equal(foreign.requests(), 0)
    assert.equal(foreign.reads(), 0, "foreign events are filtered before session IO")
    await foreign.idle({ location: undefined })
    foreign.session.title = "Fix｜Manual partial title"
    await foreign.idle({ location: undefined, type: "session.renamed", data: { sessionID: "ses_test", title: foreign.session.title } })
    assert.equal(foreign.requests(), 0)
    assert.deepEqual(foreign.updates, [])
  } finally { await own.cleanup(); await foreign.cleanup() }
})

test("workspace identity is part of location ownership", async () => {
  const f = await harness({ title: `${yesterday()}｜Fix｜Stripe retries`, location: { directory: configRoot, workspaceID: "workspace-a" },
    sessionLocation: { directory: configRoot, workspaceID: "workspace-b" } })
  try { await f.idle({ location: undefined }); assert.equal(f.requests(), 0) }
  finally { await f.cleanup() }
})

test("screenshot-only titles retry with the assistant explanation and attachment metadata", async () => {
  const f = await harness({ messages: [{ id: "msg_image", type: "user", text: '"Image 1"', files: [{ mime: "image/png", name: "screenshot.png" }] }],
    answer: "Explore｜Image 1" })
  try {
    await f.title()
    await until(() => f.mark() !== undefined)
    assert.equal(f.mark()?.provisional, true)
    assert.match(f.prompts[0], /screenshot.png \(image\/png\)/)
    f.state.messages.push({ id: "msg_reply", type: "assistant", content: [{ type: "text", text: "This screenshot shows a Ghostty fullscreen issue after unplugging the external monitor." }] })
    f.state.answer = "Fix｜Ghostty fullscreen after monitor disconnect"
    await f.idle({ type: "session.execution.succeeded" })
    await until(() => f.mark()?.provisional === false)
    assert.match(f.prompts.at(-1)!, /Ghostty fullscreen issue/)
    assert.equal(f.session.title, `${today()}｜Fix｜Ghostty fullscreen after monitor disconnect`)
    await f.idle()
    assert.equal(f.prompts.length, 2)
  } finally { await f.cleanup() }
})

for (const action of ["rename", "move", "dispose"] as const) test(`late generation cannot overwrite a ${action}`, async () => {
  const result = deferred<string>()
  const started = deferred<void>()
  const title = `${yesterday()}｜Fix｜Stripe retries`
  const f = await harness({ title, answer: async () => { started.resolve(); return result.promise } })
  try {
    f.emit()
    await started.promise
    if (action === "rename") await f.rename("Human choice")
    if (action === "move") f.session.location = { directory: join(configRoot, "moved") }
    if (action === "dispose") await f.cleanup()
    result.resolve("Fix｜Late answer")
    await pause()
    await pause()
    assert.deepEqual(f.updates, [])
    assert.equal(f.session.title, action === "rename" ? "Human choice" : title)
    assert.equal(f.requests(), 1)
  } finally { await f.cleanup() }
})

test("a rename while the first title is pending stays the person's", async () => {
  const slow = deferred<string>()
  const started = deferred<void>()
  const f = await harness({ answer: async () => { started.resolve(); return slow.promise } })
  try {
    const first = f.native()
    await started.promise
    await f.rename("Human choice")
    slow.resolve("Fix｜Late answer")
    await first
    await f.idle()
    assert.equal(f.session.title, "Human choice")
    assert.equal(f.mark()?.manual, true)
    assert.deepEqual(f.updates, [])
    assert.equal(f.requests(), 0)
  } finally { await f.cleanup() }
})

test("completion waits for a slow first title before retrying its fallback", async () => {
  const slow = deferred<string>()
  const started = deferred<void>()
  const f = await harness({ answer: async () => { started.resolve(); return slow.promise } })
  try {
    const first = f.native()
    await started.promise
    f.emit({ type: "session.execution.succeeded" })
    await pause()
    assert.equal(f.requests(), 0)
    f.state.answer = "Fix｜Recovered slow title"
    slow.resolve("Explore｜Untitled session")
    await first
    await until(() => f.session.title === `${today()}｜Fix｜Recovered slow title`)
    assert.equal(f.session.title, `${today()}｜Fix｜Recovered slow title`)
    assert.equal(f.prompts.length, 2)
    assert.equal(f.requests(), 1)
  } finally { await f.cleanup() }
})
