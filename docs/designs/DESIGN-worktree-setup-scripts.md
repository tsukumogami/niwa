---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-worktree-setup-scripts.md
problem: |
  A worktree receives every accessory a repo checkout gets except the one its
  own setup scripts produce, and a worktree hook registered under an event niwa
  does not consume is discovered and silently never run. Closing either half
  naively is destructive: a setup script that fails after writing only into an
  ignored path leaves the tree reading clean, and the delegated create path
  deletes it; a discovery error does the same thing deterministically.
decision: |
  Run the repo's setup scripts inside ApplyToWorktree beside the existing hook
  runner, gated by a per-repo switch that defaults off, and carry the outcome
  out through a nil-tolerant sink field on WorktreeApplyOptions rather than as
  an error. Validate hook event names against one named set in discovery, and
  make that specific error non-fatal inside the hook runner. Build the worktree
  path's redactor from the repo's own secret declarations rather than from a
  vault resolution that never happens there. Move the worktree fan-out after
  the clone's setup step.
rationale: |
  Every alternative was weighed against a single structural fact: five entry
  paths converge on two invocation sites, so code inside ApplyToWorktree cannot
  tell which path it is on, and those paths want opposite things from a
  failure. Carrying outcomes as data is the only shape that lets each caller
  decide, and the sink idiom already ships in this struct, so it costs no
  signature changes. Everything else follows from refusing to let any new
  failure reach the teardown.
---

# DESIGN: Worktree Setup Scripts

## Status

Planned

## Context and Problem Statement

`niwa apply` runs a repo's `scripts/setup/` against the clone it materializes,
and nothing runs them against a worktree, so a worktree arrives with tracked
source and no dependency tree. The upstream PRD carries why that matters and
why the gap is undeclared rather than deliberate. This document is about how to
close it without the fix being worse than the gap.

Three facts about the existing code shape everything below, and none of them is
obvious from the outside.

**Five entry paths, two invocation sites.** `ApplyToWorktree`
(`internal/workspace/worktree_content.go:525`) is called from exactly two
places: `applyContentToWorktree` (`internal/cli/session_lifecycle_cmd.go:366`)
and the Step 6.6 fan-out (`internal/workspace/apply.go:2433`). The helper in
turn serves four commands — `worktree create` (`:194`), `worktree apply`
(`:274`), the delegated `WorktreeCreate` hook (`session_from_hook_cmd.go:144`),
and `niwa apply` at worktree scope (`cli/apply.go:306`). So code inside
`ApplyToWorktree` cannot tell which entry path it is on.

**Those paths want opposite things from a failure.** Interactive `worktree
create` retains the worktree and tells the operator to re-sync. `worktree
apply` leaves everything untouched. The fan-out warns, forward-carries the
worktree's prior managed entries, and continues at exit 0. The delegated create
path runs a guarded teardown that deletes the worktree and ends the session.

**And the guard that decides teardown has already been neutralised by the time
anything can fail.** `DestroySession` retains a worktree only when `git status
--porcelain` reports it dirty (`internal/worktree/worktree.go:90`), with no
`--ignored`. Every file niwa writes is git-excluded at
`worktree_content.go:703` — step 4, before the hook step at `:712` — and the
comment there says why: an uncovered file makes a finished worktree read dirty,
which makes the teardown refuse to reclaim it. So a setup script that fails
after writing only into `node_modules` leaves the tree reading clean and the
worktree is deleted, while one that fails after writing an unignored log file
leaves it dirty and the worktree survives. Retention tracks what the script
happened to touch rather than whether anything was lost, and it runs backwards:
the better-behaved the script, the more certain its work is destroyed.

That last fact is a live defect independent of this feature — it is filed as
#285 and explicitly out of this scope. What matters here is the dependency
direction: this design is safe from that path because none of its failures
travel as errors, not because the guard is correct.

The second half of the problem is one layer over. `DiscoverWorktreeHooks`
(`internal/workspace/discover.go:85`) takes the event name straight off the
filesystem with no validation, and `runWorktreeHooks` reads exactly
`hooks[worktreeApplyEvent]` where that constant is `"apply"`
(`worktree_content.go:24`). A `worktree-hooks/create/` directory is discovered,
indexed, and never run, and so is a typo'd filename. The guide says these
scripts "run on every `create` and `apply`", and
`TestDiscoverWorktreeHooks_TopLevelAndSubdir` (`discover_test.go:25`) builds a
`create/` directory and asserts discovery returns both its scripts. The
documentation and the test suite both certify the behaviour the bug report
calls broken.

The two halves collide in one function. What counts as a valid worktree
lifecycle event decides whether setup can run at a moment other than `apply`,
so settling either alone produces an answer the other has to undo.

## Decision Drivers

- **No new failure may reach the teardown.** PRD R6 and R11. This is a bound on
  the design, not a preference, because of the guard inversion above.
- **Reach all five entry paths, or say which are excluded and why.** PRD R1, R2.
- **Reuse rather than fork.** `RunSetupScripts` needs no change: it never
  touches git, is already parameterized on the directory it runs in, and its
  own tests run it against bare temp directories. PRD R1 requires the same
  discovery, ordering and executable-bit policy as the clone run, which reuse
  gives for free and a reimplementation would have to re-earn.
