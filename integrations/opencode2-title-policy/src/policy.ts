import { readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

export type TitleLanguage = "auto" | "en" | "zh"

export const categories = {
  en: ["Feature", "Design", "Fix", "Optimize", "Release", "Explore", "Docs", "Research"],
  zh: ["功能", "设计", "修复", "优化", "发布", "探索", "文档", "研究"],
} as const

export function loadLanguage(env: NodeJS.ProcessEnv = process.env): TitleLanguage {
  const root = env.XDG_CONFIG_HOME || join(env.HOME || homedir(), ".config")
  try {
    const value = JSON.parse(readFileSync(join(root, "another", "config.json"), "utf8"))?.title_policy?.language
    return value === "en" || value === "zh" ? value : "auto"
  } catch {
    return "auto"
  }
}

export function titlePrompt(language: TitleLanguage): string {
  const languageRule = language === "zh"
    ? `Write both fields in Chinese. Type must be one of: ${categories.zh.join(" ")}.`
    : language === "en"
      ? `Write both fields in English. Type must be one of: ${categories.en.join(" ")}.`
      : `Use Chinese when the first user message contains any Han character; otherwise use English. Chinese types: ${categories.zh.join(" ")}. English types: ${categories.en.join(" ")}.`
  return `You are a title generator. Output ONLY one line in the exact form Type｜Topic (without a date).

Rules:
- ${languageRule}
- Use exactly U+FF5C ｜ as the separator.
- Topic must be concrete and distinct from the project name: at most 16 Chinese characters or 8 English words, counting each English word, filename, or identifier as one character.
- Preserve exact technical terms, numbers, and filenames.
- Never use tools, markdown, quotes, explanations, or trailing punctuation.
- Always output a title. An empty, minimal, or conversational session is still named: title the intent instead of refusing, such as ${language === "zh" ? "探索｜打招呼" : "Explore｜Greeting"}.
- Never output a refusal, a status word, or any text outside the Type｜Topic form.`
}

// OpenCode 2 has no sentinel for "no title": whatever the title agent returns
// becomes the session name verbatim. An earlier version of this prompt offered
// KEEP as an escape hatch, which OpenCode 2 never interpreted — every session
// the model would not summarize, an empty one or a greeting, ended up literally
// named KEEP. The prompt now forbids refusing; a refusal that arrives anyway is
// replaced rather than shown.
const refusals = new Set(["KEEP", "NONE", "NULL", "N/A", "NA", "UNKNOWN", "UNTITLED", "NO TITLE"])

export function isRefusal(title: string): boolean {
  return refusals.has(title.trim().toUpperCase())
}

// The fallback names what is actually known: a session that exists and was not
// summarizable. It carries the same date and shape as any policy title, so it
// sorts with them and is replaced by a manual rename like any other.
export function fallbackTitle(created: number, configured: TitleLanguage): string | undefined {
  return finalizeTitle(configured === "zh" ? "探索｜未命名会话" : "Explore｜Untitled session", created, configured)
}

// topicWeight measures a topic the way the caps are meant: one unit per CJK
// character and one per Latin or numeric run. Counting raw characters treated
// every technical term as if it were a sentence — `功能｜Meeting Loop会议待办总结`
// scored 18 against a cap of 16 and was refused, which left the session
// wearing the undated half of the policy. Real titles name tools, files, and
// identifiers; only genuine rambling should exceed the cap.
export function topicWeight(topic: string): number {
  const runs = topic.match(/[A-Za-z0-9][A-Za-z0-9._+#/-]*/gu)?.length ?? 0
  const rest = topic.replace(/[A-Za-z0-9._+#/-]+/gu, "")
  return runs + [...rest].filter((character) => !/[\s\p{P}\p{S}]/u.test(character)).length
}

export function parsePartialTitle(title: string): { language: "en" | "zh"; type: string; topic: string } | undefined {
  const parts = title.trim().split("｜")
  if (parts.length !== 2) return
  const [type, topic] = parts.map((part) => part.trim())
  if (!type || !topic) return
  const language = categories.zh.includes(type as never) ? "zh" : categories.en.includes(type as never) ? "en" : undefined
  if (!language) return
  if (topicWeight(topic) > (language === "zh" ? 16 : 8)) return
  return { language, type, topic }
}

export function mmdd(created: number): string | undefined {
  if (!Number.isFinite(created) || created <= 0) return
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: "Asia/Shanghai",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(new Date(created))
  const month = parts.find((part) => part.type === "month")?.value
  const day = parts.find((part) => part.type === "day")?.value
  return month && day ? month + day : undefined
}

export function finalizeTitle(partial: string, created: number, configured: TitleLanguage): string | undefined {
  const parsed = parsePartialTitle(partial)
  const date = mmdd(created)
  if (!parsed || !date) return
  if (configured !== "auto" && parsed.language !== configured) return
  return `${date}｜${parsed.type}｜${parsed.topic}`
}

// The repair below asks for a title outside OpenCode 2's own title request, so
// the first user message has to travel in the prompt. It is cut short on
// purpose: a session is recognizable from how it opens, and this request is
// meant to cost about what the one it replaces would have cost.
export function repairPrompt(language: TitleLanguage, firstUserMessage: string): string {
  return `${titlePrompt(language)}

Name the session that begins with this message:

${firstUserMessage.trim().slice(0, 2000)}`
}

// A generated answer is not a rename event. OpenCode 2 writes the title agent's
// reply to the session verbatim, so the native path only ever sees one line;
// a model answering this prompt directly still returns a code fence, a quoted
// title, or an empty line first.
export function cleanGenerated(text: string): string {
  const line = text
    .split("\n")
    .map((part) => part.trim())
    .find((part) => part !== "" && !part.startsWith("```"))
  return (line ?? "").replace(/^["'`“”‘’「」]+|["'`“”‘’「」]+$/gu, "").trim()
}

// repairTitle judges a generated answer the way the native path judges a
// rename: a refusal is replaced, and anything that is not a policy title is
// declined rather than written, which leaves the next idle free to try again.
export function repairTitle(generated: string, created: number, configured: TitleLanguage): string | undefined {
  const text = cleanGenerated(generated)
  if (!text) return
  return isRefusal(text) ? fallbackTitle(created, configured) : finalizeTitle(text, created, configured)
}
