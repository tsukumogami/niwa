---
schema: design/v1
status: Current
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
  The emitted hook command resolves niwa from PATH with the recorded absolute
  path as a fallback, so an installed hook survives a niwa upgrade, and a
  delegated create that fails partway reconciles through the same guarded
  teardown the remove path uses.
rationale: |
  The feasibility spike proved per-repo hooks fire in git repos and replace
  default worktree creation; niwa already owns the per-repo settings/hooks install
  surface. A hook-backed Go subcommand keeps logic testable and avoids brittle
  shell parsing. Hook and deny are mutually exclusive (a deny blocks the tool
  before the hook runs), so a capability probe must choose one.
---

# DESIGN: niwa as the default worktree mechanism

## Status

Current

Revised after the shipped integration proved not to survive a niwa upgrade.
Decisions 7, 8, and 9 are additions; Decision 6 no longer fixes the hook
command's shape, which Decision 7 now owns. Decision 4 was re-validated against
a live harness and stands unchanged — see the note under it.

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

- **Chosen: a `niwa worktree from-hook` subcommand, invoked directly by Claude (no
  shim script).** A Claude Code hook `command` is an arbitrary command string Claude
  runs with the hook JSON piped to stdin (the docs' own `WorktreeCreate` example is
  an inline `bash -c`), so niwa writes the hook entry as an absolute-path invocation
  of its own binary — `"<abs>/niwa worktree from-hook"` — with no separate shim file
  and no PATH dependency. The subcommand reads the hook JSON on stdin, resolves the
  repo via a **new cwd→repo-name resolver** (see Solution Architecture), and
  delegates to the same create/destroy core the human commands use, running the
  two-step flow `CreateSession` **then** `applyContentToWorktree` (the step that
  materializes secrets and CLAUDE context and carries R10's warn-and-continue), then
  prints the worktree path to stdout.
- **The reusable logic lives on the human commands, not walled off in `from-hook`.**
  `niwa worktree create` gains cwd-inference (repo arg optional, resolved via the
  same resolver) and an optional purpose, so a developer can run a bare
  `niwa worktree create` from inside a repo; `niwa worktree destroy` gains
  `--by-path`. `from-hook` is a thin entry owning only the hook I/O contract (stdin
  JSON in, bare path out, non-zero-exit-fails on create, non-blocking on remove),
  delegating the real work to that shared core.
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

  **Re-validated.** This decision was reported as falsified — the claim being
  that the harness had stopped honoring the per-repo hook above the baseline,
  making the version floor unable to express "supported in a range". Direct
  reproduction says otherwise: on both the version the report tested and a later
  one, a per-repo `WorktreeCreate` hook fires from inside a git repo and
  replaces native worktree creation. A hook that exits non-zero makes the tool
  call fail loudly with the hook's stderr; there is no silent fallback to a
  native worktree. The reported symptom — a bare worktree under the repo's own
  `.claude/worktrees/` — reproduces only in a session with no hook in scope at
  all. The probe and the version floor stand as designed, and no non-monotonic
  detection is warranted.

### Decision 5 — init-time opt-out (PRD R9)

- **Chosen: a `niwa init --no-worktree-delegation` flag persisted as an
  `InstanceState` bool**, mirroring `SkipGlobal` / `NoOverlay`, read by the apply
  pipeline to skip the entire integration (no hook, no deny, no probe). Reversible
  by re-init without the flag.
- *Alternative: a `[instance]` workspace.toml toggle.* Rejected: that section is for
  declarative config merges that materialize into output, not apply control-flow
  toggles — inconsistent with how niwa expresses opt-outs.

### Decision 6 — per-repo install and idempotency (PRD R3, R11)

- **Chosen: install via the existing per-repo `SettingsMaterializer`.** It writes
  the `WorktreeCreate`/`WorktreeRemove` hook entries — or the `permissions.deny`
  entries — into each repo's `settings.local.json`. No shim script is shipped
  (the hook command invokes the niwa binary directly), so no `HooksMaterializer`
  change is needed. The materializer already runs per repo on every apply and is
  idempotent.
- *Alternative: a new bespoke installer.* Rejected: duplicates machinery that
  already exists, runs per-repo, and is idempotent.

### Decision 7 — how the hook command names the niwa binary (PRD R3, R11)

The original form of Decision 6 wrote the hook as a bare absolute path taken
from `os.Executable()`. That does not survive a niwa upgrade. Under a versioned
install layout — where each release lives in its own directory and a stable
shim on `PATH` points at the current one — `os.Executable()` resolves to the
version-pinned path, so every installed hook keeps invoking the release that
happened to run `niwa apply`. Upgrading niwa leaves every previously applied
workspace calling the old binary until each one is re-applied, and nothing
detects or reports that. When the pinned release predates a fix the hook
depends on, agent-initiated worktree creation is broken workspace-wide.

The pinning is not recoverable from inside the process. On Linux
`os.Executable()` reads `/proc/self/exe`, which the kernel has already resolved
past every symlink, so the stable path is gone before niwa can observe it.

- **Chosen: a `PATH`-first command with the recorded absolute path as a
  fallback.** The emitted command is

  ```
  command -v niwa >/dev/null 2>&1 && exec niwa <subcommand>; exec '<abs>' <subcommand>
  ```

  `PATH` resolution is what fixes the bug: a user who upgrades niwa gets a
  working hook with no re-apply. The recorded absolute path costs one clause and
  keeps the integration working where niwa is not on the hook subprocess's
  `PATH` — a harness launched from a desktop environment rather than a shell
  inherits the session manager's `PATH`, not the one a shell profile builds.
  Dropping the absolute path entirely would turn those working setups into loud
  failures.

  Two details are load-bearing. The branches are separated by `;` rather than
  `||`: a failed `exec` terminates a non-interactive shell before `||` would be
  evaluated, so `||` reads as a fallback it is not. And `<abs>` is
  single-quoted, because hook commands go through a shell and an install path
  containing a space would otherwise split.

  The same shape applies to **both** hook consumers — the per-repo
  `WorktreeCreate`/`WorktreeRemove` command and the workspace-root `SessionStart`
  `niwa instance from-hook` command — through one shared helper. Fixing only the
  per-repo hook would leave the defect self-perpetuating: the root hook
  provisions instances in-process, so a stale binary running that apply stamps
  its own stale path into every repo of each newly created instance.

- *Alternative: `PATH`-only, dropping the absolute path.* Rejected: it produces
  a byte-identical command across hosts, which is the strongest form of
  idempotency, but it regresses every environment where the harness runs without
  niwa on `PATH` from working to failing.
- *Alternative: keep the absolute path and detect staleness* (record the
  applying version, compare on a later run, nudge a re-apply). Rejected: it
  detects rather than fixes — the hook stays broken until the user acts on a
  notice — and the detector would have to run inside the stale binary, which is
  the wrong version to know it is the wrong version.
- *Alternative: write an absolute path to a stable non-versioned location.*
  Rejected: not implementable. `os.Executable()` cannot recover the pre-symlink
  path on Linux, and reconstructing it via a `PATH` lookup at apply time is the
  chosen option with the answer re-frozen — which reacquires the staleness bug
  the moment the install manager repoints its shim.

### Decision 8 — create-path failure reconciliation (PRD R6, R8)

`niwa worktree from-hook` creates the git worktree and writes the session record
before installing content. Session creation is already atomic — every failure
after `git worktree add` cleans up after itself — but content install is not.
When it fails, the tool call fails loudly (correct, and required by R8), while
the git worktree and an `active` session record both survive. `niwa worktree
list` then reports an `active` worktree no process is in, which contradicts R6's
premise that niwa is the system of record for active worktrees. Repeated
failures accumulate: each agent retry leaves another row and another directory.

- **Chosen: reconcile through the existing teardown.** On any failure after
  `git worktree add` succeeds, the hook create path runs the same guarded
  `DestroySession` the `WorktreeRemove` path runs — non-force — and returns an
  error naming both the original failure and what niwa did about it. In the
  normal case that is a full rollback with no new machinery: the record moves to
  `ended`, the worktree is removed, and the session branch is deleted (it was
  created at HEAD with no commits, so the guarded delete succeeds). In the rare
  case where a workspace-authored worktree apply script touched tracked files
  before a later step failed, the dirty guard refuses and niwa retains and logs
  — character for character the behavior Decision 3 already specifies.

  This makes the create path consistent with Decision 3 by construction rather
  than by argument: it is the same call, the same guard ordering, the same
  retain-and-log fallback. A reviewer checks that it calls the same function
  rather than reasoning afresh about whether create-failure cleanup is safe.

  The interactive `niwa worktree create` path deliberately keeps its existing
  retain-and-tell behavior. A human is standing at the terminal, the worktree is
  a real place they asked for, and re-syncing it is a genuine repair. An agent
  never enters the directory on the failure path and will not come back to it.

- *Alternative: a new `failed` terminal status.* Rejected: terminality is
  spelled out as a two-value comparison at several production sites, and missing
  one leaves a `failed` session reading as live forever. The information such a
  status would carry — why this worktree does not exist — belongs in the error
  the user reads at the moment it happens. If it later proves necessary, an
  additive `failure_reason` field on the state file is the cheap follow-up, not
  a new status.
- *Alternative: bespoke full rollback* (remove worktree, delete branch, delete
  the state file). Rejected: deleting a state file is a new operation with no
  precedent — niwa marks records terminal and keeps them — and a rollback that
  fails partway can delete the record while leaving the directory, producing
  exactly the unsurfaced orphan this decision exists to prevent.
- *Alternative: leave the state and improve the error.* Rejected: it leaves
  `niwa worktree list` asserting an `active` worktree that is not, and the rows
  accumulate for as long as the underlying cause persists.

### Decision 9 — worktree settings parity (PRD R3)

`ApplyToWorktree` runs the repo materializers against a worktree but never
passes the worktree-delegation decision, and its options struct has no field to
carry one. A niwa-managed worktree's own `settings.local.json` therefore records
neither the hook entries nor the deny entries, while the clone it was made from
records one of them.

- **Chosen: thread the delegation decision into the worktree apply path**, so a
  worktree's settings match its clone's. The gap is latent rather than
  user-visible today — delegation still resolves for a session working inside a
  worktree — but the two configurations drifting is the kind of divergence R3's
  "install at the scope required for it to take effect" exists to rule out, and
  it would become load-bearing the moment settings resolution changed.

  The mechanism behind "latent" is worth stating, because the obvious objection
  is that installing the hook *into* worktrees would break nested creation: the
  cwd-to-repo resolver deliberately rejects a path under `.niwa/worktrees/`, so a
  hook firing with a worktree cwd would fail. It does not fire with one. For a
  session inside a linked git worktree the harness resolves settings from the
  main checkout and reports the main checkout as the hook payload's `cwd`, so the
  resolver is never handed a worktree path. Verified end to end: creating a
  worktree from inside a worktree yields a second niwa-managed worktree with its
  own session record. Parity is a consistency fix, not a repair of an observed
  bypass, and it does not introduce one.

  What parity means here is *which branch* is installed — hook or deny — not
  byte-equality. The fallback arm records whichever binary ran that particular
  apply, so a worktree and its clone can carry different absolute fallbacks.
  Since Decision 7 made PATH authoritative, that difference does not affect which
  niwa runs.
- *Alternative: leave it and document the asymmetry.* Rejected: the asymmetry
  has no rationale behind it — it is an omission, not a decision — and
  documenting an omission costs more than closing it.

## Decision Outcome

niwa gains a worktree-delegation integration, installed per-repo and on by default,
that routes Claude Code's native worktree creation through niwa:

1. On `niwa apply`, unless the instance opted out, niwa probes the Claude Code
   version once.
2. **Supported harness:** the per-repo `SettingsMaterializer` writes
   `WorktreeCreate`/`WorktreeRemove` hook entries into each repo's
   `settings.local.json`, each an absolute-path `niwa worktree from-hook` command
   Claude invokes directly (no shim script).
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
- **cwd-aware human commands** (extend existing `internal/cli` worktree commands):
  `niwa worktree create` makes the repo arg optional (inferred from the process cwd
  via the resolver) and the purpose optional, and gains `--json` (emit worktree path
  + session id, reusing the `--json` precedent from `niwa worktree list`).
  `niwa worktree destroy` gains `--by-path <path>` (resolve path→session internally).
  These are the shared create/destroy core; humans get a bare `niwa worktree create`
  from inside a repo, and `from-hook` delegates to the same core.
- **`niwa worktree from-hook`** (new subcommand, `internal/cli`): a thin entry Claude
  invokes **directly** (the hook `command` is an absolute-path `niwa worktree
  from-hook`, no shim script). Reads hook JSON on stdin; dispatches on
  `hook_event_name`; owns only the hook I/O contract and delegates to the core.
  - *Create*: resolve the repo via the cwd→repo-name resolver from the stdin `cwd`
    (reject on no match). Derive a purpose from `name` (control-chars stripped). Run
    the two-step flow — `CreateSession` then `applyContentToWorktree` — so the
    worktree gets its secrets and CLAUDE context (R10's warn-and-continue surfaced).
    Print the absolute worktree path to stdout, exit 0. On error, exit non-zero
    (Claude fails creation — correct, since a partial worktree is worse than none).
  - *Remove*: map the worktree to a niwa session by worktree path (scan
    `ListSessionLifecycleStates()` for the matching `WorktreePath`; Claude's
    `session_id` is not niwa's sid). Release the agent's attach lock, attempt
    `DestroySession(force=false)`, and on a genuine dirty rejection log-and-retain
    rather than force-delete (Decision 3). Always exit 0 (WorktreeRemove is
    non-blocking anyway).
- **Harness probe** (`internal/workspace`, e.g. `harness_compat.go`): run
  `claude --version` once per apply, parse, compare to the baseline; return
  supported/unsupported, optimistic on error.
- **Materializer change**: `SettingsMaterializer` emits either the
  `WorktreeCreate`/`WorktreeRemove` hook entries (each an absolute-path
  `niwa worktree from-hook` command) or the `permissions.deny` entries based on the
  probe result. `WorktreeCreate`/`WorktreeRemove` event names ride the existing
  snake→Pascal hook mapping. Today the materializer writes only
  `permissions.defaultMode`, so emitting a `permissions.deny` array is a new
  capability it must gain. No shim script and no `HooksMaterializer` change are
  needed. The whole block is gated off when the instance opted out.
- **Instance-state opt-out**: new `InstanceState` bool set by
  `niwa init --no-worktree-delegation`, read in the apply pipeline.
- **One-time notice**: a `worktree-fallback` notice key emitted when fallback mode
  is active, recorded in `InstanceState.DisclosedNotices`.

**Data flow (supported harness)**

```
agent asks for a worktree
  -> Claude WorktreeCreate hook (per-repo settings.local.json: abs-path command)
     -> niwa worktree from-hook  (stdin: session_id, cwd, name)
        -> cwd -> repo name (canonicalized, prefix-match, reject if outside)
        -> CreateSession(repo, purpose)          # worktree + branch + state
        -> applyContentToWorktree(...)           # secrets + CLAUDE context (R10)
        -> stdout: <absolute worktree path>
  -> Claude uses that path as the session working directory

agent/session exits
  -> Claude WorktreeRemove hook (abs-path command)
     -> niwa worktree from-hook (remove)
        -> worktree path -> session (scan WorktreePath)
        -> detach; DestroySession(force=false); dirty -> log-and-retain
```

**Data flow (unsupported harness):** `EnterWorktree` is denied; the agent reads the
steer-to-niwa guidance and runs `niwa worktree create` directly; niwa emitted the
one-time fallback notice on apply.

## Implementation Approach

1. **cwd-aware `niwa worktree create` + `--json` + `destroy --by-path`** — the
   shared core: optional repo (inferred from cwd via the resolver), optional purpose,
   machine-readable output (R4), and path-based destroy. Independently useful to
   humans.
2. **cwd→repo-name resolver + `niwa worktree from-hook` subcommand** — the
   canonicalizing resolver; create dispatch (resolver → two-step
   `CreateSession` + `applyContentToWorktree` → echo path); remove dispatch
   (path→session by `WorktreePath`, detach, guarded destroy, dirty→log-and-retain).
   Invoked directly by Claude (no shim). Unit-tested with synthetic hook JSON,
   including out-of-workspace and symlinked `cwd` rejection.
3. **Harness probe** — `claude --version` parse + baseline compare, optimistic on
   error.
4. **Materializer wiring** — `SettingsMaterializer` emits hook-or-deny per probe
   (hook entry = absolute-path `niwa worktree from-hook`; new `permissions.deny`
   capability); every-apply fallback disclosure + one-time explainer. No shim, no
   `HooksMaterializer` change.
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
- **Trusted PATH: version probe and hook command.** Executing `claude --version`
  runs a PATH binary and its output is parsed, never executed. The
  optimistic-on-error behavior (assume supported if the probe fails) assumes a
  trusted PATH — the same trust model niwa already extends to the `git` binary it
  shells out to. Decision 7 widens the same assumption to the hook command, which
  resolves `niwa` from PATH before falling back to a recorded absolute path. This
  is the same class of trust rather than a new boundary: an attacker who can
  prepend a PATH entry already controls the `git` niwa invokes constantly, and can
  rewrite `settings.local.json` (mode 0o600, user-owned) directly. A hostile PATH is
  out of scope (it would compromise far more than this feature).
- **Shell quoting in the emitted hook command.** The hook command is a shell
  string, so the fallback path is single-quoted with embedded single quotes
  escaped. Without quoting, an install path containing a space would split into
  separate words — a latent defect in the original single-token form that a
  longer composed command would otherwise make reachable.
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
- Installed hooks survive a niwa upgrade without a re-apply (Decision 7), and a
  failed delegated create leaves no phantom `active` worktree behind
  (Decision 8).

**Negative / mitigations**

- *A contributor working on niwa itself may have their hook resolve the release
  binary from PATH rather than their branch build.* Decision 7 prefers PATH, so
  a local `from-hook` change is not exercised by a manual worktree test unless
  the branch build is ahead of the release on PATH. Mitigation: the ordinary Go
  workflow (install the branch build first); worth knowing because it bites
  precisely the people changing this code.
- *Which binary actually ran is no longer readable off the settings file.* The
  emitted command names both candidates, and resolution depends on the invoking
  process's PATH. Mitigation: the fallback path is still recorded verbatim, and
  `niwa --version` from the same environment answers the question directly.
- *`command -v niwa` proves presence, not compatibility.* On a machine with more
  than one niwa on PATH, the hook runs whichever comes first; if that one
  predates `worktree from-hook` it fails rather than falling through, because
  `exec` has already replaced the shell. This is a real regression for that
  configuration, traded against permanent silent staleness for everyone else —
  and it fails loudly, which the staleness it replaces did not. Probing for the
  subcommand rather than the binary would close it at the cost of a second
  subprocess per hook invocation.
- *Workspaces applied before this change keep the old pinned command* until they
  are applied once more. The fix is not retroactive; it takes effect from the
  next apply onward, which is worth calling out in release notes.
- *A reconciled create failure records `ended`, which does not distinguish "the
  agent finished here" from "this never got off the ground".* Mitigation: the
  reason is in the error the user reads at the time; if the distinction proves
  to matter, an additive `failure_reason` field is the follow-up rather than a
  new terminal status.

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
