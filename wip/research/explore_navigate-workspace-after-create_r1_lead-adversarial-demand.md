# Demand Validation: Navigate into Workspace After Create

## Visibility

Public

## Summary

This report evaluates whether evidence supports pursuing shell integration
for post-create workspace navigation (niwa issue #31). Findings draw from
niwa's issue tracker, codebase, design documents, and tsuku's related
shell integration work.

---

## Question 1: Is demand real?

**Confidence: Medium**

A single issue exists: niwa#31, filed by the project maintainer (dangazineu)
on 2026-03-31. There are zero comments on the issue and no labels or milestone
assigned. No other users have reported this friction point or requested this
feature.

The maintainer filing the issue is meaningful -- it reflects direct experience
with the tool's UX gap. But there is no corroborating evidence from other users.
Niwa is an early-stage project (v0.0.1 was released 2026-03-25), so a small
user base is expected and absence of external reports doesn't strongly argue
against demand.

## Question 2: What do people do today instead?

**Confidence: Medium**

The current workaround is visible in the codebase. `niwa create` prints
`Created instance: <path>` to stdout (`internal/cli/create.go:104`). Users
must manually copy or retype that path in a `cd` command.

The issue body acknowledges this: "niwa create printing the instance path to
stdout (already does this) so users can `cd $(niwa create ...)`." However,
the current output format (`Created instance: /path/to/dir`) is not directly
usable in a subshell capture -- it includes the prefix text, so `cd $(niwa create)`
would fail. A user would need to pipe through `awk '{print $NF}'` or similar.

No documentation, comments, or code suggests any other workaround.

## Question 3: Who specifically asked?

**Confidence: Medium**

- **niwa#31**: Filed by dangazineu (project maintainer). No other commenters or
  reporters.
- **No linked PRs** reference this issue.
- **No external contributors** have mentioned this friction.

## Question 4: What behavior change counts as success?

**Confidence: Low**

Issue #31 lists possible approaches but does not include explicit acceptance
criteria. The constraints section specifies:

- Must work with both bash and zsh
- Must support jumping to a specific repo within the workspace, not just the root
- Must account for the binary-can't-change-parent-shell constraint

These are constraints, not success criteria. There is no stated measurable outcome
(e.g., "user lands in workspace directory after create without additional commands")
or definition of done.

## Question 5: Is it already built?

**Confidence: High (that it is NOT built)**

Searched the niwa codebase for shell function wrappers, eval hooks, completion
scripts, and navigation helpers. None exist. Relevant findings:

- `install.sh` creates an env file at `$NIWA_HOME/env` that only exports PATH.
  There is no shell function wrapping `niwa` commands. (install.sh:104-108)
- `create.go` prints a human-readable message, not a machine-parseable path.
  No `--shell-hook` flag or similar exists.
- No completion scripts (bash or zsh) exist in the repository.
- `DESIGN-workspace-config.md` explicitly lists "shell integration" as out of
  scope (line 579).

Partial foundation: the install script already sources an env file into
`.bashrc`/`.zshrc`, which is the same mechanism a shell function wrapper would
use for distribution.

## Question 6: Is it already planned?

**Confidence: Medium**

- **niwa#31** is open and represents the plan for this specific feature.
- **DESIGN-workspace-config.md** defers "shell integration" as future work
  (line 579), confirming it was considered but not yet designed.
- **tsuku** has a parallel "Shell Integration Building Blocks" milestone with
  related design issues (tsuku#1681: shell environment activation, tsuku#2168:
  project-aware exec wrapper). Both are closed (design docs delivered). These
  address tsuku's own shell integration, not niwa's, but establish patterns
  the niwa solution could follow.
- No niwa roadmap file exists. No milestone is assigned to niwa#31.

---

## Calibration

**Demand not validated.** The majority of questions returned medium or low
confidence, with no corroboration beyond the maintainer's own issue filing.

Key gaps:
- Only one person (the maintainer) has reported this need. No external user
  requests exist.
- No acceptance criteria or success definition is documented.
- No milestone or prioritization signal beyond the issue being open.

This is not the same as "demand validated as absent." There is no evidence of
rejection -- no closed PRs declining the feature, no design doc arguments against
it, no maintainer comments saying "won't do." The feature is explicitly deferred
in DESIGN-workspace-config.md, which is a "not yet" signal rather than a "no"
signal.

The low external signal is consistent with niwa's early stage (6 days old at
issue filing, v0.0.1). The maintainer's direct experience with the friction is
a legitimate demand signal for a developer tool, particularly one where the
maintainer is also the primary user. But without external validation, the urgency
and priority remain the maintainer's judgment call.

---

## Sources Cited

| Artifact | Location |
|----------|----------|
| niwa#31 | `gh issue view 31` (niwa repo) |
| create.go output | `internal/cli/create.go:104` |
| install.sh env file | `install.sh:104-108` |
| DESIGN-workspace-config.md scope | `docs/designs/current/DESIGN-workspace-config.md:579` |
| tsuku#1681 | Shell environment activation design (tsuku repo, closed) |
| tsuku#2168 | Project-aware exec wrapper design (tsuku repo, closed) |
