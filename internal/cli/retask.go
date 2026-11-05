package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(retaskCmd)
}

var retaskCmd = &cobra.Command{
	Use:   "retask <target> <prompt>",
	Short: "Hand a dispatched worker its next task in the same session",
	Long: `Hand a worker niwa dispatch launched its next task, in the same session:
same session id, same job entry, same short id, and the context of its
earlier work.

The target names the worker by its instance name, its full instance path, its
session id, its session name, its short id or its dispatch id, matched exactly
and never by prefix. Pass a prompt that begins with a dash after "--".

Retask owns a worker whose instance is there and whose job entry Claude Code
still lists, whether it is live and idle or stopped. It stops a live worker,
waits until Claude Code shows the stop settled, and wakes the session with
"claude --bg --resume <session id> -- <prompt>" from the instance, with no
other argument, so Claude Code re-applies the options it saved with the job.
It then checks from Claude Code's listing that the same session is running,
removes a copy the wake started instead, and writes nothing: the session
mapping, the launch record and the short id stay as they were.

A worker whose job entry or instance is gone is niwa resume's: retask refuses
it and names niwa resume. Retask also refuses, before it stops anything, a
worker that is in the middle of a turn (it doesn't queue the task) or waiting
for an answer, a Claude Code build older than 2.1.314, saved options that
differ from the launch record, a current permission policy stricter than the
recorded mode, another session rooted in the instance, and another niwa
command acting on the instance. Every refusal names one stable state word.

The prompt appears in the process table and in Claude Code's transcripts, so
it must not carry secrets.`,
	Args: cobra.ExactArgs(2),
	RunE: runRetask,
}

// The state words. Every retask refusal carries exactly one, right after the
// target ("niwa: error: cannot retask "<target>": <word>: ..."), and the
// words are a stable contract: scripts and the guide read them, so one is
// never renamed or dropped, and retaskStateWords lists exactly these.
const (
	retaskUnknownTarget    = "unknown-target"
	retaskAmbiguousTarget  = "ambiguous-target"
	retaskInvalidRecord    = "invalid-record"
	retaskInstanceGone     = "instance-gone"
	retaskInstanceOccupied = "instance-occupied"
	retaskTranscriptGone   = "transcript-gone"
	retaskJobEntryGone     = "job-entry-gone"
	retaskStateUnknown     = "state-unknown"
	retaskAwaitingInput    = "awaiting-input"
	retaskBusy             = "busy"
	retaskPolicyTightened  = "policy-tightened"
	retaskOptionsDrift     = "options-drift"
	retaskStraySession     = "stray-session"
	retaskConcurrency      = "concurrency"
	retaskAgentUnsupported = "agent-unsupported"
	retaskBuildForks       = "build-forks"
	retaskStopFailed       = "stop-failed"
	retaskSettleTimeout    = "settle-timeout"
	retaskCopyStarted      = "copy-started"
	retaskCopyLeft         = "copy-left"
	retaskWakeUnconfirmed  = "wake-unconfirmed"
)

// retaskStateWords is the table of state words, in the order the guide lists
// them.
var retaskStateWords = []string{
	retaskUnknownTarget,
	retaskAmbiguousTarget,
	retaskInvalidRecord,
	retaskInstanceGone,
	retaskInstanceOccupied,
	retaskTranscriptGone,
	retaskJobEntryGone,
	retaskStateUnknown,
	retaskAwaitingInput,
	retaskBusy,
	retaskPolicyTightened,
	retaskOptionsDrift,
	retaskStraySession,
	retaskConcurrency,
	retaskAgentUnsupported,
	retaskBuildForks,
	retaskStopFailed,
	retaskSettleTimeout,
	retaskCopyStarted,
	retaskCopyLeft,
	retaskWakeUnconfirmed,
}

// The settle and verify bounds. Settling polls the listing until the stopped
// worker's row shows no pid and no status, since a wake issued before then
// starts a copy; verifying polls it until the woken session is running, or a
// copy or a vanished row decides the outcome sooner.
const (
	retaskSettleInterval = 250 * time.Millisecond
	retaskSettleWindow   = 15 * time.Second
	retaskVerifyInterval = 500 * time.Millisecond
	retaskVerifyWindow   = 20 * time.Second
)

// The bounds as the core reads them, so a test can shorten them.
var (
	retaskSettlePoll  = retaskSettleInterval
	retaskSettleBound = retaskSettleWindow
	retaskVerifyPoll  = retaskVerifyInterval
	retaskVerifyBound = retaskVerifyWindow
)

// retaskMinClaudeVersion is the oldest Claude Code build retask wakes a
// worker on. An older build refuses before anything is stopped; on any build
// the copy check after the wake is what catches a copy.
var retaskMinClaudeVersion = [3]int{2, 1, 314}

// The caller kinds the core serves.
const (
	retaskCallerCLI   = "cli"
	retaskCallerWatch = "watch"
)

// retaskLockCommand is the command the core names as the instance lock's
// holder. A destroy, reap, resume or watch prune that finds the lock held
// reports the holder, and a user who never ran `niwa retask` should be told
// it is `niwa watch` continuing a review.
func retaskLockCommand(kind string) string {
	if kind == retaskCallerWatch {
		return "watch"
	}
	return verbRetask
}

