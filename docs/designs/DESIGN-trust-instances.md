---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-trust-instances.md
problem: |
  niwa writes a declared-server approval list into each instance root's
  project settings, but Claude Code ignores project settings until that exact
  directory is trusted in its user config, and a workspace overlay can't
  declare MCP servers at all. niwa's one trust writer serves short-lived review
  instances, deletes whole entries with no ownership record, ignores
  CLAUDE_CONFIG_DIR, keys on unresolved paths, and races Claude Code's own
  writes.
decision: |
  Add an internal/claudetrust package that finds the config file the way Claude
  Code does, keys on the symlink-resolved instance root, holds Claude Code's
  own config lock, and returns a grant record saying whether niwa created the
  entry or only set the flag. The create/apply pipeline gains a trust step
  driven by `[claude] trust_instances`, the grant is persisted in instance
  state, and DestroyInstance withdraws exactly what the record names. Review
  instances move onto the same writer, and reap's unconditional removal goes.
  The overlay gains `[mcp.servers]` and `[claude] trust_instances`, resolved
  against its own vault and merged base-wins.
rationale: |
  The Codex trust writer already proved the record-bounded model in this
  codebase, so the Claude writer follows its contract rather than inventing a
  new one. A neutral package keeps the writer importable by both the workspace
  pipeline and the review command without a dependency cycle. Taking Claude
  Code's own lock is the only way to close the lost-update window against a
  process that rewrites the file constantly. Merging overlay servers before
  generation means every existing validator, generator and approval-list
  writer covers them unchanged.
decision_provenance: inline-resolved
---

# DESIGN: Trust the instances niwa creates

## Status

Proposed

## Context and Problem Statement

A session Claude Code starts at a niwa instance root reads that root's
`.claude/settings.json` only if the user-level Claude config file holds
`projects[<root>].hasTrustDialogAccepted = true` for that exact path. Without
it, Claude Code shows a folder-trust dialog, then asks to approve every server
in the instance's `.mcp.json`, and the `enabledMcpjsonServers` list niwa
writes beside it does nothing. The PRD asks niwa to write that trust entry for
every instance it creates or applies when the workspace opts in, and to remove
exactly what it wrote when the instance goes.

Four existing pieces of code are in the way.

**The overlay has no MCP table.** `config.WorkspaceOverlay` carries sources,
groups, repos, `[claude]` hooks, settings, content, marketplaces and plugins,
env, files, `[instance.files]` and vault. It has no `[mcp]`. A team whose
server can't be named in a public base config has to ship a verbatim
`.mcp.json` through `[instance.files]`, and niwa never approves servers from a
file it didn't generate. The PRD requires overlay `[mcp.servers.<name>]`
entries with the same fields and validation as the base (R1), `vault://`
values in `headers` and `env` resolved against the overlay's own providers
(R2), base-wins on a name collision with a warning, without resolving the
dropped entry's secrets (R3), and overlay servers reaching every generated
output, in name order (R4).

