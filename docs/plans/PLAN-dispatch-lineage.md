---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-dispatch-lineage.md
milestone: "Dispatch lineage"
issue_count: 9
---

# PLAN: Dispatch lineage

## Status

Active

## Scope Summary

Every `niwa dispatch` writes a durable, dispatch-keyed lineage entry in `<root>/.niwa-lineage/`, holding its parent links with how niwa came by each (a worker dispatched from the workspace root linking to its coordinator as `mapped`) and the person it acts for when one is declared; a new `niwa lineage` command group reads the chain after the instances are gone, and the authority record carries the same parent session source and principal while an instance lives. Watch review sessions are deferred.

## Decomposition Strategy

**Walking skeleton.** Issue 1 writes a stub entry at dispatch and reads it back through `niwa lineage show`, so the store, the dispatch hook and the read path integrate before any of them thickens. Issues 2 to 7 each add one layer the design names: the link resolver, the principal, completion with the session id, store hardening, the authority record fields, and the full read surface. Issue 8 covers the PRD's criteria end to end with real dispatches, reaps and refreshes, and issue 9 documents the record and its trust boundary. Everything lands in one pull request: the repository declares no delivery preference, so the consolidated default applies, no hard constraint forces a split, and no layer before the read surface is usable alone.

## Issue Outlines

### Issue 1: feat(lineage): write a lineage entry at dispatch and show it

**Goal**: A dispatch writes `<root>/.niwa-lineage/<dispatch id>.json` beside its authority record and `niwa lineage show <dispatch id>` prints it, end to end, with every link and the principal still `none`.

**Acceptance Criteria**:
- [ ] `internal/lineage` defines `Entry`, `Link` and `Principal` with the PRD's R1 fields, writes the known members in a fixed order with `schema: niwa.lineage.entry/v1`, and parses them back keeping members it doesn't know: a `niwa.lineage.entry/vN` document with N > 1 parses to a distinct later-version result carrying only its dispatch id and schema, and any other schema is refused.
- [ ] `internal/workspace/lineage_store.go` adds `CreateLineageEntry` (refuses when the final name exists, writes a dot-prefixed temporary at 0600 in a 0700 `<root>/.niwa-lineage` directory, renames it into place), `ReadLineageEntry` and `RemoveLineageEntry`.
- [ ] `niwa dispatch` creates the entry right after the authority record at step (8a); a failed create refuses the dispatch and leaves no instance.
- [ ] The rollback armed at step (7) removes the entry only when this dispatch created it: with an entry already present for the minted dispatch id (through the id seam), the dispatch refuses and the existing entry's bytes are unchanged.
- [ ] A dispatch refused at authority resolution, and one rolled back during provisioning, leave no entry (unit tests through the existing dispatch seams).
- [ ] `niwa lineage show <dispatch id>` takes the id as a positional argument, prints the entry's fields, and `--json` prints `{"schema": "niwa.lineage.chain/v1", "chain": [<entry>], "stop": "root", "matched": 1, "matches": [<dispatch id>]}`.
- [ ] A functional test dispatches once, runs `niwa apply`, and reads the entry back through `niwa lineage show`; no file appears under `<root>/.niwa/` for it.

**Dependencies**: None

**Type**: code

### Issue 2: feat(lineage): resolve parent links with their sources

**Goal**: `lineage.Resolve` applies the PRD's parent-session and parent-dispatch source tables to what dispatch reads at step (2e), including the `mapped` link a worker dispatched from the workspace root gets to its coordinator.

**Acceptance Criteria**:
- [ ] `dispatchParent` also returns the working directory's `FoundInstance`, and `resolveDispatchAuthority` builds `lineage.Inputs` from it, the session variable, `--parent-from`, the parent record, the instance's newest session mapping (looked up by the cleaned instance root, only when `--parent-from` was not given), and, when the working directory is in no instance, the dispatch id of the mapping keyed by the variable's value.
- [ ] A table test covers every parent-session row: variable unset and malformed (`none`), `--parent-from` (`handed`), no instance (`handed`), an instance with no mapping (`handed`), a matching newest mapping (`observed`), and a different one (`unverified`, with `mapped_session_id`).
- [ ] A table test covers every parent-dispatch row: the working directory's instance record (`observed`), `--parent-from` (`handed`), a root dispatch whose variable names a mapping with a dispatch id (`mapped`), and otherwise (`none`); an unreadable parent record still refuses the dispatch.
- [ ] For each source row above, the dispatch exits 0 and writes an entry carrying that source: none of `handed`, `mapped`, `unverified` or `none` refuses.
- [ ] The session variable is read once, at step (2e), and the `niwa.parent.session.id` telemetry attribute is unchanged.
- [ ] The skeleton's functional test now shows a dispatch from the workspace root recording `parent_session.source` `handed` and `parent_dispatch.source` `none`, a dispatch from inside that instance recording both `observed`, and a dispatch from the workspace root whose variable names the first dispatch's session recording `parent_dispatch.source` `mapped`.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code

### Issue 3: feat(lineage): record the person a dispatch acts for

