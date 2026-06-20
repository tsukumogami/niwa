---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-niwa-plugin-record-lifecycle.md
problem: |
  Claude Code never garbage-collects its global plugin install registry,
  and niwa amplifies that gap — proliferating records across workspace
  instances, leaving them behind on destroy, and forcing marketplace
  auto-update that churns cached versions until the records go dangling.
  niwa must clean up the records it causes and stop forcing auto-update,
  while safely mutating a file it does not own and other processes write.
decision: |
  Add an internal registry-access package that reads, filters, and
  atomically rewrites installed_plugins.json with a remove-only,
  re-read-before-write discipline, a one-time backup, and fail-safe
  handling of malformed input. Wire it into three surfaces: instance-
  scoped removal on destroy, a dangling-only sweep in apply, and a new
  `niwa plugins prune` command (preview by default, --apply to act).
  Change the marketplace config to an array of tables carrying a
  per-marketplace auto_update (default false), and register marketplaces
  under their manifest-declared name.
rationale: |
  Remove-only mutation against the freshest on-disk content is
  idempotent and convergent, so niwa never corrupts or clobbers a
  Claude-owned file it cannot lock. Atomic temp-and-rename is already
  the house idiom. An array-of-tables config with a back-compat decode
  hook fits the per-marketplace knob without breaking existing configs.
---

# DESIGN: niwa plugin record lifecycle

## Status

Planned

## Context and Problem Statement

Claude Code keeps a global plugin install registry at
`~/.claude/plugins/installed_plugins.json`: a JSON document mapping each
`plugin@marketplace` key to a list of install records, where each record
carries a `scope`, a `projectPath`, an `installPath` (the cached plugin
version directory), and a `version`. Claude Code writes a record the
first time a session in a given project path enables the plugin, and
never removes a record when that project path or cached version
directory disappears.

niwa turns that dormant gap into a recurring failure. It enables plugins
per repo subdirectory of every workspace instance, so each repo of each
instance becomes a distinct `projectPath` and a distinct record
(`internal/workspace/materialize.go:374-398`). It destroys instances
without touching the registry (`internal/workspace/destroy.go`,
`destroy_workspace.go` reference no Claude state). And it force-enables
marketplace auto-update for every marketplace it registers
(`internal/workspace/workspace_context.go:328,341`), which keeps cached
versions turning over so Claude Code's own cache sweep deletes the old
directories — flipping the orphaned records to dangling. The observed
registry held 111 records for one plugin, 109 dangling, and plugin
resolution at session startup intermittently landed on a dead record,
so skills failed to register.

The PRD (`docs/prds/PRD-niwa-plugin-record-lifecycle.md`) fixes the WHAT.
This design fixes the HOW: a safe way for niwa to mutate a file it does
not own while a foreign process (live Claude sessions) may read and
write it concurrently, plus the integration points and config surface.

No niwa code reads or writes this registry today; niwa locates
`~/.claude` via `os.UserHomeDir` (`internal/plugin/embed.go:58`). This
design introduces that interface.

## Decision Drivers

- **Safety against a foreign concurrent writer (R10-R12).** niwa cannot
  make Claude Code honor a lock. Whatever niwa does must not corrupt the
  file or destroy records it is not responsible for, even when Claude
  Code writes concurrently.
- **Remove-only, criterion-bounded mutation (R9).** niwa only ever
  deletes records matching an explicit dead criterion; it never adds or
  edits records. This is what makes concurrency tractable.
- **Forward-compatibility with a foreign schema.** The registry is
  Claude Code's; niwa must round-trip fields and keys it does not model
  rather than dropping them on rewrite.
- **Fit with existing niwa conventions.** Atomic temp-and-rename is the
  house write idiom; cobra subcommands register via `AddCommand`;
  destructive actions default to safe and opt into action.
- **Backward compatibility for config.** Existing `marketplaces = [...]`
  string lists must keep working unchanged.
- **Reversibility.** A user must be able to recover from an unintended
  removal.

## Considered Options

### Decision 1 — Safe-write discipline for the foreign-owned registry

- **Last-writer-wins (read → modify → atomic rename).** Simple, matches
  the house idiom, satisfies the no-truncation guarantee. Rejected as
  the whole answer: a foreign write landing between niwa's read and
  rename is silently clobbered.
- **Optimistic CAS (re-check mtime/hash before rename, retry).** Detects
  the conflict window but then has to re-read and re-apply anyway, so
  the change-detection is brittle ceremony on top of the real fix.
  Rejected — dominated by the re-read approach.
- **Advisory `flock`.** niwa already uses `syscall.Flock` for worktree
  attach locking, but Claude Code never takes this lock on the registry,
  so it gives zero mutual exclusion against the actual foreign writer.
  Kept only as cheap insurance against niwa-vs-niwa concurrency, not as
  the foreign-writer answer.