**The trust writer lives in `internal/watch/trust.go`.** It was built for
sandboxed review instances that exist for minutes. It writes
`hasTrustDialogAccepted` and `hasTrustDialogHooksAccepted` (a key Claude Code
2.1.296 doesn't read), keys on `filepath.Abs` without resolving symlinks
(Claude Code keys on the realpath of its working directory, so a root under
macOS `/var` or `/tmp` gets an entry nobody looks up), always writes
`$HOME/.claude.json` (Claude Code reads `$CLAUDE_CONFIG_DIR/.claude.json` when
that's set), and takes no lock (Claude Code rewrites the file often and
serializes its own writes through `<config>.lock`).

**Removal deletes whole entries with no ownership record.**
`RemoveInstanceTrust` deletes `projects[<path>]`, and `niwa reap` calls it for
every instance it reclaims, after the destroy. Claude Code writes the same
entry shape when a person answers its trust prompt, so this deletes hand-made
trust. `niwa destroy` doesn't call it at all.

**Instance teardown has many routes.** `workspace.DestroyInstance` serves
`niwa destroy`, `niwa reset`, both reap sweeps, dispatch's failed-launch
cleanup and review cleanup; `workspace.DestroyWorkspace` removes instances
directly. The PRD requires every route to remove what the record names and
nothing else (R14, R15), reading the record before the directory goes.

The PRD's other requirements shape the writer itself. It sets only the trust
flag and preserves every other key (R8). It keys on the root's fully
symlink-resolved absolute path (R9). It finds the config file through
`CLAUDE_CONFIG_DIR`, then `$HOME`, and writes through a symlinked config file
(R10). It treats an existing entry by state: absent means create and record
`created`; an entry whose flag is absent or false gets the flag set and is
recorded `flag-only`; an entry already true is left alone and not recorded
unless niwa already recorded it (R11). With the option off, apply withdraws
the recorded flag and clears the record (R12). The record holds the file path,
the key and the mode, and removal uses those, never recomputed values; a
changed file or key at apply time withdraws the old grant first (R13). A
created entry is removed whole on destroy, a flag-only grant loses only its
flag (R14). The writer classifies the file before touching it (missing: create
0600 in a 0700 directory; empty: `{}`; anything unparseable, non-object or
unreadable: warn and skip), never fails a command over it (R17), holds Claude
Code's lock with a 5-second wait and a 10-second staleness rule (R18), writes
temp-fsync-rename keeping the mode (R19), doesn't rewrite a file already in
the desired state (R20), and never touches settings files, approve-all
settings or paths other than a managed instance root (R21).

## Decision Drivers

- **Never damage the user's Claude config.** It holds credentials-adjacent
  state and every project's history. Any doubt (parse failure, lock timeout,
  unexpected shape) means write nothing and warn.
- **Record-bounded removal.** Ownership can't be read from an entry, so the
  only safe removal is one a record names. This is the same contract the Codex
  directory-trust writer (`internal/workspace/codex_trust.go`) already keeps.
- **Match Claude Code exactly.** Key, file location and lock must be the ones
  Claude Code 2.1.296 uses, or the feature fails silently.
- **No dependency cycle.** The writer is called from `internal/workspace` (the
  pipeline, destroy) and from `internal/cli` (review seeding). It can't live in
  either if both need it without importing each other's internals.
- **Unit suites stay off the developer's files.** Every Applier built in
  tests has an empty `DeveloperHome`, which already gates out-of-instance
  writes; the new step honors the same gate.
- **Smallest overlay change that makes R1 to R4 true.** Existing MCP
  validation and generation run on the merged config, so merging overlay
  servers early is enough.
- **Don't collide with the open approval-list change.** That change edits
  `docs/guides/file-distribution.md`, `config.go` near the settings key
  constant, and the functional suite's step registration. This design touches
  those files only in separate regions, and documents the feature in a new
  guide.

## Considered Options

Every question below was resolved inline under `/scope` (standard tier).

### Decision 1: Where the Claude trust writer lives

**Chosen: a new neutral package, `internal/claudetrust`.** It owns config-file
resolution, key canonicalization, the lock, the classification table and the
grant record type, and imports nothing from niwa except the standard library.
`internal/workspace` (pipeline, destroy) and `internal/cli` (review seeding)
both import it. `internal/watch/trust.go` is deleted and its tests move.

Alternatives:

- **Extend `internal/watch/trust.go` in place.** Rejected: the pipeline would
  import a package named for the review command, and the file's contract
  (seed, then delete the entry) is the one this feature replaces.
- **Route through the agentplan `DirectoryTrust` capability, as Codex does.**
  Rejected: the capability table declares Claude Code has no per-directory
  trust, its procedures receive repository roots rather than the instance
  root, and its record (`TrustKeys`) holds Codex repo paths that the Codex
  writer reconciles against. Changing the declaration, the binding, its
  structural tests and the record's meaning is a larger and riskier change
  than a dedicated step.
- **Put the writer in `internal/workspace`.** Rejected: `internal/cli/watch.go`
  would then reach into the workspace package for a file-format helper, and
  the writer's tests would run inside the workspace package's large suite.

### Decision 2: The ownership record and how teardown reads it