// Seams over what the core reads and runs, so a test can answer them.
var (
	// retaskClaudeVersionFunc runs `<bin> --version` and returns its output.
	retaskClaudeVersionFunc = realRetaskClaudeVersion
	// readSavedOptionsFunc reads the options Claude Code saved with the job.
	readSavedOptionsFunc = readSavedOptions
	// retaskDelivererFor picks the deliverer an agent's declaration names,
	// nil for none.
	retaskDelivererFor = defaultRetaskDeliverer
)

// workerView is one dispatched worker as the core sees it. The ids are
// validated and the instance path is derived by niwa, never read raw from a
// file. A CLI caller sets Record and Mapping; watch sets Staged instead.
type workerView struct {
	SessionID    string                    // validated full id
	ShortID      string                    // the job handle, validated
	InstancePath string                    // derived by niwa, never read raw
	Record       *workspace.LaunchRecord   // CLI only
	Mapping      *workspace.SessionMapping // CLI only
	Staged       *watch.StagedRecord       // watch only

	// Root is the workspace root, where the launch store and the session
	// store live.
	Root string
	// Spec is the worker's agent declaration.
	Spec agentplan.LaunchSpec
	// HostMode is the machine's [global] dispatch_permission_mode, and
	// ConfigPosture the permissions posture the workspace configuration gives
	// now. Together with the instance's recorded posture they are the mode
	// current policy gives a new dispatch, which the recorded mode must not
	// be looser than.
	HostMode      string
	ConfigPosture string
	// Unset is the workspace's [session.dispatch_env] unset list, kept out of
	// the wake's environment as it is kept out of a dispatch's.
	Unset []string
}

// workerState is what the core classified a worker as, once it passed every
// check that could refuse it.
type workerState int

const (
	// liveIdle is a worker whose process is alive and idle: it is stopped,
	// left to settle, then woken.
	liveIdle workerState = iota + 1
	// stoppedWithEntry is a stopped worker whose job entry Claude Code still
	// lists: it is woken as it is.
	stoppedWithEntry
)

func (s workerState) String() string {
	switch s {
	case liveIdle:
		return "live idle"
	case stoppedWithEntry:
		return "stopped"
	}
	return "unclassified"
}

// workerSnapshot is what the core read about the worker, handed to the
// caller's hooks and to the deliverer.
type workerSnapshot struct {
	View     workerView
	State    workerState
	Row      claudeSessionRow
	Activity watch.JobActivity
	Options  []string // the job's saved options
}

// retaskCaller is who asked for the retask and what it adds. The CLI sets no
// hook; watch sets all three. Each hook runs under the instance's lock.
type retaskCaller struct {
	Kind   string // "cli" or "watch"
	Prompt string
	// Target is what the caller named the worker by, for messages. Empty
	// names it by its session id.
	Target string
	// PreStop runs after classification and before the stop, in place of
	// the CLI's saved-options and policy checks.
	PreStop func(ctx context.Context, s workerSnapshot) error
	// PreWake runs after the settle and before the final re-read, with the
	// saved options read again after the stop.
	PreWake func(ctx context.Context, s workerSnapshot) error
	// PostCommit runs after verify and copy removal, and also, with a
	// refused result, when the final re-read refuses after PreWake returned
	// nil, so the caller can undo what PreWake wrote.
	PostCommit func(ctx context.Context, s workerSnapshot, r retaskResult) error
}

// retaskOutcome is how a retask ended.
type retaskOutcome int

const (
	// retaskDelivered: the same session took the task.
	retaskDelivered retaskOutcome = iota
	// retaskRefused: nothing was woken (a stop may have happened).
	retaskRefused
	// retaskCopyRemoved: the wake started a copy, which was removed.
	retaskCopyRemoved
	// retaskUnconfirmed: the wake ran and its outcome couldn't be
	// confirmed, or a copy it started couldn't be removed.
	retaskUnconfirmed
)

// retaskResult is the core's answer.
type retaskResult struct {
	Outcome retaskOutcome
	State   string // the stable state word; empty when delivered
	Stopped bool   // the core stopped a live worker before the outcome
	CopyID  string // the copy's short id, set when a copy was found
}

// safeRetaskHandle reports whether a short id may become an argument of a
// claude verb: the staged-record charset, and no leading dash, which a verb
// would read as a flag.
func safeRetaskHandle(h string) bool {
	return watch.IsSafeHandle(h) && !strings.HasPrefix(h, "-")
}

func runRetask(cmd *cobra.Command, args []string) error {
	target, prompt := args[0], args[1]
	if err := checkRetaskPrompt(target, prompt); err != nil {
		return err
	}
	t, err := resolveWorkerTarget(cmd, verbRetask, target)
	if err != nil {
		return err
	}
	v, err := retaskViewFor(t, target)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := retaskCore(ctx, v, retaskCaller{Kind: retaskCallerCLI, Prompt: prompt, Target: target}); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Retasked session %s\n", v.SessionID)
	fmt.Fprintf(out, "  instance: %s\n", v.InstancePath)
	for _, line := range reentryHints(v.Spec, v.ShortID, v.InstancePath) {
		fmt.Fprintf(out, "  %s\n", line)
	}
	return nil
}

