#!/usr/bin/env bash
# another — Claude Code SessionEnd title hook.
#
# Claude Code exposes no title-agent hook, so this cannot govern naming the way
# the OpenCode 2 plugin does. It names the session after it ends instead, which
# is the only contract Claude Code actually offers: a `custom-title` row appended
# to the transcript, which is the same row Claude Code writes for its own rename
# and which another's Claude provider reads ahead of everything else.
#
# Reads the SessionEnd payload on stdin: {session_id, transcript_path, cwd, reason}.

set -uo pipefail

LOG="${ANOTHER_TITLE_HOOK_LOG:-${TMPDIR:-/tmp}/another-title-hook.log}"
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG" 2>/dev/null; }

payload=$(cat)

# One interpreter pass, newline-delimited: a transcript path may contain spaces.
meta=$(printf '%s' "$payload" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
print(d.get("session_id", ""))
print(d.get("transcript_path", ""))
' 2>/dev/null) || { log "skip: unreadable payload"; exit 0; }

session_id=$(printf '%s\n' "$meta" | sed -n 1p)
transcript=$(printf '%s\n' "$meta" | sed -n 2p)

[ -n "$session_id" ] || { log "skip: no session_id"; exit 0; }

# Never overwrite a title someone set by hand. Claude Code's own rename and
# another's both record one as a custom-title row, so the transcript is the
# whole test — no state of our own to keep in sync.
if [ -n "$transcript" ] && [ -f "$transcript" ] && grep -q '"type":"custom-title"' "$transcript"; then
  log "skip $session_id: already has a custom title"
  exit 0
fi

another_bin=$(command -v another 2>/dev/null) || another_bin=""
if [ -z "$another_bin" ]; then
  for candidate in "$HOME/.local/bin/another" "$(go env GOPATH 2>/dev/null)/bin/another"; do
    if [ -x "$candidate" ]; then another_bin="$candidate"; break; fi
  done
fi
[ -n "$another_bin" ] || { log "skip $session_id: another not on PATH"; exit 0; }

# Detached on purpose: the suggestion is a model call, and a session should not
# take seconds to exit because it is being named. Failures land in the log.
#
# --allow-current because SessionEnd is exactly the case the running-session
# refusal is wrong about: Claude Code exports this session's id, and the session
# is already over.
nohup "$another_bin" rename "$session_id" \
  --from claude-code --auto --allow-current --refresh \
  >>"$LOG" 2>&1 &

log "queued $session_id"
exit 0
