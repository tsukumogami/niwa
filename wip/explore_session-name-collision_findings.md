# Exploration Findings: session-name-collision

## Core Question

`niwa dispatch --name <slug>` sanitizes the user-supplied name and forwards it to
the launched agent as the session's display name. Nothing makes that slug unique,
yet the name is also the address other Claude Code sessions use to reach the
worker. Is that true of the current code, what actually happens on a send to a
colliding name, and is it worth fixing given the readability cost of a suffix?

## Round 1

Seven leads. Six ran as research agents against the repo and the documentation; the
seventh was run by the orchestrator directly, because it required calling
`ListAgents` from inside a live dispatched session.

### Verification of the incoming claims

The task brief carried five claims from a prior research pass. Re-checked against
`d436832`:

| Claim | Verdict |
|---|---|
| `--name` sanitized to `[a-z0-9_]`, capped at 40 runes | **Confirmed.** `sanitizeInstanceSlug`, `internal/cli/dispatch.go:910-929`; cap `maxDispatchSlugRunes = 40` at `:62`. Detail added: disallowed runs collapse to a *single* `_`, not one per rune, and leading/trailing `_` are trimmed. |
| 8 hex of uniqueness generated per dispatch, spent on the instance directory | **Confirmed.** `dispatchNameSuffix`, `internal/cli/dispatch.go:879-888`, `crypto/rand`, 4 bytes. Joined into `<config>+<slug>-<8hex>` at `internal/cli/create.go:84`. |
| The bare slug is what reaches the agent's display-name flag | **Confirmed, twice.** Statically at `internal/cli/dispatch.go:985` via the Claude row at `internal/agentplan/dispatch.go:356`, giving `claude --bg --name <slug>`. And first-hand: this session, dispatched as `--name session-name-collision`, reports its own name as `session_name_collision` while running in `tsuku+session_name_collision-21dbe5c8`. |
| Nothing makes the session name unique | **Confirmed** by closed enumeration: `dispatchName` is read exactly once in non-test code (`:458`), `sanitizeInstanceSlug` is pure, `slug` is not reassigned between `:458` and `:564`, and `buildDispatchPassthrough` never sees `namePrefix`. |
| Ambiguous sends resolve "latest wins", deterministically and silently | **Not confirmed — corrected.** No Claude Code documentation states any resolution rule for a programmatic bare-name send. The behavior is *unspecified*, not deterministic. |

Two claims gained important qualifiers:

- **The bug is Claude-only.** Codex's launch spec declares no display-name flag
  (`internal/agentplan/dispatch.go:414-421`), so `buildDispatchPassthrough`'s
  empty-flag guard (`:986`) drops the pair. A Codex dispatch gets no display name
  at all.
- **The design doc asserts the safety that does not hold.**
  `docs/designs/current/DESIGN-instance-dispatch.md:172-174` says the scheme "stays
  collision-safe even when two dispatches share a `--name`". That is true of the
  directory and silently false of the session name the same section forwards.

### Key Insights

- **The collision is real, and the fix is nine lines away.** (lead-slug-path) The
  uniqueness already exists in `namePrefix` at `internal/cli/dispatch.go:459`;
  `buildDispatchPassthrough` is called at `:564` with `slug` instead. The path from
  flag to exec mutates nothing in between.

- **It is not hypothetical — it is happening now, at scale.** (lead-live-peer-listing)
  A live `ListAgents` call from this session returned 115 peers in which **seven
  distinct names are shared by fifteen sessions**, one name held three ways, and one
  collision involving a live idle background session. Independently,
  (lead-collision-detection) found the job store on this machine holds two sessions
  dispatched under the same `--name`, one of which a human renamed by hand to
  `legacy-…` — a string niwa's sanitizer cannot produce, since it strips dashes.
  Two independent lines of evidence that this has already cost someone manual work.

- **The display name is write-only inside niwa, so nothing breaks.** (lead-name-dependencies)
  Every durable correlation keys on the session UUID (`internal/workspace/session_map.go:114-119`),
  the instance directory path (`internal/cli/dispatch_capture.go:44-48`), or the
  agent's own record handle. The reaper's backstop matches the directory regex
  (`internal/cli/reap.go:595`). No niwa code path reads the display name back.

