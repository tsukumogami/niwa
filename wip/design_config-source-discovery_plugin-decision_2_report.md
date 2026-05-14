<!-- decision:start id="migration-plugin-install-trigger" status="assumed" -->
### Decision: When niwa triggers the migration-plugin auto-install

**Context**

The umbrella PRD `PRD-config-source-discovery.md` requires niwa to emit a
one-time `note:`-prefixed deprecation notice when discovery resolves a
workspace's team config or overlay via rank 2 (root `workspace.toml` /
`workspace-overlay.toml`). R14 fixes the notice content to include the
literal substring `/shirabe:niwa-migrate-config`. R16 ships the migration
tool as a skill. The user has decided (above the layer of this decision)
that the skill ships as a niwa-owned Claude Code plugin auto-installed to
`~/.claude/plugins/`.

This decision answers the trigger-timing question: when does the install
attempt fire? The candidate triggers were (a) on first rank-2 detection
in init or apply, (b) always on every `niwa apply`, (c) only on explicit
`niwa install-migration-plugin` subcommand, (d) on first rank-2 detection
AND on `niwa apply` when the plugin is missing.

The structural fact that drives the choice is that the install has TWO
natural scopes that don't align with each other or with the deprecation
notice's scope:

- **Per-call**: an `os.Stat(pluginDir)` plus version-file read is sub-
  millisecond. Calling the install function repeatedly is essentially
  free.
- **Installation-wide**: the plugin lives at a single path under
  `~/.claude/plugins/`. Once it's there at the current niwa version, no
  workspace needs it re-installed.
- **Notice scope (R14 / decision_3)**: the deprecation notice fires once
  per workspace per artifact per command-type, gated by
  `DisclosedNotices` IDs `rank2-deprecation:team-config` and
  `rank2-deprecation:overlay`. This scope is per-workspace-per-artifact,
  which is finer-grained than installation-wide.

These three scopes interlock cleanly only if the install's idempotency
is its OWN concern (governed by on-disk plugin state), not borrowed from
the notice's `DisclosedNotices` mechanism.

**Assumptions**

- The niwa binary embeds the plugin contents (e.g., as `embed.FS`) so
  the install is a pure extraction with no network fetch. If wrong: the
  install gains a network round-trip, and the synthesis would need to
  reconsider deferring the install behind explicit consent — a network
  fetch during the notice phase is a behaviour change that warrants
  user awareness. The user's stated direction ("installed automatically
  by niwa") strongly implies a self-contained binary-embedded payload.
- The plugin's filesystem location is a single well-known path under
  `~/.claude/plugins/` (`~/.claude/plugins/niwa-migration/` or similar)
  and Claude Code resolves `/shirabe:niwa-migrate-config` against that
  path without further registration steps. If wrong: a "register with
  Claude Code" step may need its own trigger/consent logic that this
  decision doesn't cover.
- A small `manifest.json` (or version file) inside the plugin directory
  is sufficient for install idempotency: present-and-current means
  no-op, missing or out-of-date means re-extract.
- The install can fail (read-only home, locked-down container, Claude
  Code not installed) and MUST NOT block apply. The notice still emits;
  the install error is logged as a `warn:` line and swallowed.
- Consent (whether the install prompts the user before extracting) is a
  separate decision handled in parallel by plugin-decision-3. This
  decision is purely about trigger TIMING. Whatever consent shape lands
  in plugin-decision-3, this decision pre-commits to: the trigger fires
  inside the `if rank == 2` block in the same three call sites as the
  notice; the consent prompt (if any) lives inside the install function.

**Chosen: Option (a) — On first rank-2 detection in `niwa init` or `niwa
apply`, with on-disk presence (NOT `DisclosedNotices`) as the install
idempotency gate**

Implementation lives at the same three call sites that decision_3 added
the notice emission to:

1. **Team config in `Apply`** (`internal/workspace/apply.go`, after the
   `EnsureConfigSnapshotWithStatus` call returns `rank`):

   ```go
   if rank == 2 {
       // Install attempt is governed by on-disk presence, NOT by the
       // per-workspace DisclosedNotices state, because the plugin path
       // is installation-wide. Failures are warn-and-continue.
       if err := plugin.EnsureMigrationPluginInstalled(ctx); err != nil {
           a.Reporter.Warn("migration plugin install failed: %v (run %s manually)",
               err, "/shirabe:niwa-migrate-config")
       }
       id, msg := Rank2DeprecationNotice("team-config", cfg.Workspace.Name)
       if !sliceContains(wsDisclosedNotices, id) {
           a.Reporter.Log("%s", msg)
           result.disclosedNotices = append(result.disclosedNotices, id)
       }
   }
   ```

