# OpenCode `/ref`: references and session watching

Status: shipped in v0.28.0; see [the release verification receipt](releases/v0.28.0.md).
This is the current design, reconciling the maintainer's 2026-11-12 reference plan
with the approved unified reference/watch menu. Publication and installation
evidence belong to the release receipt rather than this design contract.

## Command and picker

- `/ref` lists the current project's sessions across enabled providers using
  `another list --json --cached`. Subagents remain hidden by the CLI's default.
- `/ref <keywords>` uses full-text `another search --json --cached --no-wait -- <keywords>`, including
  matches in conversation text rather than only titles. The parser reads the
  search response's `results[].session` summaries; the CLI's search limit applies.
- `/ref --all [keywords]` starts across all projects. Only leading `--all` is a
  scope flag; later words remain part of the query. Leading `--tui` is rejected.
- The current OpenCode session is excluded before filtering and pagination.
  A different provider's session with the same ID is still eligible.
- Rows use fixed busy/watch marker slots and a separate muted provider column
  (`OpenCode`, `Claude`, `agy`, etc.), never an inline description glued to the
  title. Project paths are empty for the current directory or relative within
  project scope; all-project paths show a three-segment, 28-cell directory tail.
  CJK cell widths are respected. The picker is xlarge and shows
  `Reference / watch` plus scope and, when selected, provider. Results are newest
  first, with 100-row pages for long lists.

| Key | Action |
| --- | --- |
| Enter on a row | Open the reference's multiline comment/send dialog |
| Ctrl+A | Switch the same query between this project / all projects; reuse cached results, reset page |
| Alt+P | Cycle All → each provider present in the results → All; reset page |
| Ctrl+W | Watch/unwatch the highlighted OpenCode session |
| Escape | Cancel the picker or comment dialog without sending |

Provider cycling uses **Alt+P**, not Ctrl+P: OpenCode reserves Ctrl+P for the
global palette and dialog navigation. Alt+P was absent from the inspected V2
default key definitions and local overrides. If a provider disappears after
changing scope/results, the filter resets to All. Provider filtering never
removes the provider tag from a row.

`●` means OpenCode's own cached session status is currently running. `盯` marks
members watched by the captured supervisor. An idle session can still be watched.

## One reference send step

Selecting a source calls the installing executable's `another context <id>
--provider <provider> --cached`; all-project selection adds `--all`. Only index
resolution is cached: the selected conversation text is still read natively.
The excerpt carries
the first 3 and last 6 eligible dialogue turns, at most 2,000 runes per turn and
24,000 total. Notification-only harness user messages do not occupy recall slots;
full exports remain unchanged.

The single optional multiline prompt has a strong heading
`Reference into <destination or New session>` and a muted second line
`From <source> · <provider> · <directory tail>`, both truncated by terminal cells.
It uses the native large, non-centered dialog with 1-row top/bottom and 2-cell
left/right padding. The inset field has a real numeric five-row minimum, grows
with document content to twelve rows (bounded by terminal height), and uses
raised/high and focused raised/max theme tokens distinct from the dialog panel.
The placeholder is short and optional; the separated hint row has clickable
primary-colour `enter send` and muted `esc cancel`, plus a newline hint.
Enter sends, Shift+Enter
inserts a newline, and Escape cancels. An empty comment is valid. There is no
second reference confirmation dialog. Native `dialog.prompt` is one-line, so
the integration supplies its own SDK textarea dialog for this step.

The destination route is captured before asynchronous work:

- Established conversation: one ordinary user prompt, `delivery: "steer"`,
  `resume: true`.
- Before its first user message: `resume: false`; references wait for that message
  and multiple references can be staged.
- Home: create a session only after Enter in the send dialog; preserve the
  captured location/model. Cancellation creates nothing.
- Changing tabs while selecting/composing never redirects the reference.

Bounded recall headers include the canonical
`another://session/<provider>/<id>` resource URI and the full-transcript export
command. The existing URI builder now lives in `internal/recall`, where both
headers and MCP resource registration use it without a dependency cycle. MCP
remains project-scoped; the header also gives
`another context <id> --provider <provider> --all` for cross-project reads.

## Server-owned watching

The TUI uses one supported plugin RPC, `another.sessions.watch`, with `status`,
`add`, and `rm`. It does not read storage separately or start follower processes.
Cross-project **watch additions** retain a separate confirmation dialog because
they are a different action from referencing. Non-OpenCode sessions can be
referenced but not watched; self-watch cannot be selected and is also rejected
by the server.

OpenCode activates server plugins per location. The integration shares one
service and one all-location event subscription per server process, while each
location registers the same RPC interface. Groups map supervisor session IDs to
member IDs in `ctx.storage`, namespaced to that server instance. TUI/plugin
reloads retain them; a restarted server has a new instance scope. Standalone
servers have separate watch state and event streams even when they share history.

