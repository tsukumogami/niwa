---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-codex-dispatch-posture-persistence.md
problem: |
  niwa elevates a dispatched Codex worker with a per-invocation trust
  override that dies with the worker's process, and every command niwa
  builds or prints to step back into that session -- the attach it runs
  itself, the resume hint after dispatch, and the resume command `niwa
  list` prints -- is assembled from the agent's resume verb and the
  session handle alone. The next process re-evaluates posture from the
  developer's own configuration, where the instance root isn't trusted,
  and silently drops to read-only with per-command approval. The PRD
  requires the grant on every re-entry surface through one testable
  production path, an explicit developer ask left intact, zero writes to
  the developer's own Codex configuration, and acceptance measured
  against the real binary with a control, at zero model cost.
decision: |
  The grant stays per-invocation and rides every re-entry command. One
  new file, internal/cli/dispatch_reentry.go, becomes the only non-test
  file in the package that reads the declaration's resume arguments or
  hint verbs: it exposes the argv constructor the attach exec uses and a
  renderer that produces every printed form -- the post-dispatch hint
  block with the grant on the resume line, and the single resume command
  `niwa list` and the attach-failure fallback print. Printed tokens pass
  a positive allowlist quoter, and a command is printed only when every
  token is free of control characters and the handle passes validation
  -- otherwise niwa prints nothing for that session. The dispatch-path
  scan gains a directory-wide rule that no other non-test file selects
  either field. Acceptance is a functional scenario against the real
  binary (codex-cli 0.149.0): one rollout, three turn contexts,
  workspace-write / read-only / workspace-write, and an isolated Codex
  home that ends holding no configuration file at all.
rationale: |
  A rule enforced by hand at three call sites is three copies of the
  rule, and a fourth surface misses it silently -- one file owning both
  declaration fields, fenced by a scan that ranges over the whole
  directory rather than a list, makes "every re-entry command carries
  the grant" something a test fails on, and covers the hint verbs,
  which are a second copy of the resume verb the argv rule alone would
  miss. Persisting a trust stanza would fix re-entry too, including the
  command niwa never printed, and is rejected on re-entry-specific
  grounds: retraction that depends on a reap that may never run, the
  developer's configuration as shared mutable state, and a grant that
  outlives the session it vouched for. The measurement stands on Codex
  recording the turn's resolved sandbox_policy at turn bootstrap,
  before the first model request, so the scenario needs no login and
  spends no model turn.
---

# DESIGN: codex dispatch posture persistence

## Status

Planned

This design owns the mechanism for carrying niwa's per-invocation workdir
grant through every command that steps back into a dispatched session:
where the re-entry argv and its printed forms are constructed, how they
are quoted and gated, how the one-path rule is enforced, and how
acceptance is measured. The upstream PRD owns the requirements (R1-R9)
and does not get re-opened here. This document is a sibling of
[DESIGN-codex-background-dispatch](current/DESIGN-codex-background-dispatch.md):
that design chose the per-invocation grant for the launch path and
rejected persistent trust on scope and footprint grounds (its Decision
9). This design extends the same grant, by the same reasoning plus
re-entry-specific grounds of its own (Decision 1 below), to the commands
that re-enter the session the launch created.

Every measured claim below was taken against codex-cli 0.149.0 on Linux,
under an isolated CODEX_HOME, at zero model turns, with the developer's
real `~/.codex` never written.

## Context and Problem Statement

`niwa dispatch` makes a Codex worker writable in a directory Codex has
never seen by putting a trust override on that one process's command
line: `-c 'projects={"<instance dir>"={trust_level="trusted"}}'`, with
the instance directory substituted into the declaration's single format
verb by `formatWorkdirGrant`. The grant is deliberately scoped to the
process -- the reasoning is recorded beside the declaration in
`internal/agentplan/dispatch.go` and argued in full in the sibling
design -- and it evaporates when the worker exits.

Nothing carries the grant to the next process. Three surfaces build or
print a command that steps back into a launched session, and none of
them carries it:

- `dispatchAttach` in `internal/cli/dispatch.go`, which execs the
  agent's resume verb against the handle with the instance as the
  working directory, when an agent hands a session over mid-dispatch.
- The hint block in the same file, which prints one line per declared
  hint verb (`<binary> <verb> <handle>`) after a successful dispatch --
  plus the attach-failure fallback a few lines below it, which repeats
  the resume command when the attach could not run.
- `sessionResumeCommand` in `internal/cli/list.go`, which builds the
  resume string `niwa list` prints beside a recorded session, from a
  `workspace.SessionMapping` that already carries `InstancePath`.

Each of them assembles from the resume verb and the handle alone, so the
resumed process re-evaluates posture from the developer's own
configuration and comes up read-only with per-command approval. The PRD
measured the drop against the real binary and ruled out the competing
explanations; this design takes the defect as established and owns the
fix's shape.

Three facts about the tree constrain that shape. First, the launch half
of the argv story already lives in `internal/cli`: `buildLaunchArgs` and
`formatWorkdirGrant` sit in `dispatch_launcher.go`, and
`internal/agentplan` is a declaring leaf that builds no argv. Second,
the declaration carries the resume verb in two fields: `ResumeArgs`,
which the attach and `niwa list` read, and `HintVerbs`, from which the
hint block builds its lines without reading `ResumeArgs` at all -- so
any rule about who may assemble a re-entry command has to cover both
fields, or a surface built from the hint verbs escapes it. Third, the
dispatch path already has a structural discipline: an AST scan in
`dispatch_layout_test.go`, whose agent-naming rule runs over a named
file list but whose completeness guard already ranges over every file
in the package -- and `list.go` is in neither's scope for re-entry
purposes, because no rule today says anything about who may assemble a
re-entry command.

