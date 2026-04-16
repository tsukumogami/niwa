# Phase 2 Research: Codebase Analyst

## Lead 1: Security Requirements

### Findings

**GlobalOverride config storage and permissions:**
- `~/.config/niwa/config.toml` stored with `0o600` file permissions (`registry.go:180`, `SaveGlobalConfigTo`)
- `ParseGlobalConfigOverride` validates `Files` destination paths and `Env.Files` source paths using `validateContentSource` semantics — rejects absolute paths and `..` components (`config.go:296–309`)
- `HooksMaterializer` (`materialize.go:77–87`) implements runtime containment checks via `checkContainment()` (`content.go:244–272`), which validates symlink escapes using `filepath.EvalSymlinks`
- Hook scripts from GlobalOverride are resolved to absolute paths in `MergeGlobalOverride` (`override.go:327–346`) before materializing, allowing source-side containment checks to be skipped for absolute paths (destination-side checks remain)

**Threat models specific to private extension:**
1. **Malicious workspace.toml pointing to attacker's companion**: If an attacker controls a workspace.toml that references a malicious `private_extension = "attacker/evil-companion"`, the companion repo could install malicious hooks. Mitigation: user controls their own `niwa config set private <repo>` registration — the companion is user-registered, not auto-discovered from workspace.toml
2. **Attacker-controlled companion adding malicious hooks**: A compromised companion repo (e.g., via supply chain) could inject hook scripts. Same trust model as GlobalOverride: the private companion is user-owned and user-registered; the user must trust their own config repos
3. **Private companion containing sensitive repo URLs**: The companion contains private repo URLs by design. These are not written to any non-repo-local file by niwa (they exist only in the companion repo itself and in cloned state). The threat is unauthorized access to the companion — mitigated by GitHub's repo visibility controls
4. **Path traversal via companion's `files` or `env.files`**: Same attack surface as GlobalOverride's `files` map. `ParseGlobalConfigOverride`-equivalent validation must be applied to private companion parsing

**Registration security:**
- `GlobalConfigSource` struct stores only the repo URL, never the local path (derived at runtime from XDG_CONFIG_HOME) — prevents stale-path attacks
- Same model should apply to private companion registration

### Implications for Requirements

- R: Private companion parsing must validate all `files` destination paths and `env.files` source paths using the same `validateContentSource` logic as GlobalOverride
- R: Hook scripts from the private companion must be resolved to absolute paths during merge (same pattern as `MergeGlobalOverride`)
- R: Private companion registration must store only the repo URL, not the local path; the local path must be derived at runtime
- R: The private companion registration file (`~/.config/niwa/config.toml`) must maintain `0o600` permissions (already true for existing config; no new requirement)
- R: niwa must not write private companion repo URLs or companion contents to any user-visible output (logs, status output) unless the user explicitly requests verbose mode

### Open Questions

- Should the PRD require a `--review` flag when registering a private companion (showing what the config will do before activation), similar to the workspace config design doc's recommendation for `niwa init`?
- Should the PRD require a `niwa config show private` command that shows the registered companion URL without revealing its contents?

---

## Lead 2: Collision Handling for Shared Source Orgs

### Findings

**discoverAllRepos behavior (apply.go:525–549):**
- Maintains a `seen` map (line 527) tracking repo names across ALL source declarations
- When a duplicate repo name is found across two source declarations, errors immediately (lines 537–540) with: `"duplicate repo name X found in orgs Y and Z; rename or use explicit repos lists to resolve"`
- There is no merge semantics — two source entries for the same org are processed independently

**What happens with shared org + explicit private list:**
- Public config: `[[sources]] org="tsukumogami"` (auto-discover, e.g., finds tsuku, koto, niwa, shirabe)
- Private companion: `[[sources]] org="tsukumogami" repos=["vision"]` (explicit, private-only)
- Result: Both source entries flow into the effective config's Sources array; `discoverAllRepos` processes sequentially and finds "vision" in the private explicit list AFTER auto-discovering it from the public source (because `tsukumogami` org has "vision" as a private repo that GitHub API returns) → duplicate error

**Workaround constraint:** If the public config uses `[[sources]] org="tsukumogami"` with a visibility-based group filter (`[groups.public] visibility="public"`), niwa still queries the GitHub API for all repos in the org and returns them — private repos are "discovered" (known to the API response) even if they match no group and are excluded with a warning. The private repos aren't managed, but their names appear in the warning output.

**True isolation requires one of:**
1. Public config uses explicit `repos` list (not auto-discovery) for orgs shared with private companion
2. Public and private configs use completely separate orgs (no org overlap)
3. A new "source merge" semantic is introduced where the same org declaration in two configs is merged rather than errored

### Implications for Requirements

- R: The PRD must define behavior when the public config and private companion both declare the same source org: error, warn, or merge
- R: The PRD must define documentation requirements explaining that teams sharing an org must use explicit repo lists in the public config for that org
- R: If the chosen approach is "merge when same org", the PRD must define the merge semantics (union of repos, which max_repos wins, etc.)
- R: The PRD must define whether the private companion is allowed to declare source orgs that also appear in the public config, or whether private companion sources must use distinct orgs

### Open Questions

- Should the PRD require a validation command (`niwa config check`) that warns when public config and private companion share a source org without explicit repo lists?
- The "source merge semantics" option (option 3) would require the most code changes but give the best UX. Is this in scope for v1 or deferred?

---

## Summary

The existing security infrastructure (path-traversal validation, `0o600` config permissions, hook path resolution to absolute paths) is directly reusable for the private companion — the PRD should mandate reuse of these patterns rather than new security mechanisms. The shared source org collision is the hardest requirement gap: today niwa errors on duplicate repo names across sources, and private companions sharing an org with the public config will trigger this error unless the PRD mandates either explicit-repo-list discipline or introduces new source-merge semantics.
