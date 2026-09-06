---
schema: prd/v1
status: Done
problem: |
  A niwa worktree receives every accessory a repo checkout gets except the one
  produced by the repo's own setup scripts, so it arrives with tracked source
  and no dependency tree, and the resulting failure names a package rather than
  the missing provisioning. The same silence covers worktree hooks: an event
  name niwa does not consume is discovered, indexed, and never run, with no
  diagnostic. Both are niwa reporting a provisioning that did not happen.
goals: |
  A worktree is provisioned the way its repo says it should be, on the surfaces
  where a repo checkout would be, and visibly not where the repo says a step
  must not run per-tree. The contract a setup script runs under is published
  rather than implied. A misconfigured config repo produces a diagnostic and
  never destroys a worktree an agent is working in.
upstream: docs/briefs/BRIEF-worktree-setup-scripts.md
motivating_context: |
  A session could not run one test file in a fresh worktree and pushed the
  question to CI, a twelve-minute round trip for something a provisioned
  worktree answers in seconds. A repo that uses niwa documents the workaround in
  its README. Separately, a hook written under worktree-hooks/create/ was
  discovered, indexed, and never ran, found only by a positive control that
  finished in zero seconds.
---

# PRD: Worktree Setup Scripts

## Status

Done

## Problem Statement

`niwa apply` runs a repo's `scripts/setup/` against the clone it materializes.
Nothing runs them against a worktree. A worktree therefore gets the repo's
tracked source, its CLAUDE content, its materialized environment, its settings
and hooks and rules import, and none of its dependency tree.

Whoever is standing in that worktree pays for it, and the failure does not name
its cause: the symptom reported from the field was a bundler error about a
package's `exports` field, which reads as a broken dependency. A session that
wanted to run one test file pushed the question to CI instead, a twelve-minute
round trip. A repo that uses niwa now documents the workaround in its README,
telling readers that in "a git worktree (which niwa does not provision)" they
have to run the setup scripts themselves.

Running them naively isn't the fix, and this is the half that makes the feature
a design rather than a patch. Setup scripts get the repo root as their working
directory and do path arithmetic upward against it. From a clone,
`<instanceRoot>/<group>/<repo>`, `cd ../..` reaches the instance root. From a
worktree, `<instanceRoot>/.niwa/worktrees/<repo>-<sid>`, the same expression
reaches `<instanceRoot>/.niwa` — a directory that exists and is writable, so the
script succeeds, writes to the wrong place, and exits 0. There's no error for
any warning stream to carry. The group segment is absent from the worktree path
as well, so a script reaching sideways is wrong in a second, independent way.
The correct hop differs by depth, which means no single relative expression is
right in both locations.

And the contract that would let a script tell the difference has never been
published. `scripts/setup` appears in exactly two files under `docs/`, both
design documents; the idempotency requirement exists as one decision-driver
bullet in one of them and nowhere a repo author would look.

The same failure class sits one layer over. Worktree hooks are discovered from
the config repo by reading event names straight off the filesystem with no
validation, and exactly one event, `apply`, is ever consumed. A hook under
`worktree-hooks/create/` is discovered, indexed, and never run; so is a hook
whose filename carries a typo. Nothing reports either. The worktree guide says
these scripts "run on every `create` and `apply`", which reads as though a
`create/` directory works, and the test suite asserts that discovery returns
such a directory's contents — so the documentation and the tests both certify
the behaviour the bug report calls broken.

The two halves are mechanically coupled. What counts as a valid worktree
lifecycle event decides whether setup can run at a moment other than the
existing `apply` event, so settling either alone produces an answer the other
has to undo.

## Goals

- A worktree of a repo that opts in is provisioned by that repo's own setup
  scripts, and converges without anyone visiting it by hand.
- A repo whose setup does shared-state work rather than per-tree work can say
  so, and a script can tell where it is running without computing it from a
  path.
- Nothing this feature adds — a setup script that fails, a hook event name that
  is wrong — can destroy a worktree someone is working in. A pre-existing path
  that already can is named in Out of Scope rather than quietly covered by this
  sentence.
