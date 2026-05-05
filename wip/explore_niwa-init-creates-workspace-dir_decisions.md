# Exploration Decisions: niwa-init-creates-workspace-dir

## Round 1

- **Trigger condition**: folder creation only when a positional `<name>` is
  given. Why: preserves the user's current `niwa init --from <src>` (no name)
  workflow; aligns with "name is explicit user intent."
- **Conflict policy**: error if `<cwd>/<name>` already exists, regardless of
  path type (file/dir/symlink/empty/non-empty). Why: simplest, safest default;
  rejects accidental overwrites; pre-1.0 means no escape hatch needed.
- **Backward compat**: clean breaking change, no `--in-place` / `--here`
  escape hatch. Why: niwa is pre-1.0; the new behavior is the natural default;
  users hitting the new error get a clear remediation message.
- **Name-override approach**: option (b) — add an override field to
  `InstanceState` (e.g., `InstanceNameOverride`); downstream readers consult
  it in preference to the cloned toml's `[workspace] name`. Why: fits the
  existing pattern of init-time overrides (`SkipGlobal`, `NoOverlay`,
  `OverlayURL`); keeps the cloned `.niwa/workspace.toml` clean against its
  source-repo HEAD; single, well-defined extension point.
- **Pre-flight shape**: caller-side existence check producing
  `ErrTargetDirExists`; `CheckInitConflicts(targetDir)` signature unchanged.
  Why: separates filesystem gate from niwa-state validation; nested-instance
  walk-up already handles the non-existent target correctly via
  `DiscoverInstance` walking from the parent.
- **Other commands**: no changes needed — all workspace-location resolution
  is dynamic (cwd-walk) or registry-driven, with no init-time-cwd
  assumptions.
