---
schema: design/v1
status: Proposed
user_visible_surface: true
problem: |
  `niwa watch --once` polls exactly one source: GitHub pull requests where the
  developer is the directly-requested reviewer. The next source is Slack -- a
  thread where the developer is @mentioned and a teammate is waiting on an
  answer grounded in the workspace's repos. Slack does not fit the shipped path
  as-is: there is no platform-vouched "awaiting my reply" query to poll, the
  answer cannot be drafted without the thread's free text (which the PR path
  deliberately never lets near a dispatch), and the answer has to land back in
  the thread, which the shipped no-egress containment forbids and the PR path
  sidesteps by making the human post by hand.
decision: |
  Add Slack as a second `level` source on the same single-shot verb: `niwa watch
  --once` polls every configured source and `--slack` / `--github` scope-limit
  it. A workspace-level `[slack]` table binds a flat list of channel ids to the
  workspace; a deterministic pre-dispatch gate (bound channel, mention required,
  fail-closed author allowlist) admits a whole thread into a sticky watched set,
  after which every allowlisted follow-up re-fires. niwa materializes the thread
  as an inert JSON file inside the instance and dispatches a metadata-only
  prompt that points at it, so the dispatch decision stays free of
  externally-authored text exactly as the PR path is. The dispatched session
  drafts an answer and posts it through Slack's hosted MCP server; a PreToolUse
  guard (`niwa watch guard-egress`) keeps the egress seal shut for every channel
  except the enumerated Slack write tools, where it emits an `ask` that renders
  the literal payload for the operator in the agents view. No resident listener,
  no broker, no user-scope MCP.
rationale: |
  Answering a code question in Slack is a high-latency investigation started by
  a mention, not a real-time chat: a thread behaves like a pull request and new
  messages behave like new commits, so the shipped dedup, cursor, freshness,
  cap, and continuation machinery carries the source almost unchanged and no
  daemon is needed. Keeping the mention as the trigger keeps every pre-dispatch
  decision deterministic, so the trust boundary never rests on a model that the
  ingested text could itself influence. Materializing the thread as a file
  rather than interpolating it into the prompt preserves the shipped property
  that a crafted message can influence reasoning inside the sandbox but never
  what was dispatched. Gating the post with a PreToolUse `ask` on the one tool
  that needs to reach the network -- rather than building a token-holding broker
  outside the cage -- reuses the operator-approval posture that already ships,
  was validated hands-on against the real Slack MCP, and keeps exactly one tool
  carved through the seal instead of reopening egress.
---

# DESIGN: niwa watch Slack source

## Status

Proposed

