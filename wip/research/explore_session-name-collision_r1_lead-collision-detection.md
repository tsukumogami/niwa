# Lead: Can niwa detect a session-name collision at dispatch time, and against what set of peers?

> **Redaction note:** this file quotes a dump of the live job store on the
> author's machine. Workspace and session names belonging to non-public
> projects have been replaced with neutral placeholders (`workspace-b`,
> `role_coordinator`, and similar). The structure, counts, and reasoning are
> unchanged; only the identifiers are substituted. `tsuku` is left as-is.


Round 1. All claims re-verified against the working tree at branch
`docs/session-name-collision` (HEAD `ef53bb3`). Line numbers below were read
from that tree, not carried over from the prior pass.

## Findings

### The short answer

Yes, and against a better peer set than expected. niwa can enumerate every
Claude Code background session on the machine, read each one's display name,
and tell whether it is still live — using only files it already reads on the
dispatch path, with no new dependency and no subprocess. What it cannot see is
anything off this machine.

The load-bearing discovery is that `~/.claude/jobs/<id>/state.json` carries a
`name` field, and niwa's decoder simply does not declare it.

### Sub-question 1: what state does niwa keep about live dispatched sessions?

There are two stores, and they answer different questions.

**Store A — niwa's own session mapping**, `internal/workspace/session_map.go`.
Persisted at the **workspace root** under `.niwa/sessions/<session_id>.json`
(`sessionsDir`, session_map.go:107-109; the `.json` naming at :114-119). The
schema is the `SessionMapping` struct at session_map.go:49-95:

| Field | Line | Carries the session name? |
|---|---|---|
| `session_id` | :50 | no |
| `instance_name` | :51 | **yes, indirectly** — it is `<config>+<slug>-<8hex>` |
| `instance_path` | :52 | indirectly (basename of the same) |
| `transcript_path` | :53 | no |
| `created` | :54 | no |
| `ephemeral` | :55 | no |
| `agent` | :64 | no |
| `handle` | :77 | no — the agent-side handle, the job dir name for Claude |
| `label` | :81 | no — this is `--label`, a different flag from `--name` |
| `origin` | :87 | no |
| `keep_alive` | :94 | no |

So **the mapping does not record the session display name as a field.** It is
recoverable from `instance_name` by stripping the `<config>+` prefix and the
`-<8hex>` tail (the regex `\+[a-z0-9_]*-[0-9a-f]{8}$` at
`internal/cli/dispatch.go:938` already parses exactly that shape), but nothing
in the tree does that today. Note also `Label` is a red herring for this lead:
`dispatch.go:769` writes `dispatchLabel` (the `--label` flag) into it, never
the `--name` slug.

`ListSessionMappings` (session_map.go:184-214) is the ready-made enumerator: it
reads the whole store, skips malformed entries rather than failing, and returns
sorted results. The reaper is its only caller today.

**Store B — Claude Code's own job state**, `~/.claude/jobs/<id>/state.json`.
niwa reads it through two paths:

- `internal/cli/job_state.go` — `defaultJobsDir()` at :70-76 hardcodes
  `~/.claude/jobs`; `jobState` at :36-65 declares the subset of fields niwa
  decodes: `sessionId`, `template`, `cwd`, `state`, `tempo`, `inFlight`,
  `block`, `needs`. **There is no `name` field in this struct.**
- `internal/cli/session_records.go` — the agent-neutral reader, driven by the
  per-agent `agentplan.SessionRecords` declaration. For Claude that declaration
  is `internal/agentplan/dispatch.go:361-379`: `HomePath: [".claude","jobs"]`,
  `Depth: 1`, `FileName: "state.json"`, `CwdPath: ["cwd"]`, `IDPath:
  ["sessionId"]`, `Handle: HandleRecordDir`, `Liveness:
  LivenessRecordPresence`. `scanSessionRecords` (session_records.go:174-209)
  walks it and decodes each file via `decodeSessionRecord` (:262-289), which
  pulls out **only** the two declared field paths — cwd and id.

