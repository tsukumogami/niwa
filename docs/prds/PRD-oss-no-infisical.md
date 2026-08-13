---
schema: prd/v1
status: Accepted
problem: |
  niwa resolves a workspace's declared secrets before it will materialize an
  instance and treats any shortfall as fatal, so a contributor on a host with
  no vault backend installed or authenticated cannot create an instance at all.
  The workspace is still created, registered, and reported healthy, so the user
  holds a well-formed workspace that can never be used. Two different users
  reach that wall through two different enforcement points, and the documented
  escape hatch covers neither.
goals: |
  Instance materialization succeeds on a host with no vault backend, producing
  every value niwa could resolve and a legible account of the values it could
  not. Strict mode stays available for anyone who wants a shortfall to stop the
  command, including on the unattended paths that take no flags. No message
  asserts the existence of a configuration layer niwa cannot verify.
upstream: docs/briefs/BRIEF-oss-no-infisical.md
motivating_context: |
  A maintainer setting up a new host without the vault client installed found that
  workspace creation failed outright. Investigation established that the same
  wall is reached from a second direction by anyone cloning a public workspace
  configuration without access to the overlay that supplies its values, and
  that none of the declared secrets is read by niwa's own clone or materialize
  path.
---

## Status

Accepted

Requirements are drafted against a completed two-round investigation and
revised against a three-reviewer jury. The downstream design owns where each
mechanism lives.

## Problem Statement

niwa resolves a workspace's declared secrets before it will materialize an
instance, and treats any shortfall as fatal. That ordering makes an absent or
unauthenticated vault backend equivalent to a broken workspace, even though
nothing niwa itself does to clone repositories or write configuration reads
those values.

The failure is displaced from the command that appears to cause it. A plain
`niwa init` succeeds — it clones the configuration, registers the workspace,
and reports success. Every command that materializes an instance then fails:
`niwa create`, `niwa apply`, `niwa dispatch`, the SessionStart hook that
provisions an ephemeral instance for a background session, and `niwa init`
itself when its bootstrap branch runs, because that branch calls into the same
create flow. `niwa status` reports the workspace healthy throughout. Nothing
connects the successful init to the wall the user hits next.

### The four enforcement points

A shortfall can stop a command at four independent places. This document refers
to them by these names throughout:

1. **Unsatisfiable declaration** — the merged configuration marks a key
   required and contains no provider and no binding that could ever supply it.
   No network call occurs.
2. **Provider unreachable** — a provider is configured but cannot be contacted:
   the client binary is not installed, the session has expired, or the network
   failed.
3. **Required-key shortfall** — after all configuration layers merge, a key
   marked required holds no value.
4. **Promotion of an absent key** — a key listed for promotion into generated
   Claude Code settings is missing from the resolved set.

### The two first-run paths

Two users reach a wall through different enforcement points, so a fix aimed at
one does nothing for the other. Someone cloning a public workspace
configuration without access to an overlay never contacts a vault: their merged
configuration hits the unsatisfiable-declaration point, a static contradiction
that surfaces late and in terms naming neither the missing layer nor the vault.
Someone who has the overlay but has not installed or authenticated the vault
client stops at the provider-unreachable point, inside the resolver, and never
reaches the required-key check at all.

The escape hatch that appears to cover this does not. `--allow-missing-secrets`
downgrades a key the backend does not hold; it does not downgrade a backend
that cannot be reached, which is the shape of both "the CLI is not installed"
and "the session expired." Its help text and a doc comment on the
provider-unreachable sentinel both read as though it covers that case. It is
absent from `niwa init` entirely, and structurally unreachable from `niwa
dispatch` and the SessionStart hook, which accept no flags because no human is
present to pass one.

The cost is borne by exactly the people least equipped to diagnose it: a
first-time contributor meets an error about credentials they have never heard
of, for a backend they have no reason to know exists, on their first contact
with the project.

## Goals

- A contributor with no vault backend can create an instance and start working.
- Every value niwa can resolve is materialized; values it cannot are absent
  rather than blank, and the absence is legible both on the terminal and in the
  generated files themselves.
