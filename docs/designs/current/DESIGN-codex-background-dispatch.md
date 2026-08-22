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
  exercised against three differently-shaped stores; the durable mapping
  records its agent; the reaper spares any mapping whose liveness it
  cannot read. Codex launches via start-and-release with setsid, stdin at
  /dev/null, stdout and stderr to separate niwa-owned files,
  --skip-git-repo-check always, and its sandbox posture from a
  per-invocation trust override whose grant is scoped to the one worker
  process and writes nothing to the developer's own Codex configuration
  -- never a --sandbox flag, never a persisted trust stanza.
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

One thing distinguishes this document from an ordinary design: both
halves of the work it describes exist. PR 1 (commit 5a7b4c4, hardened in
ac6d3ab) landed the seam against Claude alone; the branch this document
rides carries the Codex delivery on top of it. Every decision below
describes code that exists and tests that run -- where a decision was
demonstrated by a failing test, the failure was reproduced against a
real tree rather than remembered from a plan -- and the launch mechanics
are constrained throughout by measurement against codex-cli 0.147.0.

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
binary name, the launch mode (`LaunchMode`, below), the leading
arguments, the working-directory grant (`WorkdirGrantArgs`, Decision 9),
the spelling of each pass-through flag (`LaunchFlags` -- model,
permission mode, subagent type, display name, settings), the portable
model-category bindings and recognized versionless names, the
session-record description (`SessionRecords`, Decision 4), the resume
arguments, and the management verbs printed as hints. An intent an
agent has no flag for is spelled as the empty string and dropped rather
than guessed at: forwarding a flag a binary does not accept fails the
launch, and inventing a near-equivalent hands a developer something they
did not ask for (`buildDispatchPassthrough`,
`internal/cli/dispatch.go:675`).

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

**The process model is a field, and it arrived with its second answer.**
`LaunchMode` has two members: `LaunchBackgrounded` -- the binary puts its
own worker in the background and exits, so the launcher runs the command
and waits, because the process it started is the hand-off rather than
the work -- and `LaunchDetached` -- the binary runs the whole turn in
the foreground, so the launcher starts it in a session of its own and
releases it (Decision 8). The launcher branches on it, which is the
point of its timing: the field was deliberately absent from PR 1, where
every launched agent had one process model and a launch-mode field would
have been a constant nothing branches on -- the exact dead-seam shape
this contract exists to catch, in miniature. It landed in the change
that gave it a second value and a live branch, and
`TestEveryLaunchSpecFieldIsRead` holds that it stays read. One deliberate
asymmetry: the launcher's switch treats any mode other than
`LaunchDetached` as backgrounded, so the zero value degrades to the
run-and-wait path -- the same fail-safe posture as the zero agent
resolving to Claude.

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
agent, and when row 22 flipped on this branch the test changed which
branch it takes for Codex and asserted the other half with no edit -- it
never needs editing to keep passing, and it fails immediately if the
binary and the table disagree about who can be launched. That is what stops the refusal a developer
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
symlinked-instance-path case -- against three differently-shaped
stores. The first is Claude's: a directory per job holding a
pretty-printed JSON object, the directory's name as the handle. The
second is a fixture deliberately shaped unlike anything niwa ships --
records nested under a glob, metadata on a transcript's first line,
fields under a nested key, the id as its own handle -- which writes a
second transcript line specifically so a reader that swallowed the
whole file would fail to parse rather than quietly succeed. The third
is the real Codex description, driven against a fixture written in the
envelope a real rollout uses: it is the test that fails if the declared
field paths, the nesting depth, or the glob stop matching what the
agent actually writes. Running the suite against a single store would
prove the Claude path works and prove nothing about whether the
mechanism generalizes; the alien shape is what makes "one capture" a
property the suite can fail on, and the Codex shape is what binds the
declaration to the record format on disk.

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

Files that may name an agent are excused by a recorded decision, not by
omission: `excusedAgentNamingFiles` maps each one to the declaration
that makes the capability it serves one agent's rather than a gap. The
first two were known from the start:

- `dispatch_plugins.go` registers plugins with Claude Code's own plugin
  system. `MarketplaceRegistration` (row 6) is declared
  AgentCannotReceive for Codex, so there is no second agent for that
  file to be neutral about.
- `job_state.go` reads Claude Code's harness job-state file, for the
  SessionStart guard behind `EphemeralSessions` (row 17,
  AgentCannotReceive for Codex) and for `niwa watch`'s review
  continuation, which is Claude Code harness surface throughout.

