---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-authority-grants.md
milestone: "authority-grants"
issue_count: 4
---

# PLAN: authority-grants

## Status

Active

## Scope Summary

Build authority levels and named grants from
`docs/designs/DESIGN-authority-grants.md`: the `internal/authority` package
and its three config layers with `niwa authority policy`; dispatch-time
resolution, refusal and the pre-launch record; read-back through `niwa
authority show`, `niwa authority decide` and `niwa list --json`; and the
guide with tests of the read contract its later consumers depend on.
Requirement numbers below are the PRD's (`docs/prds/PRD-authority-grants.md`).
Enforcement doesn't change anywhere in this plan.

## Decomposition Strategy

Horizontal. The design sequences four phases with stable interfaces between
them: the pure package every caller shares, the record's storage and the
dispatch path that writes it, the read commands that consume the record, and
the guide and contract tests that pin the read format. Dispatch's refusal plus
its record is already the first end-to-end slice, so a separate stub skeleton
would only add a throwaway layer. Everything lands in one pull request; no
split applies, since there's one repository, no merge gate, and none of the
pieces is useful to an operator without the others.

## Issue Outlines

### Issue 1: feat(authority): declare authority levels, grants and roles and inspect the policy

**Goal**: Add the pure `internal/authority` package, the base, overlay and machine config layers with validation and warnings, `niwa config set/unset authority-level|authority-ceiling`, and `niwa authority policy`, with no change to dispatch.

**Acceptance Criteria**:

_Package `internal/authority` (pure, no I/O, no niwa imports)_

- [ ] `ParseLevel` and `ParseAuthority` trim whitespace, match exact lowercase, return nil for a blank level, and reject anything outside `autonomous|supervised|strict` and `merge-pr|close-issue|cut-release`; table tests pass, including a mixed-case and a padded input.
- [ ] `Resolve` returns narrowest-wins level and grant ceiling across workspace, overlay, machine and request, with a source for each; level-ceiling ties resolve to `machine`, then `overlay`, then `workspace`; grant-ceiling source is `machine` if the machine list removed anything, else `overlay` if the overlay list did, else `workspace`; with no level anywhere the level is `autonomous` (R13).
- [ ] `Resolve` returns `*LevelWiderError`, `*OutsideCeilingError` (listing every offending authority with its route, `--grant` or `role <name>`) and `*UnknownValueError` for a wider level, an authority outside the ceiling, and an unknown authority or role; tests assert each error type and its message content.
- [ ] `Resolve` de-duplicates roles and sources, lists roles in name order, orders grants in R3 order with `dispatch` before roles by name, and refuses a request whose encoded record, with the identity fields at their maximum lengths, exceeds `authority.MaxRecordBytes` (64 KiB), the same constant the reader uses.
- [ ] `EffectivePolicy` returns the level ceiling and grant ceiling with their sources; an absent base ceiling yields an empty grant ceiling (nothing grantable); base ceiling of all three, overlay `["merge-pr","close-issue"]` and machine `["merge-pr","cut-release"]` give `merge-pr` from `machine`.
- [ ] `Record.MarshalJSON` writes the one document the file stores and `show --json` prints, with keys in this order: `schema`, `record` (always `true`), `dispatch_id`, `parent_session_id`, `parent_dispatch_id`, `level`, `level_source`, `level_ceiling`, `level_ceiling_source`, `grant_ceiling`, `grant_ceiling_source`, `roles`, `grant`; a test compares the bytes against a golden document, and `ParseRecord` round-trips it.
- [ ] `ParseRecord` rejects with a test each: an unknown level, an unknown authority, a bad schema, a `dispatch_id` or `parent_dispatch_id` that is neither a UUID nor empty, a `parent_session_id` failing the session-id pattern, a role not matching `^[a-z][a-z0-9-]{0,63}$`, a source that is not `dispatch` or `role:<valid name>`, a grant out of R3 order, and a field containing control characters.
- [ ] `Decide` with no declaration returns decider/author_exclusion `session`/`off` at `autonomous`, `dispatcher`/`on` at `supervised` and `person`/`on` at `strict`, with `Source` equal to the level's source, and appends ` (no dispatch record)` to `Source` when `noRecord` is true; `ValidPoint` accepts `a`, `merge-2` and a 64-character name and rejects `Merge_Now`, `2merge`, `-merge`, an empty name and a 65-character name.
- [ ] Declaration-seam table rows, each asserting decider, author_exclusion and source: a declaration of `dispatcher`/`off` for `merge-own-pr` at `autonomous` (source `workspace`) returns `dispatcher`/`off`/`declaration`; the same declaration at `strict` (source `flag`) returns `person`/`on`/`flag`; at `supervised` (source `workspace`) returns `dispatcher`/`on`/`workspace`; a declaration of `person`/`on` at `autonomous` returns `person`/`on`/`declaration`; a declaration for `merge-own-pr` leaves `accept-prd` at `session`/`off` under `autonomous`.
- [ ] The session-id and dispatch-id patterns (`parentSessionPattern`, `dispatchIDPattern`) are moved from `internal/cli` into `internal/authority` and `internal/cli` (including `dispatch_lineage.go` and `list.go`) uses the moved copy, with no other definition left in `internal/cli`; existing tests still pass.