- The unsatisfiable-declaration and provider-unreachable points produce
  different messages, because they have different remedies: one needs a
  configuration fix, the other needs a client installed.
- Strict mode remains available to anyone who wants a shortfall to stop the
  command, and reaches the unattended paths.
- A required key that a configured, reachable backend simply does not hold
  still fails, because that is a real error with a clear owner.
- Recovery is re-entrant: installing and authenticating the backend, then
  re-applying, fills the gaps without re-creating anything.
- niwa never asserts or implies the existence of a configuration layer it
  cannot verify.

## User Stories

**As a first-time contributor to a public workspace**, I want to clone the
workspace configuration and create an instance without holding any credentials,
so that I can read and build the code on my first attempt instead of debugging
someone else's secret management.

**As a maintainer setting up a new machine**, I want instance creation to
succeed before I have installed and authenticated the vault client, and to be
told plainly which backend could not be reached and what to install, so that I
can get working immediately and fill in the credentials when convenient.

**As a background agent provisioned by a session hook**, I want to receive my
instance along with a statement of which capabilities are unavailable, so that
I learn what I cannot do before I attempt it rather than booting with no
instance and no explanation.

**As an operator provisioning environments where a missing credential must halt
the run**, I want to declare strict mode once in a place the unattended paths
honour, so that dispatch and hook-driven provisioning fail as loudly as an
interactive command.

**As the author of a public workspace configuration**, I want the requirement
levels I declare to mean something a contributor without my credentials can act
on, so that publishing the configuration does not publish a broken first-run
experience.

## Requirements

Requirement numbers are stable cross-reference identifiers, cited by the
acceptance criteria, the Decisions section, and downstream artifacts. The
headings below group requirements thematically, so the numbers are not
monotonic within a group.

### The default: materialize, report, exit 0

**R1.** Instance materialization SHALL NOT be gated on successful secret
resolution. `niwa create`, `niwa apply`, `niwa dispatch`, the SessionStart
ephemeral-instance path, and `niwa init`'s bootstrap branch SHALL complete and
produce a usable instance when one or more declared secrets cannot be resolved,
exiting with status 0.

R1 states the default. It is subject to exactly two exceptions: the
required-key-on-a-reachable-provider case in R10, and strict mode in R12. Both
exceptions retain today's failure semantics in full — the command exits
non-zero and no instance is left behind. No other condition overrides R1.

**R2.** A declared key that cannot be resolved SHALL be omitted from generated
environment files. It SHALL NOT be written with an empty value. Downstream
consumers distinguish an unset variable from one set to the empty string, and
the second produces failures far from their cause.

**R2a.** A key whose reference explicitly opts out of resolution failure —
today spelled `?required=false` on a `vault://` reference — is NOT an
unresolved key for the purposes of R2 and R6. Its author has declared that an
empty value is acceptable, which makes it a deliberate empty rather than a
shortfall. Such a key SHALL continue to be written with an empty value and
SHALL NOT appear in the R6 report. This preserves the silent-downgrade contract
that opt-out exists to provide.

**R3.** Generated environment files SHALL record each omitted key in a form
that is legible to a person reading the file and, for dotenv-format targets,
from which niwa's own reader of those files recovers the key's name and its
declared description. The record SHALL NOT carry a value. The record SHALL NOT
corrupt the value of any adjacent assignment.

**R3a.** For non-dotenv output targets, the record SHALL be emitted in that
target's own format. niwa's reader parses every target as dotenv regardless of
declared format, so a non-dotenv record is not recoverable by it; that is a
known consequence of the format blindness recorded in Known Limitations, not a
defect this work introduces or fixes.

**R4.** The record required by R3 SHALL be removed when a subsequent apply
resolves the key, leaving no residue of the previous omission.

**R5.** niwa SHALL distinguish, and carry through to reporting, which
enforcement point a key's shortfall reached: unsatisfiable declaration,
provider unreachable, or required-key shortfall. Promotion of an absent key is
governed separately by R11.

