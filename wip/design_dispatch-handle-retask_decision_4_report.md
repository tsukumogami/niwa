# Decision 4: Retask engine placement and interface

## Question

Where does the shared retask engine live, what is its interface — including
the R9 replaceable delivery seam — and how does watch's `continueReview`
adopt it without losing its sandbox re-assertion or its own liveness/
freshness policy?

## Options considered

**Placement**

1. New `internal/retask` package. Rejected. The orchestration primitives a
   retask engine needs — `sessionLive`/`instanceHasLiveJob` (job_state.go),
   `captureSessionID`/`matchSessionByCwd` (dispatch_capture.go),
   `dispatchLaunch`/`buildClaudeBgArgs` (dispatch_launcher.go),
   `stopSessionFunc` (watch.go) — are all unexported today and live in
   `internal/cli`. Standing up a new package would force exporting all of
   them and would force both `dispatch.go` and `watch.go` (which already sit
   in `internal/cli` and call these functions directly, unqualified) to start
   importing the new package. That is a much larger blast radius than the
   PRD scopes, for no reuse benefit — the only consumers (`dispatch`,
   `watch`, the new `retask` command) already share a package. No import
   cycle blocks this option (`internal/cli` already one-way-imports
   `internal/watch` and `internal/workspace`; neither imports back —
   confirmed via `grep -rn "tsukumogami/niwa/internal/cli" internal/watch
   internal/workspace` returning nothing), so the rejection is about
   unnecessary surface area, not feasibility. The design doc's own "affected
   code" list already frames this as `internal/cli` work.
2. Extend `internal/workspace`. Rejected. `internal/workspace/session_map.go`
   is pure durable-state I/O (atomic read/write of `SessionMapping`, no
   subprocess exec, no jobs-dir polling). Retask orchestration needs
   `exec.Command("claude", "stop"/"--bg"...)` and jobs-dir reads, which are
   deliberately kept out of `internal/workspace` today (that's why they live
   in `internal/cli` instead). Putting orchestration there breaks the
   existing layering for no gain.
3. **New files inside `internal/cli`** (`retask.go` for the command,
   `retask_engine.go` for the orchestration): recommended. `internal/cli`
   already separates "engine" files (`job_state.go`, `dispatch_capture.go`,
   `dispatch_launcher.go`, `dispatch_keepalive.go` — no command, just shared
   orchestration primitives) from "command" files (`dispatch.go`, `watch.go`,
   `reap.go`, `list.go`) inside one package. A retask engine is another
   engine file in that same established shape, callable directly (same
   package, no import) from both the new `retask.go` and the existing
   `watch.go`.

**Interface shape**

1. One `Retask(ctx, opts)` God-function engine owning stop, relaunch,
   capture, *and* rebind end-to-end. Rejected on inspection: `dispatch`'s
   durable store is `workspace.SessionMapping`, but watch's is
   `watch.StagedRecord` (a different struct, different store, saved via
   `watch.SaveStagedRecord`). A single engine function that "owns rebind"
   can't write both stores generically without either taking a callback
   (which just reintroduces a seam) or hard-coding one store and forcing the
   other caller to fake it. This finding reshapes the recommendation below.
2. Interface type `Delivery` with a resume-based implementation and a
   future channel-based one. Considered, but the existing house style never
   uses interface types for this kind of seam — `dispatchCapture`,
   `stopSessionFunc`, `dispatchLaunch`, `lookClaude` are all package-level
   **func vars** defaulting to a `real*` implementation (`dispatch.go:84-107`,
   `watch.go:463`, `dispatch_launcher.go:14`). A func var is the idiomatic,
   directly-swappable, directly-fakeable seam here; an interface type would
   be new ceremony the codebase doesn't otherwise use for this pattern.
3. **A small pipeline of exported-within-package steps the two callers
   compose**, with the delivery step behind a func-var seam and the rebind
   step left to each caller: recommended. See sketch below.

**Watch adoption**

`continueReview` keeps every one of its current pre-checks in place — id
re-validation, the two-way liveness cross-check, the PR-head fetch, and
`ApplyReviewSettings` — and calls the shared delivery step only for
stop+relaunch+capture. It keeps performing its own rebind
(`watch.SaveStagedRecord`) exactly as today, just fed by the engine's
disambiguated result instead of the current ambiguous `captureReviewSession`.

**CLI command wiring**

Follows `dispatch.go`/`reap.go`'s style (`SilenceErrors`/`SilenceUsage`,
hand-formatted `"niwa: error: ...: %w"`), not `list.go`/`watch.go`'s laxer
one — this matches the integration-surface research's explicit
recommendation (§7) and the security/DESIGN-doc-comment density `dispatch.go`
already carries.

