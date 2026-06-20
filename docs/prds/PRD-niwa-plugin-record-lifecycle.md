---
schema: prd/v1
status: In Progress
problem: |
  niwa causes Claude Code to accumulate plugin install records it never
  cleans up, and force-enables marketplace auto-update that accelerates
  the decay. Across a developer's many workspace instances the records
  pile up and go dangling, intermittently breaking workflow-skill
  registration. The failure worsens with normal niwa use.
goals: |
  niwa stops leaving the Claude plugin registry in a state that breaks
  skill registration: it retires the records it is responsible for over
  the workspace-instance lifecycle, offers a recovery path for damage
  already accumulated, and makes marketplace auto-update a configurable
  choice rather than a forced default — all without corrupting the
  Claude-owned registry file.
upstream: docs/briefs/BRIEF-niwa-plugin-record-lifecycle.md
motivating_context: |
  A multi-day investigation traced intermittent shirabe-skill
  registration failures (cleared by /reload-plugins) to niwa's plugin
  handling — 111 install records for one plugin, 109 dangling. A
  one-time manual cleanup unblocked the user; this PRD makes the fix
  systematic.
---

# PRD: niwa plugin record lifecycle

## Status

In Progress

## Problem Statement

Claude Code keeps a global plugin registry at
`~/.claude/plugins/installed_plugins.json`. It writes one record per
`(plugin, project path, scope)` the first time a session in that path
enables the plugin, and it never removes a record when the record's
project path or cached plugin-version directory disappears. That
garbage-collection gap is Claude Code's, and it stays dormant until
something produces many short-lived project paths and churns cached
versions.

niwa is that something. For every workspace instance it manages, niwa
writes plugin enablement into every repo subdirectory, so each repo of
each instance becomes a distinct project path and a distinct registry
record. niwa then destroys instances without removing those records,
and it force-enables marketplace auto-update, which keeps cached
versions turning over so Claude Code's own cache sweep deletes the old
directories. The records left pointing at the removed paths and
versions become dangling.

The accumulated registry ends up mostly dead — in the observed case 109
of 111 records for a single plugin were dangling. When a new session
resolves that plugin at startup, resolution runs against the pile and
intermittently lands on a dead record, so the plugin's skills fail to
register. The developer sees workflow skills disappear, `/plugins`
report an error, and has to run `/reload-plugins` to recover. Because
the breakage scales with the number of workspace instances a developer
churns through, it degrades the daily reliability of the exact users
who lean on niwa most.

niwa does not own the registry file and cannot fix Claude Code's
missing GC. But niwa is the actor that turns a dormant weakness into a
recurring failure, and it is the actor positioned to stop — by cleaning
up the records it causes and by not accelerating their decay.

## Goals

- A developer who churns through many niwa workspace instances stops
  experiencing intermittent skill-registration failures attributable to
  niwa-caused registry decay.
- The plugin registry stays proportional to the workspace instances
  that currently exist, rather than growing without bound.
- A developer whose registry is already damaged can recover it through
  niwa, without hand-editing a Claude-owned file.
- A developer who develops a marketplace they also consume can keep it
  stable for daily use, because auto-update is opt-in rather than
  forced.
- A consumer of a github-sourced marketplace runs its latest stable
  release by default rather than an in-development build off main.
- niwa never corrupts the registry file or destroys records it is not
  responsible for.

## User Stories

- As a developer tearing down a finished workspace instance, I want
  niwa to retire the plugin records that instance caused, so that no
  dangling records are left behind to break future sessions.
- As a developer opening a brand-new session, I want my workflow skills
  to register on the first try, so that I do not have to run
  `/reload-plugins` as a routine workaround.
- As a developer whose registry has already accumulated stale records,
  I want a niwa command that detects and removes the dead records, so
  that I can recover reliable registration without manual file surgery.
- As a developer who maintains a marketplace I also consume, I want
  auto-update to be off unless I opt in, so that pushing changes does
  not churn my other live sessions into breakage.
- As a developer running any niwa command that modifies the registry, I
  want the operation to be safe against concurrent sessions and against
  interruption, so that I never end up with a corrupted registry.

## Requirements

Functional:

- **R1.** When niwa destroys a workspace instance, it SHALL remove from
  the Claude plugin registry the install records whose project path
  falls under that instance's root.
- **R2.** When niwa destroys an entire workspace, it SHALL remove the
  install records for every instance under that workspace root.
- **R3.** Creating or updating a workspace SHALL automatically remove
  dangling records — a record whose `installPath` directory or whose
  `projectPath` directory no longer exists — from the registry, healing
  previously-accumulated damage with no separate command.
- **R4.** The automatic heal SHALL report how many records it removed and
  from which plugins.
- **R5.** The automatic heal SHALL run in the shared materialization
  path that both workspace creation and update invoke, and SHALL remove
  only dangling records, never records for live paths.
