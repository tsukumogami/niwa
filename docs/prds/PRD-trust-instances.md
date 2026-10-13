---
schema: prd/v1
status: In Progress
problem: |
  A Claude Code session that isn't in bypass mode, started in a fresh niwa
  instance, stops on a folder-trust dialog and then on an approval prompt for
  each declared MCP server. Claude Code reads the approval list niwa writes into
  the instance's project settings only once that exact directory is trusted, and
  a workspace overlay can't declare MCP servers at all, so a team whose server is
  private never gets an approval list in the first place.
goals: |
  A workspace author declares MCP servers once, in the base config or an
  overlay, and opts in to instance trust once. Every instance niwa creates or
  applies then comes up trusted, a session started at its root in any
  permission mode gets neither the folder-trust dialog nor the MCP approval
  prompt, and niwa takes back on destroy or reap exactly the trust it gave.
absorbed:
  - docs/briefs/BRIEF-trust-instances.md
---

# PRD: Trust the instances niwa creates

## Status

In Progress

Absorbed [BRIEF-trust-instances](docs/briefs/BRIEF-trust-instances.md); carried in Absorbed Brief.

## Absorbed Brief

This PRD absorbed the feature's brief, which framed why the work matters before
any requirement was written.

**The problem.** niwa makes a fresh instance for every `niwa create` and every
dispatch, and Claude Code treats each one as a stranger. A session started at
an instance root in any mode other than bypass stops on a folder-trust dialog
and then on an approval prompt for each MCP server the workspace already
declared. niwa writes an approval list for those servers into the instance's
project settings, but Claude Code ignores project settings in a directory
nobody trusted, and a trusted parent doesn't count. A team whose server is
private can't even get the list: the overlay, where it keeps what it can't
publish, has no `[mcp.servers]` table. The workarounds are bypass mode, which
drops every permission check, or hand-editing user-level Claude settings on
every host.

**The outcome.** A workspace author opts in once and declares each server once,
in the base config or the overlay. Instances come up trusted, so a developer or
a dispatched worker at an instance root gets neither prompt, and the author
knows that trust means Claude Code applies the instance's permissions, hooks
and servers without asking. Destroy and reap take back only the trust niwa
gave; a config file niwa can't read is left alone with a warning; a workspace
that doesn't opt in sees no change.

**The journeys.** A workspace author turns the option on and existing
instances catch up on the next apply. A developer starts `claude` at a fresh
instance root with no prompt. A developer reclaims instances and keeps any
entry they made by hand. A developer whose Claude config is broken gets a
warning, not a damaged file.

**Where it stops.** It doesn't write the approval list (earlier, separate
work), approve servers from a verbatim `.mcp.json`, write an approve-all
setting, trust the workspace root or repo directories, edit user-level Claude
settings, or move any particular workspace's servers into `[mcp.servers]`.

## Problem Statement

niwa makes a new instance for every `niwa create` and every dispatch. When a
developer or a dispatched worker starts a Claude Code session at an instance
root in any permission mode other than bypass, Claude Code asks whether to trust
the folder and then asks to approve each MCP server in the instance's
`.mcp.json`. The workspace author already declared those servers, so both
prompts ask a question nobody wanted asked, and a session started without a
person at the keyboard stalls on them.

niwa already tries to answer the second prompt. When it generates the
instance-root `.mcp.json` from `[mcp.servers.<name>]`, it writes those names
into the instance root's `.claude/settings.json` as the approved list. Claude
Code ignores that list until the instance root itself is trusted in the user's
Claude config; a trusted parent doesn't count. Checked against Claude Code
2.1.296: `claude mcp list` shows the declared servers as pending approval in an
untrusted instance and approved once the instance root is trusted.

Two more gaps sit behind that one. A workspace overlay has no `[mcp.servers]`
table, so a team whose server can't be named in a public base config ships its
`.mcp.json` as a verbatim file instead, and niwa never approves a file it
didn't generate. And niwa's one existing trust writer, used for short-lived
review instances, can't be pointed at long-lived instances as it stands: it
deletes a whole trust entry without knowing whether niwa created it, and reap
calls that deletion for every instance it reclaims.

## Goals

A workspace author turns instance trust on once and declares each MCP server
once, in whichever of the base config or the overlay they keep it in. From then
on, a developer who opens a session at the root of an instance niwa created or
applied sees neither of the two prompts this PRD is about: Claude Code's
folder-trust dialog and its approval prompt for the declared MCP servers. The
declared servers are approved, so they connect whenever they're reachable. A
dispatched worker in a mode other than bypass gets the same, because dispatch
provisions its instance through the same create path. Destroying or reaping an
instance removes the trust niwa gave it and leaves any trust a person gave
untouched. A workspace that doesn't opt in sees no change.

