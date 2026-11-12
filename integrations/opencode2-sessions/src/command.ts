import { execFile } from "node:child_process"
import { promisify } from "node:util"
import { appendFileSync } from "node:fs"
import type { Context, DialogSelectAction } from "@opencode/plugin/tui/context"
import { executable } from "./runtime.ts"
import { parseStatus, watchRPC, type WatchStatus } from "./watch.ts"
import { scopeDirectory, sessionOption } from "./format.ts"

export type Session = { id: string; provider: string; title: string; project_path: string; updated_at: string }
type Run = (args: string[], cwd: string) => Promise<string>
export type CommentPrompt = (options: { sourceTitle: string; destinationTitle: string; provider: string; directory: string; placeholder: string }, signal: AbortSignal) => Promise<string | undefined>
// Ctrl+P is reserved for the global palette and picker navigation. Alt+P is
// absent from OpenCode's default bindings and the inspected local overrides.
export const providerFilterKey = "alt+p"
const exec = promisify(execFile)
const runCommand: Run = async (args, cwd) => (await exec(executable, args, { cwd, timeout: 30_000, maxBuffer: 16 << 20, windowsHide: true })).stdout

function parseSessions(text: string, search = false): Session[] {
  const parsed = JSON.parse(text)
  if (search && (!parsed || typeof parsed !== "object" || !Object.hasOwn(parsed, "results") || (parsed.results !== null && !Array.isArray(parsed.results)))) throw new Error("Invalid another search results")
  const value = search ? (parsed.results ?? []).map((hit: { session?: unknown }) => hit?.session) : parsed ?? []
  if (!Array.isArray(value) || value.some(s => !s || typeof s.id !== "string" || typeof s.provider !== "string" || (s.project_path !== undefined && typeof s.project_path !== "string"))) throw new Error("Invalid another session list")
  return value.map(s => ({ ...s, project_path: s.project_path ?? "", title: s.title ?? "" })).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
}

export function parseReferenceInput(input: string) {
  let query = input.trim(), all = false
  while (/^--all(?:\s|$)/.test(query)) { all = true; query = query.replace(/^--all\s*/, "") }
  if (/^--tui(?:\s|$)/.test(query)) throw new Error("/ref does not support --tui or inline annotation.")
  return { query, all }
}

