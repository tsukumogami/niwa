<!-- decision:start id="provisioning-pipeline-changes-and-migration" status="assumed" -->
### Decision: Provisioning Pipeline Changes and Migration

**Context**

The channel installer — currently
`internal/workspace/channels.go::InstallChannelInfrastructure`, called at
step 4.75 of `Applier.runPipeline` — must be rewritten to produce the new
mesh layout (`.niwa/roles/<role>/inbox/` with `in-progress/`,
`cancelled/`, `expired/` subbuckets; `.niwa/tasks/`;
`.niwa/sessions/sessions.json`; `.niwa/daemon.pid`;
`.niwa/daemon.log`). The call site, the ManagedFiles discipline, the
daemon lifecycle hooks in `Applier.Create`/`Applier.Apply` and
`runDestroy`, and the idempotency contract (AC-P10: re-apply leaves
queued tasks, in-progress tasks, and `sessions.json` byte-identical) are
all retained.

The open question is what happens when `niwa apply` runs against an
instance provisioned under the previous layout
(`.niwa/sessions/<uuid>/inbox/`). The installer writes the same paths
that will hold the new content (`sessions.json`, `.claude/.mcp.json`,
`.niwa/hooks/mesh-*.sh`, the `## Channels` section), so those upgrade
automatically via always-overwrite. The remaining issue is old
per-session UUID directories: these are created by `niwa session
register`, are absent from `InstanceState.ManagedFiles`, and therefore
are not cleaned up by the existing `cleanRemovedFiles` pass. They
linger as filesystem litter, they contain queued envelopes under a
schema that is not byte-compatible with the new daemon, and they sit
next to new-layout directories like `.niwa/roles/` that the new daemon
actually reads.

Phase 1 research confirmed that the current `SessionEntry` schema
already carries a `Role` field (so envelope-to-role migration would be
mechanically straightforward) and that niwa is at v0.8.0 with the
mesh feature shipped behind an opt-in flag but with no formal
backward-compatibility contract. The core tradeoff is how aggressively
the installer should try to preserve pre-upgrade queued envelopes.

**Assumptions**

- The mesh feature is pre-1.0 and the user base running the current
  layout is small enough that discarding queued envelopes on upgrade
  is an acceptable cost, provided users see a clear one-shot warning.
  If wrong: users on the old layout with in-flight tasks lose
  messages during upgrade with a warning but no migration path.
- Role-collision detection (AC-R2) is enforced inside this installer
  before any directory creation. The enumeration primitive (derive
  roles from workspace.toml `[channels.mesh.roles]` plus cloned repos
  plus hardcoded coordinator) is provided by a helper established in
  other decisions (Decision 5 for skill enumeration; R5 in the PRD).
  If the helper doesn't land in scope, this decision's installer
  carries the enumeration code itself.
- `.niwa/tasks/` and `.niwa/roles/<role>/inbox/*` DIRECTORIES are not
  individually tracked in `InstanceState.ManagedFiles`. Only
  apply-time written FILES are tracked (`.mcp.json`, `sessions.json`,
  the two hook scripts, and the niwa-mesh skill files at instance
  root and per-repo). Directories are `MkdirAll`-created; destroy's
  `os.RemoveAll` cleans them up. If wrong: drift-detection on
  runtime artifacts would be required, which is architecturally
  incorrect.
- Runtime artifacts (per-envelope JSON in role inboxes, per-task
  `state.json` / `transitions.log`) are written by the daemon and
  MCP tool handlers, not by the installer, and are NOT in
  ManagedFiles. This matches R2's "every written path" read as
  "every path the installer writes."

**Chosen: Hybrid blind-rewrite with opportunistic cleanup (Alternative D)**

The rewritten `InstallChannelInfrastructure` executes the following
sequence, in order:

1. **Guard**. Return `nil` immediately when
   `cfg.Channels.IsEnabled()` is false. This preserves the hybrid
   activation semantics (Decision 6 of prior design): no mesh
   infrastructure when `[channels.mesh]` is absent and neither
   `--channels` nor `NIWA_CHANNELS=1` is set.

