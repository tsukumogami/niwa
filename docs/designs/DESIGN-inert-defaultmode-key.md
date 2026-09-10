---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-inert-defaultmode-key.md
problem: |
  `buildSettingsDoc` maps a declared `permissions` posture to Claude Code
  modes Claude Code no longer honors from project scope (`bypassPermissions`)
  or never recognized (`askPermissions`, which voids the whole file), and
  `niwa dispatch` decides whether to forward `--permission-mode
  bypassPermissions` by reading that dead value back out of the instance-root
  settings file.
decision: |
  Record the resolved instance posture in niwa's own instance state at
  materialization and have dispatch derive the flag from it, passing the
  permission mode to the shared argv builder explicitly so `niwa watch`
  passes none. Replace the mapping table with a function under which `bypass`
  writes no `permissions.defaultMode` and `ask` writes `default`. Test argv
  and the document matrix end to end and the byte-level checks in Go.
rationale: |
  niwa's instance state is not a Claude Code settings file, so a tampered or
  deleted settings document can neither grant nor withhold bypass. The
  pipeline already persists facts there the same way, and the derivation
  becomes testable against a real materialization. An explicit builder
  parameter makes watch's empty value visible rather than an accident of a
  package global. Ordering the reader's move ahead of the producer's change
  keeps every step green.
decision_provenance: inline-resolved
---

# DESIGN: inert-defaultmode-key

## Status

Proposed

## Context and Problem Statement

A workspace declares a permission posture through `permissions` under
`[claude.settings]`, with `"bypass"` and `"ask"` as the accepted values. niwa
resolves that declaration per generated document and writes the result into
`permissions.defaultMode`. `buildSettingsDoc` in
`internal/workspace/materialize.go` is the one builder for all four
documents: the instance-root `.claude/settings.json`, each repo's
`.claude/settings.local.json`, each worktree's `.claude/settings.local.json`,
and the workspace-root `.claude/settings.json`. It maps the declared value
through `permissionsMapping`, which today is `bypass -> bypassPermissions`
and `ask -> askPermissions`.

There are three coupled technical defects.

**The mapping writes values Claude Code ignores or rejects.** From Claude
Code 2.1.257, `bypassPermissions` no longer takes effect from a project or
local settings file, and `auto` stopped at 2.1.142. Measured on 2.1.267, the
ignored value wins the settings merge and is downgraded to `default`, which
overrides a developer's own `acceptEdits` or `plan`. `askPermissions` was
never a valid mode, and a file carrying it is discarded whole, so every other
key niwa writes into an `ask` scope's document (hooks, worktree-delegation
deny rules, plugins) silently stops taking effect. The PRD requires that no
generated document ever carries `bypassPermissions`, `auto`, or
`askPermissions`, and that every value written is one Claude Code honors from
project scope (`default`, `acceptEdits`, `plan`, `dontAsk`). A `bypass`
document must carry no `permissions.defaultMode`. An `ask` document must
carry `default`, with every other key identical to the undeclared case.
Undeclared carries nothing.

**The only working route reads its input from the dead value.** `niwa
dispatch` forwards `--permission-mode bypassPermissions` on the worker's
launch command, and that flag does take effect. The derivation in
`runDispatch` (`internal/cli/dispatch.go`) decides whether to forward it by
reading the instance-root `settings.json` back through `readInstanceSettings`
and the `Permissions` field of the `instanceSettings` projection
(`internal/cli/dispatch_plugins.go`), then comparing it to
`"bypassPermissions"`. So the value this change must stop writing is the
derivation's only input. The PRD requires the decision to come from the
instance's effective declared posture: the workspace overlay, the workspace,
the personal overlay, then `[instance.claude.settings]`, highest winning.
Per-repo overrides don't affect it. A stale or hand-edited instance-root
document must not grant bypass, and a deleted one must not withhold it. The
flag must reach every form of dispatch (detached, attached, with remote
control). An operator's explicit `--permission-mode` must win and be the
only one on the argv. The effective config that decision needs is already
computed during provisioning: the instance pipeline resolves it with
`ResolveAndMergeEffectiveConfig` and hands it to the materializers, but
nothing carries the resolved posture out.

**The tests pin the wrong bytes and fixture the reader.** Six materializer
tests assert the values that must change, and the `ask` test has asserted
`askPermissions` since the mapping was written. The seven dispatch
permission-mode unit tests write their own settings file instead of running
the materializer, so they would stay green if the producer changed and the
reader didn't. Per-location equality checks can't show that a value is
absent everywhere, and today's golden manifests don't cover the workspace
root.