- **The architectural guardrails do not obstruct the fix.** (lead-layout-constraints)
  The dispatch-path AST scan forbids exactly two things across nine files: the
  `AgentClaude`/`AgentCodex` constants under any import binding, and eleven
  whole-value string literals. Flag spellings are explicitly outside it —
  `internal/cli/dispatch.go:552` already ships a flag-spelling gate in a scanned
  file. A suffix on a flag *value* needs no capability row and no predicate.

- **But the fix belongs at the call site, not in the builder.** (lead-name-dependencies,
  lead-layout-constraints, lead-name-readability, converging independently)
  `buildDispatchPassthrough` has three callers, two of them in `niwa watch`
  (`internal/cli/watch.go:576`, `:836`), where the slug doubles as a staged-record
  *filename* and where a resumed review deliberately re-forwards a stable name.
  `internal/cli/dispatch_wiring_remotecontrol_test.go:127` compares argv byte-for-byte
  against a second call to the builder. So the suffix goes in `runDispatch`, between
  `:459` and `:564`.

- **The readability objection is much weaker than assumed, and misdirected.**
  (lead-name-readability) niwa never displays a dispatched session's name anywhere —
  `niwa dispatch` prints the session UUID and the instance path; `list`, `status` and
  `reap` print *instance* names, which have been `<config>+<slug>-<8hex>` (up to 55
  characters, tab-completed into `niwa destroy`) since dispatch shipped, with no
  recorded complaint. The delta under debate is 9 characters on one Agent View row
  this repo has never measured and does not own.

- **The prior justification is reusable almost verbatim.** (lead-name-readability)
  `DESIGN-instance-dispatch.md:170-174` already reasoned about two dispatches sharing
  a `--name` and concluded the random token is what preserves collision safety.
  Extending that sentence from the directory to the session name applies a shipped
  decision rather than making a new one.

- **The harness's own disambiguator is 6 hex.** (lead-live-peer-listing) Every
  `ListAgents` row carries a `[6 hex]` reference id unconditionally — this session's
  is `[07483a]`. niwa's instance names use 8. Nobody in the repo has noticed the
  neighbouring convention.

### Tensions

- **Detection looked viable, then the listing gutted it.**
  `lead-collision-detection` established that niwa *can* check for a same-name live
  peer at dispatch time by reading `~/.claude/jobs/<id>/state.json`, which carries
  `name` verbatim, machine-wide across workspaces, at a measured 0.4 ms. That is a
  genuinely cheap and correct machine-local check. But the live listing shows roughly
  **100 of 115 peers are off-machine** Remote Control sessions. A machine-local check
  would have seen about 15 of them. So a "no collision detected" result is close to
  meaningless, and printing it is worse than printing nothing because it reads as an
  all-clear. The two leads do not contradict each other on facts; the later one
  reframes what the facts are worth.

- **Detect-and-auto-disambiguate is the trap.** Suffixing only the second `review`
  keeps the common case readable, but it is a probabilistic guarantee dressed as a
  deterministic one: a reader seeing `review` next to `review-4e33acfa` would
  reasonably infer the plain one is unique when it may not be. It also races —
  the new worker's job entry appears *after* niwa's check, so two simultaneous
  dispatches both pass. That is the same TOCTOU that pushed instance naming to
  unique-by-construction in the first place (`DESIGN-instance-dispatch.md:150-158`).

- **Reuse the instance's 8 hex, or match the harness's 6?** Reuse makes the session
  name and the instance directory legible as a pair and needs no new entropy, but
  `dispatchNameSuffix` currently returns only the concatenated string and discards
  the token, so its signature would have to change. Six matches the neighbouring
  convention and is two characters cheaper. Real trade-off, evidence both sides.

- **niwa is one writer into a namespace it does not own.** Several listed peers carry
  names with spaces and capitals — shapes `sanitizeInstanceSlug` cannot emit. niwa
  cannot enforce global uniqueness even in principle. The achievable goal is narrower:
  never *be the source* of a collision it created itself.

### Gaps

