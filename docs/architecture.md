# Architecture

```
cmd/agenthop          CLI entry
internal/cli          cobra commands
internal/tui          bubbletea UI
internal/index          SQLite session index (~/.cache/agenthop/index.db)
internal/registry       provider registry
internal/provider       Provider interface
internal/providers/*    per-agent read/write
internal/model          UnifiedConversation
internal/migrate        migration engine + dedup
```

## Data flow

1. **Discover** — each provider scans its storage paths and returns lightweight `Summary` records.
2. **Index** — physical sources are reconciled transactionally into logical sessions using nanosecond mtime, size, and provider priority.
3. **Search** — changed canonical sessions are loaded once into SQLite FTS5; only normalized user/assistant text is stored.
4. **Load** — full `Conversation` parsed from JSONL or SQLite on demand.
5. **Write** — target provider serializes the conversation atomically to its native format.
6. **Verify** — the exact target is reloaded and its normalized content compared; failed writes are rolled back by exact ID/path.
7. **Dedup** — a source-provider/ID/content snapshot digest is stored in both migration metadata and the index.

## Index schema

| Column | Purpose |
|--------|---------|
| `sessions` | canonical logical session, including root/subagent kind |
| `session_sources` | every physical representation and its source stamp/priority |
| `session_fts` | searchable title plus normalized user/assistant body |
| `content_index` | canonical fingerprint and ready/pending/error state |
| `migration_dedup` | verified target for an exact source snapshot |

## Extension points

- New provider: implement `provider.Provider`, register in `internal/registry/registry.go`
- Custom paths: env vars per provider (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, etc.)
- Future: `~/.config/agenthop/config.yaml` for path overrides
