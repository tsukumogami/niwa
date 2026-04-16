# Lead: Edge cases in mixed public/private workspaces

## Findings

### State Space Mapping

A private extension (e.g., `dot-niwa-private`) layered over a public config (`dot-niwa`) creates six distinct edge cases. The design uses a three-layer merge: workspace defaults → global/private overrides → per-repo overrides. Groups drive directory structure; repos must be classified into groups. Content is installed per group and per repo. The following analysis assumes the private extension operates at the "global config" layer (parallel to the workspace config).

#### Case A: Repo only in public config (baseline)
**State:** Repo discovered from a source org in public config, classified into a public group, content installed at standard paths.

**Example:** Org `tsukumogami` declared in `[sources]`, repo `tsuku` has visibility=public, matches group `public`, installs to `instance/public/tsuku/`.

**Outcome:** No change needed. Standard workflow.

---

#### Case B: Repo only in private extension
**State:** Repo not in public config's sources; declared explicitly in private config with `[repos.REPO_NAME]` where both `url` and `group` are set.

**Mechanism:** The apply pipeline has `InjectExplicitRepos()` (classify.go:63–116) which adds repos from `cfg.Repos` entries with both `url` and `group` set, bypassing source discovery. These join the classified list and flow through the full pipeline.

**Example:** Private config declares:
```toml
[[sources]]
org = "internal-corp"  # Not in public config

[repos.secret-runner]
url = "git@github.com:internal-corp/secret-runner.git"
group = "infra"        # Group must exist in [groups]
```

**Critical Issues:**
1. **Group must be pre-defined:** The private extension **cannot create new groups**. `InjectExplicitRepos()` validates that `override.Group` exists in the `groups` map (line 92–94). If the private extension wants to add a repo to a group that only exists in the private config, it fails validation.
   
2. **Group directory creation:** Group directories are created during pipeline execution (apply.go:232–235). If a group `infra` is defined in the private config but not in the public config, the directory is created only if **at least one repo** lands in it. This works correctly for repos injected by the private extension.

3. **Content must be optional:** If a private-only repo needs CLAUDE.md content, that content file must also be private. The convention-based content fallback in `InstallRepoContent()` tries to load `{content_dir}/repos/{repo_name}.md`. If this file path is shared between public and private configs but lives only in the private repo, it's accessible only when the private extension is active.

**Outcome:** Works IF the private extension defines groups that will receive repos (empty groups are silently ignored). Content files must be in the private extension directory to stay hidden.

---

#### Case C: Repo in both public and private configs
**State:** Repo discovered from public config's sources AND has an override entry in private config.

**Mechanism:** The discovery phase (apply.go:177–179) discovers repos from all sources. Then `InjectExplicitRepos()` processes `[repos]` entries. If a repo is already discovered (checked by `existing[name]` on line 87), the explicit entry is skipped. So a repo in both configs is treated as discovered only.

**Example:** Public config discovers `tsuku` from `org=tsukumogami`. Private config has:
```toml
[repos.tsuku]
claude.hooks.pre_tool_use = ["hooks/verify-credentials.sh"]
env.vars.PRIVATE_KEY = "..."
```

**Critical Interaction:**
1. **No direct merging:** The `InjectExplicitRepos()` function sees that `tsuku` is already discovered and skips the explicit entry (line 87). The private config's per-repo overrides in `[repos.tsuku]` are **not automatically applied** during injection.

2. **Merge happens later:** Per-repo overrides are applied in the materializer phase (apply.go:363) via `MergeOverrides(effectiveCfg, cr.Repo.Name)`. So the override is applied during hook/env installation, but **only if the repo was discovered first**.

3. **Issue: Private config repo entries are ignored if discovered.** If a developer adds `[repos.tsuku]` in the private config expecting it to override the public config's handling of `tsuku`, that entry's `url`, `branch`, `group`, and other fields are silently ignored because `tsuku` was already discovered. The overrides in `[repos.tsuku.claude]`, `[repos.tsuku.env]`, and `[repos.tsuku.files]` are applied, but the repo-level fields are discarded.

4. **Visibility of merged content:** Content merges work correctly: public content is installed, then private hooks/env extend the configuration. But if the private extension wants to add a completely different content file for a public repo, that's not supported — `InstallRepoContent()` uses the first source it finds (convention-based or explicit in config) and doesn't support multiple content sources per repo.

**Outcome:** Works for hooks, env, and settings overrides. Breaks for URL, branch, group, and content file overrides of discovered repos.

---

#### Case D: Source org in both public and private configs
**State:** Same GitHub org (e.g., `org=tsukumogami`) declared in both public and private sources.

**Example:**
- Public: `[[sources]]\norg = "tsukumogami"` (auto-discovers all public repos)
- Private: `[[sources]]\norg = "tsukumogami"\nrepos = ["secret-repo"]` (explicit list to avoid discovery)

