---
status: Accepted
problem: |
  Operators of niwa workspaces get every repo's resolved secrets in one
  hardcoded file, .local.env, in one hardcoded format, dotenv. Real repos
  read different filenames (.env, .env.local) and sometimes different
  formats (JSON, sourceable shell), so operators hand-rename or
  hand-translate the file niwa just wrote, which is the manual step
  materialization was meant to remove.
goals: |
  Let an operator declare per repo which file(s) niwa expands secrets into
  and in which serialization, resolved through a repo -> workspace ->
  personal/global precedence with the current .local.env dotenv behavior
  as the untouched default, while keeping every written secret file
  invisible to the repo's git status regardless of the chosen name.
upstream: docs/briefs/BRIEF-secret-output-targets.md
motivating_context: |
  Follows configurable .env.example failure policy (#155) and
  self-guaranteed git invisibility for managed repos (#158). With
  invisibility now recorded in each repo's git exclude rather than tied to
  the .local name convention, the output filename is free to vary per repo.
---

# PRD: Configurable secret-output targets

## Status

Accepted

Upstream BRIEF (docs/briefs/BRIEF-secret-output-targets.md) is Accepted.
The architectural shape of this feature -- a pluggable output-format
writer interface, the configuration schema for the target declaration,
and the change to the managed git-exclude coverage -- warrants a DESIGN
doc before implementation. Requirements here are behavioral; the
mechanism choices they leave open are settled downstream in DESIGN.

## Problem Statement

niwa resolves each managed repo's secrets and materializes them into a
single file per repo at a hardcoded path, `.local.env`, serialized as
dotenv `KEY=value` text. This happens on both the instance `apply` path
and the worktree `apply` path.

The operators affected are anyone running niwa against repos that are
real applications. `.local.env` is a niwa-specific name almost no
framework loads: Node and Next.js read `.env.local`, python-dotenv and
Rails read `.env`, and some tools read a structured `secrets.json`.
Because the materialized name rarely matches what the application reads,
the operator finishes by hand: copy or rename `.local.env`, or translate
its contents into another format. The step that was supposed to remove
manual secret handling stops one file short of the one the application
loads.

This matters now because the two pieces that previously fixed the output
shape have been removed as constraints. Git invisibility no longer
depends on the `.local` name convention (#158 records ignore coverage in
each repo's git exclude), so the filename is free to vary. And operators
are using niwa against polyglot, multi-framework workspaces where one
fixed name cannot fit every repo.

## Goals

- An operator can point each repo's secret expansion at the file name its
  stack actually reads, without renaming anything by hand.
- An operator can choose, per target, among a small set of output
  serializations so a non-dotenv consumer is served directly.
- The choice is opt-in and inherited: a workspace-wide default set once,
  overridden per repo where stacks differ, and absent configuration
  reproduces today's `.local.env` dotenv output byte-for-byte.
- Every written secret file stays out of the repo's git status whatever
  the operator named it, with no weaker safety guarantee than today.

## User Stories

- As an operator of a polyglot workspace, I want each repo's secrets
  written to the filename that repo's framework loads, so that my apps
  pick up secrets with no manual rename.
- As an operator with a tool that reads JSON, I want a repo's secrets
  written as a JSON object, so that the tool consumes them without me
  translating dotenv by hand.
- As an operator whose repos mostly share one convention, I want to set a
  single workspace-wide output default and override only the exceptions,
  so that I configure the unusual repos, not every repo.
- As an operator who chose a git-tracked-by-default name like `.env`, I
  want the written file to stay invisible to git status, so that I never
  risk committing a real secret and never edit the repo's committed
  `.gitignore` to prevent it.
- As an operator who configured nothing, I want my existing `.local.env`
  output unchanged, so that upgrading niwa does not alter behavior I rely
  on.

## Requirements

### Functional

- **R1.** The system SHALL let an operator declare, per repo, one or more
  secret-output targets. When a repo declares no target, the system SHALL
  write a single `.local.env` target in dotenv format, identical to
  current behavior.
- **R2.** The system SHALL resolve the effective target declaration for a
  repo through a most-specific-wins precedence: a per-repo setting
  overrides a workspace setting, which overrides a personal/global
  setting; an unset level inherits from the broader level; the default
  applies when no level sets it. This mirrors the precedence model of the
  existing env-handling configuration (read-env-example, env-example
  policy).
- **R3.** A target declaration SHALL accept either a single target or a
  list of targets. A repo with multiple targets SHALL receive every one
  of its resolved secrets written to each declared target.
- **R4.** Each target SHALL be written in one of three output formats:
  dotenv, JSON, and sourceable shell-export. The format SHALL be
  determined by inference from the target's file extension, with an
  explicit per-target override available for cases the extension does not
  determine.
- **R5.** The dotenv format SHALL reproduce the current `.local.env`
  serialization, so that the default path and an explicitly-declared
  dotenv target are byte-compatible with today's output.
- **R6.** Each format SHALL serialize a flat set of name-to-value string
  pairs with escaping correct for that format, so that values containing
  spaces, quotes, newlines, or other delimiter characters round-trip
  through the consumer without corruption.
- **R7.** For every written target, the system SHALL ensure the target is
  invisible to the containing repo's git status, regardless of the
  target's name, by recording niwa-managed ignore coverage for it. The
  system SHALL NOT depend on the target name carrying any particular
  infix and SHALL NOT modify the repo's committed `.gitignore` to achieve
  this.
- **R8.** The system SHALL apply R1-R7 identically on both the instance
  `apply` materialization path and the worktree `apply` materialization
  path, so that a target declared once behaves the same wherever niwa
  writes the repo.
- **R9.** The system SHALL NOT change which values are resolved or how
  they are sourced. The existing inline `[env.vars]`/`[env.secrets]`
  resolution and the `.env.example` pre-pass SHALL be unchanged; only the
  destination of the already-resolved set changes.

### Non-functional

- **R10.** When the resolved target configuration is invalid (for
  example an unrecognized explicit format identifier, or a target whose
  ignore coverage cannot be guaranteed), the system SHALL fail closed:
  it SHALL surface a clear, actionable error naming the repo and the
  offending target rather than writing an unprotected or wrongly-formatted
  secret file. This matches the fail-closed posture already applied to
  managed-repo git invisibility.
- **R11.** No error, warning, or other diagnostic emitted by this feature
  SHALL contain a resolved secret value, a fragment of one, or any other
  secret material; diagnostics name the repo, the target path, and the
  format only.

## Acceptance Criteria

- [ ] A repo with no target declaration receives exactly one
  `.local.env` file in dotenv format, byte-identical to the pre-feature
  output. (R1, R5)
- [ ] A repo declaring a single custom target (for example `.env.local`)
  receives that file and does not receive `.local.env`. (R1)
- [ ] A per-repo target overrides a workspace target for that repo, and a
  repo with no target inherits the workspace target; with neither set,
  the default applies. (R2)
- [ ] A repo declaring a list of two targets receives both files, each
  containing the full resolved secret set. (R3)
- [ ] A `.json` target is written as a valid flat JSON object of the
  resolved secrets; a `.sh` target is written as valid sourceable
  shell-export; a `.env`/`.local.env` target is written as dotenv --
  each selected by extension inference. (R4)
- [ ] An explicit per-target format override produces that format even
  when the extension would infer a different one. (R4)
- [ ] A value containing spaces, a quote character, and a newline
  round-trips correctly out of each of the three formats. (R6)
- [ ] After apply, `git status` inside a repo with a custom-named target
  (including a git-tracked-by-default name like `.env`) shows a clean
  tree; the target does not appear as untracked, and the repo's committed
  `.gitignore` is unmodified. (R7)
- [ ] The same target declaration produces the same files with the same
  invisibility on both the instance apply path and the worktree apply
  path. (R8)
- [ ] The set of values written to a target equals the set the current
  resolution produces for that repo (no values added or dropped by this
  feature). (R9)
- [ ] An unrecognized explicit format identifier, or a target whose
  ignore coverage cannot be guaranteed, aborts the apply for that repo
  with a clear error naming the repo and target; no secret file is left
  in an unprotected or wrong-format state. (R10)
- [ ] No diagnostic emitted on the failure paths above contains any
  secret value or fragment. (R11)

## Decisions and Trade-offs

The BRIEF settled the user-facing behavior; these record the
requirements-level decisions and name what is deliberately left for
DESIGN.

- **Opt-in with an unchanged default (settled).** The default stays
  `.local.env`/dotenv rather than flipping to a new convention.
  Alternative considered: default to `.env`. Rejected because it would
  silently change every existing workspace's output on upgrade; opt-in
  keeps the change inert until an operator asks for it.
- **Format inferred from extension, with override (settled).** Inference
  keeps the common case to a single value (a filename) while the override
  covers the ambiguous case. Alternative considered: always-explicit
  format. Rejected as more verbose for every target to serve a rare case.
- **Three formats now, extensible later (settled).** dotenv, JSON, and
  shell ship; YAML and others are deferred behind a writer interface that
  admits a fourth without redesign. Alternative considered: build YAML
  now. Rejected per the BRIEF scope -- no concrete YAML consumer exists,
  and YAML carries a serializer dependency the other three do not.
- **List of targets, not a single target (settled).** Supporting more
  than one target per repo avoids a follow-up redesign for the
  app-plus-sidecar case at negligible cost over a single value.
- **Left to DESIGN (remaining unknowns).** The exact configuration key
  name and its nesting under the env configuration block; the exact
  format-identifier strings and the full extension-to-format inference
  table including the unknown-extension fallback; the writer-interface
  shape; and the precise mechanism for recording per-target ignore
  coverage. These are HOW decisions; DESIGN owns them.

## Out of Scope

- Output serializations beyond dotenv, JSON, and shell-export (for
  example YAML). Deferred until a concrete consumer needs one.
- Free-form template rendering (an operator-supplied template niwa fills
  in). A separate, larger feature; this PRD covers named files in known
  formats only.
- Per-variable routing of individual secrets to different targets. Every
  resolved secret goes to all of a repo's targets; splitting variables
  across files is excluded.
- Any change to which values are resolved or how they are sourced
  (`[env.vars]`, `[env.secrets]`, and the `.env.example` pre-pass are
  untouched).
