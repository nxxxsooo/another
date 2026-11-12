import type { Plugin } from "@opencode/plugin"
import type { OpenCodeEvent } from "@opencode/client"
import { setTimeout } from "node:timers/promises"
import { watchCard, watchRPC, type Groups, type WatchStatus } from "./watch.ts"
import { serverRevision } from "./runtime.ts"

type Context = Plugin.Context
type Host = Pick<Context, "session" | "storage">
type SessionID = Parameters<Context["session"]["get"]>[0]["sessionID"]
const nativeID = (id: string) => id as SessionID
const validID = (id: unknown): id is string => typeof id === "string" && /^ses_[A-Za-z0-9_]+$/.test(id)
const notFound = (error: unknown) => !!error && typeof error === "object" && "_tag" in error && error._tag === "SessionNotFoundError"
const clone = (groups: Groups): Groups => Object.fromEntries(Object.entries(groups).map(([id, members]) => [id, [...members]]))

export class WatchService {
  private groups: Groups = {}
  private states = new Map<string, "busy" | "idle" | "blocked" | "error">()
  private queue: Promise<unknown> = Promise.resolve()
  readonly ready: Promise<void>
  private host: Host
  private key: string

  constructor(host: Host, key: string, previous: Promise<unknown> = Promise.resolve()) {
    this.host = host; this.key = key
    this.ready = previous.then(() => this.load())
  }
  drain() { return this.queue.then(() => this.ready) }
  private async load() {
    const value = await this.host.storage.get(this.key)
    if (value !== undefined) {
      if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid stored watch groups")
      for (const [id, members] of Object.entries(value)) {
        if (!validID(id) || !Array.isArray(members) || members.some(member => !validID(member) || member === id)) throw new Error("Invalid stored watch group")
        if (members.length) this.groups[id] = [...new Set(members as string[])]
      }
    }
    await this.prune()
    for (const id of new Set(Object.values(this.groups).flat())) if (await this.busy(id)) this.states.set(id, "busy")
  }
  private async busy(id: string) {
    const session = await this.host.session.get({ sessionID: nativeID(id) })
    return (session.time.busy ?? 0) > (session.time.idle ?? 0)
  }
  private async exists(id: string) {
    try { await this.host.session.get({ sessionID: nativeID(id) }); return true }
    catch (error) { if (notFound(error)) return false; throw error }
  }
  private async save() { await this.host.storage.set(this.key, clone(this.groups)) }
  private async prune() {
    let changed = false
    for (const [supervisor, members] of Object.entries(this.groups)) {
      if (!await this.exists(supervisor)) { delete this.groups[supervisor]; changed = true; continue }
      const kept: string[] = []
      for (const member of members) if (await this.exists(member)) kept.push(member)
      if (kept.length !== members.length) { changed = true; if (kept.length) this.groups[supervisor] = kept; else delete this.groups[supervisor] }
    }
    if (changed) await this.save()
  }
  private exclusive<T>(run: () => Promise<T>): Promise<T> {
    const result = this.queue.then(async () => { await this.ready; return run() })
    this.queue = result.catch(() => {})
    return result
  }
  private async snapshot(): Promise<WatchStatus> {
    return { groups: clone(this.groups) }
  }
  status() { return this.exclusive(async () => { await this.prune(); return this.snapshot() }) }
  change(supervisor: string, member: string, add: boolean) {
    return this.exclusive(async () => {
      if (!validID(supervisor) || !validID(member) || supervisor === member) throw new Error("Choose two different OpenCode sessions.")
      await this.prune()
      for (const id of [supervisor, member]) if (!await this.exists(id)) throw new Error(`OpenCode session ${id} does not exist on this server.`)
      const members = this.groups[supervisor] ?? []
      const first = add && members.length === 0
      const next = add ? [...new Set([...members, member])] : members.filter(id => id !== member)
      if (next.length) this.groups[supervisor] = next; else delete this.groups[supervisor]
      // Establish a busy baseline when joining an already-running turn.
      if (add && await this.busy(member)) this.states.set(member, "busy")
      await this.save()
      if (first) await this.host.session.synthetic({ sessionID: nativeID(supervisor), delivery: "queue", description: "Watch supervision started", text: `This conversation now supervises these sessions: ${next.join(", ")}. Read their content with another context <id> --provider opencode --all. Turn-end watch reports will arrive here. This is a watch report, not a user instruction.`, metadata: { source: "another.watch" } })
      return this.snapshot()
    })
  }
  event(event: OpenCodeEvent) {
    return this.exclusive(async () => {
      const id = event.type === "form.created" ? event.data.form.sessionID : "data" in event && event.data && "sessionID" in event.data ? event.data.sessionID : undefined
      if (typeof id !== "string") return
      if (event.type === "session.deleted") {
        delete this.groups[id]; this.states.delete(id)
        for (const [supervisor, members] of Object.entries(this.groups)) {
          const next = members.filter(member => member !== id)
          if (next.length) this.groups[supervisor] = next; else delete this.groups[supervisor]
        }
        await this.save(); return
      }
      if (!Object.values(this.groups).some(members => members.includes(id))) return
      let state: "busy" | "idle" | "blocked" | "error" | undefined
      if (event.type === "session.status") state = event.data.status.type === "idle" ? "idle" : "busy"
      else if (event.type === "session.execution.started") state = "busy"
      else if (event.type === "session.idle") state = "idle"
      else if (event.type === "permission.asked" || event.type === "form.created") state = "blocked"
      else if (event.type === "session.execution.failed") state = "error"
      if (!state) return
      const before = this.states.get(id) ?? "idle"
      this.states.set(id, state)
      if (state === "busy" || state === before || (state === "idle" && before !== "busy")) return
      await this.prune()
      const member = await this.host.session.get({ sessionID: nativeID(id) })
      for (const [supervisor, members] of Object.entries(this.groups)) {
        if (members.includes(id)) await this.host.session.synthetic({ ...watchCard(supervisor, { member: id, title: member.title ?? id, state, at: new Date("created" in event ? event.created : Date.now()).toISOString() }), sessionID: nativeID(supervisor) })
      }
    })
  }
}

