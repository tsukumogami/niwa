# Crystallize: worktree-setup-scripts

## Candidacy

- **`/execute`** — not a candidate. `docs/plans/` does not exist in this repo; no
  qualifying PLAN.
- **Competitive analysis** — not a candidate. `## Visibility` in the scope file is
  `Public`.

## Stage 1: What the exploration is

### Rejection Record — score -2, demoted

No signals. The exploration reached the opposite conclusion: the gap should be
closed. Anti-signals present ("rejection reasoning already documented", "no positive
rejection evidence"). Off the board.

### Spike Report — score +1, demoted (2 anti-signals)

Signals present: technical uncertainty blocked a decision (does running a repo's
setup scripts in a worktree even work?); a bounded investigation produced concrete
findings; a specific technical risk was identified *and empirically tested* (the
`cd ../..` instance-root arithmetic breaking at worktree depth).

Anti-signals present, and they are decisive:
- "The question is 'should we do this?' or 'what should we build?'" — that is
  literally the brief's question.
- "Exploration was broad, not focused on a specific technical risk" — four leads
  spanning design record, hook points, cost, and precedent.

### Decision Record — score +1, demoted (1 anti-signal)

Signals: alternatives were compared with trade-offs; future contributors need the
reasoning on record.

Anti-signal, decisive: "Multiple interrelated decisions came with work attached."
The exploration produced at least six coupled decisions — hook point, create-vs-apply
event, opt-in vs opt-out, failure posture, the script-visible environment contract,
and whether to publish the idempotency contract. That is a design's worth of
decisions, not one.

### A Chain — score +5, no anti-signals — **top**

Signals present:
- Converged on something someone will build.
- Architecture and requirements questions remain open (opt-in default, which
  surfaces run it, event split, failure posture).
- Decisions made during exploration need a durable home *and* downstream work.
- A scope boundary emerged rather than just an answer: repo-authored vs
  config-authored provenance, and instance-root vs per-tree artifacts.
- The core question resolved to "what do we build, and how?".

Anti-signals: none. Nothing was left un-built; the output is not one choice between
named options; the conclusion is not "don't do it"; findings are internal, not about
external products.

**Stage 1 result: A Chain.** No tiebreaker needed — it is the only category with
zero anti-signals, and the demotion rule puts it above both +1 categories anyway.

## Stage 2: Where the chain starts

### File an Issue — score -1, demoted (2 anti-signals)

One signal (single round, one implementer plausible). But: "Others need
documentation to build from" and, decisively, "Any architectural, dependency, or
structural decisions were made during exploration" — six of them. Filing an issue
and walking away would strand every one of those in `wip/`, which is deleted before
merge.

### `/charter` — score -2, demoted

The project exists; this is one bounded feature. Anti-signals: "the project already
exists and the question is about its next feature", "the work is one bounded
feature, however large", "no sequencing question".

### `/scope` — score +8, no anti-signals — **top**

Signals present:
- A single coherent feature emerged.
- Requirements are contested: opt-in vs opt-out default is genuinely undecided, and
  the two first-party data points want opposite answers.
- What to build is clear; how to build it is not.
- Technical decisions remain between named approaches (insertion inside
  `ApplyToWorktree` vs a new step after `apply.go:1959` vs extending
  `worktree-hooks/`).
- Architecture and integration questions remain (event split, Reporter/redactor
  threading, `from-hook` rollback interaction).
- Multiple viable implementation paths surfaced.
- Architectural decisions made during exploration need to be on record.
- The core question is "what should we build, and how?".

Anti-signals: none. It is not multiple independently-sequenced features; one person
cannot act on it without a written contract; no qualifying PLAN exists; the
exploration did produce work.

**Stage 2 result: `/scope`.**

## Recommendation

**A Chain, entering at `/scope`.**

The exploration answered the verdict question decisively — the gap is real, it is
undeclared by the standard of the design that created it, and niwa has already
closed a strictly weaker instance of the same divergence on principle. What it did
*not* answer, and deliberately so, is what the feature should do. Three findings
each independently rule out a straight patch:

1. The only first-party setup script in this workspace would be silently miscompiled
   into a dead path by the naive version.
2. `DESIGN-post-clone-scripts.md`'s own motivating example (git hooks, shared through
   `git-common-dir`) is the case that must *not* run per-worktree, so a per-repo
   boolean is insufficient on its own.
3. Cost is bimodal — 0.29s here, ~13s and 341 MB for the JS profile — so one default
   is wrong for half of any mixed workspace.

Requirements are contested, the approach has three named candidates, and six coupled
decisions need a durable home. That is the tactical chain's shape exactly.

## Also produced, outside the chain

Two things should be filed as issues regardless of when the chain runs:

1. **The gap itself**, as a tracking issue on `tsukumogami/niwa`, so the finding is
   durable once `wip/` is cleaned. It points at `/scope` as the entry point rather
   than proposing an implementation.
2. **`niwa worktree create` gets no shell auto-cd** — an independent bug found on the
   way (`internal/cli/shell_init.go` never matches `worktree`), contradicting
   `docs/guides/worktree.md`. Small, decision-free, one person: a genuine
   file-an-issue case.

A third, **Codex directory trust never reaches a worktree**, is the only other cell
missing from the clone/worktree parity table. It is out of scope here and belongs to
whoever owns directory trust; noted so it is not lost.

## Filed

- tsukumogami/niwa#280 — the gap itself, labelled `needs-design`, pointing at `/scope`.
- tsukumogami/niwa#281 — `niwa worktree create` never triggers the shell auto-cd.
- tsukumogami/niwa#282 — worktree hooks under any event but `apply` are silently never run.