export function createSessionsCommand(context: Context, dependencies: { run?: Run; comment: CommentPrompt; profile?: (phase: string, ms: number) => void }) {
  const run = dependencies.run ?? runCommand
  const profile = dependencies.profile ?? ((phase: string, ms: number) => {
    const path = process.env.ANOTHER_REF_TIMING_FILE
    if (path) appendFileSync(path, JSON.stringify({ at: Date.now(), phase, ms }) + "\n")
  })
  const step = async <T>(phase: string, fn: () => Promise<T>) => {
    const start = performance.now()
    try { return await fn() } finally { profile(phase, performance.now() - start) }
  }
  const controller = new AbortController()
  const results = new Map<string, { at: number; value: Promise<Session[]> }>()
  let disposed = false, active = false
  const notice = (message: string, error = false) => { if (!disposed) context.ui.toast.show({ title: "another /ref", message, variant: error ? "error" : "info" }) }

  async function open(input = "") {
    if (disposed || active) return
    // Capture the destination before any I/O; switching tabs never redirects it.
    const route = context.ui.router.current()
    if (route.type !== "session" && route.type !== "home") return notice("Open a conversation first.")
    active = true
    const started = performance.now()
    profile("dispatch", 0)
    try {
      const initial = parseReferenceInput(input)
      const homeLocation = context.location ?? context.data.location.default()
      const model = context.ui.model.current()
      const current = route.type === "session" ? await step("session.get", () => context.client.session.get({ sessionID: route.sessionID })) : undefined
      const cwd = current?.location.directory ?? homeLocation.directory
      if (!cwd) throw new Error("Cannot determine the conversation's working directory.")
      const supervisor = current?.id
      const load = (all: boolean, query = initial.query): Promise<Session[]> => {
        const key = JSON.stringify([cwd, all, query])
        const cached = results.get(key)
        if (cached && Date.now() - cached.at < 30_000) return cached.value
        if (results.size >= 32) results.delete(results.keys().next().value!)
        const value = step(query ? "cli.search" : all ? "cli.list.all" : "cli.list", () => run(query
          ? ["search", "--json", "--cached", "--no-wait", ...(all ? ["--all"] : []), "--", query]
          : ["list", "--json", "--cached", ...(all ? ["--all"] : [])], cwd)).then(text => parseSessions(text, !!query))
        results.set(key, { at: Date.now(), value })
        void value.catch(() => { if (results.get(key)?.value === value) results.delete(key) })
        return value
      }
      const rpc = context.client.rpc(watchRPC)
      const request = { location: current?.location ?? homeLocation }
      // Start the cached index read and native status read together. Status is
      // an in-memory server snapshot, never a reconciliation/event-queue wait.
      const firstLoad = load(initial.all)
      let live: WatchStatus | undefined
      await Promise.all([firstLoad, step("rpc.status", () => rpc.status({}, request)).then(value => { live = parseStatus(value) }).catch(error => {
        notice(`Watch status unavailable: ${error instanceof Error ? error.message : String(error)}`, true)
      })])
      let all = initial.all, page = 0, provider: string | undefined
      while (!disposed) {
        const available = (await load(all)).filter(s => s.provider !== "opencode" || s.id !== supervisor)
        const providers = [...new Set(available.map(s => s.provider))].sort()
        if (provider && !providers.includes(provider)) provider = undefined
        const sessions = available.filter(s => !provider || s.provider === provider)
        let action: "scope" | "watch" | "provider" | undefined, highlighted: string | undefined
        const rows = sessions.slice(page * 100, (page + 1) * 100)
        const directories = rows.map(s => scopeDirectory(s.project_path, cwd, all))
        const directoryColumn = directories.some(Boolean)
        const options = rows.map((s, i) => sessionOption(s, directories[i], directoryColumn,
          s.provider === "opencode" && context.data.session.status(s.id) === "running",
          !!(supervisor && live?.groups[supervisor]?.includes(s.id) && s.provider === "opencode"), context.renderer?.width ?? 200))
        if (page > 0) options.push({ title: "Previous conversations…", value: "previous", footer: "" })
        if (sessions.length > (page + 1) * 100) options.push({ title: "More conversations…", value: "more", footer: "" })
        const actions: DialogSelectAction<string>[] = [{ bind: "ctrl+a", title: all ? "This project" : "All projects", selection: "none", onTrigger: () => { profile("action.scope", 0); action = "scope"; context.ui.dialog.clear() } }]
        actions.push({ bind: providerFilterKey, title: `Provider: ${provider ?? "All"}`, selection: "none", onTrigger: () => { profile("action.provider", 0); action = "provider"; context.ui.dialog.clear() } })
        if (live) actions.push({ bind: "ctrl+w", title: "Watch / unwatch", onTrigger: value => { action = "watch"; highlighted = value; context.ui.dialog.clear() } })
        const selection = context.ui.dialog.select({ title: `Reference / watch · ${all ? "All projects" : "This project"}${provider ? ` · ${provider}` : ""}`, options, actions })
        context.ui.dialog.set({ size: "xlarge" })
        profile("picker.visible", performance.now() - started)
        // The dialog API cannot update select rows in place. Prefetch the
        // opposite scope, then use bounded cached results for toggles/reopens.
        void load(!all).catch(() => {}) // Report errors only if that scope is selected.
        const selected = await selection
        if (disposed) return
        if (action === "scope") { all = !all; page = 0; continue }
        if (action === "provider") { provider = providers[(provider ? providers.indexOf(provider) : -1) + 1]; page = 0; continue }
        if (selected === "more") { page++; continue }
        if (selected === "previous") { page--; continue }
        const source = rows.find(s => `${s.provider}:${s.id}` === (highlighted ?? selected))
        if (!source) { if (action) continue; return }
        profile("action.select", 0)
        if (action === "watch") {
          if (!supervisor) { notice("Open a supervisor conversation before watching sessions."); continue }
          if (source.provider !== "opencode") { notice("Only OpenCode sessions on this server can be watched."); continue }
          if (!live) { notice("Watch status is unavailable."); continue }
          const removing = live.groups[supervisor]?.includes(source.id)
          const crossProject = all && !removing && !(await load(false, "")).some(s => s.provider === source.provider && s.id === source.id)
          if (crossProject && await context.ui.dialog.confirm({ title: "Watch another project?", message: `Send watch reports about ${source.title || source.id} from ${source.project_path} to this supervisor?` }) !== true) continue
          live = parseStatus(await rpc[removing ? "rm" : "add"]({ supervisor, member: source.id }, request))
          continue
        }
        const args = ["context", source.id, "--provider", source.provider, "--cached", ...(all ? ["--all"] : [])]
        const text = await step("cli.context", () => run(args, cwd))
        profile("comment.visible", performance.now() - started)
        const comment = await dependencies.comment({ sourceTitle: source.title || source.id, destinationTitle: current?.title || "New session", provider: source.provider, directory: source.project_path, placeholder: "How should the assistant use this conversation? (optional)" }, controller.signal)
        if (disposed || comment === undefined) return
        const spoken = supervisor ? await step("message.list", () => context.client.message.list({ sessionID: supervisor, type: "user", limit: 1, order: "desc" })) : undefined
        const fresh = !spoken?.data.length
        const destination = supervisor ?? (await context.client.session.create({ location: homeLocation, ...(model ? { model: { providerID: model.providerID, id: model.modelID, variant: model.variant } } : {}) })).id
        if (disposed) return
        if (!supervisor && context.ui.router.current().type === "home") context.ui.router.navigate({ type: "session", sessionID: destination })
        await step("send", () => context.client.session.prompt({ sessionID: destination, text: `${comment ? `${comment}\n\n` : ""}${text}`, delivery: "steer", resume: !fresh, metadata: { anotherReference: { provider: source.provider, sessionID: source.id } } }))
        notice(fresh ? "Reference attached for your first message." : "Reference attached; conversation continuing.")
        return
      }
    } catch (error) { notice(error instanceof Error ? error.message : String(error), true) }
    finally { active = false }
  }
  return { open, dispose() { disposed = true; controller.abort(); if (active) context.ui.dialog.clear() } }
}

// The server owns watching; TUI setup has no discovery or background processes.
export function setupSessions(context: Context, comment: CommentPrompt, create = createSessionsCommand) {
  const sessions = create(context, { comment })
  const unmount = context.ui.slot({ append: "app", render() {
    context.keymap.layer(() => ({ mode: "global", commands: [{ id: "another.sessions.ref", title: "Reference or watch a session", group: "another", palette: true, slash: { name: "ref", arguments: true }, run: sessions.open }] }))
    return null
  } })
  return () => { sessions.dispose(); unmount() }
}
