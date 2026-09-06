# Explore Findings: worktree-setup-scripts

Round 1. All evidence against niwa `origin/main` = `d25ad4d`.

## The brief's four evidence points

| # | Claim | Verdict |
|---|-------|---------|
| 1 | One production caller of `RunSetupScripts`, on the clone path | **Confirmed.** `internal/workspace/apply.go:1959`; definition at `internal/workspace/setup.go:53`; every other hit is a test. |
| 2 | Nothing worktree-side touches it | **Confirmed.** `internal/worktree/*.go` has no setup provisioning; the only `setup` tokens are test helpers. |
| 3 | Step 6.6 fans env out to worktrees, Step 6.75 is clone-only | **Confirmed.** Comment begins `apply.go:1921`, `refreshWorktreeEnvs` call at `:1932`, Step 6.75 at `:1951`. The R6/R7 wording is verbatim. |
| 4 | `DESIGN-post-clone-scripts.md` never mentions worktrees | **Confirmed but misattributed.** True (`grep -ic worktree` = 0), but that doc shipped 2026-03-30 and `ApplyToWorktree` did not exist until 2026-06-06 — 68 days later. It could not have excluded worktrees. The omission belongs to a different document (below). |

## The decisive evidence, which the brief did not have

### `DESIGN-worktree-command-parity.md` enumerates setup scripts and then drops them

This is the design that created `ApplyToWorktree`, and its stated job was parity
between a worktree and a repo checkout.

- Line 57 enumerates the instance pipeline it is mirroring: "... repo materializers
  (settings, env, files, hooks via `DiscoverHooks`/`DiscoverEnvFiles`) -> **setup
  scripts** -> state", concluding "A repo checkout therefore emerges fully formed."
- Line 90, decision driver R4: "The worktree verbs should map onto the instance
  lifecycle where operations correspond; **gaps should be deliberate.**"
- The token `setup` appears exactly twice in the whole document: line 57 above, and
  line 321, a Consequences bullet claiming the design delivers "no manual setup for
  agents launched there".
- It appears in no decision, no table, no content list, and no rejected alternative.
- Line 116 marks exactly one gap deliberate: `attach`/`detach`.

So by the design's own standard the gap is undeclared, and its stated positive
consequence is falsified downstream by a README instructing readers to perform the
manual setup it promised they would not need.

### niwa has already closed a strictly weaker version of this divergence

`DESIGN-niwa-default-worktree.md` Decision 9 (line 313) found `ApplyToWorktree` was
not passing the worktree-delegation decision, so a worktree's `settings.local.json`
did not match its clone's. The chosen fix, and the reasoning:

> The gap is latent rather than user-visible today -- delegation still resolves for a
> session working inside a worktree -- but the two configurations drifting is the kind
> of divergence R3's "install at the scope required for it to take effect" exists to
> rule out, and it would become load-bearing the moment settings resolution changed.

niwa fixed a clone/worktree divergence nobody could observe, on principle. The setup
divergence is the same class, except user-visible, workaround-documented, and it has
already cost people time. This argument stands without reference to Step 6.6.

### The execute-vs-sync objection is already moot

The strongest counterargument -- Step 6.6 synchronises declarative env *data* whereas
setup scripts are arbitrary side-effecting *execution*, so the former implies nothing
about the latter -- is dead on arrival, because niwa already executes scripts in
worktrees.

`runWorktreeHooks` (`internal/workspace/worktree_content.go:1061`, called at `:712` as
step 5 of `ApplyToWorktree`) runs scripts from `<configDir>/worktree-hooks/` against
the worktree on both create and apply, in lexical order, with the worktree as cwd and
`NIWA_WORKTREE_PATH` / `_REPO` / `_PURPOSE` / `_BRANCH` exported. The call site comment
at `:707` reads "Analog of the instance setup-script run". `docs/guides/worktree.md`
says the same in user-facing prose: "They are the worktree analog of instance setup
scripts."

### The gap, stated exactly

Two script provenances, two destinations. `DESIGN-post-clone-scripts.md` draws the
provenance line itself: materializers distribute "FROM the workspace config repo TO
target repos", whereas post-clone scripts "run code that lives INSIDE the target repo."