2. **Enumerate roles and check collisions**. Build the role set:
   hardcoded `coordinator` at instance root, one role per cloned repo
   (basename of the repo directory), plus any explicit
   `[channels.mesh.roles]` entries from the merged config. If two
   distinct repos yield the same basename AND there is no explicit
   role override disambiguating them, return a non-zero-exit error
   matching AC-R2 phrasing. No filesystem mutations occur on this
   branch.

3. **Write idempotent apply-time infrastructure**:
   - `MkdirAll .niwa` (mode 0700).
   - `MkdirAll .niwa/tasks` (mode 0700). No ManagedFiles entry for
     the directory itself.
   - For each role `r`, `MkdirAll .niwa/roles/<r>/inbox/` and its
     four subbuckets `in-progress/`, `cancelled/`, `expired/`,
     `read/` (all mode 0700). No ManagedFiles entries.
   - `MkdirAll .niwa/sessions` (mode 0700).
   - `.niwa/sessions/sessions.json`: write `{"sessions":[]}\n` at
     mode 0600 ONLY IF absent. Always append to ManagedFiles so
     `cleanRemovedFiles` doesn't delete it on re-apply (existing
     pattern, retained).
   - `.claude/.mcp.json` at instance root: always-overwrite at mode
     0600. Content embeds `NIWA_INSTANCE_ROOT` via `json.Marshal`
     (existing `buildMCPJSON` helper). Append to ManagedFiles.
   - `.niwa/hooks/mesh-session-start.sh` and
     `mesh-user-prompt-submit.sh`: always-overwrite at mode 0755.
     Bodies are stubs that invoke `niwa session register` (only
     coordinators register per R40). Append each to ManagedFiles.
   - `.niwa/daemon.pid` and `.niwa/daemon.log` are NOT created by
     the installer. `EnsureDaemonRunning` (called after `SaveState`
     in `Applier.Create` and `Applier.Apply`) creates the PID file
     when it spawns the daemon. Reserved paths only.
   - `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` at mode
     0600. Content owned by Decision 5. Append to ManagedFiles.
   - Per-repo `<repoDir>/.claude/skills/niwa-mesh/SKILL.md` and
     `<repoDir>/.claude/.mcp.json`: written by per-repo
     materializers (delegated — not by this installer — because
     materializer ordering is owned by a sibling decision). This
     installer is responsible only for instance-root writes.
   - `## Channels` section in `workspace-context.md`: replace
     existing body if the header is present, else append. Detection
     uses a line-range scan (find the header, find the next
     top-level `##` heading or EOF, replace the slice). The new
     body lists the R10 tool set and the short pointer to the
     niwa-mesh skill per R12. No ManagedFiles entry for this path
     because `workspace-context.md` is tracked by
     `InstallWorkspaceContext` at step 4.5 (existing discipline
     retained).

