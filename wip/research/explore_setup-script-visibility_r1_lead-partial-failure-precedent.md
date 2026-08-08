# Lead: What precedent does niwa already set for "partial failure" reporting, and is there an existing summary or exit-code convention to match?

## Findings

### 1. What a successful `niwa apply` actually prints

The whole apply/create output surface is `*Reporter` writing to **stderr**
(`internal/cli/apply.go:147`: `applier.Reporter = workspace.NewReporterWithTTY(os.Stderr,
!noProgress && term.IsTerminal(int(os.Stderr.Fd())))`; `NewApplier` defaults to
`NewReporter(os.Stderr)` at `internal/workspace/apply.go:173`). The only stdout writer on
these paths is `niwa create --json` (`internal/cli/create.go:270-282`), which emits exactly one
JSON object `{name, number, path}` and returns before the human hints.

There is no "summary block". There is exactly **one summary line**, emitted by the
command-level function, followed by a flush of everything that was deferred during the
pipeline. `Applier.Apply` (`internal/workspace/apply.go:685-694`):

```go
n := len(result.repoStates)
if n == 1 {
    a.Reporter.Log("applied %s (1 repo)", filepath.Base(instanceRoot))
} else {
    a.Reporter.Log("applied %s (%d repos)", filepath.Base(instanceRoot), n)
}
for _, w := range result.warnings {
    a.Reporter.DeferWarn("%s", w)
}
a.Reporter.FlushDeferred()
```

`Applier.Create` is structurally identical (`apply.go:493-502`), with the line
`created %s (N repos) → %s`.

So the exact shape of a run is:

```
<inline Reporter.Log / Reporter.Warn lines emitted during the pipeline, in pipeline order>
applied myws (3 repos)                       <- the summary line
warning: <deferred #1>                       <- FlushDeferred, in the order Defer/DeferWarn was called
warning: <deferred #2>
note: <deferred informational>
```

Two properties matter for this exploration:

- **Deferred warnings land strictly *below* the summary line.** That is
  `FlushDeferred`'s documented contract (`internal/workspace/reporter.go:162-164`:
  "Call after the operation summary line so messages appear as a clean block below the
  summary"). So a setup-script failure already prints *after* `applied myws (3 repos)`.
  The reader sees a success line first and a warning second.
- **`result.warnings` is appended to the deferred queue only at the very end**, so
  pipeline-time `DeferWarn` calls (which is where the setup-script failure lives) come
  *first* in the flushed block, ahead of the classify/content warnings.

There are exactly two `FlushDeferred` call sites in the whole codebase
(`apply.go:502` in Create, `apply.go:694` in Apply). Nothing else flushes; `Reporter.deferred`
is per-Reporter and the CLI holds one Reporter across the whole instance loop, so deferred
messages accumulate across instances until the next instance's summary flushes them.

`FlushDeferred` is called **before** `Apply` returns nil, and `runApply` continues to the
registry update and then `return nil` — the deferred queue never influences the return value.

### 2. Every subsystem in the apply pipeline that can partially fail, classified