The system boundaries are `internal/workspace`, which holds the materializer,
the root materializer, the dead `WorkerPermissionMode` reader in
`permissions.go`, and the override resolution. They also include
`internal/cli`: the dispatch derivation, the shared argv builder
`buildDispatchPassthrough`, the provisioning result, and the re-entry
surfaces. `internal/watch` is a boundary that must not move. Its
operator-approval posture writes `default` into the same instance-root file,
and its review launches share the argv builder with an always-empty
permission value. The workspace docs are the last boundary: seven committed
documents still describe the retired mechanism.

## Decision Drivers

- **The declared posture decides, and no file can.** The derivation's input
  is the instance's effective posture as resolved from the declaration. A
  generated settings document can neither grant bypass (tampered to say
  `bypassPermissions`) nor withhold it (deleted). (PRD R3)
- **The flag stays on the launch argv.** Every dispatch form carries the
  derived flag. An explicit operator flag wins and is the only
  `--permission-mode` on the argv. Re-entry is `claude attach <handle>`, which
  takes no options, so resume depends on Claude Code preserving the launch
  flags. (R1, R2, R11)
- **Watch never gets a derived flag.** `niwa watch` builds its review argv with
  the same `buildDispatchPassthrough`, and a command-line flag outranks the
  `default` the review writes. The derivation must not move into that builder,
  and watch's empty value should be explicit rather than incidental. (R12,
  R13)
- **The materializer output follows the expected-value matrix.** Bypass writes
  nothing, ask writes `default`, and undeclared writes nothing, at all four
  locations and under every override combination. Since apply already
  overwrites each document whole, re-apply repairs existing instances without
  a migration. (R4-R9)
- **Invalid declarations are rejected at every level.** That includes the
  personal overlay, with an error naming `bypass` and `ask`, and no document
  written carrying the value. (R10)
- **No configuration change.** The declaration key, its values, and its
  override levels are unchanged. (R16)
- **The tests have to be able to fail.** Derivation tests take their input from
  a declaration, not a hand-written file. The document matrix is asserted with
  every location present. (R17, R18)
- **Codex is untouched.** Its dispatch forwards nothing on `--permission-mode`
  or `--sandbox`. (R12)
- **Smallest diff that fits the existing grain.** `runDispatch` is dense, and
  the pipeline already carries facts into instance state through
  `pipelineResult`. Prefer extending those over new mechanisms or a second
  settings read.
- **No second `--settings` producer.** The single inline-settings slot is
  occupied by remote control, and repeated `--settings` is silent last-wins.
  Nothing in this design may add a document to that slot.

## Considered Options

### Decision 1: Where the dispatch derivation gets the instance's effective posture

The derivation decides whether a dispatched Claude worker gets
`--permission-mode bypassPermissions`. Its input has to be the instance's
effective declared posture, resolved with the same precedence the
instance-root document uses. No generated settings document can be allowed to
change it: the PRD's tamper test overwrites the instance-root document with
`bypassPermissions` under an undeclared workspace and expects no flag, and it
deletes the document under a `bypass` workspace and expects the flag. The
derivation's tests also have to take their posture from a real declaration.
And a `[claude.settings]` value can be `vault://`-backed, so its plaintext
exists only during materialization.

Key assumptions:
- The instance-root posture is available as a resolved value during
  materialization. It is: the pipeline holds `effectiveCfg` at the point it
  runs the materializers, and `RootSettingsMaterializer.Materialize` reads the
  posture from `MergeInstanceOverrides(effectiveCfg)`.
- `InstanceState` accepts an additive `omitempty` field without a migration.
  It does, and several fields are added that way today.

#### Chosen: Record the resolved posture in instance state

During instance materialization, niwa records the declared posture the
instance-root document resolved from (`"bypass"`, `"ask"`, or nothing) in a
new `InstanceState` field, persisted to `.niwa/instance.json` alongside the
other facts the pipeline already carries there. Dispatch loads the instance's
state with `workspace.LoadState` and forwards `--permission-mode
bypassPermissions` when that field is `"bypass"`, the operator supplied no
flag, and the agent's permission flag is Claude's spelling.

