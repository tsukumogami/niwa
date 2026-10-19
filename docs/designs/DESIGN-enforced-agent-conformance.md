---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-enforced-agent-conformance.md
problem: |
  niwa writes its WorktreeCreate/WorktreeRemove delegation only into each
  repo's settings, so sessions launched at an instance root or at the
  workspace root -- every dispatched and ephemeral worker -- never reach
  it. They either stall on Claude Code's relocation prompt and fail, or
  silently create native checkouts niwa can't see. `niwa worktree
  from-hook` also can't map a bare instance root to a repo, and the only
  correction available today is generated or operator-maintained prose.
decision: |
  Probe worktree-hook support once in the CLI layer and hand the result to
  every settings writer, so the workspace root, the instance root and each
  repo get the same integration. Teach `from-hook`, through a new
  resolver in internal/workspace anchored on the launch directory, to read
  a `<repo>/` or `<group>/<repo>/` prefix from the hook's name before
  falling back to the session's directory. Add a hidden `niwa worktree
  guard` PreToolUse command at root scopes that blocks path-form entry and
  hand-run `git worktree add` with exit 2 and lets every other outcome
  through. Add a hidden `niwa instance context` SessionStart command that
  emits factual layout context on every source, drop the generated
  move-into-a-repo bullets, and warn at apply when a root has no trusted
  ancestor, since Claude Code then runs none of its hooks.
rationale: |
  Every piece reuses a mechanism niwa already ships: buildSettingsDoc
  renders the hook or the deny for any document, from-hook creates niwa
  worktrees, `niwa watch guard-fs` is the exit-2 guard precedent, `niwa
  instance from-hook` emits additionalContext, and internal/claudetrust
  reads trust. Exit 2 is the one PreToolUse outcome Claude Code honors in
  every permission mode, and any other exit lets the call proceed, which
  gives fail-open without extra code. Claude Code applies its workspace
  trust check before resolving any hook source and won't start a
  background session in an untrusted directory, so a running dispatched
  worker always sits under a trusted ancestor and reads the root hooks;
  no launch-time hook delivery is needed. Rewriting path-form calls,
  starting workers inside a repo, and per-launch trust grants were set
  aside because they change what the agent asked for, demote sibling
  repos, or widen trust.
---

# DESIGN: enforced agent conformance

## Status

Planned

## Context and Problem Statement

The PRD (`docs/prds/PRD-enforced-agent-conformance.md`) states the
requirements; this section names the code they land in.

Claude Code loads project hooks only from the settings of a session's
launch directory, and doesn't walk down into nested repos or up into
parents. niwa writes three kinds of settings document:

- the workspace root's `.claude/settings.json`, written by
  `writeRootSettings` (`internal/workspace/root_materializer.go`), which
  is reached only from `internal/cli/apply.go` -- once per workspace,
  before the per-instance loop, and only when apply runs from the
  workspace root -- and from `internal/cli/init.go` when ephemeral
  sessions aren't disabled;
- the instance root's `.claude/settings.json`, written by
  `RootSettingsMaterializer` (`internal/workspace/workspace_context.go`)
  at Applier Step 4.5 (`internal/workspace/apply.go`, around line 1774);
- each repo's `.claude/settings.local.json`, written by
  `SettingsMaterializer` (`internal/workspace/materialize.go`) at
  Step 6.5.

All three go through `buildSettingsDoc`, which already renders the
`WorktreeCreate`/`WorktreeRemove` entries, or the
`EnterWorktree`/`ExitWorktree` deny, from a `WorktreeDelegation` value.
Only the per-repo call passes one. The delegation decision --
`SupportsWorktreeHooks` probing `claude --version` against `2.1.183`,
skipped when `NoWorktreeDelegation` is set -- is taken at Step 6.4
(`apply.go`, around line 2028), after the instance-root settings are
already written and long after the workspace-root settings.

`niwa worktree from-hook` (`internal/cli/session_from_hook_cmd.go`)
resolves its create target with `ResolveRepoFromCwd`, which rejects any
`cwd` outside a repo, and resolves its remove target's instance from the
process's own working directory. A bare instance root is under no repo,
and a workspace-root process is under no instance, so even correctly
installed root hooks would refuse.

Two facts about the harness shape the design, both checked against the
Claude Code 2.1.303 bundle during review. First, the workspace trust
check runs before hooks are resolved and suppresses every hook source,
command-line `--settings` included; a directory inherits trust from a
trusted ancestor, and `claude --bg` refuses to start in an untrusted
directory. Second, a PreToolUse hook exiting 2 blocks the call with its
stderr shown to the model in every permission mode, bypass included,
while any other exit or a timeout doesn't block. The default hook
timeout is 600 seconds.

The only agent-facing guidance today is prose: `generateWorkspaceContext`
writes two bullets telling agents to move into a repo before running git
commands and before making changes (`workspace_context.go` lines 716 and
718).

## Decision Drivers

- **Root scopes must get what repos get** (PRD R1, R2).
- **No permission prompt in any mode** (R14). Only the `name` form of
  `EnterWorktree` avoids the relocation check, so `name` must work and
  `path` must be steered away from.
- **Steer at the call, fail open** (R15). A guard runs on every Bash
  call in a root session; a broken or stale niwa must never block the
  shell or stall it.
- **Model input is untrusted.** `cwd`, `name`, `path` and Bash commands
  in hook payloads come from the model.
- **Reuse niwa's existing hook and trust machinery.**
- **Don't widen trust** (R13).
- **Repo-launched sessions don't change** (R10).

## Considered Options

### Decision 1: Where the delegation decision is made

- **Chosen: probe once per command, before the first settings write.**
  The Applier gains an optional `HarnessSupport` input. When a caller
  supplies it, the Applier uses it; when none does -- `niwa create`,
  `niwa reset`, and `newProvisionApplier`, the path dispatch, ephemeral
  provisioning, watch and reap go through -- the Applier probes once at
  the start of the run, before Step 4.5, instead of at Step 6.4. Every
  Applier caller therefore keeps the per-repo integration it has today
  and gains the instance-root one. `internal/cli/apply.go` and
  `internal/cli/init.go` probe once up front and pass the same value to
  `MaterializeWorkspaceRoot` (through `RootMaterializeOptions`, the only
  route to `writeRootSettings`) and to every Applier they run, so the
  workspace root and its instances agree. Each instance still applies
  its own `NoWorktreeDelegation`, read through the existing fail-closed
  `resolveWorktreeDelegation`.
- *Rejected: store the decision on the Applier.* The workspace-root
  write happens outside the Applier, before the per-instance loop, so
  the Applier can't feed it.
- *Rejected: probe per scope.* Two probes can disagree, and the PRD
  wants one choice per apply.

### Decision 2: How `from-hook` picks its target

- **Chosen: a new `ResolveHookTarget` in `internal/workspace`, anchored
  on the launch directory.** It reads `CLAUDE_PROJECT_DIR` (set by
  Claude Code for hooks and fixed at the launch directory), falling
  back to the hook process's own working directory, which Claude Code
  starts in the launch directory, when the variable is unset. It
  canonicalizes both the anchor and `cwd`, then finds the instance:
  the nearest `.niwa/instance.json` at or above the anchor, which covers
  instance-root, repo and niwa-worktree launches; or, when the anchor is
  a workspace root, the instance directly under it that contains `cwd`.
  `cwd` must lie inside the instance found, otherwise the call is
  refused. The same resolver serves the per-repo hooks, so repo-launched
  sessions resolve exactly as they do today. It then tries in order: an exact `<group>/<repo>/` or
  `<repo>/` prefix in `name` matched against the instance's configured
  repo list; `ResolveRepoFromCwd`; the single repo of a one-repo
  instance; refusal. Matching against the configured list rather than
  the on-disk scan lets it report a configured repo whose clone is
  missing. Remove uses the same anchor, resolving the instance from
  `worktree_path` within the launch directory rather than from the
  process's working directory.
- *Rejected: walk up from `cwd` alone.* `cwd` is model-controlled; a
  session could plant a `.niwa/instance.json` and point the unsandboxed
  hook at it.
- *Rejected: a `CwdChanged` hook recording the last repo entered.* It
  duplicates `cwd` and goes stale.

### Decision 3: How the guard is wired

- **Chosen: one hidden `niwa worktree guard` command, exit 2 to block.**
  The decision is a function in a new `internal/worktreeguard` package;
  a thin cobra wrapper reads at most 1 MiB of stdin, recovers panics,
  writes the reason to stderr and exits 2 to block or 0 to allow. A niwa
  too old to have the subcommand prints help and exits 0, which allows;
  any other failure is a non-blocking exit. The entry is registered
  with matcher `EnterWorktree|Bash` -- Claude Code reads that as an
  exact list of two tool names -- and a 5-second timeout, appended to
  any existing PreToolUse entries rather than replacing them. The guard
  finds its instance with the same launch-anchored lookup as
  `ResolveHookTarget`. When no instance resolves -- a workspace-root
  session that hasn't moved into one -- it refuses the R8 and R9 forms
  with text listing the workspace's instance directory names (up to 20)
  in place of repos. When the resolved instance opted out, it allows
  every call.
- *Rejected: JSON `permissionDecision: "deny"`.* niwa's
  `DESIGN-watch-operator-approval.md` records that JSON decisions are
  ignored where exit codes are not.
- *Rejected: a bare `Bash` matcher.* `niwa watch`'s
  `ApplyReviewSettings` dedupes its `gh pr review` guard by matcher
  string (`internal/watch/containment.go`, around line 335), so a second
  bare `Bash` entry would make it drop its own.
- *Rejected: rewriting `path` to `name` via `updatedInput`.* The PRD
  chose refusal.

### Decision 4: How Bash commands are parsed

- **Chosen: a small purpose-built tokenizer.** It splits on unquoted
  `;`, `&&`, `||`, `|`, `&` and newlines; honors single and double
  quotes and backslash escapes; skips heredoc bodies; strips leading
  `VAR=value` words and an `env` word; matches the command word by
  basename (so `/usr/bin/git` counts); then looks for option words (with
  `-C` and `-c` consuming their argument), `worktree`, `add`. A command
  it can't tokenize is allowed.
- *Rejected: `mvdan.cc/sh/v3/syntax`.* A full parser is a new dependency
  for a steering aid the PRD limits to simple commands.
- *Rejected: substring matching.* It blocks `grep "git worktree add"`.

### Decision 5: How layout context reaches sessions

- **Chosen: a hidden `niwa instance context` SessionStart command in
  the instance-root settings, matcher `startup|resume|clear|compact`,
  10-second timeout.** It builds `additionalContext` from the fixed R11
  sentences via a shared `BuildRootContext` that
  `buildSessionStartInjection` also calls for ephemeral sessions. The
  `exec`-based `guardedNiwaHookCommand` can't filter output, so the
  context entry gets its own form, `guardedNiwaContextCommand`: the same
  PATH-first lookup with quoted absolute fallback, captured into a
  shell variable and printed only when it starts with `{`, so an old
  niwa's help text never leaks into context. The context is built with
  `encoding/json`, and the repo list is capped at 50 entries.
- *Rejected: extending `niwa instance from-hook`.* It runs at the
  workspace root and is gated to background ephemeral jobs.
- *Rejected: keeping the facts in `workspace-context.md`.* That is the
  prose the PRD removes; it isn't re-delivered after compaction.

### Decision 6: Dispatched workers and trust

- **Chosen: rely on the harness's own trust gate, and warn.** Claude
  Code won't start `claude --bg` in a directory with no trusted
  ancestor and runs no hooks there, so a dispatched worker that is
  running at all is in a trusted tree and reads the instance-root hooks.
  `niwa apply` adds a read-only `claudetrust.IsTrusted(env, dir)` that
  mirrors the harness's ancestor walk and warns on stderr, per R13,
  when a root it wrote hooks into has no trusted ancestor. niwa writes
  nothing to the trust records.
- *Rejected: carry hooks in dispatch's launch `--settings` document.*
  Review against the bundle showed the trust check suppresses that source
  too, so it adds nothing; it would also double-register `WorktreeCreate`
  in trusted roots.
- *Rejected: a per-launch trust grant.* It edits the user's trust
  records, which the PRD forbids.

This reading changes how R13's second sentence is met: a worker in an
untrusted root never starts, so the case it describes can't arise.
The PRD records the amendment.

### Decision 7: The workspace-level switch and the per-instance opt-out

- **Chosen: `[claude] worktree_delegation` in `workspace.toml`, default
  true, plus runtime checks.** The key sits beside `trust_instances`.
  When false, `writeRootSettings` omits the integration and the guard.
  A per-instance `--no-worktree-delegation` is honored at runtime:
  `from-hook` refuses a create naming the opt-out and makes remove a
  no-op, and the guard allows every call, for any target resolved into
  that instance. The instance's own root settings omit the guard and
  the integration entirely.
- *Rejected: drop the workspace-root integration when any instance
  opts out.* One instance would disable it for all.

## Decision Outcome

One probe result and three hidden commands carry the feature. The CLI
probes once (Decision 1) and every settings document niwa writes
renders from that result and each instance's opt-out. `from-hook`
resolves its target through a launch-anchored resolver with a prefix
branch (Decision 2) and honors opt-outs at runtime (Decision 7), so a
root session's `EnterWorktree(name: "niwa/fix-x")` reaches a niwa
worktree with no prompt. The guard (Decisions 3 and 4) refuses the two
routes that can't work from a root and lets everything else through.
The context command (Decision 5) re-states the layout at every start
and after every compaction, replacing the generated bullets. Trust
stays the harness's call (Decision 6), with apply saying when a root
won't run any of this.

## Solution Architecture

### Components

| Component | Location | Change |
|---|---|---|
| Harness probe | `internal/cli/apply.go`, `internal/cli/init.go` | Probe once; pass `HarnessSupport` through `RootMaterializeOptions` and to every Applier run. |
| Applier | `internal/workspace/apply.go` | Optional `HarnessSupport`; probe at run start when absent; derive `WorktreeDelegation` per instance before Step 4.5; drop the Step 6.4 probe. |
| Workspace-root settings | `internal/workspace/root_materializer.go` | Integration and guard entries, governed by `worktree_delegation`. |
| Instance-root settings | `internal/workspace/workspace_context.go` | `RootSettingsMaterializer` takes `WorktreeDelegation`, guard and context inputs. |
| Settings rendering | `internal/workspace/materialize.go` | `buildSettingsDoc` gains guard and context entries, appended to existing event lists, with explicit timeouts. |
| Hook target resolver | `internal/workspace/hook_target.go` (new) | `ResolveHookTarget(launchDir, cwd, name)`; group-aware repo list from config; refusal texts. |
| `from-hook` | `internal/cli/session_from_hook_cmd.go` | Use the resolver for create and remove; purpose from slug; opt-out check. |
| Guard decision | `internal/worktreeguard/` (new) | Tokenizer and `Decide`; path check does filesystem lookups only. |
| Guard command | `internal/cli/worktree_guard.go` (new) | Hidden `niwa worktree guard`. |
| Context builder | `internal/workspace/session_context.go` (new) | `BuildRootContext(instanceRoot)`, reading the opt-out from instance state. |
| Context command | `internal/cli/instance_context.go` (new) | Hidden `niwa instance context`. |
| Ephemeral injection | `internal/cli/instance_from_hook.go` | Append `BuildRootContext` output. |
| Trust read | `internal/claudetrust/` | Read-only `IsTrusted` with ancestor walk; apply warns. |
| Prose | `internal/workspace/workspace_context.go` | Drop the two bullets. |
| Config | `internal/config/config.go` | `WorktreeDelegation *bool` (`worktree_delegation`). |

### Data flow: a root session creates a worktree

1. The session calls `EnterWorktree(name: "niwa/fix-x")`. The guard sees
   `name` and exits 0.
2. Claude Code runs the `WorktreeCreate` hook from the launch scope's
   settings: `niwa worktree from-hook` with `{cwd, name}` on stdin and
   `CLAUDE_PROJECT_DIR` in its environment.
3. `ResolveHookTarget` finds the instance under the launch directory,
   checks `cwd` lies inside it, checks the opt-out, matches `niwa` as a
   prefix, and returns repo `niwa`, slug `fix-x`.
4. `from-hook` calls the existing `worktree.CreateSession`, prints the
   symlink-resolved absolute path and exits 0. Claude Code moves the
   session into the worktree, whose settings are the per-repo ones.

### Data flow: a hand-built route is refused

1. The session runs `git -C public/niwa worktree add x`.
2. The guard tokenizes it, matches `git ... worktree add`, writes the
   reason (`use EnterWorktree with name`, the repo list, `niwa/<slug>`)
   to stderr, exits 2. Claude Code shows the reason to the model.

### Interfaces

- `niwa worktree guard`: stdin PreToolUse payload; exit 2 with reason on
  stderr to block, exit 0 to allow.
- `niwa instance context`: stdin SessionStart payload; stdout
  `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}`.
- `niwa worktree from-hook`: unchanged contract; new refusal texts
  `no repo selected` and `not inside a niwa instance`.

## Implementation Approach

1. **Probe, root settings and resolver together.** Move the probe to
   the CLI layer, add `worktree_delegation`, write the integration at
   both roots, and land `ResolveHookTarget` with the prefix branch,
   refusals, opt-out check and launch-anchored remove in the same
   change. Shipping root hooks without the resolver would make a bare
   root's `EnterWorktree(name)` fail where it now falls back to a native
   checkout. Golden tests for repo settings and idempotence; table tests
   over the PRD's payload criteria.
2. **Guard.** Tokenizer and decision package with table tests, the
   hidden command, registration at root scopes, the no-subprocess test.
3. **Context and prose.** Builder, hidden command, registration,
   ephemeral reuse, size and imperative-verb tests; remove the two
   bullets with a golden test.
4. **Trust warning.** `claudetrust.IsTrusted` with the ancestor walk and
   the apply warning; tests asserting trust records are unchanged.
5. **Live check and docs.** The PRD's live criteria in all three
   permission modes, recorded in the PR, plus a check that Claude Code
   sets `CLAUDE_PROJECT_DIR` for `WorktreeCreate` and `WorktreeRemove`
   hooks; update the worktree guide.

Every step's tests include the existing per-repo worktree suites
(`worktree-delegation.feature`, `session_from_hook_cmd_test.go`) passing
unchanged, and an Applier run with no supplied `HarnessSupport` (the
provisioning path) producing the per-repo integration it produces
today.

## Security Considerations

- **Launch-anchored resolution.** Hook processes run outside the
  session's sandbox, and `cwd` is model-controlled. Anchoring on
  `CLAUDE_PROJECT_DIR`, canonicalizing `cwd`, and requiring it inside an
  instance under that anchor stops a session from planting a fake
  `.niwa/instance.json` elsewhere and steering the hook's git commands
  at it.
- **Prefix matching never builds a path.** Segments are compared to the
  configured repo list only; `purposeFromHookName` keeps stripping
  control, bidi and zero-width characters, and the slug never reaches a
  git ref.
- **Guard path check.** `path` is joined with `cwd`, cleaned and
  symlink-resolved before the comparison with
  `<instance>/.niwa/worktrees/`; a path that doesn't resolve is refused,
  since every allowed target exists.
- **Fail-open is deliberate.** The guard is a steering aid; the
  `WorktreeCreate` hook remains the enforcement for the `EnterWorktree`
  route. Explicit timeouts (5 seconds for the guard, 10 for context)
  replace the 600-second default so a hung niwa can't stall a session.
- **Trust is read, never written.** `IsTrusted` opens the user's Claude
  Code config read-only through `internal/claudetrust`'s existing path
  resolution, decodes only the `projects` entries' trust flag, caps the
  read size, and treats a parse failure as unknown (no warning, a debug
  note). The warning's remedy names both trusting the workspace root
  and `trust_instances = true`.
- **Accepted risks.** The PATH-first niwa lookup, shared with every
  existing niwa hook, means a writable earlier PATH entry could shadow
  niwa. The tokenizer doesn't unwrap `exec`, `command`, `time`, `nice`
  or `nohup`. Both are accepted for a steering aid.
- **Hook command rendering.** All entries go through
  `guardedNiwaHookCommand`, which quotes the absolute fallback; no task
  text reaches a command line.

## Consequences

**Positive**

- Root-launched sessions get niwa worktrees by name with no prompt in
  every permission mode.
- The hand-built routes are refused with guidance at the call, and the
  guidance leaves generated prose.
- The guard and context commands are a reusable pattern for later
  niwa-owned rules.

**Negative**

- Every Bash call in a root session pays one niwa process start.
- Existing native `.claude/worktrees` checkouts stay unmanaged.
- Root sessions that `cd` into a repo and call `EnterWorktree(name)`
  switch from native checkouts to niwa worktrees.
- An instance root with no trusted ancestor gets none of this; apply
  can only warn.

**Mitigations**

- The decision function makes no subprocess or network call, enforced
  by a test.
- The worktree guide documents where native checkouts came from and how
  to remove them.
- The apply warning names `trust_instances = true` as the remedy.
