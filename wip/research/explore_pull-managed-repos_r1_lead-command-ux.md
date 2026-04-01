# Lead: What's the right command UX?

## Findings

### Current CLI Structure
niwa has a clear command hierarchy using Cobra:
- **niwa init** [name] [--from org/repo] — scaffold local or clone remote config
- **niwa create** [--name suffix] — create new instance from config
- **niwa apply** [workspace] [--instance name] [--allow-dirty] — converge instance(s) to config
- **niwa status** [instance] — show workspace health
- **niwa reset** [instance] [--force] — destroy + recreate instance
- **niwa destroy** [instance] [--force] — remove instance

The pattern is **compositional**: each command does one focused thing. Commands accept both positional args (scope discovery) and flags (overrides). Context is detected via marker files (.niwa/workspace.toml at root, .niwa/instance.json in instances).

### Current Apply Behavior
`niwa apply` already does intelligent work:
1. Auto-pulls config from .niwa/ if it's a git repo with remote (commit 1254e32, --allow-dirty flag exists)
2. Discovers repos from config sources
3. Clones missing repos (skips existing ones)
4. Regenerates managed files (CLAUDE.md, settings.json, hooks)
5. Cleans up removed repos/groups
6. Updates instance state

But it does NOT pull updates into existing repos — they stay at their cloned commit.

### UX Options and Trade-offs

**Option A: niwa sync (separate command)**
```bash
niwa sync [--pull]        # pull latest in all repos, optional --pull
niwa sync --instance dev  # target specific instance
```
Pros:
- Explicit, discoverable, composable with other commands
- Muscle memory: niwa sync, then niwa status, then work
- Clear mental model: "sync repos" vs "apply config"
- Can offer advanced options (--rebase, --ff-only, --strategy)
- Doesn't change existing behavior of apply

Cons:
- One more command to remember (but consistent with industry: git sync, repo sync)
- Higher friction for common case (need two commands)
- Risk: users forget to sync, then complain code is stale

**Option B: niwa apply --pull (flag on apply)**
```bash
niwa apply --pull              # apply config AND pull repos
niwa apply --pull --instance dev
```
Pros:
- Lower friction: single command for "keep me fresh"
- Backwards compatible: apply without --pull still works today
- Composable: can combine with --allow-dirty, --instance
- Discoverable via help (niwa apply -h shows --pull)

Cons:
- apply already has 2 flags; adds complexity to command semantics
- --pull is ambiguous: does it pull config? repos? both?
- Design confusion: apply's core job is "converge config", pulling repos is separate concern
- Hard to document: "apply converges config, and optionally pulls repos"

**Option C: apply always pulls (default behavior change)**
```bash
niwa apply  # always pulls config + repos
```
Pros:
- Maximum simplicity: apply = make workspace current
- Zero friction: one command does everything
- Idempotent: apply is fully "converge to desired state"

Cons:
- Breaks existing behavior: users with dirty repos suddenly fail on apply
- Can't opt-out without a flag (--no-pull or --skip-pull)
- Slow: every apply pulls even when not needed
- High surprise factor: existing apply behavior changes

**Option D: niwa apply always syncs + pulls (hybrid)**
```bash
niwa apply [flags]  # apply config + pull clean repos + warn dirty
```
Most opinionated: converge everything, handle edge cases intelligently.

### Design Tension: Idempotence vs Scope

`niwa apply` is already idempotent for config convergence: running it twice produces same result. Adding pull has edge cases:
- Dirty repo: what does pull do? Error? Skip? Force?
- Non-default branch: pull anyway? Or respect branch?
- Conflicted merge: abort? Rebase instead?

Each choice makes sense for some users, not others.

### Industry Patterns
- **git repo**: `repo sync` (separate) — but it's designed for monorepos with explicit manifest
- **devcontainers**: no explicit pull; assumes volumes or refetch each session
- **Pants, Bazel**: external dependencies refreshed per build, not per session
- **Meta's Sapling (SCS)**: `sl pull` (separate) — orthogonal to workspace config
- **Terraform**: `terraform apply` does NOT refresh remote state; you must `terraform init` or `terraform refresh`

