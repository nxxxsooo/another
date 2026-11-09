# Native Doubao completion events

```go
options, err := doubaoevents.DefaultOptions()
// Handle err. Override StatePath for the owning service's private state.
options.Ready = func() { /* record listener readiness */ }
watcher, err := doubaoevents.New(options)
// Handle err.
err = watcher.Watch(ctx, func(ctx context.Context, event doubaoevents.Event) error {
    // Target event.ConversationID; do not scan every session.
    return handleCompletion(ctx, event)
})
```

- Darwin uses blocking kqueue vnode notifications and a cancellation pipe, not
  timers. Other platforms return `ErrUnsupported`. No network operations occur.
- Desktop watches `saman_frontend_*.log` for the observed logifier envelope's
  successful `on_message_session_end` root Work records. CLI watches `doubao-*.log`
  for successful main-agent `im_task_finished` records. Unknown schemas, errors,
  cancellations and subagents are ignored. The desktop turn key is the received
  message ID (stable across duplicate/resume reports); CLI uses the task session ID.
- Initial files start at EOF, including discarding an unfinished baseline line.
  New files start at zero. Missing directories are discovered through ancestor
  notifications; registration precedes enumeration. `Ready` runs after startup
  registration/baselining. Its hook must not block. Only complete appended lines
  are parsed; lines over 256 KiB are discarded through the next newline.
- Callbacks execute serially and must honor cancellation. Ingestion continues
  while a callback runs. Up to 8,192 pending distinct turns are queued; overflow,
  I/O errors and ledger errors terminate visibly. Callback errors terminate by
  default; optional `Options.OnError func(Event, error)` reports them and continues
  queued events without restart or timed retries. A failed turn is not marked
  successful and remains eligible for later native redelivery. The hook must not
  block; the caller owns sanitized error reporting.
- The private JSON ledger stores only 4,096 SHA-256 identifier keys and is
  atomically replaced after callback success. Use one watcher per ledger. There
  is no persistent cursor or replay of events missed while stopped. A crash after
  callback success but before persistence can redeliver; callbacks should be
  idempotent. On restart, failed events are eligible if native logs emit them again.
- Rotation drains the old descriptor before retirement and preserves recent inode
  cursors. Truncation resets the cursor; a boundary fingerprint detects ordinary
  truncate-and-regrow. Files created and deleted entirely before the directory
  notification is handled cannot be recovered by vnode watching. Rewriting the
  exact same bytes and restoring size before notification is likewise not
  distinguishable from append-only activity. Native logs are expected to append.
- Each extant eligible log requires a descriptor. LaunchAgent wiring should set
  `SoftResourceLimits.NumberOfFiles` (16,384 recommended for the observed several
  thousand CLI logs); descriptor exhaustion is a visible startup failure, not a
  partially-ready watcher. Registration is capped at 12,000 files/directories
  with an explicit error. All descriptors close on exit.

Tests use native temporary directories and synthetic copies of observed log
envelopes. They never write native sessions or start a user daemon.
