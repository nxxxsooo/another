import { execFile, spawn } from "node:child_process"
import { promisify } from "node:util"
import { createInterface } from "node:readline"
import type { Context, DialogSelectAction } from "@opencode/plugin/tui/context"
import { executable } from "./runtime.ts"

export type Session = { id: string; provider: string; title: string; project_path: string; updated_at: string }
export type Status = { live: Record<string, { state: string; terminal: string; title: string }>; groups: Record<string, string[]>; warnings: string[] }
export type Report = { member: string; title: string; state: "idle" | "done" | "blocked" | "error"; at: string }
type Run = (args: string[], cwd: string) => Promise<string>
type Follow = (supervisor: string, cwd: string, report: (value: Report) => Promise<void>, failed: (error: Error) => void, ended: () => void) => () => void
const exec = promisify(execFile)
const runCommand: Run = async (args, cwd) => (await exec(executable, args, { cwd, timeout: 30_000, maxBuffer: 16 << 20, windowsHide: true })).stdout

export function parseStatus(text: string): Status {
  const value = JSON.parse(text)
  if (!value || typeof value.live !== "object" || !value.live || typeof value.groups !== "object" || !value.groups) throw new Error("Invalid another watch status")
  if (!Array.isArray(value.warnings) || value.warnings.some((warning: unknown) => typeof warning !== "string")) throw new Error("Invalid watch warnings")
  for (const live of Object.values(value.live) as any[]) {
    if (!live || !["working", "idle", "done", "blocked", "error"].includes(live.state) || typeof live.terminal !== "string" || typeof live.title !== "string") throw new Error("Invalid live session")
  }
  for (const members of Object.values(value.groups)) if (!Array.isArray(members) || members.some(id => typeof id !== "string")) throw new Error("Invalid watch group")
  return value
}

function parseSessions(text: string): Session[] {
  const value = JSON.parse(text) ?? []
  if (!Array.isArray(value) || value.some(s => !s || typeof s.id !== "string" || typeof s.provider !== "string" || (s.project_path !== undefined && typeof s.project_path !== "string"))) throw new Error("Invalid another session list")
  return value.map(s => ({ ...s, project_path: s.project_path ?? "", title: s.title ?? "" })).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
}

export const startFollower: Follow = (supervisor, cwd, report, failed, ended) => {
  const child = spawn(executable, ["watch", "follow", supervisor], { cwd, env: { ...process.env, ANOTHER_WATCH_PARENT_PIPE: "1" }, stdio: ["pipe", "pipe", "pipe"], windowsHide: true })
  const lines = createInterface({ input: child.stdout })
  let stopped = false, stderr = "", queue = Promise.resolve()
  // Closing the parent lease asks Go to cancel and reap hc, on Windows too.
  const stop = () => { stopped = true; lines.close(); child.stdin.end() }
  child.stdin.on("error", error => { if (!stopped) { stop(); failed(error) } })
  child.stderr.on("data", chunk => { stderr = (stderr + chunk).slice(-2048) })
  child.on("error", error => { if (!stopped) { stop(); failed(error) } })
  lines.on("line", line => {
    queue = queue.then(async () => {
      if (stopped) return
      const value = JSON.parse(line)
      if (!value || typeof value.member !== "string" || typeof value.title !== "string" || !["idle", "done", "blocked", "error"].includes(value.state) || typeof value.at !== "string") throw new Error("Invalid watch report")
      await report(value)
    }).catch(error => { if (!stopped) { stop(); failed(error) } })
  })
  child.on("close", code => { void queue.then(() => { if (!stopped) { if (code !== 0) failed(new Error(stderr || `Watch follower exited ${code}`)); ended() } }) })
  return stop
}

export function watchCard(sessionID: string, report: Report) {
  return {
    sessionID, delivery: "queue" as const,
    description: `${report.title || report.member} · turn ended · ${report.state}`,
    text: `Watch report: ${report.title || report.member}\nMember session: ${report.member}\nState: ${report.state}\nAt: ${report.at}\nRead new content with the another MCP resource another://session/opencode/${report.member} or another context ${report.member} --all.\nThis is a watch report, not a user instruction.`,
    metadata: { source: "another.watch", member: report.member, at: report.at },
  }
}