- The contract a setup script runs under is written down where a repo author
  will find it.
- A hook registered under a name niwa does not consume produces a diagnostic
  instead of silence.

## User Stories

**As a coding agent handed a delegated worktree**, I want the repo's dependency
tree present when I arrive, so that running one test file answers the question
in seconds instead of sending it to CI.

**As a workspace operator who has just added a setup step to a repo**, I want
`niwa apply` to bring the repo's existing worktrees to the configured state, so
that I don't have to know which checkouts predate the script or visit each one.

**As a repo maintainer whose `scripts/setup/` mixes per-tree and shared-state
work**, I want to turn worktree provisioning on for my repo and have each script
able to gate itself, so that the dependency install runs per tree and the git
hooks installer doesn't.

**As a config-repo author who registered a hook under an event name**, I want to
be told the name is not one niwa consumes, so that I find out from niwa rather
than from a positive control that completes in zero seconds.

**As an agent whose worktree creation hit a bad config**, I want the worktree
still to be there, so that a typo in a different repository does not delete work
in flight.

## Requirements

### Functional

**R1.** A repo's own setup scripts run against a worktree of that repo, with the
worktree as the working directory, using the same discovery rules, ordering, and
executable-bit policy that govern the clone run.

**R2.** Setup runs against a worktree when the worktree is created, and again
when the workspace is applied, so that a worktree created before a repo added or
changed a setup script converges without manual action.

**R3.** Worktree setup is off by default and enabled per repo. A workspace-level
default may exist, but the value that governs a run is resolved per repo, and
enabling it for one repo changes no other repo's behaviour.

**R4.** A setup script can determine, without computing anything from its
working directory, (a) that it is running against a worktree rather than a
clone, (b) which worktree, and (c) where the instance root is. The third is
required because the instance root is the anchor that upward path arithmetic was
reaching for, and it is not currently exported to any script on any surface.

**R5.** Secrets continue to reach setup scripts by file only. No value niwa
resolves from a secret source is exported into a setup script's environment.

**R6.** A worktree setup failure is carried as data, not returned as an error,
at every point on the worktree provisioning path where it could be observed. No
setup failure may cause a worktree to be removed, or a session record to be
moved to a terminal state, on any surface.

**R7.** A worktree setup failure does not change the process exit code of the
command that triggered it, on any surface.

**R8.** A setup failure is attributed to the specific worktree it occurred in,
distinguishably from that repo's clone and from other worktrees of the same
repo, everywhere niwa reports it — both where a script's output is streamed and
where failed repos are counted.

**R9.** Setup-script output produced in a worktree is scrubbed. The target is the
clone path's standard — a redactor holding the values actually present in that
tree. Where the provenance needed to register those values precisely is
unavailable on a surface, the floor is the existing minimum-fragment-length
guard, which is weaker than the target and is named as a floor rather than as
equivalent; the DESIGN states which of the two it reached and why. Declining to
run setup on a surface is not an acceptable way to satisfy this requirement —
see D6.

**R10.** Worktree-hook event names are validated against the set of events niwa
consumes. A directory or script registering an event outside that set produces a
diagnostic naming the offending path and the valid set.

**R11.** Validating a worktree-hook event name never causes a worktree to be
removed or a session to be moved to a terminal state. A config repo containing a
stale `worktree-hooks/create/` directory must not make the delegated
`WorktreeCreate` path destroy the worktree it has just created.

**R12.** The set of worktree lifecycle events niwa consumes is defined in one
place. Adding an event is adding one entry to that set; it does not require
changing validation logic or the code that consumes hooks.

**R13.** The contract a setup script runs under is documented where a repo
author will find it — not only in a design document. It states at minimum the
working directory, what the script may and may not assume about that directory,
the environment it receives, the ordering, the failure policy, and the
idempotency requirement.