- **Minimal-delta re-read merge (chosen).** Re-read the latest file
  immediately before writing and recompute the removal set against that
  content, then write via temp-and-rename. Because niwa only removes
  records still matching the dead criterion on the freshest bytes, it
  never resurrects records and never clobbers a foreign addition (beyond
  a benign, self-healing millisecond window).

### Decision 2 — Recovery command surface

- **`niwa plugins prune` (chosen).** A subcommand under the existing
  `niwa plugins` group; preview by default, `--apply` to act. Fits the
  group that already owns plugin-registry operations and the safe-by-
  default destructive posture.
- **`niwa doctor`.** No diagnostic-aggregation framework exists; building
  one for a single check is premature. Rejected for now — the same logic
  can later surface as a doctor check without breaking the command.
- **Flag on `status`/`apply`.** `status` flags are strictly read-only
  diagnostics; adding a mutation breaks that contract. `apply` already
  owns the automatic sweep, so a flag there conflates the explicit
  recovery path with the automatic one. Rejected.

### Decision 3 — Per-marketplace auto_update config shape

- **Parallel map (`marketplaces` + `marketplace_auto_update`).**
  Back-compat and trivial to parse, but splits a marketplace from its
  policy across two tables that can drift, and the keying collides with
  the R8 name change. Rejected.
- **Array of tables (chosen).** `[[claude.marketplaces]]` with `source`
  and `auto_update`, backed by a custom `UnmarshalTOML` that also
  accepts the legacy `[]string` form (each string → `auto_update:
  false`). Policy lives with its marketplace; default-false is the bool
  zero value; back-compat is handled by the same decode-hook pattern
  niwa already uses for `EnvVarsTable`.
- **Heterogeneous list / string-suffix.** TOML allows mixed arrays at
  the spec level but Go can't decode them into one typed slice cleanly;
  the suffix convention is hacky. Rejected — the inline-table ergonomics
  are folded into the chosen decode hook instead.

### Decision 4 — Registry record model (round-tripping a foreign schema)

- **Typed struct of all fields.** Risks dropping fields niwa does not
  model when it rewrites, corrupting Claude Code's own state. Rejected.
- **Preserve-unknowns model (chosen).** Model the registry generically —
  the top-level `version` plus a `plugins` map to lists of records held
  as structurally-preserved objects — reading only `scope`,
  `projectPath`, and `installPath`, and re-marshalling every other field
  and key untouched. Removal is filtering a list; nothing else changes.

### Decision 5 — Marketplace name keying (R8)

- **Keep keying by source ref.** The current bug: a `repo:tools/...`
  source is keyed `tools` while its manifest declares `tsukumogami`,
  producing a conflicting second registration. Rejected.
- **Key by manifest-declared name for local sources (chosen).** For
  `directory`/`repo:` sources the manifest is on disk at registration
  time, so read `.claude-plugin/marketplace.json`'s `name`. For `github`
  sources the manifest is not local pre-clone, so retain repo-name
  keying (no observed collision there); reconciling a github clone's
  declared name post-clone is noted as out of scope.

## Decision Outcome

niwa gains a single internal package that owns all registry access and
encodes the remove-only, re-read-before-write, atomic, backed-up,
fail-safe discipline once. Three call sites use it: destroy (instance-
scoped removal), apply (dangling-only sweep), and `niwa plugins prune`
(explicit recovery, preview by default). Separately, the marketplace
config becomes an array of tables carrying `auto_update` (default
false), the two hardcoded `autoUpdate: true` literals become the
configured value, and local marketplaces register under their manifest-
declared name.

This works as a whole because every registry mutation flows through one
audited path with one safety model, and because the mutation is
remove-only against the freshest content — the property that makes
concurrent foreign writes survivable without a lock niwa cannot enforce.

## Solution Architecture

### New package: `internal/pluginrecord`

Owns every read and write of `~/.claude/plugins/installed_plugins.json`.

Responsibilities and surface:

- **Locate** the registry from `os.UserHomeDir` +
  `.claude/plugins/installed_plugins.json` (a helper, overridable in
  tests via an injected base dir).
- **Load** into a preserve-unknowns model: top-level `version` plus
  `plugins` as an ordered map of key → list of records, each record
  decoded as a structure that exposes `scope`, `projectPath`,
  `installPath` and retains all other fields verbatim for re-marshal.
  A missing file loads as empty (no error). A malformed file returns a
  typed error and the caller leaves the file untouched (R12).