`instance.json` is niwa's own state document, not a Claude Code settings file,
so rewriting or deleting `.claude/settings.json` has no effect on the
decision, and both tamper cases hold by construction. The field records niwa's
own vocabulary (`bypass`, `ask`) rather than a Claude Code mode, so no Claude
mode string is carried into state. The value is recorded after vault
resolution, which only materialization can do, and only once the materializers
have succeeded, so an invalid value never reaches state. The derivation
becomes a pure function of the recorded value, which a unit test can drive
from a real materialization, exactly the shape the tamper test needs. Dispatch
always provisions a fresh instance, so the field is always present where the
derivation reads it.

#### Alternatives Considered

**Thread the resolved posture out through the provisioning result**: add a
posture field to `provisionResult`, filled from the Applier's effective
config. Rejected because the Applier's `Create` returns no resolved-config
fact today, and every fake `provisionInstanceFunc` in the dispatch tests
builds `provisionResult` by hand, so the posture would be invented by the
fake. That's the hand-written-input pattern R17 forbids, moved one layer up,
and the derivation couldn't be run against an already-materialized instance.

**Re-resolve the effective config at dispatch time**: load `workspace.toml`
and both overlays again in `runDispatch` and apply instance overrides.
Rejected because it duplicates the precedence rules in a second place that
can drift from the materializer's, re-runs overlay discovery and vault
resolution on the dispatch path, and recomputes a value the pipeline resolved
for the same instance moments earlier.

**Keep a niwa-owned key in the instance-root settings document**, following
the `keepAliveOnDispatch` pattern. Rejected because it fails the PRD's R3: a
tampered key grants bypass and a deleted document withholds it, and it keeps
a generated Claude Code settings file in the decision path.

### Decision 2: How the materializer maps posture to a permission mode

`bypass` must produce no `permissions.defaultMode` (the posture travels on the
dispatch flag), and `ask` must produce `default`. Only `default`,
`acceptEdits`, `plan`, and `dontAsk` may ever be written. An invalid declared
value must fail with an error naming the accepted values at every level,
including the personal overlay, and no document may carry it. The
worktree-delegation `deny` entries share the permissions map and must keep
coexisting with whatever posture is written.

Key assumptions:
- Every generated document, including the workspace-root one, is built by
  `buildSettingsDoc`. It is: `writeRootSettings` calls it too.

#### Chosen: An explicit function instead of the mapping table

`permissionsMapping` is replaced by a function that returns a mode, whether to
write it, and an error: `bypass` returns "write nothing", `ask` returns
`default`, and anything else returns an error naming `bypass` and `ask`.
`buildSettingsDoc` calls it where it read the table, emits `defaultMode` only
when told to, and keeps building the `deny` entries into the same map
independently. The function's doc comment states why `bypass` writes nothing,
so the reason survives next to the code.

Validation stays at materialize time. It's the one point where a
`vault://`-backed value has plaintext. And a personal-overlay value flows into
the same merged config the materializer reads, so the same check covers every
level. An unknown value already fails before its document is written; the
change is the error text, which also stops echoing a value that came from a
vault reference.

#### Alternatives Considered

**Change the table's values**: map `bypass` to `""` and skip empty strings.
Rejected because an empty string becomes a hidden sentinel in a table named
for Claude Code modes, and the table would keep reading as "the Claude mode
for this posture", the claim this feature retires.

**Validate at config load time**: reject unknown values while parsing
`workspace.toml` and the overlays. Rejected because a `vault://` value has no
plaintext at load time, so materialize-time validation is still required,
and doing both would put one rule behind two error paths.

### Decision 3: How tests divide across layers

Each acceptance criterion observes something at a particular layer. The
dispatch argv exists only end to end, recorded through the functional suite's
fake `claude`. The document matrix's worktree has to come through
`niwa worktree create`, the entry point that skips the personal overlay. The
tamper window sits between materialization and derivation, which nothing
outside the process can reach, because dispatch provisions its own instance.

Key assumptions:
- The nine-example matrix can run in the full functional suite with only S1
  tagged `@critical`, keeping the critical lane fast.

#### Chosen: Divide by observable

Functional scenarios cover the dispatch argv: S1-S9 posture sources, the
explicit-flag cases, remote control, Codex, and the personal overlay using
the existing `a personal overlay exists with body` step. A Scenario Outline
over S1-S9 runs `niwa init` and `niwa worktree create`, plus an instance apply
for S9. A new step asserts that all four settings files exist and parse, then
checks each file's `permissions.defaultMode` value or its absence.

