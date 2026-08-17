# Verdict: FAIL

Reviewer: clarity. Question: could two competent engineers read this and build
different things? For two requirements, yes. The rest of the document is
unusually disciplined about staying at outcome altitude.

## Ambiguity findings

**R10 vs its acceptance criterion — the blocking finding.** R10 reads:

> An interactive Codex session SHALL start in a prepared instance without
> prompting the developer for trust, review, or approval of anything niwa
> materialized.

The qualifier "of anything niwa materialized" narrows the requirement: it
forbids prompts *caused by niwa's files*, and by omission permits prompts Codex
raises on its own (for example, a generic first-visit directory-trust prompt
that would fire in that directory whether or not niwa had ever touched it).
The matching acceptance criterion asserts the broader claim:

> An interactive Codex session starts in a prepared repository ... without any
> trust or review prompt (R10).

Two readings, two builds. Engineer A takes R10 literally: a build where
Codex's own directory-trust prompt still fires is compliant, because that
prompt is not "of anything niwa materialized." Engineer B takes the AC: no
prompt of any kind, which may require niwa to pre-trust the directory. The two
builds differ observably, and Engineer A's passes the requirement while
failing the AC. The exploration (per motivating_context) verified the
no-prompt-at-all outcome, so the AC appears to carry the real intent; R10's
qualifier undersells it.

This ambiguity also has a consequence for R13: if the strong (AC) reading ever
requires recording directory trust in the developer's Codex configuration,
that collides with R13's "SHALL NOT modify the developer's Codex installation
or its configuration defaults." Whichever way R10 is resolved, the resolution
should make clear it is achievable without the act R13 forbids.

**R4 vs R7 — what may be truncated.** R4:

> A Codex session ... SHALL see the workspace context for where it's standing,
> composed from the same layers niwa materializes for Claude: instance, group,
> repository ...

R7:

> The context for the repository or worktree the session is standing in SHALL
> reach the session in full. Context delivered from an outer layer (instance
> or group) SHALL NOT crowd out, truncate, or displace the innermost layer's
> content.

R7 explicitly protects only the innermost layer. Read together with R4, two
positions are defensible: (a) all layers must arrive whole, and R7 merely
emphasizes the innermost because it is the most at risk; (b) only the
innermost layer is guaranteed whole, and a delivery mechanism that truncates
group-level content under size pressure is compliant. The AC for R7 tests
only a repository-level marker, which supports reading (b) without saying so.
One sentence stating whether outer and middle layers may ever arrive
truncated would remove the fork.

**Minor, non-blocking.** The R7 criterion's "even when the instance- and
group-level context is large" leaves "large" undefined; whether that is
testable is the testability reviewer's call, but the vagueness originates
here. Also, R12 forbids overwriting a repository-shipped file but does not
say what happens on collision (skip silently, warn, fail) — for the context
file R6 settles it, but for any other colliding path the behavior is open;
that is closer to a completeness gap, noted here only because it stems from
the requirement's wording.

Requirements R1, R2, R3, R5, R6, R8, R9, R11, R13, R14 admit one reasonable
reading each. "Byte-for-byte identical to today" (R2), "no environment
preparation, wrapper command, or shell integration" (R5), and "not untracked,
not modified, not staged" (R11) are precise where imprecision would have been
easy.

## Altitude check

Clean. No requirement names a filename, config key, directory layout, symlink,
hash, or byte limit. R11's `git status` phrasing is an observable outcome, not
a mechanism. The frontmatter and Status section explicitly assign "where the
Codex-readable context and skills live on disk and how they are kept in sync"
to the DESIGN. The sandboxed-home/sentinel-files detail in R13's criterion is
test method inside an AC, which is where such detail belongs. Every
requirement survives the test "still correct if the DESIGN chose a different
mechanism."

## Problem Statement standalone check

Stands alone. A cold reader learns what niwa does (prepare layered context
plus skills), what the prior increment built (an exclusive switch), the two
concrete costs (Codex misses repository and worktree layers; the choice is
front-loaded to creation time), and who is affected. No sentence depends on
the BRIEF to parse.

## Restatement check

This is where the document is weakest, though none of it blocks on its own:

- **Goals section is a near-verbatim copy of the BRIEF's User Outcome.** The
  first bullet reproduces "A prepared instance serves both agents, always.
  There's no agent selection at creation time and nothing to configure per
  instance ... switching agents is closing one and typing the other command in
  the same directory" word for word; the second reproduces "For Claude Code,
  nothing changes ... exactly the context and skills they get today." Copied
  prose drifts; the Goals could state what the PRD adds and cite the BRIEF for
  the rest.
- **Out of Scope claims summary but copies.** The section opens "the reasons
  are summarized here and detailed there," yet the cross-repo context gap
  bullet is character-for-character identical to the BRIEF's, and the
  ephemeral-sessions and dispatch bullets are nearly so. Either genuinely
  summarize (one line plus the citation) or drop the claim of summarizing.
- The User Stories handle it correctly: one-line stories with an explicit
  pointer to the BRIEF for full narratives. The Decisions and Trade-offs
  section overlaps the BRIEF's hook-injection wording but adds the
  rejected-alternative framing, which is legitimately the PRD's own content.

## Internal consistency

