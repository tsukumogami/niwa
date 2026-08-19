---
schema: design/v1
status: Current
upstream: docs/prds/PRD-codex-background-dispatch.md
problem: |
  The PRD requires niwa dispatch delivered to Codex across four surfaces --
  launch, capture, resume, liveness -- through one agent seam: the gate a
  declaration lookup, one session-identity representation, no agent
  constants at dispatch-path call sites, and a first PR that provably
  changes no behavior. The prior dual-agent attempt stated this exact
  diagnosis in its own design and still shipped agent-specific behavior as
  a parallel hardcoded pass, because the intended structure was never
  something a test could fail on. This design owns the launch description's
  shape and location, the launch-route binding, the capture mechanism, the
  structural scan, the liveness rule, and the measured Codex launch
  mechanics.
decision: |
  The per-agent launch description is data in internal/agentplan: a
  LaunchSpec table beside the declarations it answers for, bound by a
  two-direction drift check plus a completeness check, read through
  Producer. internal/cli consumes it -- the gate, the preflight, the argv,
  the capture, the printed hints, and the resume all come from the
  declaration -- and a new AST scan over the dispatch-path files holds that
  no file there names an agent. Capture reads the agent's own on-disk
  session record through one reader over a declared record shape,
  exercised against two differently-shaped stores; the durable mapping
  records its agent; the reaper spares any mapping whose liveness it
  cannot read. Codex launches via start-and-release with setsid, stdin at
  /dev/null, stdout and stderr to separate niwa-owned files,
  --skip-git-repo-check always, and its sandbox posture from a
  per-invocation trust override that leaves the developer's own Codex
  configuration byte-identical -- never a --sandbox flag, never a
  persisted trust stanza.
rationale: |
  Every structural decision here names the test that fails when its claim
  stops being true, because the predecessor failed precisely by lacking
  one. The launch table binds beside its declaration because nothing in
  internal/workspace launches anything, and registering a fake delivery
  there so the existing binding had something to check would recreate the
  agrees-with-itself problem that table exists to prevent. The rollout
  file beats the event stream because liveness needs the on-disk store
  anyway; the stream would mean two Codex-side stores read by two
  mechanisms where Claude reads one for both. Measured codex-cli 0.147.0
  behavior -- the stdin hang, the trust-stanza side effect of --sandbox,
  the exit-0-on-failed-writes result -- dictates the launch shape and the
  reporting posture rather than leaving either to taste.
---

# DESIGN: codex background dispatch

## Status

Current

This design owns the mechanism for delivering `niwa dispatch` to Codex:
where the per-agent launch description lives and what it carries, how the
launch route binds to the declaration table, the capture mechanism and its
record-shape vocabulary, the structural scan over the dispatch path,
session identity and the liveness rule, the measured Codex launch shape,
and the sequencing proof. The upstream PRD owns the requirements (R1-R21,
N1-N2) and does not get re-opened here. This document is a sibling of
DESIGN-agent-capability-contract.md: that design built the declaration
table and bound the plan and procedure routes; this one binds the third
route, `RouteLaunch`, whose row 22 was declared and read by nothing.

One thing distinguishes this document from an ordinary design: its first
PR is already merged on this branch (commit 5a7b4c4). Decisions 1 through
7 and 11 describe code that exists and tests that run; where a decision
was demonstrated by a failing test, the failure was reproduced against
this tree while this document was written, not remembered from a plan.
Decisions 8 through 10 describe PR 2, constrained throughout by
measurement against codex-cli 0.147.0.

## Context and Problem Statement

`niwa dispatch` creates a fresh ephemeral instance, launches a background
worker rooted in it, captures which session the worker became, writes a
durable session mapping, and steps the terminal back in unless detached.
Before this branch, every step of that flow reached Claude by name: the
gate at step (2b) compared the resolved agent against a constant, the
preflight looked up the literal `"claude"`, capture polled Claude's jobs
directory, the model resolver kept per-agent tables nothing else read, and
the reclamation sweep decided an ephemeral instance's fate by checking
that same jobs directory.

Row 22 of the capability table declared the refusal as niwa's own debt --
`DispatchLaunch` for Codex, unavailable, not-built -- and no code read the
row. `internal/cli` did not import `internal/agentplan` at all. The
`RouteLaunch` constant existed with no live enforcing test, unlike the
plan and procedure routes, whose bindings fail in both drift directions.
That is the exact configuration the prior dual-agent attempt died in: a
unifying type threaded through the code and load-bearing nowhere, with
agent-specific behavior shipped beside it as a parallel hardcoded pass.
The attempt's own design named that failure mode and shipped it anyway,
because naming a structure is not the same as making a test fail when the
structure is faked.

There is also a second, quieter defect that lifting the refusal naively
would expose. The reclamation sweep runs at the top of every dispatch and
create, and its liveness rule is "the Claude Code job entry is present."
A Codex dispatch would write a valid session mapping and never write a
job entry, so the very next dispatch would judge the first worker dead and
destroy its instance while it was still working in it. So the surfaces
are four -- launch, capture, resume, liveness -- and the last one is the
one the original task description did not name.

The design problem, then, is not how to run `codex exec`; the flags and
failure modes are measured. It is the shape of a seam that (a) a second
agent implements rather than sits beside, (b) a test can fail on when it
is faked or deleted, and (c) landed first against Claude alone with a
mechanical no-behavior-change argument.

## Decision Drivers

- **Every structural claim names its failing test.** A decision that
  cannot say which test goes red when its claim stops being true is not
  finished. This is the lesson of the prior attempt, stated as the bar
  each decision below is written to.
- **The binding bar is deletion.** If a delivery can be deleted and the
  declaration still passes, the binding is not doing its job (R4). A test
  that a delivery satisfies merely by existing does not meet it.
- **A scan must be seen red.** A structural test that has never failed is
  a structural test nobody has shown to work. Each scan here records how
  its failure was observed (R6).
- **One mechanism per question.** One capture, one record reader, one
  resume verb, one identity representation. Two implementations that
  resemble each other are the named failure mode, however tidy each half
  looks (R10, R11, R13).
- **No dead seams.** A field, kind, or constant is introduced in the PR
  that first branches on it. The launch spec's missing process-model
  field (Decision 1) is this driver applied, not an omission.
- **Fail safe on absent evidence.** A reader with no evidence must not
  answer: no home directory means no records, an unreadable liveness
  signal means the instance is spared, an unknown agent gets no spec
  rather than the first one in the map.
- **Measured Codex behavior is authoritative.** The stdin hang, the
  trust-stanza write, the exit-0-on-failed-writes result, and the resume
  flag rejections are measured facts of codex-cli 0.147.0, not
  assumptions to revisit (R7, R8, R9, R17).
- **Standard toolchain only.** The scan and binding tests use `go/ast`
  and the standard library; CI stays `gofmt -l .`, `go vet ./...`,
  `go test -race ./...` (N1). The critical path tests offline (N2).

## Considered Options

### Decision 1 -- the launch description is data in internal/agentplan, read through Producer (R2, R6, R7)

**Chosen: a per-agent `LaunchSpec` table in `internal/agentplan`
(`dispatch.go`), consulted via `Producer.LaunchSpec()`.**