Go tests cover the rest. Where a test needs a materialized instance, it runs a
real `Applier.Create` in a temporary workspace, as
`internal/workspace/allow_missing_secrets_test.go` already does, rather than
hand-writing a settings or state file. The Go tests cover:
- the derivation against a real materialization, then tampered
- watch's fresh-review and continuation argv, built on a real `bypass`
  materialization whose state records `bypass`, so a later change that reads
  the recorded posture in a shared layer fails the test
- the four re-entry strings
- the `ask`-versus-undeclared differential, at each of the four locations
- re-apply over seeded pre-change documents
- invalid values at each level
- watch's review settings applied on top of a real materialization
- the recorded posture matching the posture the instance-root document was
  written from, across S1-S9

#### Alternatives Considered

**Everything functional**: every criterion as a scenario against the binary.
Rejected because the differential, tamper, watch, and re-entry checks concern
single functions and byte comparisons, and the tamper window can't be reached
from outside the process.

**Everything as Go tests**: drive the Applier in temporary directories.
Rejected because an in-package worktree write doesn't exercise
`niwa worktree create`'s config path, and the dispatch argv has no honest
Go-level observable other than the fake-provisioner seam R17 rules out for
posture input.

### Decision 4: How the derived mode reaches the argv builder

Today `runDispatch` writes the derived value into `dispatchPermissionMode`,
the package-level variable Cobra binds to the operator's `--permission-mode`
flag. `buildDispatchPassthrough` reads that variable implicitly.
`niwa watch` calls the same builder for its fresh-review and continuation
launches, and it gets an empty value only because nothing in a watch process
sets the variable. The PRD requires watch launches never to carry a derived
flag, and requires the explicit flag to be the only one on the argv.

#### Chosen: Pass the permission mode as a builder parameter

`buildDispatchPassthrough` takes the permission mode as an explicit argument.
`runDispatch` computes it locally: the operator's flag if set, otherwise
`bypassPermissions` when the derivation applies, otherwise empty. It passes
the result, and no longer overwrites the flag variable. Both watch call sites
pass `""`. Watch's empty value then becomes a visible property of its call
sites that a test can pin, rather than an accident of which process set a
global. And there is no longer a shared variable that a future change could
set once and have leak into another launch.

#### Alternatives Considered

**Keep the package global**: leave `runDispatch` assigning the derived value
to `dispatchPermissionMode`. Rejected because watch's safety would keep
depending on nothing in its process ever setting that variable, which no test
could guard directly. The parameter costs three production call-site edits.

## Decision Outcome

**Chosen: 1 (instance state) + 2 (explicit mapping function) + 3 (tests by
observable) + 4 (builder parameter)**

### Summary

niwa stops writing permission modes that Claude Code ignores, and it stops
reading its own dispatch decision back out of them. When the instance pipeline
materializes an instance, a resolver reads the instance-root posture from
`MergeInstanceOverrides(effectiveCfg)`, the same map the root settings
materializer builds from. Once the materializers have succeeded, Create and
Apply persist that posture into `InstanceState` in `.niwa/instance.json`, next
to `shadows` and `trustKeys`.

`buildSettingsDoc` asks a new mapping function what to write. `bypass` writes
no `permissions.defaultMode`, and `ask` writes `default`. Unknown values fail
with an error naming `bypass` and `ask`, and the error never prints a value
that came from a vault reference. The worktree-delegation `deny` entries are
built into the same map, unchanged. Because apply overwrites each document
whole, the next apply repairs every existing instance and workspace root.

In `runDispatch`, dispatch loads the instance's state and hands the recorded
posture to a pure derivation, which returns the operator's flag if set.
Otherwise it returns `bypassPermissions` when the recorded posture is `bypass`
and the agent's flag spelling is `--permission-mode`, and empty otherwise.
That value is passed to `buildDispatchPassthrough` as an argument, and both
watch launch sites pass `""`. The derivation is valid only for an instance
the calling process just provisioned. A state file that exists but can't be
read or parsed degrades to "nothing derived" with a stderr warning. A missing
state file degrades silently, which happens only under test fakes that
provision no real instance. Codex is gated out by its flag spelling, as it is
today.

The `Permissions` field on `instanceSettings` goes away. `readInstanceSettings`
keeps serving remote control and keep-alive. The dead `WorkerPermissionMode`
reader is deleted with its test.