| Subsystem | Site | Class |
|---|---|---|
| Global config snapshot sync | `apply.go:808-810` | **Warn then fatal** — warns *and* returns the error (belt and braces) |
| Registered overlay sync | `apply.go:797` | **Fatal** — `workspace overlay sync failed. Use --no-overlay to skip.` |
| Convention-discovered overlay (not registered) | `apply.go` discovery branch | **Silent skip** — a clone that fails on a never-before-seen overlay is not an error |
| Overlay advanced since last apply | `apply.go:914` | DeferWarn |
| Vault provider unreachable | `apply.go:1156-1159` | **Warn** (inline, not deferred) — "falling back to local-file and cli-session credentials" |
| Vault key missing, `?required=false` | `internal/vault/resolve/resolve.go:527-529` | **Silent** |
| Vault key missing, `--allow-missing-secrets` | `resolve.go:531-535` | **Warn** (direct `fmt.Fprintf` to the resolver's stderr, which is the Reporter's `Writer()`) |
| Vault key missing, default | `resolve.go:538-543` | **Fatal**, with the remediation flag named in the message |
| `*.required` misses | `internal/workspace/required.go:39` | **Fatal**, explicitly *not* downgraded by `--allow-missing-secrets` |
| Credential audit lines | `credentialpool.go:585-615` | Informational `Log` |
| Clone failure (any repo) | `apply.go:1338-1341` | **Fatal and fail-fast** — first error cancels the context, all remaining clones abort, `return nil, cloneErr` |
| Clone succeeded after retries | `apply.go:1343` | DeferWarn |
| Repo skipped for sync (dirty / off default branch) | `apply.go:1346` (`r.syncWarn`) | DeferWarn |
| Content install (`InstallRepoContent`) warnings | `apply.go:1442-1444` → `allWarnings` → `apply.go:691` | DeferWarn, after the summary |
| Content install error | `apply.go:1439-1441` | **Fatal** |
| Materializers (`runRepoMaterializers`) | `apply.go:1533-1535` | **Fatal** |
| `.env.example` pre-pass detection | policy-resolved, default `warn` | **Configurable warn/fail** — see §4 |
| `gitexclude.EnsureRepoExclude` | `apply.go:1541-1543` | **Fatal, deliberately** — comment: "Fail closed: a repo we cannot keep clean surfaces the error rather than leaking niwa-authored files." Not-a-git-repo is a silent no-op (`internal/gitexclude/gitexclude.go`, `errNotGitRepo` branch) |
| Worktree enumeration failure | `apply.go:1829` | DeferWarn, refresh nothing, apply still succeeds |
| Worktree missing / attached / detached | `apply.go:1854`, `:1861`, `:1870` | DeferWarn + skip (+ forward-carry for the live ones) |
| Worktree env refresh failure | `apply.go:1894` | DeferWarn + forward-carry |
| Worktree file hash failure | `apply.go:1902` | DeferWarn |
| Worktree for a removed repo | `apply.go:1841-1848` | **Silent skip** (documented as "not an edge state") |
| Worktree-delegation probe: `os.Executable` failure | `apply.go:1478` | DeferWarn + deny fallback |
| Worktree-delegation deny fallback active | `apply.go:1491` | **Warn** every apply (a current-state condition), plus a one-time `Log` explainer at `:1494` |
| Managed-file drift | `apply.go:556`, `:560` | DeferWarn |
| **Setup scripts** | `apply.go:1590-1595` | **DeferWarn only** — exit code untouched |
| Plugin-record heal failure | `apply.go:1676` | DeferWarn ("fail-safe") |
| Marketplace registry reconcile failure | `apply.go:1719` | DeferWarn |
| Plugin install failure (read-only `$HOME` etc.) | `internal/cli/dispatch_plugins.go:103` | Warn; design says explicitly "apply still returns 0" |
| Managed-file removal failure during cleanup | `apply.go:1757` | DeferWarn |
| Instance-level apply failure (CLI loop) | `internal/cli/apply.go:263-273` | **Collected, printed inline as `error: applying to <path>: <err>`, then combined into a non-zero return** |

The setup-script step is the only subsystem in the pipeline that runs *external
repo-supplied code* and it is also the only one whose failure produces neither a fatal
error nor any visible reproduction of what went wrong — `runCmdWithReporter`
(`internal/workspace/gitutil.go:83-113`) routes every script line through `r.Status`,
which returns immediately when `!r.isTTY` (`reporter.go:62-64`).

### 3. Is there an articulated principle for fail-closed vs. warn?

There is no single ADR or CLAUDE.md rule. There are three articulated positions, in
different design docs, that together form the house doctrine:

**(a) Config-wrong vs. execution-failed.** `docs/designs/current/DESIGN-plugin-installation.md:158-163`:

> **Error severity:** `repo:` resolution errors are **config errors** (typos, missing
> repos) not transient failures. They are fatal -- the pipeline stops and reports the
> error. This is distinct from CLI execution failures (marketplace registration, plugin
> install) which are non-fatal warnings. The distinction: if the config is wrong, stop
> early with a clear message. If the config is right but a CLI command fails, warn and
> continue.

This is the clearest statement of the rule and, read literally, it puts a failing setup
script on the warn side (the config is right; the invoked thing failed). But it was
written about *niwa's own* auxiliary calls, not about repo-supplied provisioning code
that the operator is depending on to make the instance usable.

