---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-trust-instances.md
milestone: "Trust the instances niwa creates"
issue_count: 6
---

# PLAN: Trust the instances niwa creates

## Status

Active

## Scope Summary

Let a workspace overlay declare `[mcp.servers]`, and add an opt-in
`[claude] trust_instances` key that makes niwa mark each instance root trusted
in Claude Code's user config on create and apply, record what it wrote, and
withdraw exactly that on destroy, reap and when the option is turned off. The
review command's trust moves onto the same writer.

## Decomposition Strategy

**Horizontal decomposition.** The design names separable layers with stable
interfaces: overlay config merging, a standalone `claudetrust` writer package,
the instance-state record and teardown, the pipeline step, and the review
command's migration. Each can be built and unit-tested on its own, and the
order is fixed by safety rather than by integration risk: teardown that
removes grants has to exist before anything writes one, so a grant never
outlives its instance. A walking skeleton would put a trust writer in the
pipeline before its removal path existed, which is the state the design rules
out. The live check against Claude Code runs before the PR leaves draft, as
the gate on the design's assumptions.

All six items land in one pull request in this repository: the repo's
delivery preference is consolidated, and no hard constraint, independent
value, or stated preference calls for a split.

## Issue Outlines

### Issue 1: feat(config): let a workspace overlay declare MCP servers

**Complexity**: testable

**Goal**: A workspace overlay can declare `[mcp.servers.<name>]` exactly as the
base config does. Overlay servers whose names the base declares are dropped
with a warning before resolution; the rest resolve against the overlay's own
vault bundle and merge into the base config, so the generated `.mcp.json`,
Codex MCP config and approved-server list include them. `MergeWorkspaceOverlay`
enforces base-wins itself, so the worktree path is covered too.

**Acceptance Criteria**:
- [ ] `config.WorkspaceOverlay` decodes `[mcp.servers.<name>]` with every field the base table has.
- [ ] An overlay-only `http` server with a `vault://` header produces an instance-root `.mcp.json` entry with the resolved value, and the instance root's `.claude/settings.json` lists it in `enabledMcpjsonServers`.
- [ ] With Codex config generated, the overlay server appears in the generated `[mcp_servers.<name>]` table.
- [ ] An overlay server missing both `url` and `command` fails with the same error text a base entry gets.
- [ ] An overlay server whose name the base declares produces a warning naming it; generated files carry the base `url`; apply succeeds even though the dropped entry's `vault://` header names a key the overlay provider lacks.
- [ ] An overlay `vault://` value naming a provider only the base declares fails with the existing provider-not-declared error.
- [ ] `MergeWorkspaceOverlay` called directly (as the worktree path does) keeps the base server on a name collision.
- [ ] Base and overlay servers appear in name order in `.mcp.json`, the approval list and the Codex config.
- [ ] A `@critical` functional scenario covers an overlay-declared server reaching `.mcp.json` and the approval list.

**Dependencies**: None

### Issue 2: feat(claudetrust): add the Claude Code trust writer

**Complexity**: critical

**Goal**: A new `internal/claudetrust` package is the only code that edits
Claude Code's user config. It resolves the config file (`CLAUDE_CONFIG_DIR`,
else `$HOME`; relative values refused), computes the symlink-resolved key,
holds Claude Code's `<config>.lock` mkdir lock (5-second wait, 10-second
staleness, `Lstat`, non-recursive removal, no repeat wait after one timeout in
a process), classifies the file, applies the entry-state table, preserves every
other value through `json.RawMessage`, writes through a 0600 temp file with
fsync and rename, and returns a validated `Grant`. `Withdraw` validates a
record before acting and removes the entry (`Destroy` on `created`) or the
flag (`flag-only`, and every `Retract`).

**Acceptance Criteria**:
- [ ] With no config file, `Ensure` creates it with mode 0600 (and a missing `CLAUDE_CONFIG_DIR` directory with mode 0700) holding only `projects[<key>].hasTrustDialogAccepted = true`, and returns a `created` grant.
- [ ] An empty file is treated as `{}`.
- [ ] An entry with the flag absent or `false` ends with the flag `true` and every other key unchanged, and returns a `flag-only` grant.
- [ ] An entry already `true` and not in `prior` is left byte-identical and returns no grant; one already in `prior` returns `prior`.
- [ ] Invalid JSON, a top-level array, `"projects": []`, a non-object entry, and an unreadable file (mode 0000) each leave the file byte-identical and return a warning naming the file.
- [ ] `CLAUDE_CONFIG_DIR` set targets `$CLAUDE_CONFIG_DIR/.claude.json`; set to `""` targets `$HOME/.claude.json`; set to a relative path refuses with a warning.
- [ ] A symlinked `.claude.json` is written through to its target and the link remains, and the lock taken is `<link path>.lock`, not the target's.
- [ ] An instance root under a symlinked parent, and one that is itself a symlink, are keyed by `filepath.EvalSymlinks` of the root.
- [ ] A file at mode 0644 with other top-level keys, other projects, a large integer and an escaped string keeps all values and mode 0644 after a write and after a withdrawal.
- [ ] A second `Ensure` with nothing to change leaves contents, inode and mtime unchanged.
- [ ] A fresh lock directory held for longer than 5 seconds makes `Ensure` skip within 6 seconds with a warning; a lock directory with an 11-second-old mtime is removed and the write proceeds; a symlink at the lock path is never followed or removed; no lock directory remains after success or after an injected failure between temp write and rename, and the original file is intact in the failure case.
- [ ] Ten goroutines ensuring ten roots against one file all land.
- [ ] `Withdraw` refuses (with a warning, writing nothing) a record with an unknown mode or source, a `config_file` not named `.claude.json`, a directory, or a key that differs from the expected instance key.
- [ ] `Withdraw(Destroy)` deletes a `created` entry; deletes only the flag for `flag-only`; `Withdraw(Retract)` deletes only the flag; both are no-ops when the entry or flag is gone.
- [ ] `hasTrustDialogHooksAccepted` is never written.