Tests follow the observable. Functional scenarios pin the argv for every
posture source and dispatch form, plus the four-location document matrix over
S1-S9, with a real `niwa worktree create`. Go tests, built on real
`Applier.Create` runs, pin the derivation against a tampered materialization,
the mapping differential per location, re-apply repair, invalid values,
watch's argv and review settings, the re-entry strings, and the agreement
between the recorded posture and the written document. Seven committed
documents are corrected to describe the dispatch flag as the posture's route.

### Rationale

The four choices share one principle: niwa's own state decides, and Claude
Code's settings files only receive. The first choice moves the decision into
niwa's state. The second stops the files from claiming what they can't
deliver. The fourth makes the one shared seam between dispatch and watch
explicit. The third makes each of those properties observable where it lives.

The trade-off accepted is one additive field on persisted instance state,
which buys a derivation that no file can influence and that tests can drive
from a real declaration. Ordering the implementation so the reader moves off
the file before the producer stops writing it keeps the existing `@critical`
scenarios green at every commit.

## Solution Architecture

### Overview

The change has three moving parts and one deletion:
- A posture resolver in the pipeline, reading the same map the instance-root
  materializer builds from.
- A persisted posture field in instance state.
- A dispatch derivation that reads that field and passes an explicit mode to
  the argv builder.
- Deletion of the dead reader.

The materializer's document output changes per the PRD's expected-value table.
Nothing about which config inputs each document resolves from changes.

### Components

**Posture mapping (`internal/workspace/materialize.go`).**
`permissionsMapping` is removed. Its replacement,
`claudeDefaultMode(posture string) (mode string, write bool, err error)`,
behaves as follows:
- `"bypass"` returns `("", false, nil)`.
- `"ask"` returns `("default", true, nil)`.
- Any other value returns an error stating that the permissions value isn't
  one of `"bypass"` or `"ask"`.

The error includes the offending value only when it wasn't resolved from a
`vault://` reference. For a vault-backed value, it names the reference and the
config key, never the resolved plaintext, because the pipeline's secret
redactor doesn't scrub plain `fmt.Errorf` text. `buildSettingsDoc` calls the
function on the `maybeSecretString`-resolved value and adds `defaultMode` to
the permissions map only when `write` is true. The worktree-delegation `deny`
block is built as today, into the same map, and `permissions` is emitted only
when the map is non-empty.

**Instance posture resolver (`internal/workspace`).**
`instancePermissionsPosture(cfg *config.WorkspaceConfig) string` reads
`MergeInstanceOverrides(cfg).Claude.Settings["permissions"]` through
`maybeSecretString`. It returns the canonical literal `"bypass"` or `"ask"`
when the value matches one of them, and `""` otherwise, including when the key
is absent. It never returns the revealed string itself, and it doesn't
validate: `RootSettingsMaterializer` reads the same map and rejects an invalid
value before the pipeline saves state, so an unrecognized value never reaches
`InstanceState`. The resolver and the instance-root materializer share an
input, `MergeInstanceOverrides(effectiveCfg)`, not a code path. A test across
S1-S9 pins that the recorded posture equals the posture the instance-root
document was written from.

**Persisted posture (`internal/workspace/state.go`, `apply.go`).**
`InstanceState` gains `ClaudePermissions string
json:"claude_permissions,omitempty"`. The instance pipeline sets
`pipelineResult.claudePermissions` from the resolver, using the `effectiveCfg`
it already holds after `ResolveAndMergeEffectiveConfig`. `Create` and `Apply`
build their state fresh and copy the value into it, at the same sites that
copy `result.shadows` and `result.trustKeys`. The workspace-root state file
carries no value: it's written outside the instance pipeline, and nothing
reads a posture from it.

**Dispatch derivation (`internal/cli/dispatch.go`).** The block at step 9a
that reads `inst.Permissions` is replaced. `runDispatch` loads the state with
`workspace.LoadState(instancePath)`:
- If the file exists but can't be read or parsed, it prints a warning naming
  the state file and treats the recorded posture as empty.
- If the file doesn't exist, it treats the posture as empty silently.

It then calls the pure
`derivePermissionMode(explicit, recorded string, flags agentplan.LaunchFlags)
(mode string, derived bool)`. That function returns the operator's value when
one was given. Otherwise it returns `("bypassPermissions", true)` when
`flags.PermissionMode == "--permission-mode"` and `recorded == "bypass"`, and
`("", false)` in every other case. When `derived` is true, `runDispatch` keeps
the existing stderr notice saying the flag was derived.