- **The clone path is untouched.** PRD R15.
- **Default off, per repo.** PRD R3. Every setup script in existence was written
  before any of this, and at least one first-party example is silently wrong
  when run per-tree.
- **Publish the contract that is being relied on.** PRD R13. `scripts/setup`
  appears in two files under `docs/`, both design documents.
- **Extensibility by one entry.** PRD R12, so the two halves do not undo each
  other.

## Considered Options

### Decision 1 — where setup executes, and how its outcome travels out

**Chosen: run it inside `ApplyToWorktree` beside `runWorktreeHooks`, and carry
the outcome out on a nil-tolerant sink field on `WorktreeApplyOptions`.**

The insertion point is the only site holding `cfg`, `repo`, `group`,
`instanceRoot` and `worktreePath` at once, in the same package as
`RunSetupScripts`, and one insertion there reaches all five entry paths
including the fan-out. So no caller enumeration has to be extracted.

The outcome shape is what makes that safe. `WorktreeApplyOptions` already
carries this exact idiom twice. `Exempt *[]string` (`worktree_content.go:455`)
is an output sink filled through `collectExempt` (`:954`), which returns
silently when the pointer is nil — a caller that wants the data passes a
pointer, one that does not gets a no-op. And `WorktreeDelegation` (`:497`) is
documented as "nil installs neither... so a caller that does not set it is
unaffected". Adding `Setup *SetupResult` as a sink, plus nil-tolerant
`Reporter` and `Redactor` input fields, follows a convention this struct
already established rather than inventing one.

The analogy is exact for the sink and looser for the inputs, and the difference
needs a rule. `Exempt` and `WorktreeDelegation` are data — a slice pointer and a
value struct — whereas `*Reporter` is a stateful renderer with a mutex, a
spinner goroutine and a deferred-message queue, and the struct already carries
`Stderr io.Writer` for the diagnostic-output role. So: **`Reporter` wins when
set, and `Stderr` is the fallback.** A caller that supplies neither gets a
reporter synthesized from `os.Stderr`; a caller that supplies only `Stderr` gets
one wrapping it, which is what the two interactive commands do today; the
fan-out supplies the pipeline's own `Reporter` and its `Stderr` is then unused
for setup output. Stating the precedence is what keeps the struct from having
two overlapping output channels with no rule between them. `*secret.Redactor`
raises none of this — `internal/workspace` already imports `internal/secret`,
and a redactor is inert data.

That shape has a property worth stating plainly: **it changes no signatures.**
`WorktreeApplyOptions{}` — the zero value the existing suite uses throughout —
already means "no reporter override, no redactor, do not tell me the setup
outcome", so every existing call site keeps compiling and keeps its current
behaviour. The 92 test references to `ApplyToWorktree` are mostly doc comments
and `TestApplyToWorktree*` function names; the real count is 25 test call sites
and 2 production ones, and none of them is bound by this change. PRD R15's
regression criterion falls out as a side effect rather than as work.

*Rejected: a new step in `apply.go` after Step 6.75.* Best inputs — the
pipeline's `Reporter` and redactor are already in scope, and setup-as-data is
the established posture there. But it covers only `niwa apply` and does nothing
for `worktree create`, which is where a bare worktree is actually born and the
first user story lives. It would also need the fan-out's four-guard enumeration
extracted first.

*Rejected: extend `worktree-hooks/` to a repo-scoped event.* Reuses a mechanism
that already exports worktree context and already runs on create and apply. But
it puts per-repo provisioning knowledge in a foreign repository's switch
statement, duplicating what each repo's own `scripts/setup/` already does for
its clone, and it inherits that mechanism's fatal failure posture, which is the
wrong one here.

*Rejected: document `worktree-hooks/` as the answer and change no code.* Real
and cheap, and it is what the field workaround already does. Rejected because
the repo-local convention then stops working at exactly the point it is most
useful, and because the upstream BRIEF is explicit that a documentation-only
outcome would be an abandonment of this feature rather than a smaller version
of it.

The honest cost of the chosen option: `ApplyToWorktree` is already a long
function running eight numbered steps of unrelated work, and this makes it
nine. That complexity is inherent to five callers sharing one contract, and the
alternative to carrying it here is duplicating it at the CLI-only sites.

### Decision 2 — the shape of a repo's opt-in

**Chosen: a per-repo switch that defaults off, plus a script-facing environment
signal.**

The switch is the operator's decision, in config, at repo granularity. It
resolves most-specific-wins through the cascade
`config.EffectiveReadEnvExample` (`internal/config/env_example.go`) already
establishes for `read_env_example`, with the default flipped to off.

The signal is the script author's decision, in code, at script granularity. It
is needed because one `scripts/setup/` can hold a dependency install that must
run per tree alongside a git-hooks installer that must not, since git hooks
live in the shared `git-common-dir`. A single switch is all-or-nothing for the
directory and cannot express that.

The two are not redundant, and the reason they both exist is migration rather
than elegance. With the signal alone, a repo opts out by having each script
check the environment and exit 0 — which works, but every setup script in
existence was written before the signal existed, so turning this on
unconditionally breaks them silently. Default-off is what keeps scripts written
under the old contract safe until someone has read the new one.

The marginal cost of the signal is also close to zero, because PRD R4 forces it
into existence anyway: a script has to be able to tell where it is running
without computing it from its working directory. Given that variable exists, the
mixed-directory case costs one guard line in the one script that needs it.