**R6.** A command that materializes an instance with one or more unresolved
keys SHALL emit a single consolidated report. The report SHALL name every
affected key, and SHALL carry for each key its declared requirement level and
its declared description. It SHALL NOT name only the first key encountered.
Keys covered by R2a are not unresolved and SHALL NOT appear.

"Declared requirement level" means the key's placement in the
`required` / `recommended` / `optional` sub-tables of the workspace
configuration. It is a separate mechanism from the per-reference opt-out in
R2a, which is a property of a `vault://` reference rather than of a
declaration. The report reflects the sub-table level only.

**R7.** The report required by R6 and the records required by R3 SHALL both be
deterministically ordered across runs against unchanged input.

### Message content

**R8.** When no provider capable of supplying a key is configured anywhere in
the merged configuration, the report SHALL state that and no more. It SHALL NOT
assert, imply, or speculate about the existence of a configuration layer the
reader cannot see, and SHALL NOT name a repository niwa has not successfully
read.

**R9.** When a configured provider cannot be reached, the report SHALL name the
provider kind, state that it could not be reached, and give a remedy. niwa
SHALL distinguish at least two causes and give a different remedy for each: the
client binary is not present on the host, versus any other unreachability.

**R20.** The report required by R6 SHALL NOT contain vault-vendor or
provider-implementation vocabulary beyond the provider kind that R9 requires.
The prohibited vocabulary is a named list maintained alongside the test that
enforces this. The provider-kind token itself is exempt, since R9 mandates it.

### Strict mode and its exceptions

**R10.** A required key SHALL remain a fatal error when a provider capable of
supplying it is configured, is reachable, and does not hold the key. This is
the strict-when-reachable rule: the guarantee is retained exactly where it
still identifies a real fault with a known owner.

**R12.** niwa SHALL provide a strict mode that restores fail-on-shortfall
behaviour at all four enforcement points. Strict mode SHALL be expressible as a
setting the unattended paths — `niwa dispatch` and the SessionStart hook —
honour without a command-line flag, and SHALL additionally be settable
per-invocation on those commands in R1 that accept flags: `niwa create`, `niwa
apply`, and `niwa init`. A per-invocation setting SHALL take effect, not merely
be accepted.

**R13.** Strict mode SHALL NOT be settable by a visibility overlay layer. A
contributor's first-run experience SHALL NOT be alterable by a configuration
layer they cannot read. An attempt to set it from such a layer SHALL be
reported and SHALL NOT take effect.

**R21.** Strict mode SHALL NOT apply to worktree re-materialization, which
remains tolerant of an unresolved secret regardless of the setting. That path
is already deliberately tolerant so a transient backend outage does not break
an existing worktree, and this work does not change it.

### Adjacent behaviour

**R11.** Promotion of a declared key into generated Claude Code settings SHALL
tolerate a key omitted under R2 by omitting it from the generated settings and
reporting it under R6, rather than failing the command.

**R14.** On partial provisioning, the SessionStart hook SHALL emit its
structured output carrying the R6 report and SHALL exit 0.

**R15.** Re-running apply after a previously unreachable provider becomes
available SHALL resolve the previously omitted keys and update the generated
files in place, without requiring the instance to be re-created.

**R16.** `--allow-missing-secrets` SHALL become a no-op, because R1 makes its
tolerant behaviour the default for every command. It SHALL continue to be
accepted, SHALL be documented as deprecated, and SHALL NOT change any outcome
except as stated in the next sentence. Passing it together with the strict-mode
flag SHALL be rejected as a contradiction rather than silently resolved.

**R17.** No description of `--allow-missing-secrets` anywhere in the codebase
SHALL claim behaviour that R16 removes. Its flag help text is the user-facing
instance; the design identifies the remaining sites.

### Non-functional

**R18.** No secret value SHALL appear in any report, record, annotation, or log
introduced by this work. Key names and declared descriptions only.

**R19.** A workspace whose secrets all resolve SHALL behave exactly as it does
today: same generated files, same exit codes, no new output.

## Acceptance Criteria

