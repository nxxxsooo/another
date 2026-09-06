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
- Topic must be concrete and distinct from the project name: at most 16 Chinese characters or 8 English words.
- Preserve exact technical terms, numbers, and filenames.
- Never use tools, markdown, quotes, explanations, or trailing punctuation.
- If type or topic cannot be determined, output exactly KEEP.`
}

export function parsePartialTitle(title: string): { language: "en" | "zh"; type: string; topic: string } | undefined {
  const parts = title.trim().split("｜")
  if (parts.length !== 2) return
  const [type, topic] = parts.map((part) => part.trim())
  if (!type || !topic) return
  const language = categories.zh.includes(type as never) ? "zh" : categories.en.includes(type as never) ? "en" : undefined
  if (!language) return
  if (language === "zh" && [...topic].length > 16) return
  if (language === "en" && topic.split(/\s+/u).length > 8) return
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
