# Lead: What does the init --bootstrap scaffold look like, and is bootstrap time the right seeding moment?

## Findings

**1. Doc vs. code match exactly.** `docs/guides/init-bootstrap.md` (lines 192-235) documents the scaffold template verbatim, and it matches the actual Go source byte-for-byte:
- Template constant: `internal/workspace/scaffold.go:149-169` (`scaffoldFromSourceTemplate`)
- Substitution logic: `internal/workspace/scaffold.go:216-265` (`ScaffoldFromSource`)
- Comment at `scaffold.go:139-148` explicitly calls out that section ordering/blank lines/comments are a "byte-equality contract -- DO NOT reformat this string," tied to PRD Appendix A.

**2. What the bootstrap scaffold contains.** Only three active sections plus one commented-out example and a doc-link footer:
- `[workspace]` (name, `content_dir = "claude"`)
- `[[sources]]` (org + explicit `repos = [...]` allow-list scoped to just the bootstrap repo)
- `[groups.<vis-key>]` (visibility from a GitHub API bool lookup, R17)
- One commented-out `[claude.content.workspace]` block
- A trailing comment pointing to `docs/guides/workspace-config-sources.md` for the "full schema (claude.*, env.*, vault.*, files, instance)" -- `scaffold.go:163-168`

It does not reference `[instance.files]`, does not reference the embedded Claude Code plugin, and does not install or point to any skill. It's the leanest possible config -- active sections are only what's needed to make `niwa apply` work against exactly one repo.

Note: this bootstrap template (`scaffoldFromSourceTemplate`) is distinct from the plain `Scaffold()` template (`scaffold.go:12-107`, used by bare `niwa init` with no `--from`), which is far more richly commented, including a commented `[instance.files]` example (`scaffold.go:81-84`) and a commented `[claude]`/plugin/marketplace block (`scaffold.go:44-63`). The bootstrap-from-source template deliberately strips almost all of that down to a minimal skeleton.

**3. A precedent for embedded-plugin auto-install already exists, but it's not wired to bootstrap.** `docs/guides/workspace-config-sources.md:655-698` documents "niwa plugin install": when niwa detects a rank-2 source (deprecated root-level `workspace.toml`, not `.niwa/workspace.toml`), it auto-installs an embedded, `//go:embed`-based Claude Code plugin to `~/.claude/plugins/marketplaces/niwa/` so the `/niwa:migrate-config` skill becomes available next session. This has opt-outs (`auto_install_plugins = false`, `--no-install-plugins`), soft-fails on filesystem errors, and is idempotent (`UpToDate` short-circuit).

Critically, this trigger is gated on rank-2 detection, not on bootstrap: `internal/cli/init.go:687-700` calls `workspace.EmitRank2Notice` and the plugin-install skip logic only inside the rank-2 branch. Since `ScaffoldFromSource` always produces a rank-1 layout (`.niwa/workspace.toml`), a freshly bootstrapped repo never triggers this existing plugin-install path -- it's orthogonal machinery that already solves a "seed a skill via the embedded plugin" problem, just for a different (rank-2-migration) trigger condition.

**4. Bootstrap is one-time / forward-looking only, by construction of the discovery probe, not by an explicit "already bootstrapped" flag.**
- `--bootstrap` is only ever consulted when the materialize probe returns `*config.NoMarkerError` (`internal/config/discover.go:133-201`), i.e., neither `.niwa/workspace.toml` (rank 1) nor root `workspace.toml` (rank 2) exists in the source repo.
- Once a repo has a committed `.niwa/workspace.toml` (as commuter and equity-planner do today), that probe never returns `NoMarkerError` again for that repo -- `--bootstrap` becomes a no-op flag; the normal clone/discovery path (rank-1) takes over, per `docs/guides/init-bootstrap.md:10-12`.
- Re-running `niwa init <name> --from ... --bootstrap` against an already-bootstrapped local workspace name is explicitly rejected at the preflight/registry level with `ErrWorkspaceExists` and a "Use niwa apply" suggestion (`test/functional/features/init_bootstrap_idempotency.feature:24-35`, confirmed by `internal/cli/init_bootstrap_registry_test.go`).
- Net effect: bootstrap is inherently a first-adoption operation. Changing `scaffoldFromSourceTemplate` has zero effect on already-bootstrapped repos -- they don't re-run bootstrap, and there's no migration path from the bootstrap machinery itself. Any change to already-bootstrapped repos' `workspace.toml` would need to go through the ordinary hand-edit + `niwa apply` path, or a separate migration skill (analogous to `/niwa:migrate-config`).