**The empirical part.** I dumped the real store on this machine
(`~/.claude/jobs/*/state.json`, 10 entries). Every one carries `name` and
`nameSource`:

```
1cb5144b  name='role_substrate'          nameSource=user  template=bg  state=done     cwd=.../workspace-c+role_substrate_work-7d00309e
276fb88b  name='inert_defaultmode_key'      nameSource=user  template=bg  state=working  cwd=.../tsuku+inert_defaultmode_key-3d5dcce7
3613230a  name='legacy-role_coordinator'  nameSource=user  template=bg  state=done     cwd=.../workspace-b+role_coordinator-55821325
4b434142  name='role_drain'           nameSource=user  template=bg  state=working  cwd=.../workspace-b+role_drain-2b2d4fd2
88eeca84  name='niwa_sendmessage'           nameSource=user  template=bg  state=working  cwd=.../tsuku+niwa_sendmessage-079981c3
96d772fa  name='role_coordinator'         nameSource=user  template=bg  state=done     cwd=.../workspace-b+role_coordinator-d7578335
9af72545  name='role_ci_teardown'    nameSource=user  template=bg  state=done     cwd=.../workspace-b+role_ci_teardown-43c34906
b71b9f2f  name='session_name_collision'     nameSource=user  template=bg  state=working  cwd=.../tsuku+session_name_collision-21dbe5c8
cbc089c5  name='role_coord2'             nameSource=user  template=bg  state=done     cwd=.../workspace-c+role_coord2-20cca76d
```

Three things fall out of that dump:

1. `name` is exactly the slug niwa forwarded — compare `name` to the slug
   embedded in `cwd` on every row. `nameSource: user` confirms it came from the
   `--name` flag rather than an auto-derived topic.
2. **A collision has already happened on this machine and was patched by hand.**
   Rows `3613230a` and `96d772fa` both ran in a `workspace-b+role_coordinator-*`
   instance — the same `--name role_coordinator` dispatched twice — and one
   of them now reads `legacy-role_coordinator`. That rename cannot have come
   from niwa: `sanitizeInstanceSlug` (dispatch.go:910-927) strips dashes, so
   `legacy-role_coordinator` is not a value niwa could have produced. Someone
   renamed the session after the fact to tell the two apart. This is the bug
   occurring in the field, not a hypothetical.
3. Entries persist after the session finishes (`state=done`), which is
   deliberate — see liveness below.

### Sub-question 2: is the store per-workspace, per-user, or per-machine?

Both, and the difference decides the whole lead.

**Store A is strictly per-workspace.** Every entry point takes `workspaceRoot`
and joins `StateDir` under it (session_map.go:107-109). On this machine there
are five separate workspace roots, each with its own store:

```
~/dev/niwaw/workspace-b/.niwa/sessions/          4 mappings
~/dev/niwaw/workspace-d/.niwa/sessions/          2
~/dev/niwaw/workspace-e/.niwa/sessions/  0
~/dev/niwaw/workspace-c/.niwa/sessions/    2
~/dev/niwaw/tsuku/.niwa/sessions/             3
```

Dispatch resolves `workspaceRoot` from cwd (`workspace.ClassifyCwd`,
dispatch.go:292-299) and never climbs out of it. **A dispatch in `tsuku` cannot
see a mapping written in `workspace-b`.** So a detection built on Store A alone
would miss exactly the cross-workspace case, and the `role_coordinator`
collision above shows collisions arise from repeating a role name — the kind of
name (`coordinator`, `review`, `ci`) most likely to be reused across
workspaces.

**Store B is per-user, machine-wide.** `defaultJobsDir()` is
`~/.claude/jobs` with no workspace component (job_state.go:70-76), and the dump
above proves it: one directory holds sessions whose cwds span `tsuku`,
`workspace-b` and `workspace-c`. So yes — a dispatch in workspace A can see a
live session named `review` dispatched from workspace B, but only if it reads
Store B rather than Store A. Multi-user is out of reach (a different `$HOME`),
though that is not a realistic scenario for a developer laptop.

### Sub-question 3: does niwa know a peer is still LIVE, or only that it existed?

For Claude, it knows, and the signal is already implemented and documented.