The load site's comment states the same-process property. The recorded value
is trusted only for an instance the calling process just provisioned. A future
caller that needs the posture of an existing instance must re-resolve it from
configuration. The single `readInstanceSettings` call stays where it is for
the remote-control (9c) and keep-alive (9d) consumers.

**Argv builder (`internal/cli/dispatch.go`, `watch.go`).**
`buildDispatchPassthrough(flags, slug, model, permissionMode string)` takes the
permission mode as a parameter and no longer reads `dispatchPermissionMode`.
Dispatch passes the derived value. Both watch call sites, the fresh-review
launch and the `--resume` continuation, pass `""`. The test caller in
`dispatch_wiring_remotecontrol_test.go` is updated with them. Cobra keeps
binding `dispatchPermissionMode` to the operator's flag, and nothing else
writes it.

**Removed.** The `Permissions` field of `instanceSettings` in
`internal/cli/dispatch_plugins.go`, along with the two doc comments there that
point at `WorkerPermissionMode`. Also removed: `internal/workspace/permissions.go`
with `permissions_test.go`.

### Key Interfaces

```go
// internal/workspace
func claudeDefaultMode(posture string) (mode string, write bool, err error)
func instancePermissionsPosture(cfg *config.WorkspaceConfig) string

type InstanceState struct {
    // ...existing fields...
    // ClaudePermissions is the declared permission posture the instance-root
    // settings document resolved from ("bypass", "ask", or empty). niwa's own
    // record, read by niwa dispatch; never a Claude Code mode string.
    ClaudePermissions string `json:"claude_permissions,omitempty"`
}

// internal/cli
func derivePermissionMode(explicit, recorded string, flags agentplan.LaunchFlags) (mode string, derived bool)
func buildDispatchPassthrough(flags agentplan.LaunchFlags, slug, model, permissionMode string) []string
```

### Data Flow

1. `niwa dispatch` provisions a fresh instance through `provisionInstanceFunc`.
   The instance pipeline resolves the effective config: workspace overlay,
   workspace, personal overlay.
2. The materializers write the four documents through `buildSettingsDoc`,
   which asks `claudeDefaultMode` what each document's own posture produces:
   nothing for `bypass`, `default` for `ask`. An invalid value fails
   provisioning with the accepted-values error, before its document or any
   state is written.
3. The pipeline records `instancePermissionsPosture(effectiveCfg)` into
   `pipelineResult`. `Create` saves `InstanceState` with `ClaudePermissions`
   set.
4. `runDispatch` loads the state its own process just wrote, passes the
   recorded posture to `derivePermissionMode`, and hands the result to
   `buildDispatchPassthrough`. The argv carries `--permission-mode
   bypassPermissions` for a `bypass` instance, the operator's value when one
   was given, and nothing otherwise.
5. Remote control, when enabled, appends its `--settings` pair after the
   permission flag, unchanged. Claude Code records the launch flags. Re-entry
   is `claude attach <handle>`, and the worker comes back with its launch
   flags.

`niwa watch` provisions through the same pipeline, so its instance state also
records the posture. Watch never calls `derivePermissionMode`, and it passes
`""` to the builder.

## Implementation Approach

The phases are ordered so the reader stops depending on the file before the
producer stops writing it. The existing `@critical` dispatch scenarios stay
green at every step, and each phase compiles on its own.

### Phase 1: Record the posture in instance state

Add `instancePermissionsPosture`, the `ClaudePermissions` field, the
`pipelineResult` carry, and the copies in Create and Apply. The resolver
doesn't depend on the new mapping function, so this phase builds against
today's materializer. Nothing reads the field yet, and no output changes.

Deliverables:
- `internal/workspace/state.go`, `apply.go`, and a new resolver beside
  `materialize.go`.
- Go tests on real `Applier.Create` runs: the field is set for `bypass`, `ask`,
  and undeclared, under an `[instance]` override and under a personal
  overlay.

### Phase 2: Move the derivation onto instance state

Add `derivePermissionMode` and the state load in `runDispatch`. Give
`buildDispatchPassthrough` its parameter and update the dispatch call site,
both watch call sites, and the test caller. Remove the `Permissions`
projection and its stale doc comments, and delete `WorkerPermissionMode`.

Replace the seven fixture-based permission-mode tests with declaration-driven
ones built on a real `Create`. Add the tamper test (both cases), the watch argv
tests on a real `bypass` materialization, and the re-entry string tests.

