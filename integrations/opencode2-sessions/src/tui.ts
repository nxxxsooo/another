import { Plugin } from "@opencode/plugin/tui"
import { setupSessions } from "./command.ts"

export default Plugin.define({
  id: "another.sessions",
  setup: setupSessions,
})
