---
status: Proposed
problem: |
  niwa's .env.example pre-pass aborts apply on any probable-secret detection,
  with only coarse all-or-nothing controls. The accepted PRD requires a
  configurable response: warn by default, opt-in failures, set per detection
  category at user, project, and variable granularity with most-specific-wins
  precedence, plus a per-run override and removal of the remote-visibility
  special case.
decision: |
  Add an [env_example_policy] table at three config positions -- a net-new layer
  in the personal global override, plus the workspace and per-repo structs that
  already hold read_env_example -- resolved by a most-specific-wins cascade.
  classifyEnvValue returns a typed detection category; parseDotEnvExample also
  returns per-key inline annotations extracted independently of value quoting.
  The resolved global policy is threaded into MaterializeContext. The pre-pass
  resolves a warn/fail action per key+category, applies it, drops the
  public-remote branch, and --allow-plaintext-secrets downgrades all pre-pass
  failures to warnings for one run.
rationale: |
  The workspace/per-repo cascade mirrors EffectiveReadEnvExample, but the
  user/global rung is net-new: read_env_example has no global layer today, so
  this adds both a GlobalOverride field and the plumbing to carry it to the
  pre-pass. A typed category replaces fragile reason-string parsing. Keeping the
  bypass on the existing flag avoids proliferation, at the cost of a widened
  blast radius that the design makes auditable.
upstream: docs/prds/PRD-env-example-failure-policy.md
---

# DESIGN: env-example failure policy

## Status

Proposed

## Context and Problem Statement

The `.env.example` pre-pass (`internal/workspace/env_example_prepass.go`) reads
each repo's `.env.example`, classifies every value via
`classifyEnvValue` (`internal/workspace/envclassify.go`), and on an undeclared
probable secret accumulates an error that aborts `apply`. Today the only response
is a hard fail, and the only knobs are the all-or-nothing `read_env_example`
toggle (`WorkspaceMeta.ReadEnvExample` / `RepoOverride.ReadEnvExample`, resolved
by `EffectiveReadEnvExample` -- a repo -> workspace -> default cascade with **no
global layer**) plus a public-remote-scoped bypass (`AllowPlaintextSecrets`).

The accepted PRD (`docs/prds/PRD-env-example-failure-policy.md`) requires a
configurable failure response. This design decides how that policy is expressed,
where it lives, how it resolves, and how the pre-pass consumes it -- without
changing the detection heuristics themselves.

## Decision Drivers

- **Match the workspace/per-repo cascade where it exists.** niwa resolves
  `read_env_example` through a per-repo -> workspace -> default `*bool` cascade.
  The category policy follows the same `*`-pointer idiom at those two rungs.
- **The user rung is net-new.** `read_env_example` has no personal/global layer
  and `GlobalOverride` (`config.go:440`) carries no such field. The PRD's user
  level (R3) requires a new `GlobalOverride` field and new plumbing to carry the
  resolved global policy to the pre-pass; this is not reuse and the design treats
  it as net work.
- **Per-category granularity is required** (PRD R2): vendor-token and entropy
  detections resolve independently.
- **Testability.** Resolution is a pure function unit-testable in isolation.
- **No secret bytes in diagnostics** (PRD R10) -- preserve the current R22
  guarantee across the refactor and every new warn/fail/annotation path.
- **Auditability over a hidden blast radius.** Where a control is broadened
  (the bypass flag) or where a repo-supplied input can lower a configured floor
  (inline annotations), the effect must be greppable in output, not silent.

## Considered Options

### Decision 1: Where and how the policy is expressed

