---
status: Proposed
upstream: docs/prds/PRD-worktree-env-provisioning.md
problem: |
  `niwa worktree create`/`apply` materialize a worktree's environment through a
  worktree-specific path that re-resolves secrets from their source at
  create/apply time. That path is a fork of the instance apply pipeline and
  fails where the instance does not (unreachable or wrong-org secret source,
  unassembled provider reference), even though the instance clone already holds
  a complete, correct, materialized environment for the same repo.
decision: |
  A worktree INHERITS the instance clone's already-materialized env output
  files by copying their bytes into the worktree's config-resolved target paths
  (no secret resolution, no network). `niwa apply` becomes the single refresh:
  after materializing each clone it fans out the same inherit step to every
  live worktree, skipping locked/detached/missing worktrees with a warning. The
  live-resolution fork is removed from the worktree path entirely.
rationale: |
  Env values carry no worktree- or instance-specific content, so a worktree's
  correct env is byte-identical to its clone's; copying opaque bytes makes that
  equivalence structural across every format and target, with no parser or
  round-trip risk. Routing the apply refresh through the same inherit step keeps
  one materialization definition shared by create, worktree apply, and instance
  apply, so clone and worktree cannot drift.
---

# DESIGN: worktree environment provisioning

## Status

Proposed

## Upstream Design Reference

Upstream PRD: `docs/prds/PRD-worktree-env-provisioning.md` (In Progress).
Relevant requirements: R1-R8 (functional), N1-N3 (non-functional). This design
implements the PRD's decided direction: worktree create inherits; `niwa apply`
is the single unified refresh; worktrees mirror their instance.

Related prior designs:
- `docs/designs/current/DESIGN-worktree-command-parity.md` -- established
  `ApplyToWorktree` and the shared `runRepoMaterializers`; its Decision C ran
  the env materializer against the worktree but did not settle inherit-vs-
  resolve. This design settles it.
- `docs/designs/current/DESIGN-secret-output-targets.md` -- the configurable
  secret-output target model (`config.EffectiveEnvOutput`) this design copies.

## Context and Problem Statement

After `niwa apply`, an instance clone of a repo at `<instanceRoot>/<group>/<repo>`
holds the fully-resolved, merged environment (plaintext vars + resolved
secrets) written to each configured secret-output target, at mode 0600 and
git-excluded (`internal/workspace/materialize.go`, `EnvMaterializer.Materialize`;
`secretFileMode = 0o600`).

A worktree is a second checkout of that repo under the same instance. Today
`applyContentToWorktree` (`internal/cli/session_lifecycle_cmd.go`) materializes
the worktree's env by re-running the instance-style resolution: two
`resolve.BuildBundle` calls (team + personal-overlay vault) plus
`ResolveAndMergeEffectiveConfig`, then `ApplyToWorktree` whose `EnvMaterializer`
re-renders the resolved values. That re-resolution depends on the secret source
being reachable and correctly authenticated at create time -- which the
instance already satisfied at apply time but which need not hold later. The
observed failures (an Infisical 403 from a fallback credential session; an
unassembled `vault://` provider reference) are both instances of this fork
diverging from the instance path.

The technical problem: a worktree must obtain an environment byte-equivalent to
its clone's, without resolving secrets, and `niwa apply` must keep existing
worktrees consistent with their clones -- through one materialization
definition, not a second forked one.

## Decision Drivers

- **PRD R1/R4/N1:** worktree create performs no secret resolution and no
  network access; it cannot fail on secret-source reachability or auth.
- **PRD R2/R3:** the worktree's env is byte-equivalent to the clone's for every
  configured target -- custom names, multiple targets, and non-dotenv (json,
  shell) formats included.
- **PRD R6/R7:** `niwa apply` refreshes every live worktree, tolerating
  locked/detached/missing ones with a warning rather than failing.
- **PRD N2:** one materialization definition shared across create, worktree
  apply, and instance apply, so clone and worktree cannot diverge.
- **PRD N3:** secrecy posture preserved -- 0600 permissions and git-exclude
  coverage match the clone; no new plaintext-at-rest surface.
- **Leaf-package discipline:** `internal/worktree` is a leaf; `internal/workspace`
  may import it for enumeration, never the reverse.

## Considered Options

### Decision 1 -- How a standalone worktree obtains its env without resolving

