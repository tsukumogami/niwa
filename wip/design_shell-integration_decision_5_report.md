<!-- decision:start id="create-landing-directory" status="assumed" -->
### Decision: Landing directory UX for niwa create

**Context**

The predecessor tools (`newtsuku` and `resettsuku`) established UX patterns for
post-command navigation. `newtsuku` defaults to the workspace root but accepts a
`-c <repo>` flag to land in a specific repo. `resettsuku` detects the current repo
and returns there after reset.

`niwa create` currently outputs the instance root path. The design's stdout protocol
(Decision 1) will send this path to stdout for the shell function to cd to. The
question is whether create should support landing in a specific repo within the
instance, and whether `niwa apply` (the analog of resettsuku) needs landing behavior.

Research found a critical difference: `niwa apply` is non-destructive. Unlike
resettsuku (which deleted and recreated the workspace), apply updates in place. The
user's cwd stays valid throughout, eliminating the need for post-apply navigation.
The design already rejected intercepting apply in the shell function.

**Assumptions**

- Landing in a specific repo after create is a common enough operation to warrant a
  flag on create itself, rather than requiring a separate `niwa go` command. If wrong:
  the --cd flag is a small addition that can be removed if unused, and `go` covers the
  same use case.
- The repo name passed to --cd can be resolved after the classification pipeline
  completes, since the binary knows all repo paths at that point. If wrong: the
  flag would need to accept group-qualified paths (`public/niwa`) instead of bare
  repo names.

**Chosen: Instance root default with --cd flag for repo override**

`niwa create` defaults to landing at the instance root directory. A `--cd <repo>`
flag overrides the landing target to a specific repo within the newly created
instance.

Behavior:

- `niwa create --from example` -- stdout prints instance root path
  (`~/.niwa/instances/example`). Shell function cds there.
- `niwa create --from example --cd niwa` -- stdout prints the repo path
  (`~/.niwa/instances/example/public/niwa`). Shell function cds there.
- `niwa create --from example --cd nonexistent` -- stderr error, non-zero exit.
  Shell function does not cd.

The `--cd` flag resolves the repo name against the classified repo list after the
creation pipeline completes. Since repos are organized as `{instance}/{group}/{repo}`,
the binary resolves the group from the classification result. If the repo name is
ambiguous (appears in multiple groups), error with a message suggesting the qualified
form.

`niwa apply` does NOT get landing behavior. It's non-destructive — the user's cwd
remains valid. If navigation is needed after apply, use `niwa go`.

**Rationale**

This preserves the predecessor's UX for the common case (create + land in a repo)
while respecting the design's decision to keep apply out of the shell function.
The --cd flag is handled entirely in the binary — no shell function changes needed,
since the binary just prints a different path to stdout. The shell function doesn't
know or care whether the path is an instance root or a repo directory.

Alternative 2 (instance root only, use go separately) was close but forces a
two-command workflow for something the predecessor handled in one. Since the binary
already knows all repo paths after creation, adding --cd is cheap.

Alternative 4 (full predecessor match with apply interception) was rejected because
apply's non-destructive nature makes it fundamentally different from resettsuku. The
design already made this call in Decision 3.

**Alternatives Considered**

- **Instance root only, use go afterward**: create always lands at instance root; user
  runs `niwa go <workspace> <repo>` for repo-level navigation. Rejected: forces a
  two-step workflow for a common operation that the predecessor handled in one command.
  The go command exists for navigating to existing workspaces, not as a mandatory
  post-create step.

- **Interactive repo selection**: prompt the user to choose a repo after creation.
  Rejected: adds interactive complexity to what should be a scriptable command.
  The shell function should stay simple (no prompts, no fzf-style selection).

- **Full predecessor match (create --cd + apply returns to current repo)**: replicate
  both newtsuku and resettsuku behavior. Rejected: apply is non-destructive in niwa
  (unlike resettsuku's delete-and-recreate), so post-apply navigation is unnecessary.
  Intercepting apply was already rejected in Decision 3.

**Consequences**

- `niwa create` gains a `--cd <repo>` flag, adding ~20 lines to create.go (resolve
  repo path after pipeline, override stdout output)
- The stdout protocol stays unchanged — the flag just changes which path is printed
- Users familiar with `newtsuku -c` get a direct equivalent
- `niwa apply` explicitly does not participate in shell integration — this should be
  documented so users don't expect resettsuku-like behavior
- The --cd flag needs repo name resolution that accounts for group-based directory
  structure (repo "niwa" might be at "public/niwa")
<!-- decision:end -->