## User Stories

- As a workspace author whose team keeps a private MCP server in the workspace
  overlay, I want to declare it in the overlay's `[mcp.servers]` table, so that
  it's generated and approved the same way a base-declared server is, without
  naming it in the public base config.
- As a workspace author, I want to turn instance trust on once, in the base
  config or the overlay, so that every instance created or applied after that
  comes up trusted, including instances that existed before I turned it on.
- As a workspace author who turns instance trust back off, I want the next
  `niwa apply` in each instance to withdraw the trust niwa gave it, so that
  turning the option off actually undoes it.
- As a developer, I want to run `niwa create`, start `claude` at the new
  instance root in default permission mode, and get neither the folder-trust
  dialog nor the MCP approval prompt.
- As a developer dispatching a worker with `niwa dispatch` in default permission
  mode, I want the worker's fresh instance to be trusted before the worker
  starts, so that it doesn't stall on a prompt nobody is there to answer.
- As a developer reclaiming instances with `niwa destroy` or `niwa reap`, I
  want niwa to remove only the trust entries it added, so that a path I trusted
  by hand keeps its entry.
- As a developer whose Claude config can't be parsed or is locked by a busy
  session, I want niwa to warn me and leave the file alone, so that a failed
  trust write never damages my Claude config.

## Requirements

Terms used below. The **Claude config file** is the user-level JSON file
Claude Code keeps its per-project state in (R10). A **trust entry** is
`projects[<key>]` in that file, and its **trust flag** is
`projects[<key>].hasTrustDialogAccepted`. A **managed instance root** is the
root directory of an instance niwa created, as recorded in that instance's
`.niwa/instance.json`.

### Declaring MCP servers in an overlay

- **R1.** A workspace overlay can declare `[mcp.servers.<name>]` entries with
  the same fields and the same validation errors as the base config.
- **R2.** `vault://` references in an overlay server's `headers` and `env`
  resolve against the overlay's own vault providers, the same way the overlay's
  `[env]` does, before the overlay merges into the base config.
- **R3.** An overlay server whose name the base config also declares is
  dropped, with a warning naming the server; the base declaration is used, and
  the dropped entry's `vault://` references are never resolved.