`sessionLive(jobsDir, sessionID, now)` at job_state.go:104-123 is the rule:
the job entry at `<jobsDir>/<session-id>/state.json` present and its recorded
`sessionId` matching means **live**; entry gone means **deleted**. The doc
states the same rule in one line — "entry present = live, entry gone =
deleted" (`docs/guides/ephemeral-session-instances.md:193-203`). It is declared
per-agent as `Liveness: LivenessRecordPresence`
(`internal/agentplan/dispatch.go:377-379`).

Crucially, "live" here means *exists in Agent View*, covering a running session
and an idle-but-resumable one. Job comments at job_state.go:85-91 spell out that
liveness deliberately ignores `state`, `firstTerminalAt`, and idle TTLs, because
each of those is true of a session you can still re-open. **That is precisely
the right notion for a name collision**: a `state=done` session is still listed
and still addressable, so it still owns the name. Four of the ten rows in the
dump are `done` and would each still be a collision target.

Staleness bounds: the entry disappears only when the developer deletes the
session, so the store cannot be stale *late* by much. The known gap is at the
other end — `docs/guides/ephemeral-session-instances.md:238-245` records the
open edge that Agent View stops a finished session's supervisor after roughly an
hour and it is **not confirmed** whether that also removes the job entry. If it
does, a check would under-report (miss a resumable-but-supervisor-stopped peer)
rather than over-report. Additionally, `docs/guides/session-keep-alive.md:72-82`
notes that archiving a session in claude.ai is server-side only and leaves the
local entry in place — so an archived session reads as live locally. Both errors
are in the safe direction for a collision warning: they produce a spurious or
missed warning, never a wrong launch.