**R14.** `docs/guides/worktree.md`'s claim that `create` and `apply` need "no
network access" is corrected to state what is actually guaranteed: that neither
resolves secrets, and neither depends on a reachable secret source.

**R15.** Setup-script behaviour against a clone is unchanged — same working
directory, same ordering, same failure policy, same output shape, same exit
code — whether or not any repo has opted into worktree setup. Adding non-secret
niwa-supplied environment entries on the clone path is not a change this
requirement forbids, and R4(c)'s anchor is expected on both surfaces: an anchor
that exists only in worktrees leaves upward path arithmetic working in clones
and therefore still load-bearing, which fixes the symptom in the new location
and leaves the fragile idiom in the old one.

### Non-functional

**R16.** Enabling worktree setup for a repo causes at most one setup run per
worktree per invocation of a surface that provisions that worktree. A single
`niwa apply` doesn't run a repo's setup more than once per live worktree.

**R17.** A repo that hasn't opted in spawns no setup process in any worktree, on
any surface.

## Acceptance Criteria

- [ ] A repo with an opted-in `scripts/setup/` containing an executable script,
      and a worktree of that repo, has the script's effect present in the
      worktree after `niwa worktree create`, and the script ran with the
      worktree as its working directory. (R1, R2)
- [ ] The same is true after `niwa apply` for a worktree that already existed
      before the script was added. (R2)
- [ ] A repo with no opt-in setting runs no setup in its worktrees, and the
      clone-path run is unaffected. (R3, R15)
- [ ] An opt-in on repo A does not cause setup to run in a worktree of repo B.
      (R3)
- [ ] A setup script that dumps its own environment records the worktree path,
      the repo name, and the instance root when run in a worktree. At least one
      of those entries is absent when the same script runs against the clone, so
      its presence alone distinguishes the two. (R4)
- [ ] A setup script running against a clone observes the same environment it
      does today plus, at most, non-secret niwa-supplied entries; no value
      registered with the secret redactor appears in any setup script's
      environment on any surface. (R5, R15)
- [ ] A setup script that exits non-zero in a worktree created through the
      delegated `WorktreeCreate` path leaves the worktree present on disk and its
      session record non-terminal, and this holds whether the script wrote only
      git-ignored output or wrote nothing at all. (R6)
- [ ] The same failure leaves the exit code of `niwa worktree create`, of
      `niwa worktree apply`, and of `niwa apply` unchanged from the
      no-setup-script case. (R7)
- [ ] A repo whose setup fails in its clone and in two of its worktrees produces
      a verdict line that names three distinct locations rather than the same
      repo name three times. (R8)
- [ ] A secret value present in a worktree's inherited env file, echoed by a
      worktree setup script, does not appear unredacted in niwa's output on any
      surface where worktree setup runs. (R9)
- [ ] A config repo containing `worktree-hooks/create/01-x.sh` produces a
      diagnostic naming that path and the set of events niwa consumes. (R10)
- [ ] With that same config repo, the delegated `WorktreeCreate` path completes
      and the worktree it created is still present on disk afterwards; no
      session is moved to a terminal state. This is asserted by a test, not by
      argument. (R11)
- [ ] The test covering per-event-directory discovery asserts that a `create/`
      directory produces R10's diagnostic, rather than asserting that its
      scripts are returned. (R10, R12)
- [ ] A repo author reading the documentation — not a design document — can find
      the working directory a setup script gets, what it may assume about it,
      the environment it receives, the failure policy, and the idempotency
      requirement. (R13)
- [ ] `docs/guides/worktree.md` no longer claims `create` and `apply` need no
      network access, and states the secret-resolution guarantee instead. (R14)
- [ ] The existing setup-script test suite passes unchanged. (R15)
- [ ] A single `niwa apply` over a repo with three live worktrees runs that
      repo's setup at most three times in worktrees, in addition to the
      unchanged clone run. (R16)
- [ ] A repo with no opt-in setting spawns no setup process in any of its
      worktrees during `niwa worktree create`, `niwa worktree apply`, or
      `niwa apply`. (R17)

