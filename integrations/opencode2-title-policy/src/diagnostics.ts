import { appendFileSync, mkdirSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

// Keep a small, identifiable error record, never a serialized provider request
// (which can contain credentials, prompt text, and response bodies).
export function errorDetail(error: unknown): { type: string; message: string; status?: number } {
  const value = error && typeof error === "object" ? error as Record<string, unknown> : {}
  const redact = (text: string) => text
    .replace(/\b(?:sk|key)-[\w.-]+/giu, "[redacted]")
    .replace(/\bBearer\s+\S+/giu, "Bearer [redacted]")
    .replace(/((?:api[-_ ]?key|authorization|token|password)["']?\s*[=:]\s*["']?)[^"'\s,;]+/giu, "$1[redacted]")
    .replace(/https?:\/\/\S+/giu, "[url]")
    .slice(0, 500)
  return {
    type: redact(String(value.type ?? value._tag ?? value.name ?? "Error")),
    message: redact(typeof value.message === "string" ? value.message : typeof error === "string" ? error : "Title operation failed"),
    ...(typeof value.status === "number" ? { status: value.status } : {}),
  }
}

export function diagnostic(event: string, fields: Record<string, unknown>): void {
  const record = JSON.stringify({ at: new Date().toISOString(), plugin: "another.title-policy", event, ...fields })
  // The Promise plugin API has no server logger. Console output is not reliably
  // captured in opencode.log, so persist diagnostics at another's own XDG path.
  try {
    const dir = join(process.env.XDG_STATE_HOME || join(process.env.HOME || homedir(), ".local", "state"), "another")
    mkdirSync(dir, { recursive: true, mode: 0o700 })
    appendFileSync(join(dir, "opencode-title-policy.log"), record + "\n", { mode: 0o600 })
  } catch (error) {
    console.error("another.title-policy diagnostic write failed", errorDetail(error))
  }
  if (event.endsWith("failed") || event.endsWith("exhausted")) console.error(record)
}