**Chosen: one optional field in instance state,
`ClaudeTrust *claudetrust.Grant` (`json:"claude_trust,omitempty"`), holding
`config_file`, `key`, `mode` (`created` or `flag-only`) and `source`
(`workspace` or `review`).** omitempty keeps the schema version at 4, as the
`trust_keys` precedent did. Create and Apply carry it forward from the
existing state like `TrustKeys`, since Apply rebuilds state from scratch.
`DestroyInstance` and `DestroyWorkspace` load the instance's state before
`os.RemoveAll` and call `claudetrust.Withdraw(grant, claudetrust.Destroy)`;
failures become warnings through the destroy reporter, and the destroy goes
on. Reap's two `removeInstanceTrustFunc` calls and the review staging
cleanup's call are deleted, since the destroy they precede now does the work.

`source` exists so that apply's withdrawal with the option off (R12) leaves a
review grant alone: a review instance's trust is the review command's, not the
workspace's, and an apply in it must not pull trust out from under a running
review. An instance holds at most one grant, and the first source to record
one keeps it: a later `Ensure` from the other source finds the entry trusted
and already recorded, and returns the record unchanged.

**The record is validated before it's acted on.** Instance state is a file
anyone who controls the instance can edit, so a crafted record must not turn
removal into a write to an arbitrary file or the removal of someone else's
trust. Every `Withdraw` checks that `mode` and `source` are known values, that
`config_file` is absolute and clean with the basename `.claude.json`, and that
it resolves to a regular file; and every teardown and retraction also checks
that `key` equals `claudetrust.Key(instanceDir)` for the instance being
destroyed or applied, computed while the directory still exists. A record that
fails any check is left alone with a warning, and nothing is written.

Alternatives:

- **A separate record file in niwa's state directory, keyed by instance
  path.** Rejected: it would outlive instances destroyed by hand and need its
  own garbage collection; instance state already lives and dies with the
  instance and is read by every destroy route.
- **Reuse `TrustKeys`.** Rejected: the Codex writer reconciles that list
  against its own file, and a Claude path in it would be retracted from the
  wrong config.
- **Keep reap's removal and make it conditional.** Rejected: reap calls it
  after the destroy, when the state file is already gone, so it has nothing to
  condition on.

### Decision 3: Serializing with Claude Code's writes

**Chosen: take Claude Code's own lock.** Claude Code 2.1.296 serializes writes
to its config with a lock directory at `<config file>.lock`, created with an
atomic mkdir, treated as stale when its modification time is more than 10
seconds old, and re-reads the file under the lock before writing. niwa does
the same: `os.Mkdir(<config>.lock, 0o700)`; on `EEXIST`, stat it, and if its
mtime is older than 10 seconds, re-stat to confirm it's the same directory and
still stale, remove it with a non-recursive `os.Remove`, and retry; otherwise
sleep 50 ms and retry, for up to 5 seconds. Under the lock niwa resolves the
write target, reads, decides and writes, then releases with a non-recursive
`os.Remove` through a deferred call on every path. The lock path is built from
the unresolved config path (the name Claude Code locks), while the write goes
to the symlink-resolved target, resolved after the lock is held so a link
swapped before then can't redirect the write.

Alternatives:

- **No lock, as today.** Rejected: a Claude Code write landing between niwa's
  read and rename is lost, and that write can be an OAuth refresh or another
  project's trust answer.
- **An advisory lock in niwa's own state directory, as the Codex writer
  does.** Rejected: it serializes niwa against niwa only. Codex doesn't rewrite
  its config during sessions; Claude Code does.
- **flock on the config file itself.** Rejected: Claude Code doesn't use
  flock, so it serializes nothing against it, and a rename replaces the locked
  inode anyway.

### Decision 4: Overlay `[mcp.servers]` and `[claude] trust_instances`

