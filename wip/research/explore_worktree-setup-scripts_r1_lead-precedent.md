# Lead: What does niwa already do when a worktree needs something the clone got, and what is the precedent?

Round 1. Repo: `niwa` at branch `docs/worktree-setup-scripts` (== `origin/main` `d25ad4d`).
All file:line citations are relative to the niwa repo root.

## Findings

### 1. Inventory: everything a clone gets from `niwa apply`, and whether a worktree gets it

The instance apply pipeline is `runPipeline` in `internal/workspace/apply.go`; its ordered
steps are labelled in comments (`Step 0.3` through `Step 9`). The worktree counterpart is
`workspace.ApplyToWorktree` (`internal/workspace/worktree_content.go:525`), which is called
from exactly three places:

- `internal/cli/session_lifecycle_cmd.go:298` `applyContentToWorktree`, reached by
  `niwa worktree create` (`session_lifecycle_cmd.go:194`) and `niwa worktree apply`
  (`session_lifecycle_cmd.go:274`);
- `internal/cli/session_from_hook_cmd.go:144`, the Claude Code `WorktreeCreate` hook;
- `internal/workspace/apply.go:2433`, inside `refreshWorktreeEnvs` (Step 6.6,
  `apply.go:1921`), which fans out to every live worktree on every `niwa apply`.

Note that Step 6.6 is misnamed by its own comment: it says "refresh the env", but it calls
the *whole* of `ApplyToWorktree`, so every apply re-delivers content, skills, MCP config,
settings, hooks and the rules import to every live worktree — and re-runs worktree hooks.
That is a bigger existing fan-out than the "R6 env guarantee" framing suggests.

| What the clone gets from `niwa apply` | Pipeline site | Does a worktree get it? | Mechanism |
|---|---|---|---|
| Repo orientation content (`CLAUDE.local.md`, subdir content), overlay-merged | Step 6, `apply.go:1596` | YES | Delivered by `ApplyToWorktree` at create/apply, via the same `InstallRepoContentTo` (`worktree_content.go:563`) |
| Workspace plugin skills (`.claude/skills/...`) | Step 6.2, `apply.go:1645` | YES | Delivered at create/apply via the same `InstallRepoSkills` (`worktree_content.go:590`); resolution passes no fetcher, so it re-delivers what apply already fetched |
| MCP server config + session env + approval/sandbox posture (the "payload config") | Step 6.3, `apply.go:1681` | YES | Delivered at create/apply via the same `InstallPayloadConfig` (`worktree_content.go:621`) |
| Claude lifecycle hooks registered into `settings.local.json` (from `configDir/hooks/` + `[claude.hooks]`) | Step 6.5, `apply.go:1830` (`HooksMaterializer`) | YES | Same shared `runRepoMaterializers` loop, run against the worktree dir (`worktree_content.go:784`) |
| `settings.local.json` (permissions, promoted `[claude.env]` keys) | Step 6.5 (`SettingsMaterializer`) | YES | Same materializer loop; promoted secret-backed keys are read out of the clone's materialized env instead of re-resolved (`worktree_content.go:774`, `readCloneEnvOutput` at :385) |
| `[files]` distribution | Step 6.5 (`FilesMaterializer`) | YES | Same materializer loop |
| Resolved env / secrets output files (`.local.env`, json, shell, custom targets) | Step 6.5 (`EnvMaterializer`) | YES, but by a *different* mechanism | `EnvMaterializer` is deliberately dropped for worktrees (`worktreeRepoMaterializers`, `worktree_content.go:848`); the worktree byte-copies the clone's already-materialized output via `inheritEnvOutputs` (`worktree_content.go:262`). Fanned out again on every apply by Step 6.6 |
| Worktree-delegation entries (`WorktreeCreate`/`WorktreeRemove` hook or the deny fallback) | Step 6.4, `apply.go:1779` | YES | Threaded as `WorktreeApplyOptions.WorktreeDelegation`, recomputed on the standalone path (`session_lifecycle_cmd.go:319`). Explicitly added so clone and worktree do not drift (Decision 9) |
| git-exclude coverage for niwa-authored files | Step 6.5 tail, `apply.go:1886` | YES | `gitexclude.EnsureRepoExclude(worktreePath, …)` at `worktree_content.go:703`, plus an extra pattern for `.claude/rules/worktree-imports.md` |
| Workspace-context rules import | Step 4.5, `apply.go:1531` (instance root) | YES, worktree-specific variant | `installWorktreeRulesImport` (`worktree_content.go:865`) writes `.claude/rules/worktree-imports.md` with an absolute `@import` to the instance's `workspace-context.md` (+ overlay/global) |
| Purpose/branch context layer | n/a (worktree-only) | YES (worktree-only addition) | `installWorktreeContextLayer` (`worktree_content.go:914`), templated via `[content.worktree].source` |
| Workspace-authored lifecycle scripts run *by niwa itself* against the tree | n/a at repo level | YES (worktree-only addition) | `runWorktreeHooks` (`worktree_content.go:1061`), discovered from `<configDir>/worktree-hooks/` |
| Instance-root `CLAUDE.md`, group `CLAUDE.md` | Steps 4/5, `apply.go:1493`/`:1570` | INHERITED IMPLICITLY | A worktree lives at `<instance>/.niwa/worktrees/<repo>-<sid>/`, so Claude's CLAUDE.md walk-up reaches them. `.claude/rules/` is *not* walked up, which is exactly why the rules import above exists (`DESIGN-worktree-command-parity.md:150-165`) |
| Instance-root skills / root payload config | Steps 6.2b/6.3, `apply.go:1654` | NOT DELIVERED (worktree gets its own repo-level copies instead) | The root copies live at the instance root; a worktree launched as its own project root reads only its own tree, which is why 1b/1c re-deliver at worktree level |
| niwa's own plugin (`NiwaPlugin` procedure) | Step 6.5b, `apply.go:1915` | NOT DELIVERED per-worktree | `deliverNiwaPlugin(instanceRoot, …)` (`apply.go:2142`) writes into the developer home + instance root only. Effectively inherited via the developer-home half; nothing worktree-scoped |
| Directory trust (Codex `trusted_projects`) | Step 6.5, `apply.go:1900` | **NOT DELIVERED** | `deliverDirectoryTrust(repoRoots, …)` (`apply.go:2069`) is passed clone roots only (`apply.go:1885-1890`). No worktree path is ever trusted. Nothing in `worktree_content.go` touches trust |
| **Repo-provided setup scripts (`scripts/setup/`)** | **Step 6.75, `apply.go:1951-1978`** | **NOT DELIVERED** | `RunSetupScripts` (`internal/workspace/setup.go:53`) has exactly one production caller, `apply.go:1959`, and it is always passed a clone dir (`filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)`) |
| git clone / fetch / pull of the repo itself | Step 3, `apply.go:1417` | n/a | The worktree shares the clone's object store by construction |

