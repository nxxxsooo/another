import assert from "node:assert/strict"
import { test } from "node:test"
import type { Context } from "@opencode/plugin/tui/context"
import { createSessionsCommand, parseReferenceInput, providerFilterKey, setupSessions } from "./command.ts"
import { parseStatus, watchCard } from "./watch.ts"

function fixture() {
  const rows = [{ id: "ses_member", provider: "opencode", title: "Member", project_path: "project", updated_at: "2026-11-12T00:00:00Z" }]
  const busy = ["ses_member"]
  const groups: Record<string, string[]> = {}
  const menus: any[] = [], prompts: any[] = [], notices: any[] = [], calls: string[][] = [], rpcCalls: any[] = [], confirms: any[] = [], comments: any[] = [], created: any[] = []
  let route: any = { type: "session", sessionID: "ses_super" }, unavailable = false, fresh = false
  let response: string | undefined = "Use this evidence"
  let statusCalls = 0
  let select: (menu: any) => any = () => undefined
  const status = () => ({ groups })
  const context = {
    location: { directory: "project" },
    client: { session: {
      get: async () => ({ id: "ses_super", title: "Supervisor", location: { directory: "project" } }),
      create: async (input: any) => { created.push(input); return { id: "ses_created" } }, prompt: async (input: any) => { prompts.push(input) },
    }, message: { list: async () => ({ data: fresh ? [] : [{}] }) }, rpc: () => ({
      status: async () => { statusCalls++; if (unavailable) throw new Error("RPC unavailable"); return status() },
      add: async (input: any, options: any) => { rpcCalls.push({ method: "add", input, options }); groups[input.supervisor] = [input.member]; return status() },
      rm: async (input: any, options: any) => { rpcCalls.push({ method: "rm", input, options }); delete groups[input.supervisor]; return status() },
    }) },
    ui: { router: { current: () => route, navigate: (value: any) => { route = value } }, model: { current: () => undefined },
      dialog: { select: async (menu: any) => { menus.push(menu); return select(menu) }, set() {}, clear() {}, confirm: async (input: any) => { confirms.push(input); return true } },
      toast: { show: (value: any) => notices.push(value) },
    }, data: { location: { default: () => ({ directory: "project" }) }, session: { status: (id: string) => busy.includes(id) ? "running" : "idle" } },
  } as unknown as Context
  const dependencies = { comment: async (options: any) => { comments.push(options); return response }, run: async (args: string[], cwd: string) => {
    assert.equal(cwd, "project"); calls.push(args)
    const scoped = rows.filter(s => args.includes("--all") || s.project_path === "project")
    if (args[0] === "list") return JSON.stringify(scoped)
    if (args[0] === "search") return JSON.stringify({ results: scoped.map(session => ({ session, match_type: "content" })), content_index: {} })
    if (args[0] === "context") return "# Bounded session reference"
    throw new Error(`Unexpected command: ${args}`)
  } }
  return { context, dependencies, rows, busy, groups, menus, prompts, notices, calls, confirms, rpcCalls, comments, created,
    select(fn: typeof select) { select = fn }, route(value: any) { route = value }, unavailable() { unavailable = true }, fresh() { fresh = true },
    comment(value: string | undefined) { response = value },
    statusCalls: () => statusCalls,
  }
}

test("reference captures route before I/O and attaches optional comment then continues", async () => {
  const f = fixture()
  f.select(() => { f.route({ type: "session", sessionID: "ses_other" }); return "opencode:ses_member" })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open()
  assert.equal(f.prompts[0].sessionID, "ses_super"); assert.equal(f.prompts[0].resume, true)
  assert.match(f.prompts[0].text, /Use this evidence\n\n# Bounded/)
  assert.equal(f.prompts[0].delivery, "steer")
  assert.equal(f.confirms.length, 0)
  assert.equal(f.comments[0].title, 'Reference "Member" → "Supervisor"')
  assert.deepEqual(f.calls.find(a => a[0] === "context"), ["context", "ses_member", "--provider", "opencode", "--cached"])
  command.dispose()
})

test("fresh home session attaches without starting a turn", async () => {
  const f = fixture(); f.route({ type: "home" }); f.fresh(); f.select(() => "opencode:ses_member")
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts[0].sessionID, "ses_created"); assert.equal(f.prompts[0].resume, false)
  assert.equal(f.comments[0].title, 'Reference "Member" → "New session"')
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

test("non-OpenCode sessions can be referenced but cannot be watched", async () => {
  const f = fixture(); f.rows[0].provider = "pi"
  f.select(menu => { if (f.menus.length === 1) menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("pi:ses_member"); return undefined })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.rpcCalls.length, 0); assert.match(f.notices[0].message, /Only OpenCode/)
  f.select(() => "pi:ses_member")
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts[0].metadata.anotherReference.provider, "pi")
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
  let command: any
  const context = { ui: { slot(claim: any) { registered = true; claim.render(); return () => {} } }, keymap: { layer(factory: any) { command = factory().commands[0] } } } as unknown as Context
  const cleanup = setupSessions(context, async () => "", () => ({ open: async () => {}, dispose() { disposed = true } }))
  assert.equal(typeof cleanup, "function"); assert.equal(registered, true)
  assert.deepEqual(command.slash, { name: "ref", arguments: true })
  cleanup(); assert.equal(disposed, true)
})

