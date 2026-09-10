import type { Plugin } from "@opencode-ai/plugin"
import {
  fallbackTitle,
  finalizeTitle,
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
        await ctx.session.rename({ sessionID, title })
      } catch (error) {
        if (!controller.signal.aborted) console.error("another title policy repair failed", error)
      } finally {
        repairing.delete(sessionID)
      }
    }

    void (async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          if (event.type === "session.idle") {
            void repair(event.data.sessionID)
            continue
          }
          if (event.type !== "session.renamed") continue
          // The title the model just produced is the whole trigger. Nothing is
          // remembered between events on purpose: an earlier version kept the
          // pending sessions in memory and lost the date whenever the plugin
          // reloaded, the server restarted, or the session belonged to a
          // directory whose instance was not the one watching.
          // A refusal is a defect this policy can produce, never a name the
          // user should read: OpenCode 2 writes the title agent's answer to the
          // session as it stands, so a model that declines names the session
          // after its own refusal.
          const refused = isRefusal(event.data.title)
          if (!refused && !parsePartialTitle(event.data.title)) continue
          const session = await ctx.session.get({ sessionID: event.data.sessionID })
          // Child sessions are named after the task that spawned them, which
          // is more useful to their parent than a dated policy title.
          if (session.parentID) continue
          const title = refused
            ? fallbackTitle(session.time.created, language)
            : finalizeTitle(event.data.title, session.time.created, language)
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
