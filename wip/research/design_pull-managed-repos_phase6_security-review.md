# Security Review: pull-managed-repos design

Reviewed: DESIGN-pull-managed-repos.md, phase5 security analysis, existing
codebase (clone.go, configsync.go, apply.go, setup.go, override.go, config.go).

## Assessment of Stated Considerations

The design's seven security claims are accurate. The analysis below confirms
each and identifies gaps.

### 1. Same git credentials as clone -- CONFIRMED

`exec.CommandContext` inherits the process environment. No credential handling
code exists in niwa. The pull operation contacts the same remotes already
configured in cloned repos. No new auth surface.

### 2. --ff-only prevents merge commits -- CONFIRMED

Fast-forward-only is the correct non-destructive strategy. It prevents merge
driver execution, custom merge strategies, and merge commit creation. On
divergence it fails with a non-zero exit code rather than producing unexpected
state.

### 3. No rebase or stash -- CONFIRMED

The design explicitly excludes these. The dirty-repo skip guard means niwa
never needs to stash. No rebase means no history rewriting.

### 4. exec.CommandContext inheriting user config -- CONFIRMED

Consistent with the existing Cloner and SyncConfigDir patterns. Context
propagation enables cancellation via signals, which is correct.

### 5. No credentials stored or logged -- CONFIRMED

Inspected all exec.Command call sites. Stdout/stderr are connected to the
user's terminal. No credential capture or logging.

### 6. Repo paths from TOML under user control -- CONFIRMED with note

Paths derive from TOML group names and repo names. The clone step validates
path creation via `os.MkdirAll`. For pull, the repo directory must already
exist with a `.git` directory (checked during clone-or-skip). No path
traversal risk beyond what the user configures in their own TOML.

### 7. Post-merge hooks -- CONFIRMED, adequate documentation

The design correctly identifies this as equivalent to the existing post-checkout
hook trust model during clone.

## Attack Vectors Not Considered

### A. TOCTOU between state check and pull (Low severity, no action needed)

Between `git status --porcelain` returning clean and `git pull --ff-only`
executing, a user process could dirty the working tree. The pull would still
succeed (git pull doesn't re-check cleanliness). This is a cosmetic concern
only: the pulled content is from the same trusted remote, and any conflicts
with the concurrent modification would surface as working tree conflicts
visible to the user. Not exploitable -- the "attacker" is the user's own
processes.

### B. Symlink targets inside repo directories (Low severity, no action needed)

If a managed repo contains symlinks pointing outside the repo, `git pull`
updating those symlink targets is standard git behavior. niwa doesn't create
or follow symlinks itself. This is the same trust posture as clone. Git's
own `core.symlinks` config controls this behavior. Not a new surface.

### C. Git config injection via repo-level .gitconfig (Low severity, no action needed)

A malicious commit could add or modify `.gitconfig` or `.gitattributes` in a
pulled repo. However, git does not automatically load repo-level `.gitconfig`
files (only `.git/config`). `.gitattributes` can influence merge drivers and
diff filters, but `--ff-only` means merge drivers never execute during pull.
Diff filters are irrelevant to the pull operation. Clean-smudge filters
defined in `.gitattributes` do execute during checkout (which happens during
ff-only pull), but this is also true during the initial clone. Same trust
model.

### D. Concurrent niwa apply invocations (Low severity, no action needed)

Two `niwa apply` processes running simultaneously could both fetch and pull
the same repo. Git handles concurrent operations through lockfiles
(`index.lock`), so one would fail and produce a warning. The design's
non-fatal error handling for pull failures covers this case adequately.

### E. Fetch from a compromised remote (Out of scope)

If a remote is compromised, fetched objects could contain malicious content.
This is not specific to the pull feature -- the initial clone has the same
risk. Git's SHA-based object verification prevents tampering of objects after
they are fetched, but cannot prevent a compromised remote from serving
malicious-but-valid objects. This is a supply chain concern above niwa's
trust boundary.

### F. Denial of service via large fetch (Low severity, no action needed)

A remote could serve very large objects during fetch, consuming disk space.
The `exec.CommandContext` with context cancellation provides a timeout
mechanism if the caller sets a deadline. The design doesn't specify fetch
timeouts, but this is also true of the existing clone operation. Same risk
profile.

## Mitigations Assessment

The design's mitigations are sufficient for the threat model:

| Mitigation | Threat Addressed | Adequate? |
|------------|-----------------|-----------|
| --ff-only | Unattended merges, merge driver execution | Yes |
| Dirty-skip | Data loss from overwriting uncommitted work | Yes |
| Branch-check | Unintended pulls on feature branches | Yes |
| --no-pull flag | Environments where network or pull is unwanted | Yes |
| Non-fatal fetch errors | Network failures don't block pipeline | Yes |
| Config-first branch resolution | No remote queries, no TOCTOU on branch name | Yes |

One gap in mitigation coverage: the design doesn't mention what happens if
`git fetch origin` hangs (DNS resolution, slow remote). The existing
`SyncConfigDir` code (configsync.go:41) uses `exec.Command` without a
context, so it has the same gap. The new code uses `exec.CommandContext`,
which is better -- if the CLI's context has a timeout or responds to
SIGINT, fetch will be cancelled. This is adequate for interactive use but
worth noting for automated/CI use of `niwa apply`.

## Residual Risk

No residual risk requires escalation. All identified vectors are either:
- Equivalent to the existing clone trust model (B, C, E, F)
- Not exploitable in practice (A, D)
- Mitigated by existing design choices (ff-only, dirty-skip, branch-check)

The design's security posture is sound. The only actionable suggestion is
to ensure the implementation uses `exec.CommandContext` (not bare
`exec.Command`) for all new git operations, which the design already
specifies. The existing `SyncConfigDir` uses bare `exec.Command` -- that's
a pre-existing gap unrelated to this design.

## Summary

Security considerations in the design are accurate and complete for niwa's
trust model. Six additional vectors were analyzed; all fall within the
existing clone trust boundary or are not exploitable. No changes to the
design are needed. No residual risk requires escalation.