**Chosen: copy the clone's rendered output bytes (A1).** Resolve the target set
locally via `config.EffectiveEnvOutput(globalEnvOutput, cfg, repo)` (config
only, no vault); for each `ResolvedTarget`, copy the bytes from
`<instanceRoot>/<group>/<repo>/<target.Path>` to the worktree's containment-
checked target path, writing at 0600 and re-asserting git-exclude for custom
target names exactly as `ApplyToWorktree` already does.

*Rejected: read values back and re-render via `EnvMaterializer` (A2).* The only
available reader, `parseEnvFile`, is dotenv-only and lossy (trims whitespace,
splits on first `=`, drops non-`=` lines, cannot read json/shell). It cannot
round-trip non-dotenv targets -- a direct R3 failure -- and risks corrupting
whitespace/multiline values. Strictly worse than A1 while carrying the same
config dependency.

*Rejected: persist the resolved env in instance state, read from there (A3).*
Creates a new plaintext-secret-at-rest surface in `instance.json` (which is not
held to the 0600 env-file discipline) -- an N3 regression -- still needs a
lossy re-render for R2, and adds a second source of truth that can drift from
the clone file.

Copying opaque bytes makes R2 byte-equivalence structural (format-agnostic,
value-agnostic), gives the cleanest R8 signal (a missing clone target file is
exactly "nothing to inherit"), and removes the resolution fork most completely.

### Decision 2 -- How `niwa apply` propagates env to existing worktrees

**Chosen: clone-then-inherit fan-out (B2).** Leave the clone materializer loop
unchanged; add a step after it that enumerates live worktrees and runs the same
inherit step (Decision 1) per worktree, sourcing from the just-written clone.

*Rejected: single resolution, multiple target dirs (B1).* A parallel write
fan-out that re-pushes freshly-resolved values into worktrees. It does not reuse
the inherit path, so it creates a second definition of "a worktree's env" that
must stay in lockstep with create/apply -- the exact drift N2 forbids -- and
requires teaching `EnvMaterializer` to target multiple directories.

*Rejected: leave worktrees stale until manual `niwa worktree apply` (B3).*
Violates R6: after an apply a pre-existing worktree holds the old value while
the clone holds the new one.

B2 composes with Decision 1 (it *is* the inherit step, called per worktree),
satisfies R6 transitively (clone resolved once -> worktree inherits from clone),
and maps R7 onto the same enumeration + attach-availability projection
`niwa worktree list` already uses.

### Decision 3 -- Where the shared logic lives

**Chosen: the inherit step lives in `internal/workspace` (next to
`ApplyToWorktree`), and `niwa apply` calls it via the enumeration in the leaf
`internal/worktree`.** `internal/workspace` already imports `internal/worktree`
(per the parity design), so apply can enumerate worktrees and invoke the inherit
step with no import cycle. The CLI continues to orchestrate standalone create.

*Rejected: put the fan-out in the CLI layer.* The CLI's `applyContentToWorktree`
re-loads and would re-resolve config; apply already holds the resolved
`effectiveCfg`, `globalEnvOutput`, and `overlayDir` and should pass them
directly. Keeping the fan-out in the pipeline avoids a redundant config load and
keeps the single-resolution invariant.

## Decision Outcome