The spec is everything the dispatch path has to ask about one agent: the
binary name, the leading arguments, the spelling of each pass-through flag
(`LaunchFlags` -- model, permission mode, subagent type, display name,
settings), the portable model-category bindings and recognized versionless
names, the session-record description (`SessionRecords`, Decision 4), the
resume arguments, and the management verbs printed as hints. An intent an
agent has no flag for is spelled as the empty string and dropped rather
than guessed at: forwarding a flag a binary does not accept fails the
launch, and inventing a near-equivalent hands a developer something they
did not ask for (`buildDispatchPassthrough`,
`internal/cli/dispatch.go:657`).

It lives in `internal/agentplan` rather than beside the code that execs
because the package's boundary already permits exactly this and no more.
The boundary is "reads inputs, declares outputs," and `TestLeafNeverWrites`
enforces it mechanically -- every `exec.` selector and every write-mode
`os` call is forbidden in the leaf -- so the package can name a binary and
a path without ever being able to run one. The shape is the one
`payloadLayouts` and `compositions` already use: a per-agent table of
data, read through a method on `Producer{ag}`, with the consumer package
never branching on the agent itself. The import direction is already
established -- `agent` <- `agentplan` <- `workspace` <- `cli` -- and
`internal/cli` importing `internal/agentplan` introduces no cycle.

The accessor is fail-closed and fail-safe in the same postures the rest
of the package takes: an agent outside the accepted set gets no spec
(`TestLaunchSpecForUnknownAgentIsAbsent`), and the zero agent resolves to
Claude per `internal/agent`'s own documented contract
(`TestLaunchSpecForZeroAgentIsClaude`), so a construction site not yet
wired to set the agent degrades to today's behavior rather than to no
launch at all.

**What it deliberately does not carry yet: the process model.** Claude's
`claude --bg` is daemon-backed -- the worker backgrounds itself and the
launcher runs the command to completion. Codex's `codex exec` runs the
whole turn in the foreground, so its launch must start and release
(Decision 8). That difference is real and measured, and the field for it
is still absent from `LaunchSpec` on this branch, on purpose: every agent
niwa launches today has one process model, so a launch-mode field would be
a constant nothing branches on -- the exact dead-seam shape this contract
exists to catch, in miniature. The field arrives in PR 2 with Codex, its
first second answer, and `TestLaunchSpecsAreComplete` grows the assertion
that every spec declares one in the same change.

**Rejected: the spec beside the exec code in `internal/cli`.** Putting the
table where it is consumed reads naturally until the binding is needed.
The check that an implemented declaration has a delivery behind it would
then cross packages by name-matching, or not exist -- and the cli files
would name agents, which is what the structural scan (Decision 5) forbids.
The declaration and its delivery drift apart exactly when they live apart.

**Rejected: a per-agent interface with launch methods.** Behavior where
data suffices. A method that builds an argv can hide a branch three lines
above its return; a table of strings cannot, and every property the
dispatch path needs -- completeness, drift, argv construction -- is
assertable over the table in a pure test with no tmpdir. This is the same
reasoning that chose plans over materializer interfaces in the sibling
design, applied to a much smaller surface.

### Decision 2 -- the launch route binds in both drift directions, beside its declaration (R4)

The existing binding mechanism cannot reach a launch. The `Delivery`-name
check matches an `agentplan` name against a `Materializer` registered in
`internal/workspace`, and nothing in `internal/workspace` launches
anything. Registering a fake materializer there so the existing check had
something to compare against would recreate the agrees-with-itself
problem the binding table exists to prevent: the registration would exist
to satisfy the check, and the check would pass because the registration
exists.

**Chosen: the same two-direction check, run against the launch table in
the same package as the declarations
(`internal/agentplan/dispatch_test.go`).**

- `TestLaunchSpecsMatchTheirDeclarations`: every agent whose
  `DispatchLaunch` row is implemented must have a spec, and every agent
  carrying a spec must be declared implemented. An implemented
  declaration with nothing behind it is a declaration nobody delivers; a
  spec for an undeclared agent is a delivery nobody declared. Neither is
  visible from one side alone. The table also may not carry a row for an
  agent outside the accepted set.
- `TestLaunchSpecsAreComplete`: every spec says everything the dispatch
  path has to ask it -- binary, resume arguments, hint verbs, model flag,
  a binding for every portable category, at least one recognized model
  name, and a record description that can actually be walked and decoded
  (`checkRecords` validates the root, the depth, the
  exactly-one-of-FileName-or-FileGlob rule, both field paths, and that
  the handle and liveness kinds are members of their closed sets). A
  half-filled spec is the shape a delivery takes when it is added to
  satisfy the binding rather than to launch anything, and this test is
  what closes that route.

The bar -- deleting the delivery must fail the declaration -- was
demonstrated, and re-demonstrated against this tree while this document
was written: deleting Claude's row from `launchSpecs` fails
`TestLaunchSpecsMatchTheirDeclarations` with "(dispatch-launch, claude)
is declared implemented with no launch spec behind it", and emptying any
single field of it fails `TestLaunchSpecsAreComplete` (emptying `Binary`
yields "(claude): the launch spec names no binary").

At the consuming end, `TestDispatchGateFollowsTheDeclaration`
(`internal/cli/dispatch_contract_test.go`) ranges over every accepted
agent and asserts the command does what the table says: an implemented
agent dispatches (provision called once), an unavailable one is refused
before anything is provisioned, with the declaration's reason verbatim in
the message. It is written against the table rather than against a named
agent, so when row 22 flips in PR 2 the test changes which branch it takes
for Codex and asserts the other half -- it never needs editing to keep
passing, and it fails immediately if the binary and the table disagree
about who can be launched. That is what stops the refusal a developer
hits and the gap list they read from drifting into two explanations.

**Rejected: the functional scenario as the binding.** The sibling
design's `RouteLaunch` bullet named "the gate becomes a declaration
lookup, so the inherited scenario asserts the declaration rather than a
bare refusal" as the enforcement. That is delivered -- the gate is a
lookup and the scenario asserts the declaration -- but as a binding it is
weaker than R4's bar: a scenario pins today's truth and cannot notice a
spec deleted while its row stays implemented. The two-direction check is
what meets the bar; the scenario rides on top of it.

**Rejected: a fake registration in `internal/workspace`.** Stated above;
it is the failure this table exists to make unrepeatable.

### Decision 3 -- the capture route: the on-disk session record, not the event stream (R12)

Both routes were measured against codex-cli 0.147.0. The event stream is
genuinely fast and unambiguous: `codex exec --json` emits
`{"type":"thread.started","thread_id":"<uuid>"}` as the first line of
stdout at +0.665s into a 12.1s run, and no later event repeats the id.
The rollout file for the same run appeared at +0.716s -- 51ms later --
already holding a complete, parseable `session_meta` first line at 18,693
bytes. The hypothesis that a rollout is written only when the run
finishes is false, measured with a 50ms poller.

**Chosen: the rollout file.** Four reasons, in the order they weigh.

