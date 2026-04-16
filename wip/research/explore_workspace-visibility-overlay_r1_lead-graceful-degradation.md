# Lead: GitHub API behaviors and graceful degradation

## Findings

### GitHub Error Codes for Inaccessible Repos

When attempting to clone or access a GitHub repo via git:
- **Repo doesn't exist (as far as the user can see)**: GitHub returns 404 for both "doesn't exist" and "exists but you don't have access" — this is intentional to prevent enumeration attacks. From a security perspective, GitHub makes it impossible to distinguish "this repo doesn't exist" from "this repo exists but you can't see it."
- **Git clone behavior**: `git clone` of an inaccessible repo produces `fatal: repository 'https://github.com/org/repo/' not found` (even if the repo exists but is private). Via SSH: `ERROR: Repository not found` followed by `fatal: Could not read from remote repository.`
- **HTTP 401/403 vs 404**: GitHub API endpoints (not git-over-HTTPS) return 401 for unauthenticated requests to private repos and 404 for authenticated requests to repos you can't access (to prevent enumeration). This means niwa can't distinguish "no access" from "doesn't exist" via HTTP status alone.

### Niwa's Current Clone/Sync Error Handling

Based on investigation of the apply pipeline:
- `apply.go` calls clone for each classified repo; a clone failure returns an error that aborts the entire apply operation
- There is no current mechanism for "optional" repos that can be skipped on failure
- The existing `SyncRepo` (used for config sync) has a warning-and-continue pattern for fetch failures — but this is for repos that are already cloned, not initial clone operations
- `SyncConfigDir` (used for workspace config and global config sync) fails the apply if sync fails — consistent with the GlobalOverride design's explicit stance that "sync failure aborts apply"

### Graceful Degradation Requirements

For a private workspace extension, two failure modes need different handling:
1. **Extension repo doesn't exist / access denied**: This is the normal case for users without private access. Must be silent skip (no error, no warning to avoid leaking that the private extension exists).
2. **Extension repo is accessible but sync fails** (network error, corrupted clone, etc.): This should probably be treated as an error, since the user has access and expects the private config to be applied.

The problem: GitHub returns the same 404 status for both "doesn't exist" and "access denied". niwa cannot distinguish these cases via HTTP. This means:
- **Option A**: Treat any failure to clone/sync the companion as a silent skip. Simple, but a genuinely failing sync for an authorized user would be silently ignored.
- **Option B**: Two-phase: first check via GitHub API if the repo is accessible (using the token), then attempt clone. If the API check returns 404, silent skip. If API check succeeds but clone fails, error.
- **Option C**: If the companion repo was previously successfully cloned (exists in the local cache), sync failure is an error. If it was never successfully cloned, failure is a silent skip. This handles the common case: authorized users have an existing clone; unauthorized users never had one.

Option C (local cache as proxy for prior access) is the most practical: it doesn't require an extra API round-trip and naturally handles the common cases.

### UX Considerations

- **Silent skip (no message at all)**: Ideal for users without private access. The private extension's existence must not be revealed.
- **Warning on skip**: Problematic — would reveal to unauthorized users that a private extension exists ("could not access private extension, continuing without it")
- **Verbose flag**: A `--verbose` or `--debug` flag could show more detail about skipped extensions for debugging by authorized users

### Partial Application Risk

If the private extension is discovered and begins applying (sources discovered, some repos cloned) and then fails mid-way, the workspace would be in a partially applied state. This is the same risk that exists today for workspace config sync. The existing managed files + hash tracking in instance.json provides some recovery path via re-apply.

## Implications

- **Silent skip must be unconditional** when the extension is not available (GitHub's 404-for-all-inaccessible means niwa can't distinguish "doesn't exist" from "access denied")
- **Local clone presence as the degradation signal** (Option C) is the right model: if `$config_dir-private/` doesn't exist locally, attempt clone; if clone fails (404-style error), skip silently; if clone succeeds, use it on future applies
- **No warning message on skip** — the private extension's existence must not leak through UX
- **Verbose/debug output** for authorized users who need to diagnose sync failures

## Surprises

- GitHub intentionally returns 404 (not 401/403) for private repos you don't have access to, specifically to prevent repository enumeration. This is a deliberate security design that niwa must work around rather than with.
- The current `SyncConfigDir` pattern already shows that niwa treats config sync failures as fatal — but the private extension needs the opposite semantics for initial access. This creates an asymmetry in the apply pipeline.

## Open Questions

- Should niwa attempt a GitHub API probe (`GET /repos/{owner}/{repo}`) before cloning, to distinguish "truly doesn't exist" from "exists but no access"? (Two round-trips vs simpler code)
- For authorized users: after first successful clone, if a subsequent sync fails (network issue), should niwa proceed with the stale local clone or abort? The GlobalOverride design chose abort; private extension semantics may differ.
- Should there be a `niwa config set private-extension <repo>` command to explicitly register a private extension (bypassing naming convention), or is convention-only sufficient?

## Summary

GitHub intentionally returns 404 for both missing and access-denied repos, making it impossible for niwa to distinguish "no access" from "doesn't exist" via HTTP status codes. The recommended degradation model is: attempt to clone the companion repo by convention; on any clone failure for a previously-uncached extension, skip silently; once cached, treat sync failures as errors for authorized users. The key open question is whether the first clone attempt should include a GitHub API probe to provide better error messages for authorized users experiencing genuine access issues.