4. **Opportunistic cleanup of old-layout litter** (one-shot):
   - Read `.niwa/sessions/sessions.json`. If any entry has
     `InboxDir` matching the pattern `.niwa/sessions/<uuid>/inbox/`
     (the old layout's discriminator), old layout is detected.
   - Enumerate top-level entries under `.niwa/sessions/` that are
     directories whose name is a UUID (sibling of `sessions.json`).
     Count queued JSON envelopes inside each `<uuid>/inbox/*.json`.
   - If the count is > 0, emit a stderr warning via
     `a.Reporter.DeferWarn`:

     ```
     warning: discarded N queued mesh envelope(s) from the
     previous layout. Old envelopes are not compatible with the
     new task model.
     ```

   - `os.RemoveAll(.niwa/sessions/<uuid>)` for each UUID directory.
   - Rewrite `sessions.json` to drop entries whose `InboxDir`
     matches the old pattern (coordinator entries that re-register
     under the new layout will be re-inserted by the
     `SessionStart` hook; worker entries are abandoned per R40).
   - Record the one-shot notice key `mesh-layout-migrated` in
     `DisclosedNotices` so subsequent applies are silent. (Pattern
     matches existing `noticeProviderShadow` and
     `noticeChannelsFromFlag` keys in `apply.go`.)
   - The cleanup is written as a standalone helper function,
     `migrateLegacyMeshLayout(instanceRoot, reporter, disclosed)`,
     so it can be dropped from the codebase in a future release
     after the upgrade window is closed.

5. **Return**. Append all written files to the pipeline's
   `writtenFiles` accumulator as today. `Applier.runPipeline` then
   runs its `cleanRemovedFiles` pass (comparing
   `existingState.ManagedFiles` against the current pipeline's
   emitted managed files) and removes any prior-state path no
   longer produced. For paths that exist in both old and new
   layouts (`.mcp.json`, the hook scripts, `sessions.json`), the
   always-overwrite / only-if-absent rules above keep the content
   correct without requiring `cleanRemovedFiles` to do anything.

**`niwa destroy` interaction** is unchanged. `runDestroy` in
`internal/cli/destroy.go` calls `workspace.TerminateDaemon` (SIGTERM
+ grace + SIGKILL based on `NIWA_DESTROY_GRACE_SECONDS`) and then
`workspace.DestroyInstance` (`os.RemoveAll(instanceDir)`). The new
`.niwa/tasks/<task-id>/state.json`, `.niwa/tasks/<task-id>/transitions.log`,
and `.niwa/roles/<role>/inbox/**` subtrees are just more nested files
under the instance directory; RemoveAll handles them without any code
change. Tests should add a case that destroys an instance with
non-empty `.niwa/tasks/` and a live daemon that ignores SIGTERM
(AC-P11's scenario with `NIWA_DESTROY_GRACE_SECONDS=1`), verifying
the full tree is gone within ~2 seconds.

**Re-apply idempotency** (AC-P10). On a second apply with one queued
task at `.niwa/roles/<role>/inbox/<id>.json` and one in-progress task
at `.niwa/roles/<role>/inbox/in-progress/<id>.json` plus its
`state.json` at `.niwa/tasks/<id>/state.json`:

- The MkdirAll calls are no-ops on existing directories.
- The envelope and state files are never touched by the installer
  (they're not in ManagedFiles and they're not written at apply time).
- `sessions.json` is only-if-absent, so the registered coordinators
  survive byte-identical.
- `.mcp.json` is rewritten byte-identically because the content is a
  pure function of `instanceRoot`, which is unchanged.
- Hook scripts are rewritten byte-identically for the same reason.
- The `## Channels` section body is detected as already-new (the new
  body contains the R10 tool list; the old body contained the
  v1 tool list — after the first apply under new niwa, the body
  is the new body; subsequent applies find it already-new and
  skip). Detection strategy: if the body between `## Channels` and
  the next top-level heading contains the new R10 tool-list
  signature (e.g., the literal `niwa_delegate` token), skip;
  otherwise, replace. This gives us the required "detect old body
  and replace it; skip when already new" semantics in a single
  check.

**Rationale**

- Decomposes the question's six sub-parts into concrete mechanisms
  that the implementer can code from, each tied to an existing
  niwa pattern (ManagedFiles accumulation, MkdirAll-based directory
  idempotency, only-if-absent for state files, always-overwrite for
  pure-function content, DisclosedNotices for one-shot warnings,
  RemoveAll for destroy).
- Preserves AC-P10 byte-identical re-apply by confining mutations
  to installer-owned files and leaving runtime artifacts alone.
- Respects the "no broken instance after upgrading niwa"
  constraint: an upgrade apply succeeds on the first invocation,
  emits one warning, and leaves a working new-layout instance.
- Avoids the semantic trap of preserving old-schema envelopes that
  the new daemon cannot correctly process. The warning is honest
  about what happened.
- Keeps the migration code small and deletable. The
  `migrateLegacyMeshLayout` helper is a self-contained pure
  filesystem function; when we're past the upgrade window (niwa
  1.0, say), it can be deleted without touching the installer.
- Matches the pre-1.0 posture: don't pay migration-maintenance
  costs for a capability whose user base is small.
- Uses the ManagedFiles discipline R2 mandates for installer-
  written files, while keeping runtime artifacts (envelopes,
  per-task state) out of ManagedFiles — the architecturally
  correct boundary between "what niwa apply owns" and "what niwa
  mcp-serve / niwa mesh watch own."

**Alternatives Considered**

- **A: Detect-and-migrate (in-place transformation)**. Walks old
  `<uuid>/inbox/*.json` files and moves them into
  `<roles>/<role>/inbox/`. Rejected because (1) old envelope
  schema is not guaranteed compatible with the new task-first
  model — preserved envelopes may be un-processable, which is a
  worse outcome than discarded envelopes a user knows are gone;
  (2) ~100-200 LOC of migration code with a test matrix that
  becomes dead weight after every user upgrades; (3) payoff is
  low given pre-1.0 user base and opt-in flag.
- **B: Destroy-and-recreate (refuse-with-instructions)**. Installer
  detects old layout and returns an error telling the user to run
  `niwa destroy && niwa apply`. Rejected because (1) violates the
  "no user ends up with a broken instance after upgrading niwa"
  constraint on a strict reading — the instance is non-functional
  until the user manually reacts; (2) the UX of "your tool
  suddenly refuses to run" is worse than a one-shot warning for a
  pre-release capability; (3) only a tiny maintenance cost saving
  vs Alternative D (~20 LOC vs ~40-60 LOC).
- **C: Blind-rewrite and garbage-collect via ManagedFiles**. Write
  the new layout and let `cleanRemovedFiles` handle obsolete
  paths. Rejected because the old `<uuid>/` directories are NOT
  in ManagedFiles (the current installer never added them; the
  session-register hook created them at runtime), so
  `cleanRemovedFiles` doesn't touch them. The leftover filesystem
  litter is functionally harmless today but a debugging/forensics
  footgun and a potential source of future-bug confusion.

**Consequences**

What becomes easier:

- The installer rewrite lands as one pure-function
  `InstallChannelInfrastructure` plus one self-contained
  `migrateLegacyMeshLayout` helper. No pipeline-step reordering.
  No changes to `cleanRemovedFiles`, `runDestroy`, or
  `TerminateDaemon`.
- AC-P5, AC-P10, AC-P11, AC-P14 map to direct test cases against
  the installer plus a minimal set of destroy tests.
- Future removal of the migration shim is a single-file delete.

What becomes harder:

- Users with in-flight mesh work on the old layout must re-queue
  their tasks after the upgrade. The stderr warning prints the
  envelope count but does not print content; anyone who needs the
  payload has to read the previous state's envelope JSON from git
  history (workspaces are often git-tracked per the wip/
  discipline in CLAUDE.md, though `.niwa/` state is per-instance
  and commonly uncommitted).
- The one-shot warning via DisclosedNotices ties the "migrated
  successfully" signal to workspace-root state. If a user runs
  `niwa reset` (which destroys and re-provisions) before the
  notice is recorded, they might see the warning a second time on
  a subsequent apply against a fresh instance — harmless but
  possibly surprising. An alternative is to skip the notice
  tracking and always emit the warning when old UUID dirs are
  present (since the cleanup removes them immediately, the
  warning is naturally one-shot per instance). The
  DisclosedNotices machinery is cheap and matches the pattern
  used elsewhere, so the decision retains it.
- The `## Channels` section body detection relies on a token
  signature (presence of `niwa_delegate` in the body). If a user
  hand-edits the body to remove that token, the next apply will
  replace the body. Drift detection on `workspace-context.md`
  via `InstallWorkspaceContext`'s ManagedFiles entry warns about
  hand-edits separately, so the behavior is "drift warning plus
  body replacement," which is the existing contract for managed
  context content.
- Role enumeration inside this installer makes the installer
  aware of the full repo topology. If Decision 5 or R5's
  enumeration helper moves to a separate file, this installer
  imports it; if not, the enumeration lives here. Either way, the
  installer signature grows to accept the classified repo list
  (it currently accepts only `cfg` and `instanceRoot`) — a small
  API change at the step-4.75 call site. Apply.go passes
  `classified` to the installer (variable already in scope at
  step 4.75).

<!-- decision:end -->