The design problem is not what the grant says -- that is settled
upstream -- but how to make "every re-entry command carries it" a
property a test fails on, how to print a command containing braces,
quotes, and equals signs so a paste survives a POSIX shell and says
what it does, and how to measure the result against the real binary
without a login or a model turn.

## Decision Drivers

- **One production path or the rule is a wish.** R1 defines the covered
  set by a rule, not a list, and the PRD is explicit that the rule needs
  a testable anchor: one shared construction, and a test that fails
  anything else that assembles a re-entry command.
- **The declaration is the only source.** The grant on the re-entry path
  comes from the same `LaunchSpec` the launch path reads, keyed by the
  agent the session mapping records. No second table (R2).
- **Restraint over parsing.** An explicit posture the developer appends
  wins because the binary resolves an explicit sandbox selection above
  the trust-derived default. niwa never strips, rewrites, or refuses
  what the developer typed (R3).
- **Zero footprint, asserted not assumed.** Elevating a resumed session
  writes nothing into the developer's own Codex configuration, and the
  test that says so counts trust stanzas rather than checksumming a file
  that unrelated bookkeeping rewrites (R4).
- **Printed text is an interface, in both directions.** A command niwa
  prints is pasted, so it must hand the binary exactly the argv niwa
  intended (R5) -- and it is read first, so what the developer sees
  must be what the shell receives. A command niwa cannot print
  faithfully is not printed at all.
- **Fail closed on absent or unfit evidence.** A mapping with no
  recorded instance directory yields a command with no grant -- never
  one built from an empty or guessed path (R6) -- and a token niwa
  cannot render safely yields no printed command at all.
- **Tests don't trust the production table.** Expected values are
  written into the tests; expectations derived from the table production
  reads pass vacuously when the table is wrong (R7).
- **Acceptance is a measurement with a control.** The evidence is the
  real binary's recorded sandbox policy, with the ungranted resume as
  the negative control, at zero model cost, skipping without the binary
  (R8).
- **Measured behavior of codex-cli 0.149.0 is authoritative** for the
  scenario mechanics and the override's semantics: when the rollout
  records posture, which resume form accepts which flags, and how the
  override composes with the developer's own configuration.
- **Standard toolchain only.** The scan stays `go/ast` and the standard
  library; nothing new lands in CI.

## Considered Options

### Decision 1 -- the grant stays per-invocation, carried on the commands niwa builds and prints (R1, R4)

Before any question of construction, there is the question of mechanism:
does the posture survive re-entry because every re-entry command carries
the same per-invocation override the launch used, or because something
persistent vouches for the instance while it exists? The sibling design
answered this for launch, but re-entry shifts the calculus in
persistence's favor in one honest respect: this design concedes below
that a developer who types `codex resume <id>` from memory still lands
read-only, because niwa cannot reach a command it did not print -- and a
persisted trust entry would fix exactly that. The option deserves its
strongest form and a hearing on re-entry's own terms.

**Chosen: the same per-invocation grant, carried on every command niwa
builds or prints that re-enters the session.** Two measurements
(codex-cli 0.149.0) pin down what that pasted grant actually does.
First, the override merges with the developer's own configuration
rather than replacing it: with the developer's configuration trusting
directory A, an invocation in A carrying an override naming only B
still resolves writable for A -- so a niwa-printed command does not
strip trust the developer granted elsewhere. The measured case is the
disjoint one, which is also the only one niwa produces: the key it
names is an instance directory niwa created, so it can never collide
with a key the developer wrote. Second, the grant is keyed to
the directory it names: an invocation in B with no trust anywhere
resolves read-only, and the same invocation carrying an override naming
B resolves writable -- so a pasted command vouches for the instance
directory named inside it, and for nothing else; running it from some
other directory does not vouch for that directory. The grant a
developer pastes is therefore exactly the launch-time grant: one
process, one named directory, nothing persisted, nothing subtracted.

**Rejected: a persisted trust stanza scoped to the niwa-owned instance
path, written at dispatch and retracted at reap.** This is the
strongest form of persistence, and it is genuinely strong: it fixes
every re-entry surface at once, including the command niwa never
printed -- the from-memory `codex resume <id>` this design cannot
reach -- and it dissolves the quoting problem, because printed commands
go back to being a verb and a handle. It is rejected on three grounds
specific to re-entry, each checkable. Retraction hangs on a reap that
may never run: a crash between dispatch and reap, a deleted workspace
state, a machine that never comes back, each leaves the stanza vouching
indefinitely for a path a later instance may reuse. The developer's
configuration file becomes shared mutable state: niwa can serialize its
own writes, but the binary rewrites that file on its own schedule --
measured: unrelated bookkeeping rewrites it on every run -- so
dispatch-time writes and reap-time retractions risk contending with a
writer niwa does not control. That last one is a mechanism argument
rather than an observed clobber, and it is recorded as such. And for whatever window the stanza exists, the
grant outlives the process it was issued for and vouches for sessions
niwa did not launch -- the exact property the per-invocation form was
chosen not to have. Whether niwa should ever widen what it writes into
the developer's configuration is a product decision that belongs to
whoever owns that file's contract; this design does not pre-empt it,
it only declines to make it as a side effect of fixing resume.

