# Lead: What did the design record decide about setup scripts and about worktrees, and do they contradict?

Round 1. All citations are against `origin/main` = `d25ad4d` in the worktree
`/home/dgazineu/dev/niwaw/tsuku/tsuku+niwa_worktree_setup-b37e586e/.claude/worktrees/niwa-explore`.

## Findings

### 1. What `DESIGN-post-clone-scripts.md` actually decided

**The problem it set out to solve** (`docs/designs/current/DESIGN-post-clone-scripts.md:4-9`,
frontmatter `problem:`):

> "niwa clones repos during create/apply but can't run repo-provided setup scripts
> afterward. Some repos need git hooks installed, local config generated, or dev
> environments bootstrapped. Today this requires a separate manual step or an
> external installer. niwa should support running a repo's own setup scripts as
> part of the apply pipeline."

The framing is explicitly about **clones**, and the noun that recurs is *repo*. Line 41-49:

> "niwa's apply pipeline clones repos, installs content (CLAUDE.md hierarchy), and
> runs materializers... Today these setup steps happen outside niwa: either manually
> by the developer, or through a separate installer script. This breaks the 'one
> command to set up the workspace' promise."

Line 51-53 draws the one boundary the document actually draws, and it is a
*provenance* boundary, not a *target* boundary:

> "This is distinct from niwa's materializers, which distribute config FROM the
> workspace config repo TO target repos. Post-clone scripts run code that lives
> INSIDE the target repo."

**When scripts run: every apply, not create-only.** The decision drivers include
(line 61): "**Idempotent:** scripts must be safe to re-run on every `niwa apply`."
Consequences/Positive (line 439): "Idempotent execution means apply always
converges." Consequences/Negative (line 443): "Adding a script to the directory
silently changes behavior on next apply."

**Where the idempotency contract is written down: only in those three lines.**
It is a decision *driver* and a *consequence*, never a stated contract with the
repo author. There is no "scripts MUST be idempotent" requirement, no enforcement,
no guide page. There is no `docs/guides/` page for setup scripts at all — the only
user-facing mention of `scripts/setup/` in the whole repository outside this design
is incidental (`DESIGN-explicit-repos.md:82` listing `setup_dir` as a config key).
`README.md` mentions neither setup scripts nor worktrees.