1. **It is the same mechanism Claude's capture already is.** A directory
   of candidates, each recording a working directory and a session id,
   polled until exactly one candidate's cwd equals the instance
   directory. The correlation key, the ambiguity rule, the timeout rule,
   and the not-yet-ready-keep-polling rule all transfer unchanged -- and
   the last one turns out to be exactly the right handling for the
   residual torn-write window on an oversized first line. The stream
   route would fuse launch and capture for one agent and leave them
   separate for the other, which is the two-passes shape this work
   exists to avoid.
2. **Liveness needs the on-disk store anyway.** The reaper asks "does
   this session still exist" long after the dispatching process is gone,
   and only the record on disk can answer. Choosing the stream for
   capture would mean two Codex-side stores read by two mechanisms,
   where Claude reads one store for both.
3. **The measured objections do not survive contact with niwa's shape.**
   Concurrent Codex sessions do write into the same date directory and
   are distinguishable only by cwd -- but cwd is niwa's exact
   correlation key, the dispatch instance directory is unique by
   construction, and where two sessions genuinely claim one directory,
   ambiguity is the correct answer and the existing rule already gives
   it. The per-candidate cost is a bounded first-line read; the whole
   rollout tree on the measuring host was 31 files, and a full sweep of
   every first line measured 0.05s.
4. **The one real footgun is unreachable for niwa.** `codex exec resume`
   treats a non-UUID argument as a thread name and, for an unknown name,
   silently starts a fresh session and exits 0. niwa only ever stores an
   id that passed `workspace.ValidSessionID`, so the thread-name path
   cannot be reached from a stored handle. The stream route's ability to
   verify that a resume attached would buy protection against a case
   that cannot occur.

What the stream would have bought, and is given up with eyes open: 51ms
of latency, a smaller read than an 18.5KB first line, and post-hoc resume
verification. Recorded as the considered alternative.

Two measured traps are carried into the reader's construction rather than
worked around at call sites. The rollout tree's `YYYY/MM/DD` directories
are in host local time, not UTC -- so the reader never computes a date at
all: `descend` walks every directory at the declared depth
(`internal/cli/session_records.go:145`), and a date directory that
straddles midnight or a timezone cannot misdirect it. And the first line
embeds the whole Codex system prompt, far past `bufio.Scanner`'s 64KiB
default -- so `readRecordBytes` reads first-line-only records under an
explicit 4MiB bound (`session_records.go:221`), reading two fields
instead of a whole conversation.

### Decision 4 -- one capture over a declared record shape (R10, R11)

**Chosen: `SessionRecords` is a description, not a reader.** The agent's
spec declares where records sit (a root under home, optionally relocated
by the agent's own environment variable), how deep they nest, whether a
record is a named file or a glob match, whether it is a whole file or the
first line of one, the dotted field paths to the working directory and
the session id, which of the two available strings is the user-facing
handle, and whether presence proves liveness. One reader in
`internal/cli` (`session_records.go`) walks and decodes whatever a
description declares; one capture loop (`dispatch_capture.go`) polls it
with an injected clock, poll interval, and root, so the whole path is
offline-testable. Nothing in either file names an agent, and the scan in
Decision 5 is what holds that.

What makes this a capture rather than one agent's capture with a
parameter bolted on is the test that proves it. The capture suite
(`dispatch_capture_test.go`) runs every assertion -- cwd correlation
after symlink resolution on both sides, the ambiguity error when two
records claim one directory, the timeout error at zero matches,
keep-polling on a record whose id has not been written yet, and the
symlinked-instance-path case -- against two differently-shaped stores.
One is the shape niwa ships: a directory per job holding a
pretty-printed JSON object, the directory's name as the handle. The
other is a fixture deliberately shaped unlike anything niwa ships:
records three directories deep, matched by a glob, each a JSONL
transcript whose first line is its metadata, both fields under a nested
key, the id as its own handle. The fixture writes a second transcript
line specifically so a reader that swallowed the whole file would fail
to parse rather than quietly succeed. Running the suite against a single
store would prove the Claude path works and prove nothing about whether
the mechanism generalizes; the second shape is what makes "one capture"
a property the suite can fail on.

The shared rules are deliberately not pluggable. Refusing to guess when
two records claim the same directory, treating an id-less record as
not-ready rather than absent, and failing on an unreadable store while
tolerating an absent one are the parts that are easy to get subtly wrong,
and they are written once (`matchRecordByCwd`,
`session_records.go:259`). A candidate source that bypassed them would be
a second capture, which is R11's named failure.

**Rejected: per-agent capture functions sharing helpers.** Two captures
that resemble each other is the precise thing R11 forbids; resemblance is
not a property a test can hold.

**Rejected: generalizing by parameterizing the jobs directory only.**
That was the pre-branch shape -- `captureSessionID(jobsDir, ...)` -- and
it encodes Claude's store layout (one level, `state.json`, top-level
fields) in the reader's control flow. The description moves layout into
data, which is what lets the same reader decode a store it has never
seen.

### Decision 5 -- a new structural scan over the dispatch path (R6)

The property -- no agent constants and no agent-naming literals at
dispatch-path call sites -- is the one whose absence killed the prior
attempt, and it needs its own scan because the existing one cannot be
extended: `internal/agentplan/layout_scan_test.go` hardcodes its scope as
two directories (`workspaceDir = "../workspace"`, `leafDir = "."`), and
`internal/cli` is neither. The in-repo precedent for a file-scoped AST
test is `TestPlanVocabularyIsInterpretedInOneFile`
(`internal/workspace/applyplan_wiring_test.go`), so the idiom is
established rather than invented.

**Chosen: an AST scan in `internal/cli`
(`dispatch_layout_test.go`) over a denylist of eight files** --
`dispatch.go`, `dispatch_capture.go`, `dispatch_keepalive.go`,
`dispatch_launcher.go`, `dispatch_model.go`, `dispatch_remotecontrol.go`,
`dispatch_spill.go`, `session_records.go` -- which between them are the
whole of the `DispatchLaunch` delivery. It forbids two things, each as
its own test: the agent discriminator constants `agent.AgentClaude` and
`agent.AgentCodex` (`TestDispatchPathNamesNoAgentConstant`), and the
whole-value string literals `"claude"`, `"codex"`, `".claude"`,
`".codex"`, `"CLAUDE.md"`, `"AGENTS.md"`
(`TestDispatchPathNamesNoAgentLiteral`) -- a launch that knows the
binary's name knows which agent it is launching, whatever the
surrounding code says about being neutral. The scan reads the syntax
tree, so a comment is not a violation and code cannot hide behind
formatting; literal matching is whole-value, so
`"claude-code-is-not-the-binary-name"` is legal.

Two files sit beside the dispatch path and are deliberately not scanned,
each excused by a declaration rather than by omission:

- `dispatch_plugins.go` registers plugins with Claude Code's own plugin
  system. `MarketplaceRegistration` (row 6) is declared
  AgentCannotReceive for Codex, so there is no second agent for that
  file to be neutral about.
- `job_state.go` reads Claude Code's harness job-state file, for the
  SessionStart guard behind `EphemeralSessions` (row 17,
  AgentCannotReceive for Codex) and for `niwa watch`'s review
  continuation, which is Claude Code harness surface throughout.

