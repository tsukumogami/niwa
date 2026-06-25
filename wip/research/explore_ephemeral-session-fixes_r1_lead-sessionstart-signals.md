# Lead: What signals are reliably available at SessionStart to distinguish a *dispatched background worker* session from an *interactive/foreground* session?

Round 1. Sources: niwa code/docs in the worktree (grounding) + Claude Code official hook
docs + anthropics/claude-code GitHub issues (empirical, version-stamped). Throughout I
distinguish DOCUMENTED (official docs) from EMPIRICAL (observed in issues / niwa's own
spike). Visibility: public — all cited issues are in the public anthropics/claude-code repo.

## Findings

### Sub-question 1 — SessionStart hook JSON payload fields (stdin)

**DOCUMENTED** (https://code.claude.com/docs/en/hooks). SessionStart receives the common
hook fields plus SessionStart-specific ones:

- Common: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `permission_mode`.
- SessionStart-specific: `source`, `model`, `agent_type`, `session_title`.
- `source` documented values: `"startup"`, `"resume"`, `"clear"`, `"compact"`.
- When run with `--agent <name>` or inside a subagent, two more fields appear: `agent_id`
  and `agent_type` (`agent_type` = the agent name passed to `--agent`).

**What niwa reads today** — `instanceHookPayload` (internal/cli/instance_from_hook.go:60-66)
decodes only `hook_event_name`, `session_id`, `cwd`, `transcript_path`, `source`. It does
NOT decode `agent_type`, `model`, `agent_id`, `permission_mode`, or `session_title`. Absent
fields decode to zero values (documented in the struct comment).

**The decisive negative**: there is NO documented payload field that flags a session as a
background/dispatched worker vs interactive/foreground. `agent_type` is present whenever
`--agent` is used — and per issue #68204 bg sessions are launched with `--agent claude`,
i.e. the DEFAULT agent — so `agent_type` is `"claude"` for both a default-agent bg worker
and a `--agent claude` interactive session. It is not a fg/bg discriminator. `source` is
`"startup"` for both (confirmed by niwa's own spike, SPIKE line 75-96, and the DESIGN's
Decision-3 statement that "coordinator and workers both present source:startup,
agent_type:claude"). This matches the spike's load-bearing finding (SPIKE line 96): "No
native field distinguishes the coordinator from a worker."

Why it matters: the hook payload alone cannot answer the guard's question. niwa is forced
to a side channel (job state and/or transcript), which is exactly what creates the two bugs.

### Sub-question 2 — `~/.claude/jobs/<id>/state.json` contents and the `template` meaning

niwa reads this file via `jobState` (internal/cli/job_state.go:30-35): `sessionId`,
`template`, `state`, `updatedAt`. The guard keys on `template == "bg"`
(instance_from_hook.go:76, 295: `bgJobTemplate = "bg"`).

**EMPIRICAL state.json shape** (anthropics/claude-code#59848, verified on 2.1.143;
#60437, verified on 2.1.143; #65051 on 2.1.161; #68204 on 2.1.175). Confirmed fields:
`state`, `tempo`, `template`, `respawnFlags` (array of CLI flags, e.g.
`["--effort","high","--permission-mode","auto"]`), `name`, `nameSource`, `cliVersion`,
`cwd`, `sessionId`, and — critically — `backend` (e.g. `"daemon"`). Example from #60437:

```json
{ "template": "bg", "backend": "daemon", "sessionId": "<uuid>",
  "cwd": "<cwd>", ... }
```

**The `template` field is NOT a clean fg/bg flag.** Two contradictory observations, both
real, reconcile to "do not trust `template`":

- The lead's framing (issue #171's reading) says `template` is the launch agent/profile
  (`--agent <x>`), so a default-agent bg session carries `template:"claude"` and the guard
  silently skips it. This is consistent with the documented `agent_type` semantics and with
  #68204's "launched with `--agent claude`".
- The OPPOSITE failure is documented in #59848 (post-2.1.139, "May 11", repro on 2.1.143):
  `template:"bg"` is stamped on EVERY session including the user's interactive foreground
  session, because the daemon now registers even the TTY-attached session as a job. So
  `template:"bg"` produces FALSE POSITIVES too.

Net: across versions `template` is neither necessary nor sufficient for "bg worker". It has
been observed (a) carrying the agent name (false negatives for default-agent bg workers,
the #171 bug niwa hit) and (b) plastered on all sessions (false positives, #59848). The
DESIGN (Decision 3, lines 174-189) and the job_state.go comment both call state.json
"undocumented" and a "stability risk" — the evidence confirms it has already churned.

**There is no foreground/background flag in state.json.** The closest reliable-looking
field is `backend` (`"daemon"` for daemon-managed bg/dispatched jobs, per #60437 and
#65051's `"backend": "daemon"`). niwa does NOT currently decode `backend`. But see the
caveats below — `backend:"daemon"` is not a guaranteed clean discriminator either, and
neither issue claims an interactive session never gets a daemon backend.

### Sub-question 3 — `sessionKind: "bg"` lives in the TRANSCRIPT, and the race is REAL

**EMPIRICAL, multiply confirmed.** `sessionKind` is a field on TRANSCRIPT JSONL records
(`~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`), NOT in state.json:

- #59415 (2.1.142): bg sessions write `"sessionKind":"bg"` into JSONL entries. Quoted
  record: `{"type":"system","subtype":"compact_boundary","sessionKind":"bg","forkedFrom":{...}}`.
  "**every subsequent entry written by that bg process also carries `sessionKind:"bg"`**."
  In their sample 1831/2848 entries carried it. Confirmed it is per-record JSONL metadata
  (their fix is `sed 's/,"sessionKind":"bg"//g'` over the file).
- #65051 (2.1.161): "background job, daemon backend (`backend:"daemon"`, `sessionKind:"bg"`
  in the transcript records)" — explicitly locates `sessionKind` in the transcript and pairs
  it with `backend:"daemon"` in job state.
- #68204 (2.1.175): a completed bg job is identified by `sessionKind:"bg"`; `state.json`
  carries `state:"done"` but the bg identity marker is the transcript's `sessionKind`.

So `sessionKind:"bg"` is the most semantically-correct fg/bg discriminator that exists —
but it is transcript-resident.

**Is it readable at the MOMENT SessionStart fires? The race is real and material.**

- The first transcript record is a `type:"system"` record carrying the system prompt
  (databunny/Medium "Inside Claude Code: The Session File Format": "The first record in
  every session file is a `system` message containing the complete system prompt..."). In
  #59415 a `sessionKind`-bearing record is a `type:"system"` record — so in principle the
  very first record CAN carry `sessionKind`.
- BUT transcript writes are async and lag hook firing. #56631 documents the canonical race
  for the symmetric case: a hook "fires before Claude Code has flushed the transcript JSONL
  file" — the file can be empty/missing/partial when the hook runs. The official docs and
  community guidance both warn SessionStart fires very early (before MCP servers connect,
  before first turn). #9188 separately documents hooks receiving stale `transcript_path`
  after `/exit`/`--continue`.
- Therefore at SessionStart the transcript at `transcript_path` may not yet exist, may be
  zero-length, or may contain a first record that does not yet carry `sessionKind`. Reading
  `sessionKind` synchronously in the SessionStart hook is a race you lose an unbounded
  fraction of the time. There is no documented happens-before guarantee that the first
  `sessionKind`-bearing record is flushed before the SessionStart hook is invoked. The
  lead's #171 warning is corroborated: the transcript signal is correct-but-racy at
  SessionStart.

How bad: non-deterministic. It depends on disk-flush timing vs hook-process spawn timing;
expect frequent misses on cold start (#56631's "first-turn-of-session may miss"). A
SessionStart hook cannot safely block-and-poll either — SessionStart hooks gate session
startup, and the design (Decision 4) needs the hook to emit `additionalContext` promptly.

### Sub-question 4 — Any OTHER reliable discriminators?

- **Env var `CLAUDE_JOB_DIR`** — set for daemon-managed jobs (#59848 shows it on the
  foreground session too, so its mere PRESENCE is not a clean bg flag post-2.1.139). DESIGN
  Decision 3 already rejects it: "the `CLAUDE_JOB_DIR` env var is NOT reliably set in every
  session" (instance_from_hook.go:245-246 comment). Confirmed not reliable.
- **`backend:"daemon"` in state.json** — the strongest job-state-resident candidate
  (#60437, #65051). It marks the daemon/fleet pool. niwa does not read it today. Caveat: no
  source guarantees an interactive session is never daemon-backed (post-2.1.139 the daemon
  spawns a spare pool and registers the TTY session as a job, #59848), so `backend` may
  share the false-positive risk; it needs its own empirical confirmation before trusting.
- **`respawnFlags` array** — carries the launch flags (`--bg`, `--permission-mode`, etc.).
  #59848 notes the real daemon-layer discriminators are `--origin transient`,
  `--spawned-by {label,cwd,pid}`, `--bg`/`--bg-spare` — these live in the process argv /
  daemon layer, only partially surfaced into `respawnFlags`. Not currently a stable
  documented field niwa can key on.
- **Process tree / argv** (`--bg`, `--spawned-by`, `--bg-spare`) — the genuinely
  authoritative signal per #59848, but it is daemon-internal, not exposed to a hook, and not
  in any file niwa reads. Out of reach for a hook subcommand.
- **`agent_type` / `agent_id`** — present but not fg/bg discriminating (see sub-question 1).
- **`source`** — `"startup"` for both coordinator and worker (spike + DESIGN). Not
  discriminating.

## Implications

- The guard's current single signal (`template == "bg"`) is wrong in BOTH directions: false
  negatives when bg uses the default agent and `template` carries the agent name (#171, the
  reported bug), and false positives when the daemon stamps `template:"bg"` on every session
  (#59848). No single field niwa reads today is a correct discriminator.
- `sessionKind:"bg"` (transcript) is the most semantically-correct marker but is unreadable
  reliably at SessionStart due to the flush race. Any fix that reads it AT SessionStart must
  tolerate absence (treat "transcript not yet showing sessionKind" as undecided, not as
  "interactive"), or defer the bg-decision to a later hook (e.g. UserPromptSubmit, or a
  PostToolUse, by which time the transcript is populated) — a design change, not a one-line
  field swap.
- `backend:"daemon"` in state.json is the most promising job-state-resident replacement and
  is race-free (state.json exists when the job is dispatched), but it (a) is not currently
  decoded by niwa and (b) needs empirical confirmation that interactive sessions don't also
  carry `backend:"daemon"` post-2.1.139. A robust guard likely needs a COMBINATION, e.g.
  `backend == "daemon"` AND the re-entrancy/master-switch gates niwa already has, possibly
  corroborated by `sessionKind` once the transcript is available.
- The opt-in master switch + reaper (DESIGN Decision 3/6) already bound the blast radius of
  a misfire to wasted clones, so the cost of a slightly-too-permissive bg detector is
  bounded — which argues for choosing the race-free `backend`/job-state signal over the
  racy transcript read, accepting some false positives that the reaper cleans up.

## Surprises

- **The `template` contradiction.** The lead framed `template` as "the launch agent" (#171).
  Issue #59848 shows the exact opposite failure mode — `template:"bg"` on everything — on a
  nearby version. Both are real; the lesson is that `template` semantics have CHURNED across
  2.1.x releases. This strengthens the DESIGN's own "undocumented file, stability risk"
  caveat into "already empirically unstable", and means a fix must not just pick a different
  field-value but pick a field whose meaning is stable.
- **Default bg agent is literally `--agent claude`** (#68204). This is the precise mechanism
  of the #171 false-negative: the default agent name collides with the interactive default,
  so `template`/`agent_type` cannot separate them.
- **`backend` exists and niwa never reads it.** state.json has a `backend` field
  (`"daemon"`) that is closer to a true fg/bg signal than `template`, and niwa's `jobState`
  struct simply omits it. Low-cost to start decoding.
- **`sessionKind` taint propagates through `/compact` forks** (#59415) and even makes
  sessions invisible in `/resume`. Irrelevant to the guard directly, but it confirms
  `sessionKind` is durable per-record JSONL metadata, not a one-shot header — reinforcing
  that it CAN appear on the first system record (good) but is written by the running process
  asynchronously (the race).

## Open Questions

1. Does an interactive foreground session EVER carry `backend:"daemon"` in state.json
   post-2.1.139? If not, `backend` is the race-free fix. If yes, it shares #59848's
   false-positive problem and must be combined with another signal. Needs a live dogfood
   like the original spike.
2. At the EXACT instant SessionStart fires for a dispatched bg worker, what is the state of
   `transcript_path`: missing, zero-length, or a first `system` record already carrying
   `sessionKind:"bg"`? The race is real (#56631) but its hit-rate for THIS event on a
   dispatched worker is unmeasured. A spike that logs `os.Stat`+first-line of the transcript
   from inside the SessionStart hook would quantify it.
3. Is there a later, transcript-safe hook (UserPromptSubmit / PreToolUse / PostToolUse) at
   which `sessionKind` is reliably present, and can the provisioning decision be deferred to
   it without breaking the "instance ready before first work" contract (DESIGN Decision 4)?
4. Are `respawnFlags` / `--bg` / `--spawned-by` ever surfaced into a hook-readable file
   (not just process argv)? If `respawnFlags` reliably contains a bg marker it would be a
   race-free job-state signal.
5. Version pinning: every cited observation is 2.1.142-2.1.177. Which Claude Code version is
   the workspace actually on, and does `backend:"daemon"` / `sessionKind:"bg"` hold there?

## Summary

The SessionStart hook payload carries no fg/bg flag (`source` and `agent_type` are identical
for coordinator and worker, and bg sessions use the default `--agent claude`), so niwa is
forced onto a side channel — and the side channel it chose, job-state `template`, is
empirically unstable across 2.1.x (it has been observed both as the agent name, the #171
false-negative niwa hit, and stamped `"bg"` on every session, the #59848 false-positive),
making it wrong in both directions. The semantically-correct marker `sessionKind:"bg"` lives
in the transcript JSONL, not job state, and is genuinely racy at SessionStart because
transcript writes are async-flushed after the hook fires (#56631), so the only race-free
job-state-resident candidate is the currently-unread `backend:"daemon"` field. The biggest
open question is whether interactive sessions ever also carry `backend:"daemon"`
post-2.1.139 — if not, switching the guard from `template=="bg"` to `backend=="daemon"` is a
race-free fix; if so, the guard needs a combined signal and the reaper/master-switch must
absorb the residual false positives.
