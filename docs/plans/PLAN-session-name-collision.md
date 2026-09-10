---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-session-name-collision.md
milestone: "Unique session names for dispatched workers"
issue_count: 4
tracking_level: none
---

# PLAN: Unique session names for dispatched workers

## Status

Active

Single-pr plan with tracking level `none`: no GitHub issues or milestone are created,
and the work items live in the Issue Outlines below. The plan was authored directly at
Active, as the unified PLAN lifecycle requires when activation creates no remote
artifacts.

## Scope Summary

Make the Claude Code session name `niwa dispatch` forwards unique per dispatch, per
`docs/designs/DESIGN-session-name-collision.md`. The dispatch reuses its instance
directory's random token as `<slug>-<token>`, forwards that name only when the agent
declares a display-name flag, prints it as `  session name: <name>`, records it on the
session mapping, and `niwa list` shows it in text and JSON. `niwa watch`,
`niwa create --name`, unnamed dispatches, Codex, and sessions dispatched before the change
stay unchanged. Everything lands in one pull request.

## Decomposition Strategy

Horizontal, one issue per step of the design's Implementation Approach. The change
refactors existing code along an ordered path with a stable seam between each step, so a
walking skeleton would add nothing. Issue 1 pins today's behavior first: `niwa watch`
naming, and a legacy mapping's output. That gives issues 2 and 3 a baseline to prove
unchanged. Issue 2 is the dispatch change and introduces the `session_name` mapping field.
Issue 3 surfaces that field in `niwa list`. Issue 4 carries the documentation coverage the
design's `user_visible_surface: true` calls for. Issues 3 and 4 write disjoint files and
can proceed in parallel once issue 2 lands.

## Issue Outlines

### Issue 1: test(cli): add watch seams and pin current naming behavior

**Goal**: Add the minimal test seam `niwa watch` lacks and pin today's watch naming output and legacy-session output, so the dispatch naming change can prove it moved neither.

**Context**: The dispatch naming change edits `runDispatch`, the session mapping and `niwa list`, all of which sit next to code that must not move. `niwa watch` shares `buildDispatchPassthrough` with dispatch. It forwards its slug as the display name at staging (`stageReview`) and its recorded handle at resume (`continueReview`), and it has no test that watches what it launches. Both functions call `watch.FetchPRHead` directly, and no watch test stubs `dispatchLaunch`. Mappings written before the change also have to keep listing, reporting, reaping and hinting exactly as they do today. This issue captures both baselines from the current code before any dispatch code changes. A later regression then shows up as a failing test rather than a quiet change.

