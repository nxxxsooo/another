# Pi title-policy extension

another installs this extension into Pi's extensions directory so a Pi session
gets the same `MMDD｜Type｜Topic` name every other agent's sessions get.

It does not generate the title itself. On `agent_settled` it calls
`another rename --auto`, which already owns the configured title agent, the
language setting, the policy, and the validation that refuses a model's
refusal. A second implementation here would be a second policy, and it would
drift.

`agent_settled` is the only trigger on purpose. Pi emits it once per prompt,
after every automatic retry, compaction, and queued continuation has finished;
`agent_end` fires earlier and once per retry, so naming on both spends two
title-model calls on an ordinary turn and races two renames onto one session.

A session whose name already matches the policy is left alone, so a title set
by hand survives.

## Relationship to `@oipsanthony/pi-session-title`

Anthony's extension (MIT, <https://github.com/OiAnthony/pi-extensions>) does
much more: model roles, terminal-title templates, Herdr synchronization, and
its own completion pipeline. another previously carried a patch against it,
which could not be installed automatically: patching a package another does
not own means an upstream release silently reverts or conflicts with the patch,
and another cannot tell that apart from an edit the user made.

This extension is first-party and small instead, so another can own the
installed copy, write a manifest beside it, and report drift honestly. Run
either one, not both: two extensions naming the same session will race.