**Rejected: a niwa-owned re-entry verb -- `niwa resume <session>`.**
This option deserves more than silence, because it beats the chosen one
on two axes. A niwa verb execs the agent's binary with a discrete argv
built fresh from the declaration, so nothing printed ever needs to
survive a shell and the quoting decision below dissolves entirely; and
it closes the typed-from-memory gap in a way printing never can,
because the command a developer remembers is niwa's, and niwa rebuilds
the grant every time. It is rejected on scope and interface. The
requirements govern commands niwa builds or prints, not niwa's command
surface; the hint verbs are deliberately the agent's own vocabulary,
printed so a developer can reach the session without niwa in the way,
and replacing them with a niwa verb reverses that choice for every
agent to fix one agent's posture. The additive form -- print the
agent's verbs and offer a niwa verb beside them -- escapes that
objection, and it is the variant worth revisiting; it does not escape
the next one. A new user-facing command is a
new lifecycle -- help text, docs, functional coverage, compatibility --
which is a feature's worth of surface for a defect the printed grant
already fixes. It is recorded here as the obvious follow-on if the
typed-from-memory gap turns out to matter in practice.

### Decision 2 -- one file owns re-entry construction, and both declaration fields (R1, R2)

R1's covered set is defined by a rule -- "steps back into a session niwa
launched" -- and the PRD already concluded that the rule needs one shared
production path to be testable. What is left to decide is where that
path lives and what shape "one path" takes, given that the resume verb
reaches the surfaces through two declaration fields: `ResumeArgs` on the
attach and `niwa list` paths, `HintVerbs` on the printed hint block.

**Chosen: a single new file, `internal/cli/dispatch_reentry.go`, as the
only non-test file in the package that reads `ResumeArgs` or
`HintVerbs`.** It exposes two things. The argv constructor returns the
discrete argv that steps back into a session -- the declaration's
resume arguments, then the workdir grant formatted for the session's
instance directory through the existing `formatWorkdirGrant` (so an
agent with no grant, or a mapping with no instance directory, yields no
grant arguments at all), then the handle. The renderer produces every
printed form: the whole post-dispatch hint block for a session -- the
resume line carrying the grant, the other verbs as today -- and, as its
single-command form, the one quoted resume command that `niwa list` and
the attach-failure fallback print. `dispatch.go` calls the constructor
for the attach exec and the renderer for everything it prints, and
reads neither field; `list.go` calls the renderer and reads neither
field. The exemption is a file rather than a function so it has the
same shape as the package's other structural excusals: membership a
reader can check by name, with the file's own header recording why it
is special.

**Rejected: each surface appending the grant itself through the shared
formatter.** This satisfies today's behavior -- three call sites, three
`formatWorkdirGrant` calls -- and it is the smaller diff. It fails R1,
not cosmetically but structurally: the rule then lives in three copies,
and a fourth surface added later misses it silently, which is the exact
shape of the defect this design exists to fix. The PRD's own history
argues the point: the three surfaces that exist today were each written
correctly against the launch-time contract and still all missed the
grant, because nothing made the omission fail.

**Rejected: a method on `agentplan.LaunchSpec`.** Symmetry seems to
argue for it -- the declaration knows its own resume arguments and its
own grant shape. But `agentplan` is a declaring leaf: it says what to
launch and how to spell each intent, and it builds no argv, a division
the sibling design established and its tests hold (the leaf cannot even
name `exec`). The launch argv builder already lives in `internal/cli`;
putting the re-entry builder in the leaf would split the two halves of
the argv story across two packages, and would put the first argv
assembly into a package whose whole discipline is that it assembles
nothing.

### Decision 3 -- the printed hint carries the grant on the resume verb only, and the renderer owns the match (R1)

The post-dispatch hint block prints one line per verb in the
declaration's `HintVerbs` -- for an agent with several, that is the
resume verb alongside verbs that read logs or stop the worker. R1
covers commands that step back into a session; the question is which
hint lines that is, and whose code decides.

**Chosen: the renderer returns the whole hint block, and inside the
re-entry file the line whose verb matches the declaration's resume
arguments is the one rendered as a full re-entry command; every other
verb prints as it does today.** The match lives where both fields are
already legal to read, which is what makes this decision compatible
with the scan rule in Decision 5: `dispatch.go` receives finished lines
and never compares anything. The match is against the declaration,
never against a verb spelled at a call site, so no agent is named. For
an agent whose resume verb carries a grant, that line is the one that
starts a process; for an agent that declares no grant, the rendered
block is byte-identical to today's.

**Rejected: every hint verb carrying the grant.** Uniformity is the
draw -- one loop, no matching. But a verb that reads logs or stops a
worker does not start a session, so a grant on it vouches for no
process -- and what it certainly does is put a quoted configuration
override on every line, so the developer pastes around it on exactly
the commands they use most. Cost on every line, with a vouched-for
process behind only one of them, is the wrong trade.

**Rejected: a new declaration field listing session-starting verbs.**
This is the principled-looking version -- make the declaration say which
verbs re-enter a session, and render those. But the declaration already
answers the question through its resume arguments, and a second field
answering the same question can disagree with the first, which is
exactly the drift the declaration table exists to prevent. Nothing today
needs the distinction the field would add: every agent in the table has
one resume spelling, and a future agent with several session-starting
verbs can motivate the field when it exists. A dead seam is a cost paid
now for a customer that may never arrive.

### Decision 4 -- printed commands pass an allowlist quoter, or are not printed at all (R5)

The grant's value contains braces, quotes, and equals signs. Executed,
that is no problem -- argv elements are discrete and no shell is
involved. Printed, it is: a developer pastes the line into a shell, and
an unquoted `projects={...}` does not survive the trip. R5 requires
that pasting a printed command verbatim hands the binary exactly the
argv niwa intended. And there is a prior question quoting cannot
answer: a token can be unsafe to display before it is unsafe to parse,
because a terminal interprets control bytes in what it prints.