2. **Overlay in `runPipeline`** (same package, parallel structure):

   ```go
   if overlayRank == 2 {
       if err := plugin.EnsureMigrationPluginInstalled(ctx); err != nil {
           a.Reporter.Warn("migration plugin install failed: %v", err)
       }
       id, msg := Rank2DeprecationNotice("overlay", cfg.Workspace.Name)
       if !sliceContains(opts.disclosedNotices, id) {
           a.Reporter.Log("%s", msg)
           newDisclosures = append(newDisclosures, id)
       }
   }
   ```

3. **`internal/cli/init.go`** (init context, source slug instead of name):

   ```go
   if rank == 2 {
       if err := plugin.EnsureMigrationPluginInstalled(ctx); err != nil {
           reporter.Warn("migration plugin install failed: %v", err)
       }
       id, msg := workspace.Rank2DeprecationNotice("team-config", source)
       reporter.Log("%s", msg)
       initState.DisclosedNotices = append(initState.DisclosedNotices, id)
   }
   ```

A new package `internal/plugin` houses the install logic:

```go
package plugin

// EnsureMigrationPluginInstalled extracts the embedded migration plugin
// into ~/.claude/plugins/niwa-migration/ if it is missing or out of
// date. Returns nil if the on-disk plugin matches the embedded version,
// extracts if absent or stale, and returns the underlying error if
// extraction fails. Callers MUST treat errors as warn-and-continue:
// the deprecation notice still informs the user of the migration path
// even when the plugin cannot be auto-installed.
//
// Idempotency: a manifest.json containing the plugin version is written
// during extraction. Subsequent calls read the manifest; if the version
// matches the embedded version, the call is a sub-millisecond no-op.
// This means EnsureMigrationPluginInstalled is safe to call on every
// rank-2 detection without coordinating with DisclosedNotices.
func EnsureMigrationPluginInstalled(ctx context.Context) error {
    pluginDir := pluginInstallDir()  // ~/.claude/plugins/niwa-migration
    if currentlyInstalled(pluginDir) {
        return nil
    }
    return extractEmbeddedPlugin(pluginDir)
}
```

**Interaction with `DisclosedNotices`**: the install attempt does NOT
share a `DisclosedNotices` ID with the deprecation notice. They co-fire
from the same `if rank == 2` branch but each owns its own idempotency
mechanism:

- **Notice idempotency**: `sliceContains(wsDisclosedNotices,
  "rank2-deprecation:<artifact>")`. Scope: per workspace per artifact.
- **Install idempotency**: `currentlyInstalled(pluginDir)` (an `os.Stat`
  plus manifest version read). Scope: installation-wide.

The reason for the split: the notice is talking *to the user about this
specific workspace*, so it should fire once per (workspace, artifact).
The install is putting a file *in the user's home directory*, so it
should fire once per (installation, plugin-version). Forcing them to
share an ID would cause one of two pathologies: either the install fires
N times for N rank-2 workspaces (wasteful, even if idempotent) and the
notice over-emits (annoying); or the notice under-emits because the
install's installation-wide ID was already disclosed for some other
workspace.

**Plugin-removed-by-user recovery**: if the user manually deletes
`~/.claude/plugins/niwa-migration/`, the next rank-2 encounter on any
workspace will pass the `currentlyInstalled` check (returns false) and
re-extract. The notice itself remains suppressed for already-disclosed
workspaces — the user doesn't get nagged with a duplicate notice, but
the tool is put back where they expect to find it. If the user deletes
the plugin AND only ever applies rank-1 workspaces afterwards, the
plugin stays gone, which is correct: they have no use for it.

**Rationale**

Five points drive the choice:

1. **Matches the user's stated requirement.** The phrasing "installed
   automatically by niwa if the workspace is identified to need
   migration" is a literal description of option (a). Options (b)
   (always) and (c) (manual) each contradict half of that phrasing;
   option (d) reduces to (b) or (a) under analysis.

2. **Respects the don't-surprise constraint.** A user who never has a
   rank-2 workspace never sees a niwa-owned file appear under
   `~/.claude/plugins/`. The install only fires when the user is
   demonstrably on the migration path. Option (b) breaks this for every
   rank-1-only user.

3. **Preserves the R14 notice's call-to-action force.** The notice
   reads "run `/shirabe:niwa-migrate-config <name>` to migrate"
   verbatim. Under option (a) that command works immediately — no
   prerequisite setup. Option (c) would require lengthening the notice
   to include a setup step, weakening the call to action and
   introducing a discoverability gap for users who hit
   `/shirabe:niwa-migrate-config` via documentation or scrollback
   rather than the notice.

