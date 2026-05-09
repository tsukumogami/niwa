# Security Review: niwa-mesh-reliability

## Threat Model Baseline

niwa is single-tenant per workspace: one operator owns the workspace
directory, the daemon runs under that operator's UID, and every
process spawned by the daemon (`claude -p` worker, MCP server) inherits
the same UID and HOME. There is no privilege boundary between the
operator and the agents the operator launches; "tenant" here means
"workspace" not "user". The honest threat model is:

1. **A peer/role process is partially compromised** — a worker MCP
   client behaves badly (hallucinates, mis-routes calls, loops a tool)
   and we want process-isolation invariants to hold. Authorization
   checks already enforce delegator/executor identity via
   `authorizeTaskCall` (`internal/mcp/auth.go:92`) using
   `NIWA_INSTANCE_ROOT` + `NIWA_SESSION_ROLE` + `NIWA_TASK_ID` from the
   env block niwa controls.
2. **Filesystem state surfaces injected from outside the daemon** —
   prompt injection, hostile content in a fetched repo, manual
   tampering of `.niwa/` between daemon restarts. The dangling
   classifier already lives here.
3. **A compromised dependency repository or marketplace alias** —
   shared with the existing niwa apply / Claude Code plugin trust
   model; this design touches that surface but does not expand it.

A determined operator can already do anything the daemon can do (they
own both processes). So the review focuses on: do the new code paths
preserve the existing authorization invariants when crossing the new
seams (worker→main-instance redirect; daemon→state.json write;
delegate→manifest peek)?

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes (worker spawn) and No (other decisions).

**Decision 1 (worker discovery channel).** Decisions make the
`--settings <workspaceRoot>/.claude/settings.json` path explicit on the
`claude -p` argv, with `CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` as
fallback. The settings file is authored by `InstallWorkspaceRootSettings`
(`internal/workspace/workspace_context.go:141-264`) from the workspace
config (`niwa.toml` + repo `niwa.json` + global config). It is a
file the same operator already controls; no new external input is
trusted here.

The "could a hostile workspace plant a malicious settings.json" question
has a clear answer: yes, if you give an attacker write access to
`<workspaceRoot>/.claude/`, they can make the worker load any plugin
they want — but they could already do worse with that access (modify
`<workspaceRoot>/.mcp.json`, edit hook scripts under
`<workspaceRoot>/.claude/hooks/`, replace the `niwa` binary on
`$PATH`). The new flag does not lower the bar; it makes the trust
relationship that already existed via filesystem-walk-up explicit.

The fallback `CLAUDE_CONFIG_DIR` env var has the same trust property —
it points at the same workspace directory the operator owns. No
external network artifact is fetched as part of worker spawn.

**Severity: None added.** The `--settings` flag formalizes an existing
trust relationship; a hostile `<workspaceRoot>/.claude/settings.json`
was already trusted under filesystem-walk-up discovery and is still
trusted under explicit injection.

**Decision 3 (`required_skills` manifest read).** The handler peeks at
`body.required_skills` (a `string[]`) and intersects it with a manifest
read from "the workspace's `.claude/`". The manifest source path is
`<mainInstanceRoot>/.claude/` (or `<workspaceRoot>/.claude/` for
non-session callers) — same trust boundary as Decision 1. The peek uses
`json.Unmarshal` into a typed struct with a single string-array field;
malformed input yields an empty slice. There is no execution, no
shell-out, no eval. The `available` set returned in the
`MISSING_SKILLS` error body is read from the same workspace-owned
manifest; no caller can poison it from the wire.

**Severity: None.** Type-safe peek of a workspace-owned file.

**Decision 2 (state.json stub recreation).** When the daemon
encounters a `task.delegate` envelope whose state.json is missing, it
synthesizes a minimal state.json marked `abandoned`,
`reason="taskstore_lost"`. The body of the synthesized stub is
generated from constants and the envelope's own ID — no external input
is interpolated. The envelope file itself is the only external input;
it was originally written by `createTaskEnvelope` under the same
operator's UID. The classifier already trusts envelope contents enough
to read the `type` field and ID, so synthesizing a state stub that
simply marks the task abandoned does not expand the trust footprint.

**Severity: None.**

### Permission Scope

**Applies:** Yes — the design changes how role identity resolves in
the MCP server.

**`roleRoot` redirect (Decision 4).** The helper returns
`s.mainInstanceRoot` only when `role == "coordinator" && s.mainInstanceRoot != ""`
and `s.instanceRoot` otherwise. Three call sites switch to it:
`isKnownRole`, `sendMessageWithID`'s inbox path, and `handleAsk`'s
`askRoot` selection. The redirect is one-way and constant-target: it
never resolves a worker-supplied string to a main-instance path. A
worker calling `niwa_send_message(to="any-other-role")` still computes
`inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", "any-other-role", "inbox")`
under the worktree, which is a directory the worker's own daemon owns.
There is no way to use the `coordinator` carve-out to read or write
into the main instance's roles dir for a non-coordinator role: the
literal string `"coordinator"` gates the redirect.