## Recommendation

### Package: `internal/cli`, two new files

- `internal/cli/retask_engine.go` — the delivery seam and the busy/attached/
  gone precondition classifier. No command code, no cobra. Callable directly
  by `watch.go` (same package) and by the new `retask.go`.
- `internal/cli/retask.go` — the `niwa retask <target> <prompt>` command:
  target resolution, precondition checks, calling the engine, and rebinding
  `workspace.SessionMapping`.

### Interface sketch

```go
// --- retask_engine.go ---

// deliveryRequest carries everything a delivery mechanism needs to push a
// prompt into a session and report which ids survive. It intentionally does
// NOT carry a store to rebind: dispatch's store is workspace.SessionMapping,
// watch's is watch.StagedRecord, and forcing one shape on both callers is
// what option 1 above got wrong. Rebind stays the caller's job.
type deliveryRequest struct {
    InstancePath string
    SessionID    string       // current/prior full UUID
    ShortID      string       // current/prior short id (stop/attach handle)
    Prompt       string
    Passthrough  []string
    PreLaunch    func() error // e.g. watch.ApplyReviewSettings; nil for plain retask
}

type deliveryResult struct {
    SessionID string // surviving id -- what the caller rebinds to
    ShortID   string
    Rotated   bool   // true when a NEW job entry was minted (today: always)
}

// retaskDeliver is the R9 seam: the ONE boundary a future in-place channel
// delivery replaces. Production wires it to resumeDelivery.
var retaskDeliver = resumeDelivery

// resumeDelivery is today's only delivery mechanism: stop the prior job,
// relaunch through dispatchLaunch with --resume, then disambiguate the
// resumed job's id via capture-newest (this is the R5 / #211 fix). Ids
// always rotate.
func resumeDelivery(ctx context.Context, req deliveryRequest) (deliveryResult, error) {
    if req.PreLaunch != nil {
        if err := req.PreLaunch(); err != nil {
            return deliveryResult{}, err
        }
    }
    if err := stopSessionFunc(ctx, req.ShortID); err != nil {
        return deliveryResult{}, fmt.Errorf("stopping prior session: %w", err)
    }
    passthrough := append(append([]string{}, req.Passthrough...), "--resume", req.SessionID)
    if err := dispatchLaunch(ctx, req.InstancePath, req.Prompt, passthrough, nil); err != nil {
        return deliveryResult{}, fmt.Errorf("resuming session: %w", err)
    }
    sid, short, err := captureNewest(defaultJobsDir(), req.InstancePath, req.ShortID)
    if err != nil {
        return deliveryResult{}, err
    }
    return deliveryResult{SessionID: sid, ShortID: short, Rotated: true}, nil
}

// A future channelDelivery(ctx, req) would call req.PreLaunch if set, inject
// the prompt over the in-place channel -- no stop, no relaunch, no capture --
// and return deliveryResult{req.SessionID, req.ShortID, Rotated: false}: the
// SAME ids. The seam holds both shapes cleanly because capture/disambiguation
// is entirely internal to resumeDelivery, not part of the contract; a
// non-rotating delivery has nothing to disambiguate. Whether the caller's
// rebind is a real rotation or a same-value no-op write follows mechanically
// from Rotated -- see below.

// captureNewest resolves the R5 capture-ambiguity case deterministically:
// among job entries sharing instancePath's cwd, the one that is NOT
// priorShortID and has the newest state.json mtime wins; a tie or an
// unvalidatable id fails closed. This replaces captureReviewSession's
// current "ambiguous -> empty ids" behavior for the post-relaunch case and
// is what closes #211.
func captureNewest(jobsDir, instancePath, priorShortID string) (sessionID, shortID string, err error) { /* ... */ }

// jobActivityFor generalizes watch.go's recordJobActivity (currently keyed
// on watch.StagedRecord) to a bare sessionID, so both watch's
// recordContinuable and retask's precondition check share one reader.
func jobActivityFor(jobsDir, sessionID string) watch.JobActivity { /* ... */ }

// retaskPrecondition implements R4: distinct, fail-closed errors for busy,
// attached, and gone. Liveness (entry-present) is checked FIRST and
// separately, so "gone" is never confused with "unreadable state" -- both
// collapse under watch.ActivityDeadUnknown but liveness catches gone earlier
// with its own message.
func retaskPrecondition(jobsDir string, sessionID string, now time.Time) error {
    if !sessionLive(jobsDir, sessionID, now) {
        return fmt.Errorf("no such session: job entry not found")
    }
    switch watch.ClassifySessionActivity(jobActivityFor(jobsDir, sessionID)) {
    case watch.ActivityBusy:
        return fmt.Errorf("session is actively running a turn")
    case watch.ActivityAttached:
        return fmt.Errorf("session is attached / awaiting a human")
    case watch.ActivityDetachedIdle:
        return nil
    default:
        return fmt.Errorf("session state could not be confirmed idle")
    }
}
```

