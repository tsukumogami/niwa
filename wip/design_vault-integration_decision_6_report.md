# Decision 6: Shadow Diagnostic Integration

Where R31 shadow detection is computed in the pipeline, and how one
detection result is shared across `niwa apply` stderr, `niwa status`'s
summary line, and `niwa status --audit-secrets`'s flag column.

Decision 1 fixed the pipeline as `parse -> resolve -> merge ->
materialize`. This decision picks the location and shape of a
post-merge shadow detector whose output is both emitted live on apply
and persisted into `state.json` so that `niwa status` (which the PRD
requires to be fully offline and hash-based) can surface shadows
without re-loading team/personal configs.

## Options Evaluated

### Option 1: Compute in `MergeGlobalOverride`; return as a second value

Change the signature from
`MergeGlobalOverride(ws, g, dir) -> *WorkspaceConfig`
to
`MergeGlobalOverride(ws, g, dir) -> (*WorkspaceConfig, []Shadow)`.
Every shadow is detected as a side effect of the "global wins per
key" branches already in `override.go`. Apply reads the slice and
emits the stderr diagnostic; it persists the slice in `state.json`
so status can read it.

Trade-offs:

- Detection sits exactly where the override is applied, so there is
  no duplication of the "which keys collide" logic — it is literally
  the same map iteration.
- Breaks every caller of `MergeGlobalOverride`, including
  `runPipeline` (apply.go:223) and ten direct test callers in
  `override_test.go` (lines 606, 635, 653, 678, 694, 721, 735, 749,
  763, plus a mutation-safety test). Every test needs a second
  return. That violates the "no regression in MergeGlobalOverride
  tests" constraint unless we accept the per-test migration cost.
- Couples a diagnostic concern (R31) to a pure reduction function
  (`MergeGlobalOverride`). The merge has always been a pure "last
  writer wins" fold; this tees a diagnostic channel through it.
