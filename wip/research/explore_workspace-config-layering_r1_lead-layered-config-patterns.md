# Lead: Layered config patterns in comparable tools

## Findings

### Git Config Layering

**Precedence model:** Local (`.git/config`) > Global (`~/.gitconfig`) > System (`/etc/gitconfig`). Local wins on conflict; system loses.

**Merge semantics:**
- Scalar values: last-wins (local overrides global overrides system)
- Lists: some directives like `includeIf` merge across layers; most scalars just override
- Some settings only apply at certain layers (credentials at global/system, not local)

**Key design decision:** Precedence clarity with simple override beats complex merging. Users can predict what wins without reading a merge spec.

**Lesson:** Document the precedence order clearly. Predictability matters more than expressiveness.

### SSH Config with Include

**Mechanism:** `Include` directive loads additional config files in linear order; first-match-wins per directive.

**Merge model:** No merging -- first declaration wins. If `IdentityFile` appears in an included file and also in the main file, the first one encountered is used.

**Why Include was added:** To avoid duplication when managing multiple accounts or hosts; enables modular config composition without a merge runtime.

**Lesson:** Linear composition with first-match-wins is predictable and avoids "invisible merge" bugs. Users can reason about which file controls a setting by reading in order.

### direnv (.envrc Layering)

**Mechanism:** `source_up` walks the directory tree upward, sourcing `.envrc` in each parent directory.

**Merge model:** Later layers (deeper in tree) win because shell variable assignment overwrites earlier values. Parent config loads first; child config overrides.

**Security:** `direnv allow` must be run explicitly per directory before `.envrc` is auto-applied. Hidden auto-apply would be a security disaster.

**Lesson:** Explicit allow-listing prevents surprise auto-application of untrusted config. For niwa, opt-out at init time (`--no-personal-config`) is the analog: the user consciously controls which workspaces get personal config applied.

### npm/yarn .npmrc Layering

**Precedence:** Project (`.npmrc`) > User (`~/.npmrc`) > Global (`/etc/npmrc`).

**Merge semantics:** Scalar overrides work cleanly. Array-like settings (registries, scopes) do NOT merge -- project config fully replaces the global value.

**Known gotcha:** Users expect registry scope merging but don't get it; causes surprise when project config overrides all global scopes. This is npm's most common config-layering complaint in its issue tracker.

**Lesson:** If you make lists non-merging, document it prominently. Users' mental model defaults to "append" for lists; "replace" surprises them.

## Implications

### Patterns to adopt for niwa

1. **Clear precedence order:** Personal > Workspace. State it once, apply it everywhere. Don't make users read a merge spec to predict behavior.

2. **List merge default should be append:** All comparable tools that handle list merging use append (git includeIf, direnv source_up concatenation). The npm "replace" approach is the most-complained-about design choice. Hooks and env files should append across layers.

3. **Map/scalar default should be last-wins (personal wins):** Standard across all tools. Personal layer wins on conflict; workspace values not overridden by personal are preserved.

4. **Explicit opt-out over silent no-op:** direnv's `direnv allow` and niwa's `--no-personal-config` flag both make the user consciously control application. This prevents silent skipping and makes behavior auditable.

5. **Plugins (replace-or-inherit) is the hardest case:** npm's replace semantics are its most-complained-about feature. If niwa keeps "plugins replace entirely" for personal config, document it prominently. Consider whether "personal plugins extend workspace plugins" is the right default -- it would differentiate niwa from npm's gotcha.

### Patterns to avoid

- **Implicit merging without documentation:** If the merge rule differs by field type (lists append, maps per-key win, plugins replace), write it in one place.
- **Auto-application without consent:** direnv's model (explicit allow per directory) is the gold standard for untrusted config. Niwa's opt-out flag is the right mechanism.

## Surprises

- SSH's first-match-wins was chosen over merge-all specifically because predictability beats power. The SSH team explicitly rejected merge semantics.
- direnv's success hinges entirely on `direnv allow`; without it, the tool would be a security problem.
- npm's non-merging arrays are the #1 source of support requests in their repo. The decision to replace rather than merge arrays causes real user pain.

## Open Questions

- Should niwa's personal config plugins follow "replace" (current repo-level semantics) or "extend" (more intuitive)? The npm precedent suggests "extend" is safer from a UX perspective.
- Is `--no-personal-config` the right opt-out granularity, or should users be able to opt out per-field (e.g., skip personal hooks but apply personal env vars)?

## Summary

The strongest applicable pattern is git's three-layer precedence model (local > global > system) with scalar override and list append, which is predictable without being complex. The key lesson is that documented, consistent override semantics -- especially for lists -- matter more than powerful merging, as shown by npm's array non-merge being its most-complained-about design choice. The main open question for niwa is whether hooks and plugins (list vs replace fields) should follow append semantics across personal+workspace layers, or preserve the current "plugins replace" behavior and clearly document it.