**Acceptance Criteria**:
- [ ] `internal/cli/watch.go` gains one package-level function variable for the PR-head fetch, initialized to `watch.FetchPRHead`, and both call sites use it: the fetch in `continueReview` (currently `watch.go:549`) and the one in `stageReview` (currently `watch.go:815`). Production behavior, error wrapping (`fetching PR head: %w`) and function signatures are unchanged. `stageReview` and `continueReview` keep their concrete `*github.APIClient` parameter. Tests construct `&github.APIClient{HTTPClient: ..., BaseURL: srv.URL}` against an `httptest` server that serves `GET /repos/<owner>/<repo>/pulls/<n>` (the path `GetPullHead` builds in `internal/github/pulls.go`), so no client interface is introduced.
- [ ] A new characterization test calls `stageReview` for a fixed PR (for example `acme/widget#7`). It stubs the new fetch variable (writes nothing to the network), `provisionInstanceFunc` (returns a temp instance directory), `dispatchLaunch` (records the `launchRequest`), `watchCapture`, and `ensureInstanceTrustedFunc`/`removeInstanceTrustFunc`, with HOME pointed at a temp directory. It asserts that the recorded `Passthrough` holds the display-name flag followed by exactly one value, the sanitized slug of `watch-acme-widget-7` written as a string literal in the test and not computed through `sanitizeInstanceSlug` or `buildDispatchPassthrough`. It also asserts that the staged record saved under the workspace root has that same value as `Handle`, and that stdout contains a line equal to `niwa watch: staged review for acme/widget#7 (handle <literal>)` followed by exactly the `reviewWritePosture(...)` suffix the plan under test produces (empty on the unsandboxed plan, so the line is exact there). The unsandboxed plan is covered. The sandboxed plan is also covered if it can be reached without host sandbox support; otherwise the test documents why it's skipped.
- [ ] A new characterization test calls `continueReview` with a `watch.StagedRecord` whose `Handle`, `SessionID` (a valid lowercase UUID) and `ShortID` are fixed literals. Its setup makes both liveness checks pass: a job-state entry written under `$HOME/.claude/jobs` (the `defaultJobsDir` path) whose `sessionId` is the record's id and whose `cwd` is the instance path, reusing the `writeJobStateFile` helper in `internal/cli/continuation_test.go`. It stubs `stopSessionFunc` to succeed, stubs the fetch variable and `dispatchLaunch`, and uses the same `httptest` GitHub server. It asserts that the recorded `Passthrough` carries the display-name flag followed by the record's `Handle` literal, then `--resume <SessionID>`, and that stdout contains a line equal to `niwa watch: continued review for acme/widget#7 (handle <Handle literal>)` followed by exactly the `reviewWritePosture(...)` suffix for that plan.
- [ ] Both watch tests compare against literal expected strings, not values derived from production helpers, so a change inside `buildDispatchPassthrough` or the sanitizer that altered what watch forwards would fail them.
- [ ] A legacy mapping fixture is checked in as raw JSON bytes for `.niwa/sessions/<uuid>.json`, in today's `workspace.SessionMapping` schema. It has no `session_name` key, uses `agent` Claude, `origin` `dispatch`, `ephemeral` true, a recorded `handle`, and an instance named `<config>+review-<8hex>` (for example `test-ws+review-4e33acfa`). The test copies these bytes onto disk directly rather than building them through `workspace.WriteSessionMapping`, so a later field added to the struct can't change the fixture.
- [ ] A new golden test sets up a workspace holding that fixture and its instance, then captures the following output from the current code. It covers `niwa list` in text mode (`runList` with `listJSON=false`, including the `  resume:` line) and in JSON mode (`listJSON=true`), and the `niwa status` summary view (`showSummaryView`) plus the detail view for the legacy instance (`showDetailView`). It captures `niwa reap` twice: `runReap` stdout together with the spared-instance report for a live job entry (session kept), and the reclaim path for a dead one (`reapWorkspace` returns 1 and the instance is in the `stubDestroyAll` record). It also captures the resume, logs and stop hint lines `reentryHints(claudeLaunchSpec(), <fixture handle>, <instance path>)` produces for the fixture. Temp-directory paths are normalized to fixed placeholder tokens before comparison.
- [ ] The golden files live under `internal/cli/testdata/` (a new directory) and are regenerated only when an opt-in environment variable is set, following the pattern in `internal/workspace/characterization_test.go` (`NIWA_UPDATE_CHARACTERIZATION`). A missing golden fails the test with a message naming the variable. The goldens are generated from this branch before any dispatch or list code changes, and checked in.
- [ ] The captured text output contains no `session name:` line and the captured JSON contains no `session_name` key. The golden test asserts both directly, in addition to the byte comparison.
- [ ] `go test ./internal/cli/...` and `go vet ./...` pass. Existing tests pass unchanged, including the `TestList_*` tests in `internal/cli/list_resume_test.go`, the `TestReap_*` tests in `internal/cli/reap_test.go`, `TestPruneStagedRecords`, the continuation tests, `TestDispatchPathNamesNoAgentConstant` and `TestDispatchPathNamesNoAgentLiteral`.
- [ ] Must deliver: the watch staging and resume characterization tests and the legacy mapping golden, merged into the branch and passing against unchanged dispatch code, with literal expectations that issue 2 can run as-is (required by <<ISSUE:2>>).

**Dependencies**: None

