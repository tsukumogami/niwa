# Integration surface map: `niwa retask`

Scope: the exact code a "push a follow-up instruction to a live niwa-dispatched
session through its handle" command would touch. All paths relative to the
`niwa` repo root
(`public/niwa/.claude/worktrees/dispatch-handle-retask/`).

## 1. Dispatch capture (`internal/cli/dispatch.go`, `internal/cli/dispatch_capture.go`)

- The capture seam is a package var: `dispatchCapture = captureSessionID`
  (`internal/cli/dispatch.go:90`), overridable in tests. Production dispatch
  calls it at `internal/cli/dispatch.go:343`:
  `sessionID, shortID, err := dispatchCapture(defaultJobsDir(), instancePath, dispatchCaptureTimeout, nil, 0)`.
- `captureSessionID` (`internal/cli/dispatch_capture.go:41`) recovers **both**
  ids by polling `<jobsDir>/*/state.json` until exactly one job's `cwd` field
  equals the freshly-created, unique `instanceDir` (normalized via
  `EvalSymlinks` + `Clean`). `instanceDir` uniqueness is what makes cwd
  correlation an exact key rather than a heuristic — this is why capture is
  only ever run once, right after `dispatchLaunch`, before any second job
  could share that cwd.
- Two ids come out: `sessionID` (full UUID, the durable mapping key, and the
  id `claude --resume <id>` accepts) and `shortID` (the jobs-dir basename,
  the id `claude attach/logs/stop` accept — they reject the full UUID with
  "No job matching ..."). **Any retask command that wants to push a prompt
  into a live session needs to know which of these two ids the delivery
  mechanism (`claude attach`/`--resume`/a hypothetical `claude retask`)
  actually accepts**, mirroring the existing attach/logs/stop-vs-resume split
  documented at `dispatch.go:369-378` and `dispatch.go:385-389`.