**The setup-scripts row is not lonely — it is one of two.** Only two things a clone gets do
not reach a worktree in some form: repo-provided setup scripts, and directory trust. Every
other row is either delivered by `ApplyToWorktree`, fanned out from Step 6.6, or inherited
through the filesystem. That is a strong structural argument: the worktree path has already
absorbed *every* other class of clone provisioning, one decision at a time, and each
absorption was justified with the same sentence ("an agent launched here reads only this
tree").

### 2. Does niwa already execute repo-provided code inside a worktree?

**Executes arbitrary code inside a worktree: YES, unambiguously. Repo-*provided*: no.**

`ApplyToWorktree` step 5 (`worktree_content.go:706-714`) calls `runWorktreeHooks`, which
discovers scripts from `<configDir>/worktree-hooks/` via `DiscoverWorktreeHooks`
(`internal/workspace/discover.go:85`) and `exec.Command`s each one with `cmd.Dir =
worktreePath` and `NIWA_WORKTREE_{PATH,REPO,PURPOSE,BRANCH}` in the environment
(`worktree_content.go:1092-1105`). `worktreeApplyEvent = "apply"` (`worktree_content.go:24`)
is the single event name, and the comment there says why: create internally runs the apply
path, so one event covers both. Step 6.6 means it *also* fires on every `niwa apply`, once
per live worktree.

The code itself frames this as the setup-script analogue. `worktree_content.go:1050-1053`:
"It is the worktree analog of the instance hook surface: scripts come from the workspace
config repo the operator already trusts (same provenance as `DiscoverHooks` / setup
scripts; no new external input)." The comment at `worktree_content.go:706-710` is even more
direct: "Worktree-event hooks, run on create/apply. **Analog of the instance setup-script
run**".

So the "declarative data vs. arbitrary side-effecting execution" line is **already crossed**.
Concretely, today a worktree hook can `npm install` in the worktree, and the counterargument
that setup scripts are a different category than Step 6.6's data sync does not survive
contact with step 5, which sits in the same function three lines later.

What is *not* crossed is **provenance**. Every script niwa runs in a worktree comes from the
workspace config directory (`configDir`), which is the config repo snapshot — the same
provenance as `DiscoverHooks` and `[claude.hooks]`. Nothing repo-authored executes in a
worktree. `RunSetupScripts` reads `<repoDir>/scripts/setup/`, i.e. code that ships *inside
the target repo*, and `DESIGN-post-clone-scripts.md:47-49` calls out that distinction
explicitly: "This is distinct from niwa's materializers, which distribute config FROM the
workspace config repo TO target repos. Post-clone scripts run code that lives INSIDE the
target repo." That is the real category line, and it is a provenance line, not an
execution-vs-data line.

Two operational asymmetries between the two execution surfaces are worth carrying into any
design:

- **Redaction.** `RunSetupScripts` takes the apply's `*secret.Redactor` and streams output
  line-by-line through the Reporter with a `[<repo>/<script>]` prefix (`setup.go:53`,
  `:118-121`). `runWorktreeHooks` wires `cmd.Stdout`/`cmd.Stderr` straight to the stderr
  writer with no redactor and no prefix (`worktree_content.go:1095-1097`) — and the
  worktree's resolved secret file is already on disk beside it, because env inherit (step
  2b, `worktree_content.go:818`) runs before step 5.