One contradiction, already detailed above: R10's acceptance criterion asserts
"without any trust or review prompt" while R10 itself forbids only prompts
"of anything niwa materialized" — the AC is strictly stronger than the
requirement it cites. Related tension between the strong reading of that AC
and R13, noted above. No other pair of requirements conflicts; the Out of
Scope list excludes nothing a requirement quietly requires (hook injection,
credentials, and dispatch are excluded and no requirement depends on them).

Writing quality: no banned words ("tier", "robust", "leverage",
"comprehensive", "holistic", "facilitate" all absent), no emojis, no AI
attribution or co-author lines, prose is direct.

## Required changes

1. Reconcile R10 with its acceptance criterion. Decide which claim is the
   requirement — "no prompt attributable to anything niwa materialized" or
   "no trust/review prompt at all" — and make both say it. The exploration
   evidence suggests the stronger form is the intent; if so, drop the
   qualifier from R10 (and confirm the stronger form doesn't force the act
   R13 forbids).
2. Add one sentence to R7 (or R4) stating whether instance- and group-level
   context may ever arrive truncated, or whether all layers must reach the
   session whole and R7 merely names the highest-risk case.

## Optional improvements

- Rewrite the Goals section to cite the BRIEF's User Outcome instead of
  reproducing it, keeping only what the PRD adds.
- Make the Out of Scope entries actual summaries (one line plus citation), or
  remove "summarized here" from the section's opening sentence.
- In the R7 criterion, replace "is large" with a concrete size or a phrase
  the testability reviewer can pin down.
- State collision behavior for R12: when a repository ships a file at a path
  niwa would otherwise write, does niwa skip, warn, or fail?

# Round 2

## Verdict: PASS

## Required change 1: R10/R13 reconciliation

Landed, and it is a genuine reconciliation, not paper. R10 now carries the
strong claim — no trust, review, or approval prompt at all, with the
sufficiency stated explicitly ("the preparation is sufficient that Codex
raises none of its own for the prepared directories") — and the carve-out is
bounded twice over: it names only prompts belonging to the developer's own
setup (first-run login) and anchors that boundary to R13. A trust prompt
about a prepared directory cannot slip through the carve-out, because the
sufficiency clause covers prepared directories by name. The AC now asserts
exactly what the requirement asserts.

The R13 collision is resolved rather than hidden: the new sentence ("Scoped,
additive entries whose effect is confined to paths inside niwa-managed
instances are consistent with this requirement; anything global, destructive,
or touching the developer's own settings is not") admits precisely the act
the strong R10 needs. Does it license more than the design needs? It is
broader than "trust entries" — it would admit any additive per-project entry
— but pinning it to trust specifically would be the altitude violation, and
the license is bounded on every axis that matters: additive only, path-scoped
to instances only, pre-existing settings untouchable, nothing global. The
R13 criterion (config differs only by per-project entries keyed inside
instances, no pre-existing key removed, reordered, or altered, no global key)
closes the loop. The broadest thing the sentence licenses is per-project
behavior inside niwa instances, which is niwa's remit anyway. Acceptable.

## Required change 2: R7

Landed and unambiguous. "Every context layer present for a location SHALL
reach the session whole; no layer may arrive truncated" settles the
outer-layer question in one sentence; the innermost-layer language survives
only as an explanation of risk, subordinate to the universal claim. The R4/R7
fork from round one is gone.

## Altitude re-check on the rewritten criteria

The specific ruling requested — the 32768 threshold: **acceptable**, for two
separable reasons. The number itself is a fact about the environment being
guarded against — the external tool's documented default budget — exactly
parallel to `git status` being a fact about git. A criterion for "no layer
truncated" needs a test input past the truncation point, and the documented
default is the only principled choice of that input. The subtler clause is
the offline check, "the context budget niwa's materialization declares
covers at least the byte size of the full composed chain" — that presumes
niwa declares a budget, which looks like a design choice leaking upward. It
survives the mechanism test on inspection: workspace content is user-authored
and unbounded, so no design can guarantee R7 by keeping chains under the
default, and R5 forbids the environment-variable escape hatch; raising the
declared budget is the only mechanism the environment offers. When the
environment forces a unique mechanism, an AC guarding its failure mode is
guarding an external contract, not choosing among designs. Same reasoning
covers the trust-entry criterion ("exactly one per-project trust entry ...
keyed by a path that resolves to that tree's actual root"): given Codex's
trust model and R5's no-env-prep rule, entries in the developer's config are
the only route to R9/R10, and the criterion pins the silent-failure modes
(accumulation across applies, miskeyed paths) the exploration flagged. The
remaining named paths in the R2 criterion (`CLAUDE.md` tree, `.claude/`, the
three test files) describe today's observable state — the invariant being
preserved — not new structure.

No new ambiguity in the rewritten text. The conventions paragraph defines
"sees/receives" once and precisely; "no `NIWA_*` variables and no
`CODEX_HOME` set", "mtime-unchanged", and "mode `000`" are exact where round
one's criteria were loose. Round one's undefined "large" is now the defined
threshold.

## Non-blocking notes from round one

Both handled without loss. Goals no longer reproduces the BRIEF's User
Outcome: it cites the BRIEF for the full picture and states four goals in
the PRD's own words ("a strict invariant, not a goal to approximate" is new
content, not compression residue). Out of Scope is one line per exclusion
with the reasoning explicitly delegated to the BRIEF and, where it exists,
to Decisions and Trade-offs; nothing meaning-bearing was lost in the
compression — the two exclusions with real argumentative weight (hooks, API
key) retain their full reasoning in Decisions and Trade-offs.

## Required changes

None.
