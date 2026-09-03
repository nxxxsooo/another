# Provider storage reference

## Pi (`pi`)

- Path: `~/.pi/agent/sessions/<encoded-cwd>/<timestamp>_<uuid>.jsonl`
- Encoding: `/home/user/proj` → `--home-user-proj--`
- Format: session header followed by an event parent chain (`version: 3`)
- Env: `PI_AGENT_DIR`
- Resume: `pi --session <absolute-session-file>`
- Migration marker: a `custom` event with `customType: "agenthop-migration"`
- Migrated assistant events must be complete Pi assistant records: explicit `api`, `provider`, `model`, and `stopReason`, plus a zero-valued `usage` object when source billing data is unavailable. Omitting `usage` causes Pi's footer aggregation to fail on affected releases.

## Claude Code (`claude-code`)

- Path: `~/.claude/projects/<encoded-path>/<session-uuid>.jsonl`
- Encoding: `/home/user/proj` → `-home-user-proj`
- Format: JSONL with `type: user|assistant`, `message.role`, `parentUuid` chain
- Env: `CLAUDE_CONFIG_DIR`

## Codex (`codex`)

- Path: `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`
- Format: `session_meta`, durable migration marker, and native v2 turn records
- Native thread registration: `~/.codex/state_5.sqlite` when present
- Env: `CODEX_HOME`

## Cursor (`cursor`)

- Primary: `~/.cursor/chats/<workspace-hash>/<session-id>/store.db`
- CLI metadata: sibling `meta.json` supplies workspace, title, timestamps, and subagent state
- Fallback: `~/.cursor/projects/<encoded>/agent-transcripts/<id>/<id>.jsonl`
- Agenthop reads the active root graph, uses bounded recent blobs for previews, excludes unreachable historical blobs, and writes the database, CLI metadata, and transcript representations.

## OpenCode (`opencode`)

- Path: `~/.local/share/opencode/opencode.db`
- Tables: `session`, `message`, `part`

## CommandCode (`commandcode`)

- Path: `~/.commandcode/projects/<encoded>/<session>.jsonl`
- Claude-like JSONL with `sessionId`, `parentId`, content blocks
- Env: `COMMANDCODE_HOME`

## Hermes (`hermes`)

- Path: `~/.hermes/state.db`
- Tables: `sessions`, `messages`
- Env: `HERMES_HOME`
- Resume: `hermes --resume <session-id>`