- **Failure semantics.** Setup-script failure is data, never an error: `apply.go:1951-1955`
  says the pipeline's error path "must not be reached, since on create it deletes the
  instance root", so failures become deferred warnings and a `setupIncomplete` count.
  Worktree-hook failure is fatal to `ApplyToWorktree` (`worktree_content.go:1107-1109`,
  first non-zero exit stops the run and returns an error). See sub-question 5 for what that
  costs on each of the three call paths.

### 3. Dispatch and ephemeral sessions: instance, not worktree

**`niwa dispatch` gets a full fresh INSTANCE, so setup scripts already run for dispatched
workers.** `runDispatch` (`internal/cli/dispatch.go:269`) resolves the enclosing workspace
root, then at `dispatch.go:476` calls `provisionInstanceFunc`, which is
`realProvisionInstance` (`internal/cli/instance_from_hook.go:118`, defined at `:428`). That
builds a `workspace.Applier` and calls `applier.Create(ctx, cfg, configDir, workspaceRoot,
instanceName)` (`instance_from_hook.go:501`) — the same path `niwa create` drives, so the
full `runPipeline` runs, including Step 6.75 setup scripts. The command's own help text
says so: "dispatch creates a fresh ephemeral niwa instance" (`dispatch.go:202`). Nothing in
`dispatch.go` mentions worktrees except line 286, which notes a self-dispatching worker
creates a *sibling instance*, never a nested one.

**Ephemeral sessions likewise.** The workspace-root `SessionStart` hook runs `niwa instance
from-hook` (`internal/workspace/root_materializer.go:51-56`), which lands in
`runInstanceFromHook` and calls the same `provisionInstanceFunc` (`instance_from_hook.go:183`).
Full instance, full apply, setup scripts run. That path carries a deliberately generous
180s hook timeout precisely because provisioning is slow (`root_materializer.go:58-62`).

**`niwa watch` also provisions an instance** (`internal/cli/watch.go:790`).

**The one worktree-producing automated path is Claude Code's own `WorktreeCreate` hook**,
`niwa worktree from-hook` (`internal/cli/session_from_hook_cmd.go:116` `runFromHookCreate`),
installed into each repo's `settings.local.json` when the harness supports it
(`internal/workspace/materialize.go:795-806`). That does `worktree.CreateSession` +
`applyContentToWorktree` and no setup scripts.

**Decision-relevant conclusion:** the gap does *not* bite dispatched or ephemeral-session
agents. It bites (a) a human running `niwa worktree create`, and (b) an agent that asks
Claude Code to create a worktree in a repo where niwa installed the delegation hook. Both
are interactive-ish paths where a human is nearby. That materially lowers the urgency
relative to the framing "every dispatched agent lands in an unprovisioned tree" — which is
false.

