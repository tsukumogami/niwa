# Lead: How does the overlay mechanism compose with embedded config?

## Findings

### Current Overlay Flow (End-to-End)

#### Step 0 — Clone into XDG snapshot
- **Where**: `internal/config/overlay.go:281–312` (`OverlayDir` function)
- The overlay URL (e.g., `org/myrepo-overlay`) is normalized via `DeriveOverlayURL` (line 202–214) into a convention form.
- `OverlayDir` resolves the URL into a local XDG snapshot path: `$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/` (fallback: `$HOME/.config/niwa/overlays/<org>-<repo>/`).
- For `file://` URLs, the directory name is `file-<last-path-component>`.

#### Step 0.5 — Overlay discovery (convention or explicit state)
- **Where**: `internal/workspace/apply.go:676–735` (discovery and sync logic)
- Three branches:
  1. **NoOverlay=true** → skip entirely.
  2. **OverlayURL in state** → sync existing clone (hard error on failure).
  3. **ConfigSourceURL set** → derive convention overlay URL via `DeriveOverlayURL` (e.g., `org/myrepo` → `org/myrepo-overlay`), attempt clone/sync (silent skip if fresh clone fails; hard error if sync fails on existing snapshot).
- `EnsureOverlaySnapshot` (line 115, `internal/workspace/overlaysync.go:28–54`) materializes or refreshes the snapshot. Internally dispatches to `EnsureConfigSnapshot` (existing snapshot or legacy working tree) or `MaterializeFromSource` (fresh clone).

#### Step 0.6 — Parse and merge overlay into base config
- **Where**: `internal/workspace/apply.go:737–810`
- Reads `workspace-overlay.toml` from `overlayDir` (line 747).
- Validates overlay (rejects absolute paths, ".." traversal, protected destinations like `.claude/` and `.niwa/`).
- Resolves vault references in `overlay.Env` and `overlay.Repos` against the overlay's own vault bundle (per-layer isolation, R23).
- **Merge boundary** (`MergeWorkspaceOverlay`, `internal/workspace/override.go:652–749`):
  - **Sources**: append overlay sources (error on duplicate org).
  - **Groups**: add overlay-only groups (base wins on collision).
  - **Repos**: add overlay-only repos; for repos in both, merge env values (base wins per key).
  - **Claude.Hooks**: append overlay hooks, resolve script paths to absolute paths within `overlayDir`.
  - **Claude.Settings**: base wins per key.
  - **Claude.Marketplaces & Plugins**: append-union (entries already in base are skipped).
- Merged config replaces the base config (line 809) for all subsequent pipeline steps.

#### Step 1 — Discover repos and classify
- **Where**: `internal/workspace/apply.go:821–829`
- **Critical change from PR #138**: No filter removes the overlay repo from the discovered list. An overlay repo (e.g., `test-ws-overlay`) discovered via org auto-scan flows through `Classify` like any other repo and joins whichever group matches its visibility (private or public).
- Step 1 anchor comment (lines 813–820) explicitly forbids re-introduction of a filter, referencing issue #137.
- The XDG snapshot remains the source of truth; a working copy of the overlay repo never affects apply until pushed and re-synced into the snapshot.

### Contract Summary

| Aspect | Current Behavior |
|--------|------------------|
| **Overlay URL derivation** | `DeriveOverlayURL` (line 202) adds `-overlay` suffix to base URL; handles `https://`, `ssh://`, shorthand, and `file://` forms. |
| **Overlay snapshot path** | `OverlayDir` (line 285) resolves to `$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/`. |
| **Config file location** | `workspace-overlay.toml` lives at the root of the overlay snapshot. |
| **Merge boundary** | Step 0.6 (line 737) merges parsed overlay into base config *before* Step 1 discovery. |
| **Overlay repo classification** | Post-PR #138 (commit 9d1ba48): overlay repo flows through discovery and classification as a normal workspace component. |
| **XDG snapshot as source of truth** | Workspace-level overlay always reads from `overlayDir` (XDG path). Working-copy changes take effect only after push + re-sync. |