## Decisions and Trade-offs

### D1. Setup runs on create and on every apply, not on create alone

**Decided:** R2, both surfaces.

**Alternatives:** create-only, which avoids re-running setup once per live
worktree on every apply.

**Why:** create-only reintroduces exactly the drift this feature exists to
close. A worktree created before a repo added a setup script would never be
provisioned, with no command to fix it but a manual per-worktree apply — the
same shape of divergence `DESIGN-niwa-default-worktree.md` Decision 9 closed on
principle, in a feature whose argument is that a worktree should match a
checkout. The cost objection is also weaker than it first looked: the
double-digit figure is a cold provisioning run, and the measured warm re-run
against an already-provisioned worktree is under a second, because the script's
own guard skips the expensive step.

**What this decision rests on, stated plainly:** it rests on the idempotency
contract, which today is asserted in one design-document bullet, enforced
nowhere, and published nowhere a repo author would see it. That is why R13 is a
requirement of this feature rather than a nicety — relying on a contract nobody
has been shown isn't a decision, it's an assumption. If the DESIGN concludes
the contract cannot be relied on even once published, create-only becomes the
safe answer, and it then owes the second user story an explicit answer rather
than a silent contradiction.

### D2. Failure is data on every surface, not only where it is convenient

**Decided:** R6.

**Alternatives:** let a setup failure propagate as an error, as worktree hook
failures do today.

**Why:** the four entry paths into worktree provisioning already disagree about
what an error means. An interactive `niwa worktree create` retains the worktree
and tells the operator to re-sync. `niwa apply`'s per-worktree fan-out warns,
forward-carries, and continues at exit 0. The delegated `WorktreeCreate` path
runs a guarded teardown that deletes the worktree and ends the session.

The teardown's guard is what makes error-propagation unacceptable rather than
merely inconsistent. It retains a worktree only when `git status --porcelain`
reports it dirty, and ignored paths are not reported. So a setup script that
fails after writing only into `node_modules` — the exact shape this feature
exists to serve, and the well-behaved shape — leaves the tree reading clean and
the worktree is deleted. A script that fails after writing an unignored log file
leaves it dirty and the worktree survives. Retention is a property of what the
script happened to touch rather than of the failure, and it runs backwards: the
better-behaved the script, the more likely its work is destroyed. A script that
writes only into ignored paths is the well-behaved one, and it's the one whose
worktree gets deleted. That inverts every intuition a script author brings.

The instance pipeline already reached this conclusion one level up. Its
setup-script step carries failure as data with the comment that "the pipeline's
error path must not be reached, since on create it deletes the instance root."
This is the same hazard one level down.

**The dependency runs one way, and a future maintainer needs to know which
way.** This feature is safe from the teardown path *because its own failures are
data rather than errors* — not because the retention guard is correct. The guard
is not correct, which is why the general case is named in Out of Scope. So D2
and R11 are the whole of what keeps this feature away from `DestroySession`. A
later change that makes any of these failure modes fatal — "surely a broken
setup script should fail the create" is a reasonable-sounding instinct — puts the
destruction path back in reach immediately. Treat that as a documented
constraint on this code rather than a preference.

A config author who had to write one of these scripts arrived at the same shape
independently: a worktree bootstrap hook shipped while this was being scoped
exits 0 on every failure path — dependency install failure, build failure,
missing toolchain — precisely because a non-zero exit stops later hooks and the
delegated create path tears the worktree down. That is the posture this feature
should require of setup scripts, and R13's published contract is where a script
author learns it.

### D3. The exit code stays 0 on a setup failure

**Decided:** R7. Cited, not re-derived.

`DESIGN-post-clone-scripts.md` Decision B settled this for the clone path: the
shell wrapper's `cd` into a new instance is gated on exit 0, so a fatal setup
failure would strand the operator outside the very instance they need to enter
to fix the script. Worktree create is gated the same way. The deferred
`setup_policy = "warn" | "fail"` key specified in that same decision remains the
route for an operator who wants a stricter posture, and this feature does not
pull it forward.