**Chosen: a quoter defined by a positive allowlist, plus a fail-closed
print gate.** A token consisting only of bytes in
`[A-Za-z0-9_@%+=:,./-]` passes bare; everything else -- the empty
string included -- is single-quoted, with an embedded single quote
spelled by the standard close-escape-reopen sequence. `~` and `!` are
deliberately not in the bare set: bare, one invites tilde expansion and
the other history expansion in interactive shells. The allowlist is the
load-bearing choice -- a denylist means one missed byte prints
unquoted, and the failure of a quoter is silent until someone pastes.
Above the quoter sits the gate: a re-entry command is printed only when
every token is free of control characters and escape sequences, and the
handle has passed validation (Security Considerations); otherwise niwa
prints no command for that session -- the same fail-closed shape
`sessionResumeCommand` already has for a mapping it cannot vouch for.
Single-quoting stops word-splitting, expansion, and command splitting,
but it cannot stop a terminal from interpreting a carriage return or an
escape sequence while displaying the line -- and a line that displays
as one command and pastes as another is the failure R5 is about, in the
half a quoter cannot reach. The exec path in `dispatchAttach` is
untouched by all of this: it passes discrete argv elements and must
never be built by joining quoted strings.

**Rejected: stripping unsafe bytes and printing anyway.** The
repository already has a helper written for exactly this threat --
`stripEscapes` in `internal/workspace`, which drops escape sequences
and control bytes from text bound for a terminal. It is the right tool
where the output is display-only prose. It is wrong here because a
re-entry command is executable: a stripped command is a command that no
longer does what it says -- it names a different path than the mapping
records -- and printing a plausible-looking command that silently
diverges from the recorded state is worse than printing nothing.
Fail-closed keeps the invariant that every printed command is exactly
the recorded truth, quoted.

**Rejected: single-quoting every argument unconditionally.** Two other
packages in the tree do exactly that, and correctly -- they embed a
value inside a larger command string handed to a shell, where
unconditional quoting is the only safe posture. Rendering a command a
person reads is a different job: quoting the binary name, the verb, and
the session id makes the common case harder to read and scan, for no
safety the allowlist doesn't already provide -- a token inside the
allowlist is shell-safe bare by construction.

**Rejected: hoisting one of the two existing quoters into a shared
home.** Sharing looks like hygiene, but the existing copies quote
unconditionally by design, for callers that need exactly that. A shared
function would either force unconditional quoting on this caller's
printed output or force this caller's selectivity on callers embedding
into command strings, where selectivity is a hazard. Three small
functions, each fit to its job and each tested against its own
contract, beat one function wrong for two of them.

### Decision 5 -- the scan rule covers both fields and ranges over the whole package (R1, R7)

Decision 2 puts re-entry construction in one file; something has to
fail when any other file stops calling it. The dispatch path already
has a structural scan -- `dispatch_layout_test.go` -- and the question
is what the new rule says and what it ranges over. Two wrong shapes are
worth naming because each looks sufficient. A rule about `ResumeArgs`
alone misses that `HintVerbs` is a second copy of the resume verb:
today's hint block builds `<binary> <verb> <handle>` from `HintVerbs`
with no read of `ResumeArgs` at all, so a fourth surface written the
same way would pass the narrow rule while assembling exactly the
command the rule exists to govern. And a rule bound to an enumerated
file list reintroduces the silent omission it exists to close: a new
file never added to the list escapes it, which is the `list.go` gap
this design is fixing, rebuilt one level up.

**Chosen: no non-test file in `internal/cli` other than
`dispatch_reentry.go` may select `.ResumeArgs` or `.HintVerbs`, and the
rule ranges over every non-test file in the package** -- the way the
package's existing completeness guard already ranges over the
directory, rather than the way its agent-naming rule walks a named
list. The rule is a selector check over the syntax tree, and it is
enforceable precisely because those two fields are the only ingredients
a re-entry command needs from the declaration: a file that can read
neither cannot assemble one. `dispatch_reentry.go` itself joins the
agent-naming scan's file list, so the one file allowed to build
re-entry commands is also held to the discipline of naming no agent
while it does. Like every rule in that test file, this one must be seen
red before it is trusted -- the scan's control pattern (a deliberately
violating source fixture) extends to the new rule.

