import { Plugin } from "@opencode/plugin/tui"
import { createSessionsCommand } from "./command.ts"

export default Plugin.define({
  id: "another.sessions",
  async setup(context) {
    const sessions = createSessionsCommand(context)
    await sessions.resume()
    const unmount = context.ui.slot({ append: "app", render() {
      context.keymap.layer(() => ({ mode: "global", commands: [{ id: "another.sessions.ref", title: "Reference or watch a session", group: "another", palette: true, slash: { name: "ref" }, run: sessions.open }] }))
      return null
    } })
    return () => { sessions.dispose(); unmount() }
  },
})