**Goal**: Every entry names its principal with a source and a trust value: inherited down the tree, or the person the machine's config declares, and otherwise nobody.

**Acceptance Criteria**:
- [ ] The personal niwa config's `[global]` table accepts `principal`, valid at 1 to 128 letters, digits, `.`, `_`, `-`, `@` or `+`; an invalid value refuses dispatch at step (2a) naming the key and the file; a `principal` key in the workspace config or an overlay has no effect.
- [ ] `lineage.Resolve` implements the principal rows: `inherited` from a readable parent entry, `declared`, and `none` with an empty name; with nothing declared the principal is empty whatever the OS account.
- [ ] Inherited trust is `observed` only when the parent dispatch link is `observed`, the parent session link is not `unverified`, and the parent's principal trust is `observed`; a principal inherited through `--parent-from`, through a `mapped` link, or past an `unverified` session link reads `handed`, and a grandchild of a `handed` principal reads `handed`.
- [ ] A parent link whose entry is missing, unreadable or of a later version falls through to the declared principal; a readable parent with an empty principal is inherited as empty, even with `principal` declared.
- [ ] With `principal` declared, `GH_TOKEN` set and a stub `gh` reporting a different login first on `PATH`, the entry names the declared value.
- [ ] The worker's `OTEL_RESOURCE_ATTRIBUTES` carries no principal.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:5>>

**Type**: code

### Issue 4: feat(lineage): complete entries with the worker session id

**Goal**: The entry gains the worker's session id as soon as step (10) captures it, by a rewrite that keeps every other stored member, and otherwise exists exactly when the dispatch keeps its instance.

**Acceptance Criteria**:
- [ ] `CompleteLineageEntry` reads the stored members, refuses an entry that already has a session id or is of a later version, inserts `session_id`, and writes every other member back unchanged through a temporary file and a rename; it never re-marshals the `Entry` struct.
- [ ] An entry holding a member this niwa doesn't know keeps it, byte for byte, after completion.
- [ ] Dispatch calls completion after capture and before the session mapping write; a foreground dispatch whose session mapping write fails keeps its entry with the session id recorded.
- [ ] A detached dispatch whose capture fails leaves no entry; a foreground one whose turn ends with no session record keeps an entry without `session_id`.
- [ ] With completion made to fail through a test seam, dispatch prints one warning naming the entry, exits 0, and the entry has no `session_id`.
- [ ] An entry's bytes are identical before and after a reap, a second dispatch and a configuration refresh.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:5>>

**Type**: code

### Issue 5: fix(lineage): harden store reads and file selection

**Goal**: A planted or edited file in `.niwa-lineage/` costs only that entry, files are chosen by name, type and size, and the store's directory stays private.

**Acceptance Criteria**:
- [ ] Every read opens with `O_NOFOLLOW|O_NONBLOCK`, requires a regular file and reads at most 8 KiB; a planted symlink, FIFO, directory or oversized file reads as a per-entry error without hanging.
- [ ] An entry whose `dispatch_id` differs from its file name is unreadable, and every known field (agent, origin, `written_at`, principal, sources) is re-validated on read, with unknown name values for origin, source and trust accepted.
- [ ] `ListLineageEntries` selects files by name (`<dispatch id>.json`, dot-prefixed temporaries skipped) and reports a selected file that isn't a regular file within the cap, without parsing to decide; it returns one result per selected file (an entry, a later-version marker or a per-file error).
- [ ] A `.niwa-lineage` path that is a symlink or not a directory refuses writes; an existing directory at 0755 is 0700 after the first write in a process.
- [ ] An entry with every field at its maximum length is at most 4 KiB.

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code

### Issue 6: feat(authority): carry parent session source and principal on the authority record

**Goal**: `niwa authority show` reports the same parent session source and principal as the lineage entry while the instance lives, without any advisory field able to void a grant.

**Acceptance Criteria**:
- [ ] The record gains `parent_session_source` and `principal` after `parent_dispatch_source`, registered in `v1Optional` with defaults `""` and `null`, written from the same `Resolve` result as the entry.
- [ ] A wrong type or a value outside its pattern in either field reads as unrecorded, and the record's grant still reads.
- [ ] `niwa authority show` prints both fields, or `unrecorded` for a record written before them; `--json` carries them when valid and omits them otherwise.
- [ ] While an instance exists, `niwa authority show --json` and `niwa lineage show --json <dispatch id>` agree on the parent session source and principal; `parent_dispatch_source` `cwd` or `anchor` matches the entry's `observed` or `handed`, and a `mapped` entry has `none` there.
- [ ] The held answer and `niwa authority decide` are unchanged with the new fields present.

**Dependencies**: Blocked by <<ISSUE:3>>

**Type**: code

### Issue 7: feat(cli): add niwa lineage show and list

**Goal**: People and tools read any entry's chain up to its root, and every entry in the workspace, with the PRD's selection, stop and trust rules.

