import type { Plugin } from "@opencode/plugin"
import {
  fallbackTitle,
  finalizeTitle,
  firstTitle,
  isRefusal,
  loadLanguage,
  parsePartialTitle,
  repairPrompt,
  repairTitle,
  titlePrompt,
} from "./policy.ts"

// OpenCode 2 asks for a title once, moments after the first prompt of a
// session, and never asks again. When that single request does not land — the
// title provider fails, or the server restarts while the session is running —
// the session keeps no name at all: OpenCode 2 lists it as
// `New session - <timestamp>` and another falls back to showing the raw prompt.
// The wait below is what keeps the second attempt from racing a title that is
// merely slow, and is measured against the native one, which arrives about a
// second after the prompt.
const repairDelay = 5000

function wait(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve()
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", stop)
      resolve()
    }, ms)
    function stop() {
      clearTimeout(timer)
      resolve()
    }
    signal.addEventListener("abort", stop, { once: true })
  })
}

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

    // A title agent is free to name a model the catalog hides — a background
    // relay pinned in the configuration and marked deprecated resolves for the
    // native title request but is refused as `Model unavailable` when asked for
    // directly. That refusal costs nothing and arrives before any provider is
    // called, so it is worth trying the cheap model first and letting the
    // default model answer when it is not reachable this way.
    const generate = async (prompt: string, model?: { id: string; providerID: string; variant?: string }) => {
      if (model) {
        try {
          return (await ctx.generate.text({ prompt, model })).text
        } catch {
          // Fall through to the default model.
        }
      }
      return (await ctx.generate.text({ prompt })).text
    }

    // Supply the final title before OpenCode's native title request is sent.
    // A rename event can be missed when a location reloads or its subscription
    // fails; the native title must not depend on a later event for its date.
    const titleHook = await ctx.session.hook("title", async (event) => {
      const session = await ctx.session.get({ sessionID: event.sessionID })
      if (session.parentID || session.title?.trim()) return
      const messages = await ctx.session.context({ sessionID: event.sessionID })
      const first = messages.find((message) => message.type === "user" && message.text.trim() !== "")
      if (first?.type !== "user") return
      try {
        event.result = firstTitle(await generate(repairPrompt(language, first.text), event.model), session.time.created, language)
      } catch (error) {
        console.error("another title policy generation failed", error)
        event.result = fallbackTitle(session.time.created, language)
      }
    })

    // A session going idle is the only signal the plugin gets that a turn
    // finished, and it arrives again on every later turn, so the same session
    // can be in here more than once at a time. The set is not memory of what
    // is owed — it lasts only as long as the attempt it guards.
    const repairing = new Set<string>()
    const repair = async (sessionID: string) => {
      if (repairing.has(sessionID)) return
      repairing.add(sessionID)
      try {
        const session = await ctx.session.get({ sessionID })
        // Child sessions are named after the task that spawned them, and a
        // session that already has any name is not this plugin's to replace:
        // the dated policy title is applied on rename, not on idle.
        if (session.parentID || session.title?.trim()) return
        await wait(repairDelay, controller.signal)
        if (controller.signal.aborted) return
        if ((await ctx.session.get({ sessionID })).title?.trim()) return
        const messages = await ctx.session.context({ sessionID })
        const first = messages.find((message) => message.type === "user" && message.text.trim() !== "")
        if (first?.type !== "user") return
        // The title agent's own model, so the retry costs what the request it
        // stands in for would have cost, and background title traffic stays on
        // whichever provider the user routed it to.
        const model = (await ctx.agent.list()).data.find((agent) => agent.id === "title")?.model
        const prompt = repairPrompt(language, first.text)
        const answer = await generate(prompt, model)
        const title = repairTitle(answer, session.time.created, language)
        // An answer that is not a policy title is left unwritten rather than
        // forced: the session keeps no name, which is the state the next idle
        // will try again from.
        if (!title) return
        await ctx.session.update({ sessionID, title })
      } catch (error) {
        if (!controller.signal.aborted) console.error("another title policy repair failed", error)
      } finally {
        repairing.delete(sessionID)
      }
    }

    void (async () => {
      while (!controller.signal.aborted) {
        try {
          for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
            try {
              if (event.type === "session.idle") {
                void repair(event.data.sessionID)
                continue
              }
              if (event.type !== "session.renamed") continue
              // Keep the event path for manual Type｜Topic renames. The title
              // hook above no longer needs this path for automatic titles.
              const refused = isRefusal(event.data.title)
              if (!refused && !parsePartialTitle(event.data.title)) continue
              const session = await ctx.session.get({ sessionID: event.data.sessionID })
              if (session.parentID || session.title !== event.data.title) continue
              const title = refused
                ? fallbackTitle(session.time.created, language)
                : finalizeTitle(event.data.title, session.time.created, language)
              if (title) await ctx.session.update({ sessionID: event.data.sessionID, title })
            } catch (error) {
              if (!controller.signal.aborted) console.error("another title policy event failed", error)
            }
          }
        } catch (error) {
          if (!controller.signal.aborted) console.error("another title policy event stream failed", error)
        }
        if (!controller.signal.aborted) await wait(1000, controller.signal)
      }
    })()

    return async () => {
      controller.abort()
      await titleHook.dispose()
      await registration.dispose()
    }
  },
} satisfies Plugin.Plugin

export default plugin