The materializer still writes the old values, so behavior is unchanged, but
the derivation no longer reads them.

Deliverables:
- `internal/cli/dispatch.go`, `dispatch_plugins.go`, and `watch.go`.
- Deletion of `internal/workspace/permissions.go`.
- A rewritten `internal/cli/dispatch_permissionmode_test.go`.

### Phase 3: Change what the materializer writes

Replace `permissionsMapping` with `claudeDefaultMode` and update the
unknown-value error, including the vault-safe form.

Flip the materializer, root-materializer, apply, worktree, and secret-ref
tests that asserted `bypassPermissions` or `askPermissions`. Keep each test's
real subject intact: hooks, deny rules, secret resolution.

New tests in this phase:
- The `ask`-versus-undeclared differential at each of the four locations.
- The re-apply repair test.
- The recorded-posture-matches-document test across S1-S9.
- Invalid-value tests at each level, including a vault-backed invalid value
  whose plaintext must not appear in the error.
- A test that parses `permissions = "bypass"` and `"ask"` at the workspace,
  instance, repo, and personal-overlay levels.
- `niwa watch`'s review settings applied in both postures on top of a real
  materialization under S1, S2, and S3.

Regenerate the characterization goldens. They're content hashes, so review the
live bytes the characterization test prints on mismatch rather than the
manifest diff.

Give the `@critical` scenario in `workspace-config-sources.feature` a new
observable. Its body pushes `permissions = "bypass"` and asserts that the
workspace-root `settings.json` contains `bypassPermissions`. After this phase
that file is identical before and after the push, and the PRD forbids editing
the scenario's `workspace.toml` body. So the assertion step changes instead: it
checks that the instance's `.niwa/instance.json` records
`claude_permissions: "bypass"` after the same apply.

Deliverables:
- `internal/workspace/materialize.go` and its tests.
- `testdata/characterization/*`.
- The assertion step of the `workspace-config-sources.feature` scenario.

### Phase 4: End-to-end coverage

Add the dispatch scenarios: S2-S9, the explicit-flag cases, remote control,
Codex, and the personal overlay. Extend the existing explicit-flag scenario to
assert that exactly one `--permission-mode` is present.

Add the S1-S9 document-matrix Scenario Outline with the new settings-file step,
including an instance apply before checking S9's worktree cell. S1 stays
`@critical`.

Deliverables:
- `test/functional/features/dispatch.feature`, a new matrix feature file, and
  step definitions.

### Phase 5: Documentation

Correct the seven documents the PRD names. That includes rewording the comment
above the permission-mode scenarios and correcting `2.1.258` to `2.1.257`.

Deliverables:
- `docs/guides/ephemeral-session-instances.md` and
  `docs/guides/file-distribution.md`.
- Four documents under `docs/designs/current/`.
- `test/functional/features/dispatch.feature` (comment only).

## Security Considerations

**Who can obtain bypass doesn't change.** Whether `niwa dispatch` forwards
`--permission-mode bypassPermissions` is still decided by the same
declaration: the `permissions` key in the workspace overlay, the workspace
config, the developer's personal overlay, or `[instance.claude.settings]`,
highest winning, with per-repo overrides ignored. When nothing has been
tampered with, the same workspaces get bypass workers as before. What
changes is where dispatch reads the resolved answer. It used to read
`.claude/settings.json`. It now reads a field niwa records in its own
`.niwa/instance.json` while materializing, from the same config map the
settings document is built from.

**A settings file can no longer grant bypass.** Before this change, a session
able to edit the instance-root `.claude/settings.json` could turn a later
dispatch into a bypass launch. After it, dispatch reads no permission value
from any Claude Code settings file. A file hand-edited to say
`bypassPermissions` grants nothing, and deleting the file withholds nothing.

**The state file is trusted only right after provisioning.** `instance.json`
can be written by the same local user, and by any agent session running in
the instance, just as the settings file can. That's acceptable here for three
reasons:
- Dispatch reads the field immediately after its own process created the
  instance.
- The instance directory has an unpredictable name.
- Creating the instance writes the state file whole, so a value planted in
  advance is overwritten.

The recorded value is trusted only for an instance the calling process just
provisioned. Any future feature that needs the posture of an existing
instance should re-resolve it from configuration rather than trust this
field.

**Failure withholds the flag.** If the state file exists but can't be read or
parsed, dispatch forwards no permission mode and prints a warning naming the
file. The worker then prompts instead of running unattended. An operator's
explicit `--permission-mode` still wins, and is still the only
`--permission-mode` on the command line.

