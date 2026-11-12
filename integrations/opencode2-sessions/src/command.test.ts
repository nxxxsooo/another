import assert from "node:assert/strict"
import { test } from "node:test"
import type { Context } from "@opencode/plugin/tui/context"
import { createSessionsCommand, parseStatus, watchCard, type Report } from "./command.ts"

function fixture() {
  const rows = [{ id: "ses_member", provider: "opencode", title: "Member", project_path: "project", updated_at: "2026-11-12T00:00:00Z" }]
  const live = { ses_super: { terminal: "t1", state: "idle", title: "Supervisor" }, ses_member: { terminal: "t2", state: "working", title: "Member" } }
  const groups: Record<string, string[]> = {}
  const menus: any[] = [], prompts: any[] = [], cards: any[] = [], notices: any[] = [], calls: string[][] = [], confirms: any[] = []
  let route: any = { type: "session", sessionID: "ses_super" }, unavailable = false, stopped = 0, fresh = false
  let select: (menu: any) => any = () => undefined
  let report: ((value: Report) => Promise<void>) | undefined
  const context = {
    location: { directory: "project" },
    client: { session: {
      get: async () => ({ id: "ses_super", title: "Supervisor", location: { directory: "project" } }),
      create: async () => ({ id: "ses_created" }),
      prompt: async (input: any) => { prompts.push(input) },
      synthetic: async (input: any) => { cards.push(input) },
    }, message: { list: async () => ({ data: fresh ? [] : [{}] }) } },
    ui: { router: { current: () => route, navigate: (value: any) => { route = value } }, model: { current: () => undefined },
      dialog: { select: async (menu: any) => { menus.push(menu); return select(menu) }, set() {}, clear() {}, prompt: async () => "Use this evidence", confirm: async (input: any) => { confirms.push(input); return true } },
      toast: { show: (value: any) => notices.push(value) },
    }, data: { location: { default: () => ({ directory: "project" }) } },
  } as unknown as Context
  const dependencies = { terminal: "t1", run: async (args: string[], cwd: string) => {
    assert.equal(cwd, "project")
    calls.push(args)
    if (args[0] === "list") return JSON.stringify(rows)
    if (args[0] === "context") return "# Bounded session reference"
    if (unavailable) throw new Error("hc unavailable")
    if (args[1] === "status") return JSON.stringify({ live, groups })
    if (args[1] === "add") groups[args[2]] = [args[3]]
    if (args[1] === "rm") delete groups[args[2]]
    return '{"ok":true}'
  }, follow: (_supervisor: string, _cwd: string, onReport: (value: Report) => Promise<void>) => { report = onReport; return () => { stopped++ } } }
  return { context, dependencies, rows, live, groups, menus, prompts, cards, notices, calls, confirms,
    select(fn: typeof select) { select = fn }, route(value: any) { route = value }, unavailable() { unavailable = true }, fresh() { fresh = true },
    report: (value: Report) => report!(value), stopped: () => stopped,
  }
}

const end: Report = { member: "ses_member", title: "Member", state: "idle", at: "2026-11-12T00:00:00Z" }

test("reference captures route before I/O and attaches optional comment then continues", async () => {
  const f = fixture()
  f.select(() => { f.route({ type: "session", sessionID: "ses_other" }); return "opencode:ses_member" })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open()
  assert.equal(f.prompts.length, 1)
  assert.equal(f.prompts[0].sessionID, "ses_super")
  assert.equal(f.prompts[0].resume, true)
  assert.match(f.prompts[0].text, /Use this evidence\n\n# Bounded/)
  assert.deepEqual(f.calls.find(a => a[0] === "context"), ["context", "ses_member", "--provider", "opencode"])
  command.dispose()
})

test("fresh home session attaches without starting a turn", async () => {
  const f = fixture(); f.route({ type: "home" }); f.fresh()
  f.select(() => "opencode:ses_member")
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts[0].sessionID, "ses_created")
  assert.equal(f.prompts[0].resume, false)
})

test("scope toggle passes --all to both list and context", async () => {
  const f = fixture()
  f.select(menu => { if (f.menus.length === 1) { menu.actions.find((a: any) => a.bind === "ctrl+a").onTrigger(); return }; return "opencode:ses_member" })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.ok(f.calls.some(a => a[0] === "list" && a.includes("--all")))
  assert.ok(f.calls.some(a => a[0] === "context" && a.includes("--all")))
})