**Dependencies**: None

### Issue 3: feat(workspace): record Claude trust grants and withdraw them on destroy

**Complexity**: critical

**Goal**: `InstanceState` gains `ClaudeTrust *claudetrust.Grant`
(`claude_trust`, omitempty, schema version unchanged). `DestroyInstance` and
`DestroyWorkspace` load the instance's state before removing it and withdraw a
recorded grant, validated against the instance's canonical path, reporting any
warning without failing the destroy. Nothing writes a grant yet.

**Acceptance Criteria**:
- [ ] An `instance.json` without `claude_trust` loads and saves unchanged; one with it round-trips.
- [ ] `DestroyInstance` on an instance whose record says `created` (config file in a temp dir) removes the entry; for `flag-only` it removes only the flag.
- [ ] `DestroyWorkspace` withdraws each instance's recorded grant.
- [ ] A record naming another path's key, or a file not named `.claude.json`, leaves every file unchanged and produces a warning; the destroy completes.
- [ ] An unparseable config file at destroy produces a warning naming the file and key, and the destroy completes.
- [ ] An instance with no record leaves an existing entry at its path byte-identical.

**Dependencies**: Issue 2

### Issue 4: feat(apply): trust instance roots when the workspace opts in

**Complexity**: critical

**Goal**: `[claude] trust_instances` (boolean, base or overlay, base wins when
both set) drives `Applier.deliverClaudeTrust`, which runs after the pipeline
succeeds in `Create` and `Apply`, right before `SaveState`, and is skipped when
`DeveloperHome` is empty. On: ensure and record. Off with a workspace grant:
retract and clear the record (kept on a skip). A review grant is carried
forward. A `SaveState` failure rolls back this run's change. A changed config
file with the same key withdraws the old grant first; a changed key (moved
instance) leaves the old entry with a warning. When trust is on only because of
the overlay, a one-time per-workspace notice says so. Reap's and review
cleanup's explicit trust calls are left for Issue 5.

**Acceptance Criteria**:
- [ ] `trust_instances = "yes"` fails config parsing with an error naming the key.
- [ ] With the key true in the base config, `niwa create` exits 0, writes the resolved-root entry, and records file, key, `created` and `workspace`.
- [ ] Key only in the overlay: same result, plus the one-time notice on the first apply in the workspace and not on the next. Base `false` with overlay `true`: no entry. Base `true` with overlay `false`: entry written.
- [ ] Key absent: create and apply leave an existing `.claude.json` byte-identical and create none.
- [ ] `niwa apply` in an instance created before the option was on adds and records the entry; a second apply leaves contents, inode and mtime unchanged.
- [ ] Turning the option off and applying deletes the flag (other keys stay) and clears the record; with the entry already removed by hand, apply exits 0 and clears the record.
- [ ] `CLAUDE_CONFIG_DIR` set at create and unset at a later apply withdraws the flag from the first file, writes `$HOME/.claude.json`, and records the new file.
- [ ] `niwa dispatch` (launch stubbed) and session-start provisioning leave the entry written and recorded when they exit; so do `niwa reset` and `niwa init` creating an instance.
- [ ] An injected `SaveState` failure after a first-time grant leaves no entry in the config file; after a first-time `flag-only` grant it leaves the entry with the flag removed and its other keys intact; with a grant already recorded before the run, it leaves the entry and record as they were.
- [ ] A `Create` and an `Apply` whose pipeline fails at its last step (injected) leave the config file byte-identical and the existing record unchanged: no trust is written before the pipeline has succeeded.
- [ ] An Applier with an empty `DeveloperHome` writes nothing outside the instance and carries an existing record forward unchanged.
- [ ] With a record whose key differs from the instance's current canonical path (instance moved), apply leaves the old entry byte-identical, warns naming that key and file, and records a fresh grant for the new path.
- [ ] The overlay notice: a second instance in the same workspace gets no notice; a different workspace gets one; no notice appears when the base config sets the key.
- [ ] With an unparseable config file (and separately, with the lock held longer than 5 seconds), `niwa create` and `niwa apply` exit 0, warn naming the file, leave it byte-identical, and leave the record empty after create; with the option off, a skipped withdrawal on apply exits 0 and keeps the record.
- [ ] Apply with the option on in an instance holding a `review`-sourced record keeps that record (source stays `review`) and doesn't rewrite the file.
- [ ] For the off switch on a `created` grant, a key Claude Code added to the entry (for example `allowedTools`) is still present after the flag is withdrawn.
- [ ] After create, apply and destroy, `~/.claude/settings.json` is byte-identical, no file under HOME contains `enableAllProjectMcpServers`, and only the instance root's `projects` key was added or changed.
- [ ] `@critical` functional scenarios cover create, apply on an existing instance, destroy, and the off switch.