**Mechanism:** The `discoverAllRepos()` function (apply.go:525–549) iterates over all sources and collects repos. A `seen` map (line 527) detects duplicate repo names **across sources** and errors if found (line 537–540).

**Critical Issue:**
1. **No semantic merging of source declarations:** If both configs list the same org, the effective config would contain two source declarations for the same org. The second one is processed independently, potentially with different `max_repos` or `repos` list.

2. **Duplicate detection is by repo name, not by org:** If both public and private configs discover the same org and a repo appears in both, the duplicate repo name check triggers an error. This is correct and intentional, but it means **you cannot have the same org in both configs without explicit repo lists in the private config**.

3. **Workaround:** Declare the org explicitly in the private config with a specific `repos` list, and rely on source discovery in the public config. But there's no way to say "the same org, but also include these additional repos" — you must list all repos explicitly.

**Outcome:** Breaks. Same org in both configs causes duplicate repo detection errors unless the private config uses explicit repo lists.

---

#### Case E: Repo in public config but should be hidden
**State:** A repo is auto-discovered from a public source org (e.g., visibility=private but org is public), but the private extension needs to hide it from the workspace layout or prevent Claude from accessing it.

**Example:** Org `tsukumogami` is public with both public and private repos. Public config auto-discovers all repos via `visibility` groups. A private repo `internal-secrets` gets classified into the `private` group and installed to `instance/private/internal-secrets/`.

Now a real-world constraint: the repo `internal-secrets` must **not be cloned at all** because it contains sensitive build keys that shouldn't exist on a dev machine. Or it should be cloned but Claude disabled for it.

**Mechanism:** 
1. **Selective cloning via explicit repos:** There's no built-in "exclude" mechanism. To hide a repo from discovery, you'd need to control the discovery process itself.

2. **Alternative: Disable Claude per repo:** The `[repos.REPO_NAME]` section has a `claude` field (bool) that skips CLAUDE.md installation when false (apply.go:329). But the repo is still cloned.

3. **No "skip clone" override:** There's no way to say "discovered this repo, but don't clone it." The clone happens for every classified repo (apply.go:241–244).

**Critical Gap:**
The discovery phase is all-or-nothing per org. You either auto-discover all repos (up to `max_repos`) or list them explicitly. There's no in-between: "discover from org, but exclude these specific repos."

To truly hide a repo, the private extension would need to either:
- Redefine the sources entirely in the private config (removing the auto-discovery source), or
- Use an explicit repos list in the private config and omit the hidden repos.

But neither is possible because merging sources between configs is not supported.

**Outcome:** Breaks. Auto-discovered repos cannot be selectively hidden without a redesign of the discovery/group mechanism.

---

#### Case F: Content conflicts (same repo, different content files)
**State:** Both public and private configs declare content for the same repo.

**Mechanism:** The content installation phase (apply.go:328–342) calls `InstallRepoContent()` for each classified repo. This function (content.go) resolves the source path based on `effectiveCfg.Claude.Content.Repos[repoName]` and the convention-based fallback.

**Example:**
- Public config declares: `[claude.content.repos.tsuku]\nsource = "repos/tsuku.md"`
- Private config declares: `[claude.content.repos.tsuku]\nsource = "private-content/tsuku.md"`

**Mechanism of conflict:**
1. **Config merging doesn't handle content:** The `MergeGlobalOverride()` function (override.go:327–436) explicitly does **not** merge content. This is by design (override.go:327 comment): "Claude.Hooks, Settings, Env.Promote, Env.Vars, Plugins are merged; Content is NOT merged."

2. **Content is workspace-scoped, not override-able:** The `ClaudeOverride` type (config.go:45–51) used at per-repo and global override positions explicitly omits the `Content` field. So `[repos.REPO.claude.content]` is not allowed — it would be a TOML parsing error.

3. **No way to override content at global layer:** If the private extension is at the global config layer, it cannot change which content file is used for a repo. The content hierarchy is read from the original workspace config only.

**Critical Issue:**
This is an **intentional design boundary**, not an oversight. The DESIGN-workspace-config.md document explicitly states that content is not overrideable. But for a private extension use case, you might want to add private instructions to the same repo's CLAUDE.local.md file.

**Workaround (not supported):** Append private content to an existing file, or use a separate `CLAUDE.private.md` file that Claude Code discovers separately. But niwa doesn't manage this; it would be manual.

**Outcome:** Not supported by design. The private extension cannot override content files for repos defined in the public config. This is a fundamental assumption of the current design.

---

### Merge Semantics Summary

The current three-layer merge (apply.go:213–223) is:

```
workspace defaults (from .niwa/workspace.toml)
         ↓
global override (from ~/.niwa/global/niwa.toml or registered global config)
         ↓
per-repo override (from [repos] in workspace config)
```

