---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-niwa-default-worktree.md
problem: |
  Claude Code agents in a niwa workspace create their own bare worktrees,
  competing with niwa's managed worktrees: degraded (no secrets/context) and
  invisible to niwa. The PRD requires agent-initiated worktree creation to yield
  a niwa worktree, one per task, with a disclosed fallback where the integration
  can't be honored.
decision: |
  Install per-repo Claude Code WorktreeCreate/WorktreeRemove hooks (via niwa's
  existing per-repo materializers) that delegate to a new `niwa worktree from-hook`
  subcommand. Create routes to a niwa worktree and echoes its path; remove
  force-destroys the session. An apply-time `claude --version` probe chooses
  between the hook (supported) and a deny+steer fallback (unsupported), disclosed
  via a one-time notice. An init-time opt-out disables the whole integration.
rationale: |
  The feasibility spike proved per-repo hooks fire in git repos and replace
  default worktree creation; niwa already owns the per-repo settings/hooks install
  surface. A hook-backed Go subcommand keeps logic testable and avoids brittle
  shell parsing. Hook and deny are mutually exclusive (a deny blocks the tool
  before the hook runs), so a capability probe must choose one.
---

# DESIGN: niwa as the default worktree mechanism

## Status

Planned

## Context and Problem Statement

The accepted PRD (`docs/prds/PRD-niwa-default-worktree.md`) requires that, in a
niwa workspace, agent-initiated worktree creation produce a full niwa worktree
rather than a competing bare checkout, with niwa as the single system of record,
a disclosed fallback where the integration can't be honored, and an init-time
opt-out.

Feasibility is already settled by `docs/spikes/SPIKE-niwa-default-worktree.md`
(Complete). Live `claude` runs established the load-bearing facts this design
builds on:

- Claude Code's `WorktreeCreate` hook fires in a git repo and **replaces** default
  worktree creation; `WorktreeRemove` fires on session/subagent exit and is
  non-blocking.
- The hook only fires when installed at **per-repo** scope (a repo's
  `.claude/settings.json` / `settings.local.json`); a workspace-root install does
  not reach an agent operating inside the repo.
- `WorktreeCreate` stdin is `{session_id, transcript_path, cwd, hook_event_name,
  name}`; `cwd` is the repo root; the hook must print the worktree path to stdout.
- The settings `env` block does not propagate to the hook subprocess.

What remains is *how* to wire this into niwa: the create/remove adapters, the
machine-readable path output, fallback detection, the opt-out, and the per-repo
install. Those are the decisions below.

## Decision Drivers

- **One mechanism, not two** (PRD R1, R5): the native agent path must yield a niwa
  worktree, with no competing bare checkout.
- **niwa is the system of record** (PRD R6): no orphaned worktree dirs or stale
  session records after agent teardown.
- **Default-on, zero manual setup** (PRD R2), installed at the scope that actually
  fires (R3), idempotently (R11), via the non-interactive apply path (R12).
- **No silent degradation** (PRD R7, R8, R10): fallback and secret-resolution
  failures are surfaced, never quiet.
- **Reuse existing niwa machinery**: per-repo materializers, one-time notices,
  instance-state opt-out flags — don't invent parallel mechanisms.
- **Testable in Go**: prefer compiled, unit-testable logic over shell parsing.

## Considered Options

### Decision 1 — WorktreeCreate adapter and repo resolution