- **R6.** niwa SHALL treat marketplace auto-update as a per-marketplace
  configuration value that defaults to disabled, and SHALL only enable
  auto-update for a marketplace when the workspace configuration opts it
  in.
- **R7.** The workspace configuration SHALL provide a way to set the
  auto-update value per marketplace, and niwa SHALL write the resulting
  value into the marketplace registration it materializes.
- **R8.** niwa SHALL register a marketplace under the name declared in
  that marketplace's manifest, so a marketplace is not registered under
  a name that conflicts with its declared identity.
- **R9.** Registry removals SHALL be scoped to records that meet an
  explicit removal criterion (instance-owned for R1/R2; dangling for
  R3/R5). niwa SHALL NOT remove records that do not meet the stated
  criterion.

Non-functional:

- **R10.** Any niwa write to the Claude plugin registry SHALL be atomic
  with respect to concurrent readers and writers — a concurrent session
  reading the registry SHALL never observe a truncated or malformed
  file, and niwa SHALL tolerate the file being absent or rewritten by
  another process between read and write.
- **R11.** Before the first registry mutation in an operation, niwa
  SHALL preserve a recoverable copy of the prior registry state, so an
  unintended removal can be reversed.
- **R12.** A registry mutation SHALL be resilient to a malformed or
  unexpected registry shape: niwa SHALL fail safe (leave the file
  unchanged and report) rather than corrupt or truncate it.
- **R13.** The registry-mutating behaviors SHALL be covered by automated
  tests, including the dangling-detection criterion and the fail-safe
  behavior on a malformed registry, using niwa's existing unit and
  functional test harnesses.

Functional (version tracking):

- **R14.** niwa SHALL register each github-sourced marketplace to track
  its latest stable release by default — the highest non-prerelease
  version tag — rather than the marketplace's default branch.
- **R15.** niwa SHALL support a per-marketplace override of the tracked
  version: latest stable release (default), the default branch (the
  prior behavior), or an explicit pinned version/ref.
- **R16.** When a github marketplace has no published stable release,
  niwa SHALL fall back to the default branch and report that it did so,
  rather than failing the operation.
- **R17.** The version-tracking selection SHALL live in the same
  per-marketplace configuration as the auto-update value (R7).

## Acceptance Criteria

- [ ] Destroying an instance removes exactly the records whose project
      path is under that instance root, and leaves all other records
      intact (verified against a seeded registry).
- [ ] Destroying a workspace removes records for all of its instances
      and no others.
- [ ] After `niwa apply`, no record remains whose `installPath` or
      `projectPath` directory is missing, and apply reports the count and
      affected plugins it removed.
- [ ] Creating a fresh workspace against a registry seeded with dangling
      records removes those records too (the heal runs on create, not
      only update).
- [ ] The heal removes only records whose `installPath` or `projectPath`
      directory is missing; records for live paths are left intact.
- [ ] A marketplace registered by niwa has auto-update disabled unless
      the workspace configuration opts that marketplace in; an opted-in
      marketplace has auto-update enabled in the materialized
      registration.
- [ ] A marketplace whose manifest declares a name different from its
      source ref is registered under the manifest-declared name.
- [ ] A registry write interrupted partway leaves either the prior
      registry or the fully-updated registry on disk, never a truncated
      file (verified by the test harness).
- [ ] A registry mutation invoked when the registry file is absent
      completes without error, and a mutation whose underlying file
      changed between read and write does not clobber the concurrent
      write (verified by the test harness).
- [ ] Running a registry mutation against a malformed registry file
      leaves the file unchanged and reports the condition.
- [ ] Setting a per-marketplace auto-update value in the workspace
      configuration produces a matching auto-update value in the
      marketplace registration niwa materializes.
- [ ] A recoverable copy of the prior registry exists after a mutation,
      and restoring it reproduces the pre-mutation registry exactly.
- [ ] A github marketplace with no version override registers tracking
      its latest stable (non-prerelease) release, not its default branch.
- [ ] Overriding a marketplace to the default branch registers it
      tracking that branch; overriding to an explicit version/ref
      registers exactly that ref.
- [ ] A github marketplace with no published stable release registers
      tracking the default branch and the fallback is reported.
- [ ] New behavior is covered by unit tests and at least one functional
      (end-to-end) scenario.

## Decisions and Trade-offs

These resolve the framing questions the upstream BRIEF deferred.

- **Two surfaces: teardown removal and automatic heal — no command.**
  Teardown cleanup (R1/R2) prevents new orphans at the moment niwa
  removes the paths. The automatic dangling heal (R3/R4/R5) runs as part
  of ordinary workspace creation and update and repairs damage that
  already exists or accrues outside teardown. A standalone recovery
  command was explicitly rejected: the user does not want an extra
  command to fix broken environments, and the create/update path the
  user already runs is the right place to self-heal. Alternatives:
  teardown-only (rejected — does nothing for registries already damaged,
  which is the situation today); a command (rejected per the above);
  doing nothing automatic and relying on manual repair (rejected — it is
  exactly the manual surgery this feature removes).

