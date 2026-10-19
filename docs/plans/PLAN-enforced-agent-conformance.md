---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-enforced-agent-conformance.md
milestone: "Enforced agent conformance"
issue_count: 8
---

# PLAN: enforced agent conformance

## Status

Active

## Scope Summary

Implement docs/designs/DESIGN-enforced-agent-conformance.md: sessions
launched at a workspace root or an instance root get niwa's worktree
integration, resolve a target repo from a `<repo>/` or `<group>/<repo>/`
prefix in the worktree name, are refused at the call when they reach
for `EnterWorktree(path)` or a hand-run `git worktree add`, and receive
factual layout context at session start and after compaction, while the
generated move-into-a-repo bullets go away. Apply warns when a root it
wrote hooks into has no trusted ancestor. Repo-launched sessions don't
change.

The upstream PRD (docs/prds/PRD-enforced-agent-conformance.md) carries
requirements R1-R18 and the acceptance criteria grouped under Settings
output, `from-hook` create, `from-hook` remove, Guard, Session-start
context, Prose, Trust, and Live check. Each outline below names the
requirements and criterion groups it covers; between them they cover all
of R1-R18.

## Decomposition Strategy

**Horizontal, following the DESIGN's components, landing as one pull
request.** The components have stable interfaces between them -- a
`HarnessSupport` value, the `ResolveHookTarget` resolver, the guard's
`Decide` function, the `BuildRootContext` builder -- and each can be
built and unit-tested on its own. The order follows the DESIGN's
Implementation Approach: the probe move, root settings and resolver come
first and are meant to land together (shipping root hooks without the
resolver would break a bare root's `EnterWorktree(name)`), the guard's
pure decision package precedes its command and registration, and the
context and trust pieces depend only on the settings plumbing. The
functional tests and the live check close the PR, and the guide lands
last.

No split branch fires: the work lives in one repository, no step needs
to reach the default branch before another can run, and the repo
declares no `atomic` delivery preference. The PR is single-pr.

## Issue Outlines

### Issue 1: feat(workspace): one harness probe per command and worktree delegation at both roots

**Complexity**: critical

**Goal**: Move the `SupportsWorktreeHooks` decision out of Applier Step
6.4 (`internal/workspace/apply.go`). Add an optional `HarnessSupport`
input to the Applier; when a caller supplies none (`niwa create`, `niwa
reset`, `newProvisionApplier`), the Applier probes once at the start of
the run, before Step 4.5. `internal/cli/apply.go` and
`internal/cli/init.go` probe once and pass the same value to
`MaterializeWorkspaceRoot` through `RootMaterializeOptions` and to every
Applier they run. Derive each instance's `WorktreeDelegation` from that
value and the instance's `NoWorktreeDelegation` (read through
`resolveWorktreeDelegation`), and pass it to `RootSettingsMaterializer`
so the instance root's `.claude/settings.json` gets the same hooks or
deny as each repo. Add `[claude] worktree_delegation` (default true) to
`internal/config/config.go`; `writeRootSettings` writes the
integration into the workspace root's settings when it is on. Covers
R1, R2, R3 (settings half), R18.

**Acceptance Criteria**:

- With the harness check stubbed to pass, `niwa apply` writes
  `WorktreeCreate`/`WorktreeRemove` entries invoking `niwa worktree
  from-hook` at the instance root and in every repo; stubbed to fail, it
  writes the `EnterWorktree`/`ExitWorktree` deny at both
- `niwa init` on a new workspace writes the integration into the
  workspace root's settings; `worktree_delegation = false` omits it;
  and a workspace initialized with it on, then switched off, loses the
  integration from the workspace root's settings on the next apply
- An instance created with `--no-worktree-delegation` has no worktree
  hook or deny at its instance root or any repo
- A test that builds an Applier through `newProvisionApplier` with no
  `HarnessSupport` and the probe stubbed to pass shows every repo's
  settings carry the `WorktreeCreate` hook
- Golden-file test: each repo's `.claude/settings.local.json` is
  byte-identical before and after this change for the same config and
  harness result
- A second consecutive `niwa apply` leaves every settings file
  byte-identical; an instance applied by the previous release gains the
  instance-root integration on its first apply
- A hand edit to the instance root's settings is reported by the next
  apply's drift check
- `go test ./...` passes

**Dependencies**: None

---

### Issue 2: feat(workspace): launch-anchored hook target resolver with repo prefix

**Complexity**: critical