**What each layer can override:**
- **Global layer:** Hooks (append), Settings (per-key win), Env.Promote (union), Env.Vars (per-key win), Plugins (union), Managed Files (per-key win/suppress).
- **Per-repo layer:** Hooks (append), Settings (per-key win), Env.Promote (union), Env.Vars (per-key win), Plugins (replace), URL, Branch, Files (per-key win/suppress), Claude.Enabled.

**What neither layer can override:**
- Sources (org, repos lists, max_repos)
- Groups (definitions, visibility filters, explicit repo lists)
- Content (file sources for workspace/group/repo level)

---

### Root Cause: Config Merging Model

The current design assumes:
1. **Sources and groups are immutable after workspace config is loaded.** There is no mechanism to merge source orgs or group definitions from multiple config files.
2. **Content is workspace-scoped and non-overridable.** Only the main workspace.toml specifies content file sources.
3. **Explicit repos (with url + group) are injected but not merged.** If a repo is already discovered, explicit entries in the same layer are skipped. Cross-layer explicit repo entries don't merge.

These assumptions work for a single config source but break when layering a private extension that needs to:
- Add new sources without duplication
- Define new groups or refine group membership
- Override content files
- Selectively hide discovered repos

---

## Implications

### Must Solve for V1

**E1: Source and group merging.** If a private extension needs to add repos to a source org also in the public config, or to define new groups, the apply pipeline must support merging source and group declarations across layers. Current options:

1. **Explicit repo lists in private config** (requires naming all repos, fragile)
2. **Separate source org** (not always feasible if the org is already public)
3. **Config format redesign** (e.g., allow "additional sources" that extend public ones)

**E2: Selective hiding of auto-discovered repos.** The discovery model is all-or-nothing per org. To support real-world cases where a public org has both shareable and sensitive repos, the group classification logic needs a way to exclude repos or mark them as "discovered but not installed."

Example implementation: Add a group concept of "exclude" or "skip", or introduce a per-repo override field `clone = false` that prevents repo cloning while still allowing other metadata to flow through.

---

### Nice to Have for Future Releases

**E3: Content file override/extension.** Allow per-repo overrides (or global layer) to specify additional content files to append to `CLAUDE.local.md`. This would support "private instructions appended to public content" workflows. Requires design of: ordering (private after public?), template expansion, conflict handling (duplicate headers).

**E4: Visible group directories from private extension.** If the private extension creates directories for groups not in the public config, those directories appear in the instance layout but aren't committed to any repo. This is fine, but the documentation should clarify that group directories are managed by niwa and may come from different config sources.

---

## Surprises

1. **`InjectExplicitRepos()` silently skips discovered repos.** If a developer mistakenly puts the same repo in both a source org (public config) and an explicit entry (private config), the explicit entry is dropped with no warning. The repo is cloned with its public config, not its private overrides. A warning or error would be better.

2. **Global config layer cannot touch sources/groups/content.** The `GlobalOverride` struct is carefully designed to exclude these fields, but it's not immediately obvious from reading override.go why. The comment at line 327 clarifies this, but it's easy to miss that content is intentionally unsupported at the global layer.

3. **Content file discovery uses convention but not merging.** `InstallRepoContent()` looks for a content file source, but if both public and private configs need to contribute to the same repo's CLAUDE.local.md, there's no mechanism — the last-source-wins, and there's no ordering guarantee.

4. **Group validation happens at injection time, not at parse time.** A repo with `group = "nonexistent"` in the explicit entries is caught during injection (line 92–94), not during config parsing. This is correct but adds latency to error discovery.

---

## Open Questions

1. **What is the actual use case for a private extension?** Is it:
   - User-specific hooks/env (answered by global config)?
   - Private repos from a different org (answered by explicit source)?
   - Sensitive repos that shouldn't be discovered (not answered — E2)?
   - Private instructions for public repos (not answered — E3)?

2. **Should the private extension be a separate `dot-niwa-private` repo, or a local `.niwa.local/` directory?** This affects where content files live and whether they're version-controlled. The DESIGN assumes `.niwa/` is a git checkout, so a separate private repo works. But merging multiple config repos is not currently supported.

3. **How should conflicts be reported?** Current approach is to error on duplicate repo names across sources. Should explicit entries in different layers that reference the same repo be allowed to coexist with merging semantics?

4. **Is the "private content" use case about CLAUDE.md content, or about hiding repos entirely?** If it's hiding, then E2 (selective exclusion from discovery) is the blocker. If it's augmenting, then E3 (content override) is needed.

---

## Summary

The niwa config merge model supports a single public workspace config layered with user-specific global overrides (hooks, env, settings), but a "private extension" that adds new repos, groups, or content files hits three hard boundaries: sources and groups cannot be merged across layers, auto-discovered repos cannot be selectively hidden, and content files are workspace-scoped and non-overrideable. The current design is sound for the intended use case (team config + user config) but requires explicit repo lists and content relocation to support a full private extension layer.

