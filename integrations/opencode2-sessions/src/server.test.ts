import assert from "node:assert/strict"
import { test } from "node:test"
import type { Plugin } from "@opencode/plugin"
import type { OpenCodeEvent } from "@opencode/client"
import serverPlugin, { setupWatchServer, WatchService } from "./server.ts"
import indexPlugin from "./index.ts"
import { watchRPC } from "./watch.ts"

test("both conventional server entrypoints expose one default definition", () => {
  assert.equal(serverPlugin, indexPlugin)
  assert.equal(serverPlugin.id, "another.sessions")
  assert.equal(serverPlugin.setup, setupWatchServer)
})

test("RPC uses supported primitive input schemas; native ID validation is at the handler boundary", () => {
  // OpenCode 2.2's RPC JSON-schema validator disables pattern evaluation.
  assert.deepEqual(watchRPC.methods.add.input.properties.supervisor, { type: "string" })
  assert.deepEqual(watchRPC.methods.add.input.properties.member, { type: "string" })
})

function fixture() {
  const stored = new Map<string, any>()
  const sessions = new Map<string, any>(["ses_super", "ses_member", "ses_other"].map(id => [id, { id, title: id, time: { busy: 10, idle: 20 } }]))
  const cards: any[] = [], failures = new Set<string>()
  const context = {
    storage: { async get(key: string) { return structuredClone(stored.get(key)) }, async set(key: string, value: any) { stored.set(key, structuredClone(value)) } },
    session: {
      async get({ sessionID }: any) {
        if (failures.has(sessionID)) throw new Error("temporary session failure")
        if (!sessions.has(sessionID)) throw Object.assign(new Error("not found"), { _tag: "SessionNotFoundError" })
        return sessions.get(sessionID)
      },
      async synthetic(input: any) { cards.push(input) },
    },
  } as unknown as Plugin.Context
  const service = new WatchService(context, "groups-test")
  return { context, service, stored, sessions, cards, failures }
}

let ordinal = 0
function event(type: string, id: string, status?: string): OpenCodeEvent {
  return { type, id: `event_${++ordinal}`, created: 1794450000000 + ordinal,
    location: { directory: "another-project" },
    data: type === "form.created" ? { form: { sessionID: id } } : { sessionID: id, ...(status ? { status: { type: status } } : {}) },
  } as unknown as OpenCodeEvent
}

test("busy to idle fires exactly once, idle to idle none, unwatched sessions ignored", async () => {
  const f = fixture(); await f.service.change("ses_super", "ses_member", true)
  assert.equal(f.cards[0].description, "Watch supervision started"); f.cards.length = 0
  await f.service.event(event("session.status", "ses_member", "idle"))
  assert.equal(f.cards.length, 0)
  await f.service.event(event("session.execution.started", "ses_member"))
  await f.service.event(event("session.status", "ses_member", "busy"))
  await f.service.event(event("session.idle", "ses_member"))
  await f.service.event(event("session.status", "ses_member", "idle"))
  await f.service.event(event("session.idle", "ses_member"))
  await f.service.event(event("session.status", "ses_other", "busy"))
  await f.service.event(event("session.idle", "ses_other"))
  assert.equal(f.cards.length, 1)
  assert.equal(f.cards[0].sessionID, "ses_super"); assert.equal(f.cards[0].delivery, "queue")
  assert.match(f.cards[0].description, /turn ended · idle$/)
  assert.match(f.cards[0].text, /another context ses_member --provider opencode --all/)
})

test("permission and question waits report blocked, errors report once without a following idle duplicate", async () => {
  const f = fixture(); await f.service.change("ses_super", "ses_member", true); f.cards.length = 0
  await f.service.event(event("session.status", "ses_member", "busy"))
  await f.service.event(event("permission.asked", "ses_member"))
  await f.service.event(event("permission.asked", "ses_member"))
  await f.service.event(event("session.status", "ses_member", "busy"))
  await f.service.event(event("form.created", "ses_member"))
  await f.service.event(event("session.execution.failed", "ses_member"))
  await f.service.event(event("session.execution.failed", "ses_member"))
  await f.service.event(event("session.idle", "ses_member"))
  assert.deepEqual(f.cards.map(card => card.description.split(" · ").at(-1)), ["blocked", "blocked", "error"])
})