**Goal**: Add `ResolveHookTarget(launchDir, cwd, name)` in a new
`internal/workspace/hook_target.go`. Anchor on `CLAUDE_PROJECT_DIR`,
falling back to the process working directory; canonicalize anchor and
`cwd`; find the instance at or above the anchor, or, for a
workspace-root anchor, the instance under it containing `cwd`; require
`cwd` inside it. Split `name` on `/` and match a `<repo>/` or
`<group>/<repo>/` prefix exactly against the instance's configured repo
list (group-aware), then fall back to `ResolveRepoFromCwd`, then a
one-repo instance, then refusal. Emit the R6 refusal texts. Rewire
`runFromHookCreate` and `runFromHookRemove`
(`internal/cli/session_from_hook_cmd.go`) through it, pass the slug to
`purposeFromHookName`, honor the resolved instance's opt-out at runtime,
and make remove resolve its instance from `worktree_path` within the
anchor. Covers R3 (runtime half), R4, R5, R6, R7, R16.

**Acceptance Criteria**:

- Every criterion in the PRD's "`from-hook` create" and "`from-hook`
  remove" groups passes as a table test with payloads on stdin and
  `CLAUDE_PROJECT_DIR` set per case
- A `cwd` outside the anchor's instance (including a planted
  `.niwa/instance.json` elsewhere) is refused and nothing is created
- With `CLAUDE_PROJECT_DIR` unset, the resolver uses the process working
  directory
- `niwa/` (empty slug) is refused with the R6 message; `Niwa/fix-x` is
  treated as unprefixed
- A configured repo whose clone is missing is refused naming it
- In an instance created with `--no-worktree-delegation`, reached
  through the workspace-root hook: create exits non-zero naming the
  opt-out, and remove exits 0 and changes nothing
- On success stdout is exactly one absolute, symlink-resolved,
  dot-free path and a newline; all other output is on stderr
- The existing `session_from_hook_cmd_test.go` and
  `worktree-delegation.feature` pass unchanged for repo-launched
  sessions
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

---

### Issue 3: feat(worktreeguard): command tokenizer and guard decision

**Complexity**: testable

**Goal**: Add `internal/worktreeguard/` with a tokenizer and
`Decide(payload, instance) (allow bool, reason string)`. The tokenizer
splits on unquoted `;`, `&&`, `||`, `|`, `&` and newlines, honors
quotes and backslash escapes, skips heredoc bodies, strips leading
`VAR=value` words and an `env` word, and matches the command word by
basename. `Decide` refuses `git [global options] worktree add` and
`EnterWorktree` with a `path` that doesn't resolve (joined with `cwd`,
cleaned, symlink-resolved) under the instance's `.niwa/worktrees/`. It
allows everything else, and allows every call when the instance opted
out. With no instance resolved, the refusal lists instance directory
names (up to 20). The reason text contains `use EnterWorktree with
name`, the repo names and a `<repo>/<slug>` example. Covers R8, R9, R15
(decision half).

**Acceptance Criteria**:

- Every Bash and `EnterWorktree` case in the PRD's Guard group passes as
  a table test against `Decide`
- `bash -c "git worktree add x"` and an unterminated quote are allowed
- With an opted-out instance, `Decide` allows `git worktree add x` and
  a path-form `EnterWorktree`
- A package test using `go list -deps` asserts `internal/worktreeguard`
  imports neither `os/exec` nor `net`
- `go test ./...` passes

**Dependencies**: None

---

### Issue 4: feat(cli): niwa worktree guard command and root-scope registration

**Complexity**: testable

**Goal**: Add the hidden `niwa worktree guard` command
(`internal/cli/worktree_guard.go`): read at most 1 MiB of stdin, find
the instance with the issue 2 lookup, call `Decide`, recover panics,
write the reason to stderr and exit 2 to block or 0 to allow. Register
it through `buildSettingsDoc` with matcher `EnterWorktree|Bash`, a
5-second timeout, appended to existing PreToolUse entries, rendered by
`guardedNiwaHookCommand`, at the instance root and workspace root only
and never in an opted-out instance. Covers R8, R9, R10, R15.

**Acceptance Criteria**:

- Instance-root and workspace-root settings contain the guard entry;
  no repo-level or niwa-worktree settings file does
- An existing `niwa watch` Bash entry survives alongside the guard after
  apply and after `niwa watch` re-applies its settings
- End-to-end: the command exits 2 with the reason on stderr for `git
  worktree add ../x`, and 0 for `git worktree list`
- Malformed hook JSON, an unparseable command, and a niwa binary without
  the subcommand all let the call proceed with an error on stderr
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

---

### Issue 5: feat(workspace): session-start layout context and removal of the generated bullets

**Complexity**: testable