*Rejected: per-script opt-in by location*, for example only running scripts
under `scripts/setup/worktree/`. This is the strongest of the alternatives and
it dominates on one axis: it is safe by construction rather than safe by
default, since no existing script sits in the new location, and it gives
intra-repo granularity for free with one mechanism instead of two.

It loses on two counts. A script needed in both places must be duplicated or
symlinked, and the common case — a JavaScript repo where genuinely all of setup
should run per tree — has to restate the whole directory. More seriously, it
recreates this feature's own problem shape one layer up: a script in the wrong
directory silently does not run, with nothing to report, which is precisely the
failure the second half of this design exists to eliminate for hook events.

This one deserves a stated reversal condition rather than a verdict alone. The
case for two mechanisms rests on real repos holding both kinds of script under
one setup directory. If adopting repos' `scripts/setup/` directories turn out to
be uniformly one kind or the other, the mixed case barely occurs, and splitting
a directory once per repo would be cheaper than carrying a config field and an
environment convention forever.

**The condition has to name the right variable, and it is the switch rather
than the signal.** A survey of directory contents cannot touch the environment
signal at all: R4 requires that variable to exist whatever this decision says,
so a finding that no repo mixes would not remove it. What such a finding could
remove is the config switch — and the switch is defended on migration grounds,
that every setup script in existence predates this contract, which no survey of
directory contents speaks to either.

So the honest statement is narrower than a reversal condition. The signal is
R4's byproduct and costs nothing to keep. The switch earns its place by
protecting scripts written before the contract existed, and that argument
expires only when there are no such scripts left — which is a claim about
adoption over time, not about what any survey finds today. If that day comes,
the switch can go and the signal remains. Until then neither is carried on the
strength of the mixed-directory case, and this design should not have implied
that a headcount could settle it.

*Rejected: a per-repo switch alone.* Cannot express the mixed directory at all,
which is the case that motivated the switch being per-repo in the first place.

### Decision 3 — where the unknown-event diagnostic surfaces

**Chosen: validate in discovery against one named set, and make that specific
error non-fatal inside `runWorktreeHooks`.**

`DiscoverWorktreeHooks` checks each discovered event name against a set defined
beside `worktreeApplyEvent`. `runWorktreeHooks` turns that specific diagnostic
into a warning on the `stderr` it already receives, and otherwise behaves
exactly as it does today.

Three mechanism details are load-bearing, and each of them is the difference
between this decision and a regression.

**Discovery collects, it does not fail fast.** `DiscoverWorktreeHooks` returns
`(config.HooksConfig, error)` and every existing error path returns a nil map.
If validation followed that convention, a config repo holding a stale
`worktree-hooks/create/` alongside a live `worktree-hooks/apply/` would warn
about `create/` and then run none of the `apply/` hooks, because the map came
back nil. That configuration works today. So the valid hooks are returned
*alongside* the diagnostic: the walk builds the map, records unknown event names
as it goes, and reports them without discarding what it found. Getting this
wrong would reintroduce this feature's own failure class one layer over — a hook
that silently does not run — which is the thing the decision exists to remove.

**The downgrade is an exact match on that one error, and nothing else.**
`DiscoverWorktreeHooks` already returns errors from two other sources:
`validateWithinDir` and `os.ReadDir` on a hooks subdirectory. Those must keep
propagating as fatal errors exactly as they do now. The unknown-event case
carries its own sentinel, and the downgrade matches on that sentinel with
`errors.Is`, never on "discovery returned an error". A broader downgrade would
turn every fatal error on this path into best-effort logging on all four CLI
surfaces, and the code being edited already has the mixed-error shape that makes
the broad version the easy one to write. A test asserts that a fatal discovery
error still fails `ApplyToWorktree` after this change.

**A correction to what `validateWithinDir` is, because an earlier draft of this
document got it wrong and the wrong version was used to justify a security
claim.** It is a lexical check — `filepath.Abs`, `Clean`, and a prefix compare —
and it never resolves symlinks, so it does not detect a symlinked script pointing
outside `configDir`. Its own tests only ever call it directly with a hand-built
`..` path; nothing exercises it against a symlink. And within
`DiscoverWorktreeHooks` it cannot fail at all, because every path it guards is
built by `filepath.Join` from a bare `os.ReadDir` entry name, which contains no
separator. All three of its calls there are unreachable, and the function's doc
comment claiming scripts are "validated to stay within `configDir` (no symlink
escape)" is false.

That is a pre-existing gap and this design does not close it: deciding whether
these paths should resolve symlinks is its own call, with its own blast radius on
a config repo the operator already trusts. The defensive calls stay, the rule
above still binds them, and the reachable fatal path — an unreadable event
subdirectory — is what the test uses. The stale doc comment is corrected as part
of this work, because a comment that contradicts its own function is what misled
two readers here and is cheap to fix.

Collecting and short-circuiting have to coexist precisely, because they pull
against each other. On a containment or `ReadDir` failure the walk returns
immediately with **that error alone**, never joined with unknown-event
diagnostics already collected in the same pass. This matters more than it looks:
`errors.Is` returns true against a joined error containing the sentinel
anywhere inside it, so an implementation that aggregated both kinds into one
return before checking would let a fatal failure ride along inside what the
runner treats as the non-fatal case and be swallowed silently — turning a fatal
error into a warning through the very mechanism added to make the design safe.

