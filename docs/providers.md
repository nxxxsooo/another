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
- **Qwen Code.** A session is a transcript plus the sidecars that belong to it and the backups of every file it edited, so the same reasoning applies and the delete is final.
- **CodeM.** A session is a transcript plus the working directory CodeM keeps beside it for that session's scratchpad and tool results, so the same reasoning applies and the delete is final.

The offer lasts only as long as the list. `another` keeps no trash directory of its own, `Esc` gives it up on the spot, and a path the agent has since written to again is never overwritten.

## Codex

**Three title stores.** Codex keeps a session's name in the CLI's thread store, the legacy `session_index.jsonl`, and the Electron state Codex Desktop's sidebar actually reads. Rename writes all three. Desktop rewrites its whole state from memory while it runs, so writing underneath it would lose either the rename or whatever Desktop has not flushed; that case is reported instead of forced. The CLI store is already renamed, and the interface says the sidebar catches up when Desktop restarts rather than calling the rename a failure.

**Subagent threads.** The forks Codex Desktop spawns from a parent thread record their answers only as `event_msg`, never as the mirrored `response_item`, and their only user-role record is injected plugin transport. Those sessions open and migrate like any other. Having no prompt of their own, they are named after the subagent identity Codex recorded, such as `api_definitions · Wegener the 9th`.

**Message counts.** Codex writes each turn to a rollout up to twice, and the message count in the list used to count both halves. That number now matches what the session actually opens with, so a Codex session's count can drop after this upgrade. The index re-summarizes Codex once on the first run after the update; nothing has to be rebuilt by hand.

## Qwen Code

**Archive is a move.** Qwen Code has no archived flag in a transcript; it keeps archived sessions in an `archive` directory inside the project's own `chats` directory, and its session list reads the two directories as the two states. `another` archives by making that move, sidecars included, so an archived session leaves both lists together and unarchiving brings back the same file rather than a copy.

**Delete clears the whole session.** The transcript in whichever state holds it, the worktree, pull request and prompt-ledger sidecars, the runtime sidecar `qwen sessions ps` reads, the file backups the session's own edits produced, and its entry in the project's pin-and-group store — the same set Qwen Code's own delete removes.

**A running session is refused.** Qwen Code marks a live session with a runtime sidecar beside the transcript and refuses to archive one; `another` reads the same sidecar and refuses both archive and delete, because moving the file out from under a process that is appending to it loses the turn in flight. A sidecar written on another machine counts as live, since a pid here says nothing about a process there.

## CodeM

**A session belongs to a directory, and the directory is a hash.** CodeM files a transcript under the first 16 hex characters of the sha256 of the directory the session started in, and `codem --resume <id>` only looks inside the hash of the directory it is run from. The resume line `another` prints therefore begins with `cd`: run somewhere else, that command fails outright rather than opening the wrong session. It is also why the directory a session belongs to is read out of the transcript's own header rather than off its path — the hash does not go backwards.

**Relocate is absent for the same reason.** Moving a session to another directory would mean moving the file to a different hash and rewriting its header. CodeM has no operation that does that, so `another` does not invent one.

**Rename and archive are native.** Current CodeM transcripts persist `/rename` as a `session_renamed` record, and CodeM's archive operation moves the transcript into the project's `archived/` directory. Another writes and reads that same record and mirrors that reversible move; it does not keep private aliases or archive flags.

If a recorded working directory has moved or become a symlink whose physical path hashes differently, CodeM's default lookup can no longer find the old transcript from that directory. Another now gives CodeM a narrow `LINCO_SESSIONS_ROOT` bridge to that exact original project-hash directory. The transcript and scratchpad stay in place, CodeM remains the process reading and extending them, and a same-ID session under another project hash is never exposed.

**Injected turns do not travel.** CodeM adds loaded skills and reminders to a session as whole user messages wrapped in `<system-reminder>`. Those are the CLI talking to its own model, not the person, and they are left behind when the conversation is migrated.

**The Feishu client's sessions are ordinary sessions.** A conversation started from Feishu is written to the same store with an id like `sess_feishu_p2p_…`, so it lists, opens, and migrates like any other. The loose `sess_*.json` files at the top of the store are that client's own records, not transcripts, and are not indexed.

## Child sessions

Child sessions stay out of the list because their parent leads back to them. When the parent is no longer indexed that reasoning fails, so such a child is listed directly; otherwise only knowing its ID would find it, which is indistinguishable from losing it. It becomes a child again as soon as its parent is indexed. A child thread that records no parent at all stays hidden: Codex's guardian threads are written that way, and they are machine assessments of a requested action rather than anyone's conversation.

## Missing directories

A session whose directory no longer exists is marked as such. After a workspace is reorganized those sessions still browse and migrate, but their resume command would land on a path that is gone.