4. **Per-call cost is negligible.** `os.Stat(pluginDir)` + manifest read
   is sub-millisecond. Calling `EnsureMigrationPluginInstalled()` on
   every rank-2 visit is functionally free. The first-time extraction
   cost (one-time, <1MB) lands only for users who need it.

5. **Decoupled idempotency keeps semantics correct.** The install's
   per-on-disk-state idempotency is independent of the notice's
   per-workspace `DisclosedNotices` idempotency. Each mechanism guards
   the right scope. A user with five rank-2 workspaces sees five notices
   (once per workspace per artifact, correctly) and one install attempt
   that succeeds (the first one) plus four sub-millisecond no-ops.

**Alternatives Considered**

- **(b) Always, on every `niwa apply`.** Rejected. Installs a niwa-owned
  Claude Code plugin under `~/.claude/plugins/` for every niwa user
  regardless of whether they ever encounter rank-2 — including users
  with all-rank-1 workspaces who have no use for the migration tool.
  Surprises users and contradicts the user's stated "if the workspace
  is identified to need migration" requirement.

- **(c) Only on explicit `niwa install-migration-plugin` subcommand.**
  Rejected. Requires users to run a niwa command before
  `/shirabe:niwa-migrate-config` works. Lengthens the R14 notice
  (which would need to instruct two commands instead of one), weakens
  the call to action, and creates a discoverability gap for users who
  reach the migration command via documentation or scrollback rather
  than the live notice. Contradicts the user's stated "installed
  automatically by niwa" requirement.

- **(d) On first rank-2 detection AND on `niwa apply` if the plugin is
  missing.** Reduces under analysis to either option (b) or option (a).
  Interpretation (d1) — check-and-install on every apply regardless of
  rank — is functionally identical to (b) and inherits its rejection.
  Interpretation (d2) — only install when rank-2 is detected, with
  on-disk presence as the idempotency gate — is exactly option (a).
  The chosen option (a) is already self-healing for rank-2-encountering
  users because `EnsureMigrationPluginInstalled` re-extracts on missing
  plugin at every rank-2 visit. A rank-1-only user who deletes the
  plugin never re-encounters rank-2 and therefore never needs the
  plugin re-installed; option (a) gets this right by doing nothing for
  them.

