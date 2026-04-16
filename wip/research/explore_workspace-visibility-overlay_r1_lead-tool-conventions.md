# Lead: Analogous tool conventions for public/private config splits

## Findings

### Git's include/includeIf

Git supports conditional includes via `includeIf` directives in `.gitconfig`:
- `includeIf.gitdir:` — includes config only when the repository is in a specific directory
- `includeIf.onbranch:` — includes config when on a specific branch
- Discovery: **explicit pointer in config** (the user manually adds the conditional include)
- Fallback: **silent skip** if the include file doesn't exist
- Semantics: **additive/override** — later directives override earlier ones

The silent-skip on missing include file is the critical precedent: git doesn't error when an optionally included config is absent.

### Dotfiles Managers

**chezmoi**: Uses `encrypted_` prefix for files that should be encrypted and kept private. Templates with `{{ .chezmoi.os }}` for conditional inclusion. Discovery is explicit (declared in dotfiles structure). Fallback is template-controlled. Semantics: public base + encrypted overlays.

**yadm**: Uses file suffixes like `##CLASS` or `##OS.DISTRO` for conditional includes. No built-in "private companion repo" mechanism — relies on `.yadmignore` to exclude sensitive files from the single repo. Discovery: naming convention (file suffix). Semantics: single repo with conditional selection per host/class.

**dotbot**: No built-in private config mechanism. Philosophy: keep private files outside the dotfiles repo entirely.

### GitHub Ecosystem Conventions

There is a well-established **informal convention** in the GitHub ecosystem:
- `<org>/<repo>` (public) pairs with `<org>/<repo>-private` (private)
- `<org>/.github` pairs with `<org>/.github-private`
- GitHub uses this pattern itself for some organization-level configurations

Discovery: **pure naming convention** — no explicit pointer in the public repo. The tooling that processes the public config knows to also check for the `-private` companion.

Fallback: **silent skip** if the private repo doesn't exist or is inaccessible. The public workflow completes normally without it.

This is not formalized in GitHub's tooling but is an emergent, widely recognized pattern.

### Infrastructure Tools

**Terraform**: Uses `-private` suffix informally for secret var files. Discovery is explicit (via `terraform {}` block or `.terraformrc`). Fallback: hard error on missing required vars. Semantics: merge/override.

**Nix home-manager**: Uses overlay philosophy — `home.nix` (public) + `home-private.nix` (private). Discovery: explicit conditional import in the public config. Fallback: conditional logic can handle missing gracefully. Semantics: public base + private override.

**Kustomize**: `base/` dirs (public) + `overlays/` dirs (environment-specific). Discovery: explicit pointer via `kustomization.yaml`. Fallback: error on missing. Semantics: strategic merge patches.

### Naming Convention Patterns

- **`-private` suffix** (most common): `repo-private`, `config-private` — clear, scannable, self-documenting
- **`.private` dotdir**: Less common, more subtle
- **`_private` underscore**: Intermediate clarity, less idiomatic for repos
- **Clarity winner**: `-private` suffix is the most recognizable in the ecosystem

## Implications

1. **Pure naming convention works** — Git, GitHub ecosystem, and chezmoi demonstrate this is viable when paired with graceful fallback
2. **Silent skip is the correct default** — All successful patterns either skip silently or provide conditional logic for missing private config
3. **`owner/dot-niwa-private` aligns with established ecosystem patterns** — The `-private` suffix on a companion repo is the most recognized convention in the GitHub world
4. **Auto-discovery (zero config) is preferable to explicit pointer** — Tools that require an explicit pointer in the public config create a chicken-and-egg problem for teams that want to keep the public config unaware of the private companion's existence

The key design choice: whether the public config must mention the private companion (explicit pointer, like git's `includeIf`) or the tooling discovers it via naming convention alone (pure convention, like GitHub's `.github-private`). For a privacy-first design, pure convention is stronger because the public config never references the private companion at all.

## Surprises

- Git's silent skip on missing `includeIf` targets is specifically designed for exactly this use case (public/shared dotfiles that include private per-machine config). niwa can use this as direct precedent.
- The GitHub `.github-private` pattern is used by GitHub itself for internal community health files, giving the `-private` suffix convention extra legitimacy.
- None of the major infrastructure tools (Terraform, Kustomize) use pure naming conventions — they all require explicit configuration. This suggests pure convention is more common in personal/team config tools than in infrastructure tooling.

## Open Questions

- Should a missing private companion be a hard error in strict mode (configurable) vs always silent skip?
- If the private companion is named by convention (`<public-repo>-private`), what happens when a team's public config repo is not named `dot-niwa`? Does the convention generalize to `<any-public-config-repo-name>-private`?
- How should the public workspace.toml describe groups when those groups include repos only visible from the private extension? (The public config can't reference group names that would reveal private structure)

## Summary

The most established public/private config patterns use pure naming conventions with silent fallback (`<repo>-private` companion, skip if inaccessible) — this matches the GitHub ecosystem convention and git's `includeIf` silent-skip behavior. Niwa's proposed `owner/dot-niwa-private` naming convention is well-founded and aligns with the `-private` suffix pattern used by GitHub itself. The core design tension is explicit pointer (public config mentions private companion, more auditable) vs pure convention (public config is unaware of private companion, stronger privacy), and the privacy-first goal favors pure convention.