- **Predicates:**
  - *dangling* — `installPath` is non-empty and its directory is
    missing, OR `projectPath` is non-empty and its directory is missing.
  - *instance-owned(root)* — `projectPath` is within `root` (clean both
    paths, then `filepath.Rel` and reject results that escape with
    `..`), so an instance root removes its own records and not a sibling
    whose path shares a prefix string.
- **Prune(selector)** — the one mutating entry point. It:
  1. takes a non-clobbering snapshot backup before its first write — a
     timestamped sibling `installed_plugins.json.niwa-bak.<RFC3339>`,
     retaining the last N (e.g. 5) and pruning older ones (R11). A fixed
     single-name backup is rejected: destroy, the apply sweep, and
     `plugins prune` are independent operations that would each clobber
     it, so a later erroneous prune would overwrite the only good
     recovery point. The backup is written with `O_CREATE|O_EXCL` and
     the source file's mode (via `os.Stat`), so a pre-planted symlink in
     the shared directory cannot redirect it and the copy does not widen
     permissions.
  2. re-reads the current file (minimal-delta), applies the selector to
     remove matching records, drops now-empty plugin keys;
  3. writes to a temp file in the same directory, created with
     `O_CREATE|O_EXCL`, then `os.Rename`s over the original (atomic, no
     truncation — R10);
  4. returns a report: count removed, per-plugin breakdown, backup path.
  A `dryRun` flag computes and returns the same report without step 1 or
  the write (R4).

  The dangling existence check classifies a record from `installPath` /
  `projectPath` strings that originate in the foreign-owned file. It is
  used only to *decide removal*, never to write through the path, and it
  uses `Lstat` semantics so a record whose path is a symlink to a
  removed target is judged on the link, not a followed target. Safety
  rests on remove-only convergence (below), not on the existence check
  being authoritative about Claude Code's own liveness view.
- **Self-concurrency guard (optional):** a `flock` on a niwa-owned
  sidecar lock to serialize concurrent niwa invocations. Explicitly not
  relied on for foreign-writer safety.

### Integration points

- **Destroy.** `DestroyInstance` (`internal/workspace/destroy.go:126`)
  calls `Prune(instanceOwned(instanceRoot))` before/after
  `os.RemoveAll`. `DestroyWorkspace` (`destroy_workspace.go:53`) prunes
  each instance root it removes. (R1, R2)
- **Apply.** A pipeline step after the managed-file cleanup and before
  the state save (`internal/workspace/apply.go`, ~line 454) calls
  `Prune(dangling)`. Dangling-only keeps the broadly-run command from
  aggressive deletion. (R5)
- **Recovery command.** `niwa plugins prune` registered in
  `internal/cli/plugins.go`. Default run is `Prune(dangling, dryRun)`
  and prints the preview; `--apply` performs it. Output reports count,
  affected plugins, and backup location. (R3, R4)

### Config and marketplace registration

- `MarketplaceConfig{ Source string; AutoUpdate bool }`;
  `ClaudeConfig.Marketplaces` becomes `[]MarketplaceConfig` with a
  custom `UnmarshalTOML` accepting bare strings (legacy) and tables
  (`internal/config/config.go`, pattern from `env_tables.go`).
