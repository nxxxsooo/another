# Adaptive Migration Context Design

## Goal

Preserve every meaningful source message whenever the destination can resume it safely, while preventing oversized migrations from making the destination session immediately unusable.

“Meaningful” means user and assistant text. Provider control records, tool payloads, empty messages, and exact `[REDACTED]` placeholders are not conversation content and are never migrated as visible messages.

## User Interface

Migration accepts `--context auto|full|recent` on both the `migrate` subcommand and the root guided/direct migration flow.

- `auto` is the default. It writes the complete cleaned conversation when it is within the existing safe limits. Otherwise it writes the bounded recent projection and reports that the complete source remains searchable and exportable.
- `full` writes every cleaned user/assistant message without count or total-size limits. Individual messages are not shortened. Before confirmation and again in the result, Agenthop warns when the conversation exceeds the safe limits and may force Codex or another destination to compact or reject it.
- `recent` always uses the current bounded projection: at most 48 recent messages and 64,000 runes, with individual messages shortened to 16,000 runes.

Invalid values fail before any destination write. Existing commands without the flag retain safe behavior through the `auto` default.

## Migration Flow

The migration engine first creates one cleaned conversation shared by all modes. Cleaning keeps source order, timestamps, repeated turns, and exact user/assistant text while dropping unsupported roles, empty messages, Cursor redaction placeholders, and known provider control wrappers. Any provider-level duplicate artifact must be removed by that provider's parser, where its identity can be proven; the shared engine must not discard legitimate repeated conversation text.

The engine then selects a projection:

1. `full`: use the complete cleaned conversation.
2. `recent`: apply the bounded recent projection.
3. `auto`: use the complete cleaned conversation if it has no more than 48 messages, no more than 64,000 total runes, and no individual message over 16,000 runes; otherwise use the recent projection.

When reduction occurs, the destination receives the existing migration-handoff message explaining that older history remains in the source. Full migrations receive no synthetic handoff.

Deduplication metadata includes the context mode and projection format version. A prior recent migration must not suppress a requested full migration, and a changed default must not accidentally reuse an incompatible target.

## Full-History Access

Agenthop does not claim that an arbitrarily large transcript can fit simultaneously inside a model context window. The source session remains indexed, searchable, showable, and exportable. Reduction warnings include the source provider and ID so the user or resumed agent can retrieve exact older details with `agenthop search`, `agenthop show`, or `agenthop export`.

No second archive is created because the original provider data plus Agenthop’s index already provide that capability without duplicating sensitive transcript data.

## Error Handling and Safety

- Validation occurs before writing.
- `full` is explicit consent to context-overflow risk, not a promise that the destination model will accept every token at once.
- Provider-native write, verification, rollback, and cleanup behavior remain unchanged.
- Verification compares the destination against the selected cleaned projection, not the unfiltered raw provider stream.
- Dry-run output reports selected mode, original count, cleaned count, projected count, and whether the safe limits were exceeded.

## Tests

Small focused tests cover:

- `auto` retaining a small conversation in full.
- `auto` reducing an oversized conversation.
- `full` retaining an oversized conversation without shortening.
- `recent` always producing the bounded projection.
- invalid mode rejection before writing.
- `[REDACTED]`, empty, system, and tool records never reaching any mode.
- deduplication distinguishing full and recent projections.
- CLI flags on both migration entry points.

The complete Go test suite, race suite, vet, shell syntax checks, build, and a real Cursor-to-Codex resume smoke test must pass before release.