**Failure posture and rationale** (line 137, "Chosen: Run from repo root, warn on
failure, stop on first script error"; lines 162-164):

> "Exit code non-zero: warning printed, **remaining scripts for that repo are
> skipped**, pipeline continues with next repo, and the repo is counted in the
> verdict line below the apply summary"

Rationale (line 181-184): "If `01-git-hooks.sh` fails, running `02-install-deps.sh`
may not make sense... But one repo's failure shouldn't block other repos from
setting up. This gives fail-fast within a repo and resilience across repos."

The exit code stays 0, and the 2026-08-08 amendment (Decision B, line 539) makes
that an explicit decision rather than an omission: "`create`'s exit code already
carries a meaning that is written down and relied upon: whether a usable instance
exists." A `setup_policy = "warn" | "fail"` key is specified and deliberately
deferred (line 560-570).

**The `setup_dir` opt-out and its rationale** (lines 87-105). Workspace-level
`[workspace] setup_dir` renames the directory; per-repo `[repos.X] setup_dir`
overrides it; `setup_dir = ""` disables. `*string` distinguishes "not set" from
"explicitly empty" (line 226-228). Rationale for the *generic* name (line 108-109):
"Generic name (`scripts/setup/`) doesn't imply niwa ownership -- repos can use this
convention with any tool or manually." The disable escape hatch is justified at
line 450: "Per-repo disable provides an escape hatch when auto-execution is
unwanted."

**The security/visibility posture.** `setup_visibility_test.go` is *not* about
security visibility — it is about **operator visibility of script output**
(regression test for issue #239). Its seven tests are `TestSetupScriptOutputIsDurable`,
`...PrefixAndAnnouncement`, `...StopsOnFirstErrorWithOutput`, `...ContinueToNextRepo`,
`...OutputIsScrubbed`, `...ScrubbedThroughInterleavedEscape`, `...FilenameIsSanitized`
(`internal/workspace/setup_visibility_test.go:31,66,90,127,166,196,227`).

The *security* rule is stated at line 310-317 and is a single sentence of trust
model:

> "Post-clone scripts run arbitrary code from the cloned repo. This is inherently
> trusted -- the user chose to clone the repo, and the scripts are part of the
> repo's codebase... **The security boundary is the same as `git clone` itself: if
> you clone a repo, you trust its contents.**"

Mitigations are narrow: only executable files run; no recursive descent; and a
2026-08-08 correction (line 325-333) retracts a claimed containment check —
"No containment check exists... `os.Stat` follows symlinks, so a symlinked entry
inside the setup directory resolves outside the repo," justified as sitting "inside
the trust boundary this design already draws." Secret exposure through printed
output is mitigated by unconditional redaction with four documented surviving leak
classes (lines 399-423).

**Does the doc ever state a scope boundary excluding worktrees?** No. The word
"worktree" appears zero times (confirmed). The only scope boundary is the
provenance one at line 51-53 (repo code vs. config-repo code), and that boundary
does not distinguish clone from worktree — a worktree contains the same repo code.
**Worktrees simply never came up**, and chronology (§4) explains why: they could not
have.

### 2. The design that owns decision B2 / requirements R6 and R7

It is `docs/designs/current/DESIGN-worktree-env-provisioning.md` (decision B2,
line 123-127) over `docs/prds/PRD-worktree-env-provisioning.md` (R6/R7, line 158-170).

**The inherit primitive** (`DESIGN-worktree-env-provisioning.md:210-214`, Decision
Outcome):

> "A single inherit primitive -- 'copy the clone's resolved env output files into a
> worktree's config-resolved targets, at 0600, git-excluded' -- is the one way a
> worktree's env is produced."

Mechanically it is a **byte copy of already-materialized output files**, with no
resolution and no network (Decision 1, A1, line 97-104), chosen over re-rendering
(A2) and over persisting resolved values in state (A3).

**Decision B2 verbatim** (line 123-127):

> "**Chosen: clone-then-inherit fan-out (B2).** Leave the clone materializer loop
> unchanged; add a step after it that enumerates live worktrees and runs the same
> inherit step (Decision 1) per worktree, sourcing from the just-written clone."

**R6 verbatim** (`PRD-worktree-env-provisioning.md:158-161`):

> "**R6.** `niwa apply` MUST refresh the environment of the instance's existing
> worktrees in the same run that refreshes the clones, using the shared
> materialization, so that after an apply no worktree holds a different value than
> its clone for any key."

**R7 verbatim** (line 163-166):

> "**R7.** While refreshing worktrees, `niwa apply` MUST tolerate worktrees that
> cannot be refreshed (locked, detached, or with a missing working directory): it
> skips them with a warning identifying the worktree and continues, rather than
> failing the apply."

**Is the guarantee scoped to env deliberately, or is env just what existed?**
Deliberately scoped to env, and the PRD says so in its Out of Scope (line 218-222):

> "**Non-environment worktree content** -- CLAUDE.md content, the workspace-context
> rules import, the worktree-specific layer, and worktree hooks. **Settled by the
> worktree command parity design.**"

So this design consciously deferred *all* non-env worktree content to the parity
design. It does not claim a general principle. Its N2 is env-specific: "A worktree's
environment MUST stay equivalent to its instance clone's across every operation that
touches it" (line 190-194). Its motivating problem was a *failure* (an Infisical 403
and an unassembled provider ref), not a completeness goal.

The nearest thing to a general statement is D3 (line 205-211): "**worktrees mirror
their instance; no per-worktree divergence (v1)**... the mirror is what makes
inheritance and the apply-refresh contract coherent" — again scoped to environment.

**The strongest general statement of what a worktree IS** is not in either of these
documents. It is `docs/designs/current/DESIGN-ephemeral-session-instances.md:133-136`:

> "It has no effect at an instance or a worktree: under the inherit model
> (tsukumogami/niwa#168) **a worktree is a derived view of its instance and refreshes
> together with it**, so it is not an independently skippable scope, and a worktree
> is a leaf with nothing below it."

"A derived view of its instance" is the closest the record comes to a doctrine — and
it argues *for* provisioning parity, not against it.

### 3. Survey of worktree design docs — what is a worktree FOR, how expensive, what does it contain

**`DESIGN-worktree-command-parity.md` (the load-bearing one).** Its Context section
enumerates the instance apply pipeline in full — and *names setup scripts in that
enumeration* (line 56-58):

> "`InstallRepoContent` (per-repo `CLAUDE.local.md`) → repo materializers (settings,
> env, files, hooks via `DiscoverHooks`/`DiscoverEnvFiles`) → **setup scripts** →
> state. So **instance create = scaffold + apply-pipeline; instance apply = the
> pipeline alone.** A repo checkout therefore emerges fully formed."

Then Decision C ("What content a worktree receives", line 168-186) lists four things —
`InstallRepoContent`, the repo materializers, `.claude/rules/worktree-imports.md`,
and the purpose/branch layer. **Setup scripts are absent from C1 and absent from both
rejected alternatives C2 and C3.** They are named in the pipeline enumeration and then
never mentioned again in the document. No rationale, no deferral, no out-of-scope line.

The upstream PRD's R1 does carry a narrowing word (`PRD-worktree-command-parity.md`,
R1): the worktree gets "the same class of **CLAUDE accessories** a repo checkout
receives from `niwa apply`". "CLAUDE accessories" is a real scope word and it excludes
dependency setup. But the BRIEF above it is broader
(`BRIEF-worktree-command-parity.md`, User Outcome): "**A developer who creates a
worktree gets a working context as complete as a repo checkout**." And the parity
design's own Consequences/Positive says: "A worktree is a first-class CLAUDE working
context **at parity with a repo checkout** ... no manual setup for agents launched
there."