_Config layers and validation_

- [ ] `[authority]` in the base `workspace.toml` decodes `level`, `ceiling` and `roles.<name>.authorities`; the overlay `[authority]` decodes `level` and `ceiling` and a tombstone `roles`; `MergeWorkspaceOverlay` leaves authority untouched, and a test shows base, overlay and machine reach `Resolve` as three separate inputs.
- [ ] An overlay can only narrow: an overlay `level` wider than base, or an overlay `ceiling` entry outside the base ceiling, does not widen policy, and `niwa authority policy` reports the narrowest value with source `overlay` where the overlay narrowed it.
- [ ] An overlay `[authority.roles]` is reported once, by `config.AuthorityWarnings` (not also by `TombstoneWarnings`), as having no effect, and does not change any resolved value.
- [ ] An unknown key inside a base or overlay `[authority]` table (for example `ceilng`) and an unknown `authority_*` key under `[global]` reach `ValidateConfig` through each layer's `UnknownKeys` (the overlay and global parsers decode with `toml.Decode` to see them) and are reported as invalid values, and the base parser's generic "unknown config field" warning no longer lists them, so each is reported exactly once.
- [ ] A wrong-typed value (`ceiling = "merge-pr"`, `level = 3`, `authority_ceiling = "x"`) reaches `ValidateConfig` as an invalid value rather than failing the config decode: `niwa apply` completes with a warning, and `niwa authority policy` exits 1 naming the key.
- [ ] A misspelt key inside a role table (`[authority.roles.coordinator] authoritys = [...]`) is an invalid value naming the role and the key, checked by `ValidateConfig` itself since a role table decoded as untyped values never reports undecoded keys.
- [ ] An overlay `[authority.roles.x]` table prints its no-effect warning on `niwa apply`, `niwa dispatch` and `niwa authority policy` (dispatch and `policy` print `config.AuthorityWarnings`' lines for the tombstone to stderr and carry on).
- [ ] `workspace.ResolveAuthorityOverlay` follows provisioning's branches with a test each: `NoOverlay` in the root's `.niwa/instance.json` gives no overlay; a recorded `overlay_url` with a present clone gives that overlay, and with a missing or unparseable clone returns an error naming `niwa apply`; no recorded URL and a convention clone present locally gives the convention overlay; no recorded URL and no convention clone gives no overlay.
- [ ] `ValidateConfig` errors name the key, the (quoted, escaped) value and the accepted values; a value containing control bytes does not reach the output unescaped.
- [ ] Empty-list round trip: `authority_ceiling = []` in `[global]` and an overlay `ceiling = []` decode to a present, empty list (nothing grantable, not unset); `niwa config set authority-ceiling none`, then an unrelated `niwa config set`, leaves the file with `authority_ceiling` still empty and `policy` still showing an empty machine ceiling.
- [ ] `GlobalSettings` accessors for `authority_level` and `authority_ceiling` return an error on an invalid value, mirroring `DispatchPermissionMode()`.
- [ ] `niwa apply` against a config with an invalid authority value, an unknown `[authority]` key or an overlay `roles` table exits 0, prints the warning from `config.AuthorityWarnings` beside the existing tombstone warnings, and completes the apply (warns, never fails; R11).
- [ ] `niwa authority policy` against the same invalid config exits 1 with a message naming the key, value and accepted values.

_Commands_

- [ ] `niwa config set authority-level <level>` and `authority-ceiling <a,b,...>` validate through the same parser as the authority commands, trim and de-duplicate list entries, accept `none` as the empty list, and write `[global]` keys; `niwa config unset` removes them.
- [ ] `niwa config set authority-level bogus` and `authority-ceiling merge-pr,nope` exit non-zero with an accepted-values message and leave the config file byte-identical.
- [ ] `niwa authority policy` prints, for the base, overlay and machine layers, each declared value (`not set` where absent), the resolved level ceiling and grant ceiling with their sources, the roles, and what a dispatch with no authority flags would resolve to; `--json` prints a valid JSON document with the same content. Exit 0 on success, 1 on config error or outside a workspace, 2 on usage.
- [ ] With base ceiling of all three, machine `authority_ceiling = ["merge-pr"]` and a role naming all three, `policy` marks `close-issue` and `cut-release` on that role as outside the effective ceiling.
- [ ] Policy output shows `overlay` as the source when only the overlay narrowed, `machine` when the machine narrowed, `workspace` otherwise, and `built-in` when no layer sets a level.
- [ ] With base `ceiling = ["merge-pr", "close-issue"]` and a role `coordinator` naming all three authorities, `niwa authority policy` marks `cut-release` on that role as outside the effective ceiling (text and `--json`).
- [ ] `niwa authority --help` lists the `policy` subcommand (later issues add `show` and `decide` to the same group).
- [ ] No dispatch behaviour change: no new dispatch flag, step or argv change; the existing dispatch tests pass unchanged.
- [ ] Unit tests for the authority package run with no cobra and no filesystem; `go vet ./...` and `go test ./...` pass.

_Deliverables downstream work depends on_

- [ ] Must deliver: `authority.Resolve`, `Policy`, `Request`, and `Record` with its JSON encoding (required by <<ISSUE:2>>)
- [ ] Must deliver: `authority.ParseRecord` with the field validation above (required by <<ISSUE:2>>)
- [ ] Must deliver: the moved session-id and dispatch-id patterns in `internal/authority` (required by <<ISSUE:2>>)
- [ ] Must deliver: `authority.ValidateConfig` and `config.AuthorityWarnings` (required by <<ISSUE:2>>)
- [ ] Must deliver: the `GlobalSettings`, base and overlay config accessors and a helper that builds `authority.Policy` from the three layers (required by <<ISSUE:2>>)
- [ ] Must deliver: `workspace.ResolveAuthorityOverlay` (required by <<ISSUE:2>>)
- [ ] Must deliver: `authority.Decide`, `Declarations`, `NoDeclarations`, `ValidPoint` and `EffectivePolicy` (required by <<ISSUE:3>>)

**Dependencies**: None

**Type**: code

### Issue 2: feat(dispatch): resolve, refuse and record a dispatch's authority before launch

**Goal**: Make `niwa dispatch` resolve, refuse and record a dispatch's authority before the worker launches. Add `WriteAuthorityRecord` and `ReadAuthorityRecord` (safe open) and the stat-only instance walk `FindInstance` in `internal/workspace`, and give dispatch the `--level`, `--grant` and `--role` flags. Dispatch refuses at the new step (2e) before any side effect, prints the resolution line after step (4), records `parent_dispatch_id` from the dispatcher's own record, re-checks the policy against what the instance was built from and writes the record at step (8a), and adds the `niwa.authority.level` and `niwa.authority.grant` telemetry attributes (grant joined with `/`). Covers R12 to R19, R22, R28 to R30 and Decision 4.

**Acceptance Criteria**:

_Record I/O (`internal/workspace/authority_record.go`)_

- [ ] `WriteAuthorityRecord(instancePath, record)` creates `<instance>/.niwa/authority.json` with mode 0600 via a temp file opened `O_EXCL|O_NOFOLLOW`, synced, then renamed; a test asserts the mode is `0600` and that no temp file remains after success or failure.
- [ ] `ReadAuthorityRecord` round-trips a written record to an equal `authority.Record`, and the stored bytes equal `Record.MarshalJSON`'s golden document from Issue 1 (`record: true` included).
- [ ] Reader fixtures each return an error naming a failure class and never echo file contents or the record path: a FIFO at the record path returns promptly without blocking (test fails by timeout if the open blocks); a symlink at the final path component is refused; a file over 64 KiB is refused; a regular file with control characters in `parent_session_id`, a role or a source is refused; an unknown level, unknown authority, wrong schema, bad UUID, or grant out of R3 order is refused.
- [ ] A missing record returns a distinct "no record" result, not an error, so callers can tell absent from invalid.

_Instance walk (`internal/workspace/cwd_classify.go`)_

- [ ] `FindInstance(dir)` returns the outermost valid instance containing `dir` using stat calls only (a test seam fails on any config load, network or process call); `ClassifyCwd`'s behaviour is unchanged and its existing tests pass unmodified.
- [ ] Planted-marker fixture: a `.niwa` marker directory placed inside a cloned repo within an instance does not become the result; `FindInstance` returns the real instance, and returns "none" when the marker sits outside any workspace root's enumerated instances.
- [ ] Symlinked-start fixture: starting from a symlink that resolves into an instance returns that instance; a symlink resolving outside any instance returns "none".
- [ ] `FindInstance` returns the instance for the instance root, a repo directory inside it and a niwa worktree of it.

_Refusal at (2e), all before the prompt, sweep and provisioning_

- [ ] `--level` wider than the level ceiling exits 1 with the R13 message naming the level asked for, how it was asked (`--level`), and the ceiling with its source; the test pins the exact stderr text.
- [ ] `--grant` or `--role` that expands to an authority outside the grant ceiling exits 1 with the R14 message naming each offending authority, its route (`--grant` or `role <name>`), and the ceiling with its source; exact stderr text pinned, one line per offender.
- [ ] An unknown authority, an unknown role and an invalid machine value (`authority_level`, `authority_ceiling`) each exit 1 with a message naming the key or flag, the quoted and escaped value, and the accepted values; a value containing control bytes prints escaped.
- [ ] A workspace config that fails to load (including a missing `workspace.toml`) exits 1 at (2e) naming the file; an overlay the workspace root's `.niwa/instance.json` records but whose clone is missing or unparseable exits 1 naming `niwa apply`; neither is treated as "no narrowing".
- [ ] An unreadable or invalid dispatcher record (dispatcher cwd inside a dispatched instance) exits 1 and does not record an empty parent.
- [ ] A request whose encoded record would exceed the 64 KiB reader cap is refused at (2e), not at (8a).
- [ ] For every refusal case above, a functional test asserts: exit code 1; no `niwa dispatch: authority level` line on stderr; no new instance directory under the workspace root; no authority record and no session mapping or pending marker written; every pre-existing instance is still present (the sweep did not run); stdin was never read and no prompt was shown (a prompt-capture fake records zero calls); the fake agent was not launched.

_Re-check at (8a), after provisioning (PRD R16a)_

- [ ] The (8a) re-check: when provisioning applies a convention overlay (not recorded in the root state before the dispatch) whose `[authority] ceiling` leaves out a requested authority, or refreshes a base config whose level is narrower than the one (2e) resolved, the dispatch has already printed its `niwa dispatch: authority level` line, then exits 1 with a message naming the setting that changed, the rollback removes the new instance, no record or mapping exists, and the fake agent records zero launches. When the overlay removes `cut-release` from the ceiling of a dispatch that asked only for `merge-pr`, the dispatch launches and its record's `grant_ceiling` omits `cut-release` with source `overlay`. When the policy the instance was built from matches (2e)'s, the record equals (2e)'s resolution. When the instance state records no overlay URL and no convention clone exists, but (2e) used an overlay, the re-check uses (2e)'s overlay; when a convention clone now exists, the re-check uses it.
- [ ] A no-flag dispatch at workspace `strict` whose provisioning refreshes the base config to `autonomous` exits 1 at (8a) (a level that widened also refuses), with the same assertions as the row above: its line already printed, a message naming the setting that changed, the new instance removed, no record or mapping, and zero launches. A no-flag dispatch at workspace `strict` whose provisioning brings in an overlay that is also `strict` launches, and its record keeps `level_source: workspace` (the source R19's line printed) while `level_ceiling_source` is `overlay`. A dispatch whose ceiling only widened during provisioning launches and records the wider ceiling with an unchanged grant.
- [ ] `ResolveAuthorityOverlay` at (2e) finds the convention overlay through the same effective workspace name and registry entry provisioning uses: a test with a renamed workspace derives the same overlay URL both ways.

_Resolution line, parent link and record_

- [ ] On success, one stderr line in the R19 shape, for example `niwa dispatch: authority level strict from flag; grant merge-pr (dispatch)`, prints after the dispatch id is minted at (4) and before the sweep at (5); a dispatch that fails the binary preflight or prompt capture prints no such line (test asserts absence).
- [ ] With no `--level`, `--grant` or `--role` and no `[authority]` config, the line reads exactly `niwa dispatch: authority level autonomous from built-in; grant none`, and the record is still written.
- [ ] Dispatch-level resolution scenarios, each asserting the recorded level and source: workspace `strict`, no flag, records `strict` from `workspace`; workspace `autonomous` with `--level supervised` records `supervised` from `flag`, and a second dispatch with no flag records `autonomous` from `workspace`; workspace `supervised` with `--level supervised` records `supervised` from `flag` (tie to the flag); machine `strict`, workspace `autonomous`, no flag records `strict` from `machine`, and with `--level supervised` exits 1 naming `machine` as the ceiling's source.
- [ ] Grant scenarios: a workspace with no `ceiling` key refuses `--grant merge-pr` naming the empty ceiling and the `[authority] ceiling` key; a workspace holding R8a's example dispatched with `--role coordinator` records `merge-pr`, `close-issue` and `cut-release`, each from `role:coordinator`; `--grant close-issue --role coordinator` records `close-issue` once with sources `dispatch`, `role:coordinator`.
- [ ] The record written at (8a) contains the minted `dispatch_id`, `level` and `level_source`, both ceilings with sources, `roles` and `grant` with sources in the order `dispatch` first then roles by name; the test compares the whole file against an expected document for a request mixing `--grant`, `--role` and an overlay-narrowed ceiling.
- [ ] `parent_dispatch_id` cases: dispatcher cwd inside a dispatched instance with a valid record gives that record's `dispatch_id`; dispatcher cwd outside any dispatched instance gives an empty string; dispatcher instance with no record (older niwa) gives an empty string and the dispatch proceeds; dispatcher record present but invalid refuses the dispatch. A Codex dispatcher whose environment carries an inherited `parent_session_id` of its own dispatcher still records the `parent_dispatch_id` of the instance it runs in, found from its working directory, not from the session id variable.
- [ ] A failed record write at (8a) (for example the `.niwa` directory made read-only by the test) exits non-zero, runs the rollback so the instance directory is gone, and the fake agent records zero launches.
- [ ] The record survives `niwa apply` of the instance, a later dispatch of another session, a change to the machine config, and a resume through `claude attach` or `codex resume`, each byte for byte; `niwa reset`, `niwa destroy` and `niwa reap` of the instance remove it along with the instance.

_Unchanged behavior and telemetry_

- [ ] `--level strict` with `--permission-mode bypassPermissions` launches in bypass mode and records `level: strict`; a workspace `bypass` posture with no `[authority]` records `autonomous` and an empty grant, neither implying a level or a grant.
- [ ] With no authority settings, a Claude dispatch's argv and settings document are byte-identical to the previous release's golden output apart from the two `niwa.authority.*` lineage attributes; a Codex dispatch's argv and launch environment are byte-identical with no exception.
- [ ] A Claude dispatch's `OTEL_RESOURCE_ATTRIBUTES`, in both the launch environment and the settings document's `env`, carries `niwa.authority.level=<level>` and `niwa.authority.grant=merge-pr/close-issue` (slash-joined, R3 order) for a dispatch granted those two, beside the existing lineage attributes, which all remain present with their values; a grant of three authorities and an empty grant (`none`) both pass the composer's value pattern without dropping any existing attribute, and a pre-existing `team=x` entry in the dispatcher's own variable survives.
- [ ] A Codex dispatch granted `merge-pr` has neither `niwa.authority.level` nor `niwa.authority.grant` anywhere in its launch environment, and its record carries the same level and grant a Claude dispatch with the same flags records.
- [ ] The instance's hook files and every settings document a dispatch writes are byte-identical between a dispatch with `--role coordinator` and one with no grant, and between a dispatch with `--level strict` and one with no flags, apart from the two lineage attributes (R28).

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code

### Issue 3: feat(authority): read back a dispatch's grant with show, decide and niwa list

**Goal**: Add `niwa authority show` and `niwa authority decide <point>` and the `authority` object in `niwa list --json`, so a dispatched session, an operator and a later gate can read a dispatch's grant. Both commands select the dispatch through one `selectDispatch` function that applies R21's order, accept `-C <dir>` anchoring, answer "no record" without holding anything, print a `record=present|none` line from `decide`, run under a roughly two-second watchdog, and keep stdout clean on every failure.

**Acceptance Criteria**:

_Self-read as the first command (functional tests with a fake agent that runs `niwa authority show --json` first and writes its output to a marker file)_

- [ ] For both a Claude fake and a Codex fake, the first-command output is the dispatched record (matching `dispatch_id`, `level`, and `grant`), for detached and for foreground dispatch.
- [ ] The same output is obtained when the fake's working directory is the instance root, a cloned repo inside it, and a niwa worktree of the instance.
- [ ] With the worker's session-id environment variable set to the dispatcher's session id (the inherited Codex case), `show` still returns the worker's own record, not the dispatcher's.
- [ ] After a resume (new session id, same instance), `show` returns the same record as before the resume.
- [ ] The read works before the session mapping exists (no mapping file present at first command).

_Selector precedence and combinations (table test over `selectDispatch` plus command-level tests)_

- [ ] Precedence pairs that can both apply: `--session <a>` run with the working directory in instance `b` reports `a`; `--instance <a>` run with the working directory in instance `b` reports `a`; with no selector, the working directory in instance `b` and the session-id variable naming session `a` reports `b`. The selector pairs that can't both apply are covered by the usage-error criterion below.
- [ ] The session-id variable is never read: with it naming a session `a` that has a record, `show` with no selector from the workspace root, `show` from a directory outside any instance, and `show -C <dir>` with `<dir>` in no instance each print `dispatch record: none`; a Codex fake that changes directory out of its instance before running `show` gets `dispatch record: none`, not its dispatcher's record.
- [ ] Any two of `--session`, `--instance`, `-C` together exit 2 with a usage error and empty stdout, for `show` and `decide`.
- [ ] `-C ""`, a relative `-C`, and a nonexistent `-C` each exit 1 with empty stdout and never fall back to the working directory (run from inside a dispatched instance to prove it).
- [ ] `show -C <dir>` output is byte-identical to `show` run from `<dir>` when `<dir>` is inside an instance, for `--json` and plain output.
- [ ] `--instance <name>` naming a missing instance exits 1; an invalid name is rejected before any path is joined.
- [ ] `--session <id>` with no mapping prints the no-record answer and exits 0.
- [ ] A session mapping whose `instance_path` is not an instance directly under the workspace root (outside the workspace, or nested) is refused with exit 1 and empty stdout.
- [ ] A marker directory planted inside a cloned repo is not taken as the instance: `show` from there resolves the real enclosing instance.

_No-record document_

- [ ] In a workspace with no record for the selected dispatch, `show --json` exits 0 and prints a document with `schema` `niwa.authority.record/v1`, `"record": false`, `"grant": []`, a `level`, and a `level_source` that is a plain layer name (no suffix).
- [ ] Plain `show` with no record prints the line `dispatch record: none`.
- [ ] The no-record `level` equals the level a no-flag dispatch would resolve to in that workspace (tested with `autonomous` default and a workspace `supervised`).

_Failure behaviour_

- [ ] A malformed record, an unknown-schema record, an unknown level or authority, an oversized record (over 64 KiB), a FIFO, a symlink and an unreadable file each make `show` and `decide` exit 1 with empty stdout (stderr names a failure class only, no path and no file contents).
- [ ] With `--json`, stdout carries only the document on success, and nothing on every failure.
- [ ] A watchdog test (stalled read) exits 1 within about two seconds of start with empty stdout.
- [ ] `show` against an existing record found from the working directory or `-C` discovers no workspace config and writes nothing (filesystem unchanged before and after).
- [ ] Inside a dispatched instance with a valid record, after the base config gains `[authority] level = "Strict"` and the machine config gains `authority_level = "loose"`, `show --json` and `decide merge-own-pr` still exit 0 and report the recorded dispatch; from the workspace root (no record) with the same configs, both exit 1 naming the bad key (R11).

_`decide`_

- [ ] Output is exactly the lines `point=`, `level=`, `decider=`, `author_exclusion=`, `source=`, `record=present|none`, and the test asserts each exact line.
- [ ] `source=` is exactly one of `flag`, `machine`, `overlay`, `workspace`, `built-in`, or `declaration` with a record, and that value plus ` (no dispatch record)` without one; `record=present` pairs with the former and `record=none` with the latter.
- [ ] For records resolved from each layer, `decide merge-own-pr` prints exactly `source=flag`, `source=machine`, `source=workspace` and `source=built-in` respectively; with no record in a `strict` workspace it prints `level=strict`, `source=workspace (no dispatch record)` and `record=none`.
- [ ] A bad point name (not matching `^[a-z][a-z0-9-]{0,63}$`) exits 2 and is checked before any selector or filesystem work, so a bad point with a bad `-C` still exits 2.
- [ ] At `autonomous` it prints `decider=session` and `author_exclusion=off`, at `supervised` `decider=dispatcher` and `author_exclusion=on`, at `strict` `decider=person` and `author_exclusion=on`, each exiting 0.
- [ ] A `strict` record that also holds `merge-pr` still prints `decider=person` for the merge-pr point (R4: holding does not override the level).
- [ ] Decide-driven fake-agent marker test, both ends, in an `autonomous` workspace: the fake runs `decide merge-own-pr` and writes `settled` to its marker when it exits 0 with `decider=session`, and `waiting-for-person` otherwise. Dispatched with no flag the marker reads `settled`; dispatched with `--level strict` it reads `waiting-for-person`.

_`niwa list --json`_

- [ ] Each instance with a valid record carries an `authority` object equal to the `show --json` document for that instance (same fields and values).
- [ ] An instance with no record has no `authority` key at all; an instance with a malformed or unreadable record carries an `authority` object holding only an `error` naming a failure class, with no path or contents, while the other instances still list and the command exits 0.
- [ ] Editing only an instance's record file (changing its level) changes what both `niwa authority show` and `niwa list --json` report for that instance, showing both read the one copy.
- [ ] A Codex dispatch granted `merge-pr` appears in `niwa list --json` with an `authority` object naming `merge-pr`.
- [ ] `niwa authority --help` lists `policy`, `show` and `decide`, and `show --help` and `decide --help` list `-C`, `--session`, `--instance` and `--json`.

_Cost and isolation_

- [ ] Benchmark: with 250 session mappings in the workspace, the median of ten runs of the built binary for `show` (and for `decide`) is under 200 ms.
- [ ] A test that counts session-mapping reads shows exactly one read for a `--session` lookup and zero for every other selection.
- [ ] A seam test replaces the network and process-start seams with fakes that fail the test if called; `show`, `decide` and `list` pass it.

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code

### Issue 4: docs(authority): document the grant contract and test its consumers

**Goal**: Document the authority-grant contract in `docs/guides/authority-grants.md` and test the way outside readers consume it. The guide carries the reference held check and settle check as shell functions that return rather than exit. A CI contract test runs those functions inside a stub gate against real `show` and `decide` output and against each failure, and checks that an already-decided deny survives. A CI job runs the authority tests with no koto on `PATH`. The CLAUDE.md contributor-guides entry points at the guide. The one-time live check with a real Claude Code session is recorded in the implementing pull request.

**Acceptance Criteria**:

_Guide content_

- [ ] `docs/guides/authority-grants.md` exists and contains each of these strings: `[authority]`, `authority_level`, `authority_ceiling`, `--level`, `--grant`, `--role`, `merge-pr`, `close-issue`, `cut-release`, `niwa.authority.record/v1`, `dispatch record: none`, `decider=session`, and R8a's example block (the `[authority]` table with `level = "autonomous"`, the three-entry `ceiling` and the `coordinator` role), verbatim.
- [ ] The guide documents the `[authority]` keys, the machine keys, the flags, what each named authority covers, the record's JSON document and schema version, the selection order of R21, `-C`, the no-record answer, the held rule, the settle rule, how level and grant combine (R4), and that `level_source` describes resolution at dispatch and can differ from `level_ceiling_source` in a valid record (R13, R16a).
- [ ] The guide states, where it introduces the stderr line and the telemetry attributes, that the grant is "recorded, not enforced".
- [ ] The guide states that a gate only lifts a verdict it already reached on its own text match, on a held authority plus an exact `decider=session` line, and never denies on "not held".
- [ ] The guide states that a gate must bound the lookup time itself and, where it can't (no `timeout` and no background-job fallback), skips the lookup and keeps its deny.
- [ ] The guide states that `parent_dispatch_id` isn't a security boundary, and that a session can edit its own record.
- [ ] The guide tells callers that dispatch refuses an unknown flag, that a caller unable to pass `--level` must not dispatch without it, and that one unable to pass `--grant` or `--role` may.
- [ ] The guide tells hook authors to anchor on the hook's own absolute `$0` with `-C`, never on the payload `cwd`.
- [ ] The guide's reference held check (`niwa_holds`) and its exact-line `decider=session` check are shell functions that use `return` and contain no `exit`.

_Contract test_

- [ ] A CI test extracts the functions from the guide itself (not a copy) and runs them inside a stub gate that has already decided a deny and lifts it only on held plus `decider=session`.
- [ ] Against real `show --json` and `decide` output from a dispatched fake instance holding `merge-pr`, the held check returns 0 for `merge-pr`, returns non-zero for `close-issue`, and the stub gate lifts its deny only at `autonomous`.
- [ ] Each of these cases makes the held check return non-zero and leaves the stub gate's deny in place: no niwa on `PATH`, an old niwa that prints help with exit 0, an unknown schema, a malformed document, the no-record document (`"record": false`), and a lookup that times out.
- [ ] The timeout case finishes within the bound the stub gate sets and the deny survives; the test fails if the stub gate hangs.
- [ ] At `strict`, `decide` output makes the settle check return non-zero and the deny survives even when `merge-pr` is held.
- [ ] A `decide` run with no record (`record=none`) does not settle the point.

_CI_

- [ ] A CI job runs the authority unit and functional tests with no koto binary on `PATH` and passes. The job fails if `koto` resolves on `PATH`.
- [ ] The contract test runs in CI on every pull request, and a CI run with the guide's held check altered to `exit` fails.

_CLAUDE.md_

- [ ] The repo's CLAUDE.md "Contributor Guides" section has an entry linking `docs/guides/authority-grants.md` with a one-line description.

_Live check (recorded in the implementing pull request, run once before merge)_

- [ ] The pull request description records a live check with a real Claude Code session in a workspace holding R8a's example, with a session dispatched with `--grant merge-pr`, briefed to run `niwa authority show` and `niwa authority decide merge-own-pr`.
- [ ] The recorded transcript excerpts show, at `autonomous`, `decider=session` followed by the session proceeding with the merge step it was briefed on.
- [ ] They show, at `strict`, `decider=person` followed by the session stopping without that step.
- [ ] They show the session's own `niwa authority show` output naming `merge-pr` from `dispatch`.
- [ ] They show the refusal text for a dispatch made from inside that session asking for `--grant cut-release` past a narrowed ceiling (overlay or machine ceiling set to `merge-pr`), naming `cut-release` and the ceiling, with nothing launched.
- [ ] The live-check excerpts appear only in the pull request, not in a committed file, and the guide and the CLAUDE.md entry reference only durable paths.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: docs

## Implementation Sequence

Critical path: Issue 1, then Issue 2, then Issue 3, then Issue 4, all in one
pull request.

- Issue 1 has no dependencies and gives every later issue its rules, record
  type and config inputs.
- Issue 2 needs Issue 1's `Resolve`, `Record`, `ParseRecord` and config
  accessors, and delivers the record reader, the instance walk and real
  records.
- Issue 3 needs Issue 2's reader, walk and records.
- Issue 4 documents and contract-tests the document and line formats Issue 3
  fixes, and records the one-time live check.

There's no parallel work: each issue consumes the interfaces the one before it
delivers.

Outside this plan: making shirabe's coordinator run `niwa dispatch` from
inside its own instance, so never-wider finds its record, belongs with or
after shirabe's rewrite of the coordinator's dispatch path, not as a
separate edit. Until then a coordinator that dispatches from the workspace
root records an empty `parent_dispatch_id` and gets no cap, which is
harmless while nothing enforces one.