**`niwa watch` never gets a derived flag.** The permission mode is an explicit
argument to the shared argv builder. Dispatch passes the derived value, and
both watch launch sites, the fresh review and the continuation, pass an empty
string. Nothing in watch reads the recorded posture. The operator-approval
review posture relies on `permissions.defaultMode: "default"`, and a
command-line mode would outrank it, so tests pin watch's empty value against
an instance whose state records `bypass`.

**Side effects on watch reviews.** The hard-deny review posture sets no
permission mode of its own, so it runs in whatever the instance-root
document resolves to. In a `bypass` workspace that document no longer
carries a mode, so hard-deny and sandbox-off reviews run in the developer's
own mode rather than the `default` that Claude Code's downgrade used to
produce. The sandboxed boundary doesn't depend on the mode. The no-egress
sandbox, the egress-deny hook, and the filesystem-guard hook all apply under
any mode. In an `ask` workspace, the instance-root document used to carry a
mode Claude Code rejects, which made it discard the whole file, including the
containment settings watch merges into it. That file is now valid, so
hard-deny reviews in `ask` workspaces get their containment back.

**`ask` overrides every personal mode in its scope.** An `ask` scope writes
`defaultMode: "default"`. That replaces the developer's own default mode
there, whether it was more permissive (`acceptEdits`) or shaped differently
(`plan`, `dontAsk`). Actions not covered by an existing allow rule still need
a person's approval. Hooks and deny rules in `ask` scopes, which the previous
invalid value silently disabled, take effect again.

**Vault-backed values.** A `permissions` value can be a `vault://`
reference. niwa validates the resolved value against `bypass` and `ask`
before writing anything, and records only one of those two literals, so no
secret material reaches `instance.json`. An invalid vault-backed value fails
the operation with an error that names the reference, not the resolved
value.

**Per-repo `ask` doesn't restrain dispatched workers.** A dispatched worker
starts at the instance root. The derivation follows the instance's posture,
and a command-line `bypassPermissions` outranks a repo's `default` for files
in that repo. A maintainer who wants dispatched workers to ask declares `ask`
at the instance level.

**Containment gap carried over.** A worker running with `bypassPermissions`
skips the permission system for built-in file writes and network tools, and
`niwa dispatch` has no containment equivalent to watch's sandbox mode. This
design doesn't change that gap, or how many workers are exposed to it.
Containment for dispatched workers remains a separate follow-up.

## Consequences

### Positive

- Sessions in a `bypass` workspace keep the developer's own posture instead of
  being forced to `default`, and dispatched workers still run unattended.
- `ask` scopes get the prompting they declared, and the hooks, deny rules, and
  plugins that the invalid value was silently voiding take effect again. That
  includes the containment settings `niwa watch` merges into the instance
  root for hard-deny reviews.
- No settings file can change what dispatch forwards, and the file no longer
  shows a setting that governs nothing.
- Watch's empty permission mode is explicit at its call sites and pinned by a
  test.
- Existing instances repair themselves on the next apply, with no migration.

### Negative

- One more field on persisted instance state, and a second place, after the
  instance-root document, where the resolved posture is visible.
- A state file that can't be read withholds the derived flag, as a settings
  read failure did before, so a corrupted `instance.json` costs a worker its
  bypass.
- Hard-deny and sandbox-off `niwa watch` reviews in a `bypass` workspace run in
  the developer's own mode instead of the `default` they got by accident.
- The functional suite grows by a nine-example Scenario Outline and several
  dispatch scenarios.
- Sessions a developer starts on Claude Code older than 2.1.257 lose the
  file-borne bypass they had.

### Mitigations

- The recorded posture and the instance-root document read the same config
  map, and a test across S1-S9 pins their agreement. The field records niwa's
  vocabulary rather than a Claude mode.
- A read or parse failure prints a stderr warning naming the state file, so a
  lost bypass is visible rather than silent.
- Watch's sandboxed boundary (the no-egress sandbox and the egress and
  filesystem guard hooks) applies under any mode. A follow-up can make watch's
  settings verification reject any mode Claude Code doesn't recognize, so a
  future bad value fails the launch instead of silently dropping containment.
- Only S1 of the matrix is `@critical`, so the critical lane's runtime barely
  moves.
- The ephemeral-sessions guide names `--permission-mode` as the route for
  developers who want bypass in sessions they start themselves.
