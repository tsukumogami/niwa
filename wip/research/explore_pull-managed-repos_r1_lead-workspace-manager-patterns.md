# Lead: What do other workspace managers do to keep repos fresh?

## Findings

Research across major multi-repo management tools reveals a consistent pattern:
explicit sync commands that preserve user state.

### Tool Survey

- **Google's `repo`**: `repo sync` for multi-threaded pulling with failure tolerance.
  Does not touch dirty working trees or force branch switches.
- **`meta`**: `meta git update` clones missing repos; `meta git` commands execute
  uniformly across all repos. Preserves working tree state.
- **`gita`**: Bulk operations via command delegation (e.g., `gita pull`). Skips
  repos that can't cleanly pull.
- **`mu-repo`**: Shorthand `mu up` and `mu upd` with diff previews before applying
  changes.
- **`myrepos`**: Parallel execution via `mr update`. Configurable per-repo update
  strategies.
- **`mani`**: `mani sync` with parallel flag for concurrent operations.

### Common Patterns

1. **Explicit sync commands** -- pulling is always a deliberate user action, not
   automatic on config apply
2. **Preserve working tree** -- no automatic resets or branch switches on dirty repos
3. **Warn, don't force** -- dirty or diverged repos get warnings, not forced updates
4. **Config sync and code sync are separate** -- applying config changes (adding repos)
   is distinct from pulling code updates
5. **Parallelism for speed** -- most tools support concurrent operations across repos

## Implications

The industry consensus is clear: code freshness should be an explicit user action,
dirty repos should be warned about (not forced), and config reconciliation vs code
sync should be separate concerns. This supports either a `niwa sync` command or
an opt-in `--pull` flag on apply, rather than automatic pulling.

## Surprises

No tool surveyed makes pulling automatic on config apply. The separation between
"make config match" and "make code fresh" is universal.

## Open Questions

- Should niwa support parallel repo syncing for speed?
- Is the `repo sync` model (multi-threaded, failure-tolerant) worth the complexity?

## Summary

All major multi-repo managers use explicit sync commands that preserve user state.
The separation between config apply and code sync is universal. The biggest open
question is whether to support parallel syncing for speed.