The service observes native `session.execution.started`,
`session.execution.succeeded`, `session.execution.interrupted`, and
`session.execution.failed`, plus legacy `session.status`/`session.idle` when
emitted. Busy → idle reports once, with later idle duplicates suppressed.
`permission.asked` and `form.created` report entry into a blocked wait; execution
failure reports error. Unwatched sessions produce no reports. Deleted supervisors
are pruned and deleted members are dropped. Status returns an in-memory snapshot,
not a job queued behind event delivery. It schedules missed-deletion reconciliation
in the background at most once a minute; startup and mutations also reconcile.
Unrelated sessions' events do not enter the queue or write storage. Transient
reconciliation read failures are errors, not evidence of deletion.

The first member addition queues an introduction card. Subsequent reports are
queued synthetic cards with a description such as `<title> · turn ended · idle`,
the member ID, and `another context <id> --provider opencode --all`. They explicitly
say they are watch reports, not user instructions. Watching never expands MCP
scope or installs an agent-side search/read tool.

### Why not Herdr

OpenCode's OSC 7501 per-session records exist only while working, queued, or
blocked. On idle it sends `state: "clear"`, and hc removes the record. Therefore
absence cannot identify idle sessions or provide working → idle transitions;
TUIs sharing a tab list can also report the same session. Watching uses native
OpenCode events instead. The pre-existing Herdr **H** navigation action is unchanged.

## Integration and installation

- Integration: `opencode2-sessions`; plugin ID: `another.sessions`.
- Bundled TypeScript/TSX is embedded in the Go binary and installed under
  OpenCode's `plugins/another-sessions/`, alongside the recall MCP/skill integration.
- Enable OpenCode session references in setup, or use
  `another integrations install opencode --kind recall`. Removal uses the same
  recall adapter. Title-policy installation is independent.
- The install manifest and file hashes preserve Missing/Current/Outdated and
  Modified/Foreign protection. Modified or foreign files require explicit force.
- Both conventional server entries (`server.ts` and `index.ts`) expose one default
  definition; `tui.ts` owns `/ref`. A local module manifest avoids npm self-resolution.
- Generated `runtime.ts` records the installing executable's absolute path,
  including `another-dev` and Windows paths. The server revision fingerprint
  replaces the shared watch runtime safely when installed code changes.
- No dependency on opencode-annotate and no inline annotation, Ctrl+T, or `--tui`.

## Acceptance and verified evidence

### Interactive latency (2026-11-12)

The picker never refreshes metadata or builds pending full-text content on its
critical path. The dialog API cannot replace select rows in place, so it accepts
the cached index. Ordinary CLI reads retain automatic refresh, `--refresh` forces
it, and `another index` remains the explicit update path. Cached full-text search
can only match content already indexed.

Each TUI keeps up to 32 query/scope result promises for 30 seconds, including
in-flight searches. Provider cycling and pagination filter those arrays locally;
the opposite scope is prefetched after the picker opens. Ctrl+A reuses it rather
than spawning another process. No CLI searches run on each picker keystroke.
The first-user-message check is deferred to sending, where it is needed for
`resume`, instead of delaying browsing. Watch state is read once per open and
updated from mutation responses, not fetched again on every filter change.

The reported Doubao problem was an **enabled** provider, not a setup-filter bypass.
A failed remote discovery left zero rows/missing discovery metadata, making every
read look like a first scan. A one-minute retry deadline now persists in the index
across CLI processes, never reconciles away cached rows, and can be bypassed by
explicit refresh/rebuild. Successful empty scans are also recognized as indexed.
Provider-specific reads no longer consult or scan unrelated providers.

Before/after used the same real-server SDK harness, same `another` project,
same `webhook` keyword, and the installed `another-dev` executable. The fake
dialog only chose actions; session metadata, watch RPC, CLI processes, and staged
user-prompt admission were real. Owned receivers were deleted; `resume: false`
prevented model execution. Instrumented steps measured:

| Path | Before (`1be8a7a+dev`) | After (`fee27cd+dev`) |
| --- | ---: | ---: |
| Dispatch → picker | 3,648 ms | 45 ms |
| Keyword → results | 29,959 ms | 40 ms |
| Alt+P → next picker | 2,072 ms | 0.9 ms |
| Ctrl+A → first other-scope picker | 2,076 ms | 34 ms |
| Ctrl+A → already-cached scope | 2,039 ms | 0.03 ms |
| Source choice → comment | 758 ms | 38 ms |
| Comment send → API completion | 154 ms | 159 ms (already within target) |

The actual hc-created scratch TUI also showed dispatch → visible picker improve
from 6,597 ms to 40 ms, keyword/all-project results in 82 ms, and key-to-redraw
provider/scope actions in 42–50 ms. These include terminal rendering unlike the
SDK harness. Warm watch RPC was already around 1–4 ms; it was not the measured
open bottleneck, but a slow watched-event regression now proves status cannot
be blocked behind delivery.

