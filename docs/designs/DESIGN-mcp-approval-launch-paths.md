---
schema: design/v1
status: Accepted
upstream: docs/prds/PRD-mcp-approval-launch-paths.md
problem: |
  A `niwa dispatch` worker stops on Claude Code's per-server approval dialog
  even though the instance root's settings approve the declared MCP servers,
  and an ephemeral session started at the workspace root never loads them. The
  launch's own `--settings` document is the one place a dispatched worker was
  observed to honor the approval, and the SessionStart hook is the one place
  niwa can speak to an ephemeral session.
decision: |
  Record the instance root's approved server names in instance state, beside
  the permission posture, recomputed on every create and apply from the
  effective configuration. `niwa dispatch` reads that record and adds the
  names, filtered through a fixed name pattern, to the launch settings
  document as a typed value that can only marshal to a string array. The
  SessionStart hook reads the same record for the instance it just provisioned
  and adds one paragraph to its injected context naming the servers the
  session won't have and the two ways to get them.
rationale: |
  Dispatch already reads the permission posture from the state niwa just saved
  rather than from a generated settings file, so a hand-edited file can't
  change what the launch carries; the approval list follows that rule, and
  state holds the overlay-merged decision, which the raw config dispatch loads
  does not. A typed value plus a name pattern keeps the second exception to the
  launch document's constants-only rule as narrow as the first. The hook
  paragraph costs one read of a file the hook's own provisioning just wrote.
---

# DESIGN: MCP server approval on every launch path

## Status

Accepted

## Context and Problem Statement

The PRD (`docs/prds/PRD-mcp-approval-launch-paths.md`) requires that a
dispatched worker start with the workspace's declared MCP servers and no
approval dialog under every permission mode (R1-R4), that nothing niwa writes
today changes (R5), that an ephemeral session say which servers it lacks (R6),
and that Codex and `niwa watch` launches stay as they are (R8).

Four pieces of existing code shape the answer.

**The approval decision lives in the instance pipeline.**
`instanceRootMCPApprovals` (`internal/workspace/payload.go`) asks each agent's
producer which servers it approves at the instance root, from the effective
configuration: the workspace config with the overlay merged and secrets
resolved. That merge happens only inside `Applier.runPipeline`. The settings
materializer writes the result as `enabledMcpjsonServers` in the instance
root's `.claude/settings.json`.

**Dispatch loads the raw configuration.** `runDispatch` loads
`.niwa/workspace.toml` before provisioning, but not the overlay; a server
declared only in the overlay is invisible to it. It does already read one value
the pipeline decided: the permission posture, from `InstanceState.ClaudePermissions`,
which the pipeline recomputes on every Create and Apply and which only
dispatch reads. The comment there explains why: a generated settings document
can be hand-edited, and that must not change what the launch carries.

**The launch settings document has a constants-only rule.**
`renderLaunchSettings` (`internal/cli/dispatch_settings.go`) renders one map
that step (9c) of `runDispatch` builds from contributors (remote control,
session-message acceptance, lineage). Because the document outranks the
instance's settings files, contributors may add only constant keys and values.
The one named exception is the lineage `env` value, a type that can only
marshal to `{"OTEL_RESOURCE_ATTRIBUTES": "<value>"}`, built from pieces each
checked against a fixed pattern.

**The SessionStart hook only injects text.** `niwa instance from-hook`
(`internal/cli/instance_from_hook.go`) provisions an instance through the same
`Applier.Create` and returns `hookSpecificOutput.additionalContext` built by
`buildSessionStartInjection`. Claude Code reads MCP configuration once at
startup, from the session's project, which for an ephemeral session is the
workspace root, so nothing the hook writes reaches the session's MCP set.

## Decision Drivers

- A dispatched worker must not stall on the dialog under any permission mode.
- Nothing a person or tool edits after niwa writes it may widen what the launch
  approves (PRD R2).
- The launch document's precedence over the instance's settings must not become
  a route for workspace text to add settings keys (PRD R3).
- Overlay-declared servers must be approved like base ones.
- The paths that work today must produce byte-identical files (PRD R5).
- Follow the codebase's existing patterns over new mechanisms.

## Considered Options

### Decision 1: Where the dispatch launch gets the approved names

**Chosen: a new `InstanceState.ApprovedMCPServers` field.** The pipeline sets it
on every Create and Apply from `instanceRootMCPApprovals(effectiveCfg)`,
normalized by `approvedMCPServerList`, the same value the settings materializer
writes. Dispatch reads it from the state of the instance it just provisioned,
exactly as it reads `ClaudePermissions`.

Alternatives:

- **Read `enabledMcpjsonServers` back from the instance root's
  `settings.json`.** Simplest, and the generated file always holds the right
  list on a fresh instance. Rejected because it reads a file a person or tool
  can edit, which PRD R2 forbids and which the posture comment already argues
  against, and because a future change to the settings document's shape would
  silently change the launch.
