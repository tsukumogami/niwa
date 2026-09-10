# /scope Handoff: session-name-collision

## Provenance

Written by `/explore` on 2026-09-09 from
`wip/explore_session-name-collision_crystallize.md`.

Research files: `wip/explore_session-name-collision_findings.md`,
`wip/explore_session-name-collision_decisions.md`, and
`wip/research/explore_session-name-collision_r1_lead-*.md` (seven leads).

One discover-converge round. Six leads ran as research agents against the repo and
Claude Code's documentation; the seventh was run by the orchestrator directly,
because it required calling `ListAgents` from inside a live dispatched session and
no subagent could do that on its behalf. The round narrowed a four-way approach
space to one survivor and moved the problem from reported to observed. A second
round was not run: the one gap that remains is unresolvable without sending a real
message into a colliding session belonging to unrelated work, and every plausible
answer to it points at the same remedy.

## Problem Statement

`niwa dispatch --name <slug>` sanitizes the user-supplied name and forwards the bare
slug to the launched Claude Code worker as its session display name, while spending
the 8 hex digits of uniqueness it generates on the instance directory instead. In
Claude Code the display name is the address peers use to reach a session, and that
namespace is global across background, interactive, local and Remote Control
sessions. So two dispatches that reuse a `--name` produce two live sessions sharing
one address, and a message meant for one resolves against both by a rule nothing
documents.

This is not hypothetical. A live peer listing taken from inside a dispatched session
returned 115 peers in which seven distinct names are shared by fifteen sessions — one
name held three ways, and one collision involving a live idle background session.
The machine's own job store holds a same-name pair where a human manually renamed one
to `legacy-…` to tell them apart, a string niwa's sanitizer cannot produce. The bug
has already cost someone manual work.

## Scope Boundary

### In scope

- Making the display name `niwa dispatch` forwards unique by construction, so niwa is
  never the source of a collision it created.
- Choosing the suffix's width and derivation, and whether it reuses the instance
  directory's existing token.
- Whether `niwa dispatch` should print the resulting session name, given that it
  currently prints only the session UUID and the instance path.
- Correcting `docs/designs/current/DESIGN-instance-dispatch.md:172-174`, which asserts
  the collision safety that does not hold for session names.

### Out of scope

- The peer-message pre-approval flag, `crossSessionInbound`, permission modes, and
  approval prompts. A sibling session owns that work; this exploration was told to
  leave it alone.
- Any broader redesign of niwa's messaging or dispatch model.
- Enforcing global uniqueness across the namespace. niwa is one writer into a
  namespace it neither owns nor can see — several listed peers carry names with spaces
  and capitals, shapes niwa's sanitizer cannot emit. The achievable goal is narrower.

### Adjacent, deliberately excluded — but each needs its own home

Three collisions of the same family surfaced. None is this topic; all should be filed
rather than rediscovered.

- **`niwa watch` collides by construction.** Its slug is deterministic from the PR
  coordinates (`internal/cli/watch.go:781`) and forwarded as the display name at
  `:836`, so two review sessions for the same PR reuse the identical name *and* the
  identical staged-record filename. Worse than dispatch's coincidental collision, but
  narrower, and its slug doubles as a human-typeable `--handle`
  (`internal/cli/watch_check.go:65`), so suffixing costs that property.
- **The `/dispatch` skill overwrites brief files.** Briefs land at
  `.niwa/dispatch-briefs/<slug>.md`; two same-slug dispatches silently overwrite. One
  layer above niwa, unfixed by any session-name change.
- **Unnamed dispatches stay mutually unaddressable.**
  `internal/cli/dispatch_test.go:742-820` pins "empty slug means no `--name` forwarded
  at all". Changing that is a deliberate product call with two tests to rewrite.

## Decisions Already Settled

From `wip/explore_session-name-collision_decisions.md`. Settled inputs to the chain's
conversation, not verdicts it must accept unexamined.

