import type { Plugin } from "@opencode-ai/plugin"
import { finalizeTitle, loadLanguage, titlePrompt } from "./policy.ts"

const pendingForMs = 10 * 60 * 1000

const plugin = {
  id: "another.title-policy",
  async setup(ctx) {
    const language = loadLanguage()
    const registration = await ctx.agent.transform((editor) => {
      editor.update("title", (agent) => {
        agent.system = titlePrompt(language)
      })
    })

    const pending = new Map<string, number>()
    const controller = new AbortController()
    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          if (event.type === "session.created") {
            if (!event.data.parentID && event.data.location.directory === ctx.location.directory) {
              pending.set(event.data.sessionID, event.created)
            }
            continue
          }
          if (event.type !== "session.renamed") continue
          const started = pending.get(event.data.sessionID)
          if (!started) continue
          if (Date.now() - started > pendingForMs) {
            pending.delete(event.data.sessionID)
            continue
          }
          const title = finalizeTitle(event.data.title, started, language)
          if (!title) continue
          pending.delete(event.data.sessionID)
          await ctx.session.rename({ sessionID: event.data.sessionID, title })
        }
      } catch (error) {
        if (!controller.signal.aborted) console.error("another title policy event loop failed", error)
      }
    })()

    return async () => {
      controller.abort()
      await registration.dispose()
    }
  },
} satisfies Plugin.Plugin

export default plugin