### D4. Adding `NIWA_*` variables to a setup script's environment does not
contradict the post-clone-scripts design

**Decided:** R4 and R5 together.

The concern raised was that `DESIGN-post-clone-scripts.md`'s security section
reasons about `RunSetupScripts` setting no `cmd.Env`, so adding one changes a
property that design leaned on. Read against the text, it doesn't. The
load-bearing claim there is that secrets reach setup scripts *by file only* —
"there is no `env`-borne route to a niwa-managed secret" — and the absent
`cmd.Env` is the evidence offered for that claim, in a paragraph whose subject is
secret exposure. Go inherits the parent environment when `cmd.Env` is nil, so
appending non-secret entries to `os.Environ()` produces the identical inherited
set plus those entries and leaves the secret claim intact. The same design
already concedes the inherited environment is uncontrolled, "whatever the
operator exported before invoking niwa", so its exact contents were never being
relied on. And the worktree-hook runner in the same package already exports
`NIWA_WORKTREE_*` this way, to scripts the guide calls the analog of setup
scripts.

What would contradict the text is exporting resolved secret material. R5 keeps
that shut.

### D5. Where the unknown-event diagnostic surfaces is a requirement about blast radius, not a mechanism

**Decided:** R10 and R11 as separate requirements.

The originating issue recommended that discovery reject unknown event names,
because that also catches a plain typo. Implemented literally, that's
destructive: a discovery error propagates through the worktree content install,
and on the delegated create path a failed content install runs the guarded
teardown. Nothing has been written into the worktree at that point except
niwa's own files, all of which are deliberately git-excluded so the tree reads
clean — so the teardown succeeds every time. A stale `worktree-hooks/create/`
directory in the config repo would hard-fail and self-destruct every delegated
worktree creation in the workspace, deterministically.

So this PRD requires the diagnostic (R10) and separately forbids the blast
radius (R11), and leaves the DESIGN to choose the surface that satisfies both:
validation during the config-repo validation pass at `niwa apply`, a deferred
warning rather than an error, or a non-fatal discovery error scoped to the hook
runner. They have different blast radii and the choice is a real one.

### D6. Redaction is restored on the worktree surfaces rather than narrowed around

**Decided:** R9 is met by making redaction work where setup runs, not by
declining to run setup where redaction is unavailable.

**The constraint.** A `niwa worktree create` or `niwa worktree apply` process
resolves no secrets — that's what the inherit design is for — so a redactor
constructed there holds no registered fragments and scrubs nothing. But the
worktree does contain the clone's byte-copied plaintext env file, at 0600, in
the script's working directory. That's the exact file
`DESIGN-post-clone-scripts.md` Decision C's threat model names.

**Alternatives, both rejected.** *Run worktree setup only on the apply path*:
this doesn't narrow the feature, it removes it. Create is precisely when a
worktree has nothing, so apply-only means the first thing anyone does in a fresh
tree still fails and they still run a command by hand — the manual step this
feature exists to delete, and a direct contradiction of the first user story.
*Accept unscrubbed output on those surfaces and document it*: rejected on the
stronger of the two grounds available. Every defect this feature addresses is a
documented protection that silently doesn't hold. Introducing one deliberately,
in a document that cites the decision it's contradicting, is worse than the
accidents, because the accidents were nobody's choice.

**What makes the chosen option honest rather than optimistic:** it reads nothing
new. The plaintext is already in the tree, placed there by the same process, so
deriving fragments from what's on disk expands no exposure — it restores the
property Decision C wanted in the one place the inherit design made it
unavailable.

