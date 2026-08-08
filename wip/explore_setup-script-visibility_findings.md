# Exploration Findings: setup-script-visibility

## Core Question

When a repo's setup script fails during `niwa create` or `niwa apply`, the operator gets
one deferred warning line and an exit code of 0 — the script's own stdout and stderr never
reach the terminal at all. What is the right way to make a failed setup script both visible
and discoverable, without reversing the deliberate cross-repo resilience that Decision 2 of
`DESIGN-post-clone-scripts.md` chose?

## Round 1

### Key Insights

- **The code is the outlier, not the design.** `DESIGN-clone-output-ux.md` — the design that
  actually introduced `runCmdWithReporter` — specifies `reporter.Log` for setup scripts in
  three places (lines 382-383, 393, 509: "all lines → reporter.Log", "all output →
  reporter.Log(line)") and `r.Status` in exactly one (the interface comment at 436-437).
  The implementation followed the outlier. Combined with `DESIGN-post-clone-scripts.md`
  Decision 2's "printed to niwa's output, prefixed with the repo name", *two* design docs
  already say the output should be durable. There is no recorded rationale for `Status`
  anywhere. (leads: output-routing, output-volume-ux)

- **No new Reporter method is needed.** `Log` is already the durable both-modes channel; on
  TTY it stops the spinner, clears the line, and appends permanently, and off TTY it writes
  plainly. The whole output fix lives in `runCmdWithReporter` (`internal/workspace/gitutil.go`)
  and `setup.go`. `apply.go` needs no change for this half, because `apply.go:1592` already
  routes the script error through `DeferWarn` → `FlushDeferred` → `Log`. (lead: output-routing)

- **Option 2 as a default is not merely a behavior change — it is destructive.** Step 6.75
  runs inside `runPipeline`, which writes no state. `SaveState` happens afterward in `Create`
  (`apply.go:489`) and `Apply` (`apply.go:681`). `Create` runs `os.RemoveAll(instanceRoot)` on
  *every* pipeline error (`apply.go:430`). So making a setup failure a pipeline error would
  delete the entire instance on create and leave `state.json` stale on apply. Worse, the five
  call sites include `instance_from_hook.go:417`, which fans out to the SessionStart hook,
  `niwa dispatch`, and `niwa watch` through a provisioner that discards the instance path on
  error — costing a permanently unreclaimable orphan instance (the reap backstop excludes
  hook-named instances by design), a never-launched worker, and a lost `cd` with an empty
  `--json`. (lead: exit-code-blast-radius)

- **Nothing external constrains the exit code.** No CI workflow, Makefile target, or installer
  invokes apply/create; none of the 30 functional feature files and 288 exit-code assertions
  involve setup scripts; and `apply`/`create` have no documented exit-code contract. The
  hazard is entirely internal to niwa's own create/hook path. (lead: exit-code-blast-radius)

- **The house pattern for "warn or fail" already exists and already lives on the right
  structs.** `config.Action` (`warn`|`fail`, `internal/config/env_example_policy.go`) with a
  most-specific-wins cascade shipped for the `.env.example` pre-pass, and it hangs off the
  same `WorkspaceMeta`/`RepoOverride` types that already carry `SetupDir`
  (`internal/config/config.go:281`, `:385`). Meanwhile niwa has no `--strict`-anything; it has
  three `--allow-*` escape hatches, so a `--strict-setup` flag would invert the convention.
  (lead: partial-failure-precedent)

- **Counted summary lines are already a niwa idiom**, e.g. `healed %d dangling plugin
  record(s): %s` and `apply failed for %d instances`. And the reason today's warning is
  invisible is structural: `DeferWarn` prints *below* the `created/applied <ws> (N repos)`
  summary by contract, so it reads as trailing noise rather than as an outcome.
  (lead: partial-failure-precedent)

- **The obvious regression test would pass on `main`.** Measured with a throwaway in-package
  probe: off-TTY the buffer is literally empty, but on-TTY exactly *one* line of a fifty-line
  failing script renders before `stopSpinner` erases it — and which line is a scheduling
  artifact. So `strings.Contains(buf, marker)` is green on today's `main` in TTY mode, failing
  acceptance criterion 2. The robust form asserts on a helper that keeps only
  newline-terminated segments between `\r\x1b[K` delimiters, measured to return `""` for
  today's behavior in both modes. (lead: test-coverage-shape)

- **One currently-green test actively pins the bug.** `gitutil_test.go:149`
  (`TestRunCmdWithReporter_AllLinesViaStatus`) and the `runCmdWithReporter` docstring both
  assert that all output goes through `Status`. Both must be deliberately rewritten, not
  worked around. (lead: test-coverage-shape)

- **This repo has never written an ADR** — no `docs/decisions/`, no `ADR-` format in the
  shirabe validator. It has amended a `Current` design in place seven times, using two
  established shapes: an inline `> **Update — ...**` blockquote at the revised claim
  (`DESIGN-env-example-integration.md:40`, `DESIGN-parallel-clones.md:105,148`) and a
  trailing `## Amendment <date>:` section with a pointer under `## Status`
  (`DESIGN-niwa-plugin-record-lifecycle.md:36,461`). Only 6 of 46 design docs carry a
  `schema:` frontmatter field, so adding one is optional and would drag in a hard format
  check. (lead: artifact-choice)

- **Printing script output is a genuinely new secret-exposure path.** A setup script can read
  the `.env.local` niwa materialized one pipeline step earlier and echo it. A `secret.Redactor`
  is already constructed for every apply at `apply.go:1105` and attached to the context, so
  the mitigation is available rather than hypothetical. (lead: output-volume-ux)

### Tensions

- **Stream everything vs. buffer-and-replay-on-failure.** Prior art splits on ownership of the
  output: git and direnv stream one deliberately-invoked hook; npm (`--foreground-scripts`) and
  pre-commit (`--verbose`) buffer N side-effect hooks and replay only on failure, which puts
  niwa structurally in the second camp. The routing lead recommended mirroring
  `runGitWithReporter` (buffer, attach the tail to the returned error) as the near-zero-diff
  change. Against that: both of niwa's own design docs promise streamed, prefixed output, and
  measured volume is modest — a git-hooks installer emits 2 lines, `npm install` 4,
  `pip install` 20, a verbose `go mod download -x` 139 — and because `runCmdWithReporter`
  always attaches an `io.Pipe`, every script already runs non-TTY with its tooling's progress
  rendering self-suppressed. Streaming costs the spinner one teardown per script, not per line.

- **The issue author's stated preference vs. mechanical safety.** The author would pick option 2
  (default non-zero exit). The blast-radius evidence says a default-fatal setup failure converts
  "usable-but-degraded instance" into "no instance at all" on the create and hook paths. These
  can be reconciled only by making failure-is-fatal reachable but not default.

- **Restoring promised output vs. not leaking secrets.** The design predates the secret
  materialization pipeline; honoring Decision 2 literally would print whatever a script writes,
  including values it read from a file niwa just wrote.

### Gaps

- Exact interleaving of streamed script lines with the spinner on a TTY needs implementation
  care rather than more research; no lead measured it under a chatty script.
- Whether `Create` should return the instance path alongside a non-zero error under an opt-in
  fatal mode (so the operator can still inspect what was provisioned) is unresolved and belongs
  in design.
- No public repo in this workspace has a `scripts/setup/` directory, so volume figures are by
  class rather than from niwa's own fleet.

### Decisions

Recorded in `wip/explore_setup-script-visibility_decisions.md`.

### User Focus

Running in `--auto` mode (background dispatch, no interactive author). Decisions were taken
against the evidence above and recorded with rationale, per the research-first protocol. The
dispatch brief's stated bias — "half one alone would have caught the original failure on day
one; if you can only land one thing, land that" — was treated as the priority ordering, and the
three exit-code options were treated as a genuine open question rather than a settled
requirement.

## Accumulated Understanding

niwa#239 decomposes into three pieces of work, not two, and the research changed the shape of
all three.

**The output half is smaller and better-supported than it looked.** It is not a Reporter
redesign and not even a judgment call: `DESIGN-clone-output-ux.md` already specifies
`reporter.Log` for setup scripts three times over, and the single `r.Status` mention in an
interface comment is the line the implementation happened to follow. Restoring the promised
behavior means changing `runCmdWithReporter` to route lines through `Log`, adding the
`[<repo>/<script>]` prefix and the per-script progress line both designs promise, and
rewriting one test plus one docstring that currently assert the defect is intended. Volume
does not justify buffering; the redactor already in the apply context does need wiring in.

**The exit-code half has a wrong answer that looks right.** Option 2 as stated in the issue —
"non-zero exit when any setup script failed, after still running every other repo's scripts" —
cannot be expressed by returning an error from the pipeline, because `Create` deletes the
instance on any pipeline error and the SessionStart-hook path discards the instance on any
error. Making setup failures fatal by default would trade a silent degraded instance for a
deleted one and a never-launched dispatch worker. The truth-telling exit code is still
achievable, but only as an opt-in resolved *after* `SaveState`, and niwa already owns exactly
the right vehicle: the `warn`|`fail` `config.Action` cascade sitting on the same structs as
`SetupDir`.

**The discoverability half is a one-line structural fix.** The current warning is invisible
less because it is a warning than because `DeferWarn` prints below the summary by contract.
A counted, permanent line adjacent to the `created/applied … (N repos)` summary — in the shape
of the existing `healed %d dangling plugin record(s)` idiom — puts the failure where the
operator's eye already is.

What must be written down is the exit-code reasoning, and it belongs as an in-place amendment
to `DESIGN-post-clone-scripts.md` rather than an ADR: this repo has no ADR convention, and
introducing one as a side effect of a bugfix would leave an inert precedent. Decision 2's
"warn on failure, stop on first script error" survives intact — it is being *narrowed*, from
"warn is the only behavior" to "warn is the default, fail is configurable, and either way the
outcome is counted in the summary" — plus its two factually wrong claims (stdout/stderr are
printed; script names are printed before execution) get corrected to match what the code will
finally do.

## Decision: Crystallize