- **Chosen: a dedicated `[env_example_policy]` table at three config positions.**
  Category keys plus, at project scope only, a per-variable sub-table:

  ```toml
  # workspace.toml (project, workspace-wide) and ~/.config/niwa/config.toml
  # under [global] / [workspaces.<name>] (user) -- category keys:
  [env_example_policy]
  vendor_token = "fail"   # warn | fail
  entropy = "warn"

  # project scope only (workspace top level or [repos.<name>.env_example_policy]):
  [env_example_policy.vars]
  STRIPE_EXAMPLE_KEY = "warn"
  ```

  A new `EnvExamplePolicy` field is added to `WorkspaceMeta` and `RepoOverride`
  (alongside `ReadEnvExample`) and, **net-new**, to `GlobalOverride`. The global
  position carries category keys only; per-variable operator config lives at the
  workspace and per-repo positions, matching PRD R5 ("the operator's workspace
  config").

- *Alternative -- fold into the existing `[env]` table.* Rejected: `[env]`
  governs variable values and promotion; failure policy is orthogonal.
- *Alternative -- a single scalar `env_example_failure = "warn|fail"`.* Rejected:
  no per-category (R2) or per-variable control.
- *Alternative -- make `read_env_example` tri-state (`off|warn|fail`).* Rejected:
  conflates the whole-scan toggle (kept as the "stop looking" knob, R8) with the
  response policy and loses per-category and per-variable control.

### Decision 2: Inline annotation syntax and extraction

- **Chosen: a trailing line comment**, `KEY=value # niwa: warn` (and
  `# niwa: fail`). Marker extraction is a **distinct step performed independently
  of value parsing**, so it works across all three dotenv value forms (unquoted,
  single-quoted, double-quoted). The current parser strips trailing comments only
  for unquoted values (`env_example.go` `parseUnquoted`), so the design adds a
  dedicated extraction pass that finds a trailing ` # niwa: <action>` token after
  the value region; a `# niwa:` sequence inside a quoted value is not treated as a
  marker.
- *Alternative -- a preceding `# niwa: warn` comment line.* Rejected: weaker
  key binding and more ambiguity.
- *Alternative -- a sidecar file or sentinel value.* Rejected: duplicates the
  file, or collides with real placeholder content.

### Decision 3: Detection category representation

- **Chosen: `classifyEnvValue` returns a typed category** (a small enum:
  vendor-token, entropy, safe). The pre-pass keys policy lookup on the category,
  and the new warn/fail diagnostics print the **category name**, not the matched
  vendor prefix, so no value-derived bytes appear in output.
- *Alternative -- parse the existing reason string.* Rejected: fragile coupling
  of control flow to diagnostic text.

### Decision 4: The per-run override, its blast radius, and the dual guardrail

`--allow-plaintext-secrets` is exposed by **both** CLI commands
(`internal/cli/apply.go:133` and `internal/cli/create.go:160`), threaded to
`Applier` then `MaterializeContext`. It currently gates two things: the pre-pass
public-remote branch (being removed) and the separate
`guardrail.CheckGitHubPublicRemoteSecrets` call
(`internal/workspace/apply.go:1099`).

- **Chosen: keep the single flag, broaden its pre-pass meaning** to "downgrade
  every pre-pass `fail` to `warn` for this run," and make each downgrade emit a
  per-key audit diagnostic so the broadened effect is greppable. Its role gating
  `CheckGitHubPublicRemoteSecrets` is unchanged and out of scope (that guardrail
  calls `EnumerateGitHubRemotes` itself, so removing the pre-pass's call does not
  break it).
- *Alternative -- a new dedicated `--env-warn-only` flag.* Rejected for now to
  avoid proliferation, but noted: the flag's blast radius is now run-wide across
  all repos, so a future split is a reasonable follow-up if operators find the
  coupling surprising. The audit diagnostics are the interim mitigation.

## Decision Outcome

The policy is a new `[env_example_policy]` block recognized at the personal
global (category keys, net-new), workspace, and per-repo positions, plus inline
`# niwa: warn|fail` annotations. A pure resolver returns the effective action for
a given key and category by walking, most-specific first:

1. operator per-variable entry (per-repo `vars`, then workspace `vars`),
2. inline annotation for that key,
3. per-category policy (per-repo, then workspace, then global),
4. default `warn`.

`classifyEnvValue` returns a typed category; the pre-pass resolves each
undeclared key's action, prints a value-free diagnostic naming the key and
category, and either accumulates a failure (`fail`) or proceeds (`warn`). When an
inline annotation lowers an otherwise-configured `fail` to `warn`, a distinct,
greppable diagnostic is emitted, and an operator `ignore_inline_annotations`
switch (workspace/global) disables inline annotations entirely for operators who
do not want repo-supplied exemptions honored. The public-remote branch is
removed. `--allow-plaintext-secrets` downgrades all resolved failures to warnings
with per-key audit output.

This works as a whole because it reuses the workspace/per-repo cascade idiom,
adds the user rung explicitly where none existed, isolates the behavior change to
the pre-pass, and keeps every broadened or repo-influenced control auditable.

## Solution Architecture

**Config (`internal/config`):**

- New `Action` enum (`warn|fail`) and `EnvExamplePolicy` struct:
  `VendorToken *Action`, `Entropy *Action`, and (project positions only)
  `Vars map[string]Action`. `nil` pointer means unset/inherit, matching the
  `*bool` idiom.
- Add an `EnvExamplePolicy` field to `WorkspaceMeta`, `RepoOverride`, and
  `GlobalOverride`. The `GlobalOverride` addition also requires updating the
  global-override deep-copy (`internal/vault/resolve/deepcopy.go`
  `deepCopyGlobalOverride`) and any per-field global-override merge, since these
  are hand-written, not reflective.
- New resolver `EffectiveEnvExamplePolicy(globalPolicy *EnvExamplePolicy, ws
  *WorkspaceConfig, repoName, key string, category EnvDetectionCategory, inline
  *Action) Action` implementing the cascade above. `globalPolicy` is passed
  explicitly because it is not part of `WorkspaceConfig`.

**Classification (`internal/workspace/envclassify.go`):**

- `classifyEnvValue` returns `(category EnvDetectionCategory, reason string)`.
  Sole production caller is the pre-pass; update it and the `_test.go` callers.

**Parsing (`internal/workspace/env_example.go`):**

- `parseDotEnvExample` gains a per-key annotation return (e.g.
  `map[string]Action`) from a quoting-independent marker-extraction pass.
  Unknown markers emit a warning that names the key only and never echoes the
  marker payload, then are ignored. An annotation on a declared or excluded key
  is a no-op.

**Pre-pass (`internal/workspace/env_example_prepass.go`):**

- Remove the `EnumerateGitHubRemotes`/`publicRemotes`/`haveGit` branch entirely.
- For each undeclared, non-excluded key: classify -> resolve action via
  `EffectiveEnvExamplePolicy` (passing the inline annotation, honoring
  `ignore_inline_annotations`) -> if `AllowPlaintextSecrets`, force `warn` and
  emit the audit diagnostic -> emit the value-free key+category diagnostic ->
  on `fail` accumulate an error, on `warn` continue. Failure aggregation and the
  final non-zero exit keep their current shape.

**Plumbing (`internal/workspace/materialize.go`, `apply.go`):**

- Add a resolved-global-policy field to `MaterializeContext` (it currently
  carries only `*WorkspaceConfig` and `RepoName`). Populate it where the Applier
  builds the materialize context, from the loaded `GlobalConfig` override for the
  active workspace. Both `apply` and `create` already construct the Applier, so
  the population happens once on the shared path.

**Data flow:** `apply`/`create` -> `Applier` (loads global override policy) ->
`MaterializeContext` (workspace config + repo name + resolved global policy +
`AllowPlaintextSecrets`) -> pre-pass parses values + annotations -> per-key
classify + resolve -> warn/fail.

## Implementation Approach

1. **Config types + resolver.** Add `Action`, `EnvExamplePolicy`, the struct
   fields (incl. `GlobalOverride` + its deep-copy/merge), and
   `EffectiveEnvExamplePolicy`; unit-test every precedence rung, inheritance
   fall-through, inline-vs-config, `ignore_inline_annotations`, and default warn.
2. **Category enum.** Change `classifyEnvValue` to return the typed category;
   update its caller and tests.
3. **Inline annotation parsing.** Add the quoting-independent extraction pass to
   `parseDotEnvExample`; unit-test all three value forms, unknown-marker warning
   (no payload echo), spoofed `# niwa:` inside quoted values, and no-op on
   declared/excluded keys.
4. **Pre-pass rewire + plumbing.** Resolve per key+category, apply warn/fail,
   honor the flag downgrade with audit output, remove the public-remote branch,
   thread the global policy through `MaterializeContext`; update pre-pass tests.
5. **One-time upgrade notice.** Because the default flips from fail to warn,
   existing CI relying on a non-zero exit silently passes after upgrade. Add a
   one-time notice (mechanism in `docs/guides/one-time-notices.md`) announcing
   warn-by-default and how to restore failing, shown once per instance.
6. **Functional tests.** Add tagged Gherkin scenarios in
   `test/functional/features/` covering every PRD acceptance criterion (warn
   default, per-category fail, the three precedence levels, inline-vs-config
   override, per-run downgrade, remote-visibility removal, scan-disabled, and a
   value-bytes grep over stderr for no-secret-in-output).
7. **Docs.** Document the policy block, inline annotation, the
   `ignore_inline_annotations` switch, and the warn-by-default change in the
   relevant config/contributor guide(s).

## Security Considerations

- **Default protection drops (accepted, PRD-sanctioned).** Flipping to `warn`
  means a real secret in a public repo's `.env.example` no longer blocks `apply`
  by default; the highest-severity instance is a live vendor token in a public
  repo, which was the only remaining fail-closed floor. Mitigations: the warning
  still fires every apply; an operator restores blocking with a one-line category
  policy; and the one-time upgrade notice (step 5) surfaces the change so it is
  not discovered silently.
- **Inline annotations are repo-controlled and outrank operator category policy.**
  Most-specific-wins means a repo's inline `# niwa: warn` on a key overrides an
  operator's category-level `fail`. The PRD/BRIEF settled this (the operator wins
  only by setting a per-variable entry), which leaves a supply-chain gap: a repo
  can exempt a key the operator never inspects, amplified by warn-by-default alarm
  fatigue. Documentation alone is not a control, so the design adds two:
  (a) a distinct, greppable diagnostic whenever an inline annotation lowers a
  configured `fail` to `warn`, and (b) an operator `ignore_inline_annotations`
  switch (workspace/global) that disregards all inline annotations. The
  authority-over-specificity alternative (operator `fail` is a floor inline cannot
  lower) was rejected as contradicting the settled most-specific-wins rule; the
  switch gives operators that posture explicitly instead. **This switch is a
  design-introduced control beyond the PRD's requirements -- flagged for review.**