**Nothing anywhere says worktrees are meant to be cheap.** I grepped
`cheap|lightweight|expensive|throwaway|disposable|fast to create` across
`DESIGN-worktree-command-parity.md`, `DESIGN-niwa-default-worktree.md`,
`DESIGN-worktree-env-provisioning.md`, `docs/guides/worktree.md`,
`PRD-niwa-default-worktree.md`, `BRIEF-niwa-default-worktree.md`. Two hits, both
irrelevant (a `--force` grace period in seconds; "the cheap follow-up" describing a
state-file field). The word "ephemeral" appears once about worktrees, describing the
*content layer* ("the ephemeral worktree's purpose-specific context",
`DESIGN-worktree-command-parity.md:196`), not creation cost. **There is no written
intent that worktrees stay cheap or minimal.** The written intent runs the other way.

**`DESIGN-niwa-default-worktree.md` + `SPIKE-niwa-default-worktree.md`.** The spike's
Context frames the whole feature as *avoiding* bare worktrees
(`docs/spikes/SPIKE-niwa-default-worktree.md`, Context):

> "**Claude Code's built-in worktree tool** ... creates a bare `git worktree` ...
> **with no secret materialization, no CLAUDE content sync, and no session
> tracking.** / **niwa's worktree lifecycle** ... materializes the full workspace
> context: vault-resolved secrets, repo CLAUDE content, workspace-context imports,
> git-exclude coverage, worktree hooks, and a session-lifecycle state file."

"Materializes the full workspace context" is another enumeration with setup scripts
absent. The design's Decision 9 ("worktree settings parity", line 313-346) is the
sharpest methodological precedent in the record. It found that a worktree's
`settings.local.json` lacked the delegation entries its clone had, called the gap
*latent* and not user-visible, and closed it anyway:

> "**Chosen: thread the delegation decision into the worktree apply path**, so a
> worktree's settings match its clone's. The gap is latent rather than user-visible
> today ... but the two configurations drifting is the kind of divergence R3's
> 'install at the scope required for it to take effect' exists to rule out."
>
> "*Alternative: leave it and document the asymmetry.* Rejected: **the asymmetry has
> no rationale behind it -- it is an omission, not a decision -- and documenting an
> omission costs more than closing it.**"

That last sentence is the record's own stated test for exactly the question this
exploration is asking.

**`docs/guides/worktree.md` — the user-facing promise.** Lines 13-32 enumerate
"What a worktree gives you" in seven numbered steps: branch, `git worktree add`,
repo CLAUDE content, `worktree-imports.md`, purpose/branch layer, worktree hooks,
lifecycle state file. Setup scripts are step zero of nothing. Step 3 reads:

> "Installs the owning repo's CLAUDE content into the worktree, **the same class of
> accessories `niwa apply` installs into a repo checkout** (CLAUDE.local.md,
> subdirectory content, settings, env, hooks)."

And line 32-33:

> "**A worktree and a repo checkout cannot drift, because there is a single
> materializer path behind both.**"