A single inherit primitive -- "copy the clone's resolved env output files into a
worktree's config-resolved targets, at 0600, git-excluded" -- is the one way a
worktree's env is produced. It is invoked by `niwa worktree create`, `niwa
worktree apply`, and (per repo, after clone materialization) by `niwa apply`.
The worktree path no longer builds vault bundles or resolves secrets. `niwa
apply` enumerates live worktrees from session state and refreshes each, skipping
edge states with a warning.

This satisfies R1/R4/N1 (no resolution, no network on the worktree path),
R2/R3 (byte copy of every configured target), R5 (worktree apply uses the same
inherit), R6 (apply refreshes worktrees from the just-resolved clone), R7
(skip-with-warning), R8 (missing clone target -> error pointing at apply), N2
(one materialization definition), and N3 (0600 + git-exclude preserved, no new
at-rest surface).

## Solution Architecture

### Components

- **Inherit primitive (new, `internal/workspace`).** A function roughly
  `inheritEnvOutputs(cloneRepoDir, worktreeDir string, cfg, repo, globalEnvOutput) ([]string, error)`:
  resolves targets via `config.EffectiveEnvOutput`; for each target, runs BOTH
  the clone source path AND the worktree dest path through the materializer's
  `safeTargetPath` containment guard (the target set comes from config, which
  `safeTargetPath`'s own doc treats as untrusted -- a crafted `../` or symlink
  `target.Path` must not read outside the clone nor write outside the worktree);
  stats the source; for custom (non-`*.local*`) target names it reproduces the
  `EnvMaterializer`'s fail-closed behavior -- refuse on a non-git tree
  (`IsGitRepo`) and assert git-exclude coverage BEFORE writing -- then mkdir
  parents 0700, copy bytes, write 0600. Returns written paths. Missing required
  source -> the R8 error. The exclude-before-write ordering and the IsGitRepo
  refusal are load-bearing: dropping `EnvMaterializer` must not drop them, or a
  custom-named secret file could land git-visible.

- **`ApplyToWorktree` (`internal/workspace/worktree_content.go`).** Drop
  `EnvMaterializer` from the worktree materializer set (mirroring how
  `runRepoMaterializers` already skips hooks/settings when claude is disabled);
  call the inherit primitive instead. Settings/files/hooks materializers and the
  worktree-context layer stay.

- **`applyContentToWorktree` (`internal/cli/session_lifecycle_cmd.go`).** Remove
  both `resolve.BuildBundle` calls and `ResolveAndMergeEffectiveConfig`. Keep the
  vault-free `mergeWorktreeOverlay`. Source the global env_output rung from the
  already-parsed global override (`loadGlobalConfigOverride`), which is plain
  config, not secret-bearing.

- **Apply worktree-refresh step (new, `internal/workspace/apply.go` `runPipeline`).**
  After the clone materializer loop, enumerate
  `worktree.ListSessionLifecycleStates(<instanceRoot>/.niwa/sessions)` and apply
  one explicit inclusion pipeline: `Status == active` AND repo present in
  `repoIndex` AND working dir present (`os.Stat`) AND not attached
  (`ReadAttachState(..., reapStale=false)`) AND git-registered (per-repo
  `git worktree list --porcelain` cross-check). A worktree passing all five is
  refreshed via the inherit step using the already-resolved `effectiveCfg`;
  any worktree failing a check after `Status == active` is skip-with-warning
  (R7). Note `reapStale=false` is a deliberate divergence from `niwa worktree
  list` (which passes `true`): apply must never reap another process's lock.
  The detached/missing/attached checks gate the WRITE, not merely the warning.
- **Managed-file forward-carry invariant (apply).** A skipped-but-live worktree
  writes nothing this run, so its prior env output paths would drop out of
  `result.managedFiles` and `cleanRemovedFiles` would delete a live worktree's
  secret file on the next apply. To prevent that data loss, the refresh step
  MUST re-add a skipped live worktree's existing managed entries (from
  `existingState.ManagedFiles`) into `result.managedFiles` without rewriting
  bytes -- a skip is inert for cleanup. Only a genuinely-absent worktree
  (session ended/destroyed) drops its entries so cleanup prunes them.

- **Worktree enumeration (`internal/worktree`, leaf, unchanged).**
  `ListSessionLifecycleStates` + `ReadAttachState` provide repo, path, status,
  and lock signal -- the same projection `niwa worktree list` uses.

### Data flow

`niwa worktree create <repo>`:
1. `worktree.CreateSession` -> git worktree add + session state.
2. `applyContentToWorktree` -> `mergeWorktreeOverlay` (no vault) -> `ApplyToWorktree`.
3. `ApplyToWorktree` runs settings/files/hooks materializers + the inherit
   primitive for env (copy clone targets) + rules import + worktree-context layer.
4. If a required clone env target is missing -> R8 error naming `niwa apply`.

`niwa apply`:
1. Resolve vault once; materialize each clone (unchanged).
2. New step: enumerate live worktrees; per worktree, skip-with-warning if
   missing/detached/locked, else run the inherit primitive sourcing the
   just-written clone targets.

### Edge-state handling (R7)

Each check gates the write (not just a warning) and skip-with-warning is never
fatal to the apply:

- **Missing** working dir (`os.Stat` ENOENT) -> warn + skip; forward-carry its
  prior managed entries only if the session is still active (a destroyed session
  drops them so cleanup prunes).
- **Locked** (`ReadAttachState == AttachAttached`, `reapStale=false` so apply
  never reaps another process's lock) -> warn + skip + forward-carry (live).
- **Detached** (git no longer registers the path; detected via a per-repo
  `git worktree list --porcelain` cross-check) -> warn + skip + forward-carry.
  An undetectable worktree defaults to skip-with-warning.

## Implementation Approach

1. **Inherit primitive + R8 detection.** Add `inheritEnvOutputs` in
   `internal/workspace`; unit-test byte-equivalence across dotenv/json/shell and
   multiple/custom targets, 0600 + git-exclude, and the missing-source error.
2. **Standalone worktree path.** Wire `ApplyToWorktree` and
   `applyContentToWorktree` to the primitive; remove the vault bundles and
   resolve+merge from the worktree path; source the global env_output rung from
   the parsed override. Functional `@critical` test: create offline / with a
   broken secret-source session succeeds and matches the clone.
3. **Apply fan-out (R6/R7).** Add the worktree-refresh step to `runPipeline`;
   enumerate + filter + skip-with-warning; accumulate managed files. Functional
   test: rotate a value, `niwa apply`, assert the pre-existing worktree updates;
   assert a locked/missing worktree is skipped with a warning and apply still
   succeeds.
4. **Settings/files secret audit (see Consequences).** Audit whether
   claude.settings/files carry `vault://` refs; if so, extend the inherit-by-copy
   to their rendered outputs so the standalone path writes no unresolved ref.