- **What a bare-name send to an ambiguous name actually does remains unresolved.**
  Documentation is silent (lead-peer-addressing); a live probe was deliberately not
  run, because testing it means sending a real message to a real colliding session
  belonging to unrelated work. There is a reported issue (anthropics/claude-code#42999)
  that name-based `SendMessage` can fail silently and only an id works, which if
  current would make the failure mode "message never arrives" rather than "message
  arrives at the wrong worker". Either way the remedy is the same, which is why this
  gap does not block the decision.
- **Agent View row width and truncation are unmeasured.** If the row clips short, a
  9-character tail on a 40-rune slug is invisible-and-useless rather than merely ugly,
  which would argue for a shorter suffix.
- **`state.json`'s `name` field is undocumented harness internals.** Every niwa reader
  fails safe on a missing field, so a rename degrades a check to silence rather than a
  wrong answer — but this matters only for options that read it.

### Adjacent problems surfaced, not in scope

Three separate collisions of the same family turned up. None is the assigned topic;
all deserve recording so they are not rediscovered:

1. **`niwa watch` collides by construction.** Its slug is
   `sanitizeInstanceSlug(fmt.Sprintf("watch-%s-%s-%d", …))` at `internal/cli/watch.go:781`,
   deterministic from the PR coordinates, forwarded as the display name at `:836`. Two
   review sessions for the same PR reuse the identical name *and* the identical staged-record
   filename. Worse than dispatch's coincidental collision, but narrower blast radius, and
   its slug doubles as a human-typeable `--handle` (`internal/cli/watch_check.go:65`), so
   suffixing costs that property.
2. **The `/dispatch` skill overwrites brief files.** Briefs land at
   `.niwa/dispatch-briefs/<slug>.md`; two same-slug dispatches silently overwrite. One layer
   above niwa, unfixed by any session-name change.
3. **The no-name case stays anonymous.** `internal/cli/dispatch_test.go:742-820` pins
   "empty slug means no `--name` forwarded at all", so unnamed dispatches remain mutually
   unaddressable. Changing that is a deliberate product call, not a side effect.

### Decisions

Recorded in `wip/explore_session-name-collision_decisions.md`.

### User Focus

This exploration ran in `--auto` mode as a dispatched background worker, so the
narrowing at each decision point followed the research-first protocol rather than a
user's answer. The task brief supplied the scope boundary in advance: the
peer-message pre-approval flag, `crossSessionInbound`, permission modes, and any
broader redesign of niwa's messaging or dispatch model are out.

## Accumulated Understanding

The reported bug is real, and the evidence for it is stronger than the report claimed
on every axis except one.

`niwa dispatch` reads `--name` once, sanitizes it, and forks the value: the instance
directory gets `<slug>-<8hex>` and the session gets the bare slug. Confirmed statically
by tracing every line between the flag and the exec, and confirmed first-hand by this
very session reading back its own name. The one claim that did *not* survive is the
severity mechanism: "latest wins" appears nowhere in Claude Code's documentation. The
resolution of an ambiguous bare-name send is unspecified, which is arguably worse than
a documented rule — nothing can be relied upon, and a reported issue suggests such sends
may fail silently rather than misdeliver.

What the report could not have known is that the collision is already occurring. A live
peer listing from inside this session shows seven names shared by fifteen sessions out of
115, one of them three-deep, one involving a live background peer; and the machine's job
store holds a same-name pair where a human manually renamed one to tell them apart. This
moves the question from "is this worth fixing?" to "why has it not been fixed?".

The option space collapsed over the course of the round. Detection-and-warn looked viable
at 0.4 ms of machine-local lookup, then the listing showed roughly 100 of 115 peers are
off-machine, making a clean check nearly meaningless and an all-clear actively misleading.
Detect-and-auto-suffix is worse: it races, and it makes an unsuffixed name look like a
guarantee. What is left is unique-by-construction — the property the instance directory
already has, justified in a design document that already reasons about two dispatches
sharing a `--name`, applied to the value nine lines away that never received it.

The change is small and unusually well-fenced. The AST guardrails do not touch it, because
a suffix on a flag value is neither an agent constant nor a denied literal. No niwa consumer
reads the display name back, so nothing downstream breaks. The one real constraint is
placement: `buildDispatchPassthrough` is shared with `niwa watch`, whose slug is
simultaneously a filename and deliberately stable across resumes, so the suffix must be
applied by the dispatch caller in `runDispatch` between `:459` and `:564` — a constraint
four independent leads arrived at separately.

What remains genuinely open is not whether to fix it but three bounded design choices: the
suffix width (reuse the instance's 8 hex for pair-legibility, or match the harness's own
6-hex row id), whether `niwa dispatch` should now print the session name so the address is
discoverable at all — today it prints only the UUID and the path — and whether `niwa watch`'s
two call sites and the unnamed-dispatch case ride along or are filed separately. Those are
requirements-and-approach questions with real trade-offs and no forced answer, which is
precisely the shape that belongs in a scoping chain rather than in a bare issue.

## Decision: Crystallize
