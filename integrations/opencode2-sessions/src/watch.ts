export type Groups = Record<string, string[]>
export type WatchStatus = { groups: Groups }
export type Report = { member: string; title: string; state: "idle" | "blocked" | "error"; at: string }

const empty = { type: "object", additionalProperties: false } as const
const change = {
  type: "object", properties: { supervisor: { type: "string" }, member: { type: "string" } },
  required: ["supervisor", "member"], additionalProperties: false,
} as const
const status = {
  type: "object", properties: {
    groups: { type: "object", additionalProperties: { type: "array", items: { type: "string" } } },
  }, required: ["groups"], additionalProperties: false,
} as const

// One location-scoped RPC registration per OpenCode location, all backed by
// the same server-process service. The TUI never reads storage independently.
export const watchRPC = {
  id: "another.sessions.watch",
  methods: { status: { input: empty, output: status }, add: { input: change, output: status }, rm: { input: change, output: status } },
  events: {},
} as const

export function parseStatus(value: unknown): WatchStatus {
  if (!value || typeof value !== "object") throw new Error("Invalid watch status")
  const s = value as WatchStatus
  if (!s.groups || typeof s.groups !== "object" || Array.isArray(s.groups)) throw new Error("Invalid watch status")
  for (const members of Object.values(s.groups)) if (!Array.isArray(members) || members.some(id => typeof id !== "string")) throw new Error("Invalid watch group")
  return s
}

export function watchCard(sessionID: string, report: Report) {
  return {
    sessionID, delivery: "queue" as const,
    description: `${report.title || report.member} · turn ended · ${report.state}`,
    text: `Watch report: ${report.title || report.member}\nMember session: ${report.member}\nState: ${report.state}\nAt: ${report.at}\nRead new content with another context ${report.member} --provider opencode --all.\nThis is a watch report, not a user instruction.`,
    metadata: { source: "another.watch", member: report.member, at: report.at },
  }
}