**The consumer reads the event set, not the constant.** `runWorktreeHooks`
currently reads exactly `hooks[worktreeApplyEvent]`
(`worktree_content.go:1071`). Leaving that line alone would mean adding `create`
to the valid set makes `create/` *valid and still never run* — the original bug,
blessed by validation instead of reported by it, and PRD R12 explicitly requires
that adding an entry not change the code that consumes hooks. So the runner
iterates the event set in declared order and runs the scripts registered for
each event it is being asked to fire. Today that set has one member and the
behaviour is identical; the point is that the second member costs one entry.

Three things follow. The diagnostic reaches everyone, because
`runWorktreeHooks` is the one place all four CLI paths converge and it already
takes a writer that is wired on every one of them. The typo case is caught
identically to the wrong-directory case, since both are just discovered strings
checked against the same set — which was the originating issue's whole argument
for validating in discovery. And PRD R11 holds unconditionally rather than
contingently: the dangerous error never leaves the function, so the teardown is
never reached, rather than being reached and happening to retain.

It also gives PRD R10's acceptance criterion its teeth.
`TestDiscoverWorktreeHooks_TopLevelAndSubdir` currently asserts that a `create/`
directory yields two entries. Under this decision it asserts that `create/`
produces the diagnostic — the rule that now holds, not a test edited until it
stops failing — and a second test pins that the error does not propagate out of
`ApplyToWorktree`.

*Rejected: validate in the config-repo validation pass at `niwa apply`.* Only
the human running apply learns, and only if they happen to run it before the
next delegated create. A config-repo author works in the config repo and may
never run `niwa worktree create` at all. That is the blast radius the
motivating incident describes.

*Rejected: a deferred warning at discovery.* Same reachability problem, and
`DiscoverWorktreeHooks` takes only a `configDir` and returns
`(config.HooksConfig, error)` — there is no reporter to defer onto without
threading one through a function whose other callers do not want it.

The condition that would make this wrong: if a future requirement wanted the
diagnostic to fail `niwa apply` closed on a config defect, non-fatal is the
wrong default. Nothing in R10 through R12 asks for that.

### Decision 4 — how redaction holds on the standalone surfaces

**Chosen: build the redactor from the repo's own secret declarations, narrowed
by persisted source provenance for the one case declarations cannot see, with
the existing minimum-fragment-length guard underneath.**

The constraint is that `niwa worktree create` and `worktree apply` resolve no
secrets — that is what the inherit design is for — so a redactor constructed in
those processes holds no fragments and scrubs nothing, while the worktree does
contain the clone's byte-copied plaintext env file in the script's working
directory.

The env file cannot supply the answer. Resolved assignments are written as
plain `KEY=value`, and the record carrying provenance exists only for
*unresolved* keys, so a file-only approach would register every value in it and
over-scrub: a workspace with `NODE_ENV=production` would see "production"
replaced wherever it appeared in script output.

The declarations can. `env.secrets` and `env.vars` are sensitivity-coded
siblings in config, and `ApplyToWorktree` already holds `cfg`, so intersecting
the copied output against the repo's effective secret declarations registers
exactly the values that are actually in that tree. That is arguably a better
set than the apply path's redactor, which holds what the vault resolved this
run rather than what is on disk.

That the declarations are *available* on those surfaces is load-bearing and not
obvious: the standalone path runs the vault-free overlay merge before calling
`ApplyToWorktree` (`internal/cli/session_lifecycle_cmd.go:349-354`), so
overlay-supplied `env.secrets` declarations are present in `cfg` there. Without
that merge this decision would have an empty basis on exactly the surfaces it
exists to serve.

**Two further tables have to be in the registered set, and they are not env
declarations.** `ApplyToWorktree` writes resolved values from two other
independently-declared secret-capable slots into the worktree: MCP server
`env` and `headers` (`worktree_content.go:606`, typed as `MaybeSecret` in
`internal/config/mcp.go`), and `[session.env.vars]`
(`worktree_content.go:607`, `internal/config/session.go`). A `vault://`
reference in either is dropped on the standalone path because `cfg` is
unresolved, but a literal value is never "unresolved" and is written verbatim —
and neither table is an `env.secrets` or `env.vars` entry, so the intersection
above would not have registered it. These are covered on the apply path by the
pipeline redactor, which registers everything resolved in that run; they are
uncovered on precisely the two standalone commands this decision serves.

**But a hand-named list of tables is the wrong shape, and naming four is not
better than naming two.** Whatever list this decision writes goes stale the day
someone adds a fifth secret-capable slot that gets written into a worktree, and
nothing fails — which is this feature's own defect class, once more, in the
mechanism meant to prevent leaks.

The codebase already has the enumeration, built for a different purpose and
already obliged to be exhaustive. `walkVaultRefsForUnknownProvider`
(`internal/config/validate_vault_refs.go:228`) walks every `MaybeSecret` slot in
the workspace config — the env tables, the Claude settings and per-repo Claude
env, the instance env, MCP `env` and `headers`, `[session.env.vars]`, and the
`[files]` keys — because vault provider-name validation cannot work unless it
reaches all of them. So the registered set is derived by reusing that walk's
location enumeration rather than by maintaining a parallel list beside it. Two
lists collapse into one, and a future secret-capable field wired into a walk
that is already load-bearing feeds the redactor with no separate action. The
four tables above are what that walk finds today, not the specification.