That sentence is technically defensible (it is scoped to the materializer path) and
practically misleading: a worktree and its clone *do* differ, in exactly the way a
developer notices first — the clone has a built dependency tree and the worktree does
not. The guide's opening definition (line 3-6) is: "an isolated git checkout of one
repo in your workspace, on its own branch, **with the repo's CLAUDE content installed
into it**" — CLAUDE content, precisely.

`DESIGN-instance-lifecycle.md` and `DESIGN-shell-navigation-protocol.md` contain zero
worktree mentions.

### 4. Chronology — setup scripts predate first-class worktrees by 68 days

| What | Commit | Date |
|---|---|---|
| Post-clone setup scripts land | `17a433d` (#14) | 2026-03-30 |
| Setup scripts gain reporter/spinner routing | `2de1b56` (#68) | 2026-04-19 |
| Mesh removal + symmetric `niwa worktree` + `ApplyToWorktree` + parity design | `69b735f` (#151) | 2026-06-06 |
| `niwa` as default worktree mechanism | `7f3d2f7` (#167) | 2026-06-20 |
| Worktree env inherit + `refreshWorktreeEnvs` + B2 fan-out | `a13d462` (#168) | 2026-06-22 |
| Setup-script output/verdict amendment | `3403ee4` (#240) | 2026-08-08 |

`git log -S"RunSetupScripts"` → `17a433d` (2026-03-30), `2de1b56`, `3403ee4`.
`git log -S"refreshWorktreeEnvs"` → `a13d462` (2026-06-22), `cb16c4c`.
`git log -S"inheritEnvOutputs"` → `a13d462`, `11b5d5d`.
`git log -S"ApplyToWorktree"` → oldest `69b735f` (2026-06-06).

**This is decisive for "never reconciled."** When `DESIGN-post-clone-scripts.md` was
written, `ApplyToWorktree` did not exist and `niwa worktree create` did nothing but
`git worktree add` + a bare state scaffold. The design's silence on worktrees is not
a scope decision — there was nothing to have a scope decision about. Worktree content
installation was invented 68 days later, by a different design, which enumerated
setup scripts as part of the pipeline it was mirroring and then did not carry them
over.

The 2026-08-08 amendment (`3403ee4`) is the counter-evidence worth stating: setup
scripts were reopened and substantially re-litigated *two months after* worktree
content install existed, and worktrees still did not come up. That amendment was
tightly scoped to output routing, verdict placement, and redaction, but it did
re-examine the failure posture and specify a deferred `setup_policy` — and it never
asked where else setup runs.

### 5. Prior art — has anyone raised this?

No. Evidence:

- `git log --all --format=...` filtered for commit messages/bodies containing both
  "worktree" and "setup script": **zero matches.**
- Grep for `setup script|setup_dir|scripts/setup` across `docs/` and `README.md`
  returns 15 hits outside the post-clone design. Every one is either the
  plugin-installation design ordering itself after setup scripts, the clone-output-UX
  design describing the output helper, `DESIGN-explicit-repos.md` listing `setup_dir`
  as a per-repo key, or the two worktree-hook mentions covered in §6.
- There is no `docs/decisions/` directory.
- The nearest thing to prior contemplation is `DESIGN-agent-capability-contract.md:671`,
  which names "**setup scripts' side effects**" as one of two "known blind spots" of
  the golden-manifest test — an acknowledgment that setup scripts produce filesystem
  state the manifest does not capture. That is the same category of state a worktree
  lacks, but it is raised about test coverage, not about worktrees.
- `e00de18 docs(explore): confirm worktrees need no dedicated mechanism` (2026-08-16)
  is unrelated — it is a `wip/` exploration artifact about the dual-agent `.git`-file
  mechanism.

**Do the user-facing docs promise something not delivered?** Partially, and the
sharpest instance is `docs/guides/worktree.md:32-33`: "A worktree and a repo checkout
cannot drift, because there is a single materializer path behind both." Read by a
developer rather than a maintainer, that is a completeness claim. Conversely, setup
scripts have **no user-facing documentation whatsoever** — no guide page, no README
mention — so there is no place where niwa promises a worktree gets them, and no place
it says it does not. The asymmetry is undocumented in both directions.

### 6. The counterargument — "env is declarative DATA, setup scripts are EXECUTION"

**The distinction does not hold, and the record breaks it in three places.**

**(a) niwa already executes scripts in a worktree, on create and on every worktree
apply.** `internal/workspace/worktree_content.go:707-713`:

```
// 5. Worktree-event hooks, run on create/apply. Analog of the instance
//    setup-script run: discovered from <configDir>/worktree-hooks/ and
//    executed against the worktree, with worktree context in the env.
//    These are the workspace's own lifecycle scripts, not an agent's, so
//    no agent's gate decides whether they run.
if err := runWorktreeHooks(configDir, worktreePath, repo, purpose, branch, opts.Stderr); err != nil {
```

`runWorktreeHooks` (line 1061-1110) runs each script with `cmd.Dir = worktreePath`,
in lexical order, exporting `NIWA_WORKTREE_PATH/REPO/PURPOSE/BRANCH`. Its doc comment
(line 1049-1060) says the scripts have the "**same provenance as DiscoverHooks /
setup scripts**", that non-executable files are warned-and-skipped "Match[ing] the
setup-script policy", and that "the first non-zero exit stops the run ... (mirroring
the setup-script contract)". The user guide repeats it
(`docs/guides/worktree.md:295-297`): "They are the **worktree analog of instance setup
scripts** and come from the config repo you already trust."

So "we don't execute arbitrary side-effecting scripts in a worktree" is already false.
The line that is actually drawn is **provenance**: worktree-executed scripts come from
`<configDir>/worktree-hooks/` (the workspace config repo), never from the repo being
worktree'd. That is the same provenance boundary
`DESIGN-post-clone-scripts.md:51-53` drew — and setup scripts sit on the *other* side
of it, as repo-provided code.

**(b) That provenance boundary was never argued as a worktree boundary — it was
substituted in a code comment.** The parity design's Decision D1 introduced the
worktree hook surface as "a worktree-events hook directory (**analog of
`DiscoverHooks`**)" — i.e. the analog of *config-repo hook discovery*. The code
comment and the guide independently re-describe the same surface as "the analog of the
instance **setup-script** run". `git log -S"Analog of the instance"` and
`git log -S"same provenance as DiscoverHooks / setup scripts"` both return exactly
`69b735f` — the parity commit itself. The author was thinking about setup scripts
while writing the worktree content path, called the new hook surface their analog in
code, and did not record that substitution as a decision in the design. The design
says "analog of DiscoverHooks"; the code says "analog of setup scripts"; those are
different claims and only the weaker one is in the design record.

**(c) The failure postures are inverted, which shows they were never reconciled.**
Setup scripts: warn, skip the rest of the repo, continue, exit 0
(`DESIGN-post-clone-scripts.md:162-164`, `apply.go:1953-1976`). Worktree hooks: the
first non-zero exit is `return`ed as an error that fails the worktree apply
(`worktree_content.go:1105-1107`, `712-714`). A surface that claims to mirror the
setup-script contract inverts its central decision. That is what an unreconciled
parallel implementation looks like, not a considered boundary.

**Two things that genuinely do favor the counterargument**, stated fairly:

- The env fan-out is per-repo idempotent byte-copy, linear and local-I/O-only, and
  the design flagged even that as a cost: "`niwa apply` does more work (linear in live
  worktrees, local I/O only). Fine for the expected handful"
  (`DESIGN-worktree-env-provisioning.md`, Negative/trade-offs). Running `npm install`
  in every worktree on every apply is a materially different cost curve, and the
  post-clone design's own idempotency contract is a driver, not an enforced property.
- `apply.go` orders **Step 6.6 (worktree env fan-out) before Step 6.75 (setup
  scripts)** — lines 1921 and 1953. So in a single apply, worktrees are refreshed
  from clone env *before* the clone's own setup scripts run. Any worktree-side setup
  step would need to be a third step after 6.75, not a widening of 6.6.

## Implications

The two records do not contradict each other in the sense of asserting opposite
things. They contradict each other in the sense that **the completeness principle the
worktree record states cannot be satisfied under the scope the setup-script record
was written for, and neither document notices.** The worktree lineage says a worktree
is "at parity with a repo checkout", "as complete as a repo checkout", and "a derived
view of its instance"; the setup-script lineage defines the one pipeline step whose
output a derived view cannot inherit, because its output is not a file niwa manages —
it is arbitrary filesystem state.

The gap is an **omission, not a decision**, by the record's own test. The parity
design listed setup scripts in the pipeline it was mirroring and dropped them without
a word; the env design explicitly deferred all non-env content to the parity design;
the post-clone design could not have considered worktrees because they did not exist.
`DESIGN-niwa-default-worktree.md` Decision 9 already ruled on this exact species of
finding — "the asymmetry has no rationale behind it -- it is an omission, not a
decision -- and documenting an omission costs more than closing it" — for a gap that
was *latent and not user-visible*, which this one is not.

The real boundary in the record is provenance (config-repo code runs in worktrees;
repo-provided code does not), and it has never been argued on the merits. It is
findable only by reading a code comment and a guide sentence that describe the
worktree hook surface as the setup-script analog while the design that created it
describes it as the `DiscoverHooks` analog.

## Surprises

1. **The parity design names setup scripts in its pipeline enumeration** (line 56-58)
   and then omits them from Decision C's content list *and* from both rejected
   alternatives. This is not "worktrees never came up" — it is the reverse: setup
   scripts came up and fell out silently.
2. **`setup_visibility_test.go` is about operator visibility, not security.** The
   security posture is one paragraph of trust model plus a 2026-08-08 correction
   retracting a claimed containment check.
3. **Worktree hooks fail the apply on non-zero exit while setup scripts warn and
   continue** — an inverted failure posture in a surface whose own doc comment says it
   mirrors the setup-script contract.
4. **`git log -S` puts both "analog of the instance setup-script run" and "same
   provenance as DiscoverHooks / setup scripts" in `69b735f`**, the same commit as the
   parity design that omitted them. The substitution happened in code comments,
   simultaneously with the design that failed to record it.
5. **Setup scripts have no user-facing documentation at all** — no guide, no README
   line. Worktrees have a 564-line guide. There is no place niwa tells a user what a
   worktree does or does not get from `scripts/setup/`.
6. **Nothing in the record says worktrees should be cheap.** I looked for it directly.
   The intent runs the other way: the default-worktree feature exists specifically to
   stop agents producing "bare" worktrees.
7. **Step 6.6 runs before Step 6.75**, so today's worktree refresh reads clone env
   written before the clone's own setup scripts execute in that same apply.

## Open Questions

- Is there an idempotency floor a repo author can actually rely on? The contract is
  three lines of driver/consequence prose with no guide, no enforcement, and no
  statement to repo authors. Running setup in N worktrees multiplies whatever
  non-idempotency exists by N.
- Should worktree provisioning be a fourth pipeline step (after 6.75, sourcing from a
  clone whose setup has just run) or an extension of `ApplyToWorktree` (which would
  make it run on `worktree create` too, where no clone setup just ran)? The two give
  different answers on freshness and on `niwa worktree create`'s offline guarantee.
- Does the `setup_policy = "warn" | "fail"` key deferred at
  `DESIGN-post-clone-scripts.md:560-570` need a worktree dimension, or does the
  worktree-hook posture (fail) already establish the answer by precedent?
- The env design's D3 ("worktrees mirror their instance; no per-worktree divergence")
  is stated for env. Does it generalize? A repo whose setup script writes
  branch-specific or path-specific state would break the mirror in a way byte-copy
  never can.
- `niwa worktree create` currently makes a hard promise of no network and no secret
  source (`docs/guides/worktree.md:44-49`). Running `npm install` in a fresh worktree
  breaks that promise. Is the answer to scope worktree setup to `niwa apply` only, or
  to explicitly retire the offline guarantee for the setup step?

## Summary

The post-clone-scripts design never excluded worktrees — it could not have, because it
shipped on 2026-03-30 (`17a433d`), 68 days before `ApplyToWorktree` existed
(`69b735f`, 2026-06-06), and the only boundary it draws is provenance ("scripts run
code that lives INSIDE the target repo"), not target. The worktree record, by
contrast, repeatedly asserts completeness — "at parity with a repo checkout", "as
complete as a repo checkout", "a derived view of its instance" — and nowhere in any
worktree design, PRD, brief, spike, or guide is there a statement that worktrees are
meant to be cheap or minimal; the parity design even enumerates setup scripts as part
of the pipeline it is mirroring (line 56-58) and then drops them from Decision C with
no rationale and no rejected alternative. The "we don't execute repo code in
worktrees" defense is already breached in spirit and papered over in fact: niwa runs
`worktree-hooks/` scripts against every worktree on create and apply, the code and
guide call that surface "the analog of the instance setup-script run" while the design
that created it calls it the analog of `DiscoverHooks`, and the two surfaces have
opposite failure postures — so this is an unreconciled omission, exactly the kind
`DESIGN-niwa-default-worktree.md` Decision 9 already ruled should be closed rather
than documented.
