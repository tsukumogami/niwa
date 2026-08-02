```yaml
topic: dispatch-paste-prompt
chain_started: 2026-08-01T22:41:35Z
last_updated: 2026-08-01T22:55:00Z
phase_pointer: phase-2
exit: UNSET
exit_artifacts: []
visibility: Public
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
chain_ran:
  - name: brief
    outcome: amended-in-place
    jury_rounds: 2
    note: |
      Round-two blocking finding was a sequencing artifact -- the reviewer
      observed that the BRIEF claimed the PRD and DESIGN had been re-opened
      while both were still untouched on disk. The claim becomes true when
      the chain lands as one commit. Round-two non-blocking findings on
      frontmatter length, tense drift, and navigational scaffolding were
      taken.
  - name: prd
    outcome: amended-in-place
    jury_rounds: 2
    status_transition: Done -> In Progress
    note: |
      Round one returned FAIL from all three reviewers with substantive
      findings. The load-bearing ones: the keep-alive prepend had no
      defined home once the prompt spills (now R58); "recognizable from
      outside" was bound to no niwa-owned surface, since the session
      mapping stores no prompt text (goal rescoped to the worker's
      opening instruction); `niwa watch`'s continuation path launches
      repeatedly into an instance it did not create, so spill filenames
      must be unique (now R59); and a sibling artifact at terminal
      status, PRD-instance-dispatch R43, mandates the exact refusal this
      PRD's R43 forbids (now D2 and R56).

      One reviewer proposal was rejected on the evidence: deleting the
      spill file after exec returns would race the worker's read, since
      `claude --bg` returns before the worker has read anything.
      Instance reclamation is the disposal mechanism instead (R53).
parent_orchestration:
  parent: scope
  topic: dispatch-paste-prompt
  child: design
  invoked_at: 2026-08-02T00:15:00Z
child_snapshots:
  brief:
    status: Done
    content_hash: 27b6ab998e9aa3fac5fd2253a4d8b1d975fb23ec
    captured_at: 2026-08-01T22:55:00Z
  prd:
    status: Done
    content_hash: fa17ba9612263d407093cc6dd49c31aa9c1bbe38
    captured_at: 2026-08-01T22:55:00Z
  design:
    status: Current
    content_hash: 83210448abf526e58fca48d4b447af9c580e6f4e
    captured_at: 2026-08-01T22:55:00Z
```

## Phase 1 verdicts

Framing-shift signal: **positive**. The author is reversing two settled PRD
positions -- the "commits to failure-shaped pastes" decision and the Out-of-Scope
line excluding any transport change that would raise the ceiling. The BRIEF's
In-list bullet promising "a ceiling the developer learns about while their input
is still recoverable", its "Running past the size ceiling" journey, and its
Out-list exclusion of "solutions that require the developer to create a file
first" are all invalidated by a design in which niwa (not the developer) creates
the file.

- `/brief` -- fires (R4 EITHER-signal: framing shift positive, overriding the
  Done BRIEF at the canonical path).
- `/prd` -- fires (R5 Mandatory-with-auto-skip: the gate would auto-skip on an
  untouched Accepted PRD, but the framing shift re-opens the settled-upstream
  boundary and the author selected a Revise at that boundary).
- `/design` -- fires (R7 shape-dependent).
  - P1 architectural-alternatives: **fires**. Whether the spill happens in
    `runDispatch` or in `realDispatchLaunch` (which decides whether `niwa watch`
    inherits it), what now bounds the capture buffer once the user-facing
    ceiling is gone, and whether the pre-exec backstop check survives are all
    left open.
  - P2 new-component references: **does-not-fire**. The change lands in the
    existing `internal/cli` and `internal/promptcapture` packages; no new
    binary, service, or substrate.
  - P3 Complex classification: **fires**. The change touches the exec-ceiling
    invariant, the create-then-rollback window, and the shared launcher used by
    `niwa watch`, and it supersedes a DESIGN currently at status Current.
- `/plan` -- fires (ALWAYS).

## Author decisions (Phase 1 confirmation)

1. Amend the full chain: BRIEF + PRD + DESIGN, then produce a PLAN.
2. Spill only when the prompt will not fit as a single argv element -- the
   existing derived ceiling stays as the *spill trigger* rather than as a
   *refusal threshold*.
3. The spilled file lives inside the created instance, so the existing
   rollback-on-failure and `niwa reap` lifecycle owns its cleanup.
4. The argv prompt carries a pointer to the file plus a leading excerpt of the
   text, so Agent View rows and `niwa list` stay recognizable.

## Notes

Amendment run. The chain re-opens a settled topic: BRIEF (Done), PRD (Done),
and DESIGN (Current) all exist on main for `dispatch-paste-prompt`. The author
asked to amend those artifacts in place rather than open a new topic.

Terminology note for downstream children: PR #2 named in the invocation is a
tsuku recipe fix. The multiline capture landed in niwa #224; the size cap it
enforces came from #226 against issue #225.