**Chosen: decode both in the overlay, filter collisions before resolution,
resolve against the overlay bundle, then merge.** `WorkspaceOverlay` gains
`MCP config.MCPConfig` (`toml:"mcp"`), and `OverlayClaudeConfig` gains
`TrustInstances *bool`. In the pipeline's overlay step, before the overlay's
temporary config is resolved, niwa drops every overlay server whose name the
base declares and defers a warning naming it (so R3's dropped secrets are
never looked up). The surviving `overlay.MCP` joins `overlay.Env` and
`overlay.Repos` in the temporary config passed to `resolve.ResolveWorkspace`
with the overlay's vault bundle, whose walker already visits `MCP`. Then
`MergeWorkspaceOverlay` copies the base `MCP.Servers` map and adds the overlay's
entries, and sets `merged.Claude.TrustInstances` from the overlay only when
the base left it nil. Validation and generation run on the merged config, so
R1 and R4 follow from the existing code paths.

When trust is on only because the overlay set it, the first apply of each
instance that writes trust prints a one-time notice through the existing
disclosed-notices record: trust is on because the workspace overlay sets
`[claude] trust_instances`, niwa wrote it to the named config file, and what
that lets Claude Code do. A base config that wants to refuse the overlay's
choice has to say `trust_instances = false`; leaving the key out hands the
decision to the overlay.

Alternatives:

- **Resolve overlay servers after the merge, with the base bundle.** Rejected:
  overlay `vault://` references would resolve against providers the overlay
  didn't declare, which is the per-layer isolation the overlay's env already
  avoids.
- **Error on a name collision.** Rejected: base-wins with a warning is how
  every other overlay table treats collisions, and an overlay that goes stale
  must keep applying.
- **Treat `trust_instances` like `strict_secrets` (base only).** Rejected by
  the PRD's decision: the overlay applies only on machines that can clone it,
  so the developers it affects can read it, and the team wants the switch
  beside the servers it unlocks. Base still wins on conflict.

## Decision Outcome

niwa gains one package that is the only code allowed to edit Claude Code's
user config, and every write it makes carries a record that says exactly what
to undo. The pipeline calls it after the instance-root settings and `.mcp.json`
are written, the destroy path calls it before the directory goes, and the
review command calls it instead of its own writer. The overlay's two new
fields feed the merged config the rest of niwa already understands, so the
approval list niwa writes for base servers now covers overlay servers too,
and the trust entry makes Claude Code read that list.

## Solution Architecture

### `internal/claudetrust`

```go
type Mode string   // "created" | "flag-only"
type Source string // "workspace" | "review"

type Grant struct {
    ConfigFile string `json:"config_file"` // resolved path niwa wrote
    Key        string `json:"key"`
    Mode       Mode   `json:"mode"`
    Source     Source `json:"source"`
}

type Env struct {
    Home            string // developer home; "" disables every write
    ClaudeConfigDir string // value of CLAUDE_CONFIG_DIR at the call
}

func ConfigFile(env Env) (string, error)       // R10, unresolved name
func Key(instanceRoot string) (string, error)  // R9: Abs + EvalSymlinks

type Outcome struct {
    Grant   *Grant // nil when nothing is recorded (already trusted by someone else, or skipped)
    Changed bool   // file was rewritten
    Warning string // non-empty on a skip
}

func Ensure(env Env, instanceRoot string, prior *Grant, src Source) Outcome
func Withdraw(g *Grant, how WithdrawMode) (warning string) // Destroy | Retract
```

`Ensure` resolves the file and key, and if `prior` names a different file or
key, withdraws it (`Retract`) first (R13). It takes the lock, classifies the
file (R17), applies R11's table, and writes only when the entry changes (R20),
through a temp file created with mode 0600, fsync and rename in the resolved
target's directory, then sets the existing mode, or keeps 0600 for a new file
(R19).

The file is decoded so that nothing outside the one flag changes value:
the top level, `projects` and the entry are each decoded as
`map[string]json.RawMessage`, so every other value is written back as the
exact bytes it was read as (large integers and escapes included). Output is
two-space indented with HTML escaping off, which is the format Claude Code
writes. A top level, `projects` or entry that isn't a JSON object is never
replaced: niwa warns and skips. When `prior` already names
this key and the flag is true, it returns `prior` unchanged. A skip returns
`prior` unchanged with a warning, so a record is never lost because one write
couldn't happen.

`Withdraw(Destroy)` deletes the entry for `created` and the flag for
`flag-only`. `Withdraw(Retract)` deletes only the flag either way. Both are
no-ops when the entry or flag is already gone, and both use the grant's
recorded file and key.