**(b) Fail closed when the failure silently breaks a guarantee.** `PRD-repo-git-invisibility.md`
R9: "If niwa cannot record coverage (the coverage location is unwritable), niwa fails the
apply with a clear error rather than silently proceeding to leave niwa-authored files
visible", with the acceptance criterion "`niwa apply` exits with a non-zero status".
`DESIGN-repo-git-invisibility.md:151` restates it as "callers treat that error as fatal to
the operation (fail closed, R9)". The driver here is not severity — it's *invisibility of
the consequence*. A warning nobody reads leaves secrets committable, so it is not enough.

**(c) Error/warning visibility must not regress.** `DESIGN-clone-output-ux.md`, Decision
Drivers: "**Error/warning visibility must not regress.** Warnings and errors must always
appear clearly, even when a status line is active. ... Git subprocess errors must surface
via the goroutine pipe, not be silently swallowed." That driver belongs to the very design
that introduced the current routing.

Nothing anywhere says "niwa may report success while a step failed." The closest thing to
an explicit exit-0-on-failure commitment is `DESIGN-config-source-discovery.md:682-683`,
about plugin install: "Install failures (read-only `$HOME`, etc.) warn-and-continue: niwa
logs the failure with the manual install command but apply still returns 0" — and that is
about niwa's own optional convenience install, not about the repo's provisioning.

### 4. Existing counted-failure lines, `--strict`-style flags, and config keys of the right shape

**Counted lines exist and have a house format.** niwa already emits counted, named
outcome lines through `Reporter.Log`, inline (not deferred):

- `apply.go:1685-1687`: `healed %d dangling plugin record(s): %s` / `healed %d dangling plugin record(s)`
- `apply.go:1723`: `updated auto-update policy for %d marketplace(s): %s`
- `internal/workspace/destroy.go:203`: `pruned %d plugin record(s) across %d plugin(s) for %s`
- `internal/cli/apply.go:397`: `apply failed for %d instances: %w` (`combineInstanceErrors`)

The idiom is `<verb> <N> <noun>(s): <comma-joined names>`, with a nameless variant when the
list is long or unavailable. The summary lines themselves use the other idiom — an explicit
singular/plural branch (`(1 repo)` vs `(%d repos)`, `apply.go:495-497`, `:687-689`). Both
are established; a new "N repos did not finish setup: a, b" line fits the first cleanly.

**There is no `--strict` flag anywhere in the CLI.** A full sweep of `BoolVar`/`Bool(`
registrations across `internal/cli/` turns up zero. The house pattern is the exact
**inverse**: niwa defaults to strict and ships `--allow-*` escape hatches —
`--allow-dirty`, `--allow-missing-secrets`, `--allow-plaintext-secrets`
(`internal/cli/apply.go:22-28`). Each is documented as one-shot and non-persistent. An
opt-in `--strict-setup` would be the first flag in the CLI whose default is the lenient
side, and would read backwards against three siblings on the same command.

**There is a directly analogous config key, and it is the strongest precedent in the repo.**
`internal/config/env_example_policy.go` defines:

```go
type Action string
const (
    ActionWarn Action = "warn"   // emits a diagnostic and proceeds
    ActionFail Action = "fail"   // accumulates an error that aborts apply
)
```

with `UnmarshalText` rejecting anything else (`env_example_policy.go:21-29`), and an
`EnvExamplePolicy` struct carried at three config positions: `WorkspaceMeta.EnvExamplePolicy`
(`config/config.go:302`), `RepoOverride.EnvExamplePolicy` (`:392`), and `GlobalOverride`.
Resolution is a documented most-specific-wins cascade — per-repo vars → inline annotation →
per-category per-repo → workspace → global → **default warn** (`env_example_policy.go:114-119`).
`--allow-plaintext-secrets` downgrades all of it to warnings for one run.

That is the same question this exploration is asking, already answered once, with a named
type, a config key, a cascade, and a functional feature file
(`test/functional/features/env-example-failure-policy.feature`) whose scenarios assert
`Then the exit code is 0` for warn and non-zero for fail.

