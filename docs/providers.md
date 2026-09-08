# Provider notes

中文版：[`providers.zh.md`](providers.zh.md)

The capability table lives in the [README](../README.en.md#agents). This page explains the cells that surprise people, and the per-agent behavior behind them.

## What a dash means

A dash means that agent has no verified native contract for the operation. Rename, archive, relocate, and delete change the corresponding agent's native state rather than an another-only marker; `another` shows only operations the selected agent actually supports and does not keep private state that disappears on refresh.

Antigravity's archive is one of those dashes: its storage has no archived state at all — the annotation file carries a title and nothing else, and the legacy summaries table has no column for it — so filling that cell would mean `another` remembering it alone, and the cell stays a dash.

## Whether a delete can be taken back

It depends on who owns the session, and the confirmation says which case you are in before you commit.

- **Claude Code and Pi.** The session is one file, so `another` holds those exact bytes and `u` writes them back: same session ID, same path, same modification time, and the agent resumes it as if it had never gone.
- **OpenCode 2.** The server owns the deletion. Pushing the conversation back through its API would create a new session with a new ID, which is a copy rather than an undo, so there is none and the modal says so.
- **Antigravity.** A session is not one file but a whole brain directory beside a trajectory database, often tens of megabytes, and `another` will not hold that many bytes in memory waiting for second thoughts, so there is no undo there either.

The offer lasts only as long as the list. `another` keeps no trash directory of its own, `Esc` gives it up on the spot, and a path the agent has since written to again is never overwritten.

## Codex

**Three title stores.** Codex keeps a session's name in the CLI's thread store, the legacy `session_index.jsonl`, and the Electron state Codex Desktop's sidebar actually reads. Rename writes all three. Desktop rewrites its whole state from memory while it runs, so writing underneath it would lose either the rename or whatever Desktop has not flushed; that case is reported instead of forced. The CLI store is already renamed, and the interface says the sidebar catches up when Desktop restarts rather than calling the rename a failure.

**Subagent threads.** The forks Codex Desktop spawns from a parent thread record their answers only as `event_msg`, never as the mirrored `response_item`, and their only user-role record is injected plugin transport. Those sessions open and migrate like any other. Having no prompt of their own, they are named after the subagent identity Codex recorded, such as `api_definitions · Wegener the 9th`.

**Message counts.** Codex writes each turn to a rollout up to twice, and the message count in the list used to count both halves. That number now matches what the session actually opens with, so a Codex session's count can drop after this upgrade. The index re-summarizes Codex once on the first run after the update; nothing has to be rebuilt by hand.

## Child sessions

Child sessions stay out of the list because their parent leads back to them. When the parent is no longer indexed that reasoning fails, so such a child is listed directly; otherwise only knowing its ID would find it, which is indistinguishable from losing it. It becomes a child again as soon as its parent is indexed. A child thread that records no parent at all stays hidden: Codex's guardian threads are written that way, and they are machine assessments of a requested action rather than anyone's conversation.

## Missing directories

A session whose directory no longer exists is marked as such. After a workspace is reorganized those sessions still browse and migrate, but their resume command would land on a path that is gone.
