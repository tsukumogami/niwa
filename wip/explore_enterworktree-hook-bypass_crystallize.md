# Crystallize: enterworktree-hook-bypass (niwa#221)

## Recommended: Design Doc — a targeted revision of `DESIGN-niwa-default-worktree.md`

Scored highest (6 signals, 0 anti-signals). Matching signals, tied to findings:

- **Technical decisions need to be made between approaches.** Making the hook
  command survive a niwa upgrade has at least three viable shapes: PATH-first
  resolution, keeping the absolute path plus staleness detection at apply or
  hook time, or a guarded hybrid that prefers PATH and falls back to the
  absolute path. They differ in failure mode, not just style (F3, F7).
- **Exploration surfaced multiple viable implementation paths.** Same as above,
  plus a second open choice: whether a failed `from-hook` rolls back the git
  worktree and session record, or marks the session failed and retains it for
  inspection (F4).
- **Decisions made during exploration that belong on the record.** The shipped
  design's Decision 4 rests on a spike finding this exploration falsified, and
  its Decision 6 fixes the hook command shape that F3 shows is not durable.
  Both need correcting where they live, not only in an issue thread.
- **The core question is "how should we build this?"** What to build is settled
  by the accepted PRD; nothing about R1/R5/R7/R8 changed.

## Scope of the revision

Deliberately narrow. It revises Decision 6 (hook command provenance and
durability), corrects Decision 4's rationale to match the reproduction
evidence, and adds failure-atomicity to the create path. It does **not**
reopen the delegation model — the harness never regressed, so the spike's
layered "decide whether transparent delegation is still achievable" question
is moot.

## Alternatives

- **Decision Record** — matched on "future contributors need to understand why"
  and "alternatives compared with trade-offs", but demoted: there are two
  interrelated decisions (command durability and failure atomicity) plus
  corrections to an existing design, which is design-doc shaped rather than a
  single ADR.
- **Plan** — the upstream design exists, which is a real signal, but demoted:
  the durability approach is not settled, and a plan cannot sequence a decision
  that hasn't been made.
- **Spike Report** — the investigation was focused and produced concrete tested
  findings, but the spike for this topic already exists and is merged; the work
  here is correcting it, not writing a new one.
- **No Artifact** — demoted: architectural decisions were made and the shipped
  design carries a falsified premise that others will read.

## Decisions carried into the next artifact

- #221 is retargeted to the reproducible defects (version-pinned hook command,
  partial-failure orphans). The non-monotonic detection work it originally
  asked for is dropped as unnecessary — the harness never regressed.
- The merged spike is corrected in this same work rather than left standing.
