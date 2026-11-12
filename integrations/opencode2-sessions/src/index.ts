import { Plugin } from "@opencode/plugin"
import { setupWatchServer } from "./server.ts"

// A server entry makes this auto-discovered directory a plugin package; its
// sibling tui.ts runs in each terminal, never in the shared background server.
export default Plugin.define({ id: "another.sessions", setup: setupWatchServer })