// checkRetaskPrompt refuses a prompt that can't travel as one argument:
// empty or only whitespace, longer than one argv element may be, or holding a
// NUL byte. It runs before anything else, so a bad prompt invokes nothing.
// Unlike dispatch, retask never spills a long prompt to a file: the wake must
// carry the caller's prompt and nothing else.
func checkRetaskPrompt(target, prompt string) error {
	switch {
	case strings.TrimSpace(prompt) == "":
		return resumeRefusal(verbRetask, target, "the prompt must not be empty")
	case len(prompt) > maxArgStringBytes:
		return resumeRefusal(verbRetask, target, "the prompt is longer than %d bytes, the most one argument can carry", maxArgStringBytes)
	case strings.ContainsRune(prompt, 0):
		return resumeRefusal(verbRetask, target, "the prompt holds a NUL byte, which no argument can carry")
	}
	return nil
}

// resolveRetaskTarget resolves target, under the workspace at root, to the
// worker retask acts on.
func resolveRetaskTarget(root, target string) (workerView, error) {
	wsConfig, err := loadWorkerWorkspaceConfig(root, verbRetask, target)
	if err != nil {
		return workerView{}, err
	}
	t, err := resolveWorkerAt(root, wsConfig, verbRetask, target)
	if err != nil {
		return workerView{}, err
	}
	return retaskViewFor(t, target)
}

// retaskViewFor builds the view of a resolved worker. The short id comes
// from the session mapping dispatch wrote, which must name the record's
// session and hold a handle a claude verb can take.
func retaskViewFor(t workerTarget, target string) (workerView, error) {
	rec := t.Record
	m, err := workspace.ReadSessionMapping(t.Root, rec.SessionID)
	if err != nil {
		return workerView{}, workerRefuse(verbRetask, target, retaskInvalidRecord,
			"no readable session mapping records the short id of session %s (%v)", rec.SessionID, err)
	}
	if !workspace.ValidSessionID(m.SessionID) || m.SessionID != rec.SessionID {
		return workerView{}, workerRefuse(verbRetask, target, retaskInvalidRecord,
			"the session mapping of session %s doesn't name that session", rec.SessionID)
	}
	if !safeRetaskHandle(m.Handle) {
		return workerView{}, workerRefuse(verbRetask, target, retaskInvalidRecord,
			"the session mapping of session %s records no usable short id", rec.SessionID)
	}
	return workerView{
		SessionID:     rec.SessionID,
		ShortID:       m.Handle,
		InstancePath:  t.InstancePath,
		Record:        &rec,
		Mapping:       &m,
		Root:          t.Root,
		Spec:          t.Spec,
		HostMode:      t.HostMode,
		ConfigPosture: workspace.PermissionsPosture(t.WSConfig),
		Unset:         t.Unset,
	}, nil
}

// retaskRun is one run of the core.
type retaskRun struct {
	ctx     context.Context
	v       workerView
	c       retaskCaller
	target  string
	jobsDir string
	res     retaskResult
}