- **Recompute from the configuration dispatch loads.** Rejected: dispatch holds
  the raw base config, so overlay-declared servers would be missing, and
  reproducing the overlay merge in dispatch would duplicate the pipeline.

### Decision 2: How the names enter the launch document

**Chosen: a typed contributor, documented as the rule's second exception.** A
new value type, `mcpApprovalList`, holds the filtered names and marshals only
to a JSON array of strings. Step (9c) adds it under
`config.EnabledMcpjsonServersKey` when the Claude launch spec has a settings
flag and the filtered list is non-empty. Each name must match
`^[A-Za-z0-9_-]{1,64}$`, and at most 64 names are sent; a name that doesn't
match is left out and dispatch prints one warning line naming it in Go's `%q`
form, truncated to 80 bytes, so control characters and escape sequences reach
the terminal escaped. The `renderLaunchSettings` comment names this as the
second exception, beside lineage.

The contributor is not gated on `[claude] trust_instances`. A dispatched
worker's project is the instance root, so the only `.mcp.json` it reads is the
one niwa generated there; repositories' own `.mcp.json` files are not part of
its MCP set, so a repository can't define a server under an approved name.
Gating on trust would leave a workspace that didn't opt into trust with a
stalled worker, which is the failure the PRD exists to remove (PRD acceptance
criterion on `trust_instances` off).

Alternatives:

- **Pass the names unfiltered.** They come from niwa's own decision, so they
  can't add keys once marshaled as strings. Rejected because the rule's comment
  promises every non-constant value is checked against a fixed alphabet, and a
  name with quotes or control characters has no legitimate use in Claude Code's
  server-name space.
- **Reject the whole dispatch on a bad name.** Rejected (PRD Decisions): it
  would break workspaces that dispatch today; dropping keeps the rest of the
  approval.
- **Pass `--mcp-config` with the instance's `.mcp.json`.** Would bypass project
  approval entirely, but defines every server twice (flag and project file),
  with Claude Code's behavior on the collision unknown, and the observed fix
  already works. Rejected.

### Decision 3: What the SessionStart hook says

**Chosen: one paragraph built from the new state field.** After provisioning,
the hook reads `ApprovedMCPServers` from the new instance's state. When it is
non-empty, `buildSessionStartInjection` appends a paragraph listing the names
that match the same pattern, a count of any others, the reason (the session's
project is the workspace root), and the two workarounds. An empty list adds
nothing, so the injection is byte-identical for a workspace with no servers.

Alternatives:

- **Materialize the servers at the workspace root.** Settled against in the PRD
  (root credential file, every root session affected, root trust niwa won't
  grant).
- **Read the instance's `.mcp.json` for the names.** Rejected: it could name
  servers niwa didn't generate (a verbatim file), and it would print names
  from a file the hook didn't decide on.

## Decision Outcome

The pipeline records its approval decision in instance state once; the two
consumers that can't rely on the project settings (dispatch and the hook) read
that record. Dispatch turns it into a typed, filtered launch setting that
Claude Code honors whatever the permission mode; the hook turns it into a
notice. The instance's own files are untouched, so foreground and hand-started
background sessions keep working through the existing approval list and trust.

## Solution Architecture

```
create/apply pipeline
  effectiveCfg ──instanceRootMCPApprovals──► names
     │                                         ├─► settings.json enabledMcpjsonServers   (unchanged)
     │                                         └─► InstanceState.ApprovedMCPServers      (new)
     ▼
niwa dispatch (9c)                         niwa instance from-hook (SessionStart)
  state.ApprovedMCPServers                   state.ApprovedMCPServers
  └─ filterMCPServerNames (pattern)          └─ ephemeralMCPNotice (pattern, count)
     ├─ warn per dropped name                   └─ appended to additionalContext
     └─ launchSettings[enabledMcpjsonServers] = mcpApprovalList
        └─ renderLaunchSettings → --settings
```

Components:

- **`internal/workspace/state.go`**: `ApprovedMCPServers []string
  json:"approved_mcp_servers,omitempty"`, documented like `ClaudePermissions`:
  recomputed every Create/Apply, never carried over, read only by dispatch and
  the hook, and trusted only for an instance the reading process just
  provisioned.
- **`internal/workspace/apply.go`**: the pipeline computes the list into a
  local before building its result, from
  `approvedMCPServerList(instanceRootMCPApprovals(effectiveCfg))` (the same
  sorted, de-duplicated value the settings materializer writes). It records
  the list only when the Claude producer's instance-root payload was written,
  not exempt: if a file niwa didn't generate occupies the instance root's
  `.mcp.json`, the names would describe servers niwa didn't write, so nothing
  is recorded. Review instances (`isReviewInstance`) record nothing. Create
  and Apply copy it into the saved state. Because the instance was provisioned
  by the same process moments earlier, reading it at dispatch is the PRD R2
  computation, not a stale value.