| | Config-repo scripts | Repo's own `scripts/setup/` |
|---|---|---|
| **Clone** | materializers / `DiscoverHooks` | **yes** (`apply.go:1959`) |
| **Worktree** | `worktree-hooks/` (`worktree_content.go:712`) | **NO** |

Exactly one empty cell. The fix is not "teach worktrees to run scripts" -- they
already do. It is "the worktree runs the workspace's scripts; give it the repo's too."

## Architecture: one insertion reaches every surface

`ApplyToWorktree` has two direct callers -- `applyContentToWorktree`
(`internal/cli/session_lifecycle_cmd.go:366`) and Step 6.6's fan-out
(`internal/workspace/apply.go:2433`). Reaching it there are five distinct entry
paths, because `applyContentToWorktree` is itself called from four places:

| Call site | Surface |
|---|---|
| `internal/cli/session_lifecycle_cmd.go:194` | `niwa worktree create` |
| `internal/cli/session_lifecycle_cmd.go:274` | `niwa worktree apply` |
| `internal/cli/session_from_hook_cmd.go:144` | agent-facing `WorktreeCreate` hook |
| `internal/cli/apply.go:306` | `niwa apply` with worktree scope |
| `internal/workspace/apply.go:2433` | **Step 6.6's per-apply fan-out** (direct) |

The distinction matters for anything inserted *inside* `ApplyToWorktree`: all four
CLI paths share one invocation, so the inserted code cannot tell which entry path
it is on -- and their failure postures are opposite (interactive create retains,
the delegated create tears down).

Step 6.6 does not need extracting: its fan-out already routes through
`ApplyToWorktree`. So a single step inside `ApplyToWorktree`, beside the existing
`runWorktreeHooks` at `:712`, covers create, worktree apply, agent creates, and the
apply-time fan-out at once.

It is the only site holding `cfg`, `repo`, `group`, `instanceRoot` and `worktreePath`
together; it is in the same Go package as `RunSetupScripts` and `ResolveSetupDir`; and
`cloneRepoDir` is already computed there (`worktree_content.go:781`).

`RunSetupScripts` needs no change at all. It never touches git -- its own tests run it
against a bare `t.TempDir()` -- and it is already parameterised on `repoDir`.

Two things `ApplyToWorktree` lacks, both closeable locally: a `*Reporter` (it holds
only `opts.Stderr io.Writer`; the apply path already passes `a.Reporter.Writer()`) and
a `*secret.Redactor` (constructible from `readCloneEnvOutput`'s already-computed map at
`worktree_content.go:379`). Note that `inheritEnvOutputs` copies real resolved secrets
into the worktree while the worktree path builds no redactor at all today.

`internal/worktree.CreateSession` is ruled out: the package is a documented leaf
(package doc, `worktree.go:1-6`) and `workspace` imports it, so calling
`RunSetupScripts` from there is an import cycle. The CLI-level sites are ruled out
because neither holds `cfg` or `group` after `applyContentToWorktree` returns.

## Why this is a design, not a patch

### The naive fix is silently wrong, first-party, in this workspace

`private/tools/scripts/setup/01-build-workflow-tool.sh` -- the only setup script in
this ten-repo workspace -- computes its output location as:

```sh
# The tools repo lives at <instance-root>/private/tools/, so the
# instance root is two directories up.
INSTANCE_ROOT="$(cd ../.. && pwd)"
TARGET="$INSTANCE_ROOT/.claude/bin/workflow-tool"
```

Clones live at `<instance>/<group>/<repo>` (depth 2); worktrees at
`<instance>/.niwa/worktrees/<repo>-<sid>` (depth 3). Verified on disk:

| cwd | `cd ../..` | correct |
|---|---|---|
| `<instance>/private/tools` | `<instance>` | yes |
| `<instance>/.niwa/worktrees/tools-<sid>` | `<instance>/.niwa` | **no** |

Run naively in a worktree it writes to `<instance>/.niwa/.claude/bin/workflow-tool`,
which nothing reads, at exit 0, with no warning. The script is not badly written --
its author reasoned about the layout and wrote the assumption in a comment. niwa never
published a contract saying what a setup script may assume about its cwd, because
until `ApplyToWorktree` existed there was only one possible answer.