// retaskCore hands the worker v names its next task in the same session, for
// the caller c. It runs these steps in order, all under the instance's lock:
//
//  1. take the lock, re-read the launch record and mapping, check the
//     instance and the transcript;
//  2. check the Claude Code build (`claude --version`) before any stop;
//  3. list once, read the job state and classify the worker as live idle,
//     stopped with its job entry, or a refusal; refuse a stray session, then
//     ask the deliverer whether it accepts the state;
//  4. check the saved options and the permission policy (the CLI), or run
//     PreStop (watch);
//  5. for a live idle worker, stop it by its short id after matching the
//     row's full session id, and wait for the stop to settle;
//  6. run PreWake;
//  7. re-read the listing and the job state, and snapshot the sessions
//     rooted at the instance;
//  8. deliver: the commit point;
//  9. verify from the listing, remove a copy the wake started, run
//     PostCommit, and release the lock.
//
// A non-nil error is always the refusal or outcome the result names.
func retaskCore(ctx context.Context, v workerView, c retaskCaller) (retaskResult, error) {
	r := &retaskRun{ctx: ctx, v: v, c: c, target: c.Target}
	if r.target == "" {
		r.target = v.SessionID
	}
	r.res.Outcome = retaskRefused

	if !workspace.ValidSessionID(v.SessionID) || !safeRetaskHandle(v.ShortID) {
		return r.refuse(retaskInvalidRecord, "the worker's session id or short id isn't one niwa passes to a command")
	}
	if v.InstancePath == "" || !filepath.IsAbs(v.InstancePath) {
		return r.refuse(retaskInvalidRecord, "the worker's instance path isn't one niwa derived")
	}

	// (1) The lock is taken before anything else is read or run, and held
	// until the core returns. A platform without flock refuses: retask's stop
	// and wake must not race a destroy, a reap or a resume.
	release, err := tryInstanceLockFunc(v.InstancePath, instanceLockHolder(retaskLockCommand(c.Kind)))
	if err != nil {
		return r.refuse(retaskConcurrency, "%s", instanceLockReason(err))
	}
	if release != nil {
		defer release()
	}
	if res, err := r.rereadUnderLock(); err != nil {
		return res, err
	}

	// (2) The build check, once, before any stop.
	if res, err := r.checkBuild(); err != nil {
		return res, err
	}

	// (3) One listing, the job state, and the classification.
	rows, err := listClaudeSessionsFunc(ctx)
	if err != nil {
		return r.refuse(retaskStateUnknown, "could not list Claude Code's sessions (%v)", err)
	}
	snap, res, err := r.classify(rows)
	if err != nil {
		return res, err
	}
	d := retaskDelivererFor(v.Spec)
	if d == nil || !d.Accepts(snap.State, c.Kind) {
		return r.refuse(retaskAgentUnsupported, "the %s agent's delivery can't hand a %s worker its next task", agentNameOf(v), snap.State)
	}

	// (4) The saved options and the permission policy, or watch's PreStop.
	switch {
	case c.Kind == retaskCallerCLI:
		if res, err := r.checkOptionsAndPolicy(&snap); err != nil {
			return res, err
		}
	case c.PreStop != nil:
		if err := c.PreStop(ctx, snap); err != nil {
			return r.hookFailed(err)
		}
	}

	// (5) Stop a live worker and wait for the stop to settle.
	if snap.State == liveIdle {
		if row, ok := findSessionRow(rows, v.SessionID); !ok || row.SessionID != v.SessionID || row.ID != v.ShortID {
			return r.refuse(retaskStateUnknown, "the listing no longer matches session %s to short id %s", v.SessionID, v.ShortID)
		}
		if err := stopSessionFunc(ctx, v.ShortID); err != nil {
			r.res.Stopped = true
			return r.refuse(retaskStopFailed, "claude stop %s failed (%v). %s", v.ShortID, err, r.stoppedNote())
		}
		r.res.Stopped = true
		switch st := r.waitStopped(v.SessionID, v.ShortID); st {
		case waitSettled:
		case waitGone:
			return r.refuse(retaskJobEntryGone, "Claude Code stopped listing session %s after the stop, so there is no job entry to wake; niwa resume %s <prompt> brings it back", v.SessionID, shellToken(r.target))
		default:
			return r.refuse(retaskSettleTimeout, "session %s still showed a live process %s after claude stop, and a wake before the stop settles starts a copy, so retask didn't wake it. %s",
				v.SessionID, retaskSettleBound, r.stoppedNote())
		}
	}

	// (6) PreWake, with the saved options as they read after the stop.
	if c.PreWake != nil {
		opts, err := readSavedOptionsFunc(r.jobsDir, v.SessionID)
		if err != nil {
			return r.refuse(retaskOptionsDrift, "the options Claude Code saved for session %s can't be read again after the stop (%v)", v.SessionID, err)
		}
		snap.Options = opts
		if err := c.PreWake(ctx, snap); err != nil {
			return r.hookFailed(err)
		}
	}

	// (7) The final re-read. A refusal from here on tells PostCommit, so a
	// caller can undo what PreWake wrote.
	before, res, err := r.finalReread()
	if err != nil {
		if c.PostCommit != nil {
			_ = c.PostCommit(ctx, snap, res)
		}
		return res, err
	}

	// (8) The commit point.
	deliverErr := d.Deliver(ctx, snap, c.Prompt)

	// (9) Verify, remove a copy, and tell PostCommit.
	res, err = r.settleOutcome(before, deliverErr)
	if c.PostCommit != nil {
		if perr := c.PostCommit(ctx, snap, res); perr != nil && err == nil {
			return res, perr
		}
	}
	return res, err
}

// refuse ends the run in state with a refusal.
func (r *retaskRun) refuse(state, format string, args ...any) (retaskResult, error) {
	r.res.State = state
	if r.res.Outcome == retaskDelivered {
		r.res.Outcome = retaskRefused
	}
	return r.res, workerRefuse(verbRetask, r.target, state, format, args...)
}

// fail ends the run with a refusal another function built, taking its state.
func (r *retaskRun) fail(err error) (retaskResult, error) {
	var wr *workerRefusal
	if errors.As(err, &wr) {
		r.res.State = wr.State
	}
	return r.res, err
}

// hookFailed ends the run with the error a caller's hook returned, refused.
func (r *retaskRun) hookFailed(err error) (retaskResult, error) {
	r.res.Outcome = retaskRefused
	return r.fail(err)
}

// rereadUnderLock is step 1 after the lock: the launch record and the
// mapping read again (a CLI caller's), then the instance and the transcript.
func (r *retaskRun) rereadUnderLock() (retaskResult, error) {
	v := r.v
	name := filepath.Base(v.InstancePath)
	if v.Record != nil {
		rec, err := workspace.ReadLaunchRecordChecked(v.Root, v.Record.DispatchID)
		if err != nil {
			return r.refuse(retaskInvalidRecord, "launch record %s: %v", v.Record.DispatchID, err)
		}
		if rec.SessionID != v.SessionID || rec.InstanceName != v.Record.InstanceName || rec.Agent != v.Record.Agent {
			return r.refuse(retaskInvalidRecord, "launch record %s changed while retask was reading it", v.Record.DispatchID)
		}
		name = rec.InstanceName
	}
	if v.Mapping != nil {
		m, err := workspace.ReadSessionMapping(v.Root, v.SessionID)
		if err != nil || m.SessionID != v.SessionID || m.Handle != v.ShortID {
			return r.refuse(retaskInvalidRecord, "the session mapping of session %s changed or can't be read", v.SessionID)
		}
	}
	present, err := checkWorkerInstance(verbRetask, r.target, v.InstancePath, name, v.SessionID)
	if err != nil {
		return r.fail(err)
	}
	if !present {
		return r.refuse(retaskInstanceGone, "instance %s is gone, and retask never restores one; niwa resume %s <prompt> restores it and brings the worker back",
			v.InstancePath, shellToken(r.target))
	}
	home := userHomeDir()
	// The job entries are where the agent declares its session records.
	r.jobsDir = recordStoreRoot(v.Spec.Records, home, os.Getenv)
	if r.jobsDir == "" {
		return r.refuse(retaskStateUnknown, "the home directory can't be resolved, so Claude Code's job state can't be read")
	}
	return r.res, nil
}