- **`internal/cli/dispatch_settings.go`**: `mcpServerNamePattern`,
  `filterMCPServerNames(names) (kept, dropped []string)`, and the
  `mcpApprovalList` type with a `MarshalJSON` that emits only a string array.
  The `renderLaunchSettings` comment names the second exception.
- **`internal/cli/dispatch.go`**: step (9c) gains a contributor after lineage:
  gated on `spec.Flags.Settings != ""`, reads the provisioned instance's state
  (the same `state` read used for the posture), filters, warns, adds the key
  when the kept list is non-empty. When the state can't be read and the launch
  has a settings flag, it prints a warning naming the state file, since the
  worker will then meet the dialog; the existing posture warning only fires
  when no permission mode is set. The passthrough, settings document included,
  is built once before the attached/detached fork, so both modes carry the
  same list.
- **`internal/cli/instance_from_hook.go`**: after provisioning, load the
  instance state, and pass the names to `buildSessionStartInjection`, which
  appends `ephemeralMCPNotice(names)` when non-empty. The notice lists the
  Claude-approved servers (the recorded list), which is what a Claude session
  would have loaded; a server restricted to another agent isn't one the
  session would get anywhere, so it isn't named. Names render in backticks;
  the rest of the paragraph is fixed text.
- **`niwa watch`**: review launches build their own passthrough
  (`watchReviewLaunch`, `watchResumeLaunch`) and never run step (9c), and
  review instances record no list; a unit test pins that a review launch
  carries no `enabledMcpjsonServers`.
- **`docs/guides/instance-trust.md`**: the coverage section lists the four
  launch paths, and records the manual check and Claude Code version.

## Implementation Approach

1. State field and pipeline: add the field, set it in the pipeline result,
   copy it in Create and Apply. Unit tests: a workspace declaring base and
   overlay servers records both, sorted; no servers records nothing; the
   instance's `settings.json` and `.mcp.json` are unchanged.
2. Dispatch contributor: the pattern, filter, typed value and the (9c) block.
   Unit tests through `provisionThroughCreate` and `launchSettingsDocs`: the
   list, no servers, overlay-only server, a bad name dropped with one warning,
   only bad names, every permission mode, coexistence with the other keys,
   Codex unchanged, hand edits to `settings.json` ignored. A functional
   scenario in `dispatch.feature` asserting the launch list with the fake
   `claude`.
3. Hook notice: thread the names into `buildSessionStartInjection`, add
   `ephemeralMCPNotice`. Unit tests: names listed, both workarounds present,
   odd names counted rather than printed, empty list byte-identical.
4. Guide: coverage section and the manual check record.

## Security Considerations

The launch settings document outranks the instance's settings files, so what
enters it decides the worker's configuration. This design adds one key, with
three bounds:

- **Source.** Names come from instance state niwa wrote from its own approval
  decision over `[mcp.servers]`, never from a `.mcp.json`, a distributed file,
  or the generated settings. A person who can edit the instance's state file
  can already edit `.mcp.json` and the settings directly; the state file adds
  no reach.
- **Shape.** `mcpApprovalList` marshals only to a JSON array of strings, so no
  name can introduce a key or change the document's structure; the key itself
  is a constant.
- **Alphabet and size.** Names outside `^[A-Za-z0-9_-]{1,64}$` are dropped and
  at most 64 are sent, on the read side in both consumers, so a hand-edited
  state file can't push a key-like or instruction-like string into either. The
  warning about a dropped name prints it `%q`-escaped and truncated, so its
  raw bytes never reach the terminal.

**Relation to the trust opt-in.** The launch approval is sent whether or not
`[claude] trust_instances` is on, unlike the project-settings list, which
Claude Code reads only in a trusted directory. That doesn't widen what a
worker loads: its project is the instance root, so its MCP set is the
instance root's `.mcp.json`, which niwa generated (the list isn't recorded
when that file was exempt), and repositories' `.mcp.json` files aren't read.
What changes for a workspace without trust is that its dispatched worker
starts the servers niwa generated for it instead of stalling, which is the
intended outcome.

The approval names only servers niwa wrote into the instance root's
`.mcp.json`, never `enableAllProjectMcpServers`. `niwa watch` review launches,
which run in deliberately untrusted instances, never reach the contributor
and their instances record no list. The ephemeral notice discloses only
server names the workspace config declares, to a session in that workspace;
the workspace author already controls the instance `CLAUDE.md` the same hook
injects, so the names add no reach over the agent's context.

## Consequences

Positive:

- Dispatched workers stop stalling on the dialog on every host, independent of
  permission mode.
- Ephemeral sessions report a missing capability at start instead of mid-task.
- The approval list has one source of truth, read by every consumer.

Negative:

- The fix relies on undocumented Claude Code behavior (honoring
  `enabledMcpjsonServers` from `--settings`). Mitigation: the guide's recorded
  manual check names the version and is rerun on upgrade.
- A second exception to the constants-only rule. Mitigation: it is typed,
  filtered and named in the rule's comment like the first.
- A server with an unusual name loses its dispatch approval. Mitigation: the
  warning names it, escaped.
