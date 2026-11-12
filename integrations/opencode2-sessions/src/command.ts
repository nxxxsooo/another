import { execFile } from "node:child_process"
import { promisify } from "node:util"
import type { Context, DialogSelectAction } from "@opencode/plugin/tui/context"
import { executable } from "./runtime.ts"
import { parseStatus, watchRPC, type WatchStatus } from "./watch.ts"

export type Session = { id: string; provider: string; title: string; project_path: string; updated_at: string }
type Run = (args: string[], cwd: string) => Promise<string>
const exec = promisify(execFile)
const runCommand: Run = async (args, cwd) => (await exec(executable, args, { cwd, timeout: 30_000, maxBuffer: 16 << 20, windowsHide: true })).stdout

function parseSessions(text: string): Session[] {
  const value = JSON.parse(text) ?? []
  if (!Array.isArray(value) || value.some(s => !s || typeof s.id !== "string" || typeof s.provider !== "string" || (s.project_path !== undefined && typeof s.project_path !== "string"))) throw new Error("Invalid another session list")
  return value.map(s => ({ ...s, project_path: s.project_path ?? "", title: s.title ?? "" })).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
}

export function createSessionsCommand(context: Context, dependencies: { run?: Run } = {}) {
  const run = dependencies.run ?? runCommand
  let disposed = false, active = false
  const notice = (message: string, error = false) => { if (!disposed) context.ui.toast.show({ title: "another /ref", message, variant: error ? "error" : "info" }) }

  async function open() {
    if (disposed || active) return
    // Capture the destination before any I/O; switching tabs never redirects it.
    const route = context.ui.router.current()
    if (route.type !== "session" && route.type !== "home") return notice("Open a conversation first.")
    active = true
    try {
      const homeLocation = context.location ?? context.data.location.default()
      const model = context.ui.model.current()
      const current = route.type === "session" ? await context.client.session.get({ sessionID: route.sessionID }) : undefined
      const cwd = current?.location.directory ?? homeLocation.directory
      if (!cwd) throw new Error("Cannot determine the conversation's working directory.")
      const supervisor = current?.id
      const spoken = supervisor ? await context.client.message.list({ sessionID: supervisor, type: "user", limit: 1, order: "desc" }) : undefined
      const fresh = !spoken?.data.length
      const scoped = parseSessions(await run(["list", "--json"], cwd))
      const localIDs = new Set(scoped.map(s => `${s.provider}:${s.id}`))
      const rpc = context.client.rpc(watchRPC)
      const request = { location: current?.location ?? homeLocation }
      let all = false, page = 0
      while (!disposed) {
        const sessions = all ? parseSessions(await run(["list", "--json", "--all"], cwd)) : scoped
        let live: WatchStatus | undefined
        try { live = parseStatus(await rpc.status({}, request)) }
        catch (error) { notice(`Watch status unavailable: ${error instanceof Error ? error.message : String(error)}`, true) }
        let action: "scope" | "watch" | undefined, highlighted: string | undefined
        const rows = sessions.slice(page * 100, (page + 1) * 100)
        const options = rows.map(s => ({ title: `${s.provider === "opencode" && context.data.session.status(s.id) === "running" ? "● " : ""}${supervisor && live?.groups[supervisor]?.includes(s.id) && s.provider === "opencode" ? "盯 " : ""}${s.title || s.id}`, value: `${s.provider}:${s.id}`, footer: s.project_path, description: s.provider }))
        if (page > 0) options.push({ title: "Previous conversations…", value: "previous", footer: "", description: "" })
        if (sessions.length > (page + 1) * 100) options.push({ title: "More conversations…", value: "more", footer: "", description: "" })
        const actions: DialogSelectAction<string>[] = [{ bind: "ctrl+a", title: all ? "This project" : "All projects", selection: "none", onTrigger: () => { action = "scope"; context.ui.dialog.clear() } }]
        if (live) actions.push({ bind: "ctrl+w", title: "Watch / unwatch", onTrigger: value => { action = "watch"; highlighted = value; context.ui.dialog.clear() } })
        const selection = context.ui.dialog.select({ title: `Reference / watch · ${all ? "All projects" : "This project"}`, options, actions })
        context.ui.dialog.set({ size: "xlarge" })
        const selected = await selection
        if (disposed) return
        if (action === "scope") { all = !all; page = 0; continue }
        if (selected === "more") { page++; continue }
        if (selected === "previous") { page--; continue }
        const source = rows.find(s => `${s.provider}:${s.id}` === (highlighted ?? selected))
        if (!source) { if (action) continue; return }
        if (source.id === supervisor && source.provider === "opencode") { notice("Choose a different conversation."); continue }
        if (action === "watch") {
          if (!supervisor) { notice("Open a supervisor conversation before watching sessions."); continue }
          if (source.provider !== "opencode") { notice("Only OpenCode sessions on this server can be watched."); continue }
          if (!live) { notice("Watch status is unavailable."); continue }
          const removing = live.groups[supervisor]?.includes(source.id)
          if (!removing && !localIDs.has(`${source.provider}:${source.id}`) && await context.ui.dialog.confirm({ title: "Watch another project?", message: `Send watch reports about ${source.title || source.id} from ${source.project_path} to this supervisor?` }) !== true) continue
          parseStatus(await rpc[removing ? "rm" : "add"]({ supervisor, member: source.id }, request))
          continue
        }
        const args = ["context", source.id, "--provider", source.provider, ...(all ? ["--all"] : [])]
        const text = await run(args, cwd)
        const comment = await context.ui.dialog.prompt({ title: "Comment on reference (optional)", placeholder: "How should the assistant use this conversation?" })
        if (disposed || comment === undefined) return
        if (await context.ui.dialog.confirm({ title: "Attach conversation reference?", message: `From: ${source.title || source.id}\n${source.project_path}\nTo: ${current?.title || "New session"}\n${fresh ? "Attach for your first message?" : "Attach and continue?"}`, label: { confirm: fresh ? "Attach" : "Attach and continue" } }) !== true || disposed) return
        const destination = supervisor ?? (await context.client.session.create({ location: homeLocation, ...(model ? { model: { providerID: model.providerID, id: model.modelID, variant: model.variant } } : {}) })).id
        if (disposed) return
        if (!supervisor && context.ui.router.current().type === "home") context.ui.router.navigate({ type: "session", sessionID: destination })
        await context.client.session.prompt({ sessionID: destination, text: `${comment ? `${comment}\n\n` : ""}${text}`, delivery: "steer", resume: !fresh, metadata: { anotherReference: { provider: source.provider, sessionID: source.id } } })
        notice(fresh ? "Reference attached for your first message." : "Reference attached; conversation continuing.")
        return
      }
    } catch (error) { notice(error instanceof Error ? error.message : String(error), true) }
    finally { active = false }
  }
  return { open, dispose() { disposed = true; if (active) context.ui.dialog.clear() } }
}

// The server owns watching; TUI setup has no discovery or background processes.
export function setupSessions(context: Context, create = createSessionsCommand) {
  const sessions = create(context)
  const unmount = context.ui.slot({ append: "app", render() {
    context.keymap.layer(() => ({ mode: "global", commands: [{ id: "another.sessions.ref", title: "Reference or watch a session", group: "another", palette: true, slash: { name: "ref" }, run: sessions.open }] }))
    return null
  } })
  return () => { sessions.dispose(); unmount() }
}