// checkBuild is step 2.
func (r *retaskRun) checkBuild() (retaskResult, error) {
	want := fmt.Sprintf("%d.%d.%d", retaskMinClaudeVersion[0], retaskMinClaudeVersion[1], retaskMinClaudeVersion[2])
	bin, err := lookAgentBinary(r.v.Spec.Binary)
	if err != nil {
		return r.refuse(retaskBuildForks, "%s binary not found in PATH, so its build can't be checked", r.v.Spec.Binary)
	}
	out, err := retaskClaudeVersionFunc(r.ctx, bin)
	ver, ok := parseClaudeVersion(out)
	if err != nil || !ok {
		return r.refuse(retaskBuildForks, "could not read the Claude Code build from %s --version; retask needs %s or later, since an older build starts a copy of the session instead of continuing it",
			r.v.Spec.Binary, want)
	}
	if versionBefore(ver, retaskMinClaudeVersion) {
		return r.refuse(retaskBuildForks, "Claude Code %d.%d.%d is older than %s, which retask needs: an older build starts a copy of the session instead of continuing it. Remove the worker with claude rm %s, then bring it back with niwa resume %s <prompt>",
			ver[0], ver[1], ver[2], want, r.v.ShortID, shellToken(r.target))
	}
	return r.res, nil
}

// classify is step 3: the worker's row and job state, read into a state or
// a refusal, then the stray check.
func (r *retaskRun) classify(rows []claudeSessionRow) (workerSnapshot, retaskResult, error) {
	v := r.v
	snap := workerSnapshot{View: v}
	fail := func(state, format string, args ...any) (workerSnapshot, retaskResult, error) {
		res, err := r.refuse(state, format, args...)
		return snap, res, err
	}

	// Another session listed under the mapped short id would be the one a
	// stop by that id reaches, so it refuses before anything else.
	for _, x := range rows {
		if x.ID == v.ShortID && x.SessionID != v.SessionID {
			return fail(retaskStateUnknown, "Claude Code lists short id %s for a session other than %s, so a stop by that id could reach the wrong session", v.ShortID, v.SessionID)
		}
	}
	row, listed := findSessionRow(rows, v.SessionID)
	js, jsErr := readJobStateChecked(r.jobsDir, v.SessionID)
	switch {
	case !listed && jsErr != nil && errors.Is(jsErr, fs.ErrNotExist):
		return fail(retaskJobEntryGone, "Claude Code no longer lists session %s; a worker whose job entry is gone is niwa resume's: niwa resume %s <prompt>", v.SessionID, shellToken(r.target))
	case !listed:
		return fail(retaskStateUnknown, "Claude Code doesn't list session %s, but its job state is still there", v.SessionID)
	case jsErr != nil && errors.Is(jsErr, fs.ErrNotExist):
		return fail(retaskStateUnknown, "Claude Code lists session %s but holds no job state for it", v.SessionID)
	case jsErr != nil:
		return fail(retaskStateUnknown, "the job state of session %s can't be used: %v", v.SessionID, jsErr)
	}
	if row.ID != v.ShortID {
		return fail(retaskStateUnknown, "Claude Code lists session %s under a short id other than the mapped %s", v.SessionID, v.ShortID)
	}
	act := jobActivityFromState(js)
	snap.Row, snap.Activity = row, act

	if act.AwaitingInput {
		return fail(retaskAwaitingInput, "session %s is waiting for an answer; attach to it with claude attach %s", v.SessionID, v.ShortID)
	}
	// The row's own status counts on its own: a worker Claude Code reports
	// busy is refused even when its job state reads idle.
	if row.Status == "busy" {
		return fail(retaskBusy, "session %s is in the middle of a turn; retask hands a worker its next task once it is idle and doesn't queue one", v.SessionID)
	}
	if row.Status != "" && row.Status != "idle" {
		return fail(retaskStateUnknown, "Claude Code reports session %s as %s, which retask doesn't act on", v.SessionID, strconv.Quote(sanitizeRemovalGateText(row.Status, 40)))
	}
	switch watch.ClassifySessionActivity(act) {
	case watch.ActivityDetachedIdle:
	case watch.ActivityAttached:
		return fail(retaskAwaitingInput, "session %s is waiting for an answer; attach to it with claude attach %s", v.SessionID, v.ShortID)
	case watch.ActivityBusy:
		return fail(retaskBusy, "session %s is in the middle of a turn; retask hands a worker its next task once it is idle and doesn't queue one", v.SessionID)
	default:
		return fail(retaskStateUnknown, "the job state of session %s doesn't read as idle", v.SessionID)
	}
	snap.State = stoppedWithEntry
	if row.running() {
		snap.State = liveIdle
	}

	if strays := sessionsRootedAtInstance(rows, v.InstancePath, v.SessionID); len(strays) > 0 {
		return fail(retaskStraySession, "%s; retask doesn't act on an instance holding a session it didn't dispatch, and leaves that session alone", strayNames(strays))
	}
	return snap, r.res, nil
}

