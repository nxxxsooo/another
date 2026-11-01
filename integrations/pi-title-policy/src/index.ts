import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent"
import { spawn } from "node:child_process"

// another names a Pi session by asking another itself, not by talking to a
// model here. The binary already owns the title agent, the MMDD｜Type｜Topic
// policy, the language setting, and the validation that rejects a refusal —
// reimplementing any of that in an extension would create a second policy that
// drifts from the one every other agent gets.
//
// Pi is the trigger, another is the naming. That split is what keeps this file
// small enough to read.

export default function register(pi: ExtensionAPI): void {
  // Detached on purpose: naming is a model call, and a turn should not end
  // slowly because a title is being written. Failures land in another's own
  // log rather than interrupting the session.
  const rename = (ctx: ExtensionContext): void => {
    // An ephemeral session (`pi --no-session`) is never written to disk, so
    // there is nothing for another to find and rename.
    if (!ctx.sessionManager.getSessionFile()) return
    const id = ctx.sessionManager.getSessionId()
    if (!id) return

    // --if-stale lets another decide whether the title is due: an untitled
    // session is named, a title another wrote is refreshed on a new day or
    // after a few more turns, and a name set with /name is kept. Deciding that
    // here would duplicate a policy another owns, and pi.getSessionName()
    // reports the name Pi holds in memory, which never learns about the one
    // another wrote into the session file. No --replace-native: Pi does not
    // name sessions itself, so any other name came from a person.
    const child = spawn(
      "another",
      ["rename", id, "--from", "pi", "--auto", "--if-stale", "--allow-current", "--refresh"],
      { detached: true, stdio: "ignore" },
    )
    child.on("error", () => {
      // another is not on PATH. That is a configuration state, not an error
      // worth interrupting a session for.
    })
    child.unref()
  }

  // agent_settled, and only agent_settled: Pi emits it from the `finally` of
  // the prompt run, once, after every automatic retry, compaction, and queued
  // continuation has finished. agent_end fires earlier and fires again for
  // each of those, so listening to both spends a title-model call twice on an
  // ordinary turn and races two renames onto the same session.
  pi.on("agent_settled", (_event, ctx) => {
    rename(ctx as ExtensionContext)
  })
}