---

## Candidate Layouts for Embedded Config + Overlay

When base workspace config is at `general-repo/.niwa/workspace.toml`, the overlay has four candidate locations:

### (A) Status Quo — Dedicated Overlay Repo (Recommended for now)

```
general-repo/
  .niwa/
    workspace.toml           ← base config
    groups.toml
    ...

general-repo-overlay/       ← separate dedicated repo (convention)
  workspace-overlay.toml    ← overlay config
  hooks/
  repos/
  ...
```

**Snapshot layout:**
- Base: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo/` (contains files from `general-repo/.niwa/`)
- Overlay: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo-overlay/`

**Changes required:**
- None. `DeriveOverlayURL(general-repo)` already produces `general-repo-overlay`. `OverlayDir` resolves both snapshots independently. Step 0.6 merge works unchanged.

**Merge boundary:**
- Step 0.6 reads base from `general-repo/.niwa/*` (via snapshot path), overlay from `general-repo-overlay/*` (via snapshot path).

**Trade-offs:**
- ✓ No changes to existing overlay code.
- ✓ Audit model unchanged: base and overlay have distinct repos.
- ✓ Personal overlay (global config) can still point anywhere.
- ✗ Users must stand up a second repo if they want overlay personalization.
- ✗ Mirrors the "two repos" cognitive load that embedded config is meant to reduce.

---

### (B) Embedded Overlay — `general-repo-overlay/.niwa/`

```
general-repo/
  .niwa/
    workspace.toml           ← base config
    ...

general-repo-overlay/       ← overlay repo with embedded config
  .niwa/
    workspace-overlay.toml   ← overlay config
    hooks/
    ...
```

**Snapshot layout:**
- Base: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo/` (contains files from `general-repo/.niwa/`)
- Overlay: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo-overlay/` (contains files from `general-repo-overlay/.niwa/`)

**Changes required:**
1. Update `DeriveOverlayURL` to append `-overlay` (unchanged behavior).
2. **New**: Introduce subpath resolution for overlays (parallel to workspace config subpath discovery). When overlay snapshot is materialized, extract only `.niwa/*` (or auto-discover if missing).
3. Update `OverlayDir` to optionally resolve into `overlayDir/.niwa/` if the marker exists (or add a separate `OverlayConfigDir` helper).
4. Update Step 0.6 merge boundary: read overlay config from `overlayDir/.niwa/workspace-overlay.toml` instead of `overlayDir/workspace-overlay.toml` (line 747).

**Merge boundary:**
- Base: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo/` (files from `general-repo/.niwa/`)
- Overlay: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo-overlay/.niwa/` (files from `general-repo-overlay/.niwa/`)

**Trade-offs:**
- ✓ Both base and overlay repos follow the same embedded pattern; consistent mental model.
- ✓ Overlay repo is a general-purpose repo; can carry other content (docs, tooling, etc.).
- ✓ XDG snapshots remain independent; no cross-contamination.
- ✗ Requires subpath support for overlays (moderate complexity; mirrors base config feature).
- ✗ Overlay snapshot extraction must know about the `.niwa/` subdir convention.

---

### (C) Personal "Everything" Repo with `.niwa-overlay/` Subdir

```
general-repo/
  .niwa/
    workspace.toml           ← base config
    ...

personal-repo/             ← new personal everything repo
  .niwa/
    niwa.toml               ← personal global config
    ...
  .niwa-overlay/            ← workspace-specific overlay for general-repo
    workspace-overlay.toml
    hooks/
    ...
```

**Snapshot layout:**
- Base: `$XDG_CONFIG_HOME/niwa/overlays/org-general-repo/`
- Overlay: would resolve to `$XDG_CONFIG_HOME/niwa/overlays/org-personal-repo/` but would need to extract only `.niwa-overlay/*` (subpath).

**Changes required:**
1. Overlay URL derivation becomes context-dependent: can no longer simply append `-overlay` to base URL. Requires either:
   - New config field per workspace: `overlay_source_url` or `personal_overlay_org/repo`.
   - User-provided path or mapping in workspace config or global config.