test("native terminal execution events report idle without legacy status events and deduplicate subsequent idle", async () => {
  const f = fixture(); await f.service.change("ses_super", "ses_member", true); f.cards.length = 0
  await f.service.event(event("session.execution.started", "ses_member"))
  await f.service.event(event("session.execution.succeeded", "ses_member"))
  await f.service.event(event("session.idle", "ses_member"))
  await f.service.event(event("session.execution.started", "ses_member"))
  await f.service.event(event("session.execution.interrupted", "ses_member"))
  await f.service.event(event("session.status", "ses_member", "idle"))
  assert.equal(f.cards.length, 2)
  assert.ok(f.cards.every(card => card.description.endsWith(" · idle")))
})

test("groups survive a storage round trip, busy baselines survive reload, intro only for first member", async () => {
  const f = fixture()
  await f.service.change("ses_super", "ses_member", true)
  await f.service.change("ses_super", "ses_other", true)
  await f.service.change("ses_super", "ses_member", true)
  assert.equal(f.cards.length, 1)
  f.sessions.get("ses_member").time = { busy: 30, idle: 20 }
  const restored = new WatchService(f.context, "groups-test")
  assert.deepEqual((await restored.status()).groups, { ses_super: ["ses_member", "ses_other"] })
  await restored.event(event("session.idle", "ses_member"))
  assert.equal(f.cards.length, 2)
  await restored.change("ses_super", "ses_member", false)
  await restored.event(event("session.execution.failed", "ses_member"))
  assert.equal(f.cards.length, 2)
  assert.deepEqual(f.stored.get("groups-test"), { ses_super: ["ses_other"] })
})

test("deleted supervisors prune groups and deleted members are dropped, including missed deletion events", async () => {
  const f = fixture()
  await f.service.change("ses_super", "ses_member", true)
  await f.service.change("ses_super", "ses_other", true)
  await f.service.event(event("session.deleted", "ses_member"))
  assert.deepEqual((await f.service.status()).groups, { ses_super: ["ses_other"] })
  f.sessions.delete("ses_super")
  assert.deepEqual((await f.service.status()).groups, {})
  const restored = new WatchService(f.context, "groups-test")
  assert.deepEqual((await restored.status()).groups, {})
  f.sessions.set("ses_super", { id: "ses_super", time: {} })
  await restored.change("ses_super", "ses_other", true)
  f.sessions.delete("ses_other")
  assert.deepEqual((await restored.status()).groups, {})
})

test("invalid/self/missing sessions rejected, transient read failures do not silently prune", async () => {
  const f = fixture()
  for (const member of ["ses_super", "ses_missing", "not-an-opencode-id"]) await assert.rejects(f.service.change("ses_super", member, true))
  await f.service.change("ses_super", "ses_member", true)
  f.failures.add("ses_super")
  await assert.rejects(f.service.status(), /temporary/)
  f.failures.clear()
  assert.deepEqual((await f.service.status()).groups, { ses_super: ["ses_member"] })
})

test("location RPC registrations share one service and event subscriber; standalone namespaces stay separate", async () => {
  const f = fixture()
  let subscriptions = 0
  const handlers: any[] = []
  const make = () => ({ ...f.context,
    rpc: { async register(definition: any, implementation: any) {
      assert.equal(definition.id, "another.sessions.watch"); handlers.push(implementation)
      return { async dispose() {} }
    } },
    event: { async *subscribe({ signal }: any) {
      subscriptions++
      await new Promise<void>(resolve => { if (signal.aborted) resolve(); else signal.addEventListener("abort", () => resolve(), { once: true }) })
    } },
  }) as unknown as Plugin.Context
  const first = await setupWatchServer(make()), second = await setupWatchServer(make())
  let replacement: (() => Promise<void>) | undefined
  try {
    assert.equal(subscriptions, 1)
    await handlers[0].add({ supervisor: "ses_super", member: "ses_member" })
    assert.deepEqual((await handlers[1].status({})).groups, { ses_super: ["ses_member"] })
    replacement = await setupWatchServer(make(), "new-installed-revision")
    assert.equal(subscriptions, 2) // The first stream is aborted, not left running alongside the replacement.
    assert.deepEqual((await handlers[2].status({})).groups, { ses_super: ["ses_member"] })
    await handlers[1].rm({ supervisor: "ses_super", member: "ses_member" })
    assert.deepEqual((await handlers[0].status({})).groups, {})
    const privateServer = new WatchService(f.context, "different-server-groups")
    assert.deepEqual((await privateServer.status()).groups, {})
  } finally { await first(); await second(); await replacement?.() }
})
