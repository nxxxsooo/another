import type { Plugin } from "@opencode-ai/plugin"
import { finalizeTitle, loadLanguage, parsePartialTitle, titlePrompt } from "./policy.ts"

const plugin = {
  id: "another.title-policy",
  async setup(ctx) {
    const language = loadLanguage()
    const registration = await ctx.agent.transform((editor) => {
      editor.update("title", (agent) => {
        agent.system = titlePrompt(language)
      })
    })

    const controller = new AbortController()
    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          if (event.type !== "session.renamed") continue
          // The title the model just produced is the whole trigger. Nothing is
          // remembered between events on purpose: an earlier version kept the
          // pending sessions in memory and lost the date whenever the plugin
          // reloaded, the server restarted, or the session belonged to a
          // directory whose instance was not the one watching.
          if (!parsePartialTitle(event.data.title)) continue
          const session = await ctx.session.get({ sessionID: event.data.sessionID })
          // Child sessions are named after the task that spawned them, which
          // is more useful to their parent than a dated policy title.
          if (session.parentID) continue
          const title = finalizeTitle(event.data.title, session.time.created, language)
          if (!title) continue
          // A dated title no longer parses as Type｜Topic, so the rename this
          // triggers ends here rather than looping.
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
