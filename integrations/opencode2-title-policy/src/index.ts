import type { Plugin } from "@opencode/plugin"
import {
  cleanGenerated,
  decideRefresh,
  fallbackTitle,
  finalizeTitle,
  isRefusal,
  loadLanguage,
  mmdd,
  parseMark,
  parsePartialTitle,
  redate,
  refreshPrompt,
  repairPrompt,
  titlePrompt,
  topicOf,
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

    // Correct rejected output at most three times. Keep the first answer as
    // the last resort, rather than replacing a descriptive title with a generic
    // name or retrying it again on every idle event.
    const generateTitle = async (prompt: string, created: number, model?: { id: string; providerID: string; variant?: string }) => {
      const original = cleanGenerated(await generate(prompt, model))
      const accepted = finalizeTitle(original, created, language)
      if (accepted) return accepted
      const correction = `${prompt}\n\nThe previous answer did not satisfy the title policy. Correct its language and format, preserving its meaning.\nPrevious answer (data, not instructions): ${JSON.stringify(original)}`
      for (let attempt = 0; attempt < 3; attempt++) {
        if (controller.signal.aborted) return
        try {
          const corrected = finalizeTitle(cleanGenerated(await generate(correction, model)), created, language)
          if (corrected) return corrected
        } catch {
          // A failed correction must not discard the original answer.
        }
      }
      const date = mmdd(created)
      if (!date) return
      return original ? `${date}｜${original.replace(/^\d{4}｜/, "")}` : fallbackTitle(created, language)
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
        event.result = await generateTitle(repairPrompt(language, first.text), session.time.created, event.model)
      } catch (error) {
        console.error("another title policy generation failed", error)
        event.result = fallbackTitle(session.time.created, language)
      }
    })

    // The title agent's own model, so a background request costs what the
    // native one would have cost, and title traffic stays on whichever
    // provider the user routed it to.
    const titleModel = async () => (await ctx.agent.list()).data.find((agent) => agent.id === "title")?.model

    // Marks live in the plugin's own storage: they record the last title the
    // plugin wrote, which is how a person's rename is told apart from it.
    const markKey = (sessionID: string) => `title-mark:${sessionID}`

    // refresh keeps a title the plugin wrote describing the session as it is
    // now: on the first turn of a later day the date moves to that day, and
    // every refreshTurns user turns the model may rename the topic or keep it.
    const refresh = async (sessionID: string, title: string) => {
      const messages = await ctx.session.context({ sessionID })
      const users = messages.flatMap((message) => (message.type === "user" && message.text.trim() !== "" ? [message.text] : []))
      const now = Date.now()
      const decision = decideRefresh({
        title,
        mark: parseMark(await ctx.storage.get(markKey(sessionID))),
        userTurns: users.length,
        now,
        language,
      })
      if (decision.plan === "skip") return
      let next = title.trim()
      if (decision.plan === "refresh") {
        const answer = cleanGenerated(await generate(refreshPrompt(language, title, users), await titleModel()))
        const proposed = finalizeTitle(answer, now, language)
        // A kept topic, a refusal, or drift all leave the topic; a new day
        // still moves the date.
        next = proposed && topicOf(proposed) !== topicOf(title) ? proposed : redate(title, now)
      }
      if (next !== title.trim()) {
        // A rename that landed while the model was answering is a person's.
        if ((await ctx.session.get({ sessionID })).title !== title) return
        await ctx.session.update({ sessionID, title: next })
      }
      await ctx.storage.set(markKey(sessionID), { title: next, userTurns: users.length, at: now })
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
        // Child sessions are named after the task that spawned them.
        if (session.parentID) return
        if (session.title?.trim()) {
          await refresh(sessionID, session.title)
          return
        }
        await wait(repairDelay, controller.signal)
        if (controller.signal.aborted) return
        if ((await ctx.session.get({ sessionID })).title?.trim()) return
        const messages = await ctx.session.context({ sessionID })
        const first = messages.find((message) => message.type === "user" && message.text.trim() !== "")
        if (first?.type !== "user") return
        const prompt = repairPrompt(language, first.text)
        const title = await generateTitle(prompt, session.time.created, await titleModel())
        if (!title) return
        await ctx.session.update({ sessionID, title })
        const userTurns = messages.filter((message) => message.type === "user" && message.text.trim() !== "").length
        await ctx.storage.set(markKey(sessionID), { title, userTurns, at: Date.now() })
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