// checkOptionsAndPolicy is step 4 for the CLI: the options Claude Code saved
// with the job must be the launch record's, since the wake re-applies them,
// and the recorded permission mode must not be looser than current policy.
func (r *retaskRun) checkOptionsAndPolicy(snap *workerSnapshot) (retaskResult, error) {
	v := r.v
	if v.Record == nil {
		return r.refuse(retaskInvalidRecord, "no launch record to check the worker's saved options against")
	}
	opts, err := readSavedOptionsFunc(r.jobsDir, v.SessionID)
	if err != nil {
		return r.refuse(retaskOptionsDrift, "the options Claude Code saved for session %s can't be checked against its launch record (%v)", v.SessionID, err)
	}
	if flag, why := savedOptionsDrift(opts, *v.Record); flag != "" {
		return r.refuse(retaskOptionsDrift, "the options Claude Code saved for session %s differ from its launch record at %s (%s), and a wake re-applies the saved options",
			v.SessionID, flag, why)
	}
	snap.Options = opts

	posture := ""
	if st, err := workspace.LoadState(v.InstancePath); err == nil && st.ClaudePermissions == v.ConfigPosture {
		posture = st.ClaudePermissions
	}
	current, _ := derivePermissionMode("", v.HostMode, posture, v.Spec.Flags)
	if mode, tightened := stricterPermissionMode(v.Spec.PermissionModes, v.Record.PermissionMode, current); tightened {
		return r.refuse(retaskPolicyTightened, "the worker runs in permission mode %s and current policy gives a new dispatch %s, which is stricter; a wake keeps the recorded mode, so retask won't wake it",
			displayMode(v.Record.PermissionMode), mode)
	}
	return r.res, nil
}

// The outcomes of waiting for a stopped session's row to settle.
type waitResult int

const (
	waitSettled waitResult = iota
	waitGone
	waitTimedOut
)

// waitStopped polls the listing until the row for sessionID, under short,
// shows no pid and no status, or is gone, or the settle bound passes.
func (r *retaskRun) waitStopped(sessionID, short string) waitResult {
	deadline := time.Now().Add(retaskSettleBound)
	for {
		if rows, err := listClaudeSessionsFunc(r.ctx); err == nil {
			row, ok := findSessionRow(rows, sessionID)
			if !ok || row.ID != short {
				return waitGone
			}
			if !row.running() {
				return waitSettled
			}
		}
		if !time.Now().Before(deadline) || retaskSleep(r.ctx, retaskSettlePoll) != nil {
			return waitTimedOut
		}
	}
}

// finalReread is step 7: the worker is still listed and stopped, nothing else
// is rooted at the instance, and its job state is there. It returns the
// session ids rooted at the instance just before the wake.
func (r *retaskRun) finalReread() (map[string]bool, retaskResult, error) {
	v := r.v
	fail := func(state, format string, args ...any) (map[string]bool, retaskResult, error) {
		res, err := r.refuse(state, format, args...)
		return nil, res, err
	}
	rows, err := listClaudeSessionsFunc(r.ctx)
	if err != nil {
		return fail(retaskStateUnknown, "could not list Claude Code's sessions before the wake (%v)%s", err, r.stoppedSuffix())
	}
	row, ok := findSessionRow(rows, v.SessionID)
	if !ok {
		return fail(retaskJobEntryGone, "Claude Code stopped listing session %s before the wake; niwa resume %s <prompt> brings it back", v.SessionID, shellToken(r.target))
	}
	if row.running() || row.ID != v.ShortID {
		return fail(retaskBusy, "session %s is running again, so retask didn't wake it%s", v.SessionID, r.stoppedSuffix())
	}
	if strays := sessionsRootedAtInstance(rows, v.InstancePath, v.SessionID); len(strays) > 0 {
		return fail(retaskStraySession, "%s; retask doesn't act on an instance holding a session it didn't dispatch, and leaves that session alone%s", strayNames(strays), r.stoppedSuffix())
	}
	if _, err := readJobStateChecked(r.jobsDir, v.SessionID); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fail(retaskJobEntryGone, "the job entry of session %s is gone; niwa resume %s <prompt> brings it back", v.SessionID, shellToken(r.target))
		}
		return fail(retaskStateUnknown, "the job state of session %s can't be used: %v%s", v.SessionID, err, r.stoppedSuffix())
	}
	before := map[string]bool{v.SessionID: true}
	for _, x := range sessionsRootedAtInstance(rows, v.InstancePath, "") {
		before[x.SessionID] = true
	}
	return before, r.res, nil
}

// settleOutcome is step 9 up to PostCommit: verify, then remove any copy.
func (r *retaskRun) settleOutcome(before map[string]bool, deliverErr error) (retaskResult, error) {
	v := r.v
	state, copies := r.verify(before)
	switch state {
	case "":
		r.res.Outcome = retaskDelivered
		r.res.State = ""
		return r.res, nil
	case retaskCopyStarted:
		return r.handleCopies(copies, before)
	default:
		r.res.Outcome = retaskUnconfirmed
		why := "Claude Code didn't show it running"
		if deliverErr != nil {
			why = fmt.Sprintf("the wake reported %v", deliverErr)
		}
		return r.refuse(retaskWakeUnconfirmed, "retask woke session %s but couldn't confirm the same session took the task (%s). %s", v.SessionID, why, r.actedNote())
	}
}