```go
// --- retask.go ---

var retaskCmd = &cobra.Command{
    Use:           "retask <target> <prompt>",
    Short:         "Deliver a follow-up instruction to a dispatched session",
    Args:          cobra.ExactArgs(2),
    SilenceErrors: true,
    SilenceUsage:  true,
    RunE:          runRetask,
}

func init() {
    retaskCmd.Flags().BoolVar(&retaskJSON, "json", false,
        "emit the rebound mapping as JSON {session_id, instance_name, instance_path}")
    rootCmd.AddCommand(retaskCmd)
}

func runRetask(cmd *cobra.Command, args []string) error {
    target, prompt := args[0], args[1]
    // ... resolve workspaceRoot via workspace.ClassifyCwd (same as dispatch/list) ...

    mapping, err := resolveRetaskTarget(workspaceRoot, defaultJobsDir(), target)
    if err != nil {
        return fmt.Errorf("niwa: error: resolving retask target: %w", err)
    }
    if err := retaskPrecondition(defaultJobsDir(), mapping.SessionID, time.Now()); err != nil {
        return fmt.Errorf("niwa: error: %w", err)
    }
    curShort, ok := currentShortID(defaultJobsDir(), mapping.SessionID)
    if !ok {
        return fmt.Errorf("niwa: error: could not resolve current short id for session")
    }

    res, err := retaskDeliver(cmd.Context(), deliveryRequest{
        InstancePath: mapping.InstancePath,
        SessionID:    mapping.SessionID,
        ShortID:      curShort,
        Prompt:       prompt,
    })
    if err != nil {
        return fmt.Errorf("niwa: error: retasking session: %w", err)
    }

    updated := mapping
    updated.SessionID = res.SessionID
    if err := workspace.WriteSessionMapping(workspaceRoot, updated); err != nil {
        return fmt.Errorf("niwa: error: rebinding session mapping: %w", err) // N1
    }
    if res.Rotated && res.SessionID != mapping.SessionID {
        _ = workspace.DeleteSessionMapping(workspaceRoot, mapping.SessionID) // R3/D5
    }
    // ... print / --json emit updated ...
    return nil
}

// resolveRetaskTarget accepts either the instance name (matched against
// ListSessionMappings' InstanceName) or the session short id (looked up by
// reading <jobsDir>/<target>/state.json's sessionId, then matched against
// ListSessionMappings by SessionID). Neither lookup direction has an
// existing helper -- both are new.
func resolveRetaskTarget(workspaceRoot, jobsDir, target string) (workspace.SessionMapping, error) { /* ... */ }
```

### Watch adoption

`continueReview` (`watch.go:507-610`) restructures its existing steps (3)-(6)
into:

```go
askPosture := resolveAskPosture(instancePath, plan.sandbox)
passthrough := buildDispatchPassthrough(rec.Handle, "")
if plan.sandbox {
    passthrough = append(passthrough, "--strict-mcp-config")
}
res, err := retaskDeliver(ctx, deliveryRequest{
    InstancePath: instancePath,
    SessionID:    rec.SessionID,
    ShortID:      rec.ShortID,
    Prompt:       watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath),
    Passthrough:  passthrough,
    PreLaunch: func() error {
        return watch.ApplyReviewSettings(instancePath, plan.sandbox, askPosture)
    },
})
if err != nil {
    return fmt.Errorf("resuming review agent: %w", err)
}
rec.SessionID, rec.ShortID = res.SessionID, res.ShortID
rec.DispatchedSHA = head.SHA
if err := watch.SaveStagedRecord(root, rec); err != nil {
    return err
}
```

Everything before this — id re-validation (step 0), the two-way liveness
cross-check (step 1), and the PR-head fetch (step 2) — stays exactly as it is
today, unchanged, ahead of the call. Those are watch-specific policy
(freshness, cap accounting is handled by the caller of `continueReview`) that
a generic `niwa retask` has no equivalent of, so they correctly stay outside
the shared engine rather than becoming optional engine parameters. The one
behavior change is that `captureReviewSession`'s ambiguous-fails-closed
capture is replaced by `captureNewest` inside `resumeDelivery`, which is what
lets continuation chain past one push (closing #211 as a side effect, per PRD
D7).

