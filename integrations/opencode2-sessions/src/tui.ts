import { Plugin } from "@opencode/plugin/tui"
import { setupSessions } from "./command.ts"
import { commentPrompt } from "./comment.tsx"

export default Plugin.define({
  id: "another.sessions",
  setup(context) { return setupSessions(context, commentPrompt(context)) },
})