Naming the exclusions against declarations is the point: an exclusion a
reader can check against a row is a different thing from an exclusion
that exists because a file happened to fail.

**It was seen red.** Against the tree it arrived on, the scan failed at
four sites: the step (2b) refusal's constant comparison and the hardcoded
model resolution in `dispatch.go`, the per-agent model tables in
`dispatch_model.go`, and the launcher's `exec.LookPath("claude")` in
`dispatch_launcher.go`. Turning those green by routing each through the
launch declaration is the change the scan shipped with, in one PR, which
is how red-then-green is demonstrated inside a single review. And the red
is reproducible on demand: reintroducing the launcher's literal by hand
fails the scan today with `dispatch_launcher.go:85: names claude` --
re-verified against this tree while this document was written.

Two guards keep a passing scan meaningful over time.
`TestDispatchScanDetectsWhatItForbids` is the control: it runs the
detectors against fixture source written to contain exactly what they
look for -- two constants, two literals, plus a comment naming both and
a substring-bearing literal that must not fire -- and fails if the scan
comes back clean. A detector that matched nothing would pass the two
main tests forever, and the first person to reintroduce a hardcoded
agent would get a green run; the control is what makes that impossible.
`TestDispatchPathScanCoversTheLaunchSurface` guards the scope: every
non-test file whose name begins with `dispatch` must be either scanned
or excused in the test's own excusal map with the declaration that makes
it one agent's, so a new dispatch file added without a decision fails
instead of passing quietly. The scan also refuses to pass vacuously: a
listed file that has gone missing is a hard failure, because a scan that
passes by not looking is worse than no scan.

**Rejected: an allowlist of files permitted to name agents, seeded with
today's offenders.** A scan that cannot fail on arrival, which is the
failure mode the existing scan's own known-red comment warns about.

**Rejected: extracting the dispatch path into a subpackage and scanning
the package boundary.** It costs four outbound symbol exports consumed by
`reap.go`, `plugin_adapter.go`, and `watch.go`, and fights
`internal/cli`'s self-registering `init()` command convention, for no
additional enforcement power over the file-scoped scan.

**Rejected: grep.** String matching cannot tell code from comments, and
several of these files explain in prose which agent a mechanism came
from -- documentation the scan must not punish.

### Decision 6 -- one session identity, and the agent on the mapping (R10, R13)

Codex session ids are lowercase UUIDs -- UUIDv7, sampled from real
rollouts -- and `workspace.ValidSessionID`'s regex deliberately leaves
the version and variant nibbles unconstrained, so they pass unchanged.
Nothing about the store changes: `SessionMapping`, its path
construction, and the whole read, write, list, delete surface in
`internal/workspace/session_map.go` serve both agents as they are, with
no branch at the read or write site. That non-decision is itself checked:
the shapes being identical is also why nothing may infer the agent from
an id -- a reader that switched on shape would have no test to hide
behind and no signal to switch on.

The one asymmetry between the agents is the handle. Claude Code's
management verbs reject the full UUID and take the job directory's short
name; Codex's sessions are named by the UUID itself. So capture returns
both an id and a handle, the id keys the durable mapping, the handle is
what the hints print and what resume passes -- and which string the
handle is belongs to the agent's declaration (`HandleKind`:
`HandleRecordDir` or `HandleSessionID`), never to the caller. The
record-directory handle is the actual directory name on disk and never a
slice of the UUID, which keeps it correct if the two ever stop
coinciding.

**The mapping records its agent** (`SessionMapping.Agent`,
`session_map.go:64`). The reaper cannot pick a liveness rule without
knowing which agent a mapping belongs to, and it must not infer one from
the shape of an id. The field is `omitempty`, and an absent value reads
as the zero `Agent`, which `internal/agent` documents as Claude -- the
correct reading, because every mapping written before the field existed
describes a Claude session, the only kind niwa ever wrote one for.
`TestDispatchMappingRecordsTheLaunchingAgent` pins that dispatch writes
it; `TestReap_MappingWithNoAgentRecordedIsClaude` pins the compatibility
reading, so a pre-field mapping with no job entry stays reclaimable
exactly as it was.

Resume stays one verb on the strength of the same declaration.
`dispatchAttach` (`internal/cli/dispatch.go:135`) looks the binary up,
runs the agent's own resume arguments against the handle with inherited
stdio, and propagates the outcome -- all of it written once, with only
`spec.ResumeArgs` and `spec.Binary` varying.
`TestDispatchResumeUsesTheDeclaredVerb` substitutes the seam and asserts
the declared verb and a non-empty handle arrive, with everything around
the exec -- the lookup, the non-fatal failure handling -- the same code
whoever was launched.

**Rejected: a per-agent identity type or a second store.** Nothing
requires one: the ids share a format, the store is format-agnostic, and
a second representation would need a branch at every read site -- the
exact spread this feature's brief names as the reason to stop and
escalate.

### Decision 7 -- liveness: the reaper spares what it cannot read, and the gap is declared (R14, R15)

Which liveness rule applies to a mapping is the launching agent's own
declaration: `SessionRecords.Liveness`, a closed two-member kind.
`LivenessRecordPresence` means the record is removed when the developer
deletes the session, so presence is a faithful proxy for "still there,
running or resumable" -- Claude's jobs directory works this way, and the
entry-present rule with spare-while-resumable semantics is unchanged for
it. `LivenessNone` means the agent records no signal niwa can read that
tells a live session from a deleted one.

The reaper's gate reads the mapping's recorded agent, resolves its spec,
and spares the instance unless the declaration says presence is a
faithful proxy (`internal/cli/reap.go:159`): no spec, or any liveness
kind other than record-presence, means no evidence, and with no evidence
it must not act. Sparing an instance nobody is using costs a directory;
reclaiming one a resumable session still lives in costs the work in it,
which is the failure this whole rule exists to prevent.
`TestReap_MappingWithNoLaunchableAgent_Spared` is the safety property as
a test: a mapping for an agent with no launch spec is spared even though
the Claude-shaped store has nothing for it -- the state that, before this
branch, read as "the developer deleted this session" and destroyed the
instance.

**PR 2 declares Codex `LivenessNone`.** This is the honest declaration,
and it is worth being precise about why, because the tempting alternative
is formally available. Codex rollouts are never aged out: the agent has
no delete verb for them and removes nothing on session end, so the honest
Codex analogue of "the job entry is gone" does not exist. A
record-presence rule over the rollout store would be truthful in the
vacuous sense -- rollout gone does mean unresumable -- but its trigger
never fires without a human hand-deleting a file in a dot-directory no
guide told them about, and wiring instance destruction to that would make
an undocumented file operation the trigger for data-loss-shaped behavior
while letting the guide describe a reclamation story that in practice
never runs. `LivenessNone` says what is true: niwa cannot read Codex
session liveness, so the reaper declines, and a Codex-dispatched
instance is not reclaimed by the mapping path. That consequence is a
declared cost, visible in the table's row commentary and rendered in the
published guide (R15), rather than a surprise a developer meets as a
directory that never goes away.

