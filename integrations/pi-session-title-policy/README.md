# Pi session title-policy patch

This patch extends the locally installed `@oipsanthony/pi-session-title` with
another's shared `auto | en | zh` title policy.

- Reads `title_policy.language` from `~/.config/another/config.json`.
- Uses only Pi's authoritative JSONL `SessionHeader.timestamp` for `MMDD`, in
  `Asia/Shanghai`.
- Produces and validates `MMDD｜Type｜Topic`; invalid output keeps the old title.
- Keeps the existing stale-context crash fix.
- Uses `agent_end` as the primary trigger on Pi 0.84.4, with `agent_settled` as
  the retry/compaction fallback.

The patch is based on the local 0.3.3 installation (whose title source matched
upstream 0.3.5 when inspected). Apply it from the extension root:

```bash
patch -p1 < /path/to/another/integrations/pi-session-title-policy/pi-session-title.patch
```

Before replacing or reapplying, back up the extension. Type-check the three
extension files, run a deterministic policy fixture, then verify `/session-title`
in a real saved Pi session. Automatic first-title generation depends on the
configured title model; a slow model can leave the state pending even though
the trigger and policy path are active.