- **No secret bytes in diagnostics (R10/R22).** The new paths -- the warn
  diagnostic, the downgrade audit line, and the unknown-marker warning -- are all
  new string producers. The design binds them to the existing guarantee: they
  print the key name and the category enum only, never the value, a value
  fragment, the matched vendor prefix, the entropy score, or the raw marker
  payload. A value-bytes grep over stderr (step 6) enforces it. (This also
  resolves the latent question of whether the current `"known prefix sk_live_"`
  reason is a "fragment": the new paths print the category, not the prefix.)
- **Bypass blast radius widened.** `--allow-plaintext-secrets` now downgrades all
  pre-pass failures across every repo for the run, while still gating
  `CheckGitHubPublicRemoteSecrets`. An operator passing it for one legitimate
  config-repo secret silently downgrades genuine detections everywhere. Mitigation:
  the per-key audit diagnostic makes each downgrade visible; the Considered
  Options note flags a future flag split if the coupling proves surprising.

## Consequences

**Positive:**

- Placeholder values stop blocking `apply`; the response is proportionate and
  configurable per category, level, and variable.
- One cascade idiom at the workspace/per-repo rungs keeps the model familiar.
- Removing the public-remote branch deletes a network-dependent, hard-to-test
  code path from the pre-pass.
- Downgrades and inline-driven fail-lowering are auditable in output.

**Negative / trade-offs:**

- Weaker default posture (warn, not fail) until an operator opts in.
- Repo-controlled inline exemptions are trusted unless overridden per variable or
  disabled via `ignore_inline_annotations`.
- New net-new global-config plumbing (field, deep-copy, merge, MaterializeContext)
  and a new parse path in `.env.example`.
- The shared bypass flag now has a run-wide blast radius.

**Mitigations:**

- Loud per-key warnings; one-line opt-in to failing; per-variable override and
  the ignore-inline switch for repo-supplied exemptions; per-key audit output for
  bypass downgrades; a one-time upgrade notice for the default change.