- [ ] `niwa create` and `niwa apply` against a workspace declaring required
      secrets, on a host with no vault client installed, each exit 0 and
      produce a usable instance with all repositories cloned.
- [ ] The same command against a workspace whose merged configuration declares
      required secrets and configures no provider at all exits 0 and produces a
      usable instance.
- [ ] `niwa dispatch`, the SessionStart provisioning path, and `niwa init
      --bootstrap` each produce an instance under both conditions above, with
      no flag passed.
- [ ] Generated environment files contain no entry, empty or otherwise, for a
      key that could not be resolved.
- [ ] For a dotenv-format target, niwa's reader of generated environment files
      recovers the key name and declared description of every omitted key.
- [ ] For a json-format target, the generated file remains valid JSON with the
      omitted-key record present; for a shell-format target, the file remains
      sourceable. Neither is required to be recoverable by niwa's reader.
- [ ] An omitted-key record placed immediately before and immediately after a
      resolved assignment, and at the first and last lines of the file, leaves
      that assignment's parsed value byte-identical to its value with no record
      present.
- [ ] Re-running apply after the key becomes resolvable produces a file
      containing the key's value and no record of the earlier omission.
- [ ] The terminal report names every unresolved key, not a subset, and carries
      each key's declared requirement level and declared description, verified
      against a workspace declaring keys at more than one level.
- [ ] Two consecutive runs against unchanged input produce byte-identical
      terminal reports and byte-identical omitted-key records.
- [ ] The report emitted when no provider is configured contains no repository
      name, and no assertion about whether any additional configuration layer
      exists.
- [ ] The report emitted when a configured provider is unreachable names the
      provider kind, and gives a different remedy when the client binary is
      absent than when it is present but the provider is otherwise unreachable.
- [ ] The report contains no term from the prohibited-vocabulary list, verified
      by a test that fails when a term is added to a report.
- [ ] A required key that a configured, reachable provider does not hold causes
      the command to exit non-zero, with a message naming the key.
- [ ] A key omitted under R2 that is also listed for promotion into generated
      Claude Code settings is omitted from those settings, reported, and does
      not fail the command. The test uses a declared-but-unresolved key, not an
      undeclared one.
- [ ] With strict mode set as a workspace setting, `niwa dispatch` and the
      SessionStart path both exit non-zero on a shortfall, with no flag passed.
- [ ] With the strict-mode flag passed to `niwa create`, `niwa apply`, and
      `niwa init`, a shortfall causes a non-zero exit on each; the same
      commands without it exit 0.
- [ ] A required key that a configured, reachable provider does not hold leaves
      no instance behind, matching the failure semantics before this change.
- [ ] Strict mode set from a visibility overlay layer does not take effect, and
      the attempt is reported.
- [ ] Worktree re-materialization of an instance whose secret is unresolvable
      succeeds with strict mode set.
- [ ] On partial provisioning the SessionStart hook exits 0 and its structured
      output contains the R6 report.
- [ ] `--allow-missing-secrets` changes no outcome: for both an absent-key
      shortfall and an unreachable-provider shortfall, the exit code and
      generated files are identical with and without the flag.
- [ ] Passing `--allow-missing-secrets` together with the strict-mode flag
      exits non-zero with a message naming the contradiction.
- [ ] `--allow-missing-secrets` help text identifies the flag as deprecated.
- [ ] No description of `--allow-missing-secrets` in the codebase claims
      behaviour R16 removed, verified by a test that fails against the tree as
      it stands today.
- [ ] A workspace declaring a reference that explicitly opts out of resolution
      failure, for a key the provider does not hold, produces the same
      generated-file content and the same terminal output before and after this
      change: the key is written with an empty value and does not appear in the
      report.
- [ ] Under strict mode, a shortfall leaves no instance behind, matching the
      failure semantics before this change.
- [ ] No test fixture, report, or generated file produced by this work contains
      a secret value.
- [ ] A workspace whose secrets all resolve produces byte-identical generated
      files and identical exit codes before and after this change.

## Out of Scope

