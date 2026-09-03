#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/another"
[[ -x "$BIN" ]] || { echo "build first: make build"; exit 1; }
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

echo "== providers =="
"$BIN" providers | sed -n '1,8p'

echo "== index status =="
"$BIN" index status

echo "== list (cached) =="
"$BIN" list --provider claude-code --limit 3

echo "== doctor =="
"$BIN" providers doctor | sed -n '1,8p'

echo "== import dry-run =="
SESSION_ID=$("$BIN" list --provider claude-code --limit 1 2>/dev/null | awk 'NR==2{print $1}')
if [[ -n "$SESSION_ID" ]] && "$BIN" export "$SESSION_ID" --provider claude-code -o "$TMP" 2>/dev/null; then
	"$BIN" import "$TMP" --to codex --dry-run -y
fi

echo "OK"