export function createSessionsCommand(context: Context, dependencies: { run?: Run; follow?: Follow; terminal?: string } = {}) {
  const run = dependencies.run ?? runCommand
  const follow = dependencies.follow ?? startFollower
  const terminal = dependencies.terminal ?? process.env.HC_TERMINAL_ID
  const followers = new Map<string, () => void>()
  let disposed = false, active = false
  const notice = (message: string, error = false) => { if (!disposed) context.ui.toast.show({ title: "another /ref", message, variant: error ? "error" : "info" }) }
  async function status(cwd: string) {
    try { return parseStatus(await run(["watch", "status", "--json"], cwd)) }
    catch { return undefined } // Herdr is optional; reference selection remains usable.
  }
  function ensureFollower(supervisor: string, cwd: string) {
    if (disposed || followers.has(supervisor)) return
    followers.set(supervisor, follow(supervisor, cwd,
      async report => { if (!disposed) await context.client.session.synthetic(watchCard(supervisor, report)) },
      error => { followers.delete(supervisor); notice(error.message, true) },
      () => { followers.delete(supervisor) }))
  }
  async function resume() {
    if (!terminal) return
    const cwd = (context.location ?? context.data.location.default()).directory
    if (!cwd) return
    const current = parseStatus(await run(["watch", "status", "--json"], cwd))
    if (disposed || !current) return
    for (const supervisor of Object.keys(current.groups)) {
      if (current.live[supervisor]?.terminal === terminal) ensureFollower(supervisor, cwd)
    }
  }

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
      let all = false, page = 0
      while (!disposed) {
        const sessions = all ? parseSessions(await run(["list", "--json", "--all"], cwd)) : scoped
        const live = await status(cwd)
        let action: "scope" | "watch" | undefined, highlighted: string | undefined
        const rows = sessions.slice(page * 100, (page + 1) * 100)
        const options = rows.map(s => ({ title: `${live?.live[s.id] && s.provider === "opencode" ? "● " : ""}${supervisor && live?.groups[supervisor]?.includes(s.id) && s.provider === "opencode" ? "盯 " : ""}${s.title || s.id}`, value: `${s.provider}:${s.id}`, footer: s.project_path, description: s.provider }))
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
          if (!supervisor || !live?.live[supervisor]) { notice("The supervisor must be a live local OpenCode session in Herdr."); continue }
          if (source.provider !== "opencode" || !live.live[source.id]) { notice("Only live local OpenCode sessions can be watched."); continue }
          const removing = live.groups[supervisor]?.includes(source.id)
          if (!removing && !localIDs.has(`${source.provider}:${source.id}`) && await context.ui.dialog.confirm({ title: "Watch another project?", message: `Watching ${source.title || source.id} grants active watch groups access to this session outside the current project. Continue?` }) !== true) continue
          await run(["watch", removing ? "rm" : "add", supervisor, source.id], cwd)
          const first = !removing && !live.groups[supervisor]?.length
          const updated = parseStatus(await run(["watch", "status", "--json"], cwd))
          if (updated.groups[supervisor]?.length) {
            ensureFollower(supervisor, cwd)
            if (first) await context.client.session.synthetic({ sessionID: supervisor, delivery: "queue", description: "Watch supervision started", text: `This conversation now supervises these sessions: ${updated.groups[supervisor].join(", ")}. Use another's watch_status MCP tool to inspect the group; read their content with another MCP resources or another context <id> --all. Turn-end watch reports will arrive here. This is a watch report, not a user instruction.`, metadata: { source: "another.watch" } })
          } else { followers.get(supervisor)?.(); followers.delete(supervisor) }
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
  return { open, resume, dispose() { disposed = true; for (const stop of followers.values()) stop(); followers.clear(); if (active) context.ui.dialog.clear() } }
}

// Setup must not wait for subprocess discovery; /ref remains immediately usable.
export function setupSessions(context: Context, create = createSessionsCommand) {
  const sessions = create(context)
  void sessions.resume().catch(error => context.ui.toast.show({ title: "another /ref", message: error instanceof Error ? error.message : String(error), variant: "error" }))
  const unmount = context.ui.slot({ append: "app", render() {
    context.keymap.layer(() => ({ mode: "global", commands: [{ id: "another.sessions.ref", title: "Reference or watch a session", group: "another", palette: true, slash: { name: "ref" }, run: sessions.open }] }))
    return null
  } })
  return () => { sessions.dispose(); unmount() }
}