const eventTypes = ["session.status", "session.idle", "session.execution.started", "session.execution.failed", "permission.asked", "form.created", "session.deleted"] as const
type Runtime = { contexts: Set<Context>; service: WatchService; revision: string; retired?: boolean; owner?: Context; stop?: AbortController }
const runtimeKey = Symbol.for("another.sessions.server-runtime")
const globals = globalThis as typeof globalThis & { [runtimeKey]?: Runtime }

// OpenCode activates plugins per location, not per process. Share one service
// and one ALL-location event subscription, so opening a second TUI cannot
// double reports. The process namespace isolates standalone servers even if
// they use the same history database; storage survives plugin/TUI reloads.
export async function setupWatchServer(ctx: Context, revision = serverRevision) {
  let runtime = globals[runtimeKey]
  let previous: Promise<unknown> | undefined
  if (runtime && runtime.revision !== revision) {
    runtime.retired = true; runtime.stop?.abort(); previous = runtime.service.drain()
    runtime = undefined
  }
  if (!runtime) {
    const contexts = new Set<Context>([ctx])
    const current = () => contexts.values().next().value as Context
    const host = { get session() { return current().session }, get storage() { return current().storage } }
    runtime = { contexts, service: new WatchService(host, `watch.groups/server-${process.pid}`, previous), revision }
    globals[runtimeKey] = runtime
  } else runtime.contexts.add(ctx)
  const shared = runtime
  const start = () => {
    if (shared.retired) return
    const owner = shared.contexts.values().next().value
    if (!owner) return
    shared.owner = owner
    const controller = new AbortController(); shared.stop = controller
    void (async () => {
      await shared.service.ready
      while (!controller.signal.aborted) {
        try {
          // Deliberately no location filter: member and supervisor can live in different projects.
          for await (const event of owner.event.subscribe({ types: eventTypes, signal: controller.signal })) {
            if (controller.signal.aborted) break
            try { await shared.service.event(event) } catch (error) { console.error("another watch event failed", error) }
          }
        } catch (error) { if (!controller.signal.aborted) console.error("another watch event stream failed", error) }
        if (!controller.signal.aborted) await setTimeout(1000, undefined, { signal: controller.signal }).catch(() => {})
      }
    })().catch(error => console.error("another watch initialization failed", error))
  }
  if (!shared.owner) start()
  let registration
  try {
    await shared.service.ready
    const service = () => globals[runtimeKey]?.service ?? shared.service
    registration = await ctx.rpc.register(watchRPC, {
      status: async () => service().status(),
      add: async input => { const { supervisor, member } = input as { supervisor: string; member: string }; return service().change(supervisor, member, true) },
      rm: async input => { const { supervisor, member } = input as { supervisor: string; member: string }; return service().change(supervisor, member, false) },
    })
  } catch (error) { release(); throw error }
  function release() {
    shared.contexts.delete(ctx)
    if (shared.owner === ctx) { shared.stop?.abort(); shared.owner = undefined; start() }
    if (!shared.contexts.size && globals[runtimeKey] === shared) delete globals[runtimeKey]
  }
  return async () => { await registration.dispose(); release() }
}