// handleCopies removes the copies the wake started and ends the run:
// copy-started when every copy is gone, copy-left with the commands that
// remove the rest by hand otherwise.
func (r *retaskRun) handleCopies(copies []claudeSessionRow, before map[string]bool) (retaskResult, error) {
	v := r.v
	var left []claudeSessionRow
	var reasons []string
	for _, cp := range copies {
		if ok, why := r.removeCopy(cp, before); !ok {
			left = append(left, cp)
			reasons = append(reasons, why)
		}
	}
	r.res.CopyID = copyHandle(copies[0])
	if len(left) == 0 {
		r.res.Outcome = retaskCopyRemoved
		return r.refuse(retaskCopyStarted, "Claude Code started a copy of session %s, short id %s, instead of continuing it, and retask removed the copy. %s %s",
			v.SessionID, copyHandle(copies[0]), r.stoppedNote(), r.actedNote())
	}
	r.res.Outcome = retaskUnconfirmed
	var b strings.Builder
	fmt.Fprintf(&b, "Claude Code started a copy of session %s instead of continuing it, and retask could not remove it (%s). Remove it yourself:", v.SessionID, strings.Join(reasons, "; "))
	for _, cp := range left {
		h := copyHandle(cp)
		fmt.Fprintf(&b, "\nclaude stop %s\nclaude rm %s", h, h)
	}
	fmt.Fprintf(&b, "\n%s %s", r.stoppedNote(), r.actedNote())
	return r.refuse(retaskCopyLeft, "%s", b.String())
}

// verify polls the listing after the wake. A new session rooted at the
// instance is a copy, at once, whatever the mapped row shows; the mapped row
// running with no copy is delivered; the mapped row gone, or the bound
// passing, is unconfirmed. It returns "" when delivered.
func (r *retaskRun) verify(before map[string]bool) (string, []claudeSessionRow) {
	v := r.v
	deadline := time.Now().Add(retaskVerifyBound)
	for {
		if rows, err := listClaudeSessionsFunc(r.ctx); err == nil {
			var copies []claudeSessionRow
			for _, x := range sessionsRootedAtInstance(rows, v.InstancePath, v.SessionID) {
				if !before[x.SessionID] {
					copies = append(copies, x)
				}
			}
			if len(copies) > 0 {
				return retaskCopyStarted, copies
			}
			row, ok := findSessionRow(rows, v.SessionID)
			if !ok {
				return retaskWakeUnconfirmed, nil
			}
			if row.running() && row.ID == v.ShortID {
				return "", nil
			}
		}
		if !time.Now().Before(deadline) || retaskSleep(r.ctx, retaskVerifyPoll) != nil {
			return retaskWakeUnconfirmed, nil
		}
	}
}

// removeCopy removes a copy the wake started, only when all five hold: its
// ids pass niwa's validators; a fresh listing taken just before the removal
// still lists it; its working directory is inside the instance; it isn't the
// worker's own session; and it wasn't rooted at the instance before the wake.
// A copy that is running is stopped and left to settle first. It reports
// whether the copy is gone, or why it was left.
func (r *retaskRun) removeCopy(cp claudeSessionRow, before map[string]bool) (bool, string) {
	v := r.v
	if !safeRetaskHandle(cp.ID) || !workspace.ValidSessionID(cp.SessionID) {
		return false, "Claude Code reported it under an id niwa won't pass to a command"
	}
	if cp.SessionID == v.SessionID {
		return false, "it is the worker's own session"
	}
	if before[cp.SessionID] {
		return false, "it was rooted at the instance before the wake"
	}
	rows, err := listClaudeSessionsFunc(r.ctx)
	if err != nil {
		return false, "Claude Code's sessions could not be listed again"
	}
	fresh, ok := findSessionRow(rows, cp.SessionID)
	if !ok || fresh.ID != cp.ID {
		return false, "Claude Code no longer lists it as it did"
	}
	if !rootedAt(fresh.Cwd, v.InstancePath) {
		return false, "its working directory is outside the instance"
	}
	listed := true
	if fresh.running() {
		if err := stopSessionFunc(r.ctx, cp.ID); err != nil {
			return false, "claude stop failed on it"
		}
		switch r.waitStopped(cp.SessionID, cp.ID) {
		case waitTimedOut:
			return false, "its stop didn't settle"
		case waitGone:
			listed = false
		}
	}
	if listed {
		if _, err := removeClaudeSessionFunc(r.ctx, cp.ID); err != nil {
			return false, "claude rm failed on it"
		}
	}
	rows, err = listClaudeSessionsFunc(r.ctx)
	if err != nil {
		return false, "Claude Code's sessions could not be listed after claude rm"
	}
	if _, still := findSessionRow(rows, cp.SessionID); still {
		return false, "Claude Code still lists it after claude rm"
	}
	return true, ""
}

// stoppedNote is what a run that may have stopped the worker says about it.
func (r *retaskRun) stoppedNote() string {
	note := "The worker may have been left stopped; it stays resumable with another niwa retask."
	if r.v.Mapping != nil && r.v.Mapping.KeepAlive {
		note += " Its keep-alive self-wake is silent until the next wake."
	}
	return note
}

// stoppedSuffix is stoppedNote for a refusal after the stop, "" before it.
func (r *retaskRun) stoppedSuffix() string {
	if !r.res.Stopped {
		return ""
	}
	return ". " + r.stoppedNote()
}

// actedNote is what a run whose wake ran says about the prompt.
func (r *retaskRun) actedNote() string {
	return fmt.Sprintf("The prompt may already have been acted on: check niwa list and claude logs %s before sending it again.", r.v.ShortID)
}

