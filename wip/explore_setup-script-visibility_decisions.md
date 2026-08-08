# Exploration Decisions: setup-script-visibility

## Round 1

- **Stream setup-script output through `Reporter.Log`, do not buffer-and-replay on failure.**
  `DESIGN-clone-output-ux.md` already specifies `reporter.Log` for setup scripts in three
  places against `r.Status` in one interface comment, and `DESIGN-post-clone-scripts.md`
  Decision 2 promises printed, repo-prefixed output. The code followed the outlier; restoring
  the design is the fix, not choosing a new policy. Measured volume (2-139 lines by script
  class) does not justify diverging from the written contract, and streaming costs the spinner
  one teardown per script rather than per line.

- **Prefix every line `[<repo>/<script>]` and emit a per-script progress line before
  execution.** Both design docs promise the prefix; the Security Considerations section of
  `DESIGN-post-clone-scripts.md` additionally claims niwa "prints each script name before
  execution", which it does not. Both claims become true rather than being deleted.

- **Reject option 2 (non-zero exit by default).** Not because of the resilience argument in
  Decision 2, which stays intact, but because it is mechanically destructive: `Create` runs
  `os.RemoveAll(instanceRoot)` on any pipeline error, and `instance_from_hook.go` discards the
  instance path on error, so a default-fatal setup failure would delete a provisioned instance,
  orphan hook-created instances that `niwa reap` deliberately excludes, and turn a dispatched
  worker into one that never launches. That trades a degraded instance for no instance.

- **Adopt option 1 as the default discoverability mechanism**: a counted, permanent line
  emitted adjacent to the `created/applied <ws> (N repos)` summary rather than into the
  deferred stream. The current warning is invisible primarily because `DeferWarn` prints below
  the summary by contract; niwa already has the counted-line idiom (`healed %d dangling plugin
  record(s)`). Cheapest change that satisfies the acceptance criterion, and it is what would
  have surfaced the original failure.

- **Adopt option 3, but as `config.Action`, not as a `--strict-setup` flag.** niwa has no
  `--strict`-anything and three `--allow-*` escape hatches, so a strict flag would invert the
  house pattern. The `warn`|`fail` Action cascade already shipped for the `.env.example`
  pre-pass and already hangs off the same `WorkspaceMeta`/`RepoOverride` structs that carry
  `SetupDir`. Under `fail`, the non-zero exit is resolved after `SaveState` so the instance is
  preserved for inspection.

- **Record the decision by amending `DESIGN-post-clone-scripts.md` in place; do not write an
  ADR.** The repo has no `docs/decisions/` directory and no ADR format in its doc validator,
  but has amended a `Current` design in place seven times. Writing the first ADR as a side
  effect of a bugfix would establish a convention nobody chose. Also correct the one
  contradictory `r.Status` interface comment in `DESIGN-clone-output-ux.md` so the two designs
  stop disagreeing with each other.

- **Do not add `schema:` frontmatter to the amended design doc.** Only 6 of 46 design docs
  carry it; adding it activates a hard format check (`## Status` body must match frontmatter)
  that is unrelated to this change and would widen the diff.

- **Route script output through the existing `secret.Redactor`.** Printing script output is a
  new exposure path for values a script can read from the `.env.local` materialized one
  pipeline step earlier. The redactor is already constructed per apply at `apply.go:1105`; not
  using it would be a knowing regression introduced by a visibility fix.

- **Assert on permanent output, not raw buffer contents, in the TTY regression test.** Measured:
  a naive `Contains` assertion passes on today's `main` in TTY mode because one spinner frame
  survives briefly, which would silently violate acceptance criterion 2. The test must filter
  to newline-terminated segments outside `\r\x1b[K` delimiters.

- **Deliberately rewrite `TestRunCmdWithReporter_AllLinesViaStatus` and the
  `runCmdWithReporter` docstring.** Both currently assert the defect is intended; leaving
  either in place would leave the codebase self-contradictory after the fix.

- **Keep `#231` (setup scripts don't run for worktrees) out of scope**, per the dispatch brief.
  Noted here so a later reader knows it was considered and excluded, not missed.