### The design's own motivating example inverts

`DESIGN-post-clone-scripts.md`'s worked example is `01-git-hooks.sh`. Git hooks live in
the shared `git-common-dir`, so a worktree already has them -- the motivating use case
is precisely the one that must *not* run per-worktree. Combined with `node_modules`,
which must, a single per-repo boolean is insufficient: one repo's `scripts/setup/` can
hold both kinds. This points at a per-repo switch *plus* an environment signal so a
script can gate itself, consistent with how the idempotency contract already expects
scripts to check first.

### Cost is bimodal, not uniform

- Go, this workspace: 0.29s warm, artifact shared via `$HOME` and the instance root.
- JS monorepo (measured by the coordinator): 32.2s cold, ~13s per new tree warm
  (essentially all `npm ci`), 341 MB per tree. The build amortises through a shared
  `$HOME` cache; `node_modules` is per-tree by construction and never amortises.

A workspace-wide default is wrong for half of any mixed workspace.

### `setup_dir = ""` cannot be the opt-out

`WorkspaceMeta.SetupDir string` (`internal/config/config.go:328`) and
`RepoOverride.SetupDir *string` (`:473`); `ResolveSetupDir` (`setup.go:33`) treats a
per-repo `""` as disabled. That single key governs the clone run too, so using it to
decline worktree setup would disable the run the workspace actually depends on. The
switch has to be orthogonal. `DESIGN-post-clone-scripts.md:560` already specifies a
deferred `setup_policy = "warn" | "fail"` key resolved most-specific-wins, explicitly
"so that adding it later is additive rather than a re-litigation" -- the precedent for
adding a policy key to these same structs exists.

### Failure posture: three existing behaviours disagree

| Path | On script failure |
|---|---|
| Instance setup (`apply.go:1951`) | **data, never error** -- `setupIncomplete`, deferred warning, exit 0 |
| `runWorktreeHooks` (`worktree_content.go:1105`) | **error** -- fails `ApplyToWorktree` |
| `worktree create` CLI (`session_lifecycle_cmd.go:188`) | retains the worktree, reports the error |
| `from-hook` create (`session_from_hook_cmd.go:144`) | **full rollback** via `DestroySession` |

Matching `RunSetupScripts`' non-fatal contract is the only safe choice, and the reason
is concrete rather than aesthetic: if a repo's setup failure counted as a create
failure, one flaky `npm install` would silently destroy every agent-created worktree in
that repo, because the `from-hook` path rolls back by design.

The exit-code half is already settled in writing for the clone case, in an argument
that transfers verbatim (`DESIGN-post-clone-scripts.md:536`):

> The shell wrapper's `cd` into a new instance is gated on exit 0, so a fatal setup
> failure would strand the operator outside the very instance they need to enter to fix
> the script.

### Blocking has no escape hatch

The shell navigation protocol writes a path to `NIWA_RESPONSE_FILE` and the wrapper
`cd`s **after the process exits** (`internal/cli/shell_init.go:38-50`). Nothing can
hand the user in early and keep working. Precedent for a bounded blocking subprocess on
this exact path exists: `resolveWorktreeDelegation` runs `claude --version` on every
create and apply behind `worktreeProbeTimeout = 5s`
(`session_lifecycle_cmd.go:745`, `:783`).

The sharper constraint is the agent path: the `WorktreeCreate` hook entry niwa writes
carries **no `timeout` field** (`materialize.go:795-806`), unlike the SessionStart
entries which set 180s (`root_materializer.go:62`). A multi-second setup run there
risks the harness's default hook timeout.

## Two documented promises a fix must reconcile

1. **The offline guarantee.** `docs/guides/worktree.md` states `worktree create` and
   `worktree apply` "need no secret source and no network access" and "can't fail on an
   unreachable vault". A worktree `npm ci` breaks that in letter. The promise is really
   about niwa not *resolving secrets*, and the same nominal breach already exists on the
   clone path, so narrowing the wording is likely right -- but it is a written guarantee
   and must be addressed explicitly rather than ignored.