The upstream BRIEF's Scope Boundary defines the exclusions and they carry
forward unchanged; see `docs/briefs/BRIEF-oss-no-infisical.md`. This PRD adds
one exclusion the BRIEF did not name:

- **A diagnostic command answering "what does this workspace need and what do I
  have."** Investigation found no such command exists and that the nearest
  surfaces do not cover it — the auth audit needs an instance the failure path
  deletes, and the secrets audit reads resolved values rather than requirement
  tables. Worth building, but it is an addition rather than part of making the
  failing path degrade.

## Decisions and Trade-offs

### Strict mode is a workspace setting first, a flag second

The upstream BRIEF deferred whether strictness should be a flag, a setting, or
both. Both, with the setting primary.

Investigation established that the function serving `niwa dispatch` and the
SessionStart hook already loads the workspace and global configuration rungs,
so a setting there reaches the unattended paths that a flag structurally
cannot. This also reversed an earlier conclusion that no escape hatch could
reach those paths at all; the constraint was real, but it argued for a
different surface rather than against the capability.

The flag is retained as the interactive front door, because an operator running
a one-off strict provisioning should not have to edit configuration to do it.

**Alternative rejected:** flag only. It cannot reach two of the five affected
commands, which are precisely the ones where no human is present to diagnose a
silent shortfall.

### One strict mode, not per-enforcement-point granularity

The four enforcement points fail for different reasons, which argues for
separate controls. It was rejected.

The enforcement points are an artifact of niwa's internal structure. An
operator holds a single intent — whether an unresolvable secret should stop the
command — one altitude above that taxonomy, and per-point granularity would
require them to learn it in order to express what they already know.

The choice is deliberately reversible in one direction: adding granularity
later is additive, while removing it would break configurations that depend on
it. Starting coarse keeps the cheaper correction available.

### Strict-when-reachable retains the guarantee where it works

Softening every case uniformly was rejected in favour of R10.

When a provider is configured, reachable, and does not hold a required key,
something is genuinely wrong and someone specific can fix it. That is also the
maintainer's steady state, so retaining it means a fully provisioned workspace
behaves exactly as it does today. The trade-off is a rule with a condition
rather than a flat one, which is marginally harder to explain — accepted,
because the flat alternative discards a signal that catches real faults.

### `--allow-missing-secrets` becomes a deprecated no-op

An earlier draft required only that the flag's behaviour and its description
agree, and left the resolution to design. A reviewer correctly identified that
as ducking a user-visible question: widening the behaviour and narrowing the
description are opposite outcomes for the user this work exists to serve, and
both satisfied the requirement as written.

R16 decides it. Once R1 makes tolerance the default for every command, the flag
has nothing left to do — its entire meaning was "tolerate what is otherwise
fatal," and nothing is otherwise fatal except the two cases R10 and R12 define,
neither of which the flag should override. Widening it would give two spellings
for the default; keeping it meaningful would require it to override
strict-when-reachable, which is the one guarantee worth keeping.

**Alternative rejected:** removing the flag outright. It appears in existing
scripts and documentation, and accepting it as a documented no-op costs one
line and breaks nothing. The conflicting-flags rejection in R16 exists so that
a script combining it with strict mode fails loudly rather than picking a
winner silently.

### The disclosure constraint is a requirement, not a wording preference

R8 exists because a remote returns the same "not found" response for a private
repository the caller may not read and for one that does not exist. niwa cannot
distinguish them, so any message naming a configuration layer it failed to
fetch would assert something it does not know, and would disclose that layer's
existence to every contributor who runs the command.

This reversed two recommendations made during investigation, both of which
proposed naming the absent layer to make the error more actionable. The
actionability is real and the disclosure cost is higher.

**Consequence carried downstream:** because niwa cannot explain where the
values were supposed to come from, a workspace configuration's own key
descriptions and README carry the whole explanatory burden for a contributor.
This is why R6 requires the report to carry each key's declared description
rather than just its name — the description is the only explanation a
contributor gets.

### An unresolved key is omitted, not blanked

