# Lead: How should the explicit `<name>` override the cloned config's `[workspace] name`?

> Reconstructed from agent in-context summary (the agent did not have Write tool access).

## Findings

### 1. Where `[workspace] name` is read downstream
- **`Apply.Create()`** sets both `InstanceState.ConfigName` and `InstanceState.InstanceName`
  directly from `cfg.Workspace.Name`. Source: `apply.go:407-411` (per agent investigation).
  No override mechanism exists today.
- **`niwa status`** displays workspace identity via `InstanceStatus.ConfigName`, which is
  derived either from instance state or from `cfg.Workspace.Name` in the toml.

### 2. Where the registry name is used
- `globalCfg.LookupWorkspace(name)` is keyed on whatever was registered at init time
  (the explicit `<name>` if given, otherwise `result.Config.Workspace.Name`). The registry
  is the only place today where the explicit `<name>` "lives" after init returns —
  everywhere else, the toml's name wins.

### 3. Existing precedent for instance-state overriding workspace.toml values
- `InstanceState` already carries init-time decisions that downstream code consults
  in preference to (or in addition to) the toml: `SkipGlobal`, `NoOverlay`, `OverlayURL`,
  `OverlayCommit`. These are the established pattern for "init-time choices that
  modify subsequent niwa behavior."
- A new `InstanceNameOverride` field (or `ConfigName` shadow) would follow this
  convention exactly.

### 4. Source-repo refresh story
- The `--from` clone is a one-shot at init time. Re-syncing from the source repo
  isn't a routine operation today, but option (a) (in-place toml rewrite) would
  make any future re-sync messy because the local toml would diverge from upstream
  on the `name` field.

### 5. Critical gap
- `niwa init <name> --from <src>` today does **not** persist the explicit `<name>`
  argument anywhere except the global registry. After init returns, the toml's
  `[workspace] name` becomes the de facto source of truth. So a user who runs
  `niwa init my-override --from upstream-config` will see `upstream-config`'s
  configured name in `niwa status`, `niwa apply` output, etc. — only `niwa go`
  recognizes `my-override`. This is a user-visible inconsistency that the new
  feature must fix.

## Implications

**Recommended option: (b) instance-state override.**

- Fits the existing InstanceState pattern (SkipGlobal, NoOverlay, OverlayURL).
- Keeps the cloned `.niwa/workspace.toml` byte-identical to its upstream HEAD,
  preserving any future sync/refresh story.
- Single, well-defined extension point (state.json) instead of distributing the
  override logic across the codebase.

The implementation needs:
1. A new `InstanceState` field (e.g., `InstanceNameOverride string` or
   `ConfigNameOverride string`).
2. Init-time logic to set the field when the user supplied a positional `<name>`.
3. Downstream readers (`Apply.Create()`, `niwa status` formatting, etc.) updated
   to prefer the override when present.

Option (a) (in-place toml rewrite) is rejected: dirties the cloned config, breaks
upstream sync, requires the file to be writable.

Option (c) (registry-only override) is rejected: the user-visible inconsistency
is exactly what the user is asking us to fix.

## Surprises

- The override gap exists today, even before the directory-creation change.
  A user could already run `niwa init my-name --from upstream` and see
  `upstream`'s name in `niwa status`. The new feature is an opportunity to fix
  both UX issues (folder + name) in one stroke.

## Open Questions

1. Exact field name on InstanceState — `InstanceNameOverride`, `ConfigNameOverride`,
   or just `Name`? Design-level detail; resolved during implementation.
2. Whether downstream readers should hard-prefer the override or fall back to
   the toml name when the override is empty. (Fall back is the natural choice.)
3. Whether `niwa apply` recompute logic needs any change beyond reading the
   override at apply time.

## Summary

Workspace names are read downstream in `Apply.Create()` (apply.go:407-411) and
`niwa status` directly from `cfg.Workspace.Name` with no override path. Option
(b) — adding an `InstanceNameOverride` field to `InstanceState` — fits the
existing init-time-override pattern (SkipGlobal, NoOverlay, OverlayURL) and
keeps the cloned toml clean. The critical gap is that today the explicit
`<name>` is only persisted to the registry; the toml's name silently wins in
status and apply, creating user-visible inconsistency that this feature must
address.