**Where a `strict_setup`-shaped key would live.** `WorkspaceMeta` (`config/config.go:276-307`)
and `RepoOverride` (`:377-397`) already carry the setup-script config: `SetupDir string`
(`:281`) and `SetupDir *string` (`:385`). They also demonstrate both idioms available —
`*bool` for tri-state opt-outs (`ReadEnvExample`, `:298` and `:388`) and `*EnvExamplePolicy`
for a warn/fail action. A `setup_policy = "warn" | "fail"` reusing `config.Action` next to
`SetupDir` at both positions would need no new type and no new resolution shape.

There is **no** existing counted-failure line for repos, no "N repos skipped" summary, and
no aggregate warning count. `Reporter` does not even track how many messages it deferred
(`reporter.go:29`, a plain `[]string`).

### 5. How the repo records a decision that revises an earlier design doc

**There is no ADR convention in niwa.** `docs/decisions/` does not exist; `find docs -name
"ADR-*"` returns nothing. The workspace-level `tsukumogami:decision-record` skill describes
`docs/decisions/ADR-*.md`, but niwa has never used it. The artifact tree is
`docs/{briefs,designs,guides,prds,spikes}` with `designs/{current,archive}`.

**Status lifecycle.** Across 55 design docs the only two frontmatter values in use are
`status: Current` (46) and `status: Superseded` (9). Superseded docs live in
`docs/designs/archive/`; Current ones in `docs/designs/current/`. Docs carry a `schema:`
field once they opt into validation, and an `upstream:` field pointing at the PRD/BRIEF that
drove them (e.g. `DESIGN-env-example-failure-policy.md` has
`upstream: docs/prds/PRD-env-example-failure-policy.md`).

**Enforcement.** Two GitHub workflows, both delegating to shirabe reusable workflows pinned
at `@main`:

- `.github/workflows/lifecycle.yml` → `tsukumogami/shirabe/.github/workflows/lifecycle.yml@main`.
  Runs `shirabe validate --lifecycle .` over the whole tree. **DRAFT PRs run in draft
  posture** (mid-chain states pass); **READY PRs run in ready posture and require single-pr
  chains to be at their terminal state: PLAN deleted, BRIEF/PRD `Done`, DESIGN `Current`.**
  It re-triggers on `ready_for_review` and `converted_to_draft`.
- `.github/workflows/validate-docs.yml` → shirabe's per-file validator, changed-files-only,
  skipping docs with no `schema:` field.

The `docstatus` transition itself is a workspace skill (`/tsukumogami:docstatus`), not a
niwa-local script.

**The established pattern for revising a Current design's decision is an in-place
blockquoted amendment that points at the new artifact — the old doc stays `Current` and
stays in `current/`.** The canonical example is
`docs/designs/current/DESIGN-env-example-integration.md:40-51`:

```markdown
## Status

Current

> **Update — superseded failure handling (env-example failure policy).**
> Decision 1's "probable secret -> hard error" default and the
> all-or-nothing `read_env_example` opt-out as the only control have been
> superseded. Detections now warn by default; the fail-versus-warn response
> is an opt-in, per-category policy resolved at user, project, and variable
> granularity, and the public-remote special case is removed from the
> pre-pass. The integration mechanism in this document (the pre-pass,
> parser, classifier, and config plumbing) still stands -- only the response
> to a detection changed. See
> `docs/designs/current/DESIGN-env-example-failure-policy.md` and
> `docs/prds/PRD-env-example-failure-policy.md`.
```

Note the shape precisely: it names *which decision* is superseded, states *what replaced
it*, explicitly scopes what still stands, and links both the new design and its PRD. The
whole-document `status: Superseded` + move to `archive/` is reserved for docs whose
mechanism is gone entirely (the nine archived mesh/coordinator docs). A second variant
exists for revising reasoning inside a doc without a new doc:
`DESIGN-ephemeral-session-instances.md:328` keeps the old paragraph under
`> **Original (superseded) reasoning, retained for the audit trail:**`.

## Implications

