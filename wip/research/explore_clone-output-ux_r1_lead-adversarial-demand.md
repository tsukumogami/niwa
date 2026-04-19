# Demand Validation: clone/apply output UX (inline progress indicators)

**Topic**: Replacing niwa's linear stderr log dump during `create`/`apply`
with inline progress indicators (spinners, in-place status updates).

**Researcher role**: Adversarial — report only what durable artifacts confirm.
Verdict belongs to convergence and the user.

---

## Q1. Is demand real?

**Confidence: Low**

No GitHub issue, PR, or discussion in the tsukumogami/niwa repository
explicitly requests inline progress indicators, spinners, or in-place output
updates for `create` or `apply`. There is no issue filed by any reporter —
internal or external — requesting this feature.

The closest artifact is DESIGN-shell-navigation-protocol.md, which
acknowledges "future output modes" as a design constraint:

> "The contract must survive `--verbose`, `--debug`, `--json`,
> `NIWA_LOG_LEVEL`, and CI/quiet modes that may be added later."
> (DESIGN-shell-navigation-protocol.md, Decision Drivers section)

This is a forward-compatibility hedge, not a demand signal. The design doc
treats output modes as a class of future changes to plan around, not as a
stated roadmap item.

The vault-integration guide contains one deferred mention:

> "`*.optional` | Silent in v1; apply continues. Info-log output will land
> when a verbose flag is added."
> (docs/guides/vault-integration.md, line 170)

This defers a verbose flag to a future version as a nice-to-have for
optional-key misses, but it does not come from a user request and does not
reference inline progress indicators.

No external contributors have filed issues on this topic. All 23 issues in
the repository were filed by a single author (dangazineu).

---

## Q2. What do people do today instead?

**Confidence: Medium** (codebase-only; no user workarounds documented)

There are no documented workarounds. Users see a stream of one-line messages
on stderr, one per repo action:

```
cloned <repo> into <path>
pulled <repo> (N commits)
skipped <repo> (up to date)
skipped <repo> (dirty working tree)
warning: sync failed for <repo>: ...
```

These come from direct `fmt.Fprintf(os.Stderr, ...)` calls in
`internal/workspace/apply.go` (lines 709–727) with no buffering, no
abstraction, and no TTY awareness. Git subprocess output also flows to
stderr unfiltered (clone.go:61–62, sync.go:68–69, setup.go:105–106,
configsync.go:42–43).

No flags exist to suppress, quiet, or reformat this output. No
`--plain`, `--quiet`, `--porcelain`, `NO_COLOR`, or similar mode is
implemented or planned in any open issue or design doc.

---

## Q3. Who specifically asked?

**Confidence: Absent**

No one has filed a request. Searched:

- All 23 GitHub issues (tsukumogami/niwa, open + closed)
- All 37 PRs (tsukumogami/niwa, merged + open + closed)
- All docs in `docs/designs/current/`, `docs/prds/`, `docs/guides/`
- The explore scope document `wip/explore_clone-output-ux_scope.md`

The explore scope document (written by the maintainer, not filed as a
GitHub issue) names the topic as a research question ("Should niwa
replace its linear log-dump...") but frames it as an open question for
exploration, not a committed request or external demand signal.

No issue numbers, comment authors, or PR references exist for this
feature request.

---

## Q4. What behavior change counts as success?

**Confidence: Absent**

No acceptance criteria, stated outcomes, or measurable goals exist for
this feature in any durable artifact. The explore scope document
describes the problem ("linear log-dump") and names the desired outcome
("modern inline indicators that replace status in place") but this is
the maintainer's own framing of a potential improvement, not a shipped
requirement or user-authored acceptance criterion.

---

## Q5. Is it already built?

**Confidence: Absent**

No progress bar libraries, spinner libraries, `isatty` calls, or
`NO_COLOR` checks are present in the codebase.

`go.mod` dependencies are: `github.com/BurntSushi/toml`, `github.com/cucumber/godog`,
`github.com/spf13/cobra`, plus indirect deps. No terminal UI libraries
(bubbletea, lipgloss, mpb, uiprogress, pterm, progressbar, charm, etc.)
appear in `go.mod` or `go.sum`.

A search across all `*.go` files in `internal/` found no matches for
`isatty`, `NO_COLOR`, `--plain`, `--no-progress`, spinner patterns, or
progress bar patterns.

The output architecture is scattered direct writes: 14+ `fmt.Fprintf(os.Stderr, ...)`
calls in `internal/workspace/apply.go` alone, with subprocess stdout/stderr
also piped directly to `os.Stderr`. There is no `Reporter` or `Progress`
abstraction.

---

## Q6. Is it already planned?

**Confidence: Absent**

No open GitHub issue, design doc, PRD, or roadmap entry covers inline
progress indicators, output UX improvements, verbose/quiet modes, or
TTY-aware output for `create`/`apply`.

The shell navigation protocol design (DESIGN-shell-navigation-protocol.md)
anticipates future output modes as a compatibility concern but does not
plan them or assign them to any release. The vault integration guide
mentions a future verbose flag for optional-key miss logging but this is
a single-sentence deferral, not a planned issue.

The three open issues as of the research date (#46, #53, #61, #62) address
workspace composition, global config granularity, and env file security
gaps — none touch output UX.

---

## Calibration

### Demand not validated

The majority of questions returned Absent or Low confidence. This is **not**
the same as demand being absent. The evidence pattern is:

- Single-author repository with no external contributors filing issues.
- All feature work has been internally driven; no external demand signal
  would be expected even for features users might want.
- The explore scope document exists — the maintainer identified this as
  a potential improvement worth investigating — but that document is not
  itself a demand signal; it's a research prompt.
- One indirect signal: DESIGN-shell-navigation-protocol.md explicitly
  anticipates future output modes as a compat constraint, which means the
  maintainer has already considered that output behavior will evolve.

**Positive rejection evidence: None.** No closed PR was rejected on this
topic. No design doc de-scoped it. No maintainer comment declined a
request. The absence of demand signals is explained by the repository's
single-author pattern, not by evidence that the feature was evaluated and
found unwanted.

### What this means

Demand is not validated, but the feature has not been evaluated and
rejected. This is a "no signal yet" state, not a "no" state. The verdict
on whether to pursue it belongs to the user and convergence, not to this
research.

---

## Artifacts Consulted

| Artifact | Location | Finding |
|---|---|---|
| All 23 GitHub issues | tsukumogami/niwa | No output UX requests |
| All 37 GitHub PRs | tsukumogami/niwa | No output UX work |
| `internal/workspace/apply.go` | codebase | 14+ direct stderr writes; no abstraction |
| `internal/workspace/clone.go`, `sync.go`, `setup.go`, `configsync.go` | codebase | Subprocess output piped to os.Stderr |
| `go.mod` | codebase | No terminal UI libraries |
| `docs/designs/current/DESIGN-shell-navigation-protocol.md` | codebase | Anticipates future output modes; does not plan them |
| `docs/guides/vault-integration.md` | codebase | Defers verbose flag for optional-key logging |
| `docs/designs/current/` (all 19 files) | codebase | No output UX design doc |
| `docs/prds/` (7 files) | codebase | No output UX PRD |
| `wip/explore_clone-output-ux_scope.md` | wip | Frames the question; not a demand signal |
