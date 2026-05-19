<!-- decision:start id="init-bootstrap-rollback" status="assumed" -->
### Decision: Rollback semantics for `niwa init <name> --from <empty-remote> --bootstrap`

**Context**

The framing decision (T1) locked `niwa init --bootstrap` as a single
atomic command that chains init -> create -> session-create and lands
the user inside a worktree on success. Each step produces durable
on-disk state: init scaffolds the workspace dir, writes
`.niwa/workspace.toml` and the workspace-root `.niwa/instance.json`,
and writes a registry entry in `<XDG_CONFIG>/niwa/config.toml`; create
mkdirs the instance dir, clones source-org repos, writes the real
`instance.json`, installs `.niwa/roles/<repo>/`, and starts the mesh
daemon; session-create makes the `session/<sid>` branch and worktree
under `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, writes the
session-state JSON, and starts a per-worktree daemon.

Today's `niwa init` uses an aggressive deferred-cleanup pattern at
`internal/cli/init.go:215-226`: `os.RemoveAll(workspaceRoot)` runs on
any error path and is disarmed only on success. That contract works
for a single-step command but does not scale to a 3-step chain. The
session-create step already has its own internal rollback contract on
daemon spawn timeout (`handlers_session.go:270-277`): it removes the
worktree, the session-state file, and the branch. The bootstrap-level
question is the rollback contract at the chain layer.

The user's stated framing for `--bootstrap` is "intro to niwa." A
first-time user hitting a failure mid-chain is the worst-case
onboarding moment to make decisions about. Whatever state survives
must be self-describing, and whatever recovery path bootstrap names
should be a real niwa command the user will need to learn anyway.

**Assumptions**

- A failed mid-chain bootstrap that leaves init and optionally create
  on disk is recoverable by running the same standard CLI commands
  the user would run standalone (`niwa create`,
  `niwa session create`). If wrong: bootstrap would need a dedicated
  resume command, which is R4's territory.
- `niwa destroy --instance` works as documented (validates, scans,
  terminates daemon, removes the instance dir). Source inspection at
  `internal/cli/destroy.go:103-138` confirms this. If wrong:
  bootstrap's create-failure recovery hint points the user at
  `rm -rf` instead, which is a worse UX but the rollback shape is
  unchanged.
- The clone cost for the empty-remote v1 case is small enough that
  re-cloning under a full rollback would be acceptable, but
  preserving cloned state on partial failure is still preferable. If
  wrong, R1's cost grows and R2's value grows; the choice is
  unchanged.
- Init's existing pre-flight collision check (registry entry already
  exists for this name) gives a good-enough idempotency story for a
  retry-from-scratch flow without a transactional log. If wrong,
  bootstrap's on-retry behavior needs tightening, but that is a
  follow-up, not a rejection of R2.

**Chosen: R2 (Stepwise rollback)**

The failed step rolls back only its own artifacts. Earlier successful
steps survive. The bootstrap-level contract is:

1. **init step fails (before or during scaffolding)**: bootstrap
   removes `<cwd>/<name>/` entirely. No instance dir, no registry
   entry survives. This matches today's standalone-init behavior.
   The implementation refactors today's `workspaceCreated` defer
   into a step-scoped cleanup that disarms when the init step
   reports success, not when the whole chain reports success.

2. **create step fails after init succeeded**: bootstrap delegates
   to the in-process equivalent of `niwa destroy --instance` —
   terminate the mesh daemon, remove the instance dir (any
   partially-cloned repos and partial `instance.json` go with it).
   The workspace dir, `.niwa/workspace.toml`, the workspace-root
   `.niwa/instance.json`, and the registry entry SURVIVE. The
   failure message says: "init succeeded but create failed. Run
   `niwa create` to retry, or `niwa destroy <name>` to wipe and
   start over."

3. **session-create step fails after create succeeded**: bootstrap
   does NOT cascade-undo the create step. The session-create
   internal contract (`handlers_session.go:270-277`) has already
   cleaned up its own artifacts (worktree, session-state file,
   branch) on daemon spawn timeout. Bootstrap adds no rollback on
   top. The workspace and instance both SURVIVE. The failure
   message says: "init and create succeeded but session-create
   failed. Run `niwa session create <repo> <purpose>` to retry."

A subtle point on registry-entry timing: if create fails after init
wrote the registry entry, the registry has a pointer to a workspace
with no instance. `niwa list` shows it. `niwa init <same-name>`
refuses-with-hint (existing collision detection in init's pre-gate).
This is the correct R2 behavior — the user is told the name is taken
and pointed at the workspace. The alternative (defer the registry
write until the full chain succeeds) would split init's contract
between standalone and chained modes, a hidden mode-switch that bites
later. R2 keeps init's registry-write timing identical to standalone.

Bootstrap is NOT idempotent across runs in the sense of "skip-ahead
on retry." If the user re-runs `niwa init <same-name> --bootstrap`
after a partial failure, today's collision detection fires and refuses
with a hint. The user follows the hint to either retry the failed step
manually (`niwa create` or `niwa session create`) or wipe and start
over (`niwa destroy <name>`). This is the explicit trade-off versus
R4: bootstrap does not own a parallel state machine; the existing
lifecycle commands' own pre-existing-state detection is the
idempotency surface.

**Rationale**

R2 is the only option that satisfies all four of the following at once:

1. It matches the contract the framing decision T1 already specified
   in its Consequences section. T1 said "init failure removes the
   workspace root; create failure leaves init's scaffold intact but
   removes the instance directory; session-create failure leaves
   both intact." R2 is that contract; the other options re-litigate
   T1.

2. It preserves expensive state on later-step failures. Create just
   finished cloning the source-org repos when session-create fails;
   discarding that work to restart from `niwa create` would be
   needlessly destructive. R2 keeps it.

3. Every failure-recovery path is a real, documented niwa command.
   The user types `niwa create` or `niwa session create <repo>
   <purpose>` to retry, or `niwa destroy <name>` to wipe — all
   commands they would need to learn anyway. The failure message
   becomes an in-line tutorial. This matches the "intro to niwa"
   framing.

4. It adds no new state-machine surface. The existing lifecycle
   commands already detect their own pre-existing state (`niwa
   create` refuses on existing `instance.json`; `niwa session
   create` refuses on existing same-purpose session). Bootstrap
   delegates idempotency to those checks rather than maintaining a
   parallel log.

**Alternatives Considered**

- **R1 (Full rollback on any failure)**: tears down everything up to
  and including init on any step's failure. Rejected because it
  overpunishes the user when create has just finished cloning
  source-org repos: a session-create failure throws all of that work
  away even though session-create's own internal rollback already
  cleaned up its own artifacts. R1 also conflicts directly with the
  framing decision T1's Consequences section.

- **R3 (No rollback)**: leaves everything on disk regardless of
  failure point and prints a state-enumeration message. Rejected
  because a first-time user staring at a half-built workspace
  directory with a list of paths to clean up is exactly the worst-case
  onboarding outcome the framing decision warned against. The "intro
  to niwa" UX target requires the failure message to name standard
  niwa commands as the recovery path, which means bootstrap has to
  participate in cleanup, not abdicate it.

- **R4 (Stepwise with transactional log)**: R2 plus a transactional
  log that lets bootstrap auto-resume from the failed step on retry.
  Rejected because the auto-resume feature buys time the user can
  already buy via R2's "retry with `niwa create`" or "retry with
  `niwa session create`" hints, while introducing a parallel state
  machine that has to track drift between the log and reality (what
  if the user ran `niwa create` manually between attempts? what if
  they deleted the instance dir?). The idempotency story R4 promises
  is achievable in R2 via the existing lifecycle commands' own
  pre-existing-state detection without a separate log file. R4 is the
  natural follow-up if user feedback indicates retry friction is
  significant; v1 does not need it.

**Consequences**

What gets easier:

- The PRD can specify a single rollback contract that mirrors a
  shape the user will recognize from running the standalone
  lifecycle commands. The chained command behaves like the
  manual sequence on every failure path.
- Recovery paths are documented in failure messages, not in a
  separate troubleshooting section. The user learns
  `niwa create`, `niwa session create`, and `niwa destroy` as
  part of recovery — which is exactly the "intro to niwa"
  payoff `--bootstrap` is supposed to deliver.
- Expensive state (cloned repos) is preserved across step
  boundaries, so retrying a failed session-create does not
  re-pay the clone cost.
- session-create's existing internal rollback contract is reused
  verbatim. Bootstrap does not duplicate it.

What gets harder:

- Init's existing `workspaceCreated` defer
  (`internal/cli/init.go:215-226`) must be refactored into a
  step-scoped cleanup that disarms when the init step succeeds
  (not when the chain succeeds). Standalone init keeps the same
  user-visible behavior; bootstrap inherits the per-step
  contract. This refactor was already called out by the framing
  decision T1.
- Bootstrap must compose with `niwa destroy --instance` (or its
  in-process equivalent — internal function refactored out of
  `runDestroyInstance` in `internal/cli/destroy.go`) when create
  fails. The composition is straightforward but adds a
  cross-command dependency that needs a functional test.
- Stale registry entries are intentionally surfaced via
  `niwa list` and the `niwa init <same-name>` collision check.
  The error messages for both surfaces must clearly point at
  `niwa create` (retry) or `niwa destroy <name>` (wipe). This is
  a UX cost paid in the error-message wording, not in
  architecture.
- Idempotency for retry is delegated to the existing lifecycle
  commands' own pre-existing-state detection. If user feedback
  later indicates that re-running `niwa init --bootstrap` on a
  partial state is a common ask, R4's transactional log becomes
  a v2 conversation. The v1 contract is: re-running bootstrap on
  a partial workspace refuses with a hint; recovery is via the
  individual commands.
- Functional tests must cover three failure-injection points
  (init scaffolding failure, create pipeline failure,
  session-create daemon timeout) and assert the expected on-disk
  state after each. The `localGitServer` helper in
  `test/functional/` supports the test shape; the test count is
  three new `@critical` scenarios.
<!-- decision:end -->
