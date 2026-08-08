# Design Summary: setup-script-visibility

## Input Context (Phase 0)

**Source:** /explore handoff (crystallize decision: Design Doc, as an in-place amendment)

**Problem:** A repo's setup script can fail on every `niwa create` and `niwa apply` and leave
no usable trace: its stdout and stderr are routed through `Reporter.Status()`, which is a no-op
off a TTY and transient-then-cleared on one, so the script's own explanation reaches the
operator in neither mode; and the failure is reported only as a deferred warning printed below
the success summary, with the command still exiting 0. Two of niwa's own design docs already
promise the output behavior that the code does not implement.

**Constraints:**

- **The design output is an amendment, not a new file.** Write into
  `docs/designs/current/DESIGN-post-clone-scripts.md` (correcting Decision 2 and Security
  Considerations in place, plus a `> **Update — ...**` blockquote in the established style),
  and correct the single contradictory `r.Status` interface comment in
  `DESIGN-clone-output-ux.md`. Do NOT create `docs/designs/DESIGN-setup-script-visibility.md`;
  the repo has no ADR convention and splitting the record across files is the drift that caused
  this issue. Do NOT add `schema:` frontmatter (only 6 of 46 design docs carry it, and adding
  it activates an unrelated hard format check).
- **Decision 2's cross-repo resilience is not reversed.** Stop-on-first-error within a repo and
  continue-to-the-next-repo both survive, with tests proving it. Decision 2 is narrowed, not
  overturned.
- **Default non-zero exit is rejected on mechanical grounds**, not on the resilience argument:
  `Create` runs `os.RemoveAll(instanceRoot)` on any pipeline error (`apply.go:430`), and
  `instance_from_hook.go:417` discards the instance path on error, fanning out to the
  SessionStart hook, `niwa dispatch`, and `niwa watch`. Default-fatal would trade a degraded
  instance for a deleted one and a never-launched worker.
- **Opt-in fatality uses `config.Action` (`warn`|`fail`), not a `--strict-setup` flag.** niwa has
  no `--strict`-anything; the warn/fail cascade already ships for the `.env.example` pre-pass on
  the same `WorkspaceMeta`/`RepoOverride` structs that carry `SetupDir`. Under `fail`, the error
  is raised after `SaveState` so the instance survives for inspection.
- **Script output must pass through the existing `secret.Redactor`** (`apply.go:1105`), since
  printing script output is a new exposure path for values materialized into `.env.local` one
  pipeline step earlier.
- **Hard content constraint:** nothing identifying the private repo where this was found may
  appear in any artifact, commit message, PR body, code comment, test fixture, or doc — no repo
  name, owner, PR or issue number, CLI name, file path, or session id. Match the public issue's
  register (`<repo>`, "a repo in my workspace"). Invent neutral examples where one is needed.
- **Out of scope:** issue #231 (setup scripts and worktrees); the downstream script's own bug;
  any wholesale rework of `Reporter`.

**Open questions for design to settle:**

- Whether `Create` returns the instance path alongside a non-zero error under `setup_failure =
  "fail"`, so the operator can still inspect what was provisioned.
- The exact prefix format for streamed lines (`[<repo>/<script>]` vs the design's
  `[<script>]` under a per-repo header) and whether the per-script pre-execution line is a
  `Log` or a `Status`.
- Where the counted summary line is emitted relative to `created/applied … (N repos)` and
  `FlushDeferred`.

## Current Status

**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-08-08