**Rejected: the rule over an enumerated file list.** It matches the
agent-naming rule's existing shape, which is its appeal. But the
agent-naming list enumerates files that deliver dispatch; this rule
forbids something, and a prohibition with an opt-in scope is barely a
prohibition -- every future file is born outside it. The directory-wide
form costs one exemption (`dispatch_reentry.go`, recorded with its
reason, like the package's other excusals) and closes the class.

**Rejected: an opaque type only the constructor can produce.** The
type-system version of the rule: make the exec and print sites take a
value nothing else can construct. It does not close the hole, because
everything here is one package -- a sibling file can call
`exec.Command` with a verb and a handle directly, or print its own
string, without ever touching the type. Against an in-package offender
the type is advisory, and an advisory structure that looks load-bearing
is worse than none: it reads as enforcement while enforcing nothing.
The scan fails the offender's source instead of hoping the offender
uses the type.

**Rejected: leaving it to review.** The rule is simple and a reviewer
who knows it would catch a violation. But the three surfaces this
design retrofits are the record of what review catches: each was
written, reviewed, and merged while silently missing the grant. This
repository has already paid for the difference between a structure
stated in a design and a structure a test fails on; the scan is that
lesson applied.

### Decision 6 -- acceptance is a live scenario at zero model cost (R3, R4, R8)

R8 is explicit that an assertion about the argv niwa assembled is not
acceptance evidence: the claim is about the posture the resumed session
actually holds, so the evidence must come from the real binary, with a
negative control, without a login (CI has none) and without spending a
model turn. The question is what observation the scenario stands on.

**Chosen: a functional scenario that runs the real binary with the
model provider pointed at an unreachable endpoint, and reads the
resolved posture from the session rollout.** Measured on codex-cli
0.149.0: Codex records the turn's resolved `sandbox_policy` in the
session rollout at turn bootstrap, before the first model request, so
the posture is observable with nothing billed and no credential
involved. One rollout carries three turn contexts -- launch with the
grant, resume without it, resume with it -- and the measured result, in
that order, is `workspace-write`, `read-only`, `workspace-write`: the
regression and its negative control in one artifact. The scenario gates
on the binary being present and skips otherwise, and needs no login.

Two supporting measurements shape the scenario's edges. For R3, on the
interactive resume form: the grant alone records `workspace-write`; the
grant plus an appended `--sandbox read-only` records `read-only` --
the binary's own resolution puts an explicit selection above the
trust-derived default, so niwa's obligation is restraint, not argument
ordering. The forms are asymmetric: the interactive form accepts both
`-c` and `-s`/`--sandbox`, while the non-interactive `codex exec
resume` accepts `-c` and has no sandbox flag at all, so the
explicit-ask case exists only on the interactive form. For R4, the
trust-footprint assertion counts `[projects.` stanzas rather than
hashing the configuration file: the standing spike records that
unrelated bookkeeping rewrites that file on every run, so a checksum
reports a change that says nothing about trust. Across every measured
run above, the isolated home ended with no configuration file at all.

**Rejected by measurement: the standing spike's `-m bogus-model-xyz`
probe.** The probe was this scenario's natural starting point -- it had
already been used to observe session construction cheaply. Re-measured
for this design: with a credential present it still works, dying at the
API boundary with a 400 after constructing the session. Under an empty
CODEX_HOME on 0.149.0 it did not reach session construction within 60
seconds -- and CI has no credential, so the probe's working mode is the
one CI can never be in. The unreachable-endpoint form observes the same
bootstrap record without depending on a credential at all.

### Decision 7 -- the new measurements land in the standing spike

The measurements behind this design -- the bootstrap-time rollout
record, the three-context posture sequence, the resume forms' flag
asymmetry, the bogus-model probe's behavior under an empty home, and
Decision 1's override semantics (the merge with the developer's own
configuration, and the grant's keying to the directory it names) -- are
facts about codex-cli 0.149.0 that outlive this feature.

**Chosen: append them as new findings to
`docs/spikes/SPIKE-codex-discovery-mechanics.md`.** The repository's
rule is that new Codex measurements extend the standing spike rather
than fork a second one, so a future reader asking "how does Codex
behave here, as measured?" has one place to look, with each finding
carrying its binary version. A second spike would split the measured
record of one binary across two documents whose scopes a reader would
have to reconstruct; the rule exists because that split already had to
be prevented once, and this design follows it rather than relitigating
it.

## Decision Outcome

The posture survives re-entry the same way it arrives at launch: a
per-invocation grant, now carried on every command niwa builds or
prints that steps back into the session -- never a persisted trust
entry, whose retraction would hang on a reap that may never run and
whose vouching would outlive the process it was issued for. One new
file, `internal/cli/dispatch_reentry.go`, is the only non-test file in
the package that reads the declaration's resume arguments or hint
verbs. Its constructor returns the re-entry argv -- resume arguments,
then the grant formatted through the existing `formatWorkdirGrant` (so
no grant declared, or no instance directory recorded, means no grant
emitted), then the handle -- and its renderer produces every printed
form: the post-dispatch hint block with the grant on the resume line
and the other verbs untouched, and the single quoted resume command
that `niwa list` and the attach-failure fallback print. Printed tokens
pass a positive-allowlist quoter (`[A-Za-z0-9_@%+=:,./-]` bare,
everything else single-quoted), and a command is printed only when
every token is free of control characters and the handle passes
validation -- otherwise niwa prints nothing for that session. For an
agent that declares no grant, every surface's output is byte-identical
to today's.

The rule is enforced, not hoped for: the dispatch-path AST scan gains a
directory-wide rule that no non-test file in the package other than
`dispatch_reentry.go` selects either field, demonstrated red like every
rule in that file, and the re-entry file itself joins the agent-naming
scan. Regression coverage follows the contract-test pattern already in
the tree -- fixture launch declarations with distinctive literal grants
and with no grant, expected output written literally into the tests,
each surface failing independently when its grant goes missing -- so
nothing in the tests trusts the production table. Acceptance is a
functional scenario against the real binary: model provider pointed at
an unreachable endpoint, three turn contexts in one rollout recording
`workspace-write` / `read-only` / `workspace-write` at turn bootstrap,
trust footprint asserted by stanza count, no login, no model turn,
skipped when the binary is absent. The measurements land in the
standing Codex spike, and the contributor guide gains the posture story
the PRD's R9 requires.

## Solution Architecture

Everything lands in `internal/cli`; `internal/agentplan` is untouched
-- the declaration already carries `ResumeArgs`, `HintVerbs`, and
`WorkdirGrantArgs`, and this design only changes who assembles them.

```
internal/cli
    dispatch_reentry.go    new: the argv constructor, the printed-form
                           renderer, the quoter, and the print gates --
                           the only non-test file in the package that
                           reads ResumeArgs or HintVerbs
    dispatch_launcher.go   buildLaunchArgs, formatWorkdirGrant
                           (existing, unchanged; the constructor calls
                           the formatter)
    dispatch.go            dispatchAttach via the constructor; the hint
                           block and the attach-failure fallback via
                           the renderer; reads neither field
    list.go                sessionResumeCommand via the renderer; reads
                           neither field
    dispatch_layout_test.go  the agent-naming scan (dispatch_reentry.go
                           added to its file list) and the new
                           directory-wide field rule with its control
    dispatch_contract_test.go  fixture-spec regression tests per surface
test/functional
    the acceptance scenario against the real binary
```

The re-entry file's surface, at design altitude:

```go
// reentryArgs returns the argv, excluding the binary, that steps back
// into a dispatched session: the declaration's resume arguments, the
// workdir grant formatted for the session's instance directory, then
// the handle. No declared grant, or no instance directory, means no
// grant arguments -- never a grant naming nothing.
func reentryArgs(spec agentplan.LaunchSpec, handle, workdir string) []string

// reentryCommand returns the one printed command that steps back into
// the session, quoted for a POSIX shell -- or "" when any token fails
// the print gate, so a command niwa cannot render faithfully is not
// rendered at all.
func reentryCommand(spec agentplan.LaunchSpec, handle, workdir string) string

// reentryHints returns the whole post-dispatch hint block, one line
// per declared hint verb: the line matching the declaration's resume
// arguments as a full re-entry command, the others as today.
func reentryHints(spec agentplan.LaunchSpec, handle, workdir string) []string
```

The call sites, concretely:

- **The attach exec.** `dispatchAttach` currently runs
  `exec.Command(bin, append(ResumeArgs, handle)...)` with `cmd.Dir` set
  to the instance. It becomes `exec.Command(bin, reentryArgs(spec,
  handle, workdir)...)`; the working-directory handling and the
  non-fatal failure contract are unchanged. The argv stays discrete
  elements end to end -- the quoter has no business here. For Codex
  this surface is not reachable today (the agent holds its session for
  the length of the turn and declares so), but it is in the set so the
  guarantee doesn't depend on which agent declares what; for Claude,
  which declares no grant, the argv is unchanged.
- **The printed hint block and the fallback.** The loop over
  `spec.HintVerbs` in `dispatch.go` is replaced by printing the lines
  `reentryHints` returns; the attach-failure fallback message prints
  `reentryCommand` instead of joining the resume arguments itself.
  After this change `dispatch.go` reads neither declaration field,
  which is what lets Decision 5's rule range over it.
- **The `niwa list` resume command.** `sessionResumeCommand` keeps its
  fail-closed preamble -- unknown agent, no spec, or no usable handle
  still yield nothing -- and replaces its final string join with
  `reentryCommand`, passed the mapping's `InstancePath`. The
  no-resume-arguments case moves out of the preamble and into
  `reentryCommand`, which returns "" for a declaration that names no way
  back in. That move is not tidying: `len(spec.ResumeArgs) == 0` is a
  read of a field Decision 5's rule reserves to the re-entry file, so
  leaving it in `list.go` would put the scan red on the very code the
  call-site phase lands. A mapping with no instance directory reaches the
  constructor with an empty workdir and the command carries no grant
  (R6), inherited from `formatWorkdirGrant` rather than re-implemented;
  a token that fails the print gate yields "" and the mapping gets no
  printed command, extending the function's existing shape.

The scan closes the loop: with the directory-wide rule in force, the
only way any non-test file in the package can produce a command that
steps back into a session -- whether from the resume arguments or from
the hint verbs -- is through `dispatch_reentry.go`. A fourth surface
added later either calls it and carries the grant, or reads a field
itself and fails the scan the same day, whatever file it lives in.

Two ways past it survive, and both are recorded rather than closed.
The rule is scoped to this package, so a surface built somewhere else
that imports the declaration escapes it -- `internal/watch` already
assembles command strings of its own. What holds there is the
convention that commands niwa runs and prints live in `internal/cli`,
which is a fence made of habit rather than of tests. And a file could
spell a verb as a string literal instead of reading either field; that
is hardcoding one agent's vocabulary, the same class the scan's
agent-literal rule already targets, and the resume verbs could join
that literal set if it ever happens. Neither is silent omission, which
is what this rule exists to close -- both take a deliberate step around
a rule the author would have to notice first.

For the record, the printed Codex resume command changes shape from
`codex resume <id>` to:

```
codex resume -c 'projects={"/abs/instance/dir"={trust_level="trusted"}}' <id>
```

## Implementation Approach

Five phases, each leaving the tree green and each reviewable on its own
terms.

1. **`dispatch_reentry.go`, with its unit tests.** The constructor, the
   renderer, the quoter, and the print gates land as pure functions
   with no call site changed, so the review is entirely about the
   contract: literal expected argv for a fixture declaration with a
   distinctive grant, for one with no grant, and for an empty working
   directory; a quoter table covering bare tokens, the empty string,
   braces, quotes, equals signs, spaces, `~`, `!`, and embedded single
   quotes; gate cases for control characters, escape sequences, and an
   invalid handle, each yielding no printed command; and the R5
   acceptance shape -- each rendered command executed through
   `/bin/sh -c` against a stub that records its argv, asserted against
   a literal expected vector.
2. **The call sites.** `dispatchAttach` routes through the constructor;
   the hint block and the attach-failure fallback through the renderer;
   `sessionResumeCommand` through the renderer's single-command form.
   Reviewable as three small diffs plus the contract-test extensions
   that make R7 real: fixture-spec tests per surface, expected values
   written into the tests, each surface failing independently when its
   grant is removed -- verified by breaking each one in turn before the
   phase merges. This phase also pins the no-behavior-change half: for
   a declaration with no grant, every surface's output equals its
   pre-change output literally. When it lands, no non-test file in the
   package but the re-entry file reads either declaration field.
3. **The scan extension.** The directory-wide rule -- no non-test file
   but `dispatch_reentry.go` selects `.ResumeArgs` or `.HintVerbs` --
   lands with its control fixture and is demonstrated red against a
   hand-written violation before it is trusted, and
   `dispatch_reentry.go` joins the agent-naming scan's file list. The
   ordering claim matters and is now true by construction: phase 2
   removed every read outside the re-entry file, so this scan passes on
   a tree where phase 2 landed and fails on one where it didn't --
   which is also why this phase changes no production code: if a site
   was missed, this is where it fails loudly.
4. **The acceptance scenario.** The functional scenario from Decision
   6: real binary, isolated Codex home, unreachable model endpoint,
   three turn contexts read back from the rollout, the explicit-ask
   measurement on the interactive form, the stanza-count footprint
   assertion, and the skip guard for machines without the binary.
   Reviewable as the evidence layer: it asserts posture, not argv, and
   it can be run by hand on any machine with the binary.
5. **The guide and the spike.** `docs/guides/codex-agent.md` gains the
   posture story R9 requires -- what posture a resumed session holds,
   why the grant is per process rather than a write to the developer's
   configuration, that a resume command typed from memory carries no
   grant, and that when a degraded (grantless) command lands on the
   interactive form's trust prompt, the developer should decline it and
   re-copy the command from `niwa list` rather than answer yes and
   persist a stanza niwa would never write. The Decision 7 measurements
   are appended to the standing spike with their binary version.
   Reviewable as prose against the merged behavior.

Exit criteria: the unit and contract suites green with the fixture
expectations literal; the scan red on its control and green on the
tree; the functional scenario recording `workspace-write` /
`read-only` / `workspace-write` on a machine with codex-cli present and
skipping on one without; `gofmt -l .`, `go vet ./...`,
`go test -race ./...` clean.

## Security Considerations

- **What a pasted grant can and cannot do, measured.** Two facts about
  the override (codex-cli 0.149.0) bound the printed command's power.
  It merges with the developer's own configuration rather than
  replacing it: with the developer's configuration trusting directory
  A, an invocation in A carrying an override naming only B still
  resolves writable for A -- so a niwa-printed command cannot strip
  trust the developer granted elsewhere. And it is keyed to the
  directory it names: an invocation in B with no trust anywhere
  resolves read-only, while the same invocation carrying an override
  naming B resolves writable -- so the pasted command vouches for the
  instance directory named inside it and for nothing else, and running
  it from some other directory does not vouch for that directory.
- **The grant's value is composed from a path niwa owns.** The instance
  directory substituted into the grant is one niwa itself created and
  recorded; on the exec path it is passed as discrete argv elements, so
  no shell ever parses it and nothing in it can become a second
  argument. The printed grant also discloses nothing new: both
  `niwa dispatch` and `niwa list` already print the absolute instance
  path today.
- **The printed path is where a hostile value would bite, and the
  defense is quoting plus refusal.** Single-quoting stops
  word-splitting, expansion, and command splitting, so a path carrying
  a quote, a space, or a newline stays one token through the paste.
  What quoting cannot stop is the terminal: a token carrying a carriage
  return or an escape sequence redraws the printed line, so what the
  developer reads is not what they paste. The print gate closes that
  half: a re-entry command is printed only when every token is free of
  control characters and escape sequences, and otherwise niwa prints no
  command for that session -- fail-closed, like `sessionResumeCommand`
  already is for a mapping it cannot vouch for. The repository's
  `stripEscapes` helper in `internal/workspace` was written for this
  threat and is deliberately not used here: it is right for
  display-only prose, but a stripped command is a command that no
  longer does what it says. The remaining attacker is one who can
  rewrite the session mapping on disk -- and an attacker with write
  access to the workspace's own state files already has more direct
  means than a printed string.
- **The quoter is an allowlist, not a denylist.** Tokens of
  `[A-Za-z0-9_@%+=:,./-]` pass bare; everything else, the empty string
  included, is single-quoted. `~` and `!` are excluded from the bare
  set on purpose, for tilde and history expansion. A denylist fails
  open -- one missed byte prints unquoted -- and a quoter's failure is
  silent until someone pastes.
- **The handle is validated before it is used, which today it is not.**
  Only `SessionID` passes the UUID gate on the current tree; the
  mapping's `Handle` is recorded and read back unchecked. Since the
  printed command carries the handle, the re-entry file validates it
  with `watch.IsSafeHandle` -- already exported, and its conservative
  charset -- alphanumerics, `-` and `_` -- covers both a UUID and a
  record-directory name -- before rendering any command, and fails
  closed when it doesn't pass.
- **Zero footprint holds on the re-entry path, with one honest edge.**
  The grant rides the whole-table `projects` override -- the one
  elevation route the upstream PRD records as leaving the developer's
  configuration untouched, where a sandbox flag or a `sandbox_mode`
  override makes the binary itself write trust. The scenario asserts it
  by counting `[projects.` stanzas in the isolated home; across every
  measured run the home ended with no configuration file at all. The
  edge is the degraded case: a command printed with no grant (R6),
  pasted into the interactive form, makes the binary prompt to trust
  the directory -- and a developer who answers yes appends a stanza to
  their own configuration. No niwa code writes it, so R4 holds
  literally, but the claim should not be read as "no route from a
  niwa-printed command to a persistent write exists". The guide tells
  the developer to decline that prompt and re-copy the command from
  `niwa list`.
- **One rendering qualification, stated as such.** The declaration's
  `%q` verb is Go quoting, not TOML: for the paths niwa constructs the
  two agree, but a path carrying exotic bytes can render escapes TOML
  rejects -- which is a loud parse error at the binary, not an
  injection. The print gate makes the case unreachable on the printed
  path; on the exec path it would surface as a failed launch, never a
  mis-granted one.
- **What the grant vouches for: one process, at paste time.** The
  printed grant is inert text; it elevates only a process the developer
  starts by running the command, at the one directory named inside it,
  and it persists nowhere. Each paste is a fresh per-invocation grant,
  exactly as wide as the launch-time one. The elevation is also
  legible: it rides the command line the developer is looking at,
  rather than a configuration write they would have to go find.
- **For how long: as long as the text exists.** `niwa list` prints the
  command for the life of the mapping, and terminal scrollback outlives
  both the mapping and the instance. A stale command pasted after the
  instance is reclaimed still grants one process trust at that path --
  by then a path that does not exist, or a fresh directory under a new
  randomly suffixed name. The exposure is bounded to one process at a
  niwa-named absolute path, and it is the same exposure the developer
  accepts every time they run any niwa-printed command from scrollback.
- **The developer's explicit ask is never parsed.** niwa appends
  nothing after the handle and inspects nothing the developer adds; an
  appended `--sandbox read-only` wins in the binary's own resolution,
  measured. There is no flag-rewriting surface to get wrong.

## Consequences

Positive:

- The guarantee is a rule with an enforcement, not a list with three
  entries. A fourth re-entry surface -- built from the resume arguments
  or from the hint verbs, in any non-test file in the package -- either
  goes through `dispatch_reentry.go` and carries the grant, or fails
  the scan. The silent-omission failure mode this feature exists to
  close is closed structurally, and the enforcement does not itself
  depend on a hand-maintained file list.
- The posture claim is proven where it matters: the real binary's
  recorded sandbox policy, with a negative control, at zero model cost
  and no login, on every CI run that has the binary.
- niwa's footprint is unchanged in both directions: nothing new is
  written to the developer's configuration, and agents that declare no
  grant see byte-identical output on every surface. The pasted grant is
  measured to merge with, never replace, the developer's own trust, and
  to vouch only for the directory it names.
- The handle joins the session id in being validated before it becomes
  a command argument, closing a gap the current tree has.

Negative, accepted:

- **The printed resume command gets longer and less pretty.** What was
  `codex resume <id>` becomes a line with a quoted config override in
  the middle. The quoter keeps the binary, the verb, and the id bare,
  and the guide says what the extra argument is -- but the line no
  longer fits in a glance, and that is a real cost paid on every
  dispatch.
- **niwa cannot reach a command it did not print.** A developer who
  types `codex resume <id>` from memory still lands in the old
  posture: read-only, approval per command. No mechanism in this design
  can fix that without persisting trust, which Decision 1 rejects on
  re-entry-specific grounds -- and the same decision records the
  niwa-owned resume verb as the follow-on that would close this gap if
  it turns out to matter. Until then the mitigation is honesty: the
  guide states it plainly, and `niwa list` keeps the correct command
  one copy away for the life of the instance.
- **Fail-closed printing means some sessions get no printed command.**
  A mapping whose recorded path or handle carries control bytes, or a
  handle that fails validation, yields silence where today's code would
  print something. Silence is the point -- a command that cannot be
  rendered faithfully should not be rendered -- and it extends a shape
  `sessionResumeCommand` already has, but a developer with a corrupted
  mapping loses the printed route back and must reach the session
  through the agent's own tooling.
- **A third quoting implementation enters the tree.** Decision 4's
  analysis is that the three quoters serve genuinely different jobs,
  but the fact remains that a future reader finds three of them and
  must read each one's contract to know which to reach for. Each
  carries a comment saying what job it is fit for.
- **The scan grows another rule, and the re-entry file is a standing
  exemption.** The directory-wide rule removes the file-list hazard,
  but its exemption is one more special case a reviewer must be willing
  to challenge, with its reason recorded the way the package's other
  excusals are -- the accepted trade throughout this path: maintenance
  errors loud rather than impossible.
- **The acceptance scenario leans on a measured behavior of one binary
  version.** Codex recording resolved posture at turn bootstrap is a
  fact of 0.149.0, not a documented contract. A future binary that
  defers the record breaks the scenario -- loudly, on a machine with
  the binary, which is the acceptable failure direction -- and the
  spike carries the mechanics and the version so the breakage is
  diagnosable rather than mysterious.

## References

- docs/prds/PRD-codex-dispatch-posture-persistence.md -- the upstream
  requirements (R1-R9) this design answers, and the record of why only
  one elevation route satisfies the zero-footprint requirement.
- tsukumogami/niwa#273 -- the report of the posture drop.
- docs/designs/current/DESIGN-codex-background-dispatch.md -- the
  sibling design: the launch-time grant, its Decision 9 rejecting
  persistent trust at launch, and the dispatch-path scan this design
  extends.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- the standing spike
  for measured Codex behavior, which Decision 7 extends.
- docs/guides/codex-agent.md -- the contributor guide R9 updates.
- internal/agentplan/dispatch.go -- the launch declaration:
  `WorkdirGrantArgs`, `ResumeArgs`, `HintVerbs`, and the recorded
  reasoning for the per-invocation grant.
- internal/cli/dispatch_launcher.go -- `buildLaunchArgs` and
  `formatWorkdirGrant`, the launch half of the argv story the
  re-entry constructor joins.
- internal/cli/dispatch.go, internal/cli/list.go -- the re-entry
  surfaces as they stand.
- internal/cli/dispatch_layout_test.go,
  internal/cli/dispatch_contract_test.go -- the scan and the
  fixture-spec test pattern this design builds on.
- internal/watch/state.go -- `IsSafeHandle`, the handle gate the
  re-entry file adopts.