The separate apparent 30-second hang was reproduced without dispatch: with
`arguments: true`, the first Enter on bare `/ref` accepts OpenCode's slash
completion; the next Enter dispatches it. `/ref ` (trailing space) or keywords
followed by Enter dispatch immediately. This is native completion behaviour,
not a request waiting on the plugin. No unsupported input interception was added.

A real default CLI retry verified remote backoff: first `list --json --limit 50`
took 2,547 ms and reported Doubao; the next took 39 ms without another probe or
warning. Cached all-project list took 23 ms and cached search 37 ms.

For local diagnostics, set `ANOTHER_REF_TIMING_FILE` to a writable JSONL path in
the TUI environment. It records phase names, durations, and timestamps, never
query/prompt text; it is inactive unless explicitly enabled. Native prompt hooks
remain intact: separate short-prompt probes saw Hindsight's synchronous prompt
hook take about 3.16 seconds, while the actual reference payload above completed
in 159 ms. Returning before admission or bypassing those hooks would hide latency,
not fix it, and was deliberately avoided.

Required development checks:

```sh
go build ./...
go vet ./...
go test -race ./...
make lint
gofmt -l .                         # empty output
GOOS=windows go build ./...
GOOS=windows go vet ./...
cd integrations/opencode2-sessions
npm test
npm run typecheck
# From repository root after checks:
make install
another-dev --version              # carries +dev; binary matches tested build
```

Fixtures cover cross-agent reference selection, provider cycling/tags, self
exclusion, keyword response parsing and scope re-query, comment send/cancel,
home/pre-first/established delivery, source URI encoding and MCP agreement,
watch transitions/pruning/RPC/storage, and integration install-state protection.
Linux and Windows CI run the plugin tests/typecheck using Node 26 (the current
OpenTUI development typings require Node >=26.4), with `npm ci --ignore-scripts`;
the installed plugin uses OpenCode's host runtime, not a separate npm installation.

**Autoload risk resolved:** OpenCode's live plugin inventory reports
`another.sessions` active with both server and TUI features. The maintainer has
verified `/ref` visible in the slash menu (2026-11-12). No explicit TUI config
entry is required. The native registration is also tested with `arguments: true`.
An API-only smoke test created throwaway supervisor/member sessions, verified
shared RPC state across locations and a successful member turn's queued idle
card, then deleted the sessions and pruned the group. Windows cross-build/vet
does not substitute for native Windows runtime/CI verification.

## Differences from the 2026-11-12 plan

| Plan item | Status and reason |
| --- | --- |
| Another owns `/ref`; no inline annotation | Implemented; no Annotate runtime dependency |
| 1. Cross-agent picker, provider tag/filter, scope toggle, hide current/subagents | Adopted; Alt+P avoids reserved Ctrl+P; empty queries use `list`, not argument-less `search` |
| 1. Picker title “Another · Reference” | Deliberately different: `Reference / watch` plus scope expresses the approved combined menu |
| 2. Keyword argument and full-text search | Adopted; decode `results[].session`, preserve query when toggling scope |
| 3. Multiline comment and confirmation in one step | Adopted; Enter in the comment box is the send boundary; watch confirmation remains separate |
| 4. Bounded excerpt and source URI | Adopted; shared URI helper and a scoped-MCP/`context --all` note prevent misleading cross-project links |
| 5. `--all` retained, `--tui` dropped | Implemented; `--all` can precede keywords; no annotation path |
| Captured destination and steer/resume continuation rules | Implemented unchanged; home creation waits for the send step |
| `opencode2-reference` integration name | Deliberately different: retain `opencode2-sessions`, one plugin for references and watching |
| Embedded install/update/remove and portable executable resolution | Implemented with the existing manifest/hash protections |
| No added agent-side search/read tools | Implemented; MCP stays strictly project-scoped |
| Port Annotate flow/regressions without its reader or annotation | Adopted as native SDK picker/textarea and isolated tests, not imports from Annotate |
| Multi-select and other agents' `/ref` deferred | Retained as non-goals; staged pre-first-message references remain supported |
| Isolated-session and install/Windows acceptance; plugin checks in CI | Automated cases covered and plugin checks configured in both CI jobs; remote/native Windows execution remains unverified until an authorized push |
| Release surfaces and coordination with Annotate 0.16.0 | v0.28.0 delivery is recorded in the release receipt after maintainer acceptance; the reference/watch plugin has no Annotate runtime dependency |
| Open TUI autoload risk | Resolved by live inventory and maintainer slash-menu verification; no installer fallback |

The reviewed main-checkout plan was left untouched. Its read-only baseline SHA-256
is `e48f1986cd66b2b41f78a982b41726ac941791db56320d5a652c03583c2f49e2`.
Provenance is recorded in `sources.json`; this document is the current reconciled
design, not a second pending plan.