- **"Latest wins" is dropped as the severity mechanism.** No Claude Code documentation
  states any resolution rule for a programmatic bare-name send to an ambiguous name.
  Describe the failure as unspecified resolution, possibly silent non-delivery — not
  as deterministic misdelivery to the newer session. A reported issue
  (anthropics/claude-code#42999) suggests name-based sends may fail silently and only
  an id works.
- **The bug is worth fixing, on observed evidence rather than argument.** The brief
  asked for the case to be argued either way; "acceptable as-is" loses to two
  independent observations of it having already happened.
- **Detect-and-warn is eliminated.** Technically implementable at ~0.4 ms by reading
  `~/.claude/jobs/<id>/state.json`, which carries `name` verbatim, machine-wide across
  workspaces. But roughly 100 of the 115 observed peers are off-machine Remote Control
  sessions, so a clean check proves almost nothing and reads as a false all-clear.
  Rejected because a misleading signal is worse than no signal.
- **Detect-and-auto-disambiguate is eliminated.** It races — the new worker's job entry
  appears after niwa's check, so two simultaneous dispatches both pass — and it makes
  an unsuffixed name look like a uniqueness guarantee it is not. Same TOCTOU that
  pushed instance naming to unique-by-construction in
  `DESIGN-instance-dispatch.md:150-158`.
- **Unique-by-construction is the surviving approach.** The only remedy niwa can
  deliver against a namespace it can neither see nor lock, and it reapplies a decision
  the repo already made and documented for the instance directory.
- **The suffix belongs in `runDispatch`, between `internal/cli/dispatch.go:459` and
  `:564`** — not inside `buildDispatchPassthrough`, whose other two callers are
  `niwa watch` (`internal/cli/watch.go:576`, `:836`), where the slug doubles as a
  staged-record filename and is deliberately stable across resumes, and against which
  `internal/cli/dispatch_wiring_remotecontrol_test.go:127` compares argv byte-for-byte.
  Four leads reached this independently.
- **No capability row is warranted.** The per-agent difference is already expressed as
  `LaunchFlags.DisplayName` being empty for Codex
  (`internal/agentplan/dispatch.go:414-421`); adding a row would trip the
  declaration-coverage test into demanding declarations for a fact the flag table
  already carries.
- **The architectural guardrails do not obstruct the change.** The dispatch-path AST
  scan forbids exactly two things across nine files: the `AgentClaude`/`AgentCodex`
  constants under any import binding, and eleven whole-value string literals. Flag
  spellings are explicitly outside it, and `internal/cli/dispatch.go:552` already ships
  a flag-spelling gate in a scanned file.
- **The suffix width was left open on purpose**, not defaulted. See Shape Signals.
- **Private identifiers are redacted from all committed artifacts.** The job-store dump
  and the peer listing contain session and workspace names from non-public projects;
  this is a public repo, so those were replaced with neutral placeholders and the
  substitution noted in-file. Counts, structure, and reasoning are unchanged.

## Coverage Notes

What the exploration did not answer, and the chain should.

- **What a bare-name send to an ambiguous name actually does.** Documentation is silent
  on the programmatic case; the user-facing @-mention path is documented to prompt the
  human for disambiguation, but `SendMessage` is not. The live probe was deliberately
  not run, because it means sending a real message into a colliding session belonging
  to unrelated work. This bounds how strongly the PRD can state the consequence — write
  the requirement against "unspecified resolution", which is defensible, rather than
  against a specific misdelivery story, which is not.
- **Whether Agent View truncates the row, and at what width.** Unmeasured anywhere in
  this repo, and this repo does not own that surface. It matters only for the width
  choice: if the row clips short, a 9-character tail on a 40-rune slug is
  invisible-and-useless rather than merely ugly, which argues for a shorter suffix and
  possibly for a separate cap on the session-name side. Someone with Agent View open
  can settle it in seconds.
- **Whether `state.json`'s `name` field is stable across Claude Code versions.**
  Undocumented harness internals. Every niwa reader fails safe on a missing field, so a
  rename degrades to silence rather than a wrong answer — but this only matters if a
  design revisits detection, which the decisions above ruled out.
- **Whether the `--label` flag should absorb the human-readable role.** `dispatchLabel`
  is recorded on the mapping (`internal/cli/dispatch.go:769`) and read by nothing — a
  shipped, documented, entirely unused channel for a freeform alias. Whether it becomes
  the clean human surface while the name carries the suffix, or gets removed as dead,
  was noticed but not decided.

## Upstream Observations

Paths below are observations. No `--upstream` flag accompanies the command: `/scope`
accepts a ROADMAP, and `docs/roadmaps/` does not exist in this repo.

- **`docs/prds/PRD-instance-dispatch.md` (status: Done)** is the closest upstream, and
  it contains the precedent argument almost verbatim. **R11** requires the command to
  "name each instance from a freshly-generated unique token determined before creation,
  so that two simultaneous dispatches cannot resolve to the same instance name or
  directory." The session name is the same hazard one clause away, and it never got the
  same requirement. A new requirement for the session name is an incremental
  application of R11's own reasoning, not a new argument.
- **The same PRD's R3** bears directly on the open print-the-name question: the command
  "SHALL report, to stdout, the launched session's identifier and how to manage it
  (attach/inspect/stop), so the developer can reach the session in Agent View or from
  the terminal." If the display name is how a developer reaches the session in Agent
  View, R3 arguably already reaches it, and today's output — session UUID plus instance
  path — may already be short of it. Worth testing that reading rather than treating
  printing the name as net-new scope.
- **`docs/designs/current/DESIGN-instance-dispatch.md:170-174`** already reasoned about
  two dispatches sharing a `--name` and concluded that keeping the random token is what
  preserves collision safety. The sentence at `:172-174` — "concurrency stays
  collision-safe even when two dispatches share a `--name`" — is true of the directory
  and silently false of the session name the same section forwards. It reads as
  reassurance and is actually the bug. Whatever lands should correct it, or the next
  reader re-derives the same false confidence.
- **`docs/briefs/BRIEF-instance-dispatch.md` (status: Done)** frames dispatch as a
  whole. This topic is a bounded defect inside that feature, not a re-framing of it, so
  it wants its own BRIEF rather than an amendment to that one.
- **`docs/prds/PRD-cross-session-communication.md` (status: Done)** describes niwa's
  pre-pivot agent-facing mesh, which was removed wholesale in niwa PR #151/#154 — see
  `docs/designs/current/DESIGN-niwa-mesh-removal.md`. It is not a live upstream here:
  the addressing this topic concerns is Claude Code's, not niwa's.
- **Open issue #292** reports that `.niwa/sessions/` is written UUID-named and read
  8-hex-named, so `niwa worktree destroy` cannot resolve any session record. Different
  bug, adjacent store. It touches the same mapping file this topic's detection option
  would have read, so a design that revisits detection should check whether #292 has
  landed first.

## Framing-Shift Answer

**Pre-supplied answer:** no signal surfaced.

**Evidence:** there is no settled BRIEF for this topic to shift — this is a new bounded
defect inside the already-delivered dispatch feature, and `BRIEF-instance-dispatch.md`
frames dispatch as a whole rather than its naming contract. Nothing in round 1 changed
the target audience, the problem shape, or the success criterion of any existing
artifact.

One correction travelled with the exploration and should not be mistaken for a framing
shift: the incoming report described the consequence as deterministic "latest wins"
misdelivery, and the research established the resolution is undocumented instead. That
changes how the consequence is *stated*, not what the problem *is* — and the offsetting
discovery, that seven collisions are standing in a live 115-peer listing, moved the
evidence base from hypothetical to observed without moving the framing.

## Shape Signals

### Architectural alternatives left open

- **Reuse the instance's 8-hex token versus generating a fresh 6-hex one.** Reuse makes
  the session name and the instance directory legible as a pair, which has real
  operational value when correlating Agent View against `niwa list`, and adds no new
  entropy — but `dispatchNameSuffix` (`internal/cli/dispatch.go:879-888`) currently
  returns only the concatenated string and discards the token, so its signature has to
  change or the caller has to re-parse its own output. A fresh 6-hex matches Claude
  Code's own row disambiguator exactly — every `ListAgents` row carries a `[6 hex]` ref,
  this session's is `[07483a]` — and costs two characters less, but breaks the pairing.
  Nothing in the repo argues for 8 specifically on readability grounds; 8 was chosen for
  the instance name because it doubles as a structural signature matched by
  `dispatchInstanceNameRe` (`internal/cli/dispatch.go:941`), which the session name does
  not need to be.
- **Print the session name from `niwa dispatch`, or leave the address undiscoverable.**
  Today the command prints the session UUID and the instance path, and niwa never
  displays a dispatched session's name anywhere in the codebase. Adding a suffix without
  adding a line of output makes the address genuinely harder to find — a legitimate
  objection to that proposal specifically. One `Fprintf` near
  `internal/cli/dispatch.go:806-807`. Whether this is inside this feature or a separate
  gap that predates it is a scope call, and R3 above is the argument that it is not
  net-new.
- **Include `niwa watch`'s two call sites, or file them separately.** Its collision is
  by construction rather than coincidence, so it is arguably more urgent; but its slug
  doubles as a human-typeable handle and as a staged-record filename, so the fix there
  is not the same fix and carries a cost dispatch's does not. Deciding this determines
  whether the change touches one call site or three.
- **Whether a 40-rune slug plus suffix needs its own cap.** `maxDispatchSlugRunes = 40`
  (`internal/cli/dispatch.go:62`) was chosen for filesystem-name headroom, and the
  display name inherits it only because it is the same string. A suffixed name reaches
  49 characters. Nothing in niwa truncates a name; whether the consuming surface does is
  the unmeasured gap above.

### Complexity signals

- **The implementation is small and unusually well-fenced.** One expression change at a
  known line, with the uniqueness already in scope nine lines above it. The AST
  guardrails do not reach it, no capability row is needed, and the test cost is roughly
  one line (`internal/cli/dispatch_test.go:737`).
- **The design weight is entirely in the parameters, not the mechanism.** Four open
  choices above, each with evidence on both sides and none forced. That asymmetry —
  trivial code, contested parameters — is the reason this is a scoping problem rather
  than an issue.
- **The blast radius is verified small.** Every durable correlation in niwa keys on the
  session UUID (`internal/workspace/session_map.go:114-119`), the instance directory
  path (`internal/cli/dispatch_capture.go:44-48`), or the agent's own record handle; the
  reaper's backstop matches the directory regex (`internal/cli/reap.go:595`). The
  display name is write-only inside niwa — no code path reads it back — so a suffix
  breaks no runtime consumer. That was checked exhaustively rather than assumed.
- **The change is Claude-only by declaration.** Codex dispatches receive no display name
  at all (`internal/agentplan/dispatch.go:414-421`), so any requirement must not assume
  every harness has a name to make unique. It will need re-examining if the Codex spec
  ever grows a display-name flag.
- **A sibling session's work depends on the outcome.** Its peer-message pre-approval flag
  would turn a misdelivery that currently surfaces at a human approval prompt into a
  silent one. That exploration treats this as arguably a prerequisite, which is a
  sequencing input the chain should weigh even though the flag itself is out of scope
  here.