R2 chooses omission. The alternative — writing the key with an empty value —
keeps the file's shape stable and requires no reader changes, which is why it
is the tempting default.

It was rejected because an empty value is indistinguishable from a deliberately
empty setting, both to a person and to code, and it converts a diagnosable
absence into a silent misconfiguration whose symptom appears layers away.

Two costs follow and are accepted. R3's record has to be machine-recoverable,
because niwa's worktree path reads generated environment files back rather than
re-resolving secrets. And omission is what makes R11 necessary: the promotion
check keys on a key's absence from the resolved set, so today's empty-string
downgrade slips past it and R2's omission would start firing it. R11 is not
defensive — it addresses a failure this decision creates.

### An explicit per-reference opt-out is a deliberate empty, not a shortfall

A reference can already opt out of resolution failure, and the contract is that
the miss downgrades silently. An earlier draft would have broken it twice over:
R2 would have started omitting those keys instead of writing them empty, and
R6 would have named them in the report — destroying the silence that is the
opt-out's entire purpose. Two existing tests assert that silence, and one of
them would have failed while the other passed while the generated file changed
underneath it.

R2a resolves this in the direction the rest of the document already argues for.
The distinction R2 rests on is between a value that is deliberately empty and
one that is missing; an author who wrote the opt-out has said, in the only place
available to say it, that empty is acceptable here. So the key is resolved — to
empty, on purpose — and neither R2 nor R6 has any claim on it.

**Alternative rejected:** treating the opt-out as a shortfall and reporting it
at a lower severity. It would have made the report noisier for exactly the
authors who took the trouble to declare the value optional.

This also exposed a conflation worth naming: the `required` / `recommended` /
`optional` sub-tables and the per-reference opt-out are unrelated mechanisms
that both read as "requiredness". R6 now says which one the report reflects.

### The hook must exit 0 because that is the only delivery path

An earlier draft justified R14 by saying the hook's structured output reaches
the agent regardless of exit code. That is wrong about niwa: the provisioning
path returns an error before writing stdout, so a non-zero hook emits no
payload at all, and the repo's own design documentation states the hook must
exit 0 or lose the injection entirely.

The requirement is unchanged and the reasoning is stronger than the draft
claimed. Exit 0 is not a courtesy — it is the only way the report reaches the
agent, which is why R14 requires both together rather than either alone.

### Remaining unknown: what a total provisioning failure should say

The BRIEF deferred this and it is not settled here. A partial failure — an
instance that materializes without some values — is specified above. A total
failure, where no instance is produced at all, is a different condition whose
remedy depends on session semantics beyond the unresolved-secret case. It is
recorded in Known Limitations rather than specified.

## Known Limitations

- **Total provisioning failure is unspecified.** A background worker whose
  instance could not be created at all still lands somewhere with no
  repositories of its own and limited context. This work does not address that
  case; it only ensures an unresolved secret is not what causes it.
- **The strict-when-reachable rule has a blind spot.** A provider that is
  reachable but misconfigured — pointed at the wrong project, say — reports a
  key as absent rather than as a configuration error, and will therefore fail
  hard under R10 with a message about a missing key. That is the correct
  severity but not the most useful diagnosis.
- **Requirement descriptions become load-bearing.** Under R8, niwa cannot
  explain where a value should have come from, so a workspace whose
  declarations carry thin descriptions will produce a correct report that still
  leaves a contributor without a next step. This shifts an obligation onto
  configuration authors that nothing enforces.
- **Coarse strict mode may prove too coarse.** An operator wanting to fail on
  an unreachable provider but tolerate an unsatisfiable declaration cannot
  express that. The decision above accepts this deliberately, in the reversible
  direction.
- **The generated-file reader is format-blind.** niwa's worktree path parses
  every generated environment file as dotenv regardless of the declared output
  format. A record written in a json or shell target's own format is therefore
  invisible to that reader, which is what R3a accepts: recoverability is
  required for dotenv targets only. Records in every format must still avoid
  corrupting an adjacent assignment when dotenv-parsed, which they do because
  none of them contains an `=`. This work does not fix the underlying format
  blindness.