## Security Considerations

- **No new secret-at-rest surface.** The inherit primitive copies the clone's
  existing 0600 output files to 0600 worktree files; it never parses secret
  bytes into a Go string or serializes them elsewhere. For custom (non-`*.local*`)
  target names it asserts git-exclude coverage BEFORE writing and refuses on a
  non-git tree (`IsGitRepo`), reproducing the `EnvMaterializer`'s fail-closed
  ordering so a custom-named secret never lands git-visible. The rejected A3
  (resolved env in `instance.json`) would have added an at-rest surface; it was
  rejected partly on this ground.
- **Reduced credential surface.** Removing vault resolution from the worktree
  path eliminates a place that authenticated to the secret source -- the source
  of the observed 403 and a spot where a mis-scoped ambient credential could be
  exercised. Net reduction in surface.
- **Path containment (both ends).** BOTH the worktree dest and the clone source
  path go through `safeTargetPath`. The target set is config-derived, and
  `safeTargetPath` treats workspace config as untrusted, so a crafted custom
  `target.Path` (`../` or a symlink) cannot read outside the clone nor write
  outside the worktree. Guarding only the dest (an earlier draft) left the source
  join exposed; both ends are guarded.
- **Apply fan-out isolation.** Enumeration reads session state and other
  processes' attach locks with `reapStale=false` (never reaps a live lock);
  missing/detached worktrees are skipped, so apply never writes to an unexpected
  path. Worktrees of removed repos are filtered out.
- **No plaintext in logs.** Copy reports target paths, not contents; the
  existing redaction discipline is unaffected because no new resolution runs.

## Consequences

### Positive

- Worktree create/apply succeed offline and under a broken/mis-scoped secret
  session; the original failure class is gone.
- One materialization definition; clone and worktree cannot drift (N2).
- Byte-equivalence is structural and format-agnostic (R2/R3).
- `niwa apply` is the single, familiar refresh for the whole instance (R6).

### Negative / trade-offs

- `niwa apply` does more work (linear in live worktrees, local I/O only). Fine
  for the expected handful; parallelizable later if needed.
- Managed-file accounting grows to include worktree outputs. Two cases must be
  kept distinct (see the forward-carry invariant): a *destroyed* worktree drops
  its entries so `cleanRemovedFiles` prunes them (file already gone -> harmless
  ENOENT), but a *skipped-but-live* worktree must forward-carry its entries or
  cleanup would delete its live secret file -- a data-loss bug if conflated.
- `niwa worktree destroy` removes the worktree dir wholesale but leaves stale
  managed-file entries in `instance.json`; the next apply reconciles them via the
  forward-carry-for-live / prune-for-absent rule above.
- Detached-worktree detection is the least crisp signal; the safe default is
  skip-with-warning.

### Known limitations

- **Settings/files with `vault://` refs.** Removing `ResolveAndMergeEffectiveConfig`
  from the standalone worktree path also drops resolution for the
  settings/files materializers. If those carry secret refs, the standalone path
  would otherwise write unresolved values. v1 audits for such refs and extends
  inherit-by-copy to their outputs (Implementation step 4); during `niwa apply`
  the worktree step already runs against the resolved `effectiveCfg`, so the
  apply-refresh path is unaffected. If the audit finds no secret-bearing
  settings/files, step 4 is a no-op guard plus a test.
- **Bootstrap (R8).** A repo whose env was enabled after the last apply has no
  clone output to inherit; create errors pointing at `niwa apply` rather than
  bootstrapping a one-time resolution (PRD D2).

### Neutral

- Worktree env is a snapshot of the last apply; refreshing is `niwa apply` (the
  intended model, not a limitation).