**Complexity**: testable
**Type**: code
**Files**: `internal/cli/watch.go`, `internal/cli/watch_characterization_test.go`, `internal/cli/legacy_session_characterization_test.go`, `internal/cli/testdata/legacy_session/mapping.json`, `internal/cli/testdata/legacy_session/list.txt`, `internal/cli/testdata/legacy_session/list.json`, `internal/cli/testdata/legacy_session/status.txt`, `internal/cli/testdata/legacy_session/reap.txt`, `internal/cli/testdata/legacy_session/hints.txt`

### Issue 2: feat(dispatch): forward and report a unique session name

**Goal**: Make every named `niwa dispatch` forward, record and print a session name of the form `<slug>-<token>` that reuses the instance directory's random token, so two dispatches with the same `--name` no longer share a peer address.

**Context**: Today `runDispatch` in `internal/cli/dispatch.go` calls `dispatchNameSuffix(slug)`, which reads `crypto/rand` directly and spends the token only on the instance directory, then passes the bare slug to `buildDispatchPassthrough` as the display name. This issue adds a replaceable random source, splits the token from the instance prefix and the session name, forwards the suffixed name only when the agent declares a display-name flag, records it on `workspace.SessionMapping`, and prints it as ` session name: <name>` after the ` instance:` line. `buildDispatchPassthrough`, `sanitizeInstanceSlug`, instance naming and `niwa watch` stay unchanged; the watch characterization tests and legacy golden from issue 1 prove it.

**Acceptance Criteria**:
_Seams and naming functions in internal/cli/dispatch.go_

- [ ] `var dispatchRandReader io.Reader = rand.Reader` is added, and `func newDispatchToken() (string, error)` reads exactly 4 bytes from it with `io.ReadFull` and returns 8 lowercase hex digits.
- [ ] `func dispatchInstancePrefix(slug, token string) string` returns `slug + "-" + token`, or `"-" + token` for an empty slug, the same strings `dispatchNameSuffix` returns today.
- [ ] `dispatchNameSuffix(slug)` is kept as a thin wrapper that calls `newDispatchToken` then `dispatchInstancePrefix`; `stageReview` in `internal/cli/watch.go` still calls it and is not edited. `runDispatch` no longer calls it.
- [ ] `func dispatchSessionName(slug, token string) string` returns `slug + "-" + token`, and returns `""` when the slug is empty. It does not call `dispatchInstancePrefix`. A direct unit test asserts `dispatchSessionName("", token) == ""`.
- [ ] `const dispatchSessionNamePattern = "^[a-z0-9](?:[a-z0-9_]{0,38}[a-z0-9])?-[0-9a-f]{8}$"` and a compiled regexp beside it are defined once in `dispatch.go`, with a comment saying the token is shared with the instance name. Dispatch tests use this regexp rather than restating the pattern.
- [ ] *(review)* `dispatchSessionName` takes only the slug and token, the token comes only from `dispatchRandReader`, and the change adds no network call, subprocess, directory enumeration, or read of other sessions' records anywhere on the dispatch path.

_Wiring in runDispatch_

- [ ] At the naming step `runDispatch` calls `newDispatchToken` once and derives both the instance prefix and the session name from that one token. A random-read error returns the existing `niwa: error: generating instance name` error before `reapOpportunistically` and `provisionInstanceFunc` run.
- [ ] `forwardedName` is set to `dispatchSessionName(slug, token)` only when `spec.Flags.DisplayName != ""`, and is passed to `buildDispatchPassthrough` in the slot that carries `slug` today. `buildDispatchPassthrough`'s signature and its "flag and value both non-empty" guard are unchanged.
- [ ] *(review)* The gate keys on the declared flag spelling, not on an agent constant or name. `TestDispatchPathNamesNoAgentConstant` and `TestDispatchPathNamesNoAgentLiteral` in `internal/cli/dispatch_layout_test.go` pass unchanged.
- [ ] The session mapping the dispatch writes sets `SessionName: forwardedName`. The success block prints `  session name: <forwardedName>` on the line directly after `  instance: <path>` and before the first hint line, only when `forwardedName` is non-empty.

