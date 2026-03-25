# Security Review: workspace-config

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes

niwa processes two categories of external input: workspace.toml config files and content source files (markdown templates). It also clones git repositories from URLs derived from config.

**Config parsing.** workspace.toml is parsed by a Go TOML library (BurntSushi/toml or pelletier/go-toml). These are mature libraries with no known injection vectors in standard usage. The parsed output maps to typed Go structs, which limits what malformed input can do. Severity: low.

**Content file template expansion.** niwa reads markdown files from the content directory and performs variable substitution using four fixed variables (`{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`). The design explicitly states no arbitrary command execution or file inclusion. However, the implementation must ensure the substitution engine doesn't interpret other patterns. If a simple string replace is used, this is safe. If a template engine (like Go's `text/template`) is used, the attack surface expands significantly -- `text/template` supports method calls and can leak data or cause panics. Severity: medium if implementation uses a template engine, low if plain string replacement.

**Git clone URLs.** Repo URLs default to `https://github.com/{org}/{name}.git` where org and name come from the config. A malicious workspace.toml could specify arbitrary URLs. Since users explicitly choose to trust a workspace.toml (they either write it or clone it from a known source), this is analogous to running a Makefile or Dockerfile -- the trust boundary is the config file itself. Severity: low, but worth documenting.

**Mitigations:**
- Use plain string replacement for template variables, not `text/template` or similar engines.
- Validate that content file source paths are relative and stay within `content_dir` (no `../../etc/passwd` traversal).
- Validate repo URLs against a scheme allowlist (https, ssh/git protocols only).

### Permission Scope

**Applies:** Yes

niwa requires filesystem write access to three locations:

1. **Workspace tree** (instance directories, CLAUDE.md files, .gitignore modifications). This is the primary working area and writes are expected. niwa creates directories, writes generated markdown files, and modifies .gitignore files inside cloned repos.

2. **User config directory** (`~/.config/niwa/`). The global registry and host config live here. No elevated permissions needed -- this follows XDG conventions and stays within the user's home directory.

3. **Git operations.** `niwa create` and `niwa apply` run `git clone` for repositories. Git clone can execute arbitrary code via hooks in the cloned repo (post-checkout hooks). This is inherent to git, not specific to niwa.

There is no network access beyond git clone/fetch. There is no process spawning beyond git. There is no privilege escalation -- everything runs as the current user.

**Hook script distribution (v0.2)** is the most sensitive operation. niwa copies hook scripts from the workspace config to `.claude/hooks/` in each repo. These hooks are then executed by Claude Code. A malicious workspace.toml could distribute hooks that exfiltrate data or modify code. Again, the trust boundary is the workspace.toml and its associated files.

**Mitigations:**
- Document that workspace.toml and its content/hook files should be treated with the same trust level as shell scripts.
- When cloning repos, consider using `git clone --no-checkout` and then checking out, or at minimum document that cloned repos' git hooks could execute code.
- For `niwa reset` and `niwa destroy`, confirm before deleting instance directories to prevent accidental data loss.

### Supply Chain or Dependency Trust

**Applies:** Yes (limited)

The design mentions `niwa init <name>` resolving configs from a registry, with the global config containing a `source` field:

```toml
[registry.tsuku]
source = "tsukumogami/niwa-tsuku-config"
root = "/home/user/dev/tsuku-root"
```

This implies niwa can fetch workspace configs from remote sources (likely GitHub repos). The trust model for this remote config is the primary supply chain concern.

**Workspace config as code.** A workspace.toml and its content directory can direct niwa to clone arbitrary repos, write files to the filesystem, and (in v0.2) install executable hooks. Anyone who controls the workspace config controls what gets installed. This is similar to how a `package.json` or `Dockerfile` works -- the config is trusted code.

**No artifact signing or pinning.** The design doesn't mention verifying the integrity of workspace configs fetched from remote sources. There's no hash pinning for the config source, no signature verification.

**Mitigations:**
- When fetching remote workspace configs, pin to a specific commit or tag rather than tracking a branch.
- Consider adding a `--review` flag to `niwa init` that shows what the config will do before registering it.
- Document that the workspace.toml source repository should be treated as trusted infrastructure.

### Data Exposure

**Applies:** Yes

Several data exposure vectors exist:

**Secrets in host config.** `~/.config/niwa/hosts/<hostname>.toml` contains bot tokens and API keys. The design correctly keeps these out of workspace.toml and recommends mode 600 permissions. The design also states niwa warns if permissions are too open. This is good.

**Environment variables.** The `[env]` section can reference `.env` files and declare inline variables. Inline vars in workspace.toml are committed to git. The design says "secrets stay in `.env` files, never in TOML" but this is a convention, not enforcement. Nothing prevents a user from putting `GH_TOKEN = "ghp_xxxx"` in `[env].vars`.

**Instance state.** `.niwa/instance.json` contains repo URLs (which could reveal private repo names), file hashes, and timestamps. This file lives inside the workspace instance. If the instance directory is shared or the state file is accidentally committed, it leaks the workspace structure.

**Template variable expansion.** The `{workspace}` variable expands to an absolute filesystem path, which gets written into CLAUDE.md files. Some of these (workspace and group level) are committed to git per the design. Committed files containing absolute paths expose the user's directory structure. This is a minor information leak.

**Mitigations:**
- Validate or warn when `[env].vars` values look like secrets (high entropy strings, known token prefixes like `ghp_`, `sk-`).
- Ensure `.niwa/` is added to `.gitignore` at the instance root.
- Consider whether `{workspace}` (absolute path) in committed CLAUDE.md files is acceptable, or whether a relative path would work.
- Enforce file permission checks on host config files at read time, not just as a warning.

## Recommended Outcome

OPTION 2: Document considerations (draft the Security Considerations section)

The design already has a Security Considerations section that covers the main points well. It should be expanded to address:

1. **Template variable implementation**: State that template expansion must use plain string replacement, not a template engine that supports code execution.
2. **Path traversal validation**: Content source paths and subdirectory keys must be validated to prevent directory traversal outside the content directory and workspace tree respectively.
3. **Trust model for workspace.toml**: Explicitly state that workspace.toml and its referenced files (content, hooks, env) should be treated with the same trust as executable scripts, since they direct file writes, git clones, and (in v0.2) hook installation.
4. **Remote config fetching**: When `niwa init` fetches from a remote source, it should pin to a specific ref and provide a review mechanism.
5. **Absolute paths in committed files**: Note that `{workspace}` expansion puts machine-specific paths in committed files, which is an intentional trade-off (Claude Code needs absolute paths for its context model).

The existing section covers secrets isolation, gitignore enforcement, content integrity, template scope, and host config permissions. These are solid. The additions above fill the remaining gaps without requiring design changes.

## Summary

The design has a sound security posture for a local developer tool. Secrets are properly isolated in host-specific files outside the workspace tree, template variables are intentionally limited, and content integrity is tracked via SHA-256 hashes. The main gaps are: path traversal validation for content source paths, the need to specify plain string replacement (not a template engine) for variable expansion, and documenting the trust model for workspace.toml as equivalent to executable code. None of these require design changes -- they're implementation constraints and documentation additions that fit within the existing Security Considerations section.
