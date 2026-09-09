import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent"
import { spawn } from "node:child_process"
import { basename } from "node:path"

// another names a Pi session by asking another itself, not by talking to a
// model here. The binary already owns the title agent, the MMDD｜Type｜Topic
// policy, the language setting, and the validation that rejects a refusal —
// reimplementing any of that in an extension would create a second policy that
// drifts from the one every other agent gets.
//
// Pi is the trigger, another is the naming. That split is what keeps this file
// small enough to read.

const POLICY = /^\d{4}｜[^｜]+｜.+$/u

// Pi writes a session as <timestamp>_<uuid>.jsonl, and the uuid is the id the
// rest of another indexes it under. Reading it from the path avoids asking Pi
// for an id that its extension API does not expose.
function sessionID(file: string | undefined): string | undefined {
  if (!file) return
  const name = basename(file).replace(/\.jsonl$/u, "")
  const id = name.slice(name.indexOf("_") + 1)
  return /^[0-9a-f-]{36}$/iu.test(id) ? id : undefined
}

export default function register(pi: ExtensionAPI): void {
  // Detached on purpose: naming is a model call, and a turn should not end
  // slowly because a title is being written. Failures land in another's own
  // log rather than interrupting the session.
  const rename = (ctx: ExtensionContext): void => {
    const id = sessionID(ctx.sessionManager.getSessionFile())
    if (!id) return
    // A session that already carries a policy title is left alone: the user
    // may have set it by hand, and re-naming it would overwrite that.
    const current = pi.getSessionName()
    if (current && POLICY.test(current)) return

    const child = spawn(
      "another",
      ["rename", id, "--from", "pi", "--auto", "--allow-current", "--refresh"],
      { detached: true, stdio: "ignore" },
    )
    child.on("error", () => {
      // another is not on PATH. That is a configuration state, not an error
      // worth interrupting a session for.
    })
    child.unref()
  }

  // agent_end is the moment the turn's content is final. agent_settled repeats
  // after compaction and retries, which is where a session that was empty at
  // agent_end finally has something to name.
  pi.on("agent_end", (_event, ctx) => {
    rename(ctx as ExtensionContext)
  })
  pi.on("agent_settled", (_event, ctx) => {
    rename(ctx as ExtensionContext)
  })
}