**One thing the DESIGN must not do.** Registering every value in the copied env
file would scrub ordinary configuration out of script output — a workspace with
`NODE_ENV=production` would see "production" replaced wherever it appeared. The
file itself can't distinguish: resolved assignments are written as plain
`KEY=value` with no provenance, since the record carrying provenance exists only
for unresolved keys. The provenance is recoverable elsewhere — the config's
sensitivity-coded tables, and the vault-versus-plaintext source kind the
materializers already tag — and the existing minimum-fragment-length guard
handles short values. The DESIGN picks the route and states it, including if it
finds the provenance is not reachable on those surfaces and falls back to the
length guard alone. That's a defensible position; discovering it in review is
not.

**The two provenance sources named above are feasibility evidence, not a
mandate.** They are cited to show the chosen option is cheap, not to specify how
it must be built, and a DESIGN reviewer should not read them as binding. A third
route nobody here thought of is a fine outcome. What this decision requires is
that the property holds and that the DESIGN says which route it took and why.

### D7. The event set must be extensible by one entry

**Decided:** R12.

A single `apply` event is what forces anything expensive added to worktree
provisioning to run once per live worktree per apply. R2 accepts that cost for
setup scripts on the evidence above, but the constraint should not be baked into
the validation mechanism: if the DESIGN concludes a `create` event is warranted,
adding it must not require undoing R10's implementation. A validation expressed
as an equality check against the existing single-event constant would have to be
rewritten by the first change that adds an event.

## Out of Scope

- Any change to how setup scripts run against a clone, beyond what R15's
  regression guard requires. The runner never touches git and is already
  parameterized on the directory it runs in.
- The shell wrapper's directory handoff after `niwa worktree create`, which is
  broken for an unrelated reason and separately owned. This PRD assumes the
  handoff works, and does not lean on its being broken.
- Codex directory trust, the other cell missing from the clone/worktree parity
  table.
- A general per-event hook vocabulary for niwa as a whole. R10 and R12 settle
  what a *worktree* lifecycle event is; the instance-level hook surface is
  untouched.
- Pulling forward the deferred `setup_policy = "warn" | "fail"` key.
- **Fixing the pre-existing case where a failing worktree *hook* destroys a
  delegated worktree.** That defect is live today with no setup scripts anywhere
  near it: niwa git-excludes its own writes before the hook step runs, precisely
  so a finished worktree reads clean enough to reclaim, and the same property
  lets a failed one be deleted. Any worktree-hook failure hits it now. R6 and
  R11 bind what *this* feature adds and do not claim to repair that, because a
  scoped change stops being reviewable the moment it also becomes the place a
  pre-existing data-loss bug is fixed. It is tracked as #285, and this PRD does
  not depend on #285 being resolved first — D2's dependency direction is what
  keeps this feature clear of it.
- Persisting a record that setup ran, per repo or per worktree. R16 bounds the
  work per invocation; it does not introduce state.

## Known Limitations

- **The idempotency contract is being published and then relied on, in the same
  change.** No repo author has previously been told the requirement exists, so
  every setup script in existence was written without it in view. R3's
  default-off is what makes that safe: a repo opts in deliberately, after the
  contract is readable.

- **Meeting R9 closes a gap that is already open, and the DESIGN should say so.**
  The worktree hook runner streams its scripts' raw output with no redactor at
  all, on `worktree create` and `worktree apply`, in a tree that holds the
  clone's copied plaintext env file. So Decision C's premise — that setup output
  would otherwise be the only unscrubbed subprocess output in niwa — is already
  false, at the exact place this feature inserts. D6 chooses to fix that rather
  than add a second instance of it, which means the honest description of R9's
  outcome is "Decision C holds here for the first time", not "Decision C is
  preserved".

- **Worktree setup provisions against the clone's state as of the previous
  apply**, unless the pipeline's step order changes. The worktree fan-out
  currently runs before the clone's own setup-script step, so a worktree step
  that consumes something clone setup produces would consume the last run's
  output. This is a pre-existing ordering rather than a regression, and the
  DESIGN owns whether to reorder, forbid the dependency, or accept it.

- **A clone whose setup partly failed still provisions its worktrees.** The
  clone-path runner stops a repo's remaining scripts at the first failure, and
  nothing suppresses the worktree run. Whether it should is left to the DESIGN.