**Dependencies**: Issue 1, Issue 3

### Issue 5: refactor(watch): move review-instance trust onto claudetrust

**Complexity**: testable

**Goal**: `resolveAskPosture` seeds trust through `claudetrust.Ensure` with
source `review`, saves the grant into the review instance's state (withdrawing
it if the save fails), and reads the config home the writer resolves.
`internal/watch/trust.go` and its tests are deleted (coverage moves to
`claudetrust`), and the explicit trust removals in both reap sweeps and in
review staging cleanup are deleted, since `DestroyInstance` now withdraws the
recorded grant.

**Acceptance Criteria**:
- [ ] A review instance's trust is recorded with source `review`, keyed by the resolved path in the file `CLAUDE_CONFIG_DIR`/`$HOME` names.
- [ ] Reap removes a recorded review grant through destroy, and leaves an unrecorded entry at a reclaimed path byte-identical.
- [ ] A failed review staging destroys the instance and its grant.
- [ ] A skipped trust write (unparseable file) makes `resolveAskPosture` return false (hard-deny).
- [ ] An injected instance-state save failure after seeding leaves no entry in the config file and returns false (hard-deny).
- [ ] `niwa reset`, dispatch's failed-launch cleanup, and both reap sweeps each remove a recorded grant through `DestroyInstance` (table-driven over the routes).
- [ ] An apply with the option off in a review instance leaves its review grant in place.
- [ ] No reference to `internal/watch/trust.go`'s functions remains.

**Dependencies**: Issue 3

### Issue 6: docs: instance trust guide and live check

**Complexity**: testable

**Goal**: Run the live check on a host with Claude Code before the PR leaves
draft, then write `docs/guides/instance-trust.md`: what trust lets Claude Code
do (project permissions, hooks and MCP servers applied without asking), that
overlay content then runs unprompted, which sessions it covers, the four-step
"still seeing prompts" check, an overlay `[mcp.servers.<placeholder>]` example
with a `vault://` header, and the Claude Code version and results of the live
check. Add the guide to the CLAUDE.md guide list, point to it from the overlay
section of `docs/guides/workspace-config-sources.md`, and update the review
design's note that Claude Code doesn't honor a shared lock.

**Acceptance Criteria**:
- [ ] Live check at a fresh instance root with the option on and an overlay server: `claude -p "list your mcp tools"` in default mode exits 0 and names the server's tools; `claude mcp list` shows no "Pending approval"; `claude doctor` reports no settings errors.
- [ ] Live check inside a cloned repository of that instance: the trust dialog still appears (a non-interactive session reports the workspace untrusted).
- [ ] If either live check fails, the PR stays draft and the work stops for a redesign.
- [ ] The guide contains the trust-implication statement naming project permissions, hooks and MCP servers; the coverage statement; the four check steps; the overlay example; and the Claude Code version the live check passed on.
- [ ] CLAUDE.md lists the guide, and the overlay section of `docs/guides/workspace-config-sources.md` links it.
- [ ] No doc names a private repository, vendor or person.

**Dependencies**: Issue 4, Issue 5

## Implementation Sequence

Issues 1 and 2 have no dependencies and can be built in either order. Issue 3
needs Issue 2's `Grant` and `Withdraw`. Issue 4 needs Issue 1 (overlay config
carries the key) and Issue 3 (a grant must have a removal route before
anything writes one). Issue 5 needs Issue 3. Issue 6 closes the PR: the live
check gates the draft-to-ready move, then the docs record its result.

Right after Issue 2, run a manual probe on a host with Claude Code: a fresh
directory with a generated `.mcp.json` and approval list, trusted by hand with
Issue 2's writer, should show the server approved in `claude mcp list`. A
wrong assumption about Claude Code then surfaces before Issues 3 to 5 are
built. Until Issue 5 lands, reap still deletes whole entries, so Issue 4's
destroy criteria are exercised through `niwa destroy`, not reap.

Critical path: Issue 2, Issue 3, Issue 4, Issue 6.