_Name shape tests_

- [ ] With `dispatchRandReader` stubbed to return `0xab` for every byte, `niwa dispatch "<task>" --name review` forwards, in the recorded `launchRequest.Passthrough`, the element immediately after the display-name flag equal to exactly `review-abababab`, the instance name is `<config>+review-abababab`, and the mapping file the dispatch writes under `.niwa/sessions/<uuid>.json` decodes with `SessionName` equal to `review-abababab`.
- [ ] Two dispatches with `--name review` and different stubbed bytes forward different names, both matching `dispatchSessionNamePattern`.
- [ ] A test calls `newDispatchToken` and `dispatchSessionName` together from 64 goroutines with the real random source under `go test -race`, and gets 64 distinct names and no race report.
- [ ] With `dispatchRandReader` stubbed to return an error, the dispatch exits non-zero, `provisionInstanceFunc` is never called (no instance directory exists), `dispatchLaunch` is never called, and stdout contains no `session name:` line.
- [ ] For `--name` inputs `Review`, `auth layer`, `café!!`, and a value that sanitizes to exactly 40 runes, the forwarded name equals `sanitizeInstanceSlug(input) + "-" + token`, the 40-rune slug appears in full, stripping the final `-` and what follows yields `sanitizeInstanceSlug(input)`, and the value matches `dispatchSessionNamePattern`. A one-character slug also matches the pattern.
- [ ] `TestDispatch_Name_SlugInInstanceAndSession` in `internal/cli/dispatch_test.go` is updated to assert the forwarded value is `my_thing-<token>` where `<token>` equals the instance name's trailing 8 hex, and its instance-name assertions (`^test-ws\+my_thing-[0-9a-f]{8}$`, `isDispatchInstanceName`) still pass.

_Report line tests_

- [ ] For a named Claude dispatch, the `  session name: <name>` line sits directly after the `  instance:` line and `<name>` is byte-identical to the argv element after the display-name flag.
- [ ] The report line appears in all five success exits: default flow with `dispatchAttach` stubbed to succeed, `--detach`, `dispatchAttach` stubbed to fail, a stubbed spec that declares a display-name flag and launches in the foreground, and a stubbed spec that declares a display-name flag with `ResumeDuringTurn` false.
- [ ] The report line is absent when `dispatchLaunch`, `dispatchCapture`, or the mapping write is stubbed to fail, in both detached and foreground modes (extending or mirroring `TestDispatch_Rollback_LaunchFailure`, `TestDispatch_Rollback_CaptureFailure`, `TestDispatch_Rollback_MappingWriteFailure`, `TestDispatchForegroundCaptureFailureKeepsTheWork` and `TestDispatchForegroundMappingFailureKeepsTheWork`).
- [ ] The first stdout line still equals `Dispatched session <uuid>` with nothing after the UUID; `dispatchedInstancePath` in `internal/cli/dispatch_contract_test.go` and the tests that use it still pass; and with a stubbed agent whose binary is not `claude` and a slug without `claude` in it, stdout contains no `claude ` substring.

_Paths that must not forward a name_

- [ ] A pin test asserts that the argv `dispatchAttach` runs and the command `sessionResumeCommand` in `internal/cli/list.go` returns for a mapping with a recorded `SessionName` contain no display-name flag.
- [ ] A dispatch with no `--name` and one with `--name "!!!"` forward no display-name flag, print no report line, and write a mapping with empty `SessionName`; `TestDispatch_NoName_NoSlugNoNameFlag` and `TestDispatch_NameSanitizesEmpty_FallsBack` pass unchanged.
- [ ] A dispatch with `--name review` against Codex's declared launch spec (from `agentplan`, whose `Flags.DisplayName` is empty) forwards no display-name flag, prints no report line, and records an empty `SessionName`. No Codex binary is needed.

_Unchanged neighbors_