## Implications

- If the goal is "seed a config-editing skill for new single-repo adopters," adding an `[instance.files]` entry (or an uncommented `[claude]` plugins/marketplaces block) to `scaffoldFromSourceTemplate` in `internal/workspace/scaffold.go:149-169` is a small, surgical, purely-additive change -- it only affects future `niwa init --bootstrap` runs, never retroactively touches commuter/equity-planner.
- There's already a working pattern in the codebase (rank-2 embedded-plugin auto-install) for "install an embedded plugin/skill on behalf of the user with opt-outs and idempotency" that could be reused/generalized rather than reinvented -- e.g., extend the rank-2 trigger condition, or add a parallel bootstrap-time trigger, calling the same `internal/plugin/installer.go` machinery instead of (or in addition to) editing the scaffold TOML.
- Bootstrap time is a real "first contact" moment (single commit, feature branch, not yet pushed) -- a good place to seed something the user will `git show HEAD` and inspect before pushing, per the R19 success block (`docs/guides/init-bootstrap.md:127-142`), so a config-editing skill reference dropped into the scaffold would be visible and reviewable at adoption time.
- Because already-adopted single-repo workspaces (commuter, equity-planner) will never re-run bootstrap, seeding a skill only via the bootstrap scaffold template would leave those two existing adopters uncovered -- they'd need either a manual backfill/PR, or the rank-2-style "auto-install on `niwa apply`" pattern (which fires on every apply, not just at adoption) to reach already-onboarded repos too.

## Surprises

- The embedded-plugin auto-install feature already exists and is more general-purpose than the exploration context implied -- it's not hypothetical "or otherwise reference/install a skill" territory, it's shipped code (`internal/plugin/installer.go`, `internal/cli/plugins.go`, `internal/cli/dispatch_plugins.go`), just scoped to rank-2 migration rather than bootstrap/first-adoption.
- The bootstrap scaffold template is a hard byte-equality contract (explicitly commented as such in the source) tied to a PRD Appendix A -- any change to it is not a casual doc tweak; it needs to go through the same rigor (and likely a PRD amendment) as the original bootstrap feature.

## Open Questions

- Should a config-editing skill be delivered via a new bootstrap-time trigger of the same `internal/plugin/installer.go` machinery (parallel to rank-2), or via a scaffold-template edit (`[instance.files]`/`[claude]` block), or both?
- For already-bootstrapped single-repo adopters (commuter, equity-planner), is there an existing or planned "retrofit" mechanism (e.g., could `niwa apply` gain a similar auto-install trigger unconditional on rank, so it reaches rank-1 workspaces too), or would backfill require a manual one-off PR to each such repo?
- Is there a PRD or design doc specifically for a "config-editing skill" already in flight that specifies which delivery mechanism (scaffold template vs. plugin install) is intended? (Not found in this pass -- worth a follow-up search of `docs/prds/` and `docs/designs/` for "config-editing" or similar naming.)

## Summary
The `niwa init --bootstrap` scaffold (`internal/workspace/scaffold.go:149-169`, matching `docs/guides/init-bootstrap.md:192-235` verbatim) is a minimal TOML skeleton with `[workspace]`, `[[sources]]`, `[groups.*]`, and a commented `[claude.content.workspace]` example plus a doc-link footer -- it references no plugin, skill, or `[instance.files]` entry today. Bootstrap fires only when a repo has no `workspace.toml` marker at all, so it is strictly a one-time, forward-looking operation for new adopters; already-bootstrapped repos like commuter and equity-planner will never re-trigger it and would need a separate mechanism if a skill is to reach them retroactively. A working precedent for "auto-install an embedded Claude Code plugin/skill with opt-outs" already exists (`internal/plugin/installer.go`, triggered on rank-2 detection during `niwa init`/`niwa apply`), and generalizing that trigger to fire at bootstrap time is a plausible alternative to hand-editing the scaffold template.