Only `hasTrustDialogAccepted` is written, and only under `projects`.

### Pipeline step

`Applier.deliverClaudeTrust(instanceRoot, cfg, existing)` runs inside
`runPipeline` after the directory-trust and niwa-plugin steps, so the
instance-root `.claude/settings.json` and `.mcp.json` already exist. It
returns early, carrying `existing.ClaudeTrust` forward, when
`a.DeveloperHome == ""`. Otherwise:

- `cfg.Claude.TrustInstances` true: `claudetrust.Ensure(...,
  SourceWorkspace)`; persist the returned grant.
- false or nil, with a `workspace`-sourced record: `Withdraw(Retract)` and
  clear the record; on a skip, keep it.
- a `review`-sourced record is carried forward untouched.

`CLAUDE_CONFIG_DIR` is read from the process environment at that point. The
grant goes back through `pipelineResult` and into both state builders, beside
`TrustKeys`. Warnings join `allWarnings` and print through `DeferWarn`.

### Teardown

`DestroyInstance` and `DestroyWorkspace` call a shared
`withdrawClaudeTrust(instanceDir, reporter)` right after plugin-record pruning:
load state; if it holds a grant, `Withdraw(Destroy)`; report any warning. A
missing or unreadable state file means nothing to withdraw. Reap, dispatch,
reset and review cleanup reach this through `DestroyInstance`, so their own
trust calls are deleted.

### Review instances

`resolveAskPosture` keeps its sandbox and `~/.claude` guards, then calls
`claudetrust.Ensure(env, instancePath, state.ClaudeTrust, SourceReview)` and
saves the returned grant into the instance's state. It returns true only when
the entry is trusted afterward, whether niwa wrote it or found it trusted; a
skip falls back to the hard-deny posture as today. The `~/.claude` guard reads
the same config home the writer resolves.

### Overlay

`config.WorkspaceOverlay.MCP` and `OverlayClaudeConfig.TrustInstances` are
decoded by `ParseOverlay`. The pipeline's overlay step filters, resolves and
merges as Decision 4 describes. `MergeWorkspaceOverlay`'s doc comment lists
the two new fields.

### Data flow

```
workspace.toml + overlay  ->  merge (overlay MCP filtered, resolved)  ->  cfg
cfg.MCP  -> .mcp.json, Codex config, enabledMcpjsonServers   (existing code)
cfg.Claude.TrustInstances -> deliverClaudeTrust -> claudetrust.Ensure
                                     -> <config>.lock -> .claude.json
                                     -> InstanceState.ClaudeTrust
destroy/reap/reset -> DestroyInstance -> LoadState -> Withdraw -> RemoveAll
```

## Implementation Approach

1. **Overlay MCP servers.** Add `WorkspaceOverlay.MCP`, the collision filter,
   overlay-bundle resolution and the merge, with unit tests for R1 to R4 and a
   functional scenario generating `.mcp.json`, the approval list and Codex
   config from an overlay server.
2. **The `claudetrust` package.** Config file and key resolution, the lock, the
   classification table, `Ensure` and `Withdraw`, with unit tests for every
   row of R11 and R17, the lock timeout and staleness, symlinked config and
   root, mode preservation, value preservation outside the one flag, no-op
   rewrites, concurrent writers, and crafted records (another project's key, a
   file not named `.claude.json`, a directory) that must be refused.
3. **Pipeline, state and teardown.** `[claude] trust_instances` (base and
   overlay), `InstanceState.ClaudeTrust`, `deliverClaudeTrust`, withdrawal in
   `DestroyInstance` and `DestroyWorkspace`, review seeding moved to
   `claudetrust`, `internal/watch/trust.go` and reap's calls removed, and the
   one-time notice when the overlay is what turned trust on. Unit tests per
   route and `@critical` functional scenarios for create, apply, destroy and
   the off switch.
4. **Docs.** `docs/guides/instance-trust.md` (R22 to R24), a line in the
   CLAUDE.md guide list, a pointer from the overlay section of
   `docs/guides/workspace-config-sources.md`, and an update to the review
   design's note about the lock. Run the live check on a host with Claude
   Code, including a session started inside a cloned repository (expected to
   still see the trust dialog), and record the version and results in the
   guide.