**Goal**: Add `BuildRootContext(instance, optedOut)` in
`internal/workspace/session_context.go`, built with `encoding/json`,
emitting the R11 sentences and the `<group>/<repo>` list (capped at
50). Add the hidden `niwa instance context` command
(`internal/cli/instance_context.go`) printing the `additionalContext`
JSON, registered in the instance-root settings with matcher
`startup|resume|clear|compact`, a 10-second timeout, and a new
`guardedNiwaContextCommand` that keeps stdout only when it starts with
`{`. Append the same block in `buildSessionStartInjection` for ephemeral
sessions. Remove the two move-into-a-repo bullets from
`generateWorkspaceContext` (`internal/workspace/workspace_context.go`).
Covers R11, R12, R17.

**Acceptance Criteria**:

- Every criterion in the PRD's Session-start context and Prose groups
  passes
- A test runs the `guardedNiwaContextCommand` form against a stub
  binary that prints help text and exits 0, and asserts empty stdout;
  against a stub printing JSON, stdout is that JSON
- A golden-file test over every niwa-generated context file at the
  workspace root and instance root finds no line that matches
  `(?i)worktree` and begins with a verb from the R11 list
- Golden-file tests pin the regenerated `workspace-context.md` and the
  context text for the two-repo fixture
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

---

### Issue 6: feat(claudetrust): read-only trust check and apply warning

**Complexity**: testable

**Goal**: Add `IsTrusted(env, dir)` to `internal/claudetrust`: read the
user's Claude Code config read-only through the package's existing path
resolution, decode only the `projects` trust flags, cap the read size,
walk from `dir` up through its ancestors, and treat a parse failure as
unknown. After apply writes hooks into a workspace root or instance
root, warn on stderr when the root has no trusted ancestor, with the
root's path, `untrusted`, and a remedy naming both trusting the
workspace root and `trust_instances = true`. Covers R13.

**Acceptance Criteria**:

- Every criterion in the PRD's Trust group passes
- An unparseable config produces no warning and leaves the file
  byte-identical
- A test asserts neither apply nor dispatch changes the trust records or
  `trust_instances`
- `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

---

### Issue 7: test(functional): root-session worktree scenarios and live check

**Complexity**: testable

**Goal**: Add functional scenarios beside
`test/functional/features/worktree-delegation.feature` exercising, through
the binary, apply output at both roots, `from-hook` create and remove
from the instance root and the workspace root, the guard command, and
the context command. Run the PRD's live check on Claude Code 2.1.302 or
later in default, auto and bypass modes, plus the dispatched-worker case
and a check that Claude Code sets `CLAUDE_PROJECT_DIR` for worktree
hooks, and record the results in the PR body. Covers R14 and the PRD's
Live check group.

**Acceptance Criteria**:

- Functional scenarios cover each PRD criterion group end to end and
  `make test-functional` passes
- The PR body records, for each permission mode, that a root-launched
  `EnterWorktree(name: "<repo>/x")` raised no permission request and
  ended inside `.niwa/worktrees/`, and that `niwa worktree list` showed
  it
- The PR body records the dispatched-worker result and the
  `CLAUDE_PROJECT_DIR` finding
- The PR body records that `git worktree add` and a path-form
  `EnterWorktree` were refused with the guard text in a live session

**Dependencies**: <<ISSUE:2>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

---

### Issue 8: docs(guides): root-session worktrees in the worktree guide

**Complexity**: simple

**Goal**: Update `docs/guides/worktree.md` to describe worktree
creation from a workspace root and an instance root: the `<repo>/<slug>`
naming, the resolution order, the guard and what it refuses (stated as a
steering aid, not a boundary), the session-start context, the
`worktree_delegation` setting, the per-instance opt-out, the trust
warning, and where native `.claude/worktrees` checkouts from earlier
sessions came from and how to remove them by hand.

**Acceptance Criteria**:

- The guide covers each surface listed in the goal
- The guard section states it is not a security boundary and names the
  forms it doesn't inspect
- The prose follows the repo's writing conventions (no emojis, direct
  style)

**Dependencies**: <<ISSUE:7>>

---

## Implementation Sequence

**Critical path:** Issue 1 -> Issue 2 -> Issue 4 -> Issue 7 -> Issue 8.

Open with issue 1, which every other piece rides. Issue 3 is pure
library code and can be built in parallel from the start. Issue 2 follows
issue 1 and should land in the same commit series, since root hooks
without the resolver would refuse every bare-root request. Issue 4 joins
the decision package to the resolver and the settings plumbing. Issues 5
and 6 depend only on issue 1 and can proceed beside issues 2 through 4.
Issue 7 closes the PR with end-to-end coverage and the live check, and
issue 8 documents the final behavior.

**Parallelization opportunity:** {1, 3} first; after issue 1, issues 2,
5 and 6 run in parallel; issue 4 after 2 and 3.