- **Ambiguity failure mode** (`matchSessionByCwd`,
  `internal/cli/dispatch_capture.go:78-120`): if more than one `state.json`
  claims the same `cwd`, capture returns `ambiguous=true` and the caller gets
  `"dispatch: capture ambiguous: multiple jobs claim cwd %q"`
  (`dispatch_capture.go:57-58`). This is not hypothetical — it is the exact
  failure `continueReview` in `watch.go` documents as the reason its own
  re-capture after a resume returns empty ids (see §5 and #211 below): once a
  session is stopped-and-resumed in the same instance directory, the stopped
  prior job's entry and the new resumed job's entry **both** have `cwd` equal
  to the instance path, so a naive re-capture-by-cwd is permanently ambiguous
  until one of the two job entries disappears (i.e. until the human dismisses
  the stopped session in Agent View). **A retask command must not attempt a
  fresh `captureSessionID` call against an instance that might already hold a
  live session** — it should read the session id straight out of the
  existing `SessionMapping` (§2) instead of re-deriving it by cwd, precisely
  to avoid re-triggering this ambiguity.

## 2. Session mapping (`internal/workspace/session_map.go`)

`SessionMapping` (`session_map.go:49-73`) fields:

| Field | Type | Notes |
|---|---|---|
| `SessionID` | string | full UUID, validated via `sessionUUIDRe` (`:20`) |
| `InstanceName` | string | |
| `InstancePath` | string | |
| `TranscriptPath` | string | present in the struct but not populated by dispatch.go (dispatch's `mapping` literal at `dispatch.go:350-359` never sets it) |
| `Created` | time.Time | stamped `time.Now().UTC()` if zero on write (`:101-103`) |
| `Ephemeral` | bool | dispatch always sets `true` (`dispatch.go:354`) |
| `Label` | string, `omitempty` | optional human alias from `--label`; **never** used to rename the instance dir (`:56-58`) |
| `Origin` | string, `omitempty` | `"dispatch"` for CLI-dispatched sessions; hook/developer-written mappings leave it `""`; **informational only, the reaper ignores it** (`:60-65`) |
| `KeepAlive` | bool, `omitempty` | records that the dispatch armed the keep-alive self-wake; **informational only, the reaper never reads it** (`:66-72`) |

- Storage: `.niwa/sessions/<session_id>.json` under the **workspace root**
  (`sessionsDir`, `:77-79`), one file per session, keyed by the full UUID —
  not the instance. A retask command resolves a handle to a mapping by
  reading this directory (`ListSessionMappings`, `:154-184`) or by direct
  session-id lookup (`ReadSessionMapping`, `:127-141`, which validates the id
  before building the path).
- **Atomicity**: `WriteSessionMapping` (`:96-122`) does write-temp-then-
  rename (`target+".tmp"` → `os.Rename`), so a mapping write is atomic and a
  retask that needs to update the mapping (e.g. bump `Created`, or a future
  "last retasked at" field) can safely reuse this exact write path — do not
  hand-roll a second writer.
- `DeleteSessionMapping` (`:190-199`) is idempotent (missing file is not an
  error) specifically so teardown and the reaper can both call it without a
  race over who deletes first; the same idempotency assumption should hold
  for any retask-triggered mapping update.
- There is **no update/patch helper** — only full-struct
  `WriteSessionMapping`. A retask command that wants to touch one field
  (e.g. record last-retask time) must `ReadSessionMapping` then
  `WriteSessionMapping` the whole struct back; there's no partial-write
  primitive, and no file locking beyond the rename's atomicity, so a retask
  racing a `niwa reap`/dispatch teardown on the same session_id has the same
  exposure any other mapping writer already has today (nothing special to
  add, but nothing to lean on either).

## 3. Liveness and reap

- `sessionLive(jobsDir, sessionID, now)` (`internal/cli/job_state.go:93-112`)
  is the **sole** liveness rule: a session is live iff its job entry at
  `<jobsDir>/<session-id>/state.json` exists (and, if the file's own
  `sessionId` field is non-empty, it matches). It deliberately does **not**
  look at `state`/`tempo`/idle TTL — an idle-but-resumable session still
  counts as live. **A retask command should use this exact same function** to
  precondition "is there actually a session to retask" before attempting
  delivery, for consistency with `list`/`reap`.
- `instanceHasLiveJob(jobsDir, instancePath)` (`job_state.go:131-154`) is the
  mapping-independent counterpart: true if any present job's `cwd` is at or
  under `instancePath`. Used by the reaper's backstop and by `watch.go`'s
  continuation two-way cross-check (see §5). Not obviously needed for retask
  unless retask also wants to defend against an instance whose mapping is
  stale/wrong.
- `niwa reap` (`internal/cli/reap.go`) reads `EnumerateInstanceRecords` +
  `ListSessionMappings`, joins on `InstancePath`, and destroys an instance
  only when **both** `mapping.Ephemeral == true` **and**
  `!sessionLive(...)` **and** `!instanceHasLiveJob(...)`
  (`selectReapTargets`, `:106-174`). On destroy it also calls
  `workspace.DeleteSessionMapping(workspaceRoot, t.SessionID)`
  (`:201`) — **so a mapping whose session was removed (job entry gone) does
  not linger**: the very next `reap` (on-demand, or the opportunistic sweep
  `reapOpportunistically` that runs at the start of every `niwa create`/
  `dispatch`, `reap.go:399-410`) deletes both the instance directory and the
  mapping file together. **This means a retask command has a real race
  window**: between a user obtaining a handle (e.g. from `niwa list`) and
  actually delivering the retask prompt, the backing instance can be reaped
  out from under it if the session was concurrently deleted from Agent View.
  Retask must re-check `sessionLive` immediately before delivery (matching
  the "two-way liveness cross-check at EXECUTION time" pattern `watch.go`
  already uses in `continueReview`, §5) and fail closed with a clear "session
  no longer live" error rather than writing into a directory that might be
  destroyed mid-flight.
- The **backstop** (`selectBackstopTargets`, `reap.go:268-325`) only ever
  acts on **unmapped** dispatch-named instances past a 30-minute TTL
  (`dispatchBackstopTTL`, `:26`) — irrelevant to retask once a mapping
  exists, but worth knowing: a mapping write is what takes an instance out of
  backstop eligibility permanently (mapped instances are "never touched here
  regardless of age", `:293-297`).

## 4. `niwa list` (`internal/cli/list.go`)

- `runList` (`:39-84`) calls `workspace.EnumerateInstanceRecords` then
  `annotateKeepAlive` (`:57`, defined `:86-111`), which joins
  `ListSessionMappings` against `sessionLive` to flip `InstanceRecord.KeepAlive`
  true only when a mapping both `KeepAlive`-armed **and** is still live via
  `sessionLive`. This is the existing pattern for "derive a per-instance
  annotation by joining the mapping store with live job state at list time,
  not at write time" — the same shape a retask-eligibility annotation (e.g.
  showing which listed instances have a live, retaskable session) would
  follow if `list` grows a similar marker.
  `--json` mode (`:59-70`) always emits an array (never `null`), one
  `InstanceRecord{name, path, ephemeral[, keep_alive]}` per instance
  (`workspace/state.go:406-416`, `KeepAlive` is `omitempty` so it's absent
  unless true) — the precedent to follow if `list --json` needs to add a
  `retaskable`/`session_id` field for scripting.
- `InstanceRecord` itself carries **no session id** — `list` only exposes
  `name`/`path`/`ephemeral`/`keep_alive`. A retask command resolving "the
  instance named X" to "the session id to push into" needs its own join
  against `ListSessionMappings` (by `InstancePath`), the same join `list`
  and `reap` already do; there is no existing helper that returns
  session-id-by-instance-name, so retask will likely add one (or just
  linear-scan `ListSessionMappings` filtering on `InstanceName`/`InstancePath`).

## 5. Watch's ED2 continuation on main (`internal/cli/watch.go`)

`continueReview` (`:507-610`) is the closest existing analog to "push a
follow-up instruction into a live session, reusing the same containment and
identity guarantees a fresh dispatch has" — the sequence a retask command
should mirror:

1. **Re-validate persisted ids before use** (`:511-524`): `ValidSessionID`,
   `IsSafeHandle`, absolute-and-cleaned `InstancePath`. Never trust ids read
   from a store without re-validating the charset right before they become
   CLI args.
2. **Two-way liveness cross-check at execution time** (`:526-535`):
   `sessionLive(jobsDir, rec.SessionID, now)` **and**
   `instanceHasLiveJob(jobsDir, instancePath)`. On any mismatch, **degrade to
   a no-op this pass** rather than acting on an ambiguous handle — exactly
   the race-window defense retask needs (§3).
3. Re-assert containment (`ApplyReviewSettings`) if the delivery involves a
   sandboxed re-launch — likely N/A for a pure "send text into an existing
   session" retask, but load-bearing for watch because it re-launches.
4. **Stop-before-resume** (`:559-566`): `stopSessionFunc(ctx, rec.ShortID)`
   — a seam (`stopSessionFunc = realStopSession`, `:463`) wrapping
   `claude stop <shortID>` with a charset check
   (`watch.IsSafeHandle`) before the id becomes an argument. A stop failure
   **aborts** the continuation rather than resuming alongside a possibly-
   still-live prior process. **This step only applies to watch's
   stop-and-resume delivery mechanism** (`claude --bg --resume`, which mints
   a new session and requires the old process dead first). A retask command
   built on `claude attach`-and-inject or a hypothetical direct "send
   message to session" primitive would **not** need this stop step — worth
   flagging as a design branch point rather than assuming retask must also
   stop-then-resume.
5. **Launch through the same sandbox-applying path as a fresh dispatch**
   (`dispatchLaunch`, not the lighter sessionattach path) — `:568-581`,
   passthrough built via `buildDispatchPassthrough` (§7) plus
   `--resume <SessionID>`.
6. **Re-capture and its documented failure** (`:583-599`): `claude --bg
   --resume` mints a **new** session id (ignores `--session-id`), and
   `claude stop` leaves the stopped session's job entry present, so the
   post-resume `captureReviewSession(instancePath)` call finds **two** jobs
   sharing the instance cwd → ambiguous → empty ids returned → the record
   becomes non-continuable until the human dismisses the old session in
   Agent View. This is tracked as **issue #211** ("watch: chainable
   continuation across multiple pushes (capture-newest disambiguation)"),
   open, no assignee. Its fix would pick the job at the instance cwd with
   the newest `firstTerminalAt`/registration, or prune the stopped prior
   job's entry before re-capture. **The wiring is already in place**:
   `continueReview` already calls `captureReviewSession(instancePath)`
   post-resume, so #211's fix is purely inside the capture disambiguation —
   no call-site change needed once it lands.
   **Relevance to retask**: if a retask command is built on the same
   stop-and-`--resume` delivery mechanism as `continueReview` (rather than
   e.g. `claude attach` + inject-and-detach), it inherits this exact
   ambiguity today, and **chaining multiple retasks in a row before the
   stopped prior session is dismissed will not work** until #211 ships. A
   retask PRD should either (a) explicitly scope "one retask per session
   until dismissed" as a known limitation (same as watch today), (b) block on
   #211, or (c) choose a delivery mechanism that doesn't mint a new session
   id at all (e.g. injecting into the *existing* attached session rather
   than stop+resume) — which would sidestep the whole problem class.
- `stopSessionFunc`/`dispatchLaunch` are both **package-level var seams**
  (`stopSessionFunc = realStopSession` at `:463`; `dispatchLaunch =
  realDispatchLaunch` in `dispatch_launcher.go:14`), the established pattern
  for making an exec-shelling side effect unit-testable via fakes. A retask
  command's own delivery step should follow the identical seam pattern (a
  package var defaulting to a `real*` implementation) so it's testable the
  same way.
- `BuildResumePrompt(cloneRelPath, draftRelPath)`
  (`internal/watch/continuation.go:103-111`) is a **fixed template with no
  PR-derived free text** — the pattern to follow if retask's injected prompt
  needs the same "never let untrusted/free-form content become part of a
  built-in instruction" discipline. A retask command's actual payload (the
  user's follow-up instruction) is presumably deliberately free-form text
  from the user, unlike this — but the *wrapping* (framing text around the
  user's instruction, if any) should follow this fixed-template discipline
  rather than string-building with interpolated untrusted content.

## 6. Existing keep-alive wiring (`internal/cli/dispatch_keepalive.go`)

Precedent for **layered resolution** a retask feature (e.g. "should this
worker auto-arm something on retask") could reuse:

- `resolveDispatchKeepAlive(flag, global, inst)` (`:94-105`): precedence is
  **flag > downstream (instance settings) > host global default**, each
  layer represented as `*bool` so "unset" is distinguishable from "explicit
  false" — this is the `triBoolValue` mechanism (`:54-74`), a `pflag.Value`
  wrapping `**bool` with `NoOptDefVal = "true"` so a bare flag means explicit
  true. This is the concrete, tested mechanism to reuse for any retask flag
  that needs the same tri-state (e.g. `--keep-alive` on the retask command
  itself, if retask should be able to independently flip keep-alive).
- **Prompt-prepend mechanism**: `keepAliveArmingInstruction` is a **fixed
  constant string** prepended to the task prompt
  (`prompt = keepAliveArmingInstruction + prompt`, `dispatch.go:321`) —
  chosen specifically because the `SessionStart` hook channel
  (`niwa instance from-hook`) is materialized only into the
  **workspace-root** `.claude/settings.json`, which a `claude --bg` worker
  rooted in an *instance* directory does not load (documented at
  `dispatch_keepalive.go:16-22`). **This is directly relevant to retask**:
  any retask delivery mechanism that relies on injecting instructions via a
  session-start-style hook will hit the exact same "instance-rooted workers
  don't load workspace-root hooks" wall. The only channel niwa controls
  end-to-end for an instance-rooted worker today is the **prompt itself**
  (either the initial `--bg` prompt at dispatch time, or, per §5, a
  `--resume`'d prompt). There is currently **no channel to inject text into
  an *already-running, still-attached* session** without going through
  stop+resume — this is the central design question a retask PRD needs to
  resolve.
- `remoteControlEnabled(rcInjected, inst)` (`:115-120`) — gates keep-alive on
  RC being on; not directly reusable for retask, but shows the "compute an
  eligibility gate separately from the resolved opt-in" split
  (`resolveDispatchKeepAlive` resolves the *want*; `remoteControlEnabled`
  gates whether it can actually happen) — a useful shape if retask has its
  own "is this session retaskable" eligibility gate distinct from "does the
  user want to retask".

## 7. CLI house style (flags, errors, `--json`, exit codes, confirmation)

Observed consistently across `dispatch.go`, `list.go`, `reap.go`, `watch.go`:

- **Cobra command definition**: package-level `var xCmd = &cobra.Command{Use,
  Short, Long, Args, RunE}`, registered in an `init()` via
  `xCmd.Flags().Xxx(...)` + `rootCmd.AddCommand(xCmd)`
  (`dispatch.go:20-34`, `list.go:13-17`, `reap.go:28-30`). `Long` descriptions
  are multi-paragraph prose explaining invariants, not just usage.
- **`SilenceErrors: true, SilenceUsage: true`** on commands that produce
  their own `"niwa: error: ..."`-prefixed messages (`dispatch.go:129-130`,
  `reap.go:54-55`) — errors are hand-formatted, not left to cobra's default
  usage-dump-on-error. `list.go`/`watch.go` don't set these (they let cobra's
  default error handling apply) — **not fully consistent across the
  package**, so a retask command should default to the `dispatch.go`/
  `reap.go` style (`SilenceErrors`/`SilenceUsage` + own `"niwa: error: ...":`
  prefix) since that's the more deliberate, security/DESIGN-doc-backed
  pattern (dispatch's comments explicitly reference DESIGN Decision numbers
  and R-numbered requirements throughout).
- **Error format**: `fmt.Errorf("niwa: error: <verb phrase>: %w", err)` —
  lowercase after the prefix, wraps the underlying error with `%w`
  (pervasive in `dispatch.go`, e.g. `:151,156,159,197...`).
- **Tri-state flags**: `triBoolValue` (§6) is the established mechanism for
  a bool flag that must distinguish unset from explicit-false; a plain
  `BoolVar` is used when only "off by default, on if passed" is needed
  (e.g. `watchOnce`, `watch.go:39`; `dispatchDetach`, `dispatch.go:26`).
- **`--json` output**: exactly one pattern in this codebase (`list.go:59-70`)
  — a flag named literally `--json` (`BoolVar(&listJSON, "json", false,
  "<one-line description of the emitted shape>")`), and the handler always
  emits a valid JSON value even for the empty case (`records = []workspace.
  InstanceRecord{}` before encoding, never a bare `null`). Encoding uses
  `json.NewEncoder(cmd.OutOrStdout()).Encode(...)`, not
  `json.Marshal`+`Fprint`. A retask command exposing `--json` should follow
  this exact shape.
- **Exit codes / confirmation prompts**: no interactive confirmation
  prompts exist anywhere in this surface (`dispatch`, `list`, `reap`,
  `watch` are all non-interactive; the one "attach" happens by exec'ing
  `claude attach` which takes over the terminal, not a niwa-owned prompt).
  Destructive operations (`reap`'s instance destroy, `dispatch`'s rollback
  destroy) run **without confirmation**, gated instead by the structural
  eligibility checks themselves (ephemeral marker + dead session, or
  dispatch-name + TTL + no live job) — the house style is "fail-closed
  eligibility gates, not interactive confirmation," consistent with these
  being backgroundable/scriptable commands. A retask command mutating a
  live session should follow the same house style: no interactive prompt,
  just a fail-closed liveness/eligibility check (§3) before acting, and a
  clear stderr message when it declines (matching `reap.go`'s "niwa:
  warning: ..." pattern for non-fatal per-target failures, e.g.
  `reap.go:191,382`, vs. a fatal `return fmt.Errorf(...)` for a whole-command
  abort).
- **Seams for testability**: every exec-shelling or filesystem side effect
  used in these commands is a package-level `var` defaulting to a `real*`
  function (`dispatchCapture`, `dispatchAttach`, `dispatchLaunch`,
  `lookClaude`, `stopSessionFunc`, `provisionInstanceFunc`,
  `destroyInstanceFunc`, `stagedInstanceLiveFunc`, `ensureInstanceTrustedFunc`,
  `removeInstanceTrustFunc`, `sandboxCapabilityCheck`) so tests substitute
  fakes without touching a real `claude` binary or filesystem tree. A retask
  command's delivery step (whatever it ends up being — `claude attach` +
  inject, or stop+`--resume`, or something new) must follow this same
  seam-var convention from the start, not be hardwired to `exec.Command`.

## Summary of requirement-shaping facts

1. **Resolve by mapping, not by re-capture**: retask should look up the
   session id via `ListSessionMappings`/`ReadSessionMapping`
   (`internal/workspace/session_map.go`), never re-run `captureSessionID`
   against an instance that may already hold a live session — doing so
   reproduces the exact multi-job-same-cwd ambiguity `matchSessionByCwd`
   raises (`dispatch_capture.go:57-58`), which is precisely what strands
   `continueReview`'s post-resume re-capture (issue #211).
2. **No channel exists today to inject into an already-attached, running
   session** without stop+`--resume`; the `SessionStart`-hook channel
   (`dispatch_keepalive.go`) explicitly does not reach instance-rooted
   workers, and the only proven channel is the launch/resume prompt text
   itself. This is the central open design question for retask, not an
   implementation detail.
3. **If retask reuses `continueReview`'s stop-and-`--resume` delivery
   mechanism**, it inherits #211 as-is: the post-resume re-capture is
   ambiguous until the stopped prior session is dismissed, so **retasking
   the same session twice in a row (before dismissal) will silently strand
   the mapping's session id** exactly like chained watch-continuations do
   today. The PRD must decide whether to require #211 as a dependency, scope
   "single retask per live window" as a documented limitation, or pick a
   non-stop-resume delivery mechanism.
4. **Reaper race window**: a mapping can be deleted out from under a retask
   attempt at any moment via `reapOpportunistically` (which runs at the
   start of every `create`/`dispatch`) or an explicit `niwa reap`, because
   dead-session reclamation deletes the mapping immediately on destroy
   (`reap.go:201`). Retask must re-check `sessionLive` **immediately before
   delivery**, mirroring `continueReview`'s execution-time two-way
   liveness cross-check (`watch.go:526-535`), and fail closed rather than
   act on a possibly-already-reclaimed instance.
5. **Reusable seams, not new patterns**: `sessionLive`/`instanceHasLiveJob`
   for liveness, `SessionMapping`'s atomic read/write for the durable store,
   `triBoolValue` for any tri-state flag, `buildDispatchPassthrough`/
   `dispatchLaunch`/`stopSessionFunc` if delivery goes through a relaunch,
   and the `--json` + `SilenceErrors`/`SilenceUsage` + `"niwa: error: ...:
   %w"` house style for the command surface itself. Nothing here needs a new
   architectural pattern — retask is a new command that joins the same two
   stores (`workspace.SessionMapping`, `~/.claude/jobs/*/state.json`) every
   other lifecycle command already joins.