**The coupling that buys is real, and it is pinned rather than accepted.**
Redaction now depends on a function maintained for vault provider-name
validation, so narrowing that walk for its own reasons would silently narrow
what gets scrubbed. That is the same shape this whole document has been closing
— a mechanism whose correctness depends on another that nobody is obliged to
keep aligned — and shipping it knowingly is worse than shipping it by accident
unless it fails loudly. So a test enumerates the secret-capable config slots and
asserts the walk covers them, which makes a narrowing break a test named for
redaction rather than quietly shrink coverage.

The instrument is worth naming, because it arrived four separate times in this
investigation and in three other issues alongside it: enumerate both sides and
assert they agree. It is what turns "discovery indexes an event nothing
consumes" into a caught error, and it is what turns this coupling into a CI
failure. Four independent arrivals at the same technique is better evidence for
it than any single argument.

The one gap is a `vault://` reference sitting in the `vars` table. Nothing
forbids it: `validate_vault_refs.go` denies vault references in content paths,
env file paths, provider config and identifier fields, but not in `env.vars`
values, and the config comment states that `secrets` wraps its values
"regardless of whether the configured value is a `vault://` reference" — so
sensitivity is coded by table, not by value shape, and neither direction is
guaranteed.

That gap is closed syntactically, from `cfg`, and deliberately *not* from
persisted provenance. The materializers do tag sources as vault or plaintext,
but the record cannot answer the question: `SourceEntry`
(`internal/agentplan/provenance.go:21`) carries `Kind` and a `SourceID` that
identifies the origin — a file path, or `provider-name/key` for vault — and
those entries hang off managed *files*, keyed by the path written. So it can
say that an env output file had at least one vault-sourced input; it cannot say
which key. Using it anyway would mean registering every value in any file that
had a vault source, which is precisely the over-scrub this decision rejects two
paragraphs up.

The prefix is the marker instead, and it is right there in `cfg` on these
surfaces. A `vault://` reference in the `vars` table is syntactically
identifiable — that is how `validate_vault_refs.go` finds them at all — so the
`vars` table is scanned for values whose configured form is a vault reference,
and those keys' resolved values are registered from the copied env file. Same
coverage, no dependency on a mapping that does not exist, and it composes with
the walk reuse below rather than sitting beside it as a special case.

The scrubbing itself costs nothing, because `RunSetupScripts` already routes
its output through `runCmdWithReporter` (`setup.go:125`), which is where
`DESIGN-post-clone-scripts.md` Decision C's scrubbing happens. Passing a
populated redactor is the whole job.

*Rejected: run setup only on the apply path, where a populated redactor already
exists.* This does not narrow the feature, it removes it. Create is precisely
when a worktree has nothing, so apply-only means the first thing anyone does in
a fresh tree still fails and they still run a command by hand — the manual step
this feature exists to delete.

*Rejected: accept unscrubbed output on those surfaces and document it.* Every
defect this feature addresses is a documented protection that silently does not
hold. Introducing one deliberately, in a design citing the decision it
contradicts, would be worse than the accidents, because the accidents were
nobody's choice.

*Rejected: the minimum-fragment-length guard alone.* Kept as a floor
underneath, since it costs nothing and is already the shared default, but not
as the answer. It protects against scrubbing `3000` out of every log line; it
does not protect a secret.

**A side effect worth taking.** `runWorktreeHooks` currently pipes its scripts'
output raw — `cmd.Stdout = stderr`, no scanner, no redactor
(`worktree_content.go:1097`) — on these same surfaces, in this same tree. So
Decision C's premise, that setup output would otherwise be the only unscrubbed
subprocess output in niwa, is already false at the exact call site this design
inserts next to. Routing the hook runner through the same choke point is a
small mechanical follow-on once the redactor exists, and it means the honest
description of this decision's outcome is that Decision C holds here for the
first time rather than that it was preserved.

### Decision 5 — the script environment contract

**Chosen: reuse the `NIWA_WORKTREE_*` shape, extended with an instance-root
anchor exported on both the clone and worktree paths.**

`runWorktreeHooks` already exports `NIWA_WORKTREE_PATH`, `_REPO`, `_PURPOSE`
and `_BRANCH` to scripts the guide calls the analog of setup scripts. Opening a
second namespace for the same information would be a divergence with nothing
behind it.

The anchor is the substantive addition, and it goes on both surfaces
deliberately. The purpose of exporting an instance root is to retire upward
path arithmetic — the idiom that computes `cd ../..` and lands on
`<instanceRoot>` from a clone but on `<instanceRoot>/.niwa` from a worktree, a
directory that exists and is writable, so the script succeeds, writes to the
wrong place, and exits 0. An anchor present only in worktrees leaves that idiom
working in clones and therefore still load-bearing, which fixes the symptom in
the new location and leaves the fragile pattern in the old one. PRD R15 was
written to permit this.

