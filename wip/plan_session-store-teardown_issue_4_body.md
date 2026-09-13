---
complexity: testable
complexity_rationale: One function's write path changes plus a one-line early return, but the rule has to hold for three callers and its proof is the forced concurrency tests already written in Issue 1.
---

## Goal

Make `SaveState` write `instance.json` through a uniquely named temp file renamed into place, take the config-directory lock (and stop calling `MkdirAll`) when the target `.niwa` is a refreshed config directory, and stop `saveWorkspaceRootDisclosures` writing root state after a failed read.

## Acceptance Criteria

- [ ] `SaveState` writes the marshaled state to a uniquely named temp file in the same `.niwa/` directory as the target, sets that file to mode 0644, and renames it over `instance.json`; a reader running concurrently with the write sees either the whole previous file or the whole new one, never an empty or partial file (PRD R22).
- [ ] After a first write and after an overwrite, `instance.json` is mode 0644 (today's mode); no temp file is left in `.niwa/` on success, and when marshaling or the temp write fails an existing `instance.json` is left byte-identical.
- [ ] `SaveState` routes the write through `configdir.Mutate` on `<dir>/.niwa` when that directory is a refreshed config directory - a sibling `<dir>/.niwa.lock` exists, or `<dir>` carries `.niwa/workspace.toml` (a workspace root, which includes a single-instance root) - and on that path does not `MkdirAll` the `.niwa` directory, returning an error instead of recreating it when it is absent.
- [ ] For a target that is not a refreshed config directory (a child instance under a multi-instance root), `SaveState` keeps today's unlocked path including `MkdirAll`, creates no lock file beside that instance's `.niwa`, and the `Create` and `Apply` instance-state writes (`internal/workspace/apply.go`) produce the same result as before.
- [ ] All three refreshed-config-directory callers get the locked atomic write with no per-caller change: `saveWorkspaceRootDisclosures`, the `niwa init` root write in `internal/cli/init.go`, and the single-instance-layout `Apply` write where the instance root is the workspace root. A test covers each of the three writing while an exclusive hold on that directory's lock is in place, and asserts each waits rather than writing through.
- [ ] Forced with the `snapshot-carried-over` hook: a `SaveState` into a refreshed config directory that starts while a refresh is between carry-over and swap completes successfully, does not recreate `.niwa` (the refresh's swap still succeeds), and afterwards the directory holds that refresh's `workspace.toml` and provenance marker alongside an `instance.json` that parses (PRD R14).
- [ ] `saveWorkspaceRootDisclosures` returns without writing when its `existing` state is nil and a root `instance.json` exists at the target; it still writes when `existing` is nil and no root `instance.json` exists, and when `existing` is non-nil, so first-time disclosure recording is unchanged (PRD R23).
- [ ] The Phase 1 root-state tests turn green on this commit: the forced test where one `niwa create`'s root `instance.json` read lands while another is writing root disclosures never leaves the file without `ephemeral_session_mode` or `overlay_url` and the file always parses; and with the root read forced to fail by the `root-state-read` hook, a `niwa create` that would record a new notice leaves the root `instance.json` byte-identical while a later `niwa create` whose read succeeds records that notice.
- [ ] No `SaveState` call happens inside an existing `configdir.Mutate` or `Read` callback, so the nested-acquisition detector never fires: a single `niwa dispatch` against a local-source workspace, which refreshes twice and writes a mapping, completes with the default 30-second bound (PRD R19).
- [ ] `go test -race ./...` passes on Linux, `GOOS=windows go build ./...` succeeds, and `go.mod` gains no module.

## Dependencies

Blocked by <<ISSUE:2>>

## Downstream Dependencies

None directly. The root-state race tests added in Issue 1 turn green here, and Issue 7's guide text about `.niwa.lock` covers the lock this write now takes.