**Explicitly not built here: the real liveness proxy.** Closing the gap
means a name-plus-TTL-plus-mtime backstop for mapped Codex instances --
the analogue of the existing backstop, which today ages only unmapped
orphans (`selectBackstopTargets`, `reap.go:283`) and deliberately never
touches a mapped instance. That is the next feature's work, named as such
so the next author inherits the boundary rather than rediscovering it;
this feature's job is the narrower one of not introducing data loss. The
acceptance shape follows: with a Codex mapping present, the sweep spares
the instance; no TTL or mtime rule for mapped instances appears in PR 2's
diff.

**Rejected: record-presence over the rollout store.** Stated above: a
rule whose trigger is an action nobody performs presents a working
reclamation path that does not exist, and it would purchase a
generalization of `sessionLive` for a branch that never runs.

**Rejected: inferring liveness from the worker process.** The reaper
runs long after the dispatching process is gone, holds no pid, and a
finished `codex exec` leaves a resumable session with no process at all
-- process absence is not session death for either agent.

### Decision 8 -- the Codex launch shape (R7, R8)

Every element here is settled by measurement against codex-cli 0.147.0,
and each one is a field or rule of PR 2's spec and launcher rather than
advice.

- **Start-and-release, not run-to-completion.** `codex exec` runs the
  whole turn in the foreground, so Claude's `cmd.Run()` shape would park
  the dispatch for the entire task. Measured: `cmd.Start()` with
  `SysProcAttr{Setsid: true}` and stdio redirected to files returns in
  about 670 microseconds; the child survives the parent's exit, is
  reparented, completes its turn, writes its rollout, and ignores
  signals sent to the launcher's process group -- so a Ctrl-C on the
  niwa CLI does not take the worker with it. This is the launch-mode
  field's first second answer, and it lands in `LaunchSpec` in the same
  PR (Decision 1).
- **`exec.Command`, never `exec.CommandContext`.** The current launcher
  uses `CommandContext` and may keep it for a run-to-completion agent,
  but a released child must not be tied to the dispatch's context: a
  context cancelled when dispatch returns would kill the worker the
  instant launch finished.
- **Stdin at `/dev/null`, always.** `codex exec` reads stdin in addition
  to its positional prompt. With stdin inherited or left an open pipe,
  the process blocks before doing anything -- measured at 20 seconds
  with zero stdout, no rollout file, no API call, and one line on
  stderr -- so the failure leaves nothing on disk to diagnose it with.
  A launcher that only redirects stdout is not sufficient. The
  misleading detail is that Codex prints "Reading additional input from
  stdin..." even when stdin is `/dev/null`, so that line is not a
  signal anything was read.
- **Stdout and stderr to files niwa owns, never merged.** stderr is not
  empty on a healthy run -- 1.4KB of MCP tracing on the measuring host
  -- and `--json` stdout to a file is clean JSONL with no ANSI; merging
  would corrupt the one and bury the other. Non-empty stderr is not
  failure; exit codes are (Decision 10).
- **`--skip-git-repo-check` is mandatory, and trust does not substitute
  for it.** An instance root is not a git repository, and the launch
  refuses to start there without the flag; measured, the identical
  startup failure reproduces with the path trusted, so the flag is not a
  trust workaround.
- **Never `--ephemeral`.** It suppresses the session record on disk,
  which is the store both capture and liveness read. One flag would
  disable two mechanisms.
- **Resume is launched with the child's real cwd already set.**
  `codex exec resume` rejects `-C` and `-s` with a parse error and exit
  2, so the working directory is `cmd.Dir`, not a flag. A resume appends
  to the existing rollout and creates no new file, and its own
  `thread.started` reports the resumed id -- so a session id and its
  rollout are stable across arbitrarily many resumes, and
  capture-once-at-launch is sound.
- **niwa never chooses the id.** There is no `--session-id` or
  `--thread-id`; Codex mints the UUIDv7 and niwa learns it from the
  record. Any design premised on pre-assigning the id is dead, which is
  why capture exists at all.

Also ruled out by measurement: `--full-auto` and `-a/--ask-for-approval`
do not exist on `codex exec` in 0.147.0 -- they are interactive-only,
and any document referencing them for a headless run is stale. Autonomy
on the exec surface is `--sandbox`, `--approve-for-me`, and
`--dangerously-bypass-approvals-and-sandbox`, and Decision 9 is about
why niwa passes none of them.

### Decision 9 -- sandbox posture through a per-invocation trust override, not a flag and not persistent trust (R9)

Passing an elevated `--sandbox` has a side effect no launch flag should
have: it appends `[projects."<cwd>"] trust_level = "trusted"` to the
developer's own `~/.codex/config.toml`. Measured on fresh untrusted
directories with both `workspace-write` and
`--dangerously-bypass-approvals-and-sandbox`: the trust-stanza count
went from zero to one in each case. Every dispatch targets a fresh
instance directory, so every dispatch would append a stanza to one
shared, never-pruned file -- the measuring host's already carries 58
from ordinary use -- with concurrent dispatches racing on a write niwa
holds no lock for, and each stanza outliving the instance it names once
`niwa reap` removes it.

The plain path is clean but stuck read-only. Measured with md5 of the
config taken before and after: a plain
`codex exec --skip-git-repo-check` at an untrusted root writes nothing
and lands `approval: never`, `sandbox: read-only`, repeatably; the
identical plain command at a root carrying a trust stanza lands
`sandbox: workspace-write [workdir, /tmp, $TMPDIR]`; and neither plain
run modified the config -- md5 byte-identical before and after both. So
whether a dispatched worker can write to its own instance is decided
entirely by whether Codex considers the root trusted at that moment --
and there is a way to make it so for a single invocation.

**Chosen: a per-invocation trust override on the launch argv, and no
`--sandbox` flag on any dispatch.**

```
codex exec -c 'projects={"<abs instance dir>"={trust_level="trusted"}}' ...
```

The first reason is scope, and it is a security argument that will
outlive the particular flag implementing it. A persistent trust entry
vouches for anything anybody ever runs at that path -- including
sessions niwa did not launch and had no opinion about -- and it keeps
vouching after the instance is gone. An invocation-scoped grant vouches
for exactly the worker niwa is starting, and evaporates when it exits.
The elevation niwa needs is one worker's, and this is the mechanism
whose grant is exactly that wide.

The second is footprint. Measured: the override lands
`sandbox: workspace-write [workdir, /tmp, $TMPDIR]` and writes no
trust stanza into `~/.codex/config.toml` -- so there is nothing to
retract, nothing for concurrent dispatches to race on, and no stanza
to outlive the instance. It needs no `--sandbox` flag and no approval
flag either -- `codex exec` already lands `approval: never` by
default. One method note belongs with the measurement, because a
measurement that looks rigorous and is not should be handed to the
next reader rather than rediscovered: a checksum is not a usable
signal on that file, since every run rewrites an unrelated marketplace
timestamp; the trust-relevant signal is the count of `[projects.`
stanzas. (The earlier plain-run md5 comparisons above were taken
against an isolated, minimal configuration where the checksum was
meaningful.)

The override is per-agent launch data like every other argv element,
so it lives in the Codex spec's leading arguments with the instance
directory interpolated as its own value, never shell-composed.

