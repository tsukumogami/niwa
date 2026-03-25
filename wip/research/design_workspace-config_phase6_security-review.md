# Security Review: workspace-config design (Phase 6 - Architect Review)

## Scope

Review of the Security Considerations section in DESIGN-workspace-config.md (lines 495-505). Evaluating: uncovered attack vectors, mitigation sufficiency, N/A justification accuracy, and residual risk.

## Assessment of Each Stated Consideration

### 1. Secrets isolation (line 497)

**Stated mitigation:** Bot tokens and API keys live in `~/.config/niwa/hosts/` (mode 600) or `.env` files, never in workspace.toml. The `[channels.telegram]` section holds only access rules.

**Assessment:** The architectural separation is sound -- secrets in host config, structure in workspace.toml. However, the enforcement is convention-only. The TOML schema accepts arbitrary string values in `[env].vars`, so a user can write `GH_TOKEN = "ghp_xxxx"` directly in workspace.toml and commit it. The design says "never in workspace.toml" but the schema doesn't prevent it.

**Verdict:** Mitigation is necessary but insufficient. Add a lint-time warning for high-entropy values or known token prefixes in `[env].vars`. This doesn't need to be blocking -- a warning during `niwa apply` is enough.

### 2. Gitignore enforcement (line 498)

**Stated mitigation:** niwa ensures `*.local*` is in each repo's `.gitignore` before writing `.local.md` or `.local.env` files. `.niwa/` is added to `.gitignore` at the instance root.

**Assessment:** Solid. The write-after-gitignore ordering is correct. One gap: the design says workspace-level and group-level CLAUDE.md files are committed (not `.local`), while repo-level files are `.local`. This is stated in the content hierarchy (lines 111-114) and is consistent. The gitignore pattern `*.local*` covers `.local.md` and `.local.env`.

**Verdict:** Sufficient for the stated scope. Minor note: if `.niwa/instance.json` is added to gitignore at the instance level, but the instance root itself is inside a git repo (which it is -- the workspace root holds the config repo), the `.niwa/` gitignore entry needs to be in the workspace root's `.gitignore`, not just the instance's. The design is ambiguous about which `.gitignore` gets the entry.

### 3. Content file integrity (line 499)

**Stated mitigation:** instance.json tracks SHA-256 hashes of all managed files, enabling detection of unauthorized modifications.

**Assessment:** This is drift detection, not integrity enforcement. The hash tells you a file changed; it doesn't prevent tampering or tell you who changed it. The word "unauthorized" overstates what hash tracking provides. If an attacker can modify a CLAUDE.md file, they can also modify instance.json.

**Verdict:** The mitigation is real but the framing is misleading. It detects accidental drift (user editing a managed file), not adversarial tampering. Reframe as "drift detection" rather than "integrity" to set accurate expectations. For actual integrity against adversarial modification, you'd need the hashes stored outside the workspace (e.g., in the host config), which isn't worth the complexity for this threat model.

### 4. Template expansion (line 500)

**Stated mitigation:** Plain string replacement, not `text/template` or similar engines. Only 4 declared variables, no code execution path.

**Assessment:** This is the single most important security constraint in the design. `text/template` in Go supports `{{call .Method}}` and can execute arbitrary methods on passed objects. The design correctly identifies this and constrains it.

**Verdict:** Sufficient, and correctly prioritized. Implementation must enforce this -- a code review gate should exist to prevent future contributors from "upgrading" to `text/template` for convenience.

### 5. Path traversal validation (line 501)

**Stated mitigation:** Content source paths must stay within `content_dir`. Subdirectory keys must stay within the repo directory. Reject paths containing `..` or absolute path components.

**Assessment:** Rejecting `..` and absolute paths is necessary but not sufficient on all platforms. Symlink resolution can bypass `..` filtering -- a content file at `content_dir/legit` could be a symlink to `/etc/passwd`. The design should specify that paths are resolved (via `filepath.EvalSymlinks` or equivalent) before the containment check.

Additionally, TOML allows arbitrary strings as table keys. The repo name in `[groups.public.repos.NAME]` becomes a directory component. If NAME contains path separators (`foo/../../etc`), it could escape the group directory. The design validates content source paths but doesn't mention validating repo names or group names as safe directory components.

**Verdict:** Partially sufficient. Two gaps: symlink traversal and repo/group name validation as directory components.

### 6. Trust model (line 502)

**Stated mitigation:** workspace.toml and its referenced files should be treated with the same trust as executable scripts.

**Assessment:** Correct framing. This is the right trust boundary. The analogy to Makefile/Dockerfile is apt. The v0.2 hook distribution makes this especially important since hooks become executable code run by Claude Code.

**Verdict:** Sufficient as a documented trust model. The risk is that users won't read the documentation. Consider a first-run confirmation prompt for `niwa init` from remote sources.

### 7. Remote config pinning (line 503)

**Stated mitigation:** Pin to specific commit or tag. `--review` flag to show what the config will do before registering.

**Assessment:** Good. However, the design doesn't specify what happens on `niwa apply` after initial registration. If the source field tracks a remote repo, does `niwa apply` re-fetch? If so, pinning at `init` time but tracking at `apply` time creates a TOCTOU gap. The design should clarify whether the registered config is a snapshot (copied at init time) or a live reference (re-read on each apply).

