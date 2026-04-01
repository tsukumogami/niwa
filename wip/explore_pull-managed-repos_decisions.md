# Exploration Decisions: pull-managed-repos

## Round 1

- **Git strategy: fetch + pull --ff-only**: This is the only non-destructive combo.
  Fetch is always safe; ff-only fails cleanly. Matches proven SyncConfigDir pattern.

- **Default behavior: pull where safe, skip where not**: Only pull repos that are
  clean, on the default branch, and behind remote. Skip everything else with warnings.
  The 18-state matrix collapses to a simple rule.

- **No stash/rebase/merge in defaults**: Stashing is risky (conflicts on pop), rebase
  rewrites history, merge adds commits. All require explicit opt-in.

- **TOML drift is separate from code freshness**: Both need addressing but they're
  mechanically different. Drift detection (removed repos, URL changes) should warn
  by default with opt-in flags for remediation.

- **Shell-out pattern, no git library**: niwa already uses exec.Command for all git
  ops. No reason to add a dependency.

- **Scope: design doc is the right artifact**: Multiple design decisions remain
  (command UX, state tracking, flag surface) but the problem space is well-understood.
  A design doc resolves these before implementation.