**Consequently, row 22 carries no `Requires: DirectoryTrust` edge.**
The edge looked right while persistent trust was the only route to the
posture, and it would have been a false dependency: with the posture
granted per invocation, the launch needs nothing at all from directory
trust -- a worker at an instance root reads no project layer either
way, because niwa writes none there. Declaring the edge anyway would
put the closure test to work enforcing a dependency that does not
exist, which is drift manufactured rather than prevented.

Two measured traps are recorded because both are ways a careful
implementation still gets this wrong:

- The dotted-path spelling
  `-c 'projects."<path>".trust_level="trusted"'` parses cleanly and
  silently does nothing -- posture stays `read-only`. That is not a
  quoting artifact of the path: it fails identically on a dot-free
  path. The general fact matters more than the key, and it deserves
  its own sentence: a clean exit from a `-c` override is not evidence
  the override took effect. Anything that generates those flags
  inherits that footgun, this feature and every later one -- which is
  why the launch test asserts the inline-table spelling, not merely
  that some `-c` argument is present.
- `-c 'sandbox_mode="workspace-write"'` does change the posture and
  still writes the trust stanza, exactly like the flag. The write-back
  is triggered by an effective elevated posture at an untrusted
  directory, whatever its source -- so "set the posture through config
  rather than through a flag" is not a workaround, and R9's named
  failure (a launch whose flags make Codex itself write trust) covers
  the config spelling too.

**Rejected: passing `--sandbox workspace-write` per dispatch.** The
unmanaged config write above, per dispatch, racing, unretracted.

**Rejected: persistent trust through niwa's own writer, with a
`Requires: DirectoryTrust` edge on row 22.** This was the right answer
until the override was measured: niwa's trust writer is
lock-serialized, canonicalizes path keys, writes additively, and
retracts only what it wrote, so it avoids every defect of the flag. It
loses first on scope: it is a broader grant than the feature needs,
made permanent, as a side effect of dispatching one background task --
a persisted stanza vouches for every future session at that path, not
just the worker niwa launched, and for whatever window it exists it
vouches for sessions niwa had no opinion about. It loses again on
footprint -- a per-dispatch write to a file niwa does not own, plus a
retraction obligation wired into reap, where the override writes
nothing at all. And it would carry the false dependency above into the
declaration table.

**Rejected: no elevation, accept read-only.** Under the default
posture a worker fails every write and still exits 0 (Decision 10), so
the default experience of the feature would be a worker that burns a
turn explaining it cannot work, reported as a completed session. A
diagnosable degradation is not an acceptable default.

One host caveat is recorded rather than designed around: on hosts where
an AppArmor unprivileged-user-namespace restriction blocks bubblewrap,
`workspace-write` cannot initialize and the worker degrades to
explaining that it cannot write -- confirmed under the winning
invocation, whose one run against a real model on this host still
failed its write. That is a host property, reproduced outside Codex
entirely, and it makes a worker's inability to work a diagnosable
condition niwa should surface rather than let the worker spend tokens
on -- surfaced, not acted on.

### Decision 10 -- what an exit status is allowed to mean (R17, R18)