- Overlay merge (`internal/workspace/override.go:783`) unions on
  `.Source` (base-wins on conflict, carrying base's policy).
- `mapMarketplaceSourceWithIndex`
  (`internal/workspace/workspace_context.go:308-346`) takes the
  `AutoUpdate` value instead of hardcoding `true`, and for local
  (`directory`/`repo:`) sources reads the manifest `name` for the
  registration key. `materialize.go:386` iterates the structs.

### Data flow (prune)

```
caller (destroy | apply | plugins prune)
  -> pluginrecord.Prune(selector, opts)
       backup once (skip if dryRun)
       re-read latest installed_plugins.json   (empty if absent)
       filter records by selector              (remove-only)
       dryRun? -> return report
       write temp in same dir -> os.Rename     (atomic)
       return report (removed, per-plugin, backup path)
```

## Implementation Approach

1. **`internal/pluginrecord` package + unit tests.** Locator, preserve-
   unknowns load/save, atomic write, backup, predicates, `Prune`/dryRun,
   malformed fail-safe, absent-file no-op. Tests use `t.TempDir` and an
   injected base dir with seeded registries. (R3, R4, R9-R13)
2. **Destroy integration + tests.** Wire `DestroyInstance` /
   `DestroyWorkspace`; unit tests assert instance-scoping precision
   (sibling-prefix paths untouched). (R1, R2)
3. **Apply sweep + tests.** Add the dangling-only step; assert post-apply
   no dangling record remains. (R5)
4. **`niwa plugins prune` command + tests.** Preview default, `--apply`,
   output summary. (R3, R4)
5. **Config schema migration.** `MarketplaceConfig` + `UnmarshalTOML`
   back-compat, overlay merge on `.Source`, update all `[]string`
   consumers; tests for legacy and new forms. (R6, R7) The type change
   from `[]string` to `[]MarketplaceConfig` is breaking across more than
   the headline call sites — it also reaches the `MaterializeConfig`
   marketplace field, `internal/config/overlay.go`, and existing tests
   that assert `Marketplaces[i]` as a string. The enumerated call sites
   are not exhaustive; /plan should budget for the full blast radius via
   a compiler-driven sweep of `.Marketplaces` references.
6. **Auto-update emission + name keying.** `mapMarketplaceSourceWithIndex`
   emits configured `auto_update` (default false) and keys local
   marketplaces by declared name; tests. (R6, R8)
7. **Functional scenarios.** Gherkin under `test/functional/features/`
   using `localGitServer`: destroy→records-pruned, apply→dangling-swept,
   `plugins prune --apply`→recovered. (R13)

## Security Considerations

- **Writing outside niwa's own tree.** This is the first niwa code to
  mutate a file under `~/.claude`. The path is derived solely from
  `os.UserHomeDir` plus a fixed relative path — never from user input or
  config — so there is no path-injection surface. The temp file is
  created in the same directory and renamed, so no world-writable temp
  location is involved.
- **Removing the wrong records.** The instance-owned predicate uses
  cleaned-path `filepath.Rel` containment, not string prefix, so a
  sibling instance whose path shares a textual prefix is not matched.
  The dangling predicate only removes records whose referenced
  directories are already gone. Removal is criterion-bounded (R9) and
  the design adds no path that edits or adds records.
- **Reversibility.** A timestamped, non-clobbering `.niwa-bak.<RFC3339>`
  snapshot precedes the first mutation of each Prune invocation (R11),
  retaining the last N. Because destroy, the apply sweep, and `plugins
  prune` are independent operations, a single fixed backup name would be
  overwritten across them — losing the pre-damage recovery point — so
  the timestamped, rotated scheme is required, not optional.
- **Symlink / TOCTOU.** The temp file and the backup are created with
  `O_CREATE|O_EXCL` in the shared `~/.claude/plugins/` directory, so a
  pre-planted symlink at either path cannot redirect niwa's write; the
  backup is written with the source file's mode so it never widens
  permissions. Dangling classification uses `Lstat`-based existence
  checks only to decide removal — it never follows a path to write
  through it — and it trusts path strings from a foreign-owned file, so
  safety rests on remove-only convergence (a wrongly-classified record
  is a regenerable cache entry Claude Code re-creates), not on the check
  being authoritative.
- **No secrets.** The registry holds no credentials; the feature reads
  and writes only plugin bookkeeping. No new secret handling.
- **Preview-by-default.** The explicit recovery command makes no change
  without `--apply`, reducing the chance of an unintended destructive
  run.

## Consequences

**Positive:**

- Skills register reliably; the registry stays proportional to live
  instances; existing damage is recoverable without manual file edits.
- One audited package owns all registry access and its safety model.
- Per-marketplace auto-update removes the churn accelerant while staying
  backward-compatible with existing configs.

**Negative / costs:**

- niwa takes on a dependency on Claude Code's registry schema. Mitigated
  by the preserve-unknowns model (round-trips unmodelled fields) and the
  fail-safe-on-malformed posture (leave untouched, report).
- More integration points (destroy, apply, command, config). Mitigated
  by centralizing logic in one package the call sites delegate to.
- A residual concurrency window remains: a foreign record added in the
  millisecond between niwa's final re-read and rename can be dropped.
  Mitigated by remove-only convergence — the registry is a regenerable
  cache, and Claude Code re-creates a needed record on next resolution.
- The apply-time dangling sweep is global, not scoped to the operating
  workspace: it could remove a record belonging to a *different* live
  workspace instance that is mid-creation, if that instance's
  `projectPath` or `installPath` is transiently absent when the sweep
  runs. This is accepted under the same regenerable-cache argument — the
  swept record is re-created on next resolution — rather than guarded,
  because a cross-instance "is this path stably gone?" check adds
  complexity for a self-healing condition. Named here so it is a chosen
  trade-off, not an oversight.

**Follow-ups / not committed here:**

- Reducing per-repo enablement proliferation (enabling once per instance
  rather than per repo) would cut record creation at the source but
  depends on unverified Claude Code plugin-scoping semantics. Recommend
  a SPIKE to confirm whether a parent-scoped enablement applies to child
  repo directories before committing; out of scope for this design.
- Reporting the underlying Claude Code GC gap upstream is tracked
  separately.