### 4. Is there a cheaper lever the user already has? Yes — `worktree-hooks/`

**A workspace can already register a script that runs on worktree create.** It is
`<configDir>/worktree-hooks/apply.sh`, or `<configDir>/worktree-hooks/apply/*.sh`
(`discover.go:85-129`, layouts documented at `:79-80`). It runs on `niwa worktree create`,
`niwa worktree apply`, the `WorktreeCreate` delegation hook, and every live worktree on
every `niwa apply`. It is documented for users at `docs/guides/worktree.md:292-310`,
including the environment table, and is listed as step 6 of what `worktree create` does
(`docs/guides/worktree.md:26-28`). Two unit tests pin it:
`TestApplyToWorktreeRunsWorktreeHook` and `TestApplyToWorktreeNonExecutableHookSkipped`
(`internal/workspace/worktree_content_test.go:367`, `:394`).

**Event names are not a fixed set.** `DiscoverWorktreeHooks` maps whatever directory or
`.sh` basename it finds to an event name; only `"apply"` is consumed today
(`worktree_content.go:24`, `:1071`). There is no allowlist to extend — adding a `create`
event would be a consumer change, not a schema change. (The Claude-side `hookEventMapping`
at `materialize.go:319` is a different surface: it maps niwa's snake_case config events to
Claude Code's PascalCase names for `settings.local.json`, and covers only
`pre_tool_use`/`post_tool_use`/`stop`/`notification`.)

**There is no `[worktree]` config section and no `--exec`/post-create flag.** The only
worktree config surface is `[content.worktree].source` (`docs/guides/worktree.md:276-291`),
which shapes the context layer, not execution. `niwa worktree create`'s flags are `--json`
only (`session_lifecycle_cmd.go:117`).

**Nothing in the workspace's own config repo uses it.** The public config repo at
`public/dot-niwa` has no `worktree-hooks/` directory at all — its `.niwa/` tree contains
`workspace.toml`, `extensions/`, `claude/`, `env/`, and `hooks/{stop,pre_tool_use}/`. So
the existing lever is unused in practice, which is consistent with it being undiscovered
rather than tried-and-found-wanting.

**Honest assessment of "documentation, not code":** the lever exists but does not do the
job cleanly. A `worktree-hooks/apply.sh` is *one* script for the *whole workspace*; to run
repo-appropriate setup it must branch on `$NIWA_WORKTREE_REPO` and re-implement, in the
config repo, what each repo already declares in its own `scripts/setup/`. That inverts the
provenance model `DESIGN-post-clone-scripts.md:47-49` set up on purpose, and it means a repo
that adds a setup step has to also patch the config repo. It also re-runs on every
`niwa apply` per live worktree with no dedup. So: the cheap lever is real and should be
documented regardless, but "documentation only" leaves the repo-authored half unaddressed.
The natural minimum-code shape is a few lines in `ApplyToWorktree` reusing
`ResolveSetupDir` + `RunSetupScripts` against the worktree — not new machinery.

### 5. Blast radius

**Code paths a change would touch.**

- `internal/workspace/worktree_content.go:525` `ApplyToWorktree` — the single insertion
  point; a setup run belongs next to step 5 (`:706-714`).
- `internal/workspace/setup.go:33,53` `ResolveSetupDir` / `RunSetupScripts` — reusable
  as-is, but `RunSetupScripts` requires a `*Reporter`, and `ApplyToWorktree` has only an
  `io.Writer` (`WorktreeApplyOptions.Stderr`). Either thread a Reporter through
  `WorktreeApplyOptions` or construct one from the writer. All three callers
  (`session_lifecycle_cmd.go:316`, `session_from_hook_cmd.go:144`, `apply.go:2433`) would
  need to supply it.
- Redactor: `RunSetupScripts`'s last parameter is a `*secret.Redactor`. The worktree path
  resolves no secrets and holds no redactor — but it *does* write the clone's resolved
  secrets into the worktree at step 2b before this would run. Passing `nil` (as every test
  does) means unredacted script output. This needs a decision, not a default.
- `internal/workspace/apply.go:1921` Step 6.6 — because it calls the whole of
  `ApplyToWorktree`, adding setup scripts there means **every `niwa apply` re-runs every
  repo's setup scripts once per live worktree**, on top of the once-per-clone run at Step
  6.75. With N worktrees that is N+1 `npm install`s per apply. This is the single largest
  cost and correctness risk in the change, and it argues for either a create-only event or
  an explicit opt-out at refresh time.