A Codex worker's exit code is not a task-success signal, and dispatch's
reporting must not pretend otherwise. Measured: under the default
read-only sandbox a worker instructed to create a file fails the write
-- "writing is blocked by read-only sandbox" on stderr -- explains in
its final message that it could not create the file, and exits 0. And in
the other direction, exit 1 covers every runtime and API error alike,
including a resume against a nonexistent session ("no rollout found for
thread id"); exit 2 is an argument parse error; non-empty stderr
accompanies healthy runs.

So dispatch and resume report only what the status can support: the
session ran and ended, with which code. They never render exit 0 as "the
task succeeded" -- the truth about the task is in the session, reachable
through the printed hints, and claiming more than that from a status
byte is exactly the silent-failure mode the read-only measurement
demonstrates.

Quota exhaustion gets one narrow carve-out, and only for classification.
It is exit 1 among every other API error and is detectable only by
parsing the error payload for its markers (`usage_limit_reached`,
`UsageLimitReached`, `CreditsDepleted`). PR 2 classifies it so the
message a developer sees says what happened rather than a generic launch
failure -- and does nothing else. No automatic agent switching, no
retry, no fallback: acting on the condition is a policy decision nobody
has made, and the classification exists precisely so a future policy has
a clean condition to act on rather than a misfiled error. A test feeds a
quota-shaped payload and asserts the classified message; a generic
failure stays generic; no code path selects a different agent in
response.

### Decision 11 -- two pull requests, and what PR 1's proof rests on (R19, R20, R21)

**PR 1 -- the seam, against Claude only, no behavior change -- is done
and merged on this branch** (commit 5a7b4c4). It routed the gate, the
preflight, the launcher's argv, the model resolution, the capture, the
hints, and the resume through the launch declaration; landed the
identity representation and the agent field on the mapping; landed the
reaper's declaration-reading gate; generalized capture over the record
description; and took the structural scan from red at four sites to
green. It also deleted the per-agent model table's Codex entries: the
binding check identified them as a delivery no declaration stood behind
-- nothing read them, because the refusal fired first -- which is the
same half-built groundwork row 22's earlier reason text had cited.

The split is not optional, and the reason is reviewability, not
tidiness. PR 1's correctness argument is "nothing a user can observe
changed," provable only when the PR contains nothing else; PR 2's is
"the new thing works," reviewable only when the refactor beneath it is
already trusted. Folded together, neither argument can be made -- which
is how the prior attempt became unreviewable. If PR 2 grows beyond one
reviewable change, it splits by surface -- launch, then capture and
liveness, then resume -- never into "the plumbing" and "the behavior";
that second split is the prior attempt's other failure.

"The suite passes" is not PR 1's proof, because the dispatch path's
substitutable seams are package-level function variables and a signature
change updates every substituting fake in the same commit. The proof is
the named-seam form R20 defines, and concretely it is this. Four seam
signatures changed, each because the seam now receives the agent's
declaration, each accompanied by a test that pins what the new parameter
is used for:

- `lookClaude() (string, error)` became
  `lookAgentBinary(name string) (string, error)` -- the preflight takes
  the binary's name as data. `TestDispatchPreflightsTheDeclaredBinary`
  pins that it is asked for the declared binary;
  `TestDispatch_ClaudeNotOnPath_Errors` still pins the
  before-provisioning ordering with `provisionCalled == 0`.
- `dispatchAttach(id string)` became
  `dispatchAttach(spec agentplan.LaunchSpec, handle string)` -- resume
  runs the declared verb against the captured handle.
  `TestDispatchResumeUsesTheDeclaredVerb` pins both.
- `captureSessionID(jobsDir, instanceDir, ...) (sessionID, shortID,
  error)` became `captureSessionID(records agentplan.SessionRecords,
  root, instanceDir, ...) (sessionID, handle, error)`, and the
  `dispatchCapture` seam changed with it -- capture reads a declared
  record shape and returns the declared handle. The two-store capture
  suite pins the mechanism.
- `realDispatchLaunch` (and the `dispatchLaunch` seam) gained the
  leading `spec agentplan.LaunchSpec` parameter -- the argv is built
  from the declaration. The launcher tests read the spec rather than
  naming an agent.

Every other seam declaration -- `dispatchPromptCapture`,
`dispatchInteractive`, `provisionInstanceFunc`, `destroyInstanceFunc` --
is byte-identical to its pre-branch form, checkable by diffing the
declarations. The behavior assertions running on top of the substituted
seams were not modified or deleted; new assertions were added. The one
user-observable change is the refusal's wording, which now quotes the
declaration's reason instead of a sentence written beside it -- named as
such rather than smuggled.

**PR 2 -- Codex as the second implementation** -- adds the Codex
`LaunchSpec` row (with the launch-mode field and its Claude value, both
arriving with their first consumer), the start-and-release launcher
path, the rollout record description, the `LivenessNone` declaration,
the per-invocation trust override on the launch argv, the row 22 flip,
the quota classification and exit-status reporting, the regenerated guide section with its
surrounding prose edited in the same change (R16), and the
transformation -- not duplication -- of the functional scenario that
today pins the refusal, so that no scenario asserting the refusal
survives the delivery (R21). The gate, capture, resume, and reaper
change in PR 2 only by what the new declaration says: their code landed
in PR 1 and reads the table.

## What a Dispatched Codex Worker Does Not Receive

This section is deliberately its own rather than a caveat inside a
decision, because it records a limit this work hit and does not resolve.

A dispatched worker's working directory is the instance root, and a
Codex session started at an instance root receives none of the plugin
skills, MCP servers, session environment, or approval posture that rows
5, 8, 9, and 12 declare implemented for Codex -- because all four are
delivered inside each repository, and measurement shows Codex fixes its
discovery at session construction, keyed to the launch directory: it
follows neither the working directory as the session moves nor an
instruction naming a repository. A worker told in its prompt to work in
a particular repository still gets nothing from that repository's
delivered configuration. The files stay readable on request, which is a
categorically weaker delivery than the content being in the session's
context. The worker also gets no composed orientation -- niwa
deliberately writes nothing Codex-shaped at an instance root today --
and takes its task from the prompt alone.

Two findings follow, and both belong on the record.

**The contract cannot currently express this gap.** Declarations are per
capability and agent, two states, scoped by *who* receives and never by
*where from*. Rows 5, 8, 9, and 12 are true for a repository session and
not for a root-started one, and the schema has no axis to say so. That
is a limit this work hit, named as such. No capability row is invented
for it: the 24-row set stays closed, which is its own requirement (R5),
and a row would be the wrong shape anyway -- the gap runs along an axis
the schema does not have, not along a missing member of the axis it
does.

**Row 2's stated reason is factually wrong, which is why niwa writes
nothing at an instance root today.** The row's reason reads: Codex reads
context only from the nearest project-root marker downward, and an
instance root has none. The second clause is true. The conclusion does
not follow, because a session's own working directory always contributes
its context file -- measured both with a marker-bearing ancestor and
without one: an `AGENTS.md` placed at a markerless instance root was
read into the session's context verbatim, because Codex falls back to
treating cwd as the project root when no marker is found above it. This
is recorded as a finding about already-shipped work. This design does
not correct row 2 and writes nothing at an instance root; the launch
works from a bare directory (Decision 8's `--skip-git-repo-check`), and
the worker is briefed by its prompt.

Whether closing the gap belongs to this feature or a follow-on is an
open question being decided above this feature, and this document
leaves it stated rather than resolved. What closing it would take, for
the record: correcting row 2's reason and flipping its state, writing
Codex-shaped content at the instance root -- both touching
already-shipped declarations -- and, for the repository-scoped rows,
either delivery at the root or a scope axis the contract does not have.
One measured constraint for whoever takes it: a
`project_root_markers` override does work from a repository
subdirectory, but only via the CLI flag or the user layer -- declaring
the marker inside the instance root's own configuration is inert, and
adding `.git` to the list defeats it.

## Decision Outcome

The launch route binds the way the plan and procedure routes already do:
a delivery beside its declaration, checked in both drift directions,
with completeness closing the hollow-delivery loophole. `LaunchSpec` in
`internal/agentplan` says what to launch, how to spell each intent,
where the session records sit and how to read one, what the handle is,
whether presence proves liveness, and how to step back in;
`internal/cli` does the launching, polling, and exec, and an AST scan
holds that no dispatch-path file names an agent. Capture is one reader
and one loop over a declared record shape, proven against two store
shapes. The mapping records its agent; the reaper reads the declaration
and spares what it cannot prove dead. PR 1 shipped all of that against
Claude with a named-seam no-behavior-change proof. PR 2 adds Codex as
one table row plus the measured launch mechanics: start-and-release
under setsid, `/dev/null` stdin, split output files,
`--skip-git-repo-check`, posture via the per-invocation trust override,
`LivenessNone` with the reclamation gap declared, and exit-status
reporting that claims session completion, never task success.

## Solution Architecture

Package layout and the read direction (`internal/cli` imports
`internal/agentplan` directly -- new on this branch -- and no cycle
results; the leaf imports nothing above `internal/agent` and
`internal/config`):

```
internal/agentplan
    dispatch.go         LaunchSpec, LaunchFlags, SessionRecords,
                        HandleKind, LivenessKind, launchSpecs table,
                        Producer.LaunchSpec()
    dispatch_test.go    the launch-route binding: declaration match in
                        both directions, spec completeness, fail-closed
                        and zero-agent accessor posture
internal/cli
    dispatch.go         the flow: gate (declaration lookup), preflight,
                        provisioning, launch, capture, mapping, hints,
                        attach
    dispatch_launcher.go argv construction and exec, from the spec
    dispatch_model.go   category/name resolution, from the spec
    dispatch_capture.go the capture loop (injected clock/poll/root)
    session_records.go  the one record reader over SessionRecords
    reap.go             the liveness gate reading the mapping's agent
    dispatch_layout_test.go    the AST scan, its control, its scope guard
    dispatch_contract_test.go  gate-follows-the-declaration and friends
internal/workspace
    session_map.go      SessionMapping gains Agent (omitempty; absent
                        reads as Claude)
```

The flow, per dispatch, with the agent resolved once and threaded as
data:

1. Resolve the agent from `NIWA_AGENT` and the workspace
   `default_agent`, flag argument empty -- `niwa dispatch --agent`
   remains the subagent-type passthrough and never participates (R1).
2. Gate: `agentplan.Lookup(DispatchLaunch, agent)` plus
   `Producer.LaunchSpec()`. Unavailable, or implausibly implemented with
   no spec, refuses with the declared reason before anything exists on
   disk (R2).
3. Preflight `spec.Binary` on PATH before provisioning, so an absent
   binary fails with no instance and no mapping (R3).
4. Provision, arm rollback, launch with the spec's argv shape and (in
   PR 2) its launch mode.
5. Capture: poll the spec's record store, correlate by normalized cwd
   equality against the unique instance directory, return id and
   declared handle (R10, R11, R12).
6. Write the mapping with the agent recorded; print the spec's hint
   verbs against the handle; attach through the spec's resume arguments
   unless detached (R13).
7. Any later sweep reads the mapping's agent, resolves its spec, and
   applies the declared liveness rule -- or declines (R14).

## Implementation Approach

PR 1 is merged; its content and proof are Decision 11's. PR 2, in
increments that each keep the suite green:

1. The launch-mode field on `LaunchSpec`, with Claude's value, plus the
   completeness assertion for it -- landing with the Codex row in the
   same increment so the field's second answer arrives with the field.
2. The Codex `launchSpecs` row: binary, `exec` leading arguments with
   `--json` and `--skip-git-repo-check`, flag spellings, model
   vocabulary, the rollout `SessionRecords` (depth 3 under the sessions
   root, glob, first-line-only, nested field paths, `HandleSessionID`,
   `LivenessNone`), resume arguments, hint verbs. The binding and
   completeness tests confront it with the whole contract on arrival.
3. The start-and-release launcher path selected by the spec's launch
   mode: `cmd.Start()` with `Setsid`, `/dev/null` stdin, stdout and
   stderr to files under the instance's `.niwa/` directory, release. A
   launcher test asserts the stdio wiring rather than hanging on its
   absence.
4. Row 22 flips to implemented -- with no `Requires` edge, per
   Decision 9 -- in the same change as the delivery, never before
   (R4). The gate, capture, resume, and reaper tests change branches by
   themselves.
5. Exit-status reporting and quota classification (Decision 10), with
   the payload-shaped test.
6. The guide: regenerate the gap section via the existing `-update`
   flow, edit the surrounding hand-written prose in the same change so
   it stops describing the refusal, and state the reclamation gap with
   its named next-feature owner (R15, R16).
7. Transform the functional scenario that pins the refusal into the one
   that asserts the delivery; no refusal-asserting scenario survives
   (R21).

Exit criteria: the declaration suite, the binding tests, the scan, the
capture suite, and the reap tests all green with the Codex row present;
`gofmt -l .`, `go vet ./...`, `go test -race ./...`; one manual
dispatch-and-resume against a real `codex` binary as a sanity check,
never as the only coverage (N2).

## Security Considerations

- **No write at all to the developer's Codex configuration.** niwa
  passes no `--sandbox` flag and persists no trust stanza; the posture
  rides a per-invocation override that leaves the file byte-identical
  (Decision 9). The named failures a test catches are a launch argv
  carrying a sandbox flag and the `sandbox_mode` config spelling, both
  of which make Codex itself write trust.
- **The prompt is one argv element, never shell-interpolated.** The
  existing D8 guard carries over unchanged: flags and values are
  discrete elements, the spill path handles oversized and NUL-bearing
  prompts, and the prompt separator is per-agent data
  (`LaunchSpec.PromptSeparator`) so a dash-leading prompt cannot be
  read as a flag for an agent whose parser needs the guard.
- **Worker output lands in files niwa owns, inside the instance.**
  stdout and stderr are separate files under the instance's own
  directory, reclaimed with it; nothing is written outside the
  instance, and nothing at the instance root is Codex-readable
  configuration (R8).
- **Session ids are validated before use.** Codex ids pass through the
  same `ValidSessionID` gate as Claude's before becoming a path
  component or a command argument, and capture refuses ambiguity
  rather than guessing -- two sessions claiming one instance directory
  is an error, never a pick.
- **The resume footgun is fenced by validation, not by hope.**
  `codex exec resume` silently starts a fresh session for an unknown
  thread name; niwa only ever resumes a stored, validated UUID, so the
  name path is unreachable from niwa's surface (Decision 3).
- **The reaper cannot destroy what it cannot read.** The liveness gate
  fails safe on an absent spec, an unreadable store, or a
  `LivenessNone` declaration; the cost is a leaked directory, declared
  in the guide, never a destroyed live session (Decision 7).
- **The leaf still cannot launch.** The launch description living in
  `internal/agentplan` adds no process or write surface to the leaf:
  `TestLeafNeverWrites` continues to forbid every `exec.` selector
  there, so the layer that says what to launch cannot launch it.

## Consequences

Positive:

- The prior attempt's failure mode is structurally closed on this path:
  which agent a dispatch launches is a lookup against a table whose
  deliveries cannot be deleted, hollowed, or added without a test going
  red, and no dispatch-path file can name an agent without the scan
  catching it.
- The refusal, the declaration, and the published gap list are one
  sentence in three places, generated and drift-tested, so they cannot
  tell a developer three different things.
- A third agent's dispatch support has a defined job: one `launchSpecs`
  row, confronted by the completeness and binding tests on arrival,
  with the gate, capture, resume, and reaper needing no changes.
- The capture reader is proven against a store shape it was not written
  for, so the second real store is an entry in a table rather than an
  engineering project.

Negative, accepted:

- A Codex-dispatched instance is not reclaimed by the mapping path.
  This is Decision 7's declared cost: rollouts never age out, niwa
  refuses to guess, and the directory leaks until the developer
  destroys it or the next feature's backstop ages it. Declared in the
  table and the guide rather than discovered.
- A dispatched Codex worker starts with its task prompt and nothing
  else -- no skills, no MCP servers, no session environment, no
  composed orientation. The gap, its cause, and the contract's
  inability to express it are recorded above; its closure is decided
  above this feature.
- The launch table is more hand-maintained data, and its honesty is
  only as good as its tests -- which is the accepted trade throughout
  this contract: maintenance errors are loud rather than impossible.
- One user-visible wording change shipped in a "no behavior change"
  PR: the refusal now quotes the declaration. Named in the PR rather
  than hidden, and the alternative -- keeping the old sentence beside
  the table it duplicates -- is the drift this work removes.
- The reaper consults `agentplan` and `agent` by name, which is
  correct -- it is not on the scan's denylist and must resolve
  declarations -- but it means the scan's boundary needs its scope
  guard to stay meaningful as files move.

## References

- docs/prds/PRD-codex-background-dispatch.md -- the upstream
  requirements (R1-R21, N1-N2) this design answers.
- docs/briefs/BRIEF-codex-background-dispatch.md -- the framing: four
  surfaces, two judgments on what counts as done, the two-PR split.
- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  sibling design: the declaration table, the routes, the binding
  posture this design extends to `RouteLaunch`, and the prior attempt's
  post-mortem.
- docs/guides/codex-agent.md -- the published guide whose generated gap
  list carries row 22 today and changes when it flips; the reclamation
  gap lands in its prose per R15.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- the standing spike
  for measured Codex discovery behavior, which the instance-root
  findings above extend.
- internal/agentplan/dispatch.go and dispatch_test.go -- the launch
  description and its binding (Decisions 1, 2).
- internal/cli/session_records.go, dispatch_capture.go, and
  dispatch_capture_test.go -- the one reader, the capture loop, and the
  two-store suite (Decisions 3, 4).
- internal/cli/dispatch_layout_test.go -- the scan, its control test,
  and its scope guard (Decision 5).
- internal/cli/dispatch.go, dispatch_contract_test.go, reap.go, and
  reap_test.go -- the gate, the declaration-following tests, and the
  liveness gate (Decisions 2, 6, 7).
- internal/workspace/session_map.go -- the mapping's `Agent` field and
  `ValidSessionID` (Decision 6).
- tsukumogami/niwa#248 -- the closed prior attempt whose failure mode
  every enforcement test here exists to make unrepeatable.