Steps 1 and 2 are independent; step 3 needs step 2; step 4 needs both.

## Security Considerations

**What trust grants.** A trusted instance root makes Claude Code apply that
instance's project permissions, hooks and MCP servers without asking. Anyone
who can change the workspace config, or the overlay, for a developer who opted
in can therefore put hooks and servers in front of that developer's sessions
with no prompt, and a pre-approved stdio server runs a command of the config
author's choosing. That's the cost the owner accepted; the guide says it in
those words (R22). The option is off by default. A base config refuses an
overlay's choice only by setting `trust_instances = false`; leaving it unset
hands the decision to the overlay, and the one-time notice tells each
developer when the overlay is the reason their instance is trusted.

**Which sessions it reaches.** Claude Code 2.1.296 keys a session inside a
repository by that repository's root and bounds its trust lookup there, so the
instance-root entry covers sessions started at the instance root and not
sessions started inside a cloned repository. That reading comes from the
2.1.296 binary; the live check confirms it by starting a session inside a
repository of the test instance and expecting the trust dialog, and the guide
records the result.

**Scope of the write.** niwa writes one boolean under one key per instance:
the resolved path of a directory niwa created. It never writes
`enableAllProjectMcpServers`, never edits a settings file, and never writes a
key for a parent or the workspace root. `claudetrust` is the only code that
opens the file, so a reviewer can check this in one place.

**Not damaging the file.** The file holds login-adjacent state and every
project's history. niwa never writes without Claude Code's lock, never writes
what it couldn't parse or what has an unexpected shape, keeps every other
value byte-for-byte, writes through a 0600 temp file, fsync and rename, keeps
the existing mode, creates a new file as 0600, and follows a symlinked config
to its target (resolved under the lock) rather than replacing the link. A
stale lock is removed only after 10 seconds, Claude Code's own threshold, and
only with a non-recursive remove after a second stat, so niwa can't take a
lock a live Claude Code process holds or delete anything placed inside it.

**Not trusting a tampered record.** Removal is driven by the record in
instance state, which anyone controlling the instance can edit. Every record
is validated before use: known `mode` and `source`, an absolute `config_file`
named `.claude.json` that is a regular file, and a `key` equal to the
canonical path of the instance being destroyed or applied. A record that fails
is left alone with a warning. The worst a crafted record can then do is remove
the trust flag for its own instance's path, which destroying the instance
removes anyway.

**Not removing what isn't niwa's.** A hand-made entry is never recorded, so
it's never removed. This also fixes the current reap behavior, which deletes
any entry at a reclaimed path.

**Secrets.** Overlay `vault://` references resolve through the overlay's own
providers, so an overlay can't read secrets through providers only the base
declares, and a dropped colliding overlay server is never resolved. Resolved
header and env values land in the generated `.mcp.json` and Codex config as
they do for base servers today, written 0600 by the existing generators.
Nothing in this feature logs them; the trust write handles only paths and a
boolean.

**Residual risks.** Trust written to the file niwa's environment names won't
help a session started under a different `CLAUDE_CONFIG_DIR`, and a session
still running in a destroyed instance can write an untrusted entry back.
Neither grants anything.

## Consequences

### Positive

- A default-mode session at a fresh instance root gets no trust dialog and no
  MCP approval prompt when the workspace opts in, including for servers
  declared in a private overlay.
- Hand-made trust entries survive reap, which they don't today.
- Review-instance trust gets the correct key, config file and lock for free.

### Negative

- niwa now edits Claude Code's user config for long-lived instances, so a
  Claude Code change to the key, file or lock breaks the feature silently.
- Teardown reads instance state on every route, adding a small read to
  destroy.
- Review-instance trust entries written by older niwa versions aren't removed
  by reap any more.

### Mitigations

- The live check is recorded with the Claude Code version it passed on, and
  the guide's "still seeing prompts" check (`claude doctor`, `claude mcp list`)
  lets a developer find a broken assumption without reading code.
- Old review entries name directories that no longer exist, so they grant
  nothing.