- **R4.** After the merge, overlay-declared servers are ordinary entries of
  `[mcp.servers]`: every output generated from that table includes them,
  namely the instance-root `.mcp.json`, every other agent's generated MCP
  config (for example Codex's `[mcp_servers.<name>]` table), and the instance
  root's approved-server list. Output order is by server name, as today.

### The trust option

- **R5.** The workspace config key `[claude] trust_instances` turns instance
  trust on. It takes a TOML boolean; any other type is a config error. Absent
  means off.
- **R6.** The key can be set in the base config or in the overlay. "Set" means
  present in that file. When both set it, the base config's value wins; when
  only one sets it, that value applies.
- **R7.** When the option is on, every run of the create/apply pipeline marks
  the instance root trusted before the command exits. That covers `niwa
  create`, `niwa apply`, `niwa reset`, `niwa init` when it creates an
  instance, `niwa dispatch`, and session-start provisioning, since each of
  these runs that pipeline.
- **R8.** niwa sets only the trust flag. It doesn't write
  `hasTrustDialogHooksAccepted` or any other key, and it preserves every other
  key in the file and in the entry.
- **R9.** The entry's key is the instance root's absolute path with every
  symlink resolved, including the root itself and any parent. That is the key
  Claude Code computes for a session started at that root.
- **R10.** The Claude config file is `$CLAUDE_CONFIG_DIR/.claude.json` when
  `CLAUDE_CONFIG_DIR` is set and non-empty, and `$HOME/.claude.json`
  otherwise. When that path is a symlink, niwa writes the file it points to and
  leaves the symlink in place.
- **R11.** niwa treats an existing entry by its state:

  | Entry state when niwa looks | What niwa does | What niwa records |
  |---|---|---|
  | No entry at the key | creates the entry with the flag set | `created` |
  | Entry whose flag is absent or `false` | sets the flag | `flag-only` |
  | Entry whose flag is `true`, not in niwa's record | nothing | nothing |
  | Entry whose flag is `true`, already in niwa's record | nothing | keeps the record |

- **R12.** When the option is off, niwa writes no trust entry. A `niwa apply`
  with the option off withdraws what the instance's record names: it deletes
  the trust flag (never the whole entry, since the instance and Claude Code's
  data for it still exist) and clears the record. If the flag is already gone,
  it just clears the record. `niwa create` with the option off has nothing to
  withdraw.

### Recording and removing what niwa added

- **R13.** niwa records each trust write in the instance's state: the Claude
  config file path it wrote, the key, and `created` or `flag-only`. Removal
  uses the recorded path and key, never values recomputed at removal time.
  When an apply with the option on resolves a different config file or key
  than the record names (for example, `CLAUDE_CONFIG_DIR` changed), niwa first
  withdraws the recorded grant under R12's rules, then writes and records the
  new one.
- **R14.** Destroying an instance removes what its record names: the whole
  entry when the record says `created`, only the trust flag when it says
  `flag-only`. If the recorded entry or flag is already gone, nothing is
  removed. The record is read before the instance directory is removed. This
  applies to every route that destroys an instance: `niwa destroy` (single,
  picked, at-root, and whole-workspace), `niwa reap` (both sweeps), `niwa
  reset`, dispatch's cleanup of a failed launch, and review-instance cleanup.
- **R15.** No route removes a trust entry or flag the record doesn't name. The
  current reap behavior, which deletes the entry at every reclaimed path, is
  removed.
- **R16.** The trust niwa writes for short-lived review instances goes through
  the same writer and record: it follows R8 through R11 and R13 through R15
  (so it stops writing `hasTrustDialogHooksAccepted`, and reap removes it only
  through the record).

### Safety

- **R17.** niwa classifies the Claude config file before any write or removal:

  | File state | Treatment |
  |---|---|
  | Missing | create it (and its directory, mode 0700, if missing) with mode 0600 |
  | Empty | treat as `{}` |
  | Not valid JSON, top level not an object, `projects` not an object, the entry at the key not an object, or unreadable | write nothing, warn naming the file and the reason |

  A skipped write records nothing, so the next apply tries again. A skipped
  removal during destroy doesn't stop the destroy; the warning says the entry
  was left. A skipped withdrawal during apply keeps the record, so the next
  apply tries again. None of these changes the command's exit status.
- **R18.** niwa holds Claude Code's config lock while it reads, changes and
  replaces the file. The lock is the directory `<config file>.lock`, taken by
  creating it (creation fails if it exists) and released by removing it. niwa
  retries for up to 5 seconds. A lock directory whose modification time is
  more than 10 seconds old is stale; niwa removes it and retries. If niwa
  can't take the lock in time, it skips the write and warns, under R17's
  rules. It never writes the file without the lock, and it releases the lock
  on every path, errors included.
- **R19.** niwa writes the new contents to a temporary file in the same
  directory, flushes it to disk, and renames it over the original, so a reader
  sees either the old file or the new one. The original file's permission
  mode is kept.
- **R20.** When the file already says what niwa would write, niwa doesn't
  rewrite it: contents, inode and modification time are unchanged. The lock
  may still be taken to read.
- **R21.** niwa never edits `~/.claude/settings.json` or any other Claude Code
  settings file outside the instance, never writes an
  `enableAllProjectMcpServers` setting, and never writes a trust entry for a
  path other than a managed instance root.

### Docs

- **R22.** A guide, `docs/guides/instance-trust.md`, documents the option. It
  states that trusting an instance makes Claude Code apply that instance's
  project permissions, hooks and MCP servers without asking.
- **R23.** The guide states that trust covers a session started at the
  instance root and not one started inside a repository directory, and gives
  a four-step check for "still seeing prompts": start the session at the
  instance root; run `claude doctor` and look for settings errors; run
  `claude mcp list` at the instance root and look for "Pending approval"; and
  check whether `CLAUDE_CONFIG_DIR` differs from what niwa ran with.
- **R24.** The guide shows an MCP server declared in an overlay, with a
  placeholder name and a `vault://` header reference.

## Acceptance Criteria

Automated tests run with HOME pointed at a temporary directory.

### Overlay MCP servers

- [ ] An overlay with `[mcp.servers.example]` (an `http` server with a
      `vault://` header) and no base declaration produces an instance-root
      `.mcp.json` containing `example` with the resolved header value, and the
      instance root's `.claude/settings.json` lists `example` as approved.
- [ ] With a workspace that also generates Codex config, the same overlay
      server appears in the generated Codex `[mcp_servers.example]` table.
- [ ] An overlay server missing both `url` and `command` fails with the same
      error text the base config gives for that entry.
- [ ] An overlay server whose name the base declares, with a different `url`
      and a `vault://` header naming a key the overlay's provider lacks,
      produces a warning naming the server, generated files carry the base
      `url`, and apply succeeds (the dropped header was never resolved).
- [ ] An overlay `vault://` header that names a provider only the base
      declares fails with the existing provider-not-declared error.

### Writing trust

- [ ] With `[claude] trust_instances = true` in the base config and no
      `.claude.json`, `niwa create` exits 0, creates `$HOME/.claude.json` with
      mode 0600 holding `projects[<resolved instance root>].hasTrustDialogAccepted
      = true` and no `hasTrustDialogHooksAccepted`, and `.niwa/instance.json`
      records the file path, the key and `created`.
- [ ] With the key set only in the overlay, the same holds. With base `false`
      and overlay `true`, no entry is written; with base `true` and overlay
      `false`, the entry is written.
- [ ] `trust_instances = "yes"` fails config parsing with an error naming the
      key.
- [ ] With the key absent, `niwa create` and `niwa apply` leave an existing
      `$HOME/.claude.json` byte-identical, and create none when it's absent.
- [ ] With `CLAUDE_CONFIG_DIR` set to a temporary directory, the entry lands in
      `$CLAUDE_CONFIG_DIR/.claude.json` and `$HOME/.claude.json` is untouched.
      With `CLAUDE_CONFIG_DIR=""`, the entry lands in `$HOME/.claude.json`.
- [ ] With `$HOME/.claude.json` a symlink to another file, the entry lands in
      the target and the symlink still exists.
- [ ] An instance root whose parent directory is reached through a symlink,
      and an instance root that is itself a symlink, are each keyed by the
      `EvalSymlinks` result of the root path.
- [ ] A `.claude.json` with other top-level keys and other project entries, at
      mode 0644, keeps all of them unchanged and keeps mode 0644 after a write
      and after a removal.
- [ ] `niwa apply` in an instance created before the option was turned on adds
      and records the entry. A second `niwa apply` leaves the file
      byte-identical with the same inode and modification time.
- [ ] `niwa dispatch` (with the launch stubbed) and session-start provisioning
      each leave the instance's entry written and recorded when the command
      exits.
- [ ] An entry already holding `hasTrustDialogAccepted = true` before `niwa
      create` is byte-identical after create and after destroy, and the
      instance's record is empty.
- [ ] An entry holding `hasTrustDialogAccepted = false` and an entry with no
      flag but other keys each end with the flag `true` and the other keys
      unchanged after create, with the record saying `flag-only`.

### Removing trust

- [ ] After create with the option on, each of `niwa destroy`, `niwa reap`,
      `niwa reset`, dispatch's failed-launch cleanup and review-instance cleanup
      removes the `created` entry.
- [ ] For a `flag-only` record, destroy removes only the flag, and the entry's
      other keys remain.
- [ ] With `CLAUDE_CONFIG_DIR` set at create and unset at destroy, destroy
      removes the entry from the file named in the record.
- [ ] With `CLAUDE_CONFIG_DIR` set at create and unset at a later `niwa
      apply`, the flag is withdrawn from the first file, the entry is written
      to `$HOME/.claude.json`, and the record names the new file.
- [ ] Reaping an instance with no record leaves an existing entry at its path
      byte-identical.
- [ ] A review instance's trust is recorded at seeding, keyed by the resolved
      path in the file R10 names, and reap removes it.
- [ ] Turning the option off and running `niwa apply` deletes the flag (the
      entry's other keys remain) and clears the record. If the entry was
      already removed by hand, apply exits 0 and clears the record.

### Failure handling

- [ ] An empty `.claude.json` ends holding only the new entry. With
      `CLAUDE_CONFIG_DIR` naming a missing directory, the directory is created
      with mode 0700 and the file with mode 0600.
- [ ] With `.claude.json` holding invalid JSON, a JSON array, `"projects":
      []`, or a non-object value at the instance's key, or with the file
      unreadable (mode 0000), each of create, apply and destroy exits 0, prints a warning naming
      the file, and leaves the file byte-identical. After create, the record is
      empty; after a skipped withdrawal on apply, the record is kept.
- [ ] With `<config file>.lock` held (a fresh directory) for longer than 5
      seconds, the write is skipped within 6 seconds with a warning, the file is
      byte-identical, and the command exits 0.
- [ ] With a `<config file>.lock` directory whose modification time is 11
      seconds old, niwa removes it, writes the entry, and leaves no lock
      directory behind.
- [ ] After a successful write and after a write that fails partway (injected
      error after the temp file is written), no lock directory remains and the
      original file is intact in the failure case.
- [ ] Ten concurrent trust writes (goroutines in one test process) for ten
      different instance roots against one config file all land.

### Boundaries

- [ ] After create, apply and destroy with the option on, `~/.claude/settings.json`
      (when present) is byte-identical, no file under HOME contains
      `enableAllProjectMcpServers`, and the only `projects` key niwa added or
      changed is the managed instance root's.

### Docs and live check

- [ ] `docs/guides/instance-trust.md` contains the R22 statement naming
      project permissions, hooks and MCP servers; the R23 coverage statement
      and its four check steps; and an overlay `[mcp.servers.<placeholder>]`
      example with a `vault://` header.
- [ ] On a host with Claude Code installed, a fresh instance created with the
      option on and an overlay-declared server passes three non-interactive
      checks run at the instance root: `claude -p "list your mcp tools"` in
      default permission mode exits 0 and names the server's tools; `claude mcp
      list` shows the server without "Pending approval"; and `claude doctor`
      reports no settings errors. The guide records the Claude Code version the
      check passed on.

## Out of Scope

- Writing the approved-server list itself; separate, earlier work owns it. This
  feature makes Claude Code read it and adds overlay servers to it.
- Approving servers from a verbatim-distributed `.mcp.json`, and any
  approve-all setting.
- Trusting the workspace root or repo directories inside an instance. A session
  started inside a repo directory is keyed by that repo and still sees Claude
  Code's prompts.
- A per-developer opt-out in personal niwa config. A developer who doesn't want
  trust written uses a workspace config without the key; an opt-out can follow
  if anyone asks.
- Running `claude doctor` or `claude mcp list` from niwa as an automatic check.
- Cleaning up `hasTrustDialogHooksAccepted` values, or review-instance trust
  entries, written before this change.
- Trust for agents other than Claude Code (Codex directory trust already
  exists separately).
- Moving any particular workspace's servers into `[mcp.servers]`; that's each
  workspace author's change in their own config.

## Known Limitations

- niwa can only write to the Claude config file its own environment names. A
  session started later with a different `CLAUDE_CONFIG_DIR` reads another file
  and still prompts.
- Review-instance trust entries written by earlier niwa versions have no
  record, so reap no longer removes them. Their paths are already gone, so they
  grant nothing; they're leftover data.
- When a removal is skipped during destroy (unparseable file, lock timeout), the
  entry stays behind for a path that no longer exists.
- When an instance is destroyed while a session is still running in it, Claude
  Code can write the project entry back when that session saves its state. The
  rewritten entry carries no trust flag, so it's leftover data, not trust.
- Trust is necessary but not sufficient: if the instance's `settings.json` has
  any value Claude Code rejects, Claude Code ignores the whole file and approves
  nothing. The guide's check (R23) is how a developer finds that.
- The lock protocol (R18) and the key computation (R9) match Claude Code
  2.1.296. A later Claude Code that changes either reopens the gap this closes;
  the live check is how that's caught.

## Decisions and Trade-offs

- **Overlay `[mcp.servers]` is part of this feature.** The owner wants servers
  declared once, centrally, in the base config or the overlay. Without overlay
  support, a team whose server can't appear in a public base config gets no
  approval list, and trust alone fixes nothing for them. Alternative: approve
  the verbatim file's servers by reading its names. Rejected: niwa doesn't parse
  files it distributes verbatim, and it would mean two places to keep in step.
- **The key lives in `[claude]` and the overlay may set it.** It's Claude-only
  behavior, like the other `[claude]` switches. The overlay may set it because
  the team wants the switch next to the servers it unlocks, and an overlay only
  applies on machines that can clone it, so the developers it affects can read
  it. This differs from `[workspace] strict_secrets`, which the overlay may not
  set. Base wins on conflict so a public base config can still say no.
- **Ownership is recorded, not inferred.** Claude Code's own trust acceptance
  writes the same shape niwa does, so nothing in the entry says who wrote it.
  The instance-state record, modeled on niwa's Codex trust record, is the only
  reliable source.
- **Destroy removes a created entry whole; withdrawal removes only the flag.**
  After destroy the path is gone, and whatever Claude Code stored under it is
  dead data. On withdrawal the instance still exists and Claude Code's data for
  it still matters.
- **One writer for review instances and opted-in instances.** Keeping two
  writers would leave reap's unconditional removal in place for review
  instances, and that removal is what deletes hand-made entries today.
- **Turning the option off withdraws.** The record makes withdrawal safe, and
  leaving trust behind after an author withdrew it would surprise them.
- **Hold Claude Code's config lock.** Claude Code rewrites its config often and
  re-reads under that lock before each write, so niwa holding the same lock
  closes the lost-update window. Skipping on timeout matches the
  unparseable-file posture: never risk the user's config.
- **Write only `hasTrustDialogAccepted`.** The review-instance writer also sets
  `hasTrustDialogHooksAccepted`, which current Claude Code doesn't read.
