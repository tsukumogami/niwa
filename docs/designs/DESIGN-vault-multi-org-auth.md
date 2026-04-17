---
status: Proposed
problem: |
  The Infisical backend relies on the CLI's global session for auth, but
  that session is scoped to one organization. Users who work across
  multiple Infisical orgs (team secrets in org A, personal secrets in
  org B) get 403 errors because only one org is reachable per session.
  Single-org users are unaffected; the fix must be additive — zero new
  ceremony for the common case.
decision: |
  Placeholder — populated by Phase 4.
rationale: |
  Placeholder — populated by Phase 4.
---

# DESIGN: Vault Multi-Org Auth

## Status

Proposed

## Context and Problem Statement

niwa's v0.7.1 Infisical backend shells out to `infisical export` and
inherits the CLI's stored session for authentication. That session is
scoped to one Infisical organization — `infisical login` creates a
single session, and switching orgs requires re-logging.

This works for the common case: a developer using one Infisical org
for both team and personal secrets. It breaks when team and personal
vaults live in different orgs — the concrete scenario driving this
design is a user who maintains secrets in the Tsukumogami org (team),
a future Codespar org (another team), and a personal org (personal
PATs). A single `niwa apply` on a tsukumogami workspace needs to
reach all three.

The exploration confirmed that `infisical export --token <jwt>` fully
bypasses the stored session on a per-command basis without mutating it.
This is the designed multi-context mechanism. The gap is that niwa
doesn't obtain or pass per-provider tokens today.

## Decision Drivers

- **Zero ceremony for single-org users.** `infisical login` once +
  `niwa apply` must keep working unchanged. No new files, no new
  config, no new flags.
- **Additive multi-org opt-in.** Multi-org users create a local
  credential file and niwa handles the rest. The file is never
  committed to any repo.
- **No new Go dependencies (R20).** Token acquisition can use the
  `infisical login --method=universal-auth --silent --plain` subprocess
  or a direct HTTP POST — both are stdlib.
- **Threat model alignment.** Per-provider credentials on disk at
  0o600 are within the PRD's accepted risk (same-user processes are
  out of scope). Short-lived JWT caching further bounds exposure.
- **Backend change must be small.** The exploration estimated ~20 lines.
  The design should confirm this stays contained.
- **CI unaffected.** CI uses `INFISICAL_TOKEN` env var, which already
  works as a per-command override. No changes needed.