test("watch and unwatch reopen menu, update marks, deliver reports, stop on empty", async () => {
  const f = fixture()
  f.select(menu => {
    if (f.menus.length === 1) { assert.match(menu.options[0].title, /●/); menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_member"); return }
    assert.match(menu.options[0].title, /盯/)
    menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_member")
    // The third menu has no watch marker after removal.
    f.select(menu => { assert.doesNotMatch(menu.options[0].title, /盯/); return undefined })
  })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open()
  assert.equal(f.cards[0].delivery, "queue")
  assert.match(f.cards[0].text, /now supervises.*ses_member/)
  assert.equal(f.stopped(), 1)
  assert.ok(f.calls.some(a => a[1] === "add")); assert.ok(f.calls.some(a => a[1] === "rm"))
  command.dispose()
})

test("resume only this terminal's supervisors and queue synthetic turn-end cards", async () => {
  const f = fixture(); f.groups.ses_super = ["ses_member"]; f.groups.ses_member = ["ses_super"]
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.resume()
  await f.report(end)
  assert.equal(f.cards[0].sessionID, "ses_super")
  assert.equal(f.cards[0].description, "Member · turn ended · idle")
  assert.match(f.cards[0].text, /another:\/\/session\/opencode\/ses_member/)
  assert.match(f.cards[0].text, /not a user instruction/)
  command.dispose(); assert.equal(f.stopped(), 1)
})

test("Herdr unavailable leaves referencing usable without markers or watch key", async () => {
  const f = fixture(); f.unavailable()
  f.select(menu => { assert.equal(menu.options[0].title, "Member"); assert.ok(!menu.actions.some((a: any) => a.bind === "ctrl+w")); return "opencode:ses_member" })
  await createSessionsCommand(f.context, f.dependencies).open()
  assert.equal(f.prompts.length, 1)
})

test("cross-project watch requires explicit confirmation", async () => {
  const f = fixture()
  f.select(menu => {
    if (f.menus.length === 1) {
      // Only appears after the all-project list is fetched.
      f.rows.push({ id: "ses_cross", provider: "opencode", title: "Cross project", project_path: "other-project", updated_at: "2026-11-13T00:00:00Z" })
      ;(f.live as any).ses_cross = { terminal: "t3", state: "idle", title: "Cross project" }
      menu.actions.find((a: any) => a.bind === "ctrl+a").onTrigger()
    } else if (f.menus.length === 2) {
      menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger("opencode:ses_cross")
    }
    return undefined
  })
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.open()
  assert.equal(f.confirms[0].title, "Watch another project?")
  assert.match(f.confirms[0].message, /grants active watch groups access/)
  assert.ok(f.calls.some(a => a[1] === "add" && a[3] === "ses_cross"))
  command.dispose()
})

test("disposing a loaded plugin stops followers and suppresses later delivery", async () => {
  const f = fixture(); f.groups.ses_super = ["ses_member"]
  const command = createSessionsCommand(f.context, f.dependencies)
  await command.resume()
  command.dispose()
  await f.report(end)
  assert.equal(f.stopped(), 1)
  assert.equal(f.cards.length, 0)
})

test("non-OpenCode watch and missing supervisor show reasons without writes", async () => {
  for (const nonOpenCode of [true, false]) {
    const f = fixture()
    if (nonOpenCode) f.rows[0].provider = "pi"
    else delete (f.live as any).ses_super
    f.select(menu => { if (f.menus.length === 1) menu.actions.find((a: any) => a.bind === "ctrl+w").onTrigger(`${f.rows[0].provider}:ses_member`); return undefined })
    await createSessionsCommand(f.context, f.dependencies).open()
    assert.equal(f.calls.filter(a => a[1] === "add").length, 0)
    assert.match(f.notices[0].message, /live local OpenCode/)
  }
})

test("status parser fails closed and card contract is queue-only", () => {
  assert.throws(() => parseStatus('{"live":null,"groups":{}}'))
  assert.throws(() => parseStatus('{"live":{},"groups":{"ses_x":"bad"}}'))
  assert.equal(watchCard("ses_super", end).delivery, "queue")
})
