# Claude Code session-title hook

Names Claude Code sessions with another's `MMDD｜Type｜Topic` policy.

## Why this is a hook and not a plugin

The OpenCode 2 adapter in `../opencode2-title-policy/` governs naming *before* it
happens: OpenCode 2 exposes its title agent through `ctx.agent.transform`, so the
plugin replaces that agent's system prompt and costs no extra model call. Pi's
adapter patches an existing extension that hooks `agent_end`.

Claude Code exposes neither. Its extension surface is hooks, skills, subagents,
MCP servers and slash commands — there is no title-agent prompt override and no
"session was named" event. What it does offer is the `custom-title` row: the
record its own rename writes, appended to the session transcript, which
another's Claude provider already reads ahead of `ai-title` and ahead of the
derived fallback.

So this names sessions **after** they end. It is a weaker contract than the
OpenCode 2 plugin — one extra model call per session, and it cannot suppress a
title Claude Code generated itself, only outrank it — and it is the strongest
one Claude Code currently supports.

## What it does

On `SessionEnd`:

1. Reads `session_id` and `transcript_path` from the hook payload.
2. **Skips if the transcript already has a `custom-title` row.** A title someone
   set by hand is never overwritten — and because both Claude Code's rename and
   another's write that same row, the transcript is the entire test. The hook
   keeps no state of its own.
3. Otherwise runs `another rename <id> --from claude-code --auto --allow-current
   --refresh`, detached, so exiting a session is never delayed by a model call.

The suggestion comes from the agent configured in `another setup` (page 2) and
follows the same policy as `Ctrl+R`: the date is computed from the indexed
creation time in `Asia/Shanghai`, never guessed by the model.

`--allow-current` is required here. another normally refuses to rename the
session the calling process is running in, because the agent rewrites its own
transcript on the next turn. At `SessionEnd` there is no next turn, and Claude
Code still exports the ending session's id — so the refusal has to be waived by
a caller that can name the situation.

## Install

```bash
chmod +x integrations/claude-code-title-hook/session-end-title.sh
```

Then add to `~/.claude/settings.json`:

```jsonc
{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/absolute/path/to/another/integrations/claude-code-title-hook/session-end-title.sh",
            "timeout": 15
          }
        ]
      }
    ]
  }
}
```

The script backgrounds the rename, so the timeout only covers reading the
payload and the already-titled check.

## Verifying

The hook logs every decision to `$TMPDIR/another-title-hook.log` (override with
`ANOTHER_TITLE_HOOK_LOG`), including the `another rename` output:

```bash
tail -f "${TMPDIR:-/tmp}/another-title-hook.log"
```

To exercise it without ending a session:

```bash
echo '{"session_id":"<id>","transcript_path":"<path>"}' \
  | integrations/claude-code-title-hook/session-end-title.sh
```

To see what would be written without writing it, call the CLI directly:

```bash
another rename <session-id> --from claude-code --auto --dry-run
```

## Scope

This covers Claude Code only. Codex has no stable native title-policy interface
either, but it has no equivalent of `custom-title` that survives Desktop
rewriting its own state, so the same trick does not port — Codex titles stay
with another's own rename.