test("provider filter cycles all then each present provider, retains tags, and excludes the current session", async () => {
  const f = fixture()
  f.rows.push({ ...f.rows[0], id: "ses_super", title: "Current" }, { ...f.rows[0], id: "ses_super", provider: "pi", title: "Same ID, another agent" }, { ...f.rows[0], id: "claude", provider: "claude-code", title: "Claude" })
  const seen: string[][] = []
  f.select(menu => {
    assert.ok(menu.title.startsWith("Reference / watch · This project"))
    assert.ok(!menu.options.some((row: any) => row.value === "opencode:ses_super"))
    seen.push(menu.options.map((row: any) => row.description))
    if (f.menus.length < 5) menu.actions.find((a: any) => a.bind === providerFilterKey).onTrigger()
    return undefined
  })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.deepEqual(seen.map(tags => [...new Set(tags)]), [["opencode", "pi", "claude-code"], ["claude-code"], ["opencode"], ["pi"], ["opencode", "pi", "claude-code"]])
  assert.equal(f.notices.length, 0)
  assert.ok(f.calls.every(args => !args.includes("--include-subagents")))
})

test("keyword arguments use wrapped full-text search results and Ctrl+A re-runs the same query", async () => {
  const f = fixture()
  f.select(menu => { if (f.menus.length === 1) { menu.actions.find((a: any) => a.bind === "ctrl+a").onTrigger(); return }; return "opencode:ses_member" })
  await createSessionsCommand(f.context, f.dependencies).open("--all webhook retry")
  const searches = f.calls.filter(args => args[0] === "search")
  assert.deepEqual(searches, [["search", "--json", "--cached", "--no-wait", "--all", "--", "webhook retry"], ["search", "--json", "--cached", "--no-wait", "--", "webhook retry"]])
  assert.equal(f.calls.filter(args => args[0] === "list").length, 0)
  assert.ok(f.menus[0].title.includes("All projects")); assert.ok(f.menus[1].title.includes("This project"))
  assert.equal(f.prompts.length, 1)
})

test("--all without keywords uses widened list; --tui is rejected without I/O", async () => {
  const f = fixture(); await createSessionsCommand(f.context, f.dependencies).open("--all")
  assert.deepEqual(f.calls, [["list", "--json", "--cached", "--all"], ["list", "--json", "--cached"]])
  const bad = fixture(); await createSessionsCommand(bad.context, bad.dependencies).open("--all --tui")
  assert.match(bad.notices[0].message, /does not support --tui/); assert.equal(bad.calls.length, 0)
  assert.deepEqual(parseReferenceInput("  keyword --all literal  "), { query: "keyword --all literal", all: false })
})

test("one multiline comment sends, empty comment sends, Escape cancels without home creation", async () => {
  for (const response of ["line one\nline two", "", undefined]) {
    const f = fixture(); f.route({ type: "home" }); f.comment(response)
    f.select(() => "opencode:ses_member")
    const comment = f.dependencies.comment
    f.dependencies.comment = async options => { assert.equal(f.created.length, 0); return comment(options) }
    await createSessionsCommand(f.context, f.dependencies).open()
    assert.equal(f.confirms.length, 0); assert.equal(f.comments.length, 1)
    assert.equal(f.created.length, response === undefined ? 0 : 1)
    assert.equal(f.prompts.length, response === undefined ? 0 : 1)
    if (response !== undefined) assert.equal(f.prompts[0].text, `${response ? response + "\n\n" : ""}# Bounded session reference`)
  }
  const established = fixture(); established.fresh(); established.select(() => "opencode:ses_member")
  await createSessionsCommand(established.context, established.dependencies).open()
  assert.equal(established.created.length, 0); assert.equal(established.prompts[0].resume, false)
})

test("empty full-text results are valid and keep the scope/filter actions", async () => {
  const f = fixture(); f.dependencies.run = async () => '{"results":null,"content_index":{}}'
  f.select(menu => { assert.equal(menu.options.length, 0); assert.ok(menu.actions.some((a: any) => a.bind === providerFilterKey)); return undefined })
  await createSessionsCommand(f.context, f.dependencies).open("no matches")
  assert.equal(f.notices.length, 0)
})

test("provider and cached scope toggles never respawn or repeat native status; repeated searches reuse in-flight/results", async () => {
  const f = fixture()
  f.select(menu => {
    const actions = [providerFilterKey, "ctrl+a", "ctrl+a", providerFilterKey]
    const bind = actions[f.menus.length - 1]
    if (bind) menu.actions.find((action: any) => action.bind === bind).onTrigger()
    return undefined
  })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open("webhook")
  await command.open("webhook")
  const calls = f.calls.filter(args => args[0] === "search")
  assert.equal(calls.length, 2)
  assert.equal(f.statusCalls(), 2) // One snapshot per open, not per dialog re-render.
  assert.ok(calls.every(args => args.includes("--cached") && args.includes("--no-wait")))
  command.dispose()
})