2. **The undocumented idempotency contract.** Nothing under `docs/` or `README.md`
   mentions `scripts/setup` or `setup_dir` at all. The contract exists only as a
   decision-driver bullet inside a design doc: "Idempotent: scripts must be safe to
   re-run on every `niwa apply`" (`DESIGN-post-clone-scripts.md:61`). A script author
   has no way to find it. Any change that leans on that contract should publish it.

## How lonely is the empty cell, and who actually feels it

Exactly two things a clone gets from `niwa apply` never reach a worktree: **repo-provided
setup scripts** and **Codex directory trust**. Everything else -- repo content, plugin
skills, MCP config, session env, settings, Claude hooks, the resolved secret file,
delegation entries, git-exclude coverage -- is either delivered by `ApplyToWorktree` at
create/apply time or fanned out to every live worktree on every apply by Step 6.6.
(Directory trust is a second, separate gap, unmentioned in
`DESIGN-worktree-command-parity.md`; worth its own issue, not this one.)

Step 6.6's own comment understates what it does: it says "refresh the env of the
instance's existing worktrees", but it calls the whole of `ApplyToWorktree` -- content,
skills, MCP, settings, hooks, rules import, and worktree hooks.

**Who is affected is narrower than the brief implies, and that matters for urgency.**
`niwa dispatch`, the ephemeral-session `SessionStart` hook, and `niwa watch` all
provision full *instances* through `applier.Create`, so setup scripts already run for
them. (This session is the proof: its instance's `.claude/bin/workflow-tool` was built
by its own provisioning.) The gap bites exactly two surfaces:

1. Interactive `niwa worktree create`.
2. Claude's `WorktreeCreate` delegation hook -- which is not niche, because
   `niwa apply` makes niwa the *default* worktree mechanism for Claude Code, so every
   agent that works in a worktree lands in an unprovisioned one.

## The cheap lever that already exists, and why it is not enough

A workspace can solve this today without any niwa change: drop a bootstrap script in
`<configDir>/worktree-hooks/apply.sh`. It is documented (`docs/guides/worktree.md`),
tested, and used by nobody in this workspace's own config repo. Any honest
recommendation has to name it, and the codespar `cs-worktree-bootstrap` workaround is
structurally exactly this.

It is not sufficient, for one specific reason. `worktree-hooks/` lives in the config
repo and fires for every worktree of every repo, so per-repo provisioning has to be
written as a switch on `NIWA_WORKTREE_REPO` inside a single workspace-level script.
Every repo needing setup adds a branch to a file in a different repository, duplicating
knowledge that already sits in that repo's own `scripts/setup/` and already works for
its clone. The repo-local convention exists precisely so a repo can declare its own
setup without the workspace knowing about it; the worktree path is where that inverts.

## The event-name joint

`worktreeApplyEvent = "apply"` is a single event covering both create and apply. Because
Step 6.6 runs the whole of `ApplyToWorktree`, anything expensive added to it runs once
per live worktree per `niwa apply` -- the N+1 problem. The devcontainer
`onCreateCommand` / `postStartCommand` split is the obvious remedy: introduce a `create`
event, or gate the setup run on a create-time flag in `WorktreeApplyOptions`.

## Blast radius

- `ApplyToWorktree` has 25 call sites in tests and 2 in production code, across 8
  test files including `internal/workspace/characterization_test.go` and
  `test/functional/worktree_delegation_steps_test.go`. (An earlier draft said 92,
  which is the raw string count -- 18 of those are `func TestApplyToWorktree*`
  names and the rest are doc comments. Corrected because the figure was being
  used as a blast-radius signal, where it overstated the work fourfold.)
- Step 6.6 runs *before* Step 6.75, so a worktree refresh currently reads clone env
  written before that same apply's clone setup scripts run. Ordering worth revisiting.
- `runWorktreeHooks` already re-runs on every apply for every live worktree, so
  "runs N times per apply" is the shipped precedent, not a new cost model.

## Side finding, unrelated and independently filable

`niwa worktree create` gets no shell auto-cd. `internal/cli/shell_init.go` wraps
`create|destroy|go|init` and a nested `session` -> `create`; the string `worktree` does
not appear in the file. Only the deprecated `niwa session create` alias lands the user
in the new tree, while `docs/guides/worktree.md` states twice that the canonical command
navigates you in.

## Decision: Crystallize