- [ ] `niwa create --name "Auth Layer"` still produces `<config>+auth_layer`; dispatch instance directories still match `\+[a-z0-9_]*-[0-9a-f]{8}$`; `internal/cli/reap_backstop_test.go` and `internal/cli/dispatch_wiring_remotecontrol_test.go` pass unchanged; and the watch characterization tests and legacy-mapping golden added by issue 1 pass unchanged.

_Mapping and help text_

- [ ] `workspace.SessionMapping` in `internal/workspace/session_map.go` gains `SessionName string` with `json:"session_name,omitempty"` and a comment saying it holds the display name the dispatch forwarded, empty when none was. A mapping written without it decodes to `""` and re-encodes without the key.
- [ ] The `--name` flag help registered in `internal/cli/dispatch.go` contains the word `suffix` and says the session name is the slug plus the instance's random suffix; the `buildDispatchPassthrough` doc comment no longer contains "carries the same display name embedded in the instance directory", and a test or grep check asserts both.
- [ ] `go test ./...` and `go vet ./...` pass in the niwa repo.

_Downstream contracts_

- [ ] Must deliver: `workspace.SessionMapping.SessionName` (`json:"session_name,omitempty"`), set only when a name was forwarded, and the package-level `dispatchSessionNamePattern` constant plus its compiled regexp in `internal/cli/dispatch.go`, so `niwa list` can join and validate the recorded name (required by <<ISSUE:3>>)
- [ ] Must deliver: the final forwarded-name format `<slug>-<8hex>` sharing the instance token, the gating on the agent's declared display-name flag, and the exact `  session name: <name>` report line placed after `  instance:`, so the design doc and `/dispatch` skill text can describe them (required by <<ISSUE:4>>)

**Dependencies**: Blocked by <<ISSUE:1>>

**Complexity**: testable
**Type**: code
**Files**: `internal/cli/dispatch.go`, `internal/workspace/session_map.go`, `internal/cli/dispatch_test.go`, `internal/cli/dispatch_sessionname_test.go`, `internal/cli/dispatch_contract_test.go`, `internal/cli/dispatch_launchmode_test.go`

### Issue 3: feat(list): show the recorded session name in niwa list

**Goal**: Make `niwa list` show, in both text and `--json` output, the session name a dispatch recorded on its session mapping, and nothing for instances that recorded none.

**Context**: Issue 2 records the forwarded name on `SessionMapping.SessionName` and defines `dispatchSessionNamePattern`. This issue carries that value through the join `niwa list` already runs for `KeepAlive`, so a developer who closed the dispatch terminal can still recover the name. The name comes only from what was recorded, never from the instance directory name. Mapping files are writable by same-user workers, so the value is display-only and has to pass the pattern check before it's shown.