Nothing niwa resolves from a secret source goes into that environment. The
concern that adding `cmd.Env` at all contradicts
`DESIGN-post-clone-scripts.md`'s security section does not survive reading it:
the load-bearing claim there is that secrets reach setup scripts by file only,
and the absent `cmd.Env` is the evidence offered for that claim in a paragraph
about secret exposure. Go inherits the parent environment when `cmd.Env` is
nil, so appending non-secret entries to `os.Environ()` yields the identical
inherited set plus those entries. The same design already concedes the
inherited environment is uncontrolled — "whatever the operator exported before
invoking niwa" — so its exact contents were never a property being relied on.

### Decision 6 — the pipeline step order

**Chosen: move Step 6.6 to after Step 6.75.**

The worktree fan-out currently runs at `apply.go:1921` and the clone's own
setup-script step at `:1951`, so worktree provisioning would consume the
previous apply's clone-setup output. The move is safe on five independent
grounds: every input to `refreshWorktreeEnvs` is produced at or before Step 6.5,
and the load-bearing one is the clone's env output files written by Step 6.5's
materializer, not by setup scripts; its `[]ManagedFile` output is read in
exactly one later place, inside Step 7; `exemptPaths` has no read between the
two steps and its consumer flattens it into a map, so order is irrelevant; no
design document fixes their relative order, the stated constraint being only
"after the clone materializer loop", which both satisfy; and no existing test
observes the ordering, because nothing exercises both steps in one run.

That last point is also the reason the move needs a test of its own. An
ordering established by analysis and held in place by nothing gets reverted by
the next person who has a reason to move it.

*Rejected: forbid worktree setup from depending on clone setup within one
apply.* Expressible, but it makes a real and reasonable pattern — clone setup
produces something shared, worktree setup consumes it — permanently
unavailable, to avoid a reordering that costs nothing.

*Rejected: accept the inversion and document it.* Cheapest, and defensible
while no repo has that dependency. Rejected because the documentation would
have to explain an ordering that exists for no reason, which is a worse
artifact than the swap.

## Decision Outcome

Setup scripts run against a worktree from inside `ApplyToWorktree`, next to the
hook runner, when the repo's switch is on. The outcome — including failure —
leaves through a sink on the options struct, so each of the five entry paths
decides for itself what to do with it, and none of them sees an error.
Discovery validates hook event names against one named set, and the hook runner
downgrades that specific error to a warning so it cannot reach the teardown.
Scripts receive the worktree context they already receive from hooks, plus an
instance-root anchor on both surfaces. Output is scrubbed against a redactor
built from the repo's own declarations. The fan-out moves below the clone's
setup step.

The parts cohere because they are all answers to the same constraint. Nothing
new escapes `ApplyToWorktree` as an error — not a setup failure, not a
discovery failure — so the function's error surface is unchanged, and the
guard-inversion defect in #285 stays out of reach without this design having to
fix it.

## Solution Architecture

### Components

| Component | Change |
|---|---|
| `internal/config` | A per-repo worktree-setup switch on the workspace metadata and the repo override, with a resolver mirroring `EffectiveReadEnvExample` and an off default. |
| `internal/vault/resolve/deepcopy.go` | `deepCopyRepos` rebuilds `config.RepoOverride` field by field (`:87-104`, enumerating `SetupDir`, `ReadEnvExample`, `EnvExamplePolicy`, `EnvOutput`). The new switch is added there. This is the silent-failure site: a field omitted here is lost only on the vault-resolved path, so a repo's opt-in would work on `worktree create` and silently not on `niwa apply`, defeating R2 in a way no existing test catches — there is no exhaustive-struct linter and no field-count guard. |
| `internal/workspace/discover.go` | `DiscoverWorktreeHooks` validates each discovered event name against the valid-event set and returns a typed error naming the path and the set. |
| `internal/workspace/worktree_content.go` | The valid-event set beside `worktreeApplyEvent`. `runWorktreeHooks` downgrades the validation error to a warning. A setup step after the hook step in `ApplyToWorktree`. New nil-tolerant `Setup`, `Reporter` and `Redactor` fields on `WorktreeApplyOptions`. |
| `internal/workspace/setup.go` | Unchanged. It is called with a different `repoDir` and a populated redactor; nothing about it moves. |
| `internal/workspace/apply.go` | Step 6.6 relocated below Step 6.75. The fan-out passes the pipeline's reporter and redactor, and reads the setup sink into the existing deferred-warning and verdict machinery. |
| `internal/cli/session_lifecycle_cmd.go` | `applyContentToWorktree` constructs the sink, builds a redactor from declarations, and reports the outcome inline. Its own signature is unchanged. |
| `docs/guides/` | The published setup-script contract, and the corrected network-access sentence in the worktree guide. |

### What the apply fan-out does not reach, and why that is the right call

The fan-out already skips worktrees that are missing from disk
(`apply.go:2408`), attached — meaning another process holds the lock
(`:2415`) — or git-detached (`:2426`). Setup inherits that skip set, so
`niwa apply` provisions every live worktree of an opted-in repo except the ones
currently being worked in.

That deserves stating rather than inheriting, because the attached case is
exactly the population the second user story is about.

The honest answer is that the skip is not this design's, and this design does
not relitigate it. It is a lock-safety guard — `apply.go:2417` says why in its
own words, that apply must never reap another process's lock — and setup rides
along with the content install it already gates. Arguing instead that running
`npm ci` under a live agent would be hazardous would prove too much: the same
scripts against the same live tree are exactly what `niwa worktree apply`
offers as the override two sentences from here, so if that hazard were the
reason, the override would be unsafe.