- **Riding the deprecation notice's `DisclosedNotices` ID for install
  gating.** Considered as an implementation detail of option (a) and
  rejected. The notice is scoped per-workspace-per-artifact; the
  install is scoped installation-wide. Sharing the ID would either
  cause the install to fire N times (wasteful) or cause the notice to
  under-emit (incorrect against R14's "once per workspace per artifact
  per command-type"). The chosen design gives each mechanism its own
  idempotency check while co-locating their triggers in the same
  `if rank == 2` block.

**Consequences**

What changes:

- A new package `internal/plugin` lands with `EnsureMigrationPluginInstalled(ctx)`
  and the manifest-based idempotency check. The plugin contents are
  embedded into the niwa binary (assumption: via `embed.FS`).
- The three rank-2 call sites from decision_3 (`Apply`, `runPipeline`,
  `init.go`) each gain a single `plugin.EnsureMigrationPluginInstalled(ctx)`
  call adjacent to the existing notice emission, wrapped in
  warn-and-continue error handling.
- A new test surface in `internal/plugin` covers: first install creates
  the directory with manifest, second install is a no-op (manifest
  current), manifest with stale version triggers re-extract, missing
  plugin directory triggers re-extract, locked-home extraction error
  surfaces as a warning without aborting apply.

What becomes easier:

- A user reading the R14 deprecation notice can immediately copy-paste
  `/shirabe:niwa-migrate-config <name>` into Claude Code and have it
  work, with no prerequisite setup step.
- Future plugin updates (e.g., niwa 1.3 ships a newer migration skill)
  flow through naturally: a user on niwa 1.3 who applies a rank-2
  workspace gets the new plugin extracted in place of the old one via
  the manifest version check, on their first rank-2 visit after
  upgrading.
- Adding more niwa-owned Claude Code plugins in the future (e.g., a
  separate workspace-doctor plugin) reuses the `internal/plugin`
  package and the same on-disk-state idempotency pattern.

What becomes harder:

- The niwa binary grows by the size of the embedded plugin contents
  (likely a few hundred KB to a couple of MB). Acceptable given the
  binary is already several MB and the plugin is small.
- Testing the `EnsureMigrationPluginInstalled` function requires
  filesystem fixturing (temp HOME, manifest fakes). Standard Go test
  pattern; no new infrastructure needed.
- A user on a read-only home (CI containers, locked-down hosts) sees
  a `warn:` line on every rank-2 apply rather than nothing, because
  the on-disk presence check fails every time and the extraction
  always errors. Mitigation: the warn message includes the manual
  fallback (run `/shirabe:niwa-migrate-config` manually after sourcing
  the plugin some other way), and the user can suppress the noise by
  migrating off rank-2 — which is the point of the notice.
- Rank-2 hard-removal in the follow-up release (PRD R15) removes the
  three call sites along with the notice emission, which removes the
  install trigger. After hard-removal, the migration plugin no longer
  auto-installs anywhere. This is correct: post-removal, there are no
  rank-2 workspaces left to migrate. Stale plugins on disk from
  pre-removal applies are harmless and can be uninstalled by the user
  manually if they care. The plugin's existence in `~/.claude/plugins/`
  is not load-bearing for any other niwa behaviour.
<!-- decision:end -->

---

## Structured Result

```yaml
decision_result:
  status: "COMPLETE"
  chosen: "On first rank-2 detection in init or apply, co-located with the R14 deprecation notice, with on-disk plugin presence (NOT DisclosedNotices) as the install's idempotency gate"
  confidence: "high"
  rationale: |
    Option (a) is the only candidate that satisfies all three binding
    constraints simultaneously: matches the user's stated requirement
    verbatim ("installed automatically by niwa if the workspace is
    identified to need migration"), respects the don't-surprise
    constraint (rank-1-only users never see a niwa-owned plugin appear),
    and preserves R14's call-to-action force (the notice's
    `/shirabe:niwa-migrate-config <name>` instruction works immediately
    with no prerequisite setup step). Options (b) and (c) each fail at
    least one of those constraints outright. Option (d) reduces under
    analysis to either (b) (rejected) or (a) (chosen). The install fires
    inside the same `if rank == 2` branch as the notice but uses an
    independent idempotency check (`os.Stat` + manifest version) because
    the install's natural scope is installation-wide while the notice's
    scope is per-workspace-per-artifact.
  assumptions:
    - "The niwa binary embeds the plugin contents via embed.FS so the install is a pure extraction with no network fetch."
    - "The plugin's location is a single well-known path under ~/.claude/plugins/ (likely ~/.claude/plugins/niwa-migration/), and Claude Code resolves /shirabe:niwa-migrate-config against that path without separate registration steps."
    - "A manifest.json containing the plugin version inside the install directory is sufficient for idempotency: present-and-current is a no-op, missing or stale triggers re-extract."
    - "Install failures (read-only home, locked-down container, Claude Code not installed) are warn-and-continue: the notice still emits and apply still succeeds."
    - "Consent (whether the install prompts before extracting) is a separate decision (plugin-decision-3) and lives inside the install function; this decision pre-commits only to the trigger TIMING, not to the prompt shape."
  rejected:
    - name: "Always on every niwa apply (option b)"
      reason: "Installs a niwa-owned Claude Code plugin for every niwa user regardless of rank, surprising users who never need migration. Contradicts the user's stated 'if the workspace is identified to need migration' requirement."
    - name: "Only on explicit niwa install-migration-plugin subcommand (option c)"
      reason: "Forces users to run a niwa command before /shirabe:niwa-migrate-config works, lengthening R14's notice text and weakening its call to action. Creates a discoverability gap for users who reach the migration command via documentation rather than the live notice. Contradicts the user's stated 'installed automatically by niwa' requirement."
    - name: "Rank-2 detection AND every-apply self-heal (option d)"
      reason: "Reduces to either option (b) (interpretation d1: check on every apply regardless of rank) which is already rejected, or option (a) (interpretation d2: check only on rank-2 visits, which is what option (a) already does via its os.Stat-based idempotency). Option (a) is naturally self-healing for users who continue encountering rank-2; rank-1-only users have no use for the plugin and correctly don't get it re-installed."
    - name: "Sharing the deprecation notice's DisclosedNotices ID as the install gate"
      reason: "The notice is scoped per-workspace-per-artifact; the install is scoped installation-wide. Sharing the ID would either fire the install N times for N rank-2 workspaces (wasteful) or under-emit the notice (incorrect against R14). Each mechanism owns its own idempotency check; they only share the trigger branch (if rank == 2)."
  report_file: "wip/design_config-source-discovery_plugin-decision_2_report.md"
```