The complementary risk — that a worker writes a `task.delegate` into
the main instance's coordinator inbox and triggers an ephemeral
coordinator spawn there — is the exact failure PR #93 closed. The
daemon's `daemonOwnsInboxFile` guard
(`internal/cli/mesh_watch.go:746-758`) only claims envelopes whose
`type` is `task.delegate` and whose `to.role` matches a watched role.
Decision 4 routes only `task.ask` notifications and peer messages
(`task.send_message`) to the coordinator inbox, never `task.delegate`,
so the spawn-fabrication path stays closed.

The redirect also does not bypass `authorizeTaskCall`. That check
keys off `state.json` fields (envelope.from.role, state.worker.role)
which are written under the original task's directory regardless of
which inbox the message landed in. A worker that sends a message
labeled `from.role: coordinator` cannot impersonate the coordinator
because `authorizeTaskCall` reads role from the server's
`identity()` (which is set from the env block niwa controls), not
from the message body.

**Severity: None added.** Coordinator-only redirect; literal-string
gate; orthogonal to the lifecycle authorization that prevents
impersonation.

**`niwa_redelegate` authorization (Decision 5 / #114).** `kindDelegator`
auth reuses `authorizeTaskCall(s.identity(), source_task_id, kindDelegator)`
which checks `id.Role == env.From.Role` (`auth.go:153-155`). Only the
role that originally delegated the task can re-issue it.

The "stale credentials in body" question is real but is a property of
the body convention, not the redelegate primitive. The body is
opaque, written by the original delegator, and now re-emitted by the
*current* delegator (whose role matches). If the original body
contained a short-lived token, the re-issued task carries it forward.
This is true today for any delegator that copy-pastes a prior body
into a new `niwa_delegate` call. The redelegate handler does not
upgrade access — it cannot add credentials a fresh delegate could not
add — but it does mechanize body propagation across a longer time
window (source task could be days old).

**Mitigation surface.** The handler should support `body_overrides`
that the caller passes explicitly, and it should NOT auto-merge the
source body if `body_overrides` is `null` and the source is older
than some configurable wall clock — that complicates the API for
limited gain. A simpler and more honest contract is:

- Document in the niwa-mesh skill that `niwa_redelegate` propagates
  the source body verbatim unless `body_overrides` rewrites the
  affected fields. Callers responsible for credential hygiene must
  pass `body_overrides: {…}` to refresh time-sensitive material.
- Note that `redelegated_from` makes the audit chain visible, so an
  operator reviewing post-incident can trace which redelegations
  carried which body forward.

The `redelegated_from` field itself does not leak data the new
delegator could not already see: the new delegator's role matches the
original's (kindDelegator gate), so they are the same actor. There is
no "different person sees prior delegators' identities" failure mode,
because by construction the new delegator IS the prior delegator.

**Severity: Low.** Body propagation is a contract clarity issue, not a
privilege escalation. Documentation mitigates.

**Daemon writing state.json for non-owned tasks (Decision 2).** Today
the MCP server transitions state.json via `UpdateState`
(`taskstore.go`) with flock discipline, and the daemon writes only
`envelope.json`-adjacent metadata (e.g., worker.pid backfill at
`mesh_watch.go` post-spawn). Decision 2 introduces a new daemon write
path that authors state.json transitions for the dangling case
(`queued -> abandoned`, or stub creation for taskstore_lost).

This is not a privilege escalation per se — the daemon already runs
under the same UID as the MCP server and has filesystem write
permission to the entire `.niwa/` tree. But it does expand the
write surface across process boundaries. Two concrete risks:

1. **Lock-ordering between daemon and MCP server.** Both processes
   now write to the same state.json file. The `UpdateState` helper
   uses flock for exclusive access; the daemon path must use the same
   helper (or a shared lock primitive) to avoid TOCTOU between a
   running tool call and the daemon's transition. The decision report
   acknowledges this ("New write-path needs flock discipline … the
   existing `taskstore.go` write helpers already provide it").
2. **Error-code confusion.** A delegator running `niwa_update_task`
   on a queued task that gets transitioned to `abandoned` mid-call
   will see `TASK_ALREADY_TERMINAL` instead of the previous
   `too_late`/`queued` contradiction. This is the *fix*, not a bug,
   but it changes observable error codes for callers; documentation
   should call this out.

**Severity: Low.** Lock discipline is enforceable via the existing
`UpdateState` helper; the design report names this requirement. Test
coverage in `mesh_watch_test.go::TestHandleInboxEvent_DanglingEnvelope`
must extend to cover concurrent MCP-server tool calls.

### Supply Chain or Dependency Trust

**Applies:** Yes — Decision 1 touches plugin alias resolution.

The workspace `enabledPlugins` references aliases like `shirabe@shirabe`.
When the worker's `claude -p` is launched with
`--settings <workspaceRoot>/.claude/settings.json`, Claude Code
resolves these aliases against marketplaces declared in
`extraKnownMarketplaces` plus user-level `~/.claude.json`. The trust
chain is:

1. **Workspace marketplace declaration.** The workspace settings
   file's `extraKnownMarketplaces` block names each marketplace by
   GitHub `org/repo` (or `directory` source for `repo:` references —
   `internal/workspace/workspace_context.go:300-330`). Operators
   audit this list when running `niwa apply`.
2. **User-level fallback.** If a workspace doesn't redeclare a
   marketplace its plugins reference, the worker falls back to the
   user's `~/.claude.json` plugin store. An operator who runs niwa in
   a hostile workspace inherits whatever marketplaces their user-level
   config has registered.
3. **GitHub source resolution.** Claude Code resolves
   `org/repo` marketplaces by cloning that repo. The same trust
   boundaries apply as anywhere `git clone <github-url>` runs in CI.

Could plugin alias resolution silently load an attacker-controlled
plugin? Two paths:

- **A workspace declares a marketplace with the same name as a
  user-level marketplace.** The decision report's lead 1 §3 hypothesis
  is that aliases drop silently when neither resolves. A more
  concerning case is when both resolve and one shadows the other.
  Today the workspace path takes precedence because Claude Code reads
  workspace settings on top of user settings. Decision 1 changes
  *delivery*, not precedence — the same `enabledPlugins` is now
  guaranteed to reach the worker, not silently dropped. So if there
  was a "hostile-workspace plants attacker marketplace alias" attack
  surface, it existed pre-design via the operator's running `niwa
  apply` on the workspace, and the design does not expand it.
- **A workspace references a marketplace alias that doesn't exist
  yet — and an attacker registers a same-named marketplace later.**
  This is a name-squatting risk for any marketplace ecosystem; not
  niwa-specific.

The design does not introduce its own marketplace registry, registry
mirror, or alias resolver. It pipes existing settings to the worker.

**Severity: None added.** Same trust model as `niwa apply`.

### Data Exposure

**Applies:** Yes (minor leakage in three places).

**`niwa_list_sessions` daemon sub-object (#111).** The new
`{alive, pid, started_at}` block exposes a daemon's PID and start
time. Recipients of this info are MCP callers under the same
`<instanceRoot>/.niwa/sessions/`, which is a directory only the
operator can read (mode 0700 per the workspace conventions). PIDs are
already visible to the same UID via `/proc/<pid>/`; `started_at` is
already readable from `/proc/<pid>/stat`. The new field is a
convenience; it does not expose data a same-UID process couldn't get.

The "leak from one workspace tenant to another" concern: niwa is
single-tenant per workspace by design, and the MCP server only reads
the `sessions/` directory under its own `instanceRoot`. There is no
cross-workspace API surface. **Severity: None.**

**`MISSING_SKILLS` `available` set (#113).** The error body returns
the list of skills the workspace currently has installed. Anyone
authorized to call `niwa_delegate` is by construction a niwa MCP
caller running under the operator's UID, with read access to the
manifest already. The error response repeats information the caller
could read from `<workspaceRoot>/.claude/` directly. No new
information is exposed.

A subtler concern: a misbehaving worker could learn about skills
installed in *the main instance* by sending a delegate that's
guaranteed to fail the gate. This is a real signal — workers don't
ordinarily enumerate the workspace plugin set — but the same worker
already sees the workspace settings via Decision 1's `--settings`
flag. The `MISSING_SKILLS` response leaks nothing the worker process
isn't already configured with.

**Severity: None added.** Information already accessible via the
worker's own settings file.

**`redelegated_from` chain.** As established under Permission Scope:
the `kindDelegator` gate ensures only the original delegator (by role
equality) can redelegate. The chain reveals task IDs of prior
redelegations to the operator inspecting the audit log; it does not
reveal any new identity beyond the role string. **Severity: None.**

**Audit-log fidelity for `required_skills` (#113).** Decision 3 places
`required_skills` inside `body`, which means `extractArgKeys`
(`audit.go:114-133`) does not surface it as a discrete `arg_keys`
entry. The trade-off is documented in the decision report. Operators
who want to grep "calls that asserted a skill requirement" lose that
visibility; they can recover it indirectly by grepping
`error_code=MISSING_SKILLS` (failed gates) but successful gates remain
invisible at the audit-log level.

**Severity: Low (observability gap, not a leak).** Mitigations: emit
a structured success-side audit field, or log to a separate
`mcp-skill-gate.log` if the gate signal becomes operationally
important.

## Summary

The design preserves niwa's existing authorization invariants while
formalizing several previously-implicit trust relationships. The four
primary decisions cluster into two security-relevant patterns:

1. **Explicit injection over filesystem walk-up (Decisions 1, 4).**
   Replaces fragile filesystem discovery with `--settings`/`--mcp-config`
   argv flags and the `roleRoot` redirect helper. Both make existing
   trust relationships visible without expanding what's trusted.
2. **Lifecycle truthfulness (Decisions 2, 5).** The daemon now
   transitions state.json for dangling tasks; `niwa_redelegate` provides
   an authorized recovery path. Lock discipline (`UpdateState` helper)
   keeps writes coherent across the daemon and MCP server.

Three concerns surfaced, all low-severity:

- **`niwa_redelegate` propagates body verbatim.** Time-sensitive
  credentials in the source body travel forward across redelegations.
  Mitigation: documentation in the niwa-mesh skill text. The
  `redelegated_from` audit chain provides post-hoc forensics.
- **Daemon-driven state.json writes need flock.** The decision report
  names the `UpdateState` helper as the discipline; tests in
  `mesh_watch_test.go` must extend to cover concurrent MCP tool calls
  on dangling-classified tasks.
- **`required_skills` is invisible in audit logs (success path).**
  Body-peek convention loses `arg_keys` discoverability for assertions
  that pass. Mitigation: emit a structured success-side audit field
  if the signal becomes operationally important. Documented in the
  decision report's Cons section.

No high or medium severity findings. No supply-chain expansion. No
cross-tenant leakage (niwa is single-tenant per workspace by design).
The plugin trust chain in Decision 1 is the same one `niwa apply`
already establishes; the design pipes existing settings to a new
process rather than introducing a new registry or resolver.

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design should add a `Security Considerations` section that
captures the three concerns above:

---

### Security Considerations

**Trust model.** niwa is single-tenant per workspace. The daemon, MCP
server, and worker processes all run under the operator's UID. The
boundary the design protects is *between roles within a workspace*
(`authorizeTaskCall`'s delegator/executor checks), not between
workspaces or between the operator and external attackers.

**Plugin and skill discovery.** The `--settings <workspaceRoot>/.claude/settings.json`
flag points the worker at the same settings file `niwa apply` already
authors. The trust relationship that existed implicitly via Claude
Code's filesystem-walk-up discovery is now explicit. A hostile
`<workspaceRoot>/.claude/settings.json` was already trusted under
walk-up; the design does not expand the trust footprint. Plugin alias
resolution (e.g., `shirabe@shirabe`) follows the standard Claude Code
trust chain: workspace `extraKnownMarketplaces` first, user-level
`~/.claude.json` as fallback.

**Coordinator role redirect.** `roleRoot(role)` returns
`mainInstanceRoot` only for the literal string `"coordinator"`; all
other roles route to the worktree's local roles dir. The redirect
cannot be used to write into the main instance's roles dir for any
other role. `task.delegate` envelopes are not routed through the
redirect — only `task.ask` notifications and `task.send_message`
peer messages — so PR #93's "ephemeral coordinator spawn" closure is
preserved.

**Daemon-driven state transitions.** When a `task.delegate` envelope's
`state.json` is missing, the daemon transitions state to `abandoned`
with `reason="taskstore_lost"`. Writes use the existing `UpdateState`
flock helper, so concurrent MCP tool calls on the same task observe
the transition atomically. Operators recover via `niwa_redelegate`,
which produces a new task with `redelegated_from` for audit chain
visibility.

**Redelegate body propagation.** `niwa_redelegate` is gated by
`kindDelegator` authorization (only the original delegator's role can
re-issue). The source `body` propagates verbatim unless the caller
passes `body_overrides`. **If the source body contains time-sensitive
material (short-lived tokens, expiring URLs), the caller must pass
`body_overrides` to refresh those fields.** The `redelegated_from`
envelope field provides post-hoc audit chain visibility for forensics.

**Audit log observability.** `required_skills` lives inside the
opaque `body`, so successful skill-gate assertions do not appear as
discrete `arg_keys` entries in `mcp-audit.log`. Failed assertions
(`error_code=MISSING_SKILLS`) remain observable. If fleet-level
"are coordinators using the gate?" telemetry becomes operationally
important, emit a structured success-side audit field as a follow-up.

---

The design's security posture is sound for an internal-mesh feature
that does not change the workspace's external trust boundary. The
three documented considerations are clarity items, not blockers: each
has a clear mitigation that is either already specified in the
decision reports or reduces to "document the contract in the
niwa-mesh skill text."