For Codex the picture is worse but moot: its liveness is
`LivenessRecordActivity` — mtime plus a probed advisory flock
(`internal/agentplan/dispatch.go:468` and `recordActivityLive` at
session_records.go:130-163) — which is genuinely fuzzy. But Codex's
`LaunchFlags` has **no `DisplayName` at all** (`internal/agentplan/dispatch.go:414-425`,
with the comment "No subagent types, no session display name, and no inline
settings document"), so a Codex worker has no session name to collide. The
collision question is Claude-only by declaration, which conveniently matches the
agent whose liveness signal is exact.

### Sub-question 4: visibility into other machines and the cloud

**None, definitively.** Three independent lines of evidence:

1. Every store niwa reads is a local path under `$HOME` or under the workspace
   root — `defaultJobsDir()` (job_state.go:70-76), `recordStoreRoot`
   (session_records.go:75-90), `sessionsDir` (session_map.go:107-109). There is
   no other source.
2. The dispatch path makes no network call. `grep -n "net/http"` over
   `internal/cli/dispatch*.go`, `session_records.go` and `job_state.go` returns
   nothing; the only `exec.Command` uses in the dispatch path are the launcher
   itself (`dispatch_launcher.go:152,211,276`), the plugin registration
   (`dispatch_plugins.go:171`), and `lookAgentBinary` → `exec.LookPath`
   (dispatch.go:145).
3. The docs say so implicitly, twice. `docs/guides/remote-control-on-dispatch.md:66-68`
   states that remote-control eligibility gaps "are surfaced by Claude Code
   itself when the worker tries to connect the bridge, not by niwa" — niwa never
   talks to the bridge. And `docs/guides/session-keep-alive.md:72-82` documents
   a server-side action (archive on claude.ai) that local state cannot observe.

**What that costs.** Remote Control makes a dispatched session reachable from
claude.ai and mobile (`docs/guides/remote-control-on-dispatch.md:1-7`), and
`remote_control_on_dispatch = true` makes that the default for every dispatch on
the host. So the addressable peer set is potentially larger than the machine —
sessions dispatched from a second laptop, or cloud sessions — and niwa sees none
of them. A local check is therefore **necessarily one-sided**: a hit is real
evidence of a collision, a miss is not evidence of uniqueness. Any feature built
on it must be phrased as "this name is taken" and never as "this name is free".

### Sub-question 5: prior art for a uniqueness-check-then-retry loop

Yes, in `niwa create`, and dispatch deliberately does not use it.

- **Check-then-retry loop**: `computeInstanceName` at
  `internal/cli/create.go:96-105` scans `<config>-2`, `<config>-3`, … stat'ing
  each until one does not exist. Same shape as `NextInstanceNumber`
  (`internal/workspace/state.go:541-561`), which scans instances and returns the
  lowest unused number.
- **Check-then-refuse**: `create.go:207-211` stats the computed directory and
  returns `instance directory already exists: %s`.

**Dispatch is unique by construction, not by checking.** `dispatchNameSuffix`
(dispatch.go:878-887) draws 4 bytes from `crypto/rand` and hex-encodes them.
The suffix is **random, not derived** — it is not a hash of the slug, the
prompt, the time, or the session id. It never reads the filesystem. The design
states why: D2 in `docs/designs/current/DESIGN-instance-dispatch.md:150-158`
rejects the numbered scan explicitly as "TOCTOU, the exact race R36/R37 forbid"
and rejects a timestamp for colliding under concurrency. The code comment at
dispatch.go:450-457 repeats it: "sidestepping the racy numbered scan".

The difference matters for this lead. Unique-by-construction needs no lock and
cannot race. A collision *check* is inherently check-then-act: two concurrent
dispatches with the same `--name` would both scan before either worker's job
entry exists (the entry is written by the launched worker, after niwa's check),
and both would pass. There is no lock anywhere on this path. **So option (b)
cannot be made correct under concurrency** — it can only be made *usually*
right, which is fine for a warning and not fine for a guarantee.

The same design doc, at DESIGN-instance-dispatch.md:170-173, says the random hex
keeps things "collision-safe even when two dispatches share a `--name`" — which
is true of the instance directory and silently not true of the session name.
That sentence is where the gap was introduced.

### Sub-question 6: what would a collision check cost?

Almost nothing, and it is a scan niwa already performs.

`instanceHasLiveJob` (job_state.go:142-165) already does the exact walk a name
check would need: `os.ReadDir(jobsDir)`, then decode every
`<entry>/state.json`. It runs on **every** dispatch, via the opportunistic reap
at dispatch.go:475 (`reapOpportunistically(workspaceRoot)`), which is called
before the instance is even created. `captureSessionID`
(`internal/cli/dispatch_capture.go:35-62`) then polls `scanSessionRecords` over
the same directory after launch.

Measured cost on this machine: reading and JSON-decoding all 10 `state.json`
files took **0.4 ms** in Python; files are 1.2–9.6 KB. In Go it would be faster.
No subprocess, no process table walk, no network.

So the incremental cost of a name check is: add a `NamePath: ["name"]` field to
`agentplan.SessionRecords`, have `decodeSessionRecord` (session_records.go:262-289)
pull it out via the existing `stringAt` helper, and compare. That is additive to
a declaration-driven reader that already has `CwdPath` and `IDPath` doing the
same thing. It stays inside the `dispatch_layout_test.go` discipline (that scan
covers `session_records.go` and forbids naming an agent in it —
`internal/cli/dispatch_layout_test.go:38-49`) because the field path would live
in the per-agent declaration, not in the reader.

Prior art for pre-flight checks that gate a dispatch:
- `lookAgentBinary(spec.Binary)` at dispatch.go:405-407 — refuses before any
  instance exists.
- `internal/cli/watch.go:962-980` probes `bwrap` / `socat` / `sandbox-exec` with
  `exec.LookPath` before committing to a sandbox mode.
- `internal/cli/watch.go:476-480` shells out to `claude stop <shortID>` — so
  calling the agent binary is not off-limits, though nothing here needs it.

### Sub-question 7: is there a convention for "we cannot know for sure"?

Yes, and it is unusually well established on this exact command. `niwa dispatch`
prints five distinct advisory lines to stderr **before provisioning anything**
and continues in every case:

| What it cannot be sure of | Site |
|---|---|
| `--agent` may have been meant as `--harness` | dispatch.go:355-357, `harnessMismatchWarning` |
| a config rung was unreadable, so resolution used fewer inputs | dispatch.go:328-330 |
| a renamed env var is set that niwa no longer reads | dispatch.go:335-337 |
| the model value is not in the agent's vocabulary — forwarded as-is | dispatch.go:527-530, `resolveDispatchModel` |
| the worker starts under-equipped for a repo-scoped config agent | dispatch.go:419-424 |

The comment at dispatch.go:346-354 states the doctrine outright: "A warning
rather than a refusal … refusing would break setups that already work. The
trigger is deliberately narrow." The keep-alive guide does the same thing at
the feature level — "Requesting it for a worker that starts without remote
control prints a warning and arms nothing; the dispatch still succeeds"
(`docs/guides/session-keep-alive.md:31-33`).

The `niwa: warning:` prefix is used elsewhere for post-hoc uncertainty
(dispatch.go:741,745,785,854; reap.go:477,488,743); the pre-flight advisories on
this command use the `niwa dispatch: ` prefix. A collision advisory would use
the latter and sit alongside the existing block at dispatch.go:328-357.

There is also a "when in doubt, spare" convention in the reaper — see
`recordActivityLive`'s `known bool` return (session_records.go:130-163) and the
comment "A caller that cannot tell must spare". Its analogue here is: when the
check cannot answer, say nothing rather than assert uniqueness.

## Implications

**Option (b) — keep the readable name when unambiguous, disambiguate only on
collision — is implementable, and cheaper than expected.** The data is a
`name` field in a file niwa already opens on every dispatch, the peer set is
machine-wide (which beats the per-workspace mapping store and covers the
cross-workspace case the real-world `role_coordinator` collision came from),
liveness is an exact entry-present rule that correctly counts finished-but-
resumable sessions as still owning their name, and the cost is sub-millisecond.
The change is roughly one field in `agentplan.SessionRecords`, one line in
`decodeSessionRecord`, and a comparison in `runDispatch` before step (4).

**But it can only ever be a warning or a best-effort disambiguation, never a
guarantee.** Three independent reasons, each sufficient on its own:

1. *Off-machine peers are invisible.* Remote Control (on by default for
   dispatch on hosts that set `remote_control_on_dispatch`) puts sessions from
   other machines and the cloud in the same address space. A local hit proves a
   collision; a local miss proves nothing.
2. *No lock, so check-then-act races.* The new worker's job entry appears after
   niwa's check, written by the launched worker. Two simultaneous
   `--name review` dispatches both pass. This is the same TOCTOU the design
   already rejected for instance naming (DESIGN-instance-dispatch.md:150-158) —
   and the reason dispatch went unique-by-construction in the first place.
3. *One documented staleness edge.* The supervisor-stop question at
   `docs/guides/ephemeral-session-instances.md:238-245` is unresolved, so the
   peer set may under-report.

That reshapes the exploration's option space. It is not "(a) always suffix vs.
(b) detect vs. (c) nothing". It is:

- **(a)** always suffix — a guarantee, at the cost of every name in Agent View
  reading `review-4e33acfa`.
- **(b1)** detect and *warn*, launch anyway with the plain name — cheap, honest,
  fits the command's existing five-warning pre-flight doctrine exactly, and
  leaves the human to decide. Does not fix silent misdelivery; it only makes the
  collision visible at the moment it is created.
- **(b2)** detect and *auto-disambiguate* — suffix only the second `review`.
  Readable in the common case, but it is a probabilistic guarantee dressed as a
  deterministic one, and a reader seeing `review` and `review-4e33acfa` would
  reasonably infer the plain one is unique when it may not be.
- **(c)** nothing.

My reading is that (b1) is the one that survives scrutiny, and (b2) is the trap:
it buys readability by making a promise the mechanism cannot keep, exactly the
kind of "clean exit is not evidence it took effect" failure the codebase warns
about elsewhere (`internal/agentplan/dispatch.go:409-413`). (a) and (b1) also
compose — warn on a detected collision *and* let the developer opt into a
suffix — which may be the real shape.

One more implication worth carrying forward: the empirical `legacy-role_coordinator`
rename is evidence this bug has already cost someone manual work on this
machine. That is a stronger argument for acting than the theoretical one in the
scope doc.

## Surprises

1. **`state.json` carries `name` and `nameSource` and niwa's decoder throws them
   away.** The `jobState` struct (job_state.go:36-65) enumerates eight fields
   and skips the one this lead needed; the declaration-driven reader
   (`internal/agentplan/dispatch.go:361-379`) declares `CwdPath` and `IDPath`
   and no name path. The capability was one struct field away the whole time.

2. **A collision already happened here and was fixed by hand.** Two
   `workspace-b+role_coordinator-*` instances, one session renamed to
   `legacy-role_coordinator` — a name niwa's own sanitizer could not have
   produced, since it strips dashes (dispatch.go:910-927).

3. **The per-workspace store is the *worse* option.** I expected
   `.niwa/sessions` to be the answer and `~/.claude/jobs` to be the fallback.
   It is the reverse: the mapping store does not record the name as a field at
   all and is blind across workspaces, while the machine-wide jobs dir has the
   name verbatim and spans all five workspaces on this host.

4. **The design doc contains the exact sentence that hides the gap.**
   DESIGN-instance-dispatch.md:170-173 says the scheme "stays collision-safe
   even when two dispatches share a `--name`" — true of the instance directory,
   silently false of the session name it forwards in the next clause.

5. **Codex has no display name at all**
   (`internal/agentplan/dispatch.go:414-425`), so this is a Claude-only problem
   by declaration — which happens to be the agent with the exact
   entry-present liveness rule rather than the fuzzy mtime-plus-flock one.

## Open Questions

1. **Does a `state=done` session still receive messages addressed to its name?**
   I established it is still *listed* and still holds the name locally. Whether
   Claude Code's message routing will deliver to it is lead 2's territory, and
   it decides whether the peer set is "all entries" or "entries where
   `state != done`". If done sessions do not receive, the check should still
   count them (a name in Agent View is still a name a human will confuse), but
   the severity differs.

2. **Is `name` in `state.json` stable across Claude Code versions?** It is an
   undocumented internal file — the guide already flags the `template` read as
   "a stability risk if its format changes"
   (`docs/guides/ephemeral-session-instances.md:108-112`). Every reader in
   job_state.go fails safe on a missing field, so a rename degrades the check to
   silence rather than to a wrong answer. Worth confirming across a version or
   two before relying on it.

3. **Does the ~1-hour supervisor stop remove the job entry?** Still the open
   edge at `docs/guides/ephemeral-session-instances.md:238-245`. It bounds how
   badly a check can under-report.

4. **Is a warning enough, given the stakes shift the scope doc describes?** The
   scope notes a sibling session may pre-approve inbound peer delivery, which
   turns a visible misdelivery into a silent one. If that lands, a stderr line
   printed once at dispatch time may be too weak — a human reads it once, and
   the misdelivery happens hours later. That is a product judgment, not a
   research finding.

5. **Should the check consult both stores?** Store B (machine-wide, has the
   name, Claude only) is strictly better for this purpose, but Store A carries
   the slug inside `instance_name` and would catch a workspace-local peer whose
   job entry vanished. Probably not worth the complexity; flagging it so the
   decision is explicit rather than accidental.

## Summary

niwa can detect a same-name live peer at dispatch time by reading
`~/.claude/jobs/<id>/state.json`, which carries a `name` field verbatim from
`--name` and which niwa already scans on every dispatch — the peer set is
machine-wide across all workspaces (better than the per-workspace
`.niwa/sessions` store, which does not record the name as a field at all), and
liveness is the exact entry-present rule that correctly counts finished-but-
resumable sessions, at a measured 0.4 ms. So the "detect and disambiguate" middle
path is implementable, but it can only ever be a one-sided warning: off-machine
Remote Control and cloud peers are fundamentally invisible, and with no lock the
check races two concurrent dispatches — the same TOCTOU that pushed instance
naming to unique-by-construction in the first place. The biggest open question is
whether a stderr warning at dispatch time is a strong enough remedy given the
sibling work that would make the resulting misdelivery silent.