The industry leans toward **separate** for clarity, but tight integration (apply-then-pull) for convenience.

### Idempotence Analysis

Apply is idempotent today:
- Running apply twice = same config state + same repo list
- But repos don't update between runs (clone skips existing)

For pull to be idempotent:
- First apply --pull: repos at origin HEAD
- Second apply --pull: repos still at origin HEAD (pull is idempotent)
- But what if user's branch != origin/main?
  - Pull on non-default branch: fast-forward succeeds OR fails
  - If fails: idempotence broken (apply must fail or force)

This suggests **--pull must have a strategy**: --ff-only (safe, can fail), --force (always succeeds, can lose work).

### Discoverability and Pit of Success

**Pit of success** = easiest thing to do is the right thing.

Option A (niwa sync): User must discover or read docs. No automatic freshness.
Option B (niwa apply --pull): Documented in help, but tempting to omit.
Option C (apply always pulls): Automatic, but breaks on dirty repos.
Option D (apply + intelligent pull): Automatic + forgiving, but complex.

Option C/D puts users in the pit of success (apply = get fresh code) but at cost of complexity + surprise.
Option B is middle ground: documented, backward-compatible, but requires knowing about --pull.

## Implications

1. **If the goal is "zero-friction freshness"**: Option C (apply always pulls) is simplest but most risky. Option D (apply + intelligent handling) is safer but complex to document.

2. **If the goal is "clear, composable, discoverable"**: Option A (separate niwa sync) wins, but requires discipline (sync before every session).

3. **If the goal is "backward-compatible default with opt-in freshness"**: Option B (niwa apply --pull) is the safe bet. Existing workflows unaffected, new workflows opt-in.

4. **Idempotence isn't free**: Any pull strategy must handle dirty repos, non-default branches, and merge conflicts. That's at least 1-2 flags per strategy.

5. **Config and repo freshness are orthogonal**: Config pull (already in apply) and repo pull are separate concerns with separate edge cases. Coupling them makes each harder.

## Surprises

1. **Config pull already exists**: `niwa apply` already auto-pulls `.niwa/` config (commit 1254e32). So some "freshness" is automatic.

2. **No command currently exists for repo sync**: Despite being a key use case (keeping Claude sessions current), there's no `niwa sync` or `niwa pull` command yet.

3. **Status command doesn't show freshness**: `niwa status` shows repo list and drift, but not "is this repo up-to-date with origin?". So users can't easily detect staleness.

4. **Default behavior is idempotent-for-config but not for-repos**: This creates asymmetry: apply config twice = same result, apply twice doesn't refresh repos. Could be confusing.

## Open Questions

1. **What is the primary use case frequency?**
   - Every Claude session (daily+): leans toward "apply auto-pulls"
   - Weekly or less: leans toward "separate sync command"
   - Unclear from current docs

2. **How should dirty repos be handled?**
   - Error + suggest --force?
   - Skip silently?
   - Warn but continue?
   Each has UX implications.

3. **Should non-default branches be pulled?**
   - If user is on `dev`, should `niwa apply --pull` pull from `origin/dev`?
   - Or error/skip?

4. **Should status show if repos are stale?**
   - e.g., "niwa status" shows "niwa: cloned 3 days ago (outdated)"
   - Would make freshness discoverable without a separate command

5. **Is there a time-based heuristic?**
   - e.g., "apply pulls if last apply was >1 day ago"
   - Could make "apply every session" safe (avoids useless pulls)

## Summary

The current apply already auto-pulls config, so some freshness is automatic. The missing piece is repo pull. Option B (niwa apply --pull flag) is the safest path: backward-compatible, discoverable, composable, and doesn't require complex edge-case handling. Option A (separate niwa sync command) is cleaner architecturally if usage is infrequent (weekly), but requires user discipline. Option C (apply always pulls) achieves the "pit of success" but with higher risk of surprising existing users on dirty repos. The key open question is usage frequency and whether status should expose staleness to make freshness discoverable.