**Failure semantics and the shell navigation protocol.**

- `niwa worktree create`: `applyContentToWorktree` failing returns early at
  `session_lifecycle_cmd.go:195-197`, **before** `validateLandingPath` /
  `writeLandingPath` (`:222-228`). So a failing setup script would leave the worktree on
  disk *and* break the `cd` handoff — the shell wrapper gets no landing path
  (`DESIGN-shell-navigation-protocol.md`, `NIWA_RESPONSE_FILE` contract at :198-214). A
  *slow* create does not break the protocol (the wrapper reads the file after the command
  exits), but it does block the user's shell for the duration with no progress output
  unless a Reporter is threaded.
- Claude's `WorktreeCreate` hook: a failure triggers `reconcileFailedHookCreate`
  (`session_from_hook_cmd.go:166`), which **destroys the whole worktree**. That comment
  already anticipates the dirty case: "the one way real work can be present is a
  workspace-authored worktree apply script that touched tracked files before a later step
  failed" (`session_from_hook_cmd.go:177-180`). Setup scripts make that case common rather
  than theoretical, and a dirty worktree then triggers the retain-and-warn branch, which is
  the orphan-accumulation path.
- **Hook timeout.** The `WorktreeCreate`/`WorktreeRemove` entries are written with *no*
  `timeout` field (`materialize.go:795-806`), so they get Claude Code's default (60s). The
  `SessionStart` instance hook deliberately sets 180s "so the clone + vault cost of
  `niwa create` does not trip the harness timeout" (`root_materializer.go:58-62`). A
  `go mod download` or `npm install` inside `worktree from-hook` will exceed 60s on a cold
  cache and the tool call fails — which then rolls the worktree back. **If setup scripts
  run on the from-hook path, `worktreeCreateEvent`'s entry needs a timeout too.**
- On `niwa apply` (Step 6.6) a failing `ApplyToWorktree` is already non-fatal:
  warn + forward-carry the worktree's prior managed entries (`apply.go:2452-2456`).

**Tests that would have to change or could break.**

- `internal/workspace/characterization_test.go:62-199` — `TestApplyCharacterization` builds
  a golden manifest of the worktree's written files and compares against
  `testdata/.../worktree_files.txt`. The manifest is built from the returned `written` list
  (`:197`), so it only moves if setup-produced paths are added to that list. It also pins
  an exact duplicate count for `CLAUDE.local.md` (`:191`) — any change to the written list
  is loud here by design.
- `internal/workspace/worktree_content_test.go` — the `ApplyToWorktree` unit suite,
  including the two worktree-hook tests at `:367` and `:394` whose ordering assumptions
  (hooks last) a setup run would sit next to.
- `internal/workspace/apply_worktree_refresh_test.go` — Step 6.6 behavior, the skipped/
  locked/detached/forward-carry matrix.
- `internal/workspace/worktree_promote_inherit_test.go`, `worktree_secret_ref_test.go`,
  `worktree_disclosure_test.go`, `materialize_worktree_test.go`, `agent_gate_test.go` —
  all call `ApplyToWorktree` and would see any new required option or side effect.
- `internal/workspace/setup_test.go`, `internal/workspace/setup_visibility_test.go` —
  `RunSetupScripts` contract, including the reporter-prefix and redaction assertions
  (`setup_visibility_test.go:181`, `:214`) that a worktree caller passing `nil` would
  bypass.
- `internal/cli/session_lifecycle_cmd_test.go`, `session_lifecycle_overlay_test.go`,
  `session_from_hook_cmd_test.go` — the CLI create/apply/from-hook paths.