**Acceptance Criteria**:
- [ ] `workspace.InstanceRecord` in `internal/workspace/state.go` gains `SessionName string` with tag `json:"session_name,omitempty"`, declared after `KeepAlive` so the existing JSON keys keep their order. A doc comment says the CLI layer fills it, the same way it fills `KeepAlive`, and that it's empty when no name was recorded. `EnumerateInstanceRecords` leaves it empty.
- [ ] `annotateFromSessionMappings` in `internal/cli/list.go` collects `m.SessionName` by `m.InstancePath` and sets it on the matching record, but only when the value matches the regexp issue 2 compiles from the `dispatchSessionNamePattern` constant. A value that fails the pattern counts as absent.
- [ ] When several mappings point at the same instance path, the name comes from the mapping with the latest `Created` time, not from whichever mapping `ListSessionMappings` returns last (it returns them sorted by `SessionID`). A unit test seeds two mappings for one instance with different `Created` times and names, giving the NEWER mapping the `SessionID` that sorts FIRST, and asserts the newer mapping's name wins. A second case reverses the assignment (newer mapping sorts last) and asserts the same winner, so a first-seen-wins or last-seen-wins join fails at least one case. The latest-`Created` mapping is chosen first and its `SessionName` is then checked against the pattern; if it fails, the instance shows no session name (an older mapping's name is not used as a fallback). The existing `  resume:` line keeps its current mapping selection; this issue doesn't change it, and one-mapping instances (the normal dispatch case) show both lines from the same mapping.
- [ ] Text output of `runList` prints `  session name: <name>` (two leading spaces) on the line directly after the instance name line (including when that line carries the ` (keep-alive)` marker) and before that instance's `  resume:` line, only when `r.SessionName` is non-empty. A test in `internal/cli/list_resume_test.go` seeds a Claude-agent mapping with `SessionName: "review-4e33acfa"` and a handle, then asserts the three lines appear consecutively in that order.
- [ ] `niwa list --json` carries `"session_name": "<name>"` on that instance's record. A test decodes the JSON output and asserts the field is byte-identical to the recorded value.
- [ ] After a named Claude dispatch run through the real `runDispatch` with a stubbed random source (the issue 2 test seams), both the `niwa list` text line and the `--json` `session_name` value are byte-identical to the value forwarded after the display-name flag in the recorded launch argv.
- [ ] Mappings with an empty `SessionName`, standing in for an unnamed dispatch, a Codex dispatch and a mapping with no `session_name` key, produce no `session name:` line in text output and no `session_name` key in `--json` output. The issue 1 legacy-mapping golden (the `niwa list` text and `--json` output for a pre-change mapping) passes without its expected output changing.
- [ ] A mapping whose `session_name` fails the pattern shows as absent: no `session name:` line and no `session_name` key. Cases cover a value containing an ANSI escape sequence (for example `"review-4e33acfa\x1b[31m"`), a value with no hex suffix (`"review"`), and a value whose suffix isn't exactly 8 lowercase hex digits.
- [ ] `TestList_JSONShapeIsUnchanged` in `internal/cli/list_resume_test.go` passes, its seeded mapping still lists without a `session_name` key, and its doc comment now says the only key added to the documented record shape is the optional `session_name`, present only when a dispatch recorded a name.
- [ ] `listCmd.Long` in `internal/cli/list.go` describes the `session name:` line and the `session_name` JSON field. The `--json` flag description (registered in `init` in `list.go`) lists `session_name` among the optional keys, next to `keep_alive`. A new test asserts `listCmd.Long` contains both `session name:` and `session_name`, and the `json` flag's `Usage` contains `session_name`.
- [ ] `go test ./...` and `go vet ./...` pass, including the existing `TestList_*` tests in `internal/cli/list_resume_test.go` without changes to their assertions.

**Dependencies**: Blocked by <<ISSUE:2>>

**Complexity**: testable
**Type**: code
**Files**: `internal/workspace/state.go`, `internal/cli/list.go`, `internal/cli/list_resume_test.go`

### Issue 4: docs(dispatch): describe suffixed session names

**Goal**: Correct the instance-dispatch design and the `/dispatch` root skill so they describe the suffixed session name `<slug>-<token>` and tell the coordinating agent to relay the reported session name.

**Context**: Issue 2 changes `niwa dispatch` so it forwards `<slug>-<8hex>` (the same token as the instance directory) as the session name and prints ` session name: <name>` right after the ` instance:` line. `docs/designs/current/DESIGN-instance-dispatch.md` still says the bare slug names the Claude session and that dispatch is collision-safe for shared `--name` values, and the `/dispatch` skill's `--name` text and report-back step don't mention the suffix or the new line. `niwa apply` rewrites installed root skills, so existing workspaces pick up the skill text on their next apply. The `--name` flag help, the `buildDispatchPassthrough` comment, and the `niwa list` help are handled by issues 2 and 3, not here.