- **Chosen: a `niwa worktree from-hook` subcommand, invoked by a mandatory thin
  hook shim.** A Claude Code hook entry can only be a script path, so niwa ships a
  small shim script (via the `HooksMaterializer`) whose only job is to invoke
  `niwa worktree from-hook`. The subcommand reads the hook JSON on stdin, resolves
  the repo via a **new cwd→repo-name resolver** (see Solution Architecture), runs
  the same two-step flow `niwa worktree create` uses today — `CreateSession`
  **then** `applyContentToWorktree` (the step that materializes secrets and CLAUDE
  context and carries R10's warn-and-continue) — and prints the worktree path to
  stdout.
- *Alternative: a pure shell hook script* that parses JSON with `jq` and calls
  `niwa worktree create`. Rejected: brittle shell parsing, hard to unit-test,
  command-injection surface from interpolating `name`/`cwd` into shell.

  Note: `CreateSession` alone (worktree.go) only creates the worktree, branch, and
  session state — it does **not** materialize secrets or CLAUDE content. The
  content/secret step lives in `applyContentToWorktree`
  (`internal/cli/session_lifecycle_cmd.go`), which is also where R10's
  `AllowMissingSecrets` warn-and-continue is surfaced. `from-hook` MUST run both
  steps, exactly as `runSessionCreate` does, or delegated worktrees would be the
  degraded checkouts this feature exists to eliminate.

### Decision 2 — machine-readable worktree path (PRD R4)

- **Chosen: add `--json` to `niwa worktree create`** (and have `from-hook` use the
  same internal path), emitting the absolute worktree path as a stable field.
  `from-hook` prints just the path to stdout for the hook contract.
- *Alternative: parse the existing human line* `session: created <id> at <path>`.
  Rejected: scraping prose is fragile; `--json` already exists for
  `niwa worktree list`, so this matches precedent.

### Decision 3 — WorktreeRemove reconciliation (PRD R6)

- **Chosen: `from-hook` remove path tries a guarded destroy first, then forces only
  past the attach-lock — never past the dirty guard.** It maps the worktree to a
  niwa session by worktree path (see the resolver note below), then: (1) releases
  the agent's own attach lock; (2) attempts `DestroySession(force=false)`; (3) if
  that is rejected only because the worktree is dirty (`ErrWorktreeDirty` on
  genuine, non-git-excluded uncommitted work), it does **not** force-delete —
  instead it logs the orphan and leaves the worktree for the developer, so agent
  teardown never silently discards real work. The attach-lock that the exiting
  agent itself holds is the one guard it does bypass. niwa stays system of record:
  sessions are either ended cleanly or explicitly logged as retained-dirty.
- *Alternative: unconditional `force=true`.* Rejected: it bypasses the dirty guard
  too, so a worktree Claude removed while holding uncommitted work would be deleted
  silently — the data-loss path the security review flagged. Defense-in-depth beats
  relying on Claude's clean-only removal as the sole safeguard.
- *Alternative: non-force only.* Rejected: the agent's own attach lock would
  routinely block teardown, leaving orphaned `active` sessions — violating R6.
- *Alternative: detach + mark-for-sweep.* Rejected: niwa has no sweep mechanism
  today, so cleanup would never complete; orphans accumulate.

  Resolver note: Claude's `session_id` is not niwa's session id, so the remove path
  maps by worktree path — scanning `ListSessionLifecycleStates()` for the matching
  `WorktreePath`. The exact `WorktreeRemove` stdin schema was not exercised by the
  spike; the plan must confirm which field carries the path and treat that as a
  small implementation risk.

### Decision 4 — fallback detection and disclosure (PRD R7, R8)

- **Chosen: an apply-time `claude --version` probe.** If the version is at/above the
  known-good baseline (v2.1.183 from the spike), niwa installs the hook; if it is
  below baseline, niwa instead writes `permissions.deny: ["EnterWorktree",
  "ExitWorktree"]` plus CLAUDE-content guidance steering agents to
  `niwa worktree create`. Hook and deny are **mutually exclusive** — a deny blocks
  the tool before the hook would run — so the probe must choose one. Because
  fallback is a *current-state* condition (an unsupported harness stays unsupported
  across applies), it is disclosed on **every** apply as a warning (per
  `docs/guides/one-time-notices.md`, current-state conditions surface every run, not
  via a one-time notice), with an optional one-time first-encounter explainer
  pointing at `niwa worktree create`. A probe that errors or finds no `claude` on
  PATH is treated optimistically (assume supported) to avoid spurious denies — this
  assumes a trusted PATH (see Security Considerations); the opt-out (Decision 5) is
  the manual override.
- *Alternative: assume-supported, no probe.* Rejected: silent degradation on old
  harnesses — the exact failure R7/R8 forbid.
- *Alternative: lazy post-hoc detection* (observe whether the hook fired).
  Rejected: only triggers after a user already got a bare worktree, and needs a
  brittle success-observation state machine.

### Decision 5 — init-time opt-out (PRD R9)

- **Chosen: a `niwa init --no-worktree-delegation` flag persisted as an
  `InstanceState` bool**, mirroring `SkipGlobal` / `NoOverlay`, read by the apply
  pipeline to skip the entire integration (no hook, no deny, no probe). Reversible
  by re-init without the flag.
- *Alternative: a `[instance]` workspace.toml toggle.* Rejected: that section is for
  declarative config merges that materialize into output, not apply control-flow
  toggles — inconsistent with how niwa expresses opt-outs.

### Decision 6 — per-repo install and idempotency (PRD R3, R11)

- **Chosen: install via the existing per-repo materializers.** The
  `HooksMaterializer` ships the worktree hook script(s); the `SettingsMaterializer`
  writes the `WorktreeCreate`/`WorktreeRemove` (or `permissions.deny`) entries into
  each repo's `settings.local.json`. Both already run per repo on every apply and
  are idempotent.
- *Alternative: a new bespoke installer.* Rejected: duplicates machinery that
  already exists, runs per-repo, and is idempotent.

## Decision Outcome

niwa gains a worktree-delegation integration, installed per-repo and on by default,
that routes Claude Code's native worktree creation through niwa:

1. On `niwa apply`, unless the instance opted out, niwa probes the Claude Code
   version once.
2. **Supported harness:** the per-repo materializers install a `WorktreeCreate`
   hook (and `WorktreeRemove` hook) into each repo's `settings.local.json`, wired
   to `niwa worktree from-hook`.
3. **Unsupported harness:** the materializers instead write
   `permissions.deny: ["EnterWorktree","ExitWorktree"]` and steer-to-niwa guidance,
   and niwa emits a one-time fallback notice.
4. At runtime, `WorktreeCreate` → `niwa worktree from-hook` create path → a niwa
   worktree (with secrets + context) → its path echoed back to Claude as the session
   working dir. `WorktreeRemove` → `from-hook` remove path → guarded teardown
   (force only past the agent's own attach lock; dirty worktrees are retained and
   logged, never silently deleted).
5. `niwa worktree create` gains `--json` so the path is machine-readable.

This satisfies "one mechanism, not two": when the hook is active the native tool
produces a niwa worktree; when it isn't, the native tool is denied and the agent is
explicitly redirected — never a silent bare checkout.

## Solution Architecture

**Components**

- **cwd→repo-name resolver** (new, `internal/workspace`): the component that turns a
  hook-supplied `cwd` path into a known workspace repo name. No such reverse
  resolver exists today (`findRepoInWorkspace` is name→path; the repo index built at
  apply.go is name→absolute-path). It walks the instance's repo set, canonicalizes
  both the incoming `cwd` and each candidate repo path with `filepath.EvalSymlinks`
  + `Clean`, and returns the repo whose canonical path is a prefix of the canonical
  `cwd` (longest-prefix match). A `cwd` that resolves outside every workspace repo
  is rejected. This resolver is the single enforcement point for the security
  section's "reject out-of-workspace cwd" guarantee.
- **`niwa worktree from-hook`** (new subcommand, `internal/cli`): the single entry
  point for both hook events. Reads hook JSON on stdin; dispatches on
  `hook_event_name`.
  - *Create*: resolve the repo via the cwd→repo-name resolver (reject on no match).
    Derive a purpose from `name` (control-chars stripped). Run the full two-step
    flow — `CreateSession` then `applyContentToWorktree` — so the worktree gets its
    secrets and CLAUDE context (R10's warn-and-continue is surfaced here). Print the
    absolute worktree path to stdout, exit 0. On error, exit non-zero (Claude fails
    creation — correct, since a partial worktree is worse than none).
  - *Remove*: map the worktree to a niwa session by worktree path (scan
    `ListSessionLifecycleStates()` for the matching `WorktreePath`; Claude's
    `session_id` is not niwa's sid). Release the agent's attach lock, attempt
    `DestroySession(force=false)`, and on a genuine dirty rejection log-and-retain
    rather than force-delete (Decision 3). Always exit 0 (WorktreeRemove is
    non-blocking anyway).
- **`niwa worktree create --json`** (extend existing command): emit the worktree
  path (and session id) as JSON, reusing the `--json` precedent from
  `niwa worktree list`. `from-hook` shares the same internal create path.
- **Harness probe** (`internal/workspace`, e.g. `harness_compat.go`): run
  `claude --version` once per apply, parse, compare to the baseline; return
  supported/unsupported, optimistic on error.
- **Materializer changes**: `SettingsMaterializer` emits either the worktree-hook
  entries or the `permissions.deny` entries based on the probe result.
  `WorktreeCreate`/`WorktreeRemove` event names ride the existing snake→Pascal hook
  mapping. Today the materializer writes only `permissions.defaultMode`, so emitting
  a `permissions.deny` array is a new capability the materializer must gain.
  `HooksMaterializer` ships the mandatory hook shim script (a hook entry can only be
  a script path). The whole block is gated off when the instance opted out.
- **Instance-state opt-out**: new `InstanceState` bool set by
  `niwa init --no-worktree-delegation`, read in the apply pipeline.
- **One-time notice**: a `worktree-fallback` notice key emitted when fallback mode
  is active, recorded in `InstanceState.DisclosedNotices`.

**Data flow (supported harness)**

```
agent asks for a worktree
  -> Claude WorktreeCreate hook shim (per-repo settings.local.json)
     -> niwa worktree from-hook  (stdin: session_id, cwd, name)
        -> cwd -> repo name (canonicalized, prefix-match, reject if outside)
        -> CreateSession(repo, purpose)          # worktree + branch + state
        -> applyContentToWorktree(...)           # secrets + CLAUDE context (R10)
        -> stdout: <absolute worktree path>
  -> Claude uses that path as the session working directory

agent/session exits
  -> Claude WorktreeRemove hook shim
     -> niwa worktree from-hook (remove)
        -> worktree path -> session (scan WorktreePath)
        -> detach; DestroySession(force=false); dirty -> log-and-retain
```

**Data flow (unsupported harness):** `EnterWorktree` is denied; the agent reads the
steer-to-niwa guidance and runs `niwa worktree create` directly; niwa emitted the
one-time fallback notice on apply.

## Implementation Approach

1. **`--json` for `niwa worktree create`** — smallest, independently useful;
   unblocks the adapter's machine-readable contract (R4).
2. **cwd→repo-name resolver + `niwa worktree from-hook` subcommand** — the
   canonicalizing resolver; create dispatch (resolver → two-step
   `CreateSession` + `applyContentToWorktree` → echo path); remove dispatch
   (path→session by `WorktreePath`, detach, guarded destroy, dirty→log-and-retain).
   Unit-tested with synthetic hook JSON, including out-of-workspace and symlinked
   `cwd` rejection.
3. **Harness probe** — `claude --version` parse + baseline compare, optimistic on
   error.
4. **Materializer wiring** — emit hook-or-deny per probe; ship the hook shim;
   one-time fallback notice.
5. **Init opt-out** — flag + `InstanceState` field + apply-pipeline gate.
6. **Functional coverage** — a `@critical` Gherkin scenario exercising the
   create → list → destroy path through the hook, plus the deny path.

## Security Considerations

- **Path traversal / arbitrary-location worktrees via `cwd`.** `cwd` comes from
  Claude's hook payload and is **not** trusted as a location. The cwd→repo-name
  resolver canonicalizes both the incoming `cwd` and each candidate repo path with
  `filepath.EvalSymlinks` + `Clean` before a longest-prefix comparison, and rejects
  any `cwd` (including `..`- or symlink-bearing paths) that does not resolve under a
  known workspace repo. Without canonicalization, a `..` or symlinked `cwd` could
  evade or spoof the workspace check — so canonicalization is a security
  requirement, not a nicety.
- **Command injection via hook stdin.** `from-hook` passes `name`/`cwd` as argv and
  never interpolates them into a shell. `name` is only persisted as the session
  purpose and never enters a git ref (branches are `prefix + random-hex`), so there
  is no branch-ref injection risk; the residual concern is control characters in
  stored/displayed metadata, which `from-hook` strips from `name`.
- **Hook command provenance.** The hook entry runs a fixed niwa command niwa itself
  writes into `settings.local.json` (mode 0o600, git-excluded via
  `.git/info/exclude`). The trust boundary is identical to every other
  niwa-materialized hook; an attacker who can already rewrite `settings.local.json`
  has local write access and a larger problem.
- **Force-destroy data loss — defense in depth.** Rather than rely on Claude's
  clean-only removal as the sole safeguard, the remove path attempts a guarded
  (non-force) destroy and forces only past the agent's own attach lock, never past
  the dirty guard (Decision 3). Genuine uncommitted work (niwa scaffolding is
  git-excluded, so it doesn't count) causes a log-and-retain, not a silent delete.
- **Version probe / trusted PATH.** Executing `claude --version` runs a PATH binary
  and its output is parsed, never executed. The optimistic-on-error behavior
  (assume supported if the probe fails) assumes a trusted PATH — the same trust
  model niwa already extends to the `git` binary it shells out to. A hostile PATH is
  out of scope (it would compromise far more than this feature).
- **No new secret surface.** Secret materialization reuses `applyContentToWorktree`
  unchanged; R10 only requires that resolution failures be surfaced (they already
  are, on stderr), not new handling.

## Consequences

**Positive**

- The native agent worktree path yields a niwa worktree — one mechanism, not two
  (R1, R5), with niwa as system of record (R6).
- Built on existing per-repo materializers, one-time notices, and instance-state
  opt-outs — minimal new surface, idempotent by construction (R3, R11, R12).
- Fallback and secret-degradation are disclosed, never silent (R7, R8, R10).
- Logic lives in a testable Go subcommand, not shell.

**Negative / mitigations**

- *Couples niwa to Claude Code's release behavior via the version baseline.*
  Mitigation: baseline is a single constant, optimistic on probe failure, and the
  opt-out plus fallback both exist; update the constant in a patch if hook behavior
  changes.
- *Apply-time probe adds a subprocess per apply.* Mitigation: one `claude --version`
  call per apply, off the per-tool-call path; skip entirely when opted out.
- *A worktree the agent left with genuine uncommitted work is not auto-cleaned.*
  By design the remove path retains (and logs) a dirty worktree rather than
  force-deleting it, trading a possible orphan for never silently destroying work.
  The developer reclaims it with `niwa worktree destroy --force`. niwa still knows
  about it (the session record persists), so this is a surfaced orphan, not a
  silent one.
- *A pre-baseline harness loses transparent delegation.* Mitigation: that is exactly
  the disclosed deny+steer fallback, not a silent failure.

## References

- docs/prds/PRD-niwa-default-worktree.md — requirements this design implements.
- docs/briefs/BRIEF-niwa-default-worktree.md — framing.
- docs/spikes/SPIKE-niwa-default-worktree.md — feasibility, hook contract, install scope.
- tsukumogami/niwa#166 — originating issue.