This design covers the Slack skeleton feature (ED5) of the event-driven-dispatch
roadmap. The upstream roadmap assigns that feature a shirabe-primary design with
a niwa seam; this document takes the opposite split and explains why in
[Placement and Divergence from the Roadmap](#placement-and-divergence-from-the-roadmap).
It is the primary artifact; a small companion in shirabe carries the drafting
skill.

## Context and Problem Statement

### What ships today

`niwa watch --once` is a stateless single-shot verb. One pass polls GitHub for
open PRs where the developer is the directly-requested reviewer, intersects them
with the workspace's repos, decides per PR what to do, and for each new one
provisions an ephemeral instance, fetches the PR head as inert data, merges a
containment profile into the instance's `.claude/settings.json`, and launches a
detached agent that drafts a review and halts.

Four shipped properties carry the whole machine, and Slack has to fit inside
them or justify changing them:

- **The dispatch prompt carries no externally-authored text.**
  `BuildReviewPrompt` is a pure function of platform-vouched identifiers --
  owner, repo, number, URL. No title, no body, no diff, no author name. A
  crafted PR can influence reasoning inside the sandbox but can never influence
  *what was dispatched*. The diff reaches the agent as a checked-out clone, not
  as prompt text.
- **Containment is egress denial, not credential hiding.** The session keeps the
  developer's real HOME, environment, and credentials so it authenticates
  normally and registers in the agents view; every channel a secret could leave
  by is closed instead. The OS sandbox (`sandbox.enabled`, empty
  `allowedDomains`, `failIfUnavailable`, no unsandboxed escape hatch) cages Bash
  egress and Bash filesystem writes. Because that sandbox binds Bash
  subprocesses only, PreToolUse hooks close the rest: an egress-deny hook on
  `WebFetch|WebSearch|mcp__`, a filesystem guard on
  `Write|Edit|MultiEdit|NotebookEdit` delegating to `niwa watch guard-fs`, and
  `--strict-mcp-config` to unload the MCP fleet.
- **State is split by lifetime.** A permanent, SHA-aware handled-set
  (`.niwa/watch-handled`) records what was dispatched and at which head, and
  declares the source's trigger semantics in its header. A per-dispatch
  `StagedRecord` (`.niwa/watch/<handle>.json`) carries the instance path, the
  dispatched SHA, and the captured session ids; a GC prunes dead and stale
  records each pass. `Decide` is a pure function producing
  `Fresh`/`Noop`/`Defer`/`Continue`, a `StageBudget` composes a per-run bound
  with a cross-run staged-agent cap, and `Freshness` re-validates at unblock
  time so a resolved item self-discards.
- **Posting is a human act.** The agent writes `watch-review-draft.md` and
  stops. A Bash post-guard hook refuses a stray `gh pr review`/`gh pr comment`.
  The operator reads the draft and submits it from their own session. The
  operator-approval posture (a non-bypass permission mode plus niwa-seeded
  workspace trust, so a hook's `ask` decision is honored rather than silently
  allowed) ships for out-of-instance writes but is not yet used for posting.

### The gap

Slack is the second source. A teammate asks a code question in a channel and
@mentions the developer; the useful thing is a drafted, workspace-grounded
answer waiting when the developer next looks, which they read, steer, and
release into the thread.

The mapping onto the shipped machine is close enough that most of it is reuse:

| PR-review (shipped) | Slack (this design) |
|---|---|
| A PR awaiting my review | A thread where I am @mentioned, awaiting my reply |
| `user-review-requested` (given, deterministic) | the @mention (given, deterministic) |
| New commits pushed, re-fire | New messages in the thread, re-fire |
| Standing "awaiting" set, re-polled idempotently (**level**) | Standing "mentions awaiting reply" set (**level**) |
| Head SHA is the dedup key | Message `ts` is the dedup key |
| Diff read from the in-instance clone | Thread read from the in-instance thread file |
| Agent drafts, human posts via `gh` | Agent drafts, human releases the post via an approval |

Because the thread behaves like a pull request and new messages behave like new
commits, Slack is a **level** source, not an edge source: the same item
reappears on every pass until it is actioned, and re-polling is idempotent. The
reserved-but-unused `SemanticsEdge` path stays unused.

Three things genuinely do not carry over, and they are what this design has to
decide:

1. **There is no "awaiting my reply" query.** GitHub hands the watcher a
   platform-vouched set. Slack has no equivalent endpoint, so the set has to be
   *constructed* from a bounded scan plus durable cursors.
2. **The answer needs the thread's free text.** The PR path's
   injection-proof-dispatch property comes from never letting external text into
   the dispatch. A Slack answer cannot be drafted from metadata alone, so the
   ambient trust controls the roadmap calls "Bar B" -- a deterministic
   pre-dispatch admission gate, and structural instruction/data separation --
   become load-bearing here for the first time.
3. **The answer has to land in Slack.** "The human posts it themselves" is
   tolerable for a PR review sitting in a file the operator is about to open
   anyway; it is a bad answer for a chat thread, and it is the one place the
   no-egress boundary and the product's core gesture collide.

### Placement

Almost every mechanism this feature needs -- the poll, the admission gate, the
cursor state, the brief assembly, the channel configuration, the containment
carve-out, the dispatch -- is niwa's. The only genuinely model-bearing part is
the in-session skill that researches the workspace and drafts the answer. So the
primary artifact is this niwa design, and the companion is a small shirabe
skill. The roadmap says the reverse; see
[Placement and Divergence from the Roadmap](#placement-and-divergence-from-the-roadmap).

## Decision Drivers

- **D1 -- No resident process.** The toolkit's no-daemon identity holds.
  `watch --once` stays a stateless single-shot verb; Slack must be pollable.
- **D2 -- niwa stays model-free.** Every pre-dispatch decision -- poll, admit,
  assemble, dispatch -- is deterministic. No LLM call in the watcher.
- **D3 -- The trust boundary precedes any model.** An injectable judge cannot be
  the guard. Whatever admits content must be a deterministic filter that runs
  before a model sees anything.
- **D4 -- Externally-authored text must not influence what is dispatched.** The
  shipped metadata-only prompt is the property to preserve, not a detail to
  trade away. Instruction/data separation has to be structural, not a sentence
  in a prompt asking the model to behave.
- **D5 -- A release-to-act gate must show the literal payload and must survive
  `bypassPermissions`.** An approval that says "the agent wants to post
  something" is not a review. A gate that is silently allowed in the dispatch's
  permission mode is not a gate.
- **D6 -- The egress seal opens for one tool, not for a category.** Widening
  containment to "MCP is fine now" would retract the boundary the PR wedge
  proved.
- **D7 -- Watching and responding are co-configured.** A workspace that can
  watch Slack but cannot post back produces orphaned drafts, which is worse than
  not watching.
- **D8 -- Every re-fire spends an instance.** A dispatch is a provisioned
  instance plus a metered session. The watched set only grows, so cost bounds
  and a retirement path are part of v1, not a later refinement.
- **D9 -- The dispatch prompt argument is size-bounded.** A long thread cannot
  be an argument string.
- **D10 -- Per-dispatch instances are ephemeral.** Anything that Claude Code
  keys on the project path -- workspace trust, project-MCP enablement -- is new
  for every dispatch, so niwa has to seed it. A human cannot click a per-project
  prompt for a background session that was created a second ago.
- **D11 -- Reuse the shipped state contract.** The dedup/cursor split, the
  `Fresh`/`Noop`/`Defer`/`Continue` decision, the freshness predicate, the
  staged cap, and the record GC are proven. A second source should instantiate
  them, not fork them.

## Considered Options

### Decision 1 -- Where Slack lives in the CLI, and its trigger semantics

The watcher could grow a second verb (`niwa slack watch`) or a second source
behind the existing one. The choice sets whether the two sources share state,
caps, and a pass, or run as parallel machines that happen to look alike. It also
forces a declaration of trigger semantics: the shipped state header records
`level` or `edge` per source precisely so a second source is not silently forced
into PR coalescing.

#### Chosen: one verb, scope flags, Slack declares `level`

`niwa watch --once` polls **every configured source** in one pass. `--slack` and
`--github` are scope limiters, not separate verbs: `--once --slack` runs a
Slack-only pass, `--once` with neither runs both. One pass means one staged-agent
cap, one record GC, one inbox, and one cost budget across sources -- which is
what the operator actually experiences.

Slack declares `SemanticsLevel`. A thread is the item; the newest message `ts`
is the "head" the source re-fires on; intermediate messages coalesce into one
re-fire per pass. Dedup keys on `ts`, which is unique within a channel and
monotonically increasing, so it substitutes for the head SHA everywhere the
shipped machinery expects one. `ts` is stored verbatim as the string Slack
returns (the API requires the exact string back on `oldest`), but ordering
comparisons parse it as a decimal rather than comparing bytes, so a future
change in field width cannot silently reorder the watermark.

#### Alternatives Considered

**A separate `niwa slack watch` verb.** Cleanest to write, since neither source
constrains the other. Rejected on D11 and D8: two verbs means two caps and two
record stores, so nothing bounds the *combined* number of staged agents, which
is the number that actually costs money and floods the inbox. It also
duplicates the GC, freshness, and liveness sweeps that already exist.

**Declare Slack `edge`.** Superficially attractive because a chat message *feels*
like an event. Rejected because it is false and expensive: `conversations.history`
and `conversations.replies` are durable, re-pollable logs bounded by an `oldest`
cursor, so a poll is idempotent and nothing is lost by re-reading -- the cost of
polling a Slack source is latency, not missed messages. Declaring `edge` would
demand exactly-once admission machinery for a source that does not need it, and
would forgo the coalescing that turns a ten-message burst into one dispatch.

### Decision 2 -- What admits a thread, and what the deterministic gate checks

Something has to decide which Slack content is allowed to reach a session
running with the developer's authority. Per D3 that decision must be
deterministic and must precede any model. The roadmap's original framing put a
manufactured-relevance model at the front of this wedge -- "is this addressed to
me, is it actionable, which repos" -- which is precisely the shape D3 forbids
for the *safety* decision, whatever it may be worth for relevance.

#### Chosen: the mention is the trigger; a four-clause gate is the boundary

The @mention of the configured user id is the relevance signal, given and
deterministic, exactly as `user-review-requested` is on the PR side. There is no
pre-dispatch relevance model. Any "is this actually actionable" judgment happens
*inside* the dispatched session, where a model already runs, post-cage, and its
worst outcome is a wasted instance and a discarded draft.

The admission gate (the roadmap's Bar B) has four clauses, all deterministic,
all evaluated before anything is dispatched:

- **B1 -- bound channel.** The message's `channel` is in the workspace's
  configured channel list. Everything else is invisible to the watcher.
- **B2 -- mention required.** The message text contains the configured mention
  token (`<@Uxxxx>`). Checked by exact substring against a Slack-vouched id, not
  by name matching.
- **B3 -- author allowlist, fail-closed.** The message's `user` is in the
  configured allowlist. An empty or missing allowlist admits nothing. A message
  carrying `bot_id` is never a trigger.
- **B4 -- instruction/data separation.** The thread reaches the session as inert
  data, never as instruction text. This is Decision 4's mechanism, listed here
  because it is a clause of the gate, not a separate concern.

The mention **admits the whole thread**, not just the one message: the thread
(channel plus root `ts`) enters a sticky watched set. This is the true analogue
of a review request -- it admits an item once and does not have to repeat. What
happens on follow-ups is Decision 6.

#### Alternatives Considered

**Any message in a bound channel, filtered by a relevance model.** Higher
recall, and it is what an ambient assistant "should" do. Rejected on D3 and D8:
it puts a fallible, injectable model in the security path, and it dispatches
speculatively against channel chatter, where each false positive costs a real
instance and erodes trust in the whole inbox. A false negative merely reverts to
today's status quo -- the asymmetry argues for precision.

**Mention plus a channel-to-repo binding as the gate's scoping clause.** The
roadmap frames the binding as channel-to-repo, which would make B1 "this
channel's repos". Rejected as scoping: `niwa dispatch` materializes the entire
workspace instance regardless, so narrowing to a repo subset saves no
provisioning and only risks grounding the answer in too few repos. The workspace
is the relevance unit; grounding is decided in-session by an agent that can see
all of it. Per-repo hints stay available as a later precision knob if
whole-workspace grounding proves noisy on a large workspace.

**Relaxing B3 for follow-up messages inside an admitted thread.** Tempting,
because a thread is a conversation and non-allowlisted people join
conversations. Rejected: an unvouched party's free text would then enter a brief
on their own initiative, which reopens exactly the surface B3 closes.
Non-allowlisted messages remain *context* -- they are inlined with the rest of
the thread and the agent reads them -- but they are never *triggers*.

### Decision 3 -- How the "awaiting my reply" set is constructed

GitHub answers "which PRs await me" in one query. Slack has no equivalent
endpoint, so the watcher must build the set from a scan plus durable cursors,
and it must do so with a bot token (the poll identity), which cannot call
`search.messages`.

#### Chosen: a bounded channel scan plus a two-level cursor

Two pieces of durable state, mirroring the shipped split by lifetime:

- **A per-channel poll cursor** -- the `ts` of the newest message the channel
  was scanned through. It bounds the *discovery* query.
- **A per-thread dispatch watermark** -- the `ts` of the newest message the
  thread was last dispatched against. It is the direct analogue of
  `DispatchedSHA` and it drives the re-fire decision.

One pass, per configured channel:

1. **Discover new admissions.** `conversations.history` over the window
   `[now - lookback_days, now]`, which returns thread parents. For each parent
   whose own `ts` is newer than the poll cursor, evaluate B1-B3 against the
   parent. For each parent whose `latest_reply` is newer than the poll cursor,
   fetch `conversations.replies` and evaluate B1-B3 against each new reply. A
   qualifying message admits its thread (`thread_ts` when set, otherwise `ts`)
   into the watched set with an empty watermark.
2. **Poll watched threads.** For each thread already in the watched set,
   `conversations.replies(channel, ts, oldest=<watermark>)` returns everything
   new since the last dispatch.
3. **Advance the cursor** to the newest `ts` observed, and only after the pass
   completed without error. A failed poll is fail-loud and does not advance:
   a broken poll must never look like "nothing to answer".

The lookback window is what makes discovery bounded and is the one place this
construction is lossy: a mention posted as a reply to a thread whose *parent* is
older than `lookback_days` is not discovered, because `conversations.history`
keys on the parent's own `ts`. A thread that has already been admitted is
unaffected (step 2 has no lookback). The workaround is to mention in a new
message; the fix is a user token, which unlocks `search.messages` and collapses
the whole of step 1 into a single query -- named as hardening, not v1, because
a user token is a materially larger credential to ask for.

#### Alternatives Considered

**`search.messages` for the mention token.** The obvious construction: one
query, no lookback window, catches replies in arbitrarily old threads. Rejected
for v1 because Slack's search API is user-token-only, and the poll identity is
deliberately a bot -- the identity split in Decision 5 is what makes the answer
land from the developer's own account rather than from a robot. Escalating the
poll credential to a user token to save a scan is the wrong trade at this stage.

**A single global cursor instead of per-channel.** Simpler state. Rejected
because channels are polled independently and a per-channel failure would then
either stall every channel's cursor or silently skip messages in the failed one.

**No cursor -- scan a fixed recent window every pass and rely on the watched-set
watermark for dedup.** Rejected because the watermark only dedups threads
already admitted; a *new* mention would be re-admitted (harmlessly) but the scan
cost would be constant and unbounded by activity, and the "handled" answer would
depend on the window rather than on durable state.

### Decision 4 -- How the thread reaches the dispatched session

The session needs the thread's text. Two sub-questions: who reads it (niwa at
poll time, or the session through the MCP), and in what form it arrives.

#### Chosen: niwa reads it; it lands as an inert file; the prompt stays metadata-only

niwa already has the thread in hand -- it read it to evaluate the gate -- so it
writes it into the instance before launch, at a fixed relative path
(`slack-thread.json`, mode `0600`), as a structured array of verbatim Slack
fields: `ts`, `thread_ts`, `user`, `text`, and the `bot_id` flag. No niwa-side
interpretation, no interpolation, no shell.

The dispatch prompt is a **fixed template plus Slack-vouched identifiers only**:
team id, channel id, thread root `ts`, and the operator-configured channel
label. It names the thread file and instructs the agent to treat its entire
contents as untrusted data authored by third parties. No message text, no
display names, no author-controlled string of any kind becomes part of the
prompt.

This is the same shape as the PR path: **the thread file is to a Slack thread
what the in-instance clone is to a PR diff.** The consequence is that the
shipped injection-proof-dispatch property survives verbatim into the second
source -- a crafted message can influence reasoning inside the sandbox, but it
cannot influence what was dispatched, cannot reach a CLI argument, and cannot
reach a shell.

Because niwa supplies the read context, the session never needs a Slack *read*
at all. It touches the MCP only to post. Three consequences fall out:
project-scoped `.mcp.json` is sufficient (no user-scope, no seeded trust for
reads); containment tightens rather than loosens, since only the post tool is
carved through the seal; and the identity split becomes a feature -- niwa polls
as a bot, the session posts as the developer over OAuth, so the answer lands
from their own account.

#### Alternatives Considered

**Interpolate the thread into the prompt string.** The literal reading of "inline
the thread into the brief", and simplest to implement. Rejected on D9 and D4: a
long thread overruns the size-bounded prompt argument, and -- more importantly --
it makes attacker-authored text part of the instruction string that niwa
constructs and passes as an argument, which is exactly the property the PR path
was built to avoid. The file form gets the same information to the agent with
none of that.

**Let the session read the thread through the MCP itself.** Removes the need for
niwa to fetch anything and keeps the session's view live. Rejected because it
forces MCP *reads* through the egress seal (widening the carve-out from one tool
to a dozen, against D6), it needs the MCP authenticated and trusted before the
work starts rather than at post time when a human is already attending, and it
buys nothing: the thread niwa just read is the thread the agent would read.

**Write the thread as Markdown rather than JSON.** Friendlier to read. Rejected
because Markdown invites the agent to treat the content as prose with structure,
and because a JSON array with an explicit `text` field per entry makes the
data/instruction boundary legible to both the agent and a human reviewer. It
also keeps the `bot_id` and `user` fields available for the agent to reason
about provenance.

### Decision 5 -- How the answer gets posted

The answer has to land in the thread, the session is caged, and the operator
must see what is about to be said before it is said. The roadmap tracks this as
a deferred cross-cutting capability whose assumed shape is a token-holding
broker outside the cage.

#### Chosen: a PreToolUse `ask` on the Slack write tools

The dispatched session posts directly through Slack's hosted MCP server. A
PreToolUse hook interposes: a new `niwa watch guard-egress` subcommand, built on
the shipped `niwa watch guard-fs` precedent, replaces the inline egress-deny
command for Slack dispatches. It reads the hook payload on stdin and decides:

| Tool | Decision |
|---|---|
| `WebFetch`, `WebSearch` | deny (exit 2) |
| `mcp__slack__<tool>` where `<tool>` is in the write allowlist | `ask`, with the literal payload rendered in the reason |
| any other `mcp__*` | deny |
| unparseable input, missing posture flag | deny (fail closed) |

The operator sees a native approval in the agents view carrying the literal
tool arguments -- the target channel, the thread `ts`, and the message text --
and approves, declines, or steers. "Steer" is a resume of the session with
direction; the agent revises and re-surfaces. There is no broker, no second
token, and no new process.

Two things make this safe rather than merely convenient:

- **The `ask` must be honored, not silently allowed.** A hook's `ask` decision
  is inert under `bypassPermissions`. So a Slack dispatch runs in the shipped
  operator-approval posture: `permissions.defaultMode = "default"`, niwa-seeded
  workspace trust, and the auto-allow hook for the ordinary in-instance tools so
  the background session does not hang. niwa bakes the posture into the hook
  command as an explicit `--ask-post` flag, exactly as the filesystem guard
  takes `--ask-outside`. Without the flag the guard hard-denies. If the
  operator-approval posture cannot be established for an instance, the Slack
  dispatch **degrades to draft-only**: writes are hard-denied, the agent writes
  `slack-answer-draft.md`, and the operator is told the answer must be posted by
  hand. It never falls back to an ungated post.
- **`--strict-mcp-config` stays on.** Today the sandboxed dispatch passes
  `--strict-mcp-config` with no `--mcp-config`, which unloads the whole MCP
  fleet. A Slack dispatch passes `--mcp-config <instance>/.mcp.json
  --strict-mcp-config`, so strictness is preserved and exactly one server loads.
  The carve-out is one server and, within it, one enumerated set of tools.

The instruction the agent receives is posture-independent: always draft first,
write the draft to a known file, then attempt the post. In the enforced posture
the attempt surfaces the approval; in the uncontained posture the same
instruction holds by convention. What differs is enforcement, not what the agent
is told.

#### Alternatives Considered

**A token-holding broker outside the cage.** The roadmap's assumed shape: an MCP
server running outside the sandbox that holds the Slack token, accepts a
"post this" request from the caged agent, and performs it on the operator's
native approval. Rejected because it is strictly more machinery for the same
guarantee. The approval, the literal-payload rendering, and the fail-closed
behavior all come from the PreToolUse hook either way; the broker adds a second
credential to store, a second process to run, and a second thing to get wrong,
and it makes the answer post from a bot identity rather than the developer's.
The hook path also reuses a mechanism that already runs in production.

**Draft-only, operator copies the answer into Slack.** What the PR path does, and
it needs no new mechanism at all. Rejected as the *primary* path because the
copy-paste step is the whole ergonomic difference between a staged answer and a
useful one -- a review draft is read in a file the operator was going to open;
a chat answer is not. It is retained as the fail-closed degradation above.

**Post from niwa after the operator approves in a niwa-side prompt.** Keeps the
cage absolutely shut. Rejected because it puts niwa in the business of composing
and sending chat messages, and because the approval would then be a niwa CLI
prompt rather than the agents-view approval the operator is already using for
everything else -- two review surfaces for one inbox.

### Decision 6 -- The thread lifecycle: what re-fires, and what stops

A PR's watched set self-cleans: the PR merges and the item leaves. A thread has
no merge. Once a mention admits a thread, something has to decide when the
thread re-fires and when it stops being watched at all.

#### Chosen: every allowlisted follow-up re-fires; retirement is explicit

Admission happens once (Decision 2). After that, **every new message in a
watched thread re-fires a dispatch with no new mention required** -- B2 is
relaxed *within* an admitted thread, because the thread is the admitted unit and
a teammate answering a clarifying question should not have to re-mention.

The re-fire gate keeps three conditions:

- at least one message with `ts` greater than the thread's dispatch watermark;
- from an **allowlisted** author (B3 holds -- see Decision 2's third
  alternative);
- **not our own posted answer.**

That last clause needs care, because the answer posts as the developer's own
identity (Decision 4's identity split) and the developer is also an allowlisted
trigger author -- so "exclude messages authored by us" would also exclude the
developer's legitimate in-thread steering, which is a feature we want. Instead a
**PostToolUse companion hook** (`niwa watch record-slack-post`) records the `ts`
of each posted answer into the thread's record, and the re-fire gate excludes
recorded `ts` values specifically. Messages carrying `bot_id` are excluded
unconditionally.

If that record ever misses -- the hook fails, the session dies between the post
and the record -- the failure is benign rather than a loop: the next pass
re-fires once, the agent sees a thread whose last message is its own answer with
no new question, no-ops, posts nothing, and therefore produces no new message to
re-trigger on. The design depends on that no-op instruction being in the skill
(see [Companion Artifact](#companion-artifact-the-shirabe-drafting-skill)), and
it is why the skill's no-op behavior is a contract, not a nicety.

New messages coalesce: one re-fire per pass sees all of them, which is the
payoff of declaring `level`.

The per-thread verdict reuses the shipped `PlanKind`:

- **Fresh** -- never dispatched, or new messages and no live session. Consumes
  the stage budget.
- **Noop** -- no new messages past the watermark.
- **Defer** -- the thread's session is running right now, or the operator is
  attached to it. Transient only: skip this pass, pick it up next. It never
  yanks a session out from under a human and it self-clears.
- **Continue** -- a live, detached-idle session is resumed at the new messages
  instead of dispatching fresh. This is **purely a cost optimization** for
  Slack. The shipped continuation limitation (resume works once per session,
  then degrades to Defer) does not gate this source: because niwa inlines the
  full thread on every dispatch, a fresh dispatch always has complete context,
  so an ineligible Continue falls through to Fresh rather than dropping a
  follow-up.

**Superseding.** If an un-released draft for a thread is still staged when new
messages arrive, the stale one is stopped and a fresh dispatch runs against the
updated thread. Resuming instead is the Continue optimization, not a
requirement.

**Freshness at release.** Before a draft is released, the thread head is
re-checked. If it resolved -- someone else answered, the asker withdrew -- the
draft self-discards, reusing the shipped freshness pattern.

**Retirement.** A watched thread is never dropped implicitly. v1 ships:

- `niwa watch slack-ignore <channel>/<thread_ts>` -- marks the thread retired in
  the watched set. Deterministic, one verb, no new surface.
- `slack_thread_max_dispatches` (default 20) -- a per-thread lifetime dispatch
  counter; past it the thread auto-retires with a notice on the pass output.
  This is the backstop against one chatty thread consuming instances forever.
- `slack_max_watched_threads` (default 200) -- when the watched set is full, new
  admissions are **refused loudly** rather than evicting an existing thread.
  Silent eviction would drop a live conversation; a loud refusal tells the
  operator to retire something.

Retirement by Slack-side gesture -- an agreed emoji reaction on the thread root,
or a discard in the agents view -- is deferred, and is the natural next
increment.

#### Alternatives Considered

**Re-fire only on a *new* mention.** Much cheaper, and it makes every dispatch
explicitly requested. Rejected because it breaks the analogy that makes the
whole wedge work: new commits do not require a new review request, and a
teammate replying "no, I meant the retry path" in the thread they already
mentioned you in should not have to mention you again. It would make the second
turn of every conversation fail silently.

**Auto-retire a thread after N days of inactivity.** Standard, and it bounds the
set without operator action. Rejected for v1 because a slow thread is a normal
thread -- an answer arriving a week later is still the product working -- and a
time-based drop is exactly the silent failure the loud refusal above avoids. The
dispatch-count cap bounds cost without guessing at conversation tempo.

**Let the watched set grow unbounded and rely on the staged-agent cap.** The
staged cap does bound concurrent cost, which is the immediate risk. Rejected on
D8 because it does not bound the *scan*: every watched thread costs a
`conversations.replies` call on every pass forever, so the pass itself degrades
over months even when nothing dispatches.

### Decision 7 -- Where the channel binding and credentials live

Channels are net-new workspace surface: repos enter a workspace through
`[[sources]]` and per-repo override tables, not a first-class member array. The
binding has to live somewhere discoverable, portable, and validatable, and the
poll credential has to live somewhere that is none of those things (it is a
secret and it is machine-local).

#### Chosen: binding in `workspace.toml`, credential through the existing secret path

A flat, workspace-level `[slack]` table:

```toml
[slack]
team_id       = "T0123456789"
mention       = "U0123456789"
channels      = ["C0123456789", "C0987654321"]
allow_from    = ["U0AAAAAAAA", "U0BBBBBBBB"]
lookback_days = 14
```

Every value is a Slack-vouched id or an integer -- no free text, no names --
and each is charset-validated on load like any other workspace field
(`^T[A-Z0-9]{6,}$`, `^[UW][A-Z0-9]{6,}$`, `^[CG][A-Z0-9]{6,}$`). `allow_from`
missing or empty admits nothing.

The bot token resolves through the same shape `resolveGitHubToken` already uses:
the `SLACK_BOT_TOKEN` environment variable first, then a workspace secret
binding (`vault:` reference). A plaintext token in `workspace.toml` is rejected
at load, not merely discouraged. A workspace that already keeps chat credentials
in a host-local chat-bridge config points the vault binding at that.

**The V9 precondition.** `niwa watch` polls Slack only if the workspace
distributes a Slack MCP server -- that is, a `.mcp.json` reaching the instance
root through `[instance.files]`. The presence of the Slack MCP is what makes the
source *answerable*, so it gates whether the source is *watched*. This
co-configures watching with responding: a workspace cannot end up staging drafts
it has no way to post. It also makes the workspace configuration a single source
of truth -- distributing the Slack `.mcp.json` is how a workspace opts into the
wedge. When the `[slack]` table is present but no Slack MCP is configured, the
pass reports the mismatch and skips the source rather than failing the whole
run.

#### Alternatives Considered

**Put the binding in the host-local chat-bridge config next to the credential.**
The status-quo pull: that file already holds chat configuration
(mention-required and author-allowlist primitives among them), so the binding
would sit beside prior art. Rejected because it is the wrong scope. That file is
machine-local, host-keyed, and secret-bearing; a workspace-portable binding
belongs in the workspace declaration, where it is discoverable, reviewable, and
travels with the workspace to another machine. Its correct role here is the
credential, which is exactly what stays there.

**Model channels as first-class workspace members with per-channel tables.**
More expressive -- per-channel allowlists, per-channel repo hints. Rejected as
premature: nothing in v1 varies per channel, and a flat list of ids is trivially
validated. Per-channel structure can be added later without breaking a flat
list.

**Infer the channel list from the Slack MCP's visible channels.** Zero
configuration. Rejected outright: the channel list is the outer boundary of the
admission gate. Deriving the security boundary from the ambient capability of a
token means the boundary changes when someone adds the bot to a channel.

### Decision 8 -- How the dispatched instance gets to use the project MCP server

A project-scoped `.mcp.json` is not usable by a session until the project is
trusted and the server is enabled for that project -- state Claude Code keys on
the project path in the user-global `~/.claude.json`. Every dispatch creates a
brand-new instance directory (D10), so that state is absent every time, and a
background session cannot wait for a human to clear a prompt for a directory
that appeared a second ago.

#### Chosen: extend the shipped trust seeding

`EnsureInstanceTrusted` already writes `projects[<abs>].hasTrustDialogAccepted`
and `hasTrustDialogHooksAccepted` for the ephemeral instance, atomically,
preserving every other key, and `RemoveInstanceTrust` removes the entry when the
instance is reclaimed. This design adds `enabledMcpjsonServers: ["slack"]` to
the same entry, written and removed on the same lifecycle. It is one more field
in a store niwa already owns for exactly one path.

The OAuth credential itself is user-global and per-machine, established once by
an interactive `/mcp` authentication, and niwa does not manage it. `niwa watch`
preflights it: if the Slack source is configured but the MCP is not
authenticated, the pass says so and skips the source with an actionable message
rather than staging drafts that cannot be posted.

#### Alternatives Considered

**Promote the Slack MCP to user scope.** Then every session has it, and no
per-instance enablement is needed. Rejected because it grants every Claude Code
session on the machine a Slack tool, including sessions that have nothing to do
with this workspace, in exchange for saving one field in a file niwa already
writes. Decision 4's read/post split is what made user scope unnecessary; taking
it anyway would give that back.

**Add a `[claude.settings]` passthrough for arbitrary `settings.json` keys.**
Would let the workspace declare project-MCP enablement declaratively. Rejected
as both insufficient and too broad: insufficient because the relevant state
lives in the user-global `~/.claude.json` keyed by instance path, not in the
instance's `settings.json`; too broad because an arbitrary-key passthrough is a
much larger surface than this feature needs, and it would let a workspace
declare settings that contradict the containment profile niwa fully owns.

**Have the operator authenticate once per instance.** Honest, and it puts a
human in the loop. Rejected on D10: instances are created per dispatch and are
reclaimed; this would put a manual step in front of every single answer.

## Decision Outcome

**Chosen: one verb with a second `level` source, a mention-triggered
deterministic gate over a workspace-level channel list, niwa-materialized thread
context, and an `ask`-gated post through the project-scoped Slack MCP.**

### Summary

`niwa watch --once` grows a second source. A workspace opts in by declaring a
`[slack]` table -- team id, the mention user id, a flat list of channel ids, a
fail-closed author allowlist, and a lookback window -- and by distributing a
Slack MCP `.mcp.json` to its instance roots. Either half alone is a reported
misconfiguration, not a partial feature: without the MCP the watcher would stage
answers it cannot post, so it skips the source and says so. `--slack` and
`--github` scope-limit a pass; with neither, one pass polls both sources against
one shared staged-agent cap, one record GC, and one inbox.

A pass over Slack does three things. It **discovers** new admissions by scanning
each bound channel's `conversations.history` over the lookback window and, for
any thread with activity newer than the channel's poll cursor, its
`conversations.replies`; a message that is in a bound channel, contains the
mention token, and comes from an allowlisted human author admits its whole
thread into a sticky watched set keyed by channel plus root `ts`. It **polls**
every watched thread for messages past that thread's dispatch watermark. And it
**decides**, per thread, one of the shipped four verdicts: Fresh when there are
new allowlisted messages and no live session, Noop when there are none, Defer
when the thread's session is busy or a human is attached to it, Continue when a
detached-idle session can be cheaply resumed instead. Only Fresh consumes the
stage budget. Cursors advance only on a clean pass; a failed poll is fail-loud
and never looks like an empty inbox.

Staging a thread provisions an instance, writes the thread into it as
`slack-thread.json` at mode `0600` -- a verbatim array of `ts`, `thread_ts`,
`user`, `text`, and `bot_id`, with no niwa-side interpretation -- and launches a
detached agent with a prompt built only from Slack-vouched identifiers plus the
operator-configured channel label. No message text and no display name ever
enters the prompt, so the dispatch decision stays exactly as
injection-proof as the PR path's: the thread file is to a Slack thread what the
in-instance clone is to a PR diff.

The session drafts an answer, writes it to `slack-answer-draft.md`, and then
attempts to post it through the Slack MCP. In the enforced posture the OS
no-egress sandbox and the filesystem guard are unchanged, but the inline
egress-deny hook is replaced by `niwa watch guard-egress --ask-post`, which
denies `WebFetch`, `WebSearch`, and every MCP tool except an enumerated set of
Slack write tools, where it emits `ask` with the literal channel, thread, and
message text in the reason. The dispatch passes `--mcp-config
<instance>/.mcp.json --strict-mcp-config`, so exactly one server loads.
`permissions.defaultMode` is `"default"` and niwa seeds
`hasTrustDialogAccepted`, `hasTrustDialogHooksAccepted`, and
`enabledMcpjsonServers: ["slack"]` for the instance path, all removed when the
instance is reclaimed. If that posture cannot be established, the dispatch
degrades to draft-only with writes hard-denied and says so; it never posts
ungated. A `PostToolUse` companion records the posted message `ts` so the answer
does not re-trigger its own thread.

Threads are watched until they are retired. Retirement is explicit: a
`niwa watch slack-ignore` verb, a per-thread lifetime dispatch cap (default 20)
that auto-retires with a notice, and a watched-set ceiling (default 200) that
refuses new admissions loudly rather than evicting silently.

### Rationale

The decisions reinforce each other around one property: **every step before a
model runs is deterministic and reuses something already proven.** The mention
being the trigger is what lets the gate be four boolean clauses instead of a
judgment. niwa reading the thread is what lets the prompt stay metadata-only,
which is what preserves the injection-proof-dispatch property, *and* what
reduces the MCP carve-out from "reads and writes" to "one write tool", *and*
what makes project scope sufficient so no user-scope credential is needed. The
level declaration is what lets a burst of messages coalesce into one dispatch
and what lets the shipped dedup/cursor/freshness/cap machinery be instantiated
rather than forked.

The trade-offs accepted are real. The lookback window makes discovery lossy for
mentions buried in replies to old threads, which is the price of keeping the
poll credential a bot token rather than escalating to a user token for search.
Watching threads forever makes cost bounds load-bearing sooner than they were
for PRs, which is why retirement is in v1 rather than deferred. And the release
gate depends on the operator-approval posture being establishable, which is why
its failure mode is a loud degradation to draft-only rather than a silent
fallback.

## Solution Architecture

### Components

New, in `internal/slack`:

- **`client.go`** -- a minimal Slack Web API client: `ConversationsHistory`,
  `ConversationsReplies`, `AuthTest`. Bot-token only. Returns typed structs with
  no free-text field ever used for a decision other than the B2 mention
  substring check.
- **`ids.go`** -- charset validation for team, channel, and user ids, and for
  the `ts` format. Every id crossing into state, a path component, or a CLI
  argument passes through here first.

New, in `internal/watch`:

- **`slack_state.go`** -- the watched-thread set and the per-channel poll
  cursors. The watched set is `.niwa/watch-slack-threads`, one line per thread,
  `<team>/<channel>@<thread_ts>#<watermark>[!retired]`, with the same
  `# niwa-watch-state v2 semantics=level` header and the same skip-malformed
  tolerance the handled-set has. Cursors are `.niwa/watch-slack-cursors`, one
  `<channel>=<ts>` line each. Both written atomically via temp-file-and-rename.
- **`slack_select.go`** -- `DecideSlack`, a pure function from (threads, watched
  state, live map, continuable map, latest-`ts` map, bound) to `[]SlackPlan`,
  reusing `PlanKind`. Table-testable exactly as `Decide` is.
- **`slack_gate.go`** -- the B1-B3 admission predicate and the re-fire predicate,
  both pure functions over Slack-vouched fields plus the configured lists.
- **`slack_prompt.go`** -- `BuildSlackPrompt`, a pure function of identifiers and
  a fixed template, and `WriteThreadFile`, which materializes `slack-thread.json`
  into the instance.
- **`slack_containment.go`** -- the Slack variant of the containment profile:
  the same sandbox stanza, the same filesystem guard, the same post-guard, but
  `guard-egress` in place of the inline egress-deny, and the MCP config
  passthrough.

New, in `internal/cli`:

- **`watch_guard_egress.go`** -- the `niwa watch guard-egress` subcommand. Reads
  a PreToolUse payload on stdin; emits `deny`, or `ask` with a rendered payload
  when `--ask-post` is set and the tool is in the write allowlist. Fails closed
  on any parse error, missing field, or unrecognized tool.
- **`watch_record_slack_post.go`** -- `niwa watch record-slack-post`, the
  PostToolUse companion that appends a posted message `ts` to the thread record.
- **`watch_slack_ignore.go`** -- `niwa watch slack-ignore <channel>/<thread_ts>`.

Extended:

- **`internal/config`** -- the `[slack]` table, its validation, and the
  `SLACK_BOT_TOKEN`-then-vault credential resolution.
- **`internal/watch/trust.go`** -- `enabledMcpjsonServers` alongside the existing
  trust keys, added and removed on the same lifecycle.
- **`internal/cli/watch.go`** -- source dispatch (`--slack` / `--github`), the
  Slack branch of the pass, and the shared budget across sources.

### Data flow, one `watch --once` pass with Slack enabled

```
preflight
  |- resolve sandbox posture + staged cap        (shipped)
  |- resolve [slack] config + bot token          (new)
  '- verify Slack MCP distributed + authenticated (new; skip source if not)

poll
  |- GitHub: SearchReviewRequestedPRs            (shipped)
  '- Slack, per bound channel:                   (new)
       conversations.history(oldest = now - lookback)
         -> parents with ts > cursor        -> gate B1-B3 -> admit thread
         -> parents with latest_reply > cursor
              -> conversations.replies      -> gate B1-B3 -> admit thread
       conversations.replies(watched thread, oldest = watermark)
         -> new messages for the re-fire decision

GC + liveness                                    (shipped, now over both stores)
  '- prune dead/stale records; classify live and continuable sessions

decide
  |- Decide(...)      -> []Plan        (shipped)
  '- DecideSlack(...) -> []SlackPlan   (new; same PlanKind, shared StageBudget)

apply, per Fresh Slack plan
  |- provision instance                          (shipped)
  |- write slack-thread.json (0600)              (new)
  |- seed trust + enabledMcpjsonServers          (extended)
  |- apply Slack containment profile             (new variant)
  |- launch detached, metadata-only prompt,      (shipped launcher)
  |    --mcp-config <instance>/.mcp.json --strict-mcp-config
  |- capture session ids                         (shipped)
  '- save thread record; advance watermark       (new, mirrors handled-set)

in-session
  research -> draft to slack-answer-draft.md -> attempt post
     '- PreToolUse guard-egress --ask-post -> operator approval (literal payload)
          approve -> posted; PostToolUse records the message ts
          decline -> draft remains; operator steers or discards

cursors advance only if the pass completed clean
```

### The containment profile, side by side

| Control | PR dispatch (shipped) | Slack dispatch (this design) |
|---|---|---|
| OS sandbox | enabled, empty `allowedDomains`, `failIfUnavailable`, no escape hatch | unchanged |
| `WebFetch`/`WebSearch` | denied by inline hook | denied by `guard-egress` |
| MCP tools | all denied by inline hook | all denied by `guard-egress` except the Slack write allowlist, which `ask`s |
| MCP loading | `--strict-mcp-config`, no config named | `--mcp-config <instance>/.mcp.json --strict-mcp-config` |
| Filesystem escape | `guard-fs`, ask or hard-deny | unchanged |
| Bash post-guard | refuses `gh pr review`/`comment` | unchanged |
| Permission mode | `default` in the ask posture, else inherited bypass | `default` **required**; no ask posture means draft-only |
| Project trust | `hasTrustDialogAccepted`, `hasTrustDialogHooksAccepted` | plus `enabledMcpjsonServers: ["slack"]` |

### The Slack write-tool allowlist

The guard is **default-deny**: an MCP tool that is not on the allowlist is
denied, so an unenumerated write tool fails closed rather than passing
unreviewed. The allowlist therefore determines which writes are *possible*, not
which are *blocked*, and getting it wrong is a usability bug, not a security
hole.

The hosted server's read tools are known verbatim from the validation rig:
`slack_read_channel`, `slack_read_thread`, `slack_read_canvas`,
`slack_read_file`, `slack_read_user_profile`, `slack_get_reactions`,
`slack_list_channel_members`, `slack_search_channels`, `slack_search_public`,
`slack_search_public_and_private`, `slack_search_users`, `slack_search_emojis`.
All twelve are denied for a Slack dispatch -- the session has no reason to read
(Decision 4).

The write side is documented by capability rather than by identifier. Slack's
documentation names: send a message, draft messages, create a
conversation/channel, add reactions, create a canvas, update a canvas.
Third-party catalogues give literal names for three of these --
`slack_send_message`, `slack_add_reaction`, `slack_create_canvas` -- consistent
with the `slack_<verb>_<noun>` convention the confirmed read names follow, but
this has not been verified against the live server.

So v1 allowlists exactly one tool, **`slack_send_message`**, which is the only
write the wedge needs, and the build carries an explicit verification step:
enumerate the server's tools in a live session and record the actual write
identifiers in the guard's table. Reactions become relevant only when the
deferred emoji-based retirement lands; canvases are out of scope. Anything not
on the list is denied and reported, so a wrong guess surfaces as a visible,
harmless failure on the first attempt.

## Implementation Approach

Five batches, sequenced so each is independently useful and the risky parts land
against a working thread rather than in the abstract.

**Batch 1 -- Configuration and the source seam.** The `[slack]` table with
validation and credential resolution, the `--slack`/`--github` scope flags, the
V9 precondition check, and a pass that polls Slack and *prints what it would
dispatch* without dispatching. Ends with a verb that proves the poll, the
cursors, and the gate against a real workspace with zero blast radius. This
batch is where the admission gate gets its adversarial tests -- crafted mention
tokens, non-allowlisted authors, bot messages, out-of-band channels -- because
the gate is a pure function and can be tested exhaustively before anything is
dispatched.

**Batch 2 -- State and the decision.** The watched-thread set, the poll cursors,
`DecideSlack`, the re-fire predicate, and the shared stage budget. Table-driven
tests mirroring the shipped `Decide` tests. Still no dispatch: the pass reports
verdicts.

**Batch 3 -- Dispatch, contained, draft-only.** Instance provisioning, the
thread file, the metadata-only prompt, the Slack containment profile with
`guard-egress` in **hard-deny mode** (no `--ask-post`), and the thread record.
At the end of this batch the wedge works end to end except that the answer is a
file the operator posts by hand -- which is exactly the PR path's behavior, and
a defensible shipping point on its own.

**Batch 4 -- The release gate.** `--ask-post`, the payload rendering, the
operator-approval posture requirement with its draft-only degradation, the
`enabledMcpjsonServers` seeding, the MCP config passthrough, and the
`PostToolUse` record. This is sequenced last among the mechanism batches because
it is the only one that can post to a third party, and because Batch 3 gives it
a working thread to be verified against.

**Batch 5 -- Bounds and retirement.** `slack-ignore`, the per-thread dispatch
cap, the watched-set ceiling, and the pass-output reporting for each. Small, but
in v1 rather than deferred, because the watched set is accumulate-only from the
moment Batch 3 ships.

The companion shirabe skill is developed against Batch 3 and required by Batch
4.

## Security Considerations

### The threat

This source routes text authored by third parties into a session running with
the developer's authority -- their credentials, their cloned repos, their
dispatch. Anyone who can post in a watched channel can attempt to route
instructions into that session, and because dispatch is proactive the session is
staged before the human looks. This is the ambient free-text surface the roadmap
defers behind the PR wedge precisely so it is built with the PR wedge's
enforcement already proven.

### Attack vectors and mitigations

**Injecting into the dispatch decision.** *Mitigated structurally.* No
externally-authored text reaches the prompt, a CLI argument, a shell, or any
state file. The prompt is a fixed template over Slack-vouched ids plus one
operator-configured label; the thread is a `0600` JSON file written by trusted
code before the session starts. Message text is used for exactly one decision --
the B2 substring test for the mention token -- and that test is against a
Slack-vouched id, not a name. Every id that becomes a path component, a state
line, or an argument is charset-validated first, following the shipped
`isSafeHandle`/`isHexSHA` pattern.

**Injecting into the session's reasoning.** *Accepted and contained.* A crafted
message can influence what the agent thinks, exactly as a crafted PR diff can
today. The containment is what bounds the consequence: no Bash egress, no
filesystem writes outside the instance, no `WebFetch`/`WebSearch`, no MCP tool
but one, and that one gated by a human-reviewed approval showing the literal
payload. The blast radius of a fully-successful injection is "the drafted answer
says something wrong, and the operator reads it before it is sent".

**Getting an unvouched party's text into a brief.** *Mitigated by B3 holding on
re-fire.* Only allowlisted authors trigger. Non-allowlisted messages are
included as context in the thread file -- the agent must see the conversation --
but they never cause a dispatch. Without this, anyone who could post in a
watched thread could initiate dispatches at will.

**Escalating a read into a post.** *Mitigated by default-deny.* The guard
allowlists tool names; everything else is denied. The session has no read tools
at all, so there is no "read then write" chain to walk.

**Posting without review.** *Mitigated by posture, with a fail-closed
degradation.* A hook's `ask` is inert under `bypassPermissions`, so the guard
emits `ask` only when niwa baked `--ask-post` into the hook command, and niwa
only does that when it established the non-bypass operator-approval posture. If
the posture cannot be established -- HOME unresolvable, the instance under
`~/.claude`, trust not seedable -- the dispatch runs draft-only with writes
hard-denied. There is no path where a failure to establish the gate results in
an ungated post.

**Answer loops.** *Mitigated, and benign when the mitigation misses.* The
posted-`ts` record excludes our own answer from the re-fire gate; if the record
misses, one extra dispatch occurs, the agent no-ops on a thread with no new
question, nothing is posted, and no further re-fire is generated.

**Credential exposure.** *Unchanged from the shipped posture.* The session keeps
the developer's real environment, and the boundary is egress denial rather than
credential hiding. Two Slack-specific credentials enter the picture: the poll
bot token, which lives only in niwa's process and never in the instance or the
prompt; and the MCP OAuth credential, which is user-global, managed by Claude
Code, and never read or written by niwa.

**Supply chain.** The Slack MCP server is Slack's own hosted service reached over
HTTPS; the `.mcp.json` distributed to instances carries a public OAuth client id
and no secret. niwa adds no new binary and no new dependency for the poll beyond
the Slack Web API over HTTPS.

**Denial of service and cost.** *Bounded.* The shared staged-agent cap bounds
concurrent instances across both sources; the per-run bound bounds a single
pass; the per-thread dispatch cap bounds a chatty thread's lifetime cost; the
watched-set ceiling bounds the scan. An allowlisted author can still burn
instances by posting repeatedly in a watched thread -- they are, by
construction, trusted not to.

### Validation of the release-to-act gate

The load-bearing mechanism -- that a PreToolUse hook on an MCP tool surfaces a
native approval rendering the literal arguments -- was **validated hands-on
against the real Slack hosted MCP server** during the upstream exploration,
using a secret-free rig with zero write risk: the hook hard-denied every write
and turned one *read* into an `ask`, so nothing could be posted by the test. The
rig confirmed four things:

1. a dispatched session is interactive and attachable, so a human can clear its
   prompts;
2. MCP tool calls do fire PreToolUse hooks;
3. the native approval renders the literal tool arguments (observed:
   `slack - Read channel messages(channel_id: "C0BC7GFBFS4", limit: 10)`);
4. the hook can additionally curate its own payload preview in the reason
   string, so a post gate can render a clean "about to post to #X: ..." above
   the raw arguments.

The rig is reproducible from the exploration record (a minimal `workspace.toml`
distributing Slack's official hosted `.mcp.json` via `[instance.files]`, plus the
deny-writes hook) and does not need to be re-derived here; the live rig was torn
down deliberately.

**One residual to re-verify during Batch 4.** The rig observed an `ask` prompting
in a workspace configured for bypass permissions, whereas the PR wedge's earlier
finding was that an in-cage `ask` is fail-open under `bypassPermissions` -- which
is why the operator-approval posture was built. The likely explanation is that
the rig's session was interactive rather than a `--bg` dispatch. This design does
not rely on the more favorable reading: it *requires* the non-bypass posture and
hard-denies without it. The residual is worth closing empirically anyway, because
if `ask` genuinely holds under bypass the draft-only degradation could be
relaxed.

## Consequences

### Positive

- The second source costs far less than the first. The dedup/cursor split, the
  four-verdict decision, the freshness predicate, the staged cap, the record GC,
  the containment profile, the launcher, and the session-id capture are all
  instantiated rather than rebuilt.
- The no-daemon identity holds unchanged. No resident listener, no socket, no
  new process; `watch --once` stays a stateless single-shot verb.
- The injection-proof-dispatch property extends to a free-text source. This was
  the open question the roadmap flagged for this feature, and it is answered
  structurally rather than by prompt discipline.
- Containment gets *tighter* in the process. Naming the MCP config explicitly
  means the Slack dispatch loads exactly one server, where the PR dispatch
  relies on `--strict-mcp-config` with nothing named.
- The release-to-act gate lands without the broker the roadmap budgeted for, and
  the answer posts from the developer's own account rather than a bot's.
- The staged answer is one gesture from being sent, which is the ergonomic
  difference between a staged draft and a useful one.

### Negative and risks

- **Discovery is lossy at the edge.** A mention in a reply to a thread older than
  `lookback_days` is not found. *Mitigation:* the window is configurable, already-
  admitted threads are unaffected, and the failure is visible to the person
  mentioning (no answer appears) rather than silent to the operator. A user token
  and `search.messages` removes the limitation entirely and is the named next
  step.
- **The watched set is accumulate-only.** Every watched thread costs a
  `conversations.replies` call per pass, forever. *Mitigation:* the explicit
  `slack-ignore` verb, the per-thread dispatch cap, and the loud watched-set
  ceiling, all in v1. A Slack-side dismiss gesture is the deferred improvement.
- **Every follow-up spends an instance.** Even a "thanks!" in a watched thread
  triggers a dispatch that the agent will no-op on. *Mitigation:* the caps above
  bound it; the human gate keeps the inbox clean. This is a real cost the
  re-fire-on-every-message choice buys the conversational behavior with, and it
  is the most likely thing to need tuning after real use.
- **The release gate depends on a posture that can fail to establish.**
  *Mitigation:* the failure is loud and degrades to draft-only, never to an
  ungated post. But an operator on a machine where the posture cannot be
  established gets the PR path's ergonomics, not the Slack path's.
- **The write-tool allowlist is not fully verified.** *Mitigation:* the guard is
  default-deny, so the consequence of an incomplete list is a blocked post with a
  clear message, and Batch 4 carries an explicit live-enumeration step.
- **A trusted author can burn budget.** B3 is a trust boundary, not a rate limit.
  *Mitigation:* the caps bound the damage; the allowlist is small and
  operator-curated by construction.
- **niwa grows a Slack API client.** New surface to maintain against a third-party
  API. *Mitigation:* three endpoints, bot-token only, no SDK dependency.

### Residuals to close during implementation

- Verify the literal Slack write-tool identifiers against the live server and
  record them in the guard's table (Batch 4).
- Re-verify whether a PreToolUse `ask` is honored in a `--bg` dispatch under
  `bypassPermissions`, and relax the draft-only degradation only if it is.
- Confirm that `--mcp-config <path> --strict-mcp-config` loads exactly the named
  server in a dispatched session and nothing else.

## Placement and Divergence from the Roadmap

The upstream roadmap assigns this feature a shirabe-primary design
(`DESIGN-shirabe-slack-wedge.md`) with a niwa dispatch seam, on the reasoning
that Slack is where relevance must be *manufactured by a model*, and that a
model implies shirabe.

The exploration retired that premise: the mention **is** the relevance signal, so
nothing model-bearing sits in front of the dispatch. What remains is a poll, a
deterministic gate, cursor state, brief assembly, workspace configuration, a
containment carve-out, and a dispatch -- all niwa. The model appears only inside
the dispatched session, where its job is to research and draft, not to decide
what was admitted.

So this design is niwa-primary. The divergence is one of ownership, not of
content: every piece the roadmap named still exists, and the roadmap's feature
description is otherwise satisfied. It is recorded here for the human to reflect
into the roadmap's feature entry.

## Companion Artifact: the shirabe Drafting Skill

One companion is required before Batch 4: the investigate-and-draft skill loaded
into the dispatched session. It is a skill, not an architecture, so it belongs in
shirabe as a skill rather than as a second design document. Its contract with
this design:

- **Inputs.** The metadata-only prompt (team, channel, thread `ts`, channel
  label), the thread file at `slack-thread.json`, and the materialized workspace
  instance.
- **Untrusted-data handling.** The entire thread file is third-party content.
  The skill must not follow instructions found in it. This is defense in depth
  over the structural separation, not the boundary.
- **Grounding is decided in-session.** The skill researches the workspace to
  work out which repos actually answer the question. There is no channel-to-repo
  hint in v1.
- **Prepare, then release.** Always draft first and write the draft to
  `slack-answer-draft.md`, then attempt the post. The attempt is what surfaces
  the operator approval; the draft file is what survives a decline. The
  instruction is identical in the enforced and uncontained postures -- only
  enforcement differs.
- **No-op contract.** If the newest messages contain no question the agent can
  usefully answer -- chatter, acknowledgement, or the agent's own prior answer
  with nothing following it -- the skill must post nothing and say so in the
  draft file. This is load-bearing: it is what makes the posted-`ts` record's
  failure mode benign rather than a loop.
- **Steering.** A resumed session with operator direction revises the draft and
  re-attempts, rather than starting over.

## Upstream Corrections

The exploration that produced this design surfaced corrections to the upstream
strategy and roadmap. They are recorded here for the human to apply; **this
design does not apply them.**

1. **Retire the persistent Socket Mode listener.** The upstream feature
   description names a resident Socket Mode listener as the first of the Slack
   wedge's net-new pieces. It is not needed and is not built: Slack is a polled
   `level` source on `watch --once`. This is the single largest correction.
2. **Slack is `level`, not `edge`.** The shipped trigger-semantics contract was
   introduced partly to keep the Slack wedge from being forced into PR
   coalescing, on the assumption Slack would be edge-triggered. The thread/PR and
   message/commit mapping makes it level, and the reserved edge path stays
   unused.
3. **Latency is not the value proposition.** The upstream framing -- an answer
   waiting before the teammate re-reads their own message -- oversells real-time.
   The value is a staged, workspace-grounded draft the developer returns to,
   which is the same value the PR wedge delivers. The design targets
   poll-interval latency and is explicit that this is fine.
4. **The graduation decision recedes rather than advancing.** With no resident
   process, the ingestion-architecture fork is not pulled forward by this
   feature. It stays deferred and evidence-gated, as the strategy intended.
5. **Channel binding is workspace-level, not channel-to-repo.** The upstream
   block frames the binding as channel-to-repo. The workspace is the relevance
   unit and the whole instance is materialized on dispatch, so per-repo narrowing
   saves nothing; it becomes an optional later precision knob.
6. **Correct the chat-bridge-config premise.** The upstream text treats the
   existing host-local chat-bridge config as already holding the channel binding.
   It has no repo-binding field, and it is machine-local and secret-bearing. Its
   real role in this feature is the poll credential.
7. **Release-to-act is solved for this wedge.** The strategy lists it as a
   deferred cross-cutting capability whose shape is an out-of-cage broker. For
   Slack it is a PreToolUse `ask` hook on the write tools, validated hands-on,
   with no broker. The read-side half of that capability (safe review context for
   a caged PR reviewer) remains open and unaffected.

A supporting correction, less load-bearing than the seven above: polling
`conversations.history` does not *lose* messages -- it is a durable log bounded
by an `oldest` cursor -- so the cost of a polled Slack source is latency, not
missed events.