What matters for the user story is that the deferral is short and does not
require anything of the operator. A worktree is skipped only while a live
process holds its lock: `ReadAttachState` reports attached only when the owning
PID is alive (`internal/worktree/attach_state.go:76`), and a dead owner yields
stale, which the fan-out's test does not match — so a crashed agent's worktree
is provisioned on the next apply with nobody reaping anything. It does not
depend on a clean detach. And an operator who wants it now runs
`niwa worktree apply` against that one worktree. So "I should not have to visit
each worktree by hand" holds for every worktree not currently in use, and for
the ones that are, provisioning arrives on its own shortly after.

PRD R16 is satisfied as a byproduct of call cardinality rather than by new
bookkeeping: each of the two invocation sites calls `ApplyToWorktree` once per
worktree per invocation, and setup sits inside it. PRD R17 holds because the
switch is resolved before the runner is entered — a repo that has not opted in
reaches no `exec.Command`, rather than reaching one that exits early.

### Data flow on a failure

A setup script exits non-zero. `RunSetupScripts` records it in the
`SetupResult` and stops that repo's remaining scripts, exactly as it does for a
clone. `ApplyToWorktree` writes the result into `opts.Setup` if the caller
supplied a sink, and returns its normal `([]string, nil)`. Each caller then
does what suits it: the two interactive commands print the failure and exit as
they otherwise would; the delegated create path prints it and still emits the
worktree path on stdout, because the worktree exists and is usable; the fan-out
folds it into the deferred warnings and the counted verdict below the apply
summary. No caller sees an error, so no caller reconciles, so nothing is
destroyed.

### Attribution

`RunSetupScripts` derives its display name from `filepath.Base(repoDir)`, which
for a worktree is `<repo>-<sid>` — which is what distinguishes one worktree's
output from another's and from the clone's when several interleave. The
caller-side warning and verdict use the plain repo name and have no dedupe, so
a repo failing in its clone and two worktrees would today render as three
identical names. PRD R8 requires those to be distinguishable, so the verdict
carries the location rather than the bare repo name.

## Implementation Approach

**Phase 1 — the event set and its non-fatal surface.** The valid-event set, the
collect-then-report validation in discovery, the sentinel and the `errors.Is`
downgrade in the hook runner, the runner switched from reading the constant to
iterating the set, the rewritten discovery test asserting the new rule, a test
pinning that the unknown-event error does not propagate out of
`ApplyToWorktree`, a test pinning that a fatal discovery error still does, and a
test that a stale `create/` alongside a live `apply/` still runs the `apply/`
hooks. The fatal-error test uses an unreadable event subdirectory rather than a
symlink escape, for the reason given in Decision 3. This lands first because it is what makes adding an event a
one-entry change, and because doing it second would mean writing the setup step
against a single-event vocabulary and then reworking it.

**Phase 2 — the options struct and the pipeline order.** The `Setup`,
`Reporter` and `Redactor` fields with their nil-tolerant behaviour, the Step 6.6
relocation, and the ordering test that holds it in place. No behaviour change
yet: the fields are unset everywhere and the zero value means what it means
today.

**Phase 3 — the config switch and the setup step.** The config fields, the
resolver, the addition to `deepCopyRepos` with a test that the opt-in survives a
vault-resolved apply, the call in `ApplyToWorktree`, the environment the script
receives, and the per-caller reporting of the sink. The fan-out's three skip
warnings say "skipping env refresh" (`apply.go:2411`, `:2416`, `:2425`); once
setup runs inside `ApplyToWorktree` a skipped worktree is also one whose setup
did not run, so the wording is widened to say so. This phase carries the
`NIWA_RESPONSE_FILE` check from Security Considerations: before setup runs on a
surface, the unset must be in effect there. If the separate fix for the
persistent-hook shadowing has not landed by then, this phase does not enable
setup on the affected surfaces until it verifies the variable is gone.

**Phase 4 — redaction.** The declaration-derived redactor, built from the
existing exhaustive walk's slot enumeration and the syntactic `vault://` scan of
the `vars` table, wired on the standalone paths; the test that pins the walk
against the secret-capable slots so a narrowing breaks a redaction-named test;
and the hook runner routed through the same choke point.

**Phase 5 — the published contract.** The setup-script contract in the guides,
and the corrected network-access sentence.

## Security Considerations

**Secrets stay file-only.** No value niwa resolves from a secret source enters
a setup script's environment. The `NIWA_*` entries added are paths and names.
This preserves `DESIGN-post-clone-scripts.md`'s stated boundary rather than
weakening it, and the boundary is what that design's threat model actually
rests on — not the incidental fact that `cmd.Env` was nil.

**Redaction improves on both surfaces.** Today the standalone worktree paths
have no redactor available at all, and the hook runner streams raw script
output in a tree holding copied plaintext. After this design, setup output is
scrubbed against a redactor built from the repo's declarations, and the hook
runner is routed through the same choke point. The residual limits of Decision
C carry over unchanged: coverage is limited to values the redactor knows about,
and a script that transforms a secret before printing it defeats any redactor.