1. **Reuse, don't invent.** The `.env.example` failure policy already answers "warn or
   fail?" with a named `config.Action` type, a `warn|fail` TOML value, a most-specific-wins
   cascade over workspace/per-repo/global, a documented default of `warn`, and a one-shot
   flag downgrade. A `setup_policy = "warn" | "fail"` key on `WorkspaceMeta` and
   `RepoOverride`, sitting next to the `SetupDir` fields that already govern this feature,
   reuses that type verbatim and costs no new resolution machinery. Option 3 ("opt-in
   `--strict-setup` flag") is the right *idea* expressed in the wrong *mechanism*: niwa
   has no `--strict` flag and three `--allow-*` flags, so the policy belongs in
   `workspace.toml`, not on the command line.

2. **Options 1 and 2 are not exclusive, and option 1 has an existing format.** The
   counted-line idiom `<verb> <N> <noun>(s): <names>` is already used three times in the
   apply path. "1 repo did not finish setup: <name>" is a natural instance of it. But note
   *where* it must go: the summary line is emitted by `Applier.Apply`/`Create`
   (`apply.go:687`, `:495`) and the deferred block flushes below it. A counted line printed
   through the current `DeferWarn` path lands below `applied myws (3 repos)`, which is the
   same position the existing warning already occupies. To be discoverable it either has to
   modify the summary line itself or be `Log`ged inline like the `healed %d` line — the
   deferred channel is exactly the channel that failed to make this visible.

3. **The exit-code precedent is more favorable to option 2 than it first looks.** Every
   pipeline step whose failure silently breaks a promise the operator is relying on already
   fails the apply: clone (fatal, fail-fast, cancels siblings), materializers (fatal),
   content install (fatal), gitexclude (fatal *by explicit design*, R9, with a non-zero-exit
   acceptance criterion), required secrets (fatal, non-downgradeable). Warn-and-continue is
   reserved for things that are *auxiliary* — plugin installs, registry heals, worktree
   env refreshes for worktrees that are locked by someone else. The question this
   exploration has to answer is which of those two categories a repo's own provisioning
   script belongs to, and the honest answer is that it depends on the workspace: in a
   many-repo workspace it is auxiliary, in the single-repo case that motivated the issue it
   is load-bearing. That is precisely the shape of question the `.env.example` policy
   resolved with a cascading config key rather than a global default.

4. **The CLI already has a working model for "run everything, then exit non-zero."**
   `internal/cli/apply.go:263-273` collects per-instance failures, prints
   `error: applying to <path>: <err>` inline as each occurs, continues to the next
   instance, and returns `combineInstanceErrors` at the end — which produces
   `apply failed for %d instances: ...`. Option 2 ("non-zero exit after every repo has
   run") is that exact structure moved one level down, into the per-repo setup loop. It
   would not be a new convention.

5. **The artifact question (Lead 6) is settled by precedent.** niwa has no ADR mechanism, so
   an ADR would be a fourth convention. The house move is: write the new chain
   (BRIEF/PRD as warranted → DESIGN) for the revision, and amend
   `DESIGN-post-clone-scripts.md` in place with a blockquoted `> **Update — ...**` note
   directly under `## Status`, naming Decision 2, saying what still stands (directory
   convention, lexical order, stop-on-first-error-within-repo, continue-to-next-repo) and
   what changed (visibility of output, and the failure response), and linking the new doc.
   The doc stays `Current` and stays in `docs/designs/current/`. Anything else risks the
   ready-posture lifecycle gate, which requires DESIGN docs at `Current` for a
   ready-for-review PR.

## Surprises

**`DESIGN-post-clone-scripts.md` is not the only doc the implementation diverges from —
`DESIGN-clone-output-ux.md` contradicts itself, and the implementation followed the wrong
half.** The doc specifies `runCmdWithReporter` three times and says two different things:

- Components table, `gitutil.go` entry: "runCmdWithReporter: same pipe pattern, all lines
  **via reporter.Log**, no classifier (setup scripts)" (line ~381)
- Components table, `setup.go` entry: "uses runCmdWithReporter (all lines → **reporter.Log**;
  no git-specific classifier for arbitrary scripts)" (line ~392)
- Implementation Plan: "`runCmdWithReporter`: for non-git subprocesses (setup scripts). No
  classifier — all lines route through **`r.Log`**." (line ~575)
- Key Interfaces code block: "No line classification — all output through **`r.Status`**
  (transient; silent in non-TTY/CI contexts)." (line ~436)

Three statements say `Log` (a permanent line, printed in both TTY modes); one says `Status`
(transient, no-op off-TTY). The shipped code is the `Status` variant
(`internal/workspace/gitutil.go:85`, doc comment reproducing the `Status` wording verbatim).
So the output half of niwa#239 is a divergence *within a single design doc*, resolved
against the doc's own majority text and against its own stated decision driver ("Error/warning
visibility must not regress"). This strengthens the case that the output fix is a
plain-bug reconciliation needing no new decision, and that the amendment to
`DESIGN-clone-output-ux.md` is a one-line correction of the Key Interfaces block, separate
from the exit-code decision.

**The design that introduced the Reporter also records that `setup.go` used to print
directly to stderr.** `DESIGN-clone-output-ux.md:44-46` lists `setup.go` among the six
subprocess pipes that used `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` before the
refactor. Setup-script output was visible in every context — TTY and CI alike — until the
Reporter landed. This is a regression with a datable cause, not an original omission.

**The gitexclude fail-closed comment and the setup-script warn sit twelve lines apart.**
`apply.go:1541-1543` fails the entire apply because a `.git/info/exclude` file could not be
written; `apply.go:1590-1595` defers a warning because the repo's own provisioning script
exited non-zero. Both are in `runPipeline`, both concern one repo. Whatever gets decided,
the two need to be justifiable side by side.

**Deferred messages leak across instances.** One `Reporter` is constructed per `runApply`
and reused across every instance in the loop (`internal/cli/apply.go:147`, then the loop at
`:263`). `Reporter.deferred` is only cleared in `FlushDeferred` (`reporter.go:165-170`),
which is called at the end of each instance's `Apply` — so a warning deferred while
processing instance 1 is flushed under instance 1's summary. That works, but it means the
counted-line design must not assume a fresh Reporter per instance.

## Open Questions

- Does an operator-configurable `setup_policy` need per-repo granularity, or is workspace-
  level enough? `SetupDir` already has both positions, so per-repo is nearly free — but the
  `.env.example` policy justified its three rungs against explicit PRD requirements, and
  there is no comparable requirement here yet.
- If the policy defaults to `warn` (matching the `.env.example` precedent and preserving
  Decision 2), the single-repo case that motivated the issue is still exit-0 unless the
  operator opts in. Is a counted summary line enough for that case, or does the default
  itself need to change? This is the actual decision, and the precedent survey does not
  settle it — the precedent settles the *mechanism*, not the *default*.
- If a summary line is chosen, does it belong on the summary line itself
  (`applied myws (3 repos, 1 setup incomplete)`) or as a separate `Log` line above the
  deferred block? The first is unmissable but changes a string that functional tests and
  possibly external parsers key on; the second matches the `healed %d` idiom.
- `niwa create --json` returns before any human output and has no field for partial
  failure. If discoverability matters to programmatic callers, the JSON shape may need a
  field — but that widens the scope into machine-readable output that no lead currently
  covers.
- Whether the amendment to `DESIGN-clone-output-ux.md` (correcting the `r.Status` line in
  Key Interfaces) needs the same blockquoted-update ceremony as a decision revision, or
  whether it is a plain typo fix. The doc's other three statements already say `Log`, so it
  reads as an editing error rather than a decision.

## Summary

niwa has no `--strict`-style flag anywhere and three `--allow-*` escape hatches on apply, so a `--strict-setup` flag would invert the house pattern; the real precedent is `config.Action` (`warn`|`fail`) with a most-specific-wins cascade over global/workspace/per-repo positions, already shipped for the `.env.example` pre-pass and sitting on the same `WorkspaceMeta`/`RepoOverride` structs that carry `SetupDir`. Counted-outcome lines also already exist in the apply path (`healed %d dangling plugin record(s): %s`, `apply failed for %d instances`), and the CLI's instance loop is already a working "run everything, collect failures, exit non-zero" model — but the setup warning currently rides the deferred channel, which by contract prints *below* the `applied myws (N repos)` summary, which is exactly why it is invisible. For recording the decision, niwa has no ADR convention at all: the established move is a new BRIEF/PRD/DESIGN chain plus a blockquoted `> **Update — ...**` note inserted under `## Status` in the doc being revised, which stays `Current` in `docs/designs/current/` (the shirabe lifecycle gate requires exactly that for a ready-for-review PR).