**Acceptance Criteria**:
- [ ] In `docs/designs/current/DESIGN-instance-dispatch.md`, the D1 flag description (currently lines 88-89: "`--name`/`-n` for an optional session name (see D2 -- sanitized into a slug that names both the instance and the Claude session)") no longer contains the phrase "a slug that names both the instance and the Claude session" unless the same sentence goes on to name the suffix. It describes the session name as the slug plus `-` and the instance's 8-hex token. Check with line wraps ignored, since the phrase spans two lines today.
- [ ] In the same file, the D2 forwarding sentence (currently around line 167: "forwarded to the session as `claude --bg --name <slug>` so the Claude session carries a human display name in Agent View") no longer gives `<slug>` as the forwarded value. It shows the forwarded value as `<slug>-<8 random hex>` (or equivalent wording naming the suffix) and says it's the same token used in the instance name.
- [ ] In the same file, the sentence ending "concurrency stays collision-safe even when two dispatches share a `--name`" (currently line 173) either applies only to instance directories or states that session names carry the suffix too. Checked with line wraps ignored: the phrase "stays collision-safe even when two dispatches share a `--name`" appears only in a sentence that meets one of those two conditions.
- [ ] In the same file, the phrase "the slug, which names the instance and the session" (currently lines 175-176, in "`--name` (the slug, which names the instance and the session) is distinct from `--label`") no longer appears, unless the sentence goes on to name the suffix. Check with line wraps ignored.
- [ ] Nothing else in `DESIGN-instance-dispatch.md` changes meaning. The instance-name format `<config>+<slug>-<8 random hex>`, the `isDispatchInstanceName` regex `\+[a-z0-9_]*-[0-9a-f]{8}$`, and the naming table rows (for example `tsuku+auth_layer-4e33acfa`) stay as they are.
- [ ] In `internal/workspace/rootskills/dispatch/SKILL.md`, the `--name` bullet under step 3 (currently "**`--name`** gives the session a readable name in Agent View (sanitized into a slug; it also names the instance, e.g. `<config>+<slug>-<id>`).") says the session name is the slug followed by a random suffix, so two dispatches with the same `--name` get different session names. The bullet contains the word `suffix`.
- [ ] In the same file, step 4 "Report back" (currently "Tell the user: the brief path, the dispatched session id and how to reach it (`claude attach <id>` / `claude logs <id>` / `claude stop <id>`), ...") tells the agent to relay the session name from the `session name:` line in the dispatch output alongside the session id. The step contains the literal text `session name`.
- [ ] `TestMaterializeWorkspaceRoot_DispatchSkill` in `internal/workspace/root_materializer_test.go` (or a new test next to it) reads the installed `.claude/skills/dispatch/SKILL.md` that the materializer writes and asserts that the "Report back" step names the session name. The assertion is scoped to that step's text (for example, the substring between `### 4. Report back` and the next heading), not the whole file, so it fails if the report-back step drops the session name even when other sections still mention it.
- [ ] The new assertion fails when run against the current `SKILL.md` and passes after the edit.
- [ ] The existing assertions in `TestMaterializeWorkspaceRoot_DispatchSkill` (frontmatter, `name: dispatch`, `# /dispatch`, the returned paths including the skill path) still pass, and `go test ./internal/workspace/...` passes.
- [ ] No edits touch `internal/cli/dispatch.go`, `internal/cli/list.go`, or anything under `docs/guides/`.

**Dependencies**: Blocked by <<ISSUE:2>>

**Complexity**: testable
**Type**: docs
**Files**: `docs/designs/current/DESIGN-instance-dispatch.md`, `internal/workspace/rootskills/dispatch/SKILL.md`, `internal/workspace/root_materializer_test.go`

## Dependency Graph

## Implementation Sequence

Critical path: Issue 1, then Issue 2, then Issue 3 (length 3).

1. Start with Issue 1. Add the watch seams, then the characterization tests and the
   legacy-mapping golden, and confirm they pass against the current code before touching
   any production naming code.
2. Then Issue 2, the dispatch change. Issue 1's tests must still pass unchanged afterward.
3. Then Issues 3 and 4, which can proceed in parallel. They touch disjoint files, and each
   depends only on Issue 2.

Run `go test ./...` and `shirabe validate` on the changed docs before opening the pull
request.