- **The automatic heal is dangling-only, not instance-scoped.** R3/R5
  remove only records failing the missing-directory criterion, never
  records for live paths. This keeps a frequently-run, broadly-scoped
  heal from making aggressive deletions. Removing records for
  marketplaces merely absent from the current config was considered and
  rejected — too easy to delete a record a different live workspace still
  needs. Targeted instance-scoped removal stays bound to destroy, where
  the instance is unambiguously going away.

- **Safety contract is stated here, mechanism deferred to DESIGN.**
  R10–R12 fix the requirement — atomic, backed-up, fail-safe mutation of
  a file niwa does not own and other processes touch concurrently. The
  concrete mechanism (temp-file-and-rename, lock discipline, backup
  location and retention) is a DESIGN decision, not a PRD one.

- **Auto-update defaults to disabled for all niwa-registered
  marketplaces (R6).** This matches Claude Code's own safer default for
  third-party marketplaces and removes the accelerant. Alternatives:
  default-on for github sources and off for local (rejected — the
  observed breakage was on a github source the user develops);
  keep-forcing-on with a global off switch (rejected — the default is
  the problem). Trade-off: a developer who wants a github marketplace to
  track upstream automatically must opt in per marketplace; accepted
  because opting in is explicit and the churn cost of default-on is what
  this feature exists to remove.

- **Track the latest stable release by default, with override (R14-R17).**
  niwa registers github marketplaces against their main branch today, so
  it installs in-development versions (e.g. a `-dev` build) that change
  on every upstream commit. Tracking the latest stable release instead
  cuts version turnover sharply — which directly reduces the cache churn
  that produces dangling records — and gives consumers shipped versions.
  Alternatives: explicit-pin-only (rejected as the default — forces a
  manual bump to ever move forward, and most consumers want "latest
  stable" not a frozen pin); default-to-main (rejected — it is the
  current behavior and the source of the churn); release-only with no
  override (rejected — a marketplace author consuming their own
  in-development marketplace still needs a `main` escape hatch). Chosen:
  default to latest stable release, with per-marketplace overrides for
  the branch or an explicit ref. One mechanism unknown — whether Claude
  Code's github marketplace source accepts a pinned ref, or whether niwa
  must resolve and express it another way — is left to the DESIGN, which
  may gate it on a spike.

- **The marketplace name-keying fix is in scope (R8).** niwa currently
  keys a marketplace by its source ref rather than its manifest-declared
  name, which can register the same logical marketplace under two names
  and produce conflicting entries. It is a small, related correctness
  fix in the same registration code the auto-update change touches, so
  it is folded in rather than split out.

- **Reducing per-repo enablement proliferation is NOT committed here.**
  Enabling plugins once per instance instead of once per repo would cut
  records at the source, but it depends on Claude Code's plugin-scoping
  semantics (whether a parent-scoped enablement applies to child repo
  directories), which is unverified. Whether to pursue it is a
  spike/DESIGN question, recorded as a remaining unknown the DESIGN may
  investigate — not a requirement this PRD commits to.

- **Fixing Claude Code's missing GC is out of scope.** The durable
  cross-vendor fix is an upstream report tracked separately; this PRD is
  niwa mitigating its own amplification.

- **Complexity: Complex — a DESIGN is warranted before implementation.**
  The feature requires niwa to safely mutate a global file owned by
  another tool while concurrent sessions may read or write it, and it
  spans several integration points (teardown, the create/update heal,
  and marketplace registration). The architectural shape
  of this feature warrants a DESIGN doc.

## Out of Scope

- Fixing Claude Code's own garbage collection of the registry. niwa
  does not own `installed_plugins.json`; the upstream fix is tracked
  separately and is not a dependency of this work.
- Reducing the per-repo-per-instance record proliferation by changing
  how niwa enables plugins. Deferred to a spike/DESIGN investigation
  (see Decisions and Trade-offs); this PRD treats it as a non-commitment.
- Changing which plugins or marketplaces niwa installs, or any plugin's
  content. The feature concerns record lifecycle and update policy, not
  the plugin set.
- A standalone repair command (a `niwa plugins prune` / `niwa doctor`
  surface). Recovery is automatic on create/update; the user explicitly
  does not want an extra command to fix broken environments.
- The one-time manual registry cleanup already performed during the
  investigation. That unblocked the user; this work makes the fix
  systematic and is not a redo of it.
- Managing Claude Code's marketplace clones or its plugin cache
  directories directly. The feature acts on the install-record registry
  and on the marketplace registration niwa materializes, not on Claude
  Code's cache lifecycle.
