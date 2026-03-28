# Security Review: Init Command Design

## Dimension 1: External Artifact Handling

**Applies: Yes**

The `--from <org/repo>` mode downloads an entire git repository and installs it as `.niwa/`. The cloned repo contains:

- `workspace.toml` -- parsed by niwa and drives workspace layout decisions
- Content files (markdown templates) -- written into repo directories as CLAUDE.md / CLAUDE.local.md by `niwa apply`

### Risks

**Malicious workspace.toml fields.** A cloned config could specify `content_dir` values with path traversal (e.g., `../../etc`). The existing `checkContainment()` in `content.go` mitigates this for content file installation, resolving symlinks and verifying paths stay within bounds. However, the init command itself doesn't parse or validate workspace.toml beyond confirming it exists. Malformed TOML won't cause harm at init time -- it would fail at `niwa apply` time.

**Content template injection.** Cloned content files are installed verbatim (with variable expansion) as CLAUDE.md files that direct AI agent behavior. A malicious config repo could include instructions that cause an AI agent to exfiltrate code, modify files outside the workspace, or run arbitrary commands. This is the primary trust boundary: the config repo author controls AI agent instructions for every repo in the workspace.

**Git hooks in cloned repo.** The `.niwa/` directory is a git checkout. If the cloned repo contains `.git/hooks/` with executable scripts, those hooks run when git operations occur inside `.niwa/`. For example, a `post-checkout` hook executes during clone. This is standard git behavior but means cloning an untrusted repo executes arbitrary code.

### Assessment

The design already identifies remote config trust as a concern and proposes `--review` for future mitigation. The git hooks vector deserves mention in the design's security section, since it means `niwa init --from` is equivalent to running arbitrary code from the source repo -- the trust decision happens before any review is possible.

**Existing mitigations that work well:**
- `checkContainment()` prevents path traversal in content installation
- Pre-flight checks prevent overwriting existing configs
- Writes are scoped to `.niwa/` during init

**Gap:** The design doesn't mention git hooks. A `--review` flag won't help here because hooks execute during the clone itself, before any review can occur. Mitigation options: (1) document that `--from` requires the same trust level as cloning any git repo, or (2) consider passing `--config core.hooksPath=/dev/null` to the clone command to disable hooks during the initial clone, then let the user inspect. Option 2 adds complexity but closes the gap. Option 1 is adequate if the threat model treats init as equivalent to `git clone` (which it is).

## Dimension 2: Permission Scope

**Applies: Yes (but minimal)**

The init command requires:

- **Filesystem write** to `$PWD/.niwa/` -- creating the config directory and its contents
- **Filesystem write** to `~/.config/niwa/config.toml` -- updating the global registry
- **Network access** for git clone (remote mode only)
- **Process execution** for `git clone` via `exec.CommandContext`

### Risks

**No elevated privileges needed.** All writes target user-owned directories. No sudo, no system paths.

**Registry write is append-only.** `SetRegistryEntry` adds/updates entries but doesn't delete them. A malicious init could overwrite a legitimate registry entry for a workspace name, causing future `niwa init <name>` to clone from a different source. This is low severity -- it requires the attacker to run commands on the user's machine, at which point they already have broader access.

### Assessment

Permission scope is appropriate. No design changes needed.

## Dimension 3: Supply Chain and Dependency Trust

**Applies: Yes**

The init command has two trust dependencies:

1. **Git binary.** Invoked via `exec.CommandContext(ctx, "git", ...)`. niwa trusts whatever `git` is on the user's PATH. If an attacker controls the PATH, they control what `niwa init --from` executes. This is standard for any tool that shells out to git and doesn't warrant special mitigation.

2. **Config repo source.** The `<org/repo>` argument resolves to a GitHub URL via the `CloneProtocol` setting. The org and repo names are validated against `[a-zA-Z0-9._-]+`, which prevents command injection through the URL. The URL construction is:
   - HTTPS: `https://github.com/{org}/{repo}.git`
   - SSH: `git@github.com:{org}/{repo}.git`

   Neither format allows injection as long as the regex validation holds.

### Risks

**Name validation is critical.** The regex `[a-zA-Z0-9._-]+` is the key defense against command injection in clone URLs. If this validation is bypassed or loosened (e.g., to support subgroups with `/`), the URL construction could become injectable. The design should note this as an invariant.

**No pinning for registry-resolved clones.** When `niwa init <name>` resolves a name via the registry and clones from the stored source, there's no version pin. The source repo's default branch HEAD is cloned. If the source repo is compromised between the first user's `--from` and a second user's name-based init, the second user gets compromised content. The design's `--ref` flag mitigates this for explicit invocations but not for registry-resolved ones.

### Assessment

The URL construction and input validation are solid. The registry-resolved clone without pinning is a known trade-off -- documenting it is sufficient since the same risk exists for any git-based dependency without pinning.

## Dimension 4: Data Exposure

**Applies: No (with one minor note)**

The init command doesn't transmit user data. It clones a public or accessible repo (outbound git fetch) and writes locally. No telemetry, no analytics, no phone-home behavior.

**Minor note:** When using HTTPS clone protocol, git may prompt for credentials or use stored credentials from the credential helper. When using SSH, the user's SSH key authenticates to GitHub. In both cases, credential handling is delegated to git, not to niwa. This is appropriate -- niwa shouldn't handle credentials.

## Summary

The init command's security profile is straightforward: it's a thin wrapper around `git clone` plus local file scaffolding. The primary risk is the trust relationship with remote config repos, which the design already identifies. Two items deserve attention:

1. **Git hooks execute during clone.** The design's security section should mention that `--from` cloning executes any git hooks in the source repo. This makes `niwa init --from` equivalent in trust level to `git clone` -- the user must trust the source. The planned `--review` flag won't protect against hooks since they run during the clone itself.

2. **Name validation as security invariant.** The `[a-zA-Z0-9._-]+` regex on org/repo names is the key defense against command injection. The design should call this out as an invariant that must be maintained if the validation is ever modified.

Neither item requires architectural changes. Both are documentation-level additions to the existing security section.

## Recommended Outcome

**2 -- Document considerations.** Add the git hooks trust note and name validation invariant to the design's Security Considerations section. No structural changes needed.