- Does not cover R12 provider-name collision, which is a hard error
  raised at resolve time (Decision 1's resolver), before
  `MergeGlobalOverride` ever runs. A second detector would still be
  needed for providers.
- The collision sites inside `MergeGlobalOverride` are value-level
  (both `g.Env.Vars[k]` and `merged.Env.Vars[k]` are set); the
  function has no access to the source-file paths the user needs in
  the diagnostic. Passing those paths in as arguments pushes file-
  provenance into a pure-data merge function.

### Option 2: Compute post-merge via a visitor walk

Keep `MergeGlobalOverride` unchanged. Add a new function
`DetectShadows(team, personal *config.WorkspaceConfig,
overlay *config.GlobalOverride) []Shadow` that walks the pre-merge
team and overlay structs, emits a `Shadow` for every key or provider
name the overlay redeclares, and returns the list. Shadows are a
pure function of inputs the caller already holds at the merge call
site.

Shape at the call site (apply.go:211-227 region):

```go
resolved := workspace.ResolveGlobalOverride(globalOverride, cfg.Workspace.Name)
shadows := workspace.DetectShadows(cfg, globalOverride, resolved)
effectiveCfg := workspace.MergeGlobalOverride(cfg, resolved, a.GlobalConfigDir)
```

For R12 (personal-overlay provider with same name as team provider):
the resolver in Decision 1 sees both registries in its two-pass walk.
It can emit the Shadow record BEFORE raising the R12 hard error so
the user sees both (per the PRD's explicit ordering requirement:
"shadow detection should fire before the R12 error"). The resolver
exposes a hook — `DetectProviderShadows(team *config.WorkspaceConfig,
overlay *config.GlobalConfigOverride) []Shadow` — that apply calls
between parse and the R12 check.

Trade-offs:

- `MergeGlobalOverride` signature unchanged. All ten existing tests
  pass untouched, satisfying the stated constraint.
- Shadow detection is a pure function of team + overlay configs, so
  it is independently testable without running the merge at all.
- Detection is redundant with the merge's internal map iteration,
  but the redundancy is narrow and well-scoped — a few dozen lines
  of map comparison, not a re-implementation of merge semantics.
- File provenance (team source file, personal source file) flows
  naturally because the detector takes the parsed configs plus their
  origin file paths from the caller. The merge function stays
  provenance-free.
- R12 provider case and env-vars case are detected by different
  callers (resolver for providers, post-merge walk for env), but
  both produce the same `Shadow` record type that flows into the
  same `[]Shadow` slice apply persists.

### Option 3: Track during merge via a side-channel

Change the signature to
`MergeGlobalOverride(ws, g, dir, *ShadowTracker) -> *WorkspaceConfig`
where `ShadowTracker` is an optional accumulator. When `nil`, merge
behaves identically to today. Callers that want diagnostics pass a
non-nil tracker.

Trade-offs:

- Preserves backwards compatibility for existing callers if they
  pass `nil`. Test suite passes unchanged if fixtures are updated to
  add `nil` — still a 10-file touch, just less semantically
  disruptive than Option 1.
- Mutable argument is un-Go-idiomatic for what is otherwise a pure
  function. Introduces an aliasing / race-safety surface the merge
  does not have today.
- Still couples the merge function to a diagnostic concern, just
  hides the coupling behind an opt-in argument. The concern leaks
  into the function's signature permanently.
- Does not solve the R12 provider-shadow case (which fires in the
  resolver, not the merge).

### Option 4: Compute twice — apply re-detects, status re-detects

Apply keeps both pre-merge snapshots in memory, runs
`DetectShadows(team, overlay, merged)` for its stderr output, and
discards the result. For `niwa status`, persist the minimal
`[]Shadow` list in `state.json` at apply time. Status reads only
from state, never re-loads configs.

This is effectively Option 2 + "persist the output" — and since we
have to persist anyway to keep `niwa status` offline, this is a
strict cost increase over Option 2 only if you read it as "compute
twice." In the chosen design we compute ONCE in apply, persist the
result, and status reads from state. That collapses Option 4 into
Option 2.

### Option 5: Merge returns a typed `MergeReport` bundle

Variant on Option 1 where the return type is a struct:
`MergeGlobalOverride(...) *MergeReport` where
`MergeReport{Config *WorkspaceConfig; Shadows []Shadow; Warnings []string}`.
Lets future diagnostics (drift warnings, deprecation notices)
piggy-back on the same return channel.

Trade-offs:

- Future-proof, but over-engineers for a single consumer today. The
  constraint explicitly prohibits changing the signature, so this
  loses on day one.
- Same coupling concern as Option 1 — merge-as-pure-reduction
  becomes merge-as-diagnostic-producer.

## Chosen

**Option 2: Compute post-merge via a visitor walk, persist to
state.json, `niwa status` reads from state.**

Concrete pipeline placement:

```
parse (config.Load)
 -> resolve (vault.ResolveWorkspace / ResolveGlobalOverride)
   -> DetectProviderShadows(team, overlay)     <-- R12 provider case
     -> R12 hard error raised after shadow emit
       -> workspace.ResolveGlobalOverride       <-- flatten overlay
         -> DetectShadows(team, overlay, flat)  <-- env.vars + files case
           -> workspace.MergeGlobalOverride     <-- unchanged signature
             -> persist shadows into state.json
               -> emit stderr diagnostic for each shadow
```

Shadow records are the single source of truth: computed once in
apply, emitted live to stderr, persisted to `state.json` under a new
top-level `shadows` field. `niwa status` (offline, hash-based) reads
the persisted list to print the summary line and the
`--audit-secrets` column — no config re-load.

## Rationale

Option 2 wins because it is the only option that keeps
`MergeGlobalOverride`'s signature intact (constraint stated
explicitly) and keeps the merge function a pure reduction (it is
referenced from several call paths and is the load-bearing merge
primitive for Decision 1's pipeline). Detection becomes a pure
function over pre-merge inputs, independently testable without
merge fixtures and without provider mocks. The detector runs at the
same call site that already holds `cfg`, `globalOverride`, and
`resolved` (apply.go:222-223), so the new code lands in one place.
Persisting the result to `state.json` is the only viable path for
the offline-by-default `niwa status` requirement; recomputing in
status would need both configs on disk, violating the offline
contract. R22 redaction is satisfied by construction: `Shadow`
records contain names and layer labels only, never values, so the
stderr, state, and status surfaces all print safe strings.

## Rejected

- **Option 1 (return as second value).** Rejected: breaks the stated
  constraint on `MergeGlobalOverride` signature and forces ten
  existing tests plus apply.go to migrate. Merge function gains a
  diagnostic concern it should not own. Does not address R12
  provider shadows (resolver-layer concern).
- **Option 3 (side-channel tracker).** Rejected: still changes the
  merge signature; introduces a mutable-argument surface to a pure
  function; does not address the R12 case; offers no benefit over
  Option 2 once you accept that detection is a pure function of
  pre-merge inputs.
- **Option 4 (double detection).** Rejected as stated. Collapses
  into Option 2 once you observe that status must be offline, so
  persistence is required regardless, and recomputing in status
  requires both configs on disk. Option 2 computes once, persists
  once, reads once.
- **Option 5 (typed `MergeReport`).** Rejected: still breaks the
  signature constraint; over-engineers for a single consumer;
  future callers can adopt a separate `Report` struct without
  retrofitting the merge function.

## Shadow Detection API Sketch

New package: `internal/workspace` adds the detector alongside
`override.go`. A separate file (`shadow.go`) keeps the concern
isolated from the merge code.

```go
// Shadow records a single instance where the personal overlay
// redeclares a team-declared name. Values are never included.
// R22 redaction: every field is a name or a layer label, never a
// secret value.
type Shadow struct {
    // Kind classifies what was shadowed. Exactly one of:
    //   "provider"       - personal overlay declares a [vault.providers.<name>]
    //                      matching a team-declared name (R12 hard-error case;
    //                      this record is emitted before the error so the user
    //                      sees both).
    //   "env.var"        - [env.vars.<KEY>] redeclared.
    //   "env.secret"     - [env.secrets.<KEY>] redeclared.
    //   "claude.env.var" - [claude.env.vars.<KEY>] redeclared.
    //   "claude.env.secret" - [claude.env.secrets.<KEY>] redeclared.
    //   "files"          - [files] source key redeclared.
    //   "settings"       - [claude.settings.<key>] redeclared.
    //   "workspace-scoped.env.var" - [workspaces.<scope>.env.vars.<KEY>] redeclared.
    //   ... additional kinds for each scoped env table under
    //       [workspaces.<scope>.env.*].
    Kind string `json:"kind"`

    // Name is the provider name or the env/setting/file key that was
    // shadowed. Never a value. For scoped keys, Name is the unqualified
    // key; Scope carries the workspace scope label.
    Name string `json:"name"`

    // Scope is the [workspaces.<scope>] label when the shadow was
    // declared under a scoped table. Empty for flat [global] shadows.
    Scope string `json:"scope,omitempty"`

    // TeamSource is the file path (relative to the workspace config dir)
    // that declared the team value. Used in the stderr diagnostic so
    // users can open the right file.
    TeamSource string `json:"team_source"`

    // PersonalSource is the file path (relative to the global config
    // dir) that declared the shadowing value. Usually
    // "<global-config-dir>/niwa.toml".
    PersonalSource string `json:"personal_source"`

    // Layer is always "personal-overlay" in v1. Reserved for future
    // layers (e.g., "instance-override") without schema churn.
    Layer string `json:"layer"`
}

// DetectShadows returns the env/files/settings shadows between a
// parsed team config and the resolved (flattened) personal overlay.
// It does NOT detect provider-name shadows — those fire earlier in
// the resolver via DetectProviderShadows so the R12 error can
// reference them.
//
// Inputs:
//   team         - the parsed team workspace config (pre-merge).
//   resolved     - the flattened GlobalOverride returned by
//                  workspace.ResolveGlobalOverride, carrying the
//                  effective personal-overlay values for this
//                  workspace scope.
//   overlay      - the raw GlobalConfigOverride (needed for scope
//                  attribution when a key was declared under
//                  [workspaces.<scope>]).
//   teamSource   - path of the team's workspace.toml for diagnostics.
//   personalSource - path of the personal overlay's niwa.toml for
//                    diagnostics.
//
// Pure function; no IO.
func DetectShadows(
    team *config.WorkspaceConfig,
    overlay *config.GlobalConfigOverride,
    resolved config.GlobalOverride,
    teamSource string,
    personalSource string,
) []Shadow

// DetectProviderShadows returns provider-name shadows. Lives
// alongside the resolver (internal/vault) because it fires at
// resolve time, but emits the same Shadow record shape.
func DetectProviderShadows(
    team *config.WorkspaceConfig,
    overlay *config.GlobalConfigOverride,
    teamSource string,
    personalSource string,
) []Shadow
```

Shadows flow through apply unchanged:

```go
// apply.go (approximate)
var allShadows []Shadow

if a.GlobalConfigDir != "" && !opts.skipGlobal {
    // ... parse overlay ...

    // R12 provider shadows emit BEFORE the R12 error (if any).
    provShadows := vault.DetectProviderShadows(cfg, globalOverride,
        configPath, overridePath)
    allShadows = append(allShadows, provShadows...)
    emitShadowsStderr(os.Stderr, provShadows)
    if err := vault.CheckProviderCollision(provShadows); err != nil {
        return err // R12 hard error, after shadows are emitted.
    }

    resolved := workspace.ResolveGlobalOverride(globalOverride, cfg.Workspace.Name)
    envShadows := workspace.DetectShadows(cfg, globalOverride, resolved,
        configPath, overridePath)
    allShadows = append(allShadows, envShadows...)
    emitShadowsStderr(os.Stderr, envShadows)

    effectiveCfg = workspace.MergeGlobalOverride(cfg, resolved, a.GlobalConfigDir)
}

// ... state update ...
state.Shadows = allShadows
```

`emitShadowsStderr` formats one line per shadow (see Output
Examples).

## Output Examples

Three scenarios demonstrate the diagnostic shape.

### Scenario A: personal overlay redeclares a team env var

Team `workspace.toml`:
```toml
[env.vars]
GITHUB_TOKEN = "vault://team/pat"
NPM_TOKEN    = "vault://team/npm"
```

Personal overlay `niwa.toml`:
```toml
[global.env.vars]
GITHUB_TOKEN = "vault://personal/my-pat"
```

`niwa apply` stderr:
```
shadow env.var GITHUB_TOKEN: personal-overlay (/Users/me/.config/niwa/global/niwa.toml) shadows team (workspace.toml)
```

`niwa status` (summary view, added line after applied time):
```
Instance: main
Config:   myorg
Root:     /workspaces/myorg/main
Created:  2026-04-14 10:02
Applied:  2026-04-15 14:11

1 key shadowed by personal overlay (see niwa status --audit-secrets)
```

`niwa status --audit-secrets` (new column "shadowed"):
```
KEY              NAMESPACE     CLASS       SHADOWED
GITHUB_TOKEN     env.vars      vault-ref   yes (personal-overlay)
NPM_TOKEN        env.vars      vault-ref   no
```

### Scenario B: personal overlay declares a provider matching team

Team `workspace.toml`:
```toml
[vault.providers.team-infisical]
kind = "infisical"
```

Personal overlay:
```toml
[global.vault.providers.team-infisical]
kind = "infisical"
```

`niwa apply` stderr (shadow diagnostic prints BEFORE the R12 error):
```
shadow provider team-infisical: personal-overlay (/Users/me/.config/niwa/global/niwa.toml) shadows team (workspace.toml)
error: personal overlay cannot override team-declared provider `team-infisical` — use per-key overrides in [env.secrets] instead.
```

Apply exits non-zero, so nothing persists to state and status shows
the previous apply's state.

### Scenario C: three scoped shadows at once

Team:
```toml
[env.vars]
AWS_REGION = "us-east-1"

[claude.env.vars]
ANTHROPIC_BASE = "https://api.anthropic.com"
```

Personal overlay:
```toml
[workspaces.myorg.env.vars]
AWS_REGION = "eu-west-1"

[workspaces.myorg.claude.env.vars]
ANTHROPIC_BASE = "https://proxy.example"

[workspaces.myorg.files]
"context/team.md" = "context/mine.md"
```

`niwa apply` stderr (one line per shadow):
```
shadow env.var AWS_REGION (scope=myorg): personal-overlay (~/.config/niwa/global/niwa.toml) shadows team (workspace.toml)
shadow claude.env.var ANTHROPIC_BASE (scope=myorg): personal-overlay (~/.config/niwa/global/niwa.toml) shadows team (workspace.toml)
shadow files context/team.md (scope=myorg): personal-overlay (~/.config/niwa/global/niwa.toml) shadows team (workspace.toml)
```

`niwa status`:
```
3 keys shadowed by personal overlay (see niwa status --audit-secrets)
```

`niwa status --audit-secrets`:
```
KEY              NAMESPACE           CLASS       SHADOWED
AWS_REGION       env.vars            plaintext   yes (personal-overlay, scope=myorg)
ANTHROPIC_BASE   claude.env.vars     plaintext   yes (personal-overlay, scope=myorg)
```
(Files shadows don't appear in `--audit-secrets` because that
subcommand enumerates env tables only, per R13. The summary line's
count of 3 still includes the files shadow; the count is "keys
shadowed overall," not "secret keys only.")

Format rules:
- Stderr line: `shadow <kind> <name>[ (scope=<scope>)]: <layer> (<personal-source>) shadows team (<team-source>)`
- Summary line: `<N> key[s] shadowed by personal overlay (see niwa status --audit-secrets)` — omitted entirely when N=0 so users with no shadows see no extra line.
- Audit column: `yes (personal-overlay[, scope=<scope>])` or `no`.

## State Persistence Design

Add a `shadows` field to `InstanceState` in `internal/workspace/state.go`:

```go
type InstanceState struct {
    SchemaVersion  int                  `json:"schema_version"`
    // ... existing fields ...
    Shadows        []Shadow             `json:"shadows,omitempty"`
}
```

Shape in `state.json`:

```json
{
  "schema_version": 2,
  "instance_name": "main",
  "shadows": [
    {
      "kind": "env.var",
      "name": "GITHUB_TOKEN",
      "team_source": "workspace.toml",
      "personal_source": "/Users/me/.config/niwa/global/niwa.toml",
      "layer": "personal-overlay"
    },
    {
      "kind": "claude.env.var",
      "name": "ANTHROPIC_BASE",
      "scope": "myorg",
      "team_source": "workspace.toml",
      "personal_source": "/Users/me/.config/niwa/global/niwa.toml",
      "layer": "personal-overlay"
    }
  ]
}
```

Schema version bumps from 1 to 2. A state file loaded with
`schema_version=1` has `Shadows == nil`, which status renders as
zero shadows — safe default for existing instances before the first
post-upgrade apply.

`niwa status` reads `state.Shadows` and:
- Summary view: counts non-empty slice, prints the summary line.
- Detail view: inserts the summary line between the Applied time
  and the Repos section.
- `--audit-secrets`: builds a set of shadowed key names (indexed by
  `kind` + `name` + `scope`) from the slice; when enumerating
  `*.secrets` and `*.vars` tables (per R13), it stamps the
  `SHADOWED` column from that set.

Shadows never carry version tokens or content hashes. They are a
declarative record of "at last apply, these names collided" and
become stale only at the next apply — the same staleness model as
the rest of `state.json`.

## Open Items for Phase 3 Cross-Validation

1. **Decision 1 (pipeline ordering) — detector placement consistency.**
   This decision places `DetectShadows` immediately after
   `ResolveGlobalOverride` and before `MergeGlobalOverride`, which
   matches Decision 1's `parse -> resolve -> merge -> materialize`
   layering. If Decision 1 ends up moving merge behind a typed
   `MaybeSecret` transformation that re-shapes `GlobalOverride`,
   the detector's input types adapt trivially (it walks map keys,
   which are strings under both representations), but the
   `teamSource`/`personalSource` plumbing needs to survive the
   type migration. Verify the resolver threads through file-path
   metadata that the detector can attach.

2. **Decision 2 (`secret.Value` redaction) — Shadow R22 compliance.**
   `Shadow` struct fields are all strings (names, paths, labels).
   None is a `secret.Value`. Because `fmt.Sprintf` on a
   `Shadow` will print every field verbatim, the type must never
   gain a `Value` field. Add a compile-time trap: `Shadow` embeds
   nothing and has no `secret.Value`-typed field. The acceptance
   test for R22 should include "print a Shadow slice to stderr; no
   secret bytes appear."

3. **Decision 5 (public-repo guardrail) — ordering with R12.** The
   R12 hard error fires during apply after provider shadows emit.
   The public-repo guardrail (R14/R30) also raises a hard error at
   apply time. If both conditions hold, the ordering matters for
   UX: user sees provider shadow, then guardrail error, then R12
   error — confusing. Recommend: run provider shadow emit, then
   R14 guardrail, then R12 error, in that order; all three before
   any cloning or materialization. Confirm with Decision 5's
   placement.

4. **Scope-aware detection and `vault_scope` override.** This
   decision assumes the detector uses the same `workspaceName ->
   scope` resolution as `ResolveGlobalOverride`. If the team config
   sets `[workspace].vault_scope` to override the default
   `[[sources]][0].org`, the detector must resolve to the same
   scope — otherwise scoped shadows are either missed or reported
   under the wrong label. Verify the scope resolver is factored
   into a single function the detector can reuse.

5. **State schema migration.** Bumping `SchemaVersion` from 1 to 2
   needs a compatibility plan. Options: (a) load both versions,
   treat missing `shadows` as empty; (b) force re-apply on upgrade.
   This decision assumes (a). Cross-check with any state-schema
   versioning decision.

6. **R13 audit table coverage.** The `SHADOWED` column is defined
   only for env rows. Files and settings shadows appear in the
   summary count but have no row in `--audit-secrets` output.
   Confirm with R13's acceptance criteria that the audit table
   scope stays env-only (files/settings are non-secret by
   declaration, so their shadow visibility lives in the summary
   line and the stderr diagnostic only). The ACs at line 1026 read
   "flags every shadowed team key," which is ambiguous on whether
   non-env shadows need a row. Phase 3 should clarify with the PRD
   or narrow the detector to env-only with a separate non-env
   diagnostic channel.