- `test/functional/features/worktree.feature` (5 scenarios, incl. "installs repo content,
  rules import, and purpose/branch layer" at :35 and "apply re-syncs idempotently" at :64),
  `worktree-env-parity.feature`, `worktree-delegation.feature`, plus their step files
  `test/functional/session_steps_test.go` and `worktree_delegation_steps_test.go`.
- `test/functional/features/shell-navigation.feature` — the cd handoff.
- `internal/workspace/discover_test.go:12-45` — `DiscoverWorktreeHooks`, if a `create`
  event is added.

**Not affected.** `niwa worktree list` renders only the lifecycle state
(`internal/worktree/session_lifecycle.go:37-69`); nothing there records content or setup
status, and the state schema would not need a field unless the design wants to record
"setup ran". No test in-tree measures or asserts on `worktree create` *timing* (I found no
timing assertions in the worktree suites) — the only timing constraint is the harness hook
timeout described above.

### 6. Outside precedent (brief)

- **git itself.** `git worktree add` *does* fire the `post-checkout` hook (unless
  `--no-checkout`), and since a 2018 fix it runs with the new worktree as the working
  directory and with `GIT_DIR`/`GIT_WORK_TREE` sanitized so the hook is not confused by the
  parent's values ([Sunshine patch](https://public-inbox.org/git/20180215191841.40848-1-sunshine@sunshineco.com/),
  [githooks docs](https://git-scm.com/docs/githooks)). The idiom for detecting "this is a
  fresh worktree" is that the previous HEAD arrives as the null ref
  ([mskelton](https://mskelton.dev/bytes/using-git-hooks-when-creating-worktrees),
  [nshephard](https://blog.nshephard.dev/posts/git-worktree-hooks/)). So a post-create
  provisioning hook is a first-class, upstream-supported idea. The catch that makes it a
  poor fit here: hooks live in `.git/hooks`, are not cloned, and are shared across all
  worktrees of a repo — a repo cannot ship one, which is precisely the provenance problem
  niwa's setup-script convention solves. git-lfs also has a documented history of
  `post-checkout` hanging `git worktree add`
  ([git-lfs#2848](https://github.com/git-lfs/git-lfs/issues/2848)), a concrete instance of
  the blocking-hook failure mode.
- **Worktree-oriented tooling has converged on exactly this feature.** `workmux` makes the
  problem statement explicitly ("new worktrees are clean checkouts with no gitignored
  files — .env, node_modules") and answers it with declarative copy/symlink globs plus
  lifecycle commands run at specific points ([workmux](https://github.com/raine/workmux)).
  There is a packaged `worktree-setup-hook` skill that installs a `post-checkout` template
  which sniffs `package.json`/`requirements.txt`/`Cargo.toml`/`go.mod`/`Gemfile` and then
  runs `setup.sh` or `scripts/setup.sh` if present
  ([LobeHub](https://lobehub.com/skills/lambda-curry-devagent-worktree-setup-hook)) — note
  it converges on the *same* `scripts/setup` convention niwa already has.
- **mise / direnv.** Neither has a worktree-create event. `mise generate pre-commit`
  installs into the main repo's `.git/hooks/` and is picked up from every worktree
  ([mise#4853](https://github.com/jdx/mise/discussions/4853)) — the shared-hooks model
  again. The Claude-Code-specific direnv workaround is a `SessionStart` hook that loads the
  nearest `.envrc` into `$CLAUDE_ENV_FILE`
  ([gist](https://gist.github.com/eshaham/8e3b63fb077530dffc2964b648145ec9)), i.e. push the
  provisioning to session start rather than tree creation.
- **devcontainers** are the clearest statement of the blocking/background split:
  `postCreateCommand` runs once after creation and is the documented home for `npm install`
  and codegen, while `postStartCommand`/`postAttachCommand` run later and cheaper
  ([VS Code docs](https://code.visualstudio.com/docs/devcontainers/create-dev-container),
  [containers.dev](https://containers.dev/implementors/features/)). The lesson worth
  importing: separate the expensive once-per-tree step from the cheap every-attach step,
  which is exactly the distinction niwa currently lacks (one `"apply"` event covering both
  create and every refresh).
- **jj workspaces.** `jj workspace add` has no documented setup-hook surface; workspaces are
  independent filesystem copies sharing history
  ([jj-workspace-add(1)](https://man.archlinux.org/man/extra/jujutsu/jj-workspace-add.1.en)).
  No precedent to borrow.

## Implications

1. The strongest counterargument to the fix — "Step 6.6 syncs declarative data, setup
   scripts are execution, different category" — does not hold. `ApplyToWorktree` already
   `exec.Command`s workspace-authored shell scripts inside the worktree, three lines after
   the content delivery, and its own comment calls that the "analog of the instance
   setup-script run". The live category line is *provenance* (config-repo-authored vs
   repo-authored), not data-vs-execution.
2. Urgency is lower than the framing suggests. Dispatch, ephemeral sessions, and `niwa
   watch` all provision full instances and already run setup scripts. Only interactive
   `niwa worktree create` and Claude's `WorktreeCreate` delegation hook land in an
   unprovisioned tree.
3. A cheap lever exists and is documented but unused: `<configDir>/worktree-hooks/apply.sh`.
   Any recommendation should mention it, and any design should explain why the repo-authored
   half still needs its own answer.
4. The single event name `"apply"` is the design's weak joint. Because Step 6.6 runs the
   whole of `ApplyToWorktree`, anything expensive added to it runs once per live worktree
   per `niwa apply`. The devcontainer create/start split is the obvious remedy: introduce a
   `create` event, or gate the setup run on a create-time flag in `WorktreeApplyOptions`.
5. Two concrete hardening items ride along with any fix: the `WorktreeCreate` hook entry
   carries no `timeout` (60s default vs. the SessionStart hook's deliberate 180s), and
   `runWorktreeHooks` streams script output with no redactor even though the worktree's
   resolved secret file is already on disk.

## Surprises

- Step 6.6's comment says "refresh the env of the instance's existing worktrees", but the
  code calls all of `ApplyToWorktree` — content, skills, MCP, settings, hooks, rules import,
  *and* worktree hooks. The stated R6 guarantee understates what actually fans out.
- `worktree-hooks/` already exists, is documented, is tested, and is used by nobody in the
  workspace's own config repo.
- Failure semantics for the two execution surfaces are exact opposites: setup-script failure
  is deliberately non-fatal data (because the error path deletes the instance root), while
  worktree-hook failure is fatal and, on the from-hook path, destroys the worktree.
- Directory trust is the other thing a worktree never gets. Nobody appears to have noticed;
  it is not mentioned in `DESIGN-worktree-command-parity.md` at all.
- `DESIGN-worktree-command-parity.md:57` names setup scripts as part of the instance
  pipeline it was achieving parity with, then Decision C (`:150-178`) enumerates what a
  worktree receives and silently omits them. The omission was in view and unaddressed.

## Open Questions

- Should a setup run be create-only, or every apply like the existing worktree hooks? The
  Step 6.6 N+1 cost says create-only; `DESIGN-post-clone-scripts.md`'s idempotency driver
  says every-apply is at least defensible.
- Failure policy: setup-script semantics (warn, continue, count) or worktree-hook semantics
  (fatal)? Fatal on the from-hook path destroys the worktree; non-fatal on the CLI path
  still needs to reach `writeLandingPath` so the `cd` works.
- Does `RunSetupScripts` need the redactor threaded into the worktree path, given the
  worktree already holds the clone's resolved secrets by the time it would run?
- Should the `WorktreeCreate` hook entry gain an explicit timeout regardless of this change?
- Does directory trust for worktree paths belong in the same change, or is it a separate
  gap worth its own issue?

## Summary

Only two things a clone gets from `niwa apply` never reach a worktree: repo-provided setup scripts and Codex directory trust — everything else (repo content, plugin skills, MCP config, session env, settings, Claude hooks, the resolved secret file, delegation entries, git-exclude coverage) is either delivered by `ApplyToWorktree` at create/apply or fanned out to every live worktree on every apply by Step 6.6, which despite its name runs the entire worktree apply rather than just the env. The "declarative data vs. arbitrary execution" objection is already moot: `ApplyToWorktree` step 5 (`worktree_content.go:706-714`) discovers `<configDir>/worktree-hooks/` and `exec.Command`s those scripts inside the worktree on create and on every apply, and its own comment calls this the "analog of the instance setup-script run" — so the surviving distinction is provenance (config-repo-authored, not repo-authored), and a workspace can already register a worktree bootstrap script today, documented at `docs/guides/worktree.md:292-310` and used by nobody. Urgency is lower than assumed because `niwa dispatch`, the ephemeral-session `SessionStart` hook, and `niwa watch` all provision full instances through `applier.Create` and therefore already run setup scripts; the gap bites only interactive `niwa worktree create` and Claude's `WorktreeCreate` delegation hook, where the blast radius is a broken `cd` handoff (`writeLandingPath` is unreachable after an install error), a worktree rollback on the from-hook path, an N+1 re-run per apply through Step 6.6, and a 60s default hook timeout the `WorktreeCreate` entry never overrides.