Three more were found by the completeness guard's own first run (below):
`instance_from_hook.go` (serves the row 17 hook path and reads the
context document only that agent's session would load),
`repo_resolve.go` (skips one agent's own directory when enumerating a
workspace's repositories -- a directory to ignore, not a delivery to
make), and `watch.go` (the review continuation is that agent's harness
surface throughout; row 20 is declared NoSuchConcept for the other).
Naming every exclusion against a declaration is the point: an exclusion
a reader can check against a row is a different thing from an exclusion
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

Three guards keep a passing scan meaningful over time.

`TestDispatchScanDetectsWhatItForbids` is the control: it runs the
detectors against fixture source written to contain exactly what they
look for -- two constants, two literals, plus a comment naming both and
a substring-bearing literal that must not fire -- and fails if the scan
comes back clean. A detector that matched nothing would pass the two
main tests forever, and the first person to reintroduce a hardcoded
agent would get a green run; the control is what makes that impossible.

`TestNoUnreviewedAgentNamingInThisPackage` is the completeness guard,
and it is worth recording that its first version had a hole a reviewer
found. That version enumerated files whose names begin with `dispatch`
and checked each was scanned or excused -- which leaves a hole the
width of a filename: a new `internal/cli/codex_launcher.go` matches no
name pattern, lands in neither list, and is free to hardcode an agent
on every line while every other test stays green. And that is the
*natural* name for the file the next change adds, so the hole sat on
the path of least resistance rather than at the end of an adversarial
one. The scan's own file list already disproved the name-pattern
premise: `session_records.go` is on the dispatch path and shares no
prefix with it. The guard is now inverted. It ranges over every
non-test file in the package and asks one question that needs no name
pattern: does this file name an agent, and if so, has somebody decided
it may? A dispatch-path file may not -- the two scans fail it -- and
any other file that does must appear in `excusedAgentNamingFiles` with
its declaration. The inverted guard went red on its own arrival at
four sites in three files -- `instance_from_hook.go`,
`repo_resolve.go`, `watch.go` -- each now excused on a checkable
declaration, and the reviewer's exact cheat was verified closed by
planting the file they described: it failed with
`codex_launcher.go:8: names agent.AgentCodex`. A guard whose failure
mode was found and fixed is worth more here than one presented as
having been right from the start.

`TestDispatchPathFilesAreAllPresent` keeps the scanned list from
quietly shrinking: a file removed from `dispatchPathFiles` but still on
disk would silently drop to the weaker excusable rule, so every listed
file must exist, every excused file must exist, and no file may appear
on both lists -- it cannot be held to two rules. The scan itself also
refuses to pass vacuously: a listed file that has gone missing is a
hard failure, because a scan that passes by not looking is worse than
no scan.

One more scan belongs beside these, aimed at the other direction of the
same failure. The scans above catch a delivery decision made outside
the declaration; `TestEveryLaunchSpecFieldIsRead` catches a declaration
nothing consumes. It reflects over every field of `LaunchSpec`,
`SessionRecords`, and `LaunchFlags` and asserts each is selected
somewhere in `internal/agentplan` or `internal/cli` -- because a field
nobody reads is precisely the shape that closed the prior attempt, and
a completeness suite that checks a field is *populated* does not catch
it: a populated field nothing reads is the failure. It was demonstrated
red by adding a decorative field, failing with
`LaunchSpec.UnreadDecoration`. Its limit is stated as plainly as its
power: a field name coinciding with an unrelated selector counts as
read, so it proves a field is read *nowhere*, not that it is read for
the right reason -- the coarse net under the finer tests, not a
replacement for them.

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
`dispatchAttach` (`internal/cli/dispatch.go:147`) takes the spec, the
handle, and the working directory the session ran in, looks the binary
up, runs the agent's own resume arguments against the handle with
inherited stdio, and propagates the outcome -- all of it written once,
with only `spec.ResumeArgs` and `spec.Binary` varying. The working
directory is threaded because a resumed session runs where it ran: an
agent that narrows its own session list by working directory would
otherwise refuse to find a session started elsewhere, and a session
reopened in the terminal's directory rather than its own is a different
session in every way that matters to the work in it.
`TestDispatchResumeUsesTheDeclaredVerb` substitutes the seam and asserts
the declared verb and a non-empty handle arrive, with everything around
the exec -- the lookup, the non-fatal failure handling -- the same code
whoever was launched.

The "one verb" property has a test only a cross-agent run can be:
`TestDispatchSharedHalfRunsForEveryAgent` runs one dispatch per
launchable agent through the same code and asserts the parts that must
not vary do not -- the mapping is written and records that agent, the
handle capture returned reaches resume unchanged, and resume is told
which directory the session ran in -- while the binary and the verb
come from the declaration. Two resume implementations selected by a
conditional at the call site is the named failure mode, and it is not
one a per-agent test catches, because each half would pass its own; the
shared half is only shown shared by running it for more than one agent,
and the test skips itself with a complaint if the table ever shrinks to
one.

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

The reaper's gate reads the mapping's recorded agent and spares the
instance unless the declaration says presence is a faithful proxy
(`livenessUnreadable`, `internal/cli/reap.go:151`). Three shapes reach
that gate and all three mean the same thing to the sweep: an agent
outside the accepted set -- a mapping written by something this build
does not understand, which would otherwise be spared silently forever, a
case nobody had considered until the gate was written to enumerate its
inputs; an agent niwa launches no worker for, which has no record store
to read at all; and an agent whose records are never removed, whose
store answers a different question than the one being asked. No
evidence, and with no evidence it must not act. Sparing an instance
nobody is using costs a directory; reclaiming one a resumable session
still lives in costs the work in it, which is the failure this whole
rule exists to prevent.
`TestReap_MappingWithNoLaunchableAgent_Spared` is the safety property as
a test: a mapping for an agent with no launch spec is spared even though
the Claude-shaped store has nothing for it -- the state that, before this
branch, read as "the developer deleted this session" and destroyed the
instance.

**Sparing is visible, not silent.** A sweep that spares something and
says nothing is indistinguishable from a sweep that found nothing, so
the instances would accumulate with no symptom until somebody counted
directories -- a declared gap nobody can observe at runtime is only
half declared. So the sweep reports what it spared and why
(`reportSparedInstances`): grouped by reason so a dozen spared
instances say one thing rather than a dozen, with the paths listed so
the note is actionable, and with `niwa destroy <instance>` named as the
way out. It goes to stderr because every opportunistic sweep runs
underneath a command the developer actually asked for, and this is a
note about the sweep, not a result of that command. `niwa reap`'s own
help now describes the sparing rule alongside the entry-present one,
so the behavior is discoverable before it is encountered.

**Codex declares `LivenessNone`.** This is the honest declaration,
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
orphans (`selectBackstopTargets`, `reap.go:367`) and deliberately never
touches a mapped instance. That is the next feature's work, named as such
so the next author inherits the boundary rather than rediscovering it;
this feature's job is the narrower one of not introducing data loss. The
acceptance shape holds on this branch: with a Codex mapping present, the
sweep spares the instance and says so; no TTL or mtime rule for mapped
instances appears in the delivered code.

**The unmapped backstop's live-worker guard had to widen too, and it
costs something.** The mapped path above is only half the reaper. The
other half ages *unmapped* dispatch instances on name and mtime, for the
orphan the deferred rollback cannot reach: a detached worker outlives a
niwa killed before the mapping is written, so the instance holding it is
unmapped and stays unmapped. Thirty minutes later an opportunistic sweep
-- one runs at the top of every `create` and every `dispatch` -- finds it
eligible on name and age. The only thing between that and destroying the
directory a worker is writing in is the live-worker guard, and that guard
read one agent's harness job state, because for as long as niwa launched
one agent that was the whole question. Detaching a second agent's worker
made the case reachable, so this branch reaches it: `instanceHasLiveJob`
is now joined by `instanceHasRecordedSession`, which asks every launchable
agent's own declared store whether a session is rooted in the instance.

It is worth being plain about how much that second call adds and to
which agent. Claude's declared store is `~/.claude/jobs` with the cwd on
each record -- the same tree `instanceHasLiveJob` reads, asked the same
question -- so for Claude the two guards are redundant rather than
complementary and the new one changes nothing. This is a Codex-shaped
fix written agent-neutrally, which is the right way to write it and a
weaker claim than it would be to say the guard is stronger than what it
replaces. It is not stronger; it is wider.

There is one exception, and it is a Claude-path behavior change worth
naming rather than filing under "no change". The two guards compare
paths differently: `instanceHasLiveJob` cleans, `instanceHasRecordedSession`
resolves symlinks first. So a Claude instance whose recorded working
directory and whose instance path are one directory under two spellings
-- equal once resolved, unequal once merely cleaned -- is now spared on
the backstop line where it would previously have been reaped. It takes a
symlinked instance path to reach, it is in the direction the backstop
should err, and it is arguably a latent bug the wider guard fixed on its
way past. It is still a change to what happens to a Claude instance, and
it is the reason the eventual collapse of the two calls has to go toward
the resolving one.
`TestBackstop_LiveWorkerOfEveryAgent_Spared` runs the scenario once per
launchable agent and is honest about the same asymmetry: the Claude
subtest is a control that passes with the new guard removed, proving the
generalization broke nothing, and the Codex subtest is the one that fails
without it. A third agent arrives covered and lands in whichever role its
declaration puts it in.

The cost lands on the same declaration. For an agent with
`LivenessRecordPresence` the guard tracks the session and the sparing
ends when the session does. For `LivenessNone` it does not: the record
stays, so the answer stays yes, and an unmapped instance a Codex worker
once ran in is spared by every future sweep until somebody runs `niwa
destroy`. That is the mapped path's declared cost reaching the backstop,
and it is reported the same way -- `selectBackstopTargets` returns the
spared instances with a reason and `reapBackstop` prints them, so the
class is observable rather than a slowly filling disk.

The narrow safe answer was taken deliberately over two alternatives that
look better and are not. Reaping anyway is the data loss the guard
exists to prevent. Making the guard agent-aware in the shape the mapped
path uses -- consult the declaration, act only on a faithful signal --
cannot help, because for `LivenessNone` there is no faithful signal to
act on; it would resolve to exactly this behavior with more code.

**Explicitly not built here: the process-level check that actually
closes it.** The backstop is not asking "did this agent's session end".
It is asking "is anything running in this directory right now", and that
question has an agent-neutral answer one level down: whether any live
process has a working directory inside the instance. It needs no
cooperation from either agent's bookkeeping, it is strictly stronger than
both readings the guard uses today -- it would also catch a Claude worker
whose job-state file went missing, which nothing here can -- and it makes
the sparing temporary again for every agent. It is not built. niwa has
the beginnings of the machinery in `internal/worktree/procinfo_linux.go`
(`pidStartTime`, `readPPID`), and the filename says the portability
question it raises: what a non-Linux host offers instead is unanswered,
and answering it is more than a launch feature should carry. Named here
so the next author inherits the boundary.

**Rejected: record-presence over the rollout store.** Stated above: a
rule whose trigger is an action nobody performs presents a working
reclamation path that does not exist, and it would purchase a
generalization of `sessionLive` for a branch that never runs.

**Rejected: inferring liveness from the worker process.** The reaper
runs long after the dispatching process is gone, holds no pid, and a
finished `codex exec` leaves a resumable session with no process at all
-- process absence is not session death for either agent.

> **Update — both "explicitly not built" paragraphs above are superseded,
> and one of them was pointing at the wrong thing.** The reclamation gap
> this decision deferred is closed in
> `docs/designs/current/DESIGN-codex-instance-reclamation.md`, and not in
> the shape predicted here. The process-cwd check named as "the check
> that actually closes it" was not built and is no longer the recommended
> route: Codex takes a real advisory lock on a per-session file for as
> long as a writer is attached, measured against the same 0.147.0 build,
> and probing one known path answers "is a writer attached to this
> session" better than a scan of every process answers "is anything
> running in this directory". It also dissolves rather than answers the
> portability question this decision raised, since `flock` is available
> wherever niwa runs.
>
> What survives unchanged is the reasoning immediately above. Process
> absence really is not session death, so the lock could only ever be the
> guard; the trigger is record staleness, which this decision did not
> consider because the rollout's mtime had not been measured. The
> name-plus-TTL analogue proposed here was close to right about the
> trigger and wrong about what makes it safe.

### Decision 8 -- the Codex launch shape (R7, R8)

Every element here is settled by measurement against codex-cli 0.147.0,
and each one is now a field of the Codex spec or a rule of the launcher
rather than advice. The launcher's detached half is
`startDetachedWorker` (`internal/cli/dispatch_launcher.go`), selected by
`spec.Mode`; the argv is pinned whole by `TestCodexLaunchArgv`, and the
detached mechanics run against a real process in
`TestStartDetachedWorker`, which checks the launch returns without
waiting (and that the worker had not already finished when it did, so
the test proves something about detaching), that the worker gets a
session of its own, and that the two output streams land in separate
files unmerged.

- **Start-and-release, not run-to-completion.** `codex exec` runs the
  whole turn in the foreground, so the run-and-wait shape would park
  the dispatch for the entire task. Measured: `cmd.Start()` with
  `SysProcAttr{Setsid: true}` and stdio redirected to files returns in
  about 670 microseconds; the child survives the parent's exit, is
  reparented, completes its turn, writes its rollout, and ignores
  signals sent to the launcher's process group -- so a Ctrl-C on the
  niwa CLI does not take the worker with it. This is `LaunchDetached`,
  the launch-mode field's second answer, landed in the same change as
  the field (Decision 1); the launcher releases the process after
  `Start`, waiting to learn nothing from an exit status that -- for
  this agent -- does not report task success anyway (Decision 10).
- **`exec.Command`, never `exec.CommandContext`, on the detached
  path.** A released child must not be tied to the dispatch's context:
  a context cancelled when dispatch returns -- which is every dispatch
  -- would kill the worker the instant launch finished. The
  backgrounded path keeps `CommandContext` precisely because the
  process it starts is the hand-off, not the worker.
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
  would corrupt the one and bury the other. The files live inside the
  instance at `.niwa/dispatch-<binary>.out` and `.err`, mode 0o600,
  named by the binary so two agents' logs could never be confused and
  so the function that opens them names no agent. Non-empty stderr is
  not failure; exit codes are (Decision 10).
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
- **A session cannot be resumed while its turn is still running, so
  dispatch does not try.** Measured: with a worker mid-turn, `codex exec
  resume <id>` exits 1 in under a second with "thread-store conflict:
  thread `<id>` already has an active writer", raised by
  `codex_core::session`. The interactive verb, which is the one niwa
  runs, was measured separately under a real pty and refuses with the
  same error during TUI bootstrap. The mechanism is a per-thread writer
  lock at `$CODEX_HOME/thread-writer-locks/<id>.lock`, created when a
  process opens a thread and removed when it exits, which is what makes
  the behavior deterministic rather than racy. It clears when the turn
  ends; the end-to-end resume in this branch's evidence was against a
  finished session, which is why this went unnoticed until it was asked
  about directly.

  The refusal is clean rather than merely survivable, which is worth
  recording because it removes a whole class of worry: across a rejected
  resume the worker's rollout was byte-identical, the worker completed
  normally and knew nothing about the attempt, no second rollout was
  forked, and no model spend was incurred -- the refusal happens during
  session bootstrap, before a turn starts. A lock left behind by a
  SIGKILLed worker does not brick the thread either; a later resume
  re-acquires it. It matters because dispatch's last step without `--detach`
  is to resume the session it just started, when the worker is mid-turn
  by construction: every non-detached Codex dispatch would have ended in
  a store-conflict error from a dispatch that in fact succeeded.

  The fix is a declared field rather than a branch: `ResumeDuringTurn`
  on the launch description, true for an agent that backgrounds its own
  session and expects to be attached to, false for one that holds an
  exclusive writer for the length of the turn. False is the default, so
  an agent whose behavior here is unmeasured gets niwa's honest sentence
  instead of an error from its own store. The alternatives are worse for
  ordinary reasons rather than dangerous ones: waiting for the turn would
  make dispatch block for as long as the task, and there is nothing to
  force -- the lock is what the agent uses to keep a second writer out,
  so retrying just produces the same error more slowly.
  `TestDispatchDoesNotResumeAnAgentThatRefusesMidTurn`
  binds it in both directions against a substituted spec, so it tests
  the declaration and not one agent's row.

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

The override is per-agent launch data like every other argv element: it
lives in the spec's `WorkdirGrantArgs`, with the absolute working
directory substituted into a single verb in the last element by
`formatWorkdirGrant` -- never shell-composed, and yielding nothing at
all for an agent that declares no grant or a call with no directory,
since a grant naming no directory would either fail to parse or grant
something nobody asked for. The grant precedes the pass-through flags
in the argv, so a developer who asks for a posture explicitly gets the
last word on it -- which is also why the Codex spec still spells
`--sandbox` as its permission-mode flag: niwa passes nothing there of
its own, but a deliberate developer intent has somewhere to go.

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
  why `TestCodexLaunchArgv` pins the whole argv including the
  inline-table spelling, not merely that some `-c` argument is
  present.
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

One constraint on any future posture choice, recorded here because
nothing in this design depends on it and the next person choosing a
posture would otherwise rediscover it. Measured in a live session at
`danger-full-access`: the model's shell tool sees the launching process
environment regardless of what `shell_environment_policy` declares,
including a CLI-layer `inherit = "none"`, which is the
highest-precedence layer and needs no trust. The policy itself works --
the same override applied through the sandbox surface strips the
environment to nothing -- so what this measures is that one posture's
shell tool does not take the policy-aware route, not that the policy is
broken. Whether a sandboxed posture behaves the same way is untested,
for a host reason unrelated to Codex, and the untested half is the half
that matters: `danger-full-access` is the posture almost nobody runs.
The consequence for a chooser is narrow and real -- dispatching at
`danger-full-access` would mean the environment policy does not
constrain what that worker's shell sees. The route this design chose
does not go there.

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

The delivered posture follows, and the detached launch sharpens it:
dispatch never observes a Codex worker's exit at all. The launcher
releases the process after `Start`, its own comment naming the reason
-- nothing there is waiting to learn anything from an exit status that
does not report whether the work succeeded anyway -- so dispatch's
success message claims exactly what it knows: a session was launched
and captured, never that the task succeeded. The worker's stdout and
stderr land in the instance's log files, which is where the truth about
a run lives, and the published guide says so in as many words: read the
last message or the run's output rather than the exit code, because a
worker that could not write still exits 0 and every API failure --
quota exhaustion included -- exits 1 alongside every other error.
Resume is the one place an exit status is observed, and `dispatchAttach`
propagates it without interpretation.

Quota exhaustion stays a documentation obligation rather than a code
path on this branch, and that is a narrower delivery than this design
first specified. The condition is detectable only by parsing the error
payload for its markers (`usage_limit_reached`, `UsageLimitReached`,
`CreditsDepleted`), and the detached launch dissolved the surface that
was going to do the parsing: dispatch has returned before any error
payload exists, so the payload lands in the worker's stderr log, where
no launch-time classifier can see it. What survives is the boundary the
classification existed to protect: no code path switches agents,
retries, or falls back on any error, quota-shaped or otherwise --
acting on the condition is a policy decision nobody has made -- and the
guide names quota exhaustion explicitly so a developer reading an exit
1 does not misread it as a niwa failure. A log-reading classifier
remains open to a follow-on if reading the logs proves too slow a
diagnosis in practice.

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
  `TestDispatchResumeUsesTheDeclaredVerb` pins both. (The Codex
  delivery later widened it again to
  `(spec agentplan.LaunchSpec, handle, workdir string)`, threading the
  working directory for the reason Decision 6 gives; that change
  belongs to the delivery, not to the no-behavior-change proof.)
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
seams were not modified or deleted; new assertions were added.

"No behavior change" is, stated precisely, four user-visible changes --
three incidental to routing the gate through the declaration and one a
fix -- and naming all four is a stronger and truer claim than a flat
none:

- **The refusal's wording.** It now carries the declaration's own
  reason, and it names the agents the table says *can* be launched,
  enumerated from the declarations (`launchableAgentsHint`, backed by
  `agentplan.LaunchableAgents()` and pinned against the table by
  `TestLaunchableAgentsMatchesTheDeclarations`) rather than spelled
  into the string. The old message ended "Set NIWA_DISPATCH_HARNESS=claude";
  dropping the suggestion would leave the developer who hits the
  refusal without the one fact they are missing, and hardcoding it
  back would go stale, silently, the day a row flips -- at exactly the
  moment it is being read.
- **The gate now runs even when the workspace config cannot be
  loaded.** Previously the whole gate sat inside the config-load
  success branch, so `NIWA_DISPATCH_HARNESS=codex` plus an unreadable config
  skipped the check and launched Claude anyway -- a worker the
  developer explicitly asked not to get. The gate now resolves the
  agent from the environment alone and refuses. This is the one
  genuine fix riding along, stated as one.
- **`--model` help lists only the portable categories,** no longer the
  concrete model names. The old text was one agent's vocabulary in a
  flag that reaches whichever agent the workspace resolves to, so it
  would have started lying the day a second agent shipped. The
  concrete names still surface where they are agent-specific by
  construction: the unrecognized-value warning, which knows which
  agent it resolved against.
- **The preflight error names the binary rather than the product** --
  "install it before dispatching" against the declared binary name,
  not "install Claude Code" -- for the same reason: the sentence is
  printed for whichever binary the declaration named.

One further test closes a hole that naming the seams still leaves
open. Three of the contract tests -- the preflight, the printed hints,
the resume verb -- resolved their expected values from the one real
agent's spec, which proves the dispatch reads *a* description, not
that it reads the *resolved agent's*: hardcoding the agent would have
passed all three, and the difference between those two claims is the
whole feature. `TestDispatchUsesTheResolvedAgentsSpec` substitutes,
through the `dispatchLaunchSpec` seam that exists for exactly this, a
description for an agent that does not exist -- different binary,
different resume verb, different management verbs -- and asserts the
preflight, the hints, and the resume all follow it, with nothing from
the real table appearing. It is the same move the capture suite makes
with its second fixture store, applied to the launch surface.

**PR 2 -- Codex as the second implementation -- is the branch this
document rides.** It adds the Codex `LaunchSpec` row (with the
launch-mode field and its Claude value, both arriving with their first
consumer), the detached launcher path, the rollout record description,
the `LivenessNone` declaration and the sparing report beside it, the
per-invocation trust override on the launch argv, the row 22 flip with
no `Requires` edge, the regenerated guide section with its surrounding
prose edited in the same change (R16), and the transformation -- not
duplication -- of the functional scenario that pinned the refusal
(R21). It is the same scenario asserting the other side of the same
declaration: it launches a Codex worker through a fake binary and
asserts the row is declared implemented, the guide has stopped
publishing it as a gap, the mapping records the agent, the printed hint
is the agent's own verb, and the launch argv carries what the real
binary requires while omitting what would break capture. The same
scenario matters, because the alternative -- a parallel scenario beside
the old one -- is how a table and a test start disagreeing. The guide
gained a hand-written background-dispatch section covering the three
caveats a user needs -- the unbriefed worker, the exit status, the
unreclaimed instance -- and one generated row title changed: row 17 now
reads "An instance provisioned automatically for a session niwa did not
launch", because "for each dispatched session" became actively
misleading the moment dispatch provisions one for Codex too. The gate,
capture, resume, and reaper themselves changed only by what the new
declaration says: their code landed in PR 1 and reads the table.

### Decision 12 -- selection gets its own flag name, and the durable home is the file niwa owns (R1, R1a)

Added in the second round of this chain, after the delivery was judged
unreachable. The PRD's original R1 forbade a selection flag; what
follows is the design half of its replacement.

**The flag is `--harness` on `niwa dispatch`, and `--agent` is left
alone.** The collision is real: `--agent` already exists on that command
as the subagent-type passthrough. What settles it is that the launch
description's own field is named `SubagentType`
(`agentplan.LaunchFlags`), bound to the string `"--agent"` in the Claude
row -- so the codebase already calls the concept by its right name and
only the user-facing flag disagrees. Three options were live:

- **A new name, `--agent` untouched.** Chosen. Additive, breaks no
  script, and leaves the passthrough meaning what it has always meant.
  The name itself went through one revision: the flag first shipped in
  this chain as `--launch-agent` and was renamed to `--harness` before
  merge, together with the environment variable and the host key, so all
  three surfaces of one setting say one word. The reasoning is in **the
  vocabulary is "dispatch harness"** below.
- **Rename the passthrough to `--subagent-type` with `--agent` as a
  deprecated alias, then repoint `--agent` later.** This is the right
  eventual fix and is recorded as follow-on rather than dropped. It
  cannot be the answer now: during its own deprecation window `--agent`
  still means subagent type, so selection needs a distinct name today
  regardless. It is strictly more work now for a payoff a release later.
- **Repoint `--agent` immediately, no alias.** Rejected. It is a loud
  break for `--agent general-purpose` and a *silent* one for `--agent
  claude`, and "the outcome is near-identical" is not a standard to ship
  a breaking flag on -- near-identical is the claim that turns out to
  have an exception nobody enumerated.

The flag belongs on `niwa dispatch` and nowhere else. `resolveSessionAgent`
has one call site, nothing else in niwa is agent-specific at invocation
time because every apply prepares every agent, and the functional
scenario pinning `apply --agent codex` as an unknown flag stands
unchanged.

**The durable home is `~/.config/niwa/config.toml`, never the
workspace's `.niwa/`.** This corrects a premise this design was written
under. Two different files share the word "global", and only one is
writable: `<workspace>/.niwa/` is materialized from a source repo and
rotated wholesale on refresh, so a write there produces a setting that
works and then silently stops. `~/.config/niwa/config.toml` is a plain
local file niwa owns, never materialized, already written by `config set
global` through `SaveGlobalConfigTo`, and already home to a row of
dispatch-scoped host defaults of exactly this shape -- `dispatch_model`,
`remote_control_on_dispatch`, `keep_alive_on_dispatch`. The harness
default is the same kind of setting; putting it anywhere else would make
it the odd one out. So `niwa config set default-dispatch-harness <agent>`
writes `[global].default_dispatch_harness` there, with a matching
`unset`.

**The vocabulary is "dispatch harness", on the three surfaces niwa owns
end to end.** The setting first landed with a different name on each
surface -- `--launch-agent`, `NIWA_AGENT`, `[global].default_agent` --
which reads as three settings rather than three lifetimes of one, and
the guide had to spend a table teaching that they are the same knob.
They are now `--harness`, `NIWA_DISPATCH_HARNESS`, and
`[global].default_dispatch_harness`. "Harness" says what the value picks
without colliding with `--agent`'s established meaning on this command,
and it names the thing precisely: the coding agent that harnesses a
dispatched turn.

`NIWA_AGENT` is renamed rather than aliased. It shipped in v0.9 and is
therefore a user-visible break, taken deliberately: it is set per shell,
so the fix is one line in a profile, and carrying a permanent alias for
a variable one release old would preserve the split this rename exists
to close.

That argument only holds if the developer is told which line, so
`niwa dispatch` prints a notice when the old name is set and the new one
is not. Without it the break is silent in the worst way available: no
rung held a bad value, so resolution raises no error, and a profile that
has said `NIWA_AGENT=codex` for a release launches claude with niwa
saying nothing. The notice reports and never resolves -- reading the old
name as a fallback is the alias this decision rejected. It is scoped to
the case that needs it: both set is a developer mid-migration who has
already found the new name, and gets silence.

`[workspace].default_agent` keeps its name and is the one rung that does
not say "harness". It is also shipped, but it lives in committed
`workspace.toml` files across every workspace that sets it, and those are
not one line in one developer's profile. The asymmetry is deliberate and
is stated in the guide's table rather than left for a reader to notice.
Renaming it, with the old spelling accepted as a fallback, is follow-on
work.

**Precedence: flag > `NIWA_DISPATCH_HARNESS` > `[workspace].default_agent` >
`[global].default_dispatch_harness` > claude.** The host default is the weakest
rung above the built-in. The argument is consistency rather than
ergonomics: `RemoteControlOnDispatch` and `KeepAliveOnDispatch` both
document a downstream workspace value outranking the host default, and
`DispatchModel` documents the flag always winning. A third key in the
same file with the opposite polarity would make that file's precedence
something a reader looks up per key instead of learning once, and
unpredictable-per-key is a worse outcome than any single key's
ergonomics. The personal-machine case that argues for the other order --
"on this machine I use Codex" -- is already served twice, by `NIWA_DISPATCH_HARNESS`
in a shell profile and now by the flag per dispatch.

**Rejected: refusing to write anything.** An earlier framing of this
work assumed no durable local target existed and proposed that `config
set` fail with directions to the config source repo instead. That would
have been the right call had the premise held, on the principle that a
command which appears to work and silently does not is worse than one
that refuses with directions. The premise did not hold, and the
principle is what identified the real target rather than what blocked
it.

### Decision 13 -- the launch mode is a function of the agent and the invocation, not the agent alone (R7a)

Added in the third round, correcting Decision 1 rather than extending it.

`LaunchMode` was declared as a field of the launch description, so the
process model became a property of the agent: Claude backgrounded,
Codex detached, decided before any flag was read. `realDispatchLaunch`
switches on `spec.Mode` and nothing else. `--detach` was wired to a
different question entirely -- whether an attach step runs after the
launch -- and the two never met.

For one agent that was invisible, because `claude --bg` backgrounds its
own session and there is no foreground alternative to choose. For Codex
it produced a command that ignores its own flag: the worker was detached
whether or not the developer asked for it, and dispatch then explained
that Codex would not hand over a session whose turn was still running.
Both statements are true and the combination is a defect. The
un-attachability is real and is Codex's; being unable to *watch* the work
is ours, and we introduced it by detaching a process that runs its turn
in the foreground natively.

So the decision that was "which mode does this agent use" becomes "which
mode does this invocation want, of the ones this agent can offer". A
runner that backgrounds its own session offers one mode and the flag
changes nothing about how it is started. A runner that executes the turn
in the foreground offers both: run it in the terminal, or detach it.

**What the foreground path must not quietly drop.** Three properties
were measured into Decision 8 and are easy to lose to an implementation
that reaches for "inherit stdio and be done":

- **Stdin stays `/dev/null`.** The measured hang is on stdin
  specifically -- `codex exec` reads it in addition to the positional
  prompt and blocks on an inherited or open one, 20 seconds of nothing
  with no rollout and no API call. Attaching the terminal's stdout and
  stderr does not require attaching its stdin, and a foreground worker
  that hangs is a worse outcome than the detached one it replaces.
- **The prompt stays one argv element.** Unchanged, and unchanged for the
  same reason.
- **Exit status still is not task success.** A read-only sandbox failure
  exits 0. What a foreground run can honestly report at the end is that
  the turn ended.

**`--json` belongs to the detached path.** It is there so niwa can parse
a log nobody is watching. In the foreground the developer is the reader,
and handing them an event stream instead of the human output would be a
regression justified as consistency. Capture is indifferent: the session
id comes from the rollout record on disk, written about 0.7 seconds in,
not from stdout. That is also what lets the mapping be written while a
foreground turn is still running rather than after it.

**Ctrl-C changes meaning, and that is correct.** A detached worker
survives a signal to the launcher's process group, which is what
`Setsid` buys and what `TestStartDetachedWorker` holds. A foreground
worker shares the terminal's group and dies with it. That is the
behavior a developer expects from a command running in front of them,
and it is a difference worth stating rather than discovering.

**What this does to the mid-turn resume refusal: it relocates, and gets
more load-bearing rather than less.** The refusal measured in Decision 8
-- a session whose turn is running cannot be opened, refused by the
store's per-thread writer lock -- stops being something niwa collides
with and becomes something the developer can collide with.

On the foreground path there is nothing left to refuse. niwa never
resumes, because the developer is already watching the turn; the attach
step that used to run into the lock does not exist on that path. On the
detached path the collision is still live, but the party who can cause it
is now the developer: they were handed `codex resume <id>` and nothing
stops them running it while the worker is still going, at which point
they get `thread-store conflict` phrased in Codex's internal vocabulary
rather than in terms of their session.

So the load moves from a gate to a sentence. What has to be right is the
guidance printed alongside the handle on a detached dispatch -- it has to
say the session is resumable *once the turn ends*, because that is now
the only place the constraint is communicated. Getting it wrong costs a
developer one confusing error rather than costing niwa a broken attach,
which is a smaller failure and an easier one to leave un-noticed.

`ResumeDuringTurn` itself survives with a narrower consumer. It no longer
decides whether dispatch attaches on the foreground path, because that
path has no attach; it decides it for an agent that backgrounds its own
session, which is the only shape where niwa still performs a resume the
developer did not ask for. No agent ships today that both backgrounds its
own session and refuses a mid-turn handover, so the field currently
guards a combination nobody has -- which is a reason to keep it declared
and a reason not to claim it is doing more than it is.

**Rejected: keep detaching, and tail the log.** niwa could have followed
`.niwa/dispatch-codex.out` and presented that as the attach. It reads as
close to the same thing and is strictly worse: a second copy of the
output, a stream to keep in sync with a process niwa no longer controls,
and no answer for Ctrl-C. Running the process in the foreground is not a
workaround for the absence of attach -- for this runner it is what attach
would have been.

**Rejected: block until the turn ends, then resume.** Waiting for the
writer lock to clear and then opening the session would put the developer
in the conversation, after making them wait the length of the task with
nothing on screen. The foreground path gives them the same endpoint and
the work to watch on the way.

### Decision 14 -- the session mapping outlives a config-snapshot refresh (R11)

Recorded here because it would otherwise survive only in a pull-request
body and a deleted plan, and it is the kind of fact that gets
rediscovered expensively.

Dispatch writes its mapping to `<workspace>/.niwa/sessions/<id>.json`.
That path is inside the directory the snapshot writer rotates wholesale
on refresh, and exactly two things were carried across the swap:
`instance.json` via `preserveInstanceState`, and `dispatch-briefs/` via
`preserveDispatchBriefs`. `sessions/` was not. So a refresh destroyed
every dispatch mapping in the workspace -- the resume handle
unrecoverable, and the reaper's mapped sweep losing the join it decides
on, dropping every dispatched instance through to the name-and-age
backstop where only the record-store guard stands between a live worker
and its directory being deleted.

The trigger is narrower than every dispatch and wider than it looks. For
a GitHub-sourced snapshot the swap fires only on upstream drift, so the
event is a teammate pushing any commit to the shared config repo; for a
non-GitHub source it fires on every reconcile. Either way it is somebody
else's unrelated change destroying your session handles.

The fix is a third preserver beside the two that were already there,
which is the shape the existing code had been asking for -- the comment
above `preserveInstanceState` already names issue #74 as the structural
version, where niwa pulls only files it knows about from upstream and the
state-versus-source distinction at this seam stops being something each
new state file has to remember to opt into.

**What it costs, stated rather than discovered.** Preserving the
directory wholesale means a stale or corrupt mapping now survives
forever, where a refresh used to clear it. Nothing prunes that store. The
reaper deletes a mapping when it reclaims the instance, so the normal
path stays clean; what accumulates is mappings whose instances were
removed by hand. That is a smaller cost than losing live handles and it
is not zero.

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

What it does keep is worth stating, because the loose version of this
paragraph overstates the gap. The developer's own user-level Codex
configuration still loads, skills included: the first event of the
end-to-end run was Codex's own skills-budget notice, from a worker
launched at an instance root. niwa neither delivers nor withholds that.
What a root-launched worker loses is the *workspace's* delivery, which
is a materially smaller claim than "no skills" and the only one the
measurement supports.

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

**Update: the orientation half of this was taken as a follow-on.** Row 2's
reason is corrected and its state flipped, and an instance root now
carries an `AGENTS.md` composing the workspace layer and the documents a
Claude Code session reaches from there by `@import`. A dispatched worker
is oriented. The rest of this section still holds: rows 5, 8, 9 and 12
remain repository-scoped, for the reason stated above -- the contract has
no where-from axis -- plus one the follow-on measured, that delivering
the configuration half at the instance root would need a trust entry for
that directory, which niwa does not write today. Row 18 moved with row 2
rather than being delivered: it rested on the same false reason and is
now declared niwa's own debt. See `docs/guides/codex-agent.md`,
"Starting a session at the instance root".

## Decision Outcome

The launch route binds the way the plan and procedure routes already do:
a delivery beside its declaration, checked in both drift directions,
with completeness closing the hollow-delivery loophole. `LaunchSpec` in
`internal/agentplan` says what to launch, how to spell each intent,
where the session records sit and how to read one, what the handle is,
whether presence proves liveness, and how to step back in;
`internal/cli` does the launching, polling, and exec, and an AST scan
holds that no dispatch-path file names an agent. Capture is one reader
and one loop over a declared record shape, proven against three store
shapes, one of them the real Codex rollout envelope. The mapping
records its agent; the reaper reads the declaration, spares what it
cannot prove dead, and says what it spared and why. PR 1 shipped the
seam against Claude with a named-seam no-behavior-change proof; this
branch ships Codex as one table row plus the measured launch mechanics:
start-and-release under setsid, `/dev/null` stdin, split output files,
`--skip-git-repo-check`, posture via the per-invocation trust override,
`LivenessNone` with the reclamation gap declared, and reporting that
claims a session was launched, never that the task succeeded.

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

1. Resolve the agent from `--harness`, then `NIWA_DISPATCH_HARNESS`, then the
   workspace `default_agent`, then the host `default_agent`, then claude
   (R1, R1a). `niwa dispatch --agent` remains the subagent-type
   passthrough and never participates in that resolution; it names a role
   within whichever agent is launched (Decision 12).
2. Gate: `agentplan.Lookup(DispatchLaunch, agent)` plus
   `Producer.LaunchSpec()`. Unavailable, or implausibly implemented with
   no spec, refuses with the declared reason before anything exists on
   disk (R2).
3. Preflight `spec.Binary` on PATH before provisioning, so an absent
   binary fails with no instance and no mapping (R3).
4. Provision, arm rollback, launch with the spec's argv shape under the
   mode resolved from the agent's runner *and* the invocation's
   `--detach` (Decision 13). A runner that backgrounds its own session
   offers one mode and the flag does not change it; a runner that
   executes the turn in the foreground is run in the developer's terminal
   without `--detach` and detached with it, and the argv differs between
   the two only by the machine-readable stream flag (R7a).
5. Capture: poll the spec's record store, correlate by normalized cwd
   equality against the unique instance directory, return id and
   declared handle (R10, R11, R12).
6. Write the mapping with the agent recorded; print the spec's hint
   verbs against the handle. Then attach through the spec's resume
   arguments only where an attach is still meaningful: a backgrounded
   runner that was not detached and hands over a running session (R13).
   A foreground run has nothing to attach to -- the developer watched the
   turn -- and a detached run is the developer declining to attach.
7. Any later sweep reads the mapping's agent, resolves its spec, and
   applies the declared liveness rule -- or declines (R14).

## Implementation Approach

PR 1 is merged; its content and proof are Decision 11's. The Codex
delivery on this branch landed as one change, and its pieces are the
ones the decisions above describe:

1. `LaunchMode` on `LaunchSpec`, with Claude's value and Codex's second
   one arriving together, so the field lands with a live branch
   (Decision 1) -- `TestEveryLaunchSpecFieldIsRead` holds that it and
   every other field stay read.
2. The Codex `launchSpecs` row: binary, mode, `exec` leading arguments
   with `--json` and `--skip-git-repo-check`, the `WorkdirGrantArgs`
   grant, flag spellings, model vocabulary, the rollout
   `SessionRecords` (depth 3 under the sessions root, glob,
   first-line-only, `payload`-keyed field paths, `HandleSessionID`,
   `LivenessNone`), resume arguments, hint verbs. The binding and
   completeness tests confronted it with the whole contract on
   arrival.
3. The detached launcher path (`startDetachedWorker`) selected by the
   spec's mode: `cmd.Start()` with `Setsid`, `/dev/null` stdin, stdout
   and stderr to `.niwa/dispatch-<binary>.{out,err}`, release.
   `TestStartDetachedWorker` runs it against a real process;
   `TestCodexLaunchArgv` pins the argv whole.
4. Row 22 implemented -- with no `Requires` edge, per Decision 9 -- in
   the same change as the delivery (R4). The gate, capture, resume,
   and reaper tests changed branches by themselves, and
   `TestDispatchSharedHalfRunsForEveryAgent` became runnable the
   moment the table held two rows.
5. The sparing report and the widened `niwa reap` help (Decision 7),
   landing with the declaration that makes sparing a daily reality
   rather than an edge case.
6. The guide: gap section regenerated via the existing `-update` flow,
   the surrounding prose rewritten off the refusal, the hand-written
   background-dispatch section with its three caveats, the reclamation
   gap with its named next-feature owner, and row 17's regenerated
   title corrected (R15, R16).
7. The functional scenario transformed -- the same scenario, asserting
   the delivery; no refusal-asserting scenario survives (R21).

Exit criteria, all holding on this branch: the declaration suite, the
binding tests, the scans, the three-store capture suite, and the reap
tests green with the Codex row present; `gofmt -l .`, `go vet ./...`,
`go test -race ./...`; one manual dispatch-and-resume against a real
`codex` binary as a sanity check, never as the only coverage (N2).

## Security Considerations

- **No write at all to the developer's Codex configuration.** niwa
  passes no `--sandbox` flag and persists no trust stanza; the posture
  rides a per-invocation override whose grant is scoped to the one
  worker process and adds no stanza to the file (Decision 9).
  `TestCodexLaunchArgv` pins the argv whole; the named failures are a
  launch argv carrying a sandbox flag and the `sandbox_mode` config
  spelling, both of which make Codex itself write trust.
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
- The capture reader was proven against a store shape it was not
  written for before the second real store arrived -- and when it did,
  the Codex delivery was one table entry and one fixture, which is the
  claim demonstrated rather than promised.

Negative, accepted:

- A Codex-dispatched instance is not reclaimed by the mapping path.
  This is Decision 7's declared cost: rollouts never age out, niwa
  refuses to guess, and the directory stays until the developer
  destroys it or the next feature's backstop ages it. Declared in the
  table, published in the guide, and reported at runtime by the sweep
  itself, so it is never discovered by counting directories.
- A dispatched Codex worker starts with its task prompt and none of
  what the workspace delivers -- not the workspace's skills, its MCP
  servers, its session environment, or its composed orientation. What
  it does keep is the developer's own user-level configuration, which
  niwa neither delivers nor withholds; the loss is niwa's delivery
  rather than the agent's whole surface, and stating it the looser way
  would overstate a real gap. The gap, its cause, and the contract's
  inability to express it are recorded above; its closure is decided
  above this feature.
- The launch table is more hand-maintained data, and its honesty is
  only as good as its tests -- which is the accepted trade throughout
  this contract: maintenance errors are loud rather than impossible.
- Four user-visible changes shipped in a "no behavior change" PR:
  the refusal's wording, the gate running under an unreadable config,
  the `--model` help text, and the preflight error's noun (Decision
  11). Each named in the PR rather than hidden, and the alternative --
  keeping one agent's sentences beside the table they duplicate -- is
  the drift this work removes.
- The completeness guard scans every file in the package, so its
  discipline now rests on `excusedAgentNamingFiles` staying an honest
  record of decisions rather than on file naming -- a file like
  `reap.go`, which consults the declarations without naming an agent,
  passes on its content, not by being unlisted. Five excusals is five
  entries a reviewer must be willing to challenge.

## References

- docs/prds/PRD-codex-background-dispatch.md -- the upstream
  requirements (R1-R21, N1-N2) this design answers.
- docs/briefs/BRIEF-codex-background-dispatch.md -- the framing: four
  surfaces, two judgments on what counts as done, the two-PR split.
- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  sibling design: the declaration table, the routes, the binding
  posture this design extends to `RouteLaunch`, and the prior attempt's
  post-mortem.
- docs/guides/codex-agent.md -- the published guide: its generated gap
  list no longer carries row 22, and its hand-written
  background-dispatch section states the three caveats and the
  reclamation gap per R15.
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