// findSessionRow returns the listing row for sessionID.
func findSessionRow(rows []claudeSessionRow, sessionID string) (claudeSessionRow, bool) {
	for _, x := range rows {
		if x.SessionID == sessionID {
			return x, true
		}
	}
	return claudeSessionRow{}, false
}

// rootedAt reports whether cwd, made canonical, is the instance or lies
// under it at a separator boundary: the rule sessionsRootedAtInstance uses.
func rootedAt(cwd, instancePath string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	return pathWithin(workspace.CanonicalPath(cwd), workspace.CanonicalPath(instancePath))
}

// copyHandle is a copy's short id as a message prints it.
func copyHandle(cp claudeSessionRow) string {
	if safeRetaskHandle(cp.ID) {
		return cp.ID
	}
	return "<its short id in claude agents>"
}

// strayNames names the stray sessions by short id.
func strayNames(strays []claudeSessionRow) string {
	names := make([]string, 0, len(strays))
	for _, s := range strays {
		if safeRetaskHandle(s.ID) {
			names = append(names, s.ID)
		} else {
			names = append(names, "one Claude Code lists under an unusable id")
		}
	}
	return fmt.Sprintf("another Claude Code session is rooted in the instance (%s)", strings.Join(names, ", "))
}

func agentNameOf(v workerView) string {
	if v.Spec.Binary != "" {
		return v.Spec.Binary
	}
	return "worker's"
}

// retaskSleep waits d, or until ctx is done.
func retaskSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// realRetaskClaudeVersion runs `<bin> --version`.
func realRetaskClaudeVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	return string(out), err
}

// claudeVersionRe reads the build from `claude --version`, which prints
// "<major>.<minor>.<patch> (Claude Code)".
var claudeVersionRe = regexp.MustCompile(`^\s*(\d{1,6})\.(\d{1,6})\.(\d{1,6})(?:\s|$)`)

// parseClaudeVersion reads a build number, or reports it can't.
func parseClaudeVersion(out string) ([3]int, bool) {
	m := claudeVersionRe.FindStringSubmatch(out)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// versionBefore reports whether a is an older build than b.
func versionBefore(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// The saved options a wake re-applies.
//
// retaskCheckedOptions are compared with the launch record: each must be
// present exactly when the record has a value, and equal to it.
//
// retaskAllowedSavedOptions are the other flags a dispatch, or a resume of
// one, can leave in a worker's saved options, with whether each takes a
// value: --bg (the launch's leading argument), --agent (the subagent type
// dispatch forwards) and --resume (a worker niwa resume brought back). They
// aren't compared, since the record doesn't hold them, but any flag on
// neither list refuses: a wake would re-apply something niwa never launched.
var (
	retaskCheckedOptions      = []string{"--permission-mode", "--name", "--model", "--settings"}
	retaskAllowedSavedOptions = map[string]bool{
		"--bg":     false,
		"--agent":  true,
		"--resume": true,
	}
)

// retaskOptionNameRe is the shape a flag name must have to be printed in a
// refusal.
var retaskOptionNameRe = regexp.MustCompile(`^--[a-z0-9-]+$`)

// savedOptionsDrift compares the saved options with the launch record and
// returns the flag where they differ and why, or "" when they agree. Both
// "--flag value" and "--flag=value" are read. It never returns a value, since
// the settings document carries environment values.
func savedOptionsDrift(opts []string, rec workspace.LaunchRecord) (string, string) {
	saved := map[string]string{}
	for i := 0; i < len(opts); i++ {
		name, value, joined := strings.Cut(opts[i], "=")
		if !retaskOptionNameRe.MatchString(name) {
			return "an unrecognized option", "not a flag retask knows"
		}
		checked := slices.Contains(retaskCheckedOptions, name)
		takesValue, allowed := retaskAllowedSavedOptions[name]
		if !checked && !allowed {
			return name, "not a flag niwa launches a worker with"
		}
		if checked {
			takesValue = true
		}
		switch {
		case takesValue && !joined:
			if i+1 >= len(opts) {
				return name, "it has no value"
			}
			i++
			value = opts[i]
		case !takesValue && joined:
			return name, "it takes no value"
		}
		if checked {
			if _, dup := saved[name]; dup {
				return name, "it is saved more than once"
			}
			saved[name] = value
		}
	}
	for _, f := range []struct{ name, recorded string }{
		{"--permission-mode", rec.PermissionMode},
		{"--name", rec.SessionName},
		{"--model", rec.Model},
		{"--settings", rec.Settings},
	} {
		value, ok := saved[f.name]
		switch {
		case ok && f.recorded == "":
			return f.name, "saved, but the record has none"
		case !ok && f.recorded != "":
			return f.name, "recorded, but not saved"
		case !ok:
		case f.name == "--settings":
			if !sameJSONDocument(value, f.recorded) {
				return f.name, "the saved document isn't the recorded one"
			}
		case value != f.recorded:
			return f.name, "the saved value isn't the recorded one"
		}
	}
	return "", ""
}

// sameJSONDocument reports whether two settings documents hold the same
// JSON value, or, when either doesn't parse, the same bytes.
func sameJSONDocument(a, b string) bool {
	var av, bv any
	if json.Unmarshal([]byte(a), &av) != nil || json.Unmarshal([]byte(b), &bv) != nil {
		return a == b
	}
	return reflect.DeepEqual(av, bv)
}
