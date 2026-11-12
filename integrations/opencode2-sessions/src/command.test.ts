import assert from "node:assert/strict"
import { test } from "node:test"
import type { Context } from "@opencode/plugin/tui/context"
import { createSessionsCommand, setupSessions } from "./command.ts"
import { parseStatus, watchCard } from "./watch.ts"

function fixture() {
  const rows = [{ id: "ses_member", provider: "opencode", title: "Member", project_path: "project", updated_at: "2026-11-12T00:00:00Z" }]
  const busy = ["ses_member"]
  const groups: Record<string, string[]> = {}
  const menus: any[] = [], prompts: any[] = [], notices: any[] = [], calls: string[][] = [], rpcCalls: any[] = [], confirms: any[] = []
  let route: any = { type: "session", sessionID: "ses_super" }, unavailable = false, fresh = false
  let select: (menu: any) => any = () => undefined
  const status = () => ({ groups })
  const context = {
    location: { directory: "project" },
    client: { session: {
      get: async () => ({ id: "ses_super", title: "Supervisor", location: { directory: "project" } }),
      create: async () => ({ id: "ses_created" }), prompt: async (input: any) => { prompts.push(input) },
    }, message: { list: async () => ({ data: fresh ? [] : [{}] }) }, rpc: () => ({
      status: async () => { if (unavailable) throw new Error("RPC unavailable"); return status() },
      add: async (input: any, options: any) => { rpcCalls.push({ method: "add", input, options }); groups[input.supervisor] = [input.member]; return status() },
      rm: async (input: any, options: any) => { rpcCalls.push({ method: "rm", input, options }); delete groups[input.supervisor]; return status() },
    }) },
    ui: { router: { current: () => route, navigate: (value: any) => { route = value } }, model: { current: () => undefined },
      dialog: { select: async (menu: any) => { menus.push(menu); return select(menu) }, set() {}, clear() {}, prompt: async () => "Use this evidence", confirm: async (input: any) => { confirms.push(input); return true } },
      toast: { show: (value: any) => notices.push(value) },
    }, data: { location: { default: () => ({ directory: "project" }) }, session: { status: (id: string) => busy.includes(id) ? "running" : "idle" } },
  } as unknown as Context
  const dependencies = { run: async (args: string[], cwd: string) => {
    assert.equal(cwd, "project"); calls.push(args)
    if (args[0] === "list") return JSON.stringify(rows)
    if (args[0] === "context") return "# Bounded session reference"
    throw new Error(`Unexpected command: ${args}`)
  } }
  return { context, dependencies, rows, busy, groups, menus, prompts, notices, calls, confirms, rpcCalls,
    select(fn: typeof select) { select = fn }, route(value: any) { route = value }, unavailable() { unavailable = true }, fresh() { fresh = true },
  }
}

test("reference captures route before I/O and attaches optional comment then continues", async () => {
  const f = fixture()
  f.select(() => { f.route({ type: "session", sessionID: "ses_other" }); return "opencode:ses_member" })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open()
  assert.equal(f.prompts[0].sessionID, "ses_super"); assert.equal(f.prompts[0].resume, true)
  assert.match(f.prompts[0].text, /Use this evidence\n\n# Bounded/)
  assert.deepEqual(f.calls.find(a => a[0] === "context"), ["context", "ses_member", "--provider", "opencode"])
  command.dispose()
})

test("fresh home session attaches without starting a turn", async () => {
  const f = fixture(); f.route({ type: "home" }); f.fresh(); f.select(() => "opencode:ses_member")
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts[0].sessionID, "ses_created"); assert.equal(f.prompts[0].resume, false)
})

test("scope toggle passes --all to both list and context", async () => {
  const f = fixture()
  f.select(menu => { if (f.menus.length === 1) { menu.actions.find((a: any) => a.bind === "ctrl+a").onTrigger(); return }; return "opencode:ses_member" })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.ok(f.calls.some(a => a[0] === "list" && a.includes("--all")))
  assert.ok(f.calls.some(a => a[0] === "context" && a.includes("--all")))
})

test("busy marker comes from OpenCode, idle sessions can be watched through RPC, marks update", async () => {
  const f = fixture()
  f.select(menu => {
    if (f.menus.length === 1) {
      assert.match(menu.options[0].title, /●/)
      f.busy.length = 0
      menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_member"); return
    }
    assert.doesNotMatch(menu.options[0].title, /●/); assert.match(menu.options[0].title, /盯/)
    menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_member")
    f.select(menu => { assert.doesNotMatch(menu.options[0].title, /盯/); return undefined })
  })
  const command = createSessionsCommand(f.context, f.dependencies); await command.open(); command.dispose()
  assert.deepEqual(f.rpcCalls.map(c => c.method), ["add", "rm"])
  assert.deepEqual(f.rpcCalls[0].input, { supervisor: "ses_super", member: "ses_member" })
  assert.equal(f.rpcCalls[0].options.location.directory, "project")
})

test("RPC failure is visible, but referencing remains usable", async () => {
  const f = fixture(); f.unavailable()
  f.select(menu => { assert.match(menu.options[0].title, /● Member/); assert.ok(!menu.actions.some((a: any) => a.bind === "ctrl+w")); return "opencode:ses_member" })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts.length, 1); assert.match(f.notices[0].message, /RPC unavailable/)
})

test("cross-project watch requires explicit confirmation", async () => {
  const f = fixture()
  f.select(menu => {
    if (f.menus.length === 1) {
      f.rows.push({ id: "ses_cross", provider: "opencode", title: "Cross project", project_path: "other-project", updated_at: "2026-11-13T00:00:00Z" })
      menu.actions.find((a: any) => a.bind === "ctrl+a").onTrigger()
    } else if (f.menus.length === 2) menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_cross")
    return undefined
  })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.confirms[0].title, "Watch another project?")
  assert.equal(f.rpcCalls[0].input.member, "ses_cross")
})

test("non-OpenCode sessions cannot be watched; self-watch is refused", async () => {
  for (const self of [true, false]) {
    const f = fixture(); if (self) f.rows[0].id = "ses_super"; else f.rows[0].provider = "pi"
    f.select(menu => { if (f.menus.length === 1) menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger(`${f.rows[0].provider}:${f.rows[0].id}`); return undefined })
    await createSessionsCommand(f.context, f.dependencies).open()
    assert.equal(f.rpcCalls.length, 0); assert.match(f.notices[0].message, self ? /different conversation/ : /Only OpenCode/)
  }
})

test("watch status parser and report contract", () => {
  assert.throws(() => parseStatus({ busy: [], groups: null }))
  assert.throws(() => parseStatus({ busy: [], groups: { ses_x: "bad" } }))
  const card = watchCard("ses_super", { member: "ses_member", title: "Member", state: "idle", at: "now" })
  assert.equal(card.delivery, "queue"); assert.match(card.text, /another context ses_member --provider opencode --all/)
  assert.match(card.text, /not a user instruction/); assert.doesNotMatch(card.text, /MCP/)
})

test("setup is synchronous and starts no follower/discovery", () => {
  let registered = false, disposed = false
  const context = { ui: { slot() { registered = true; return () => {} } } } as unknown as Context
  const cleanup = setupSessions(context, () => ({ open: async () => {}, dispose() { disposed = true } }))
  assert.equal(typeof cleanup, "function"); assert.equal(registered, true)
  cleanup(); assert.equal(disposed, true)
})