**Trust boundary is unchanged.** Setup scripts are repo-provided code, and the
boundary is the one `git clone` already draws. Running them in a worktree adds
no new source of code — the same repo's scripts, from the same clone's history.
Worktree hooks likewise come from the config repo the operator already trusts.

**The teardown interaction is the security-relevant one, and it is closed by
construction.** A failure that travels as an error can reach `DestroySession`
and delete a worktree whose ignored contents may include real work. Every new
failure mode this design introduces travels as data instead. That property is
load-bearing and fragile: a later change making any of them fatal reopens
#285's path immediately. It is stated in the Consequences below as a constraint
on the code rather than a note.

**No new file is written outside the worktree**, and no path is composed from
anything a repo controls. The setup directory is resolved from workspace
config, not from the repo, exactly as on the clone path.

### A dependency this design must not inherit by luck

niwa already treats one environment variable as something children must never
receive. `captureNiwaResponseFile` (`internal/cli/landing.go:24`) caches
`NIWA_RESPONSE_FILE` and unsets it "so subprocesses (git, gh, hooks) don't
inherit it", because a child that writes to that file redirects the shell
wrapper's `cd` target.

That unset does not currently happen on the worktree commands. It runs from the
root command's `PersistentPreRunE` (`internal/cli/root.go:44`), and cobra
executes only the first persistent hook it finds walking from the leaf command
up to the root unless `EnableTraverseRunHooks` is set — the loop is at
`command.go:983-997` of cobra v1.10.2, and it `break`s after the first command
carrying either hook variant. `sessionCmd` declares its own `PersistentPreRun`
for the deprecation notice (`internal/cli/session.go:42`) and is the parent of
every worktree subcommand (`session.go:12-13`, `session_lifecycle_cmd.go:21-23`,
`session_from_hook_cmd.go:18`). `EnableTraverseRunHooks` appears nowhere in this
repository. So for `worktree create`, `worktree apply` and the delegated create,
the root hook never runs, the unset never happens, and `runWorktreeHooks`'
`cmd.Env = append(os.Environ(), ...)` (`worktree_content.go:1102`) hands every
script a live, writable `NIWA_RESPONSE_FILE`.

Today that reaches config-repo scripts, which come from a repository the
operator has already chosen to trust. R1 puts **repo-authored** scripts on the
same path, and the whole provenance distinction this feature rests on is that
those are a different trust class. Extending the surface without the unset in
place would hand the weaker-trust class a channel to redirect the operator's
shell, on a surface where niwa's own code says that channel must be unreachable.

This design therefore depends on the unset being in effect on every surface R1
covers, and does not assume it. A separate change fixing the hook shadowing is
in flight; if it lands first this closes on its own. If it has not, the
implementation verifies the unset holds on all three surfaces before enabling
setup on them, and the phase that adds the setup call carries that check. An
ordering that holds by accident is the thing this whole feature exists to stop
trusting.

It also completes the environment contract R13 has to publish. R5 is a rule
about what niwa *adds* to a script's environment; this is a rule about what
niwa *removes* before the script runs. The published contract states both, so a
script author can learn that `NIWA_RESPONSE_FILE` is not part of their
environment — which right now it silently is.

## Consequences

### Positive

- A worktree of an opted-in repo is provisioned by the repo's own scripts, on
  create and on every apply, and converges without anyone visiting it.
- `ApplyToWorktree` gains no new error returns, so the guard-inversion defect
  stays out of reach without this change having to fix it.
- Two silent failures become loud: an unconsumed hook event now reports, and a
  setup failure in a worktree is attributed to that worktree.
- Unscrubbed subprocess output on the worktree paths is closed as a side
  effect.
- The setup-script contract exists in documentation for the first time.
- No signatures change, so the existing suite keeps compiling unchanged, which
  is what makes the clone-path regression guarantee cheap to hold.

### Negative

- `ApplyToWorktree` grows a ninth numbered concern in a function already long
  enough to need reading start to finish.
- Two mechanisms carry the opt-in where one might have, and the case for the
  second rests on repos holding mixed setup directories — an assumption stated
  in Decision 2 with the finding that would overturn it.
- Every-apply provisioning relies on setup scripts being idempotent, a contract
  that until now has been asserted in one design-document bullet, enforced
  nowhere, and published nowhere.
- `deepCopyRepos` enumerates `RepoOverride` fields by hand, so the new switch
  must be added there too, and omitting it fails silently on the vault-resolved
  path alone — the opt-in would work on `worktree create` and not on
  `niwa apply`. Nothing in the toolchain catches that.
- `niwa apply` does not provision worktrees that are attached, detached or
  missing, so an agent's live worktree is skipped until it detaches.

### Mitigations

- The default is off, per repo, so the idempotency reliance is only taken on by
  a repo whose author opted in after the contract became readable.
- Phase 5 publishes that contract, and Phase 1 lands the event set before
  anything depends on its shape.
- The ordering test in Phase 2 keeps the step relocation from being silently
  reverted.
- The failure-as-data property is written into the code's own comments where
  the sink is filled, so the next person meets it as a constraint rather than
  discovering it.
- The deep-copy site is named in the Components table and in Phase 3 rather
  than left to be remembered, and a test that a repo's opt-in survives a
  vault-resolved apply pins it.
- The attached-worktree skip is documented in the setup-script contract, with
  `niwa worktree apply` named as the way to force provisioning of a worktree
  that is in use.