2. Subpath support for overlays (extract `.niwa-overlay/` instead of `workspace-overlay.toml` at root).
3. Rethink Step 0.6 merge boundary: overlay config location becomes variable (per-workspace config, not convention).

**Merge boundary:**
- Base: `general-repo/.niwa/`
- Overlay: `personal-repo/.niwa-overlay/` (resolved via explicit config, not convention)

**Trade-offs:**
- ✗ Breaks convention-based discovery (overlay URL no longer derivable from base URL).
- ✗ Requires explicit per-workspace overlay source in config.
- ✗ Couples personal global config repo with workspace-specific overlays.
- ✗ Complicates Step 0.6 boundary and merge logic.
- ✓ Single "personal everything" repo for all personal config (global + all workspace overlays).

---

### (D) Same Repo, Sibling Subdir (Not Recommended)

```
general-repo/
  .niwa/
    workspace.toml           ← base config
    ...
  .niwa-overlay/             ← overlay config in same repo
    workspace-overlay.toml
    hooks/
    ...
```

**Snapshot layout:**
- Base and overlay both materialize from the same clone into separate XDG paths (or the same path, with subdir separation).

**Changes required:**
1. Option D1 (separate snapshots): Treat `general-repo` and `general-repo + overlay-subdir` as two separate snapshot entries. Requires treating subdir as part of the overlay identity (e.g., `general-repo:.niwa-overlay`).
2. Option D2 (single snapshot, subdir merge): Materialize once and extract both `.niwa/` and `.niwa-overlay/`. Step 0.6 reads from two subdirs within the same snapshot.

**Merge boundary:**
- Base: `<snapshot>/.niwa/`
- Overlay: `<snapshot>/.niwa-overlay/`

**Trade-offs:**
- ✗ **Breaks audit model**: base and overlay are no longer independently auditable. A single repo change affects both layers.
- ✗ **Conflates concerns**: base (canonical, published) and overlay (personal, private) live in the same version-control boundary.
- ✗ Snapshot deduplication logic becomes unclear: is `.niwa-overlay` fetched once (with `.niwa/`) or separately?
- ✗ Difficult to say "this repo contributed only to the overlay layer" in audit output.
- ✓ Single repo clone; no `-overlay` convention to manage.
- ✗ High footgun risk: a commit that touches both `.niwa/` and `.niwa-overlay/` becomes hard to reason about (personal + shared changes in one atomic commit).

---

## Detailed Impact Analysis

### Option A vs. Option B: `OverlayDir` Behavior Change

**Option A (status quo):**
```go
// OverlayDir("org/myrepo-overlay")
// Returns: $XDG_CONFIG_HOME/niwa/overlays/org-myrepo-overlay/
// Files: workspace-overlay.toml at root
```

**Option B (embedded overlay subdir):**
```go
// OverlayDir("org/myrepo-overlay") 
// Returns: $XDG_CONFIG_HOME/niwa/overlays/org-myrepo-overlay/ (same)
// BUT: Step 0.6 must check for .niwa/ subdir and read from:
//   $XDG_CONFIG_HOME/niwa/overlays/org-myrepo-overlay/.niwa/workspace-overlay.toml
// OR update OverlayDir to return the subdir path directly:
//   $XDG_CONFIG_HOME/niwa/overlays/org-myrepo-overlay/.niwa/
```

### How PR #138 Impact on This

PR #138 (commit 9d1ba48) deleted the unconditional filter that removed the overlay repo from discovery. This is **orthogonal** to embedded config:

- **Overlay repo classification (PR #138)**: Whether the overlay repo is a workspace component depends only on whether it's discovered and whether it matches a group's visibility. This is unchanged by embedded config.
- **Overlay config source (embedded config question)**: Where the overlay *config* lives (root of snapshot vs. `.niwa/` subdir) is independent.

**For Option A**: No change to PR #138's logic.

**For Option B**: The overlay repo (e.g., `general-repo-overlay`) would still flow through discovery and classification unchanged. But when Step 0.6 reads the overlay config, it looks for `.niwa/workspace-overlay.toml` instead of root.

---

## Open Questions

1. **Should overlay repos *always* embed their config in `.niwa/` (Option B), or is the status quo (Option A) acceptable as a stepping stone?**
   - Option A requires zero code changes and aligns with the user's "overlay mechanism hopefully stays the same shape" statement.
   - Option B would be more consistent but requires subpath support (moderate effort).

2. **How does convention discovery interact with embedded overlay config?**
   - If a user creates a `general-repo-overlay` repo and does *not* include a `.niwa/workspace-overlay.toml` marker, should niwa:
     - (a) Look for a root-level `workspace-overlay.toml` as a fallback (backwards compat)?
     - (b) Reject with a clear error ("overlay repo missing expected `.niwa/workspace-overlay.toml`")?
     - (c) Auto-discover (look for `.niwa/workspace-overlay.toml` first, then root)?

3. **Should the audit model be updated to reflect that overlay config is now in a subdir?**
   - Current audit references `personal-overlay` as the credential source label. If overlay config is at `personal-repo/.niwa-overlay/`, do we track subpath in audit records?

4. **When the base repo is embedded (`.niwa/`) but the overlay is not, does this create a user-facing inconsistency?**
   - User sees `general-repo/.niwa/workspace.toml` but `general-repo-overlay/workspace-overlay.toml` (root level).
   - Is this acceptable as a transitional state, or should we migrate overlays when we migrate base?

---

## Surprises

1. **PR #138 is fully compatible with Option B**: The removal of the filter is orthogonal to config source location. An overlay repo can be discovered and classified regardless of whether its config lives at root or in a `.niwa/` subdir.

2. **OverlayDir already handles multiple URL forms correctly**: The function works with shorthand, HTTPS, SSH, and `file://` URLs, deriving a stable directory name. It does not need changes for embedded config—only the post-materialization path lookup needs adjustment.

3. **The merge boundary is clean**: Step 0.6 is the only place that needs to change. Reading `workspace-overlay.toml` from `overlayDir/.niwa/` (Option B) vs. `overlayDir/` (Option A) is a one-line change in apply.go:747.

4. **Option C (personal everything repo) would be a *larger* change than it initially appears** because it breaks convention discovery. The overlay URL can no longer be derived from the base URL alone; requires explicit per-workspace config. This would necessitate changes to init, apply, and the InstanceState schema.

---

## Implications

1. **For embedded config itself**: The overlay mechanism does not block embedded config. Both Options A and B are viable.

2. **For consistency**: Option B (embedded overlay in `.niwa/`) would create a uniform pattern across base and overlay, but requires moderate implementation effort (subpath resolution, Step 0.6 path lookup update).

3. **For migration**: If the decision is to migrate existing `dot-niwa` repos to embedded config, then Option A (leaving the overlay at root) is a viable intermediate state. Option B could be adopted later as a follow-up.

4. **For the audit model**: Under Option B, audit records should track that the overlay config came from `<snapshot>/.niwa/workspace-overlay.toml`, not just the repo name. Minor documentation update needed.

---

## Summary

The current overlay mechanism clones the entire `org/repo-overlay` repo into `$XDG_CONFIG_HOME/niwa/overlays/org-repo-overlay/`, reads `workspace-overlay.toml` at the root, and merges it in Step 0.6 before discovery. PR #138 ensures the overlay repo itself (if discovered) flows through classification as a normal workspace component. 

**Option A (status quo, no overlay change)** is the simplest path and aligns with the user's stated preference ("overlay mechanism hopefully stays the same shape"). Option B (embedded overlay at `.niwa/`) is more consistent but requires ~100 lines of changes to add subpath resolution for overlays and update Step 0.6 to read from the subdir. Option C and D are either breaking (C) or audit-model-breaking (D) and not recommended. The choice between A and B should be driven by whether a unified embedded convention (both base and overlay at `.niwa/`) is worth the modest implementation cost for consistency.

