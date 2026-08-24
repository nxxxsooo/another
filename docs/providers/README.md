# Provider storage reference

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