**Verdict:** Incomplete. The pinning mitigation covers `init` but the lifecycle after init is unspecified. If configs are live references, every `apply` is an opportunity for the remote to change what gets installed.

### 8. Host config permissions (line 504)

**Stated mitigation:** Create with mode 600, warn if permissions are too open.

**Assessment:** Standard practice. Warning is appropriate -- hard-failing would break workflows where permissions are managed by other tools.

**Verdict:** Sufficient.

### 9. Absolute paths in committed files (line 505)

**Stated mitigation:** Intentional trade-off, documented. Claude Code needs absolute paths.

**Assessment:** The information leak is real but low-severity (directory structure, username). The design correctly calls it out as intentional. One consideration: if multiple developers share a workspace config repo, their different absolute paths would create merge conflicts in committed CLAUDE.md files. This is more of a usability issue than a security issue, but it's worth noting.

**Verdict:** Sufficient as documented. The merge conflict angle is a design consequence, not a security gap.

## Uncovered Attack Vectors

### A. Hook script injection via content files (not addressed)

Content files are markdown, but they're written to locations where Claude Code reads them as context. A malicious content file could contain instructions that manipulate Claude Code's behavior -- effectively a prompt injection via CLAUDE.md content. This is different from hook distribution (which the design addresses). The attack surface is: anyone who can modify the content source directory can influence what Claude Code does in every repo.

**Severity:** Medium. The trust boundary (workspace.toml trust) nominally covers this, but the design doesn't call it out explicitly. Content files are treated as passive data when they're actually active instructions.

### B. Instance enumeration information leak (not addressed)

Instance discovery scans immediate subdirectories for `.niwa/` markers. If the workspace root is accessible to other users on a shared machine, they can enumerate workspace instances and read instance.json (which contains repo URLs, potentially including private repo names). The design assumes single-user machines.

**Severity:** Low in practice (most developer machines are single-user). Worth a sentence noting the assumption.

### C. Race condition in gitignore-then-write (not addressed)

The design says niwa ensures gitignore entries exist before writing sensitive files. If `niwa apply` is interrupted between the gitignore check and the file write, or if another process (e.g., `git add .`) runs concurrently, the sensitive file could be staged without gitignore protection. This is a narrow window and unlikely in practice.

**Severity:** Very low. Not worth mitigating, but worth noting as a known limitation.

### D. Git clone URL scheme (not addressed)

The default URL template is `https://github.com/{org}/{name}.git`, but the `url` override in RepoConfig accepts arbitrary strings. A malicious config could specify `file:///` URLs (reading local repos), or SSH URLs to arbitrary hosts. The design's trust model covers this implicitly, but there's no scheme validation.

**Severity:** Low (covered by trust model). A URL scheme allowlist (https, ssh, git) would be a cheap defense-in-depth measure.

### E. TOML key injection in group/repo names (not addressed)

Group names and repo names become filesystem paths. TOML allows most strings as keys, including ones with path separators, null bytes, or other characters that are unsafe in filenames. The design validates content source paths but doesn't mention validating the names that become directory components.

**Severity:** Medium. A repo name like `../../.ssh/authorized_keys` would escape the workspace tree. This should be validated.

## Residual Risk Assessment

### Must escalate (blocking)

1. **Repo/group name validation as directory components** (Vector E): Not addressed anywhere in the design. A repo name containing path separators creates a path traversal that bypasses the content-path validation. Add validation: group names and repo names must match `[a-zA-Z0-9._-]+` (or similar safe charset).

2. **Symlink traversal in content paths** (under item 5): The `..` rejection is necessary but not sufficient. Add `filepath.EvalSymlinks` before containment check.

### Should document (advisory)

3. **Remote config lifecycle after init** (under item 7): Clarify whether registered configs are snapshots or live references. If live, every `apply` needs the same trust verification as `init`.

4. **Content files as active instructions** (Vector A): The trust model section should explicitly state that content files are AI instructions, not passive documentation, and carry corresponding risk.

5. **Secret detection in env vars** (under item 1): A warning for known token patterns in `[env].vars` would catch the most common mistake.

### Accept as known limitations

6. Gitignore race condition (Vector C)
7. Absolute path information leak (item 9) -- already documented
8. Instance enumeration on shared machines (Vector B)

## Summary Table

| Item | Status in Design | This Review's Verdict |
|------|------------------|-----------------------|
| Secrets isolation | Addressed | Convention-only; add lint warning |
| Gitignore enforcement | Addressed | Sufficient; clarify which .gitignore |
| Content integrity | Addressed | Reframe as drift detection |
| Template expansion | Addressed | Sufficient; critical to enforce |
| Path traversal | Addressed | Incomplete: symlinks, name validation |
| Trust model | Addressed | Sufficient; extend to content files |
| Remote pinning | Addressed | Incomplete: post-init lifecycle |
| Host permissions | Addressed | Sufficient |
| Absolute paths | Addressed | Sufficient |
| Repo/group name validation | Not addressed | Blocking gap |
| Content as AI instructions | Not addressed | Should document |
| Git URL scheme validation | Not addressed | Defense-in-depth; low priority |