**Acceptance Criteria**:
- [ ] `show` selects by positional dispatch id, `--session`, `--instance`, the working directory and `-C`; several matches start the chain from the latest `written_at`, ties to the lower dispatch id, print `<n> entries matched; showing the latest` with the other ids, and the JSON carries `"matched": <n>` and `"matches"` with every matching id; an instance-name fallback prints `selected by instance name`.
- [ ] Two selectors exit 2; an empty, relative or nonexistent `-C` exits 1; outside a workspace exits 1 naming it; no match prints `no lineage entry` and exits 1, including with no store, from the workspace root with no selector, and when the authority record's dispatch id has no entry.
- [ ] The walk follows `parent_dispatch` ids of any source and stops with the PRD's texts and JSON values for `root`, `not-on-record`, `unreadable`, `later-version`, `cycle` and `depth` (64 levels counting the selected entry); a selected later-version entry exits 0 and an unreadable selected entry exits 1.
- [ ] Each level prints `directed by <session id>` when its parent session id is set.
- [ ] In `parent_session.source`, `parent_dispatch.source` and `principal.trust`, values other than `observed` (`mapped` and unknown values included) print with `(not observed)` except `none` on a link; JSON prints values as stored; `principal.source` prints unannotated.
- [ ] `list` orders by `written_at` newest first, ties by dispatch id, one line per entry, lists an unreadable entry as its id and error, exits 0, and lists nothing with exit 0 when no store exists; `list --json` prints `niwa.lineage.list/v1`.

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:4>>, <<ISSUE:5>>

**Type**: code

### Issue 8: test(lineage): cover the trees, durability and offline paths end to end

**Goal**: Functional tests show the PRD's tree, durability and constraint criteria hold with real dispatches, reaps and refreshes.

**Acceptance Criteria**:
- [ ] With `principal` declared, dispatching A from the workspace root and B from inside A's instance, then reaping both, `niwa lineage show --session <B>` prints the two levels, sources and principal the PRD's first criterion lists; a third level C prints three levels nearest first.
- [ ] A coordinator K dispatches worker W from the workspace root; after both are reaped, `show` on W prints W's `mapped` link to K, K's level, stop `root`, and a principal trust of `handed`.
- [ ] An entry survives each of: mapped reap, stored-mapping reap, backstop reap, `niwa destroy`, `niwa destroy --keep-session`, `niwa reset`, a failing on-reap command, one failing until its mapping is dropped, and `niwa apply` refreshing the configuration directory; two dispatches run while a third provisions through a refresh all keep their entries; `niwa destroy --workspace` removes the store.
- [ ] A dispatch from a session with no mapping in this workspace (an imported one) records parent session `handed`.
- [ ] With `HTTPS_PROXY` and `HTTP_PROXY` pointed at a closed local port, dispatch writes its entry and `show` and `list` succeed.
- [ ] The lineage functional tests pass with no workflow engine on `PATH`.
- [ ] A workspace that never dispatched has no store, and the existing dispatch, authority and telemetry tests pass unchanged.

**Dependencies**: Blocked by <<ISSUE:6>>, <<ISSUE:7>>

**Type**: code

### Issue 9: docs(guides): document the lineage record and its trust boundary

**Goal**: Operators and tool authors learn from the guides what the lineage record holds, how far each link can be trusted, how to declare the person, and how long entries last.

**Acceptance Criteria**:
- [ ] `docs/guides/dispatch-lineage.md` gains a section on the lineage record stating: a first-level parent session is `handed`; `--parent-from` makes both links `handed`; a worker dispatched from the workspace root links to its coordinator as `mapped`, not observed; `unverified` is expected after a resume and for a Codex worker's dispatches, and the parent dispatch link is the one to follow; a resumed Claude session with a new id keeps its record under the original id; only `observed` counts as observed, unknown values included; same-user processes can edit the store, so entries are advisory; entries last until the workspace is destroyed.
- [ ] The same section shows how to set `[global] principal`, says the principal is empty until it is set, that a worker can read and pass on its own principal, that lineage never travels with a session export or import, that `show --session` and `--instance` may match more than one entry (an older entry for a reused id among them), and that `.niwa-lineage/` should stay out of anything published from the workspace root.
- [ ] The section documents `niwa lineage show` (with its positional dispatch id, unlike `niwa authority show`) and `niwa lineage list`, their selectors, exit codes and JSON schemas, and that a reader treats an unknown `stop` value as not `root`.
- [ ] `docs/guides/authority-grants.md` notes the two new authority record fields and their unrecorded reading.

**Dependencies**: Blocked by <<ISSUE:6>>, <<ISSUE:7>>

**Type**: docs

## Implementation Sequence

The skeleton (issue 1) comes first. Store hardening (issue 5) and the link resolver (issue 2) can then proceed side by side; the principal (issue 3) needs both, and completion with the session id (issue 4) needs the hardened store. Once issues 3 and 4 are in, the authority record fields (issue 6) and the read surface (issue 7) are independent of each other. The end-to-end tests (issue 8) and the guides (issue 9) close the work and can run together.

Critical path: issue 1, then 2, 3, 7 and 8; the path through 5 and 4 to 7 is the same length, so issue 5 should start as soon as the skeleton lands.