**Staged-record update when the engine owns rebind**: it doesn't — rebind
stays with each caller, per the "God-function" rejection above. `watch.go`
keeps writing `watch.StagedRecord` via `watch.SaveStagedRecord`;
`retask.go` keeps writing `workspace.SessionMapping` via
`workspace.WriteSessionMapping`/`DeleteSessionMapping`. Both are driven by
the same `deliveryResult`, so the *logic* ("assign the surviving ids, delete
the old entry only if `Rotated`") is identical prose in both call sites even
though the storage calls differ — that duplication is small (a handful of
lines) and cheaper than forcing one generic rebind function to abstract over
two structurally different stores.

## Assumptions

1. **`SessionMapping` has no `ShortID` field** (confirmed in
   `internal/workspace/session_map.go:49-73`). The engine's `deliveryRequest`
   needs the *current* short id to pass to `stopSessionFunc`, so `retask.go`
   must derive it fresh from the jobs dir (`currentShortID(jobsDir,
   sessionID)`, a small new variant of `readJobState` that also returns the
   matched directory basename) rather than storing it durably. Recommend
   **not** widening the mapping schema for this — the short id is exactly as
   volatile as the session id it names, and a jobs-dir lookup is already the
   established source of truth (`captureSessionID` derives it the same way).
2. **R4's two eligible worker states — "(a) live and idle" vs "(b) stopped
   with its job entry intact" — may collapse to the same code path.**
   `sessionLive`'s own doc comment (`job_state.go:70-95`) says liveness is
   "entry-present," covering both a running and an "idle-but-resumable"
   session, and is silent on whether `claude stop` removes the entry or
   leaves it with stale state fields. If a stopped session's entry persists
   and its stale `State`/`Tempo` still reads as done/idle, `retaskPrecondition`
   above handles both PRD cases with the same `sessionLive` +
   `ActivityDetachedIdle` check, no branch needed. This needs verification
   against real Claude Code job-state behavior (an integration-level fact),
   not something the codebase's comments settle definitively — flagging
   for whoever writes the acceptance-criteria integration test rather than
   assuming.
3. **"Newest registration" (R5) is proposed as `state.json` file mtime**,
   since `jobState` (`job_state.go:24-52`) does not currently decode a
   `firstTerminalAt`/registration timestamp field, only `SessionID`,
   `Template`, `Cwd`, `State`, `Tempo`, `InFlight`, `Block`, `Needs`. Mtime is
   available via `os.Stat` with no new decoded field and is monotonic enough
   for "which of these two entries appeared more recently." If Claude Code's
   job state does expose a more precise registration timestamp field, using
   it instead is a strict improvement, not a design change.
4. **`retaskPrecondition`'s tie-break to a single generic "state could not be
   confirmed idle" error** for the `ActivityDeadUnknown`-but-entry-present
   case (corrupt/unreadable state.json) is a simplification of R4, which only
   asks that busy/attached/gone be distinguished — an unreadable-but-present
   state is a genuinely rare edge the PRD doesn't separately name, so folding
   it into a fourth, less-specific error seems acceptable but is worth
   confirming doesn't silently violate N3's "reason" requirement.

## Confidence

**High** on placement (`internal/cli`, no new package) — the existing engine-
file/command-file split inside `internal/cli` is an established, visible
pattern, there's no import-cycle obstacle, and the design doc's own affected-
code list already scopes this as `internal/cli` work.

**High** on the delivery seam being a func var (`retaskDeliver`), not an
interface type — this is the codebase's only pattern for exactly this kind of
swappable side-effecting seam (`dispatchCapture`, `stopSessionFunc`,
`dispatchLaunch`, `lookClaude` all do it this way).

**Medium-high** on rebind staying caller-owned rather than engine-owned —
this follows directly from `watch.StagedRecord` and `workspace.SessionMapping`
being genuinely different stores; I did not find (and don't believe there is)
a way to unify them without a callback that just re-creates the same split.

**Medium** on the specific helper signatures (`captureNewest`,
`currentShortID`, `jobActivityFor`, `resolveRetaskTarget`) — the shapes are
grounded in real code (`matchSessionByCwd`, `readJobState`,
`recordJobActivity`, `ClassifySessionActivity`) but are my own sketch, not
verified against a WIP implementation; expect small signature drift once
someone writes the actual code and tests.

**Lower** on Assumptions 2 and 3 specifically — both depend on real Claude
Code job-state behavior this static read of the repo can't settle; they
should be treated as open questions for the plan/implementation phase, not
closed decisions.
