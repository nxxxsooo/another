import { posix, win32 } from "node:path"

// Cell-width/directory-tail rules ported from Annotate's reference picker.
const wide = /[\u1100-\u115F\u2E80-\uA4CF\uAC00-\uD7A3\uF900-\uFAFF\uFE10-\uFE19\uFE30-\uFE6F\uFF00-\uFF60\uFFE0-\uFFE6\u{1F300}-\u{1FAFF}\u{20000}-\u{3FFFD}]/u
const zeroWidth = /[\p{Mark}\u200D\uFE0F]/u
const characterWidth = (c: string) => zeroWidth.test(c) ? 0 : wide.test(c) ? 2 : 1
export const textWidth = (text: string) => Array.from(text).reduce((n, c) => n + characterWidth(c), 0)

export function truncate(text: string, width: number, left = false) {
  if (width <= 0) return ""
  if (textWidth(text) <= width) return text
  let result = "", used = 0
  for (const c of left ? Array.from(text).reverse() : Array.from(text)) {
    const next = characterWidth(c)
    if (used + next > width - 1) break
    result = left ? c + result : result + c
    used += next
  }
  return left ? `…${result}` : `${result}…`
}

export function directoryTail(directory: string, depth = 3) {
  const separator = directory.includes("\\") && !directory.includes("/") ? "\\" : "/"
  const parts = directory.split(/[\\/]/).filter(Boolean)
  if (parts.length <= depth && textWidth(directory) <= 28) return directory
  const kept = parts.slice(-depth)
  while (kept.length > 1 && textWidth(kept.join(separator)) > 28) {
    let widest = 0
    for (let i = 1; i < kept.length - 1; i++) if (textWidth(kept[i]) > textWidth(kept[widest])) widest = i
    kept.splice(widest, 1)
  }
  return truncate(kept.join(separator), 28, true)
}

const providers: Record<string, string> = {
  opencode: "OpenCode", "claude-code": "Claude", codex: "Codex", pi: "Pi", agy: "agy",
  qwen: "Qwen", cursor: "Cursor", commandcode: "Command", codem: "CodeM", hermes: "Hermes", doubao: "Doubao",
}
export const providerName = (id: string) => providers[id] ?? truncate(id, 8)
const pad = (text: string, width: number) => text + " ".repeat(Math.max(0, width - textWidth(text)))

export function scopeDirectory(directory: string, cwd: string, all: boolean) {
  if (!directory) return ""
  if (all) return directoryTail(directory)
  const paths = /^(?:[a-z]:[\\/]|\\\\)/i.test(cwd) ? win32 : posix
  const relative = paths.relative(cwd, directory)
  return relative ? directoryTail(paths.isAbsolute(relative) ? directory : relative) : ""
}

export function sessionOption(session: { id: string; provider: string; title: string }, directory: string, directoryColumn: boolean, busy: boolean, watched: boolean, terminalWidth: number) {
  const footer = pad(`[${providerName(session.provider)}]`, 10) + (directoryColumn ? "  " + pad(directory, 28) : "")
  // The SDK's footer is a separate muted, non-shrinking column. Do not use
  // description: it is appended inside the title's text node and can collide.
  const markers = `${busy ? "●" : " "} ${watched ? "盯" : "  "}  `
  const rowWidth = Math.min(61, Math.max(1, Math.min(116, terminalWidth - 2) - 12 - textWidth(footer)))
  return { title: markers + truncate(session.title || session.id, Math.max(1, rowWidth - textWidth(markers))), value: `${session.provider}:${session.id}`, footer }
}

export function commentLayout(columns: number, rows: number, lines = 1) {
  const width = Math.max(1, Math.min(88, columns - 2) - 4)
  const maxRows = Math.max(1, Math.min(12, rows - 14))
  return { width, fieldRows: Math.min(maxRows, Math.max(5, lines)), maxRows }
}

// virtualLineCount can be viewport-limited by OpenTUI. Grow from document
// content instead, including wrapped CJK cells, not the current viewport.
export function commentLineCount(text: string, width: number) {
  return text.split(/\r?\n/).reduce((n, line) => n + Math.max(1, Math.ceil(textWidth(line) / Math.max(1, width))), 0)
}

export function commentHeader(source: string, destination: string, provider: string, directory: string, width: number) {
  const tail = directoryTail(directory)
  const suffix = ` · ${providerName(provider)}${tail ? ` · ${tail}` : ""}`
  return {
    title: truncate(`Reference into ${destination || "New session"}`, width),
    source: truncate(`From ${truncate(source, Math.max(1, width - 5 - textWidth(suffix)))}${suffix}`, width),
  }
}
