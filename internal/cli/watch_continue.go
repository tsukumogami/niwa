package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// saveStagedRecordFunc is the seam over watch.SaveStagedRecord for the
// continuation's two writes (the pending marker and the outcome), so a test
// can fail a write or check that it happens under the instance lock.
var saveStagedRecordFunc = watch.SaveStagedRecord

// watch's own state words for a continuation it defers. Each names one
// refusal of watch's hooks; the core's refusals keep the core's words.
const (
	watchDeferLiveness       = "liveness"
	watchDeferRecordChanged  = "record-changed"
	watchDeferSavedOptions   = "saved-options"
	watchDeferSettingsFile   = "settings-file"
	watchDeferReviewSettings = "review-settings"
	watchDeferSettingsDigest = "settings-digest"
	watchDeferMissingDigest  = "missing-digest"
	watchDeferMarkerWrite    = "marker-write"
)

// watchDeferError is a refusal by one of watch's continuation hooks. It is not
// a *workerRefusal, so the core hands it back unchanged and continueReview can
// tell a gate refusal (a defer line, no pass error) from a real failure. The
// reason names paths, flags and state words only, never file content, a
// settings value or an environment value.
type watchDeferError struct {
	State, Reason string
}

func (e *watchDeferError) Error() string { return e.State + ": " + e.Reason }

func watchDefer(state, format string, args ...any) error {
	return &watchDeferError{State: state, Reason: fmt.Sprintf(format, args...)}
}

// watchHaltStates are the core outcomes after the wake ran: the prompt may
// already have been acted on, so the record stays halted and the pass fails
// rather than deferring.
var watchHaltStates = map[string]bool{
	retaskCopyStarted:     true,
	retaskCopyLeft:        true,
	retaskWakeUnconfirmed: true,
}

// watchCoreDeferReasons is watch's own text for each core refusal it defers
// on, so a watch user never reads a retask message. Every core state word but
// the halt words has an entry (TestWatchDeferReasonsCoverCoreStates). The
// concurrency entry is replaced by the lock's own reason, which names the
// holder.
var watchCoreDeferReasons = map[string]string{
	retaskUnknownTarget:    "the review session couldn't be found",
	retaskAmbiguousTarget:  "the review session matched more than one session",
	retaskInvalidRecord:    "the staged record's session id, short id or instance path can't be used",
	retaskInstanceGone:     "the review instance is gone; the record is discarded once its session is gone",
	retaskInstanceOccupied: "something other than the review instance occupies its path",
	retaskTranscriptGone:   "the review session's transcript is gone, so there is nothing to continue",
	retaskJobEntryGone:     "the review session's job entry is gone; the record is discarded and a fresh review staged on a later pass",
	retaskStateUnknown:     "the review session's state couldn't be read as idle",
	retaskAwaitingInput:    "the review session is waiting for an answer",
	retaskBusy:             "the review session is in the middle of a turn",
	retaskPolicyTightened:  "the permission policy for the review session changed",
	retaskOptionsDrift:     "the options saved with the review session can't be read after the stop",
	retaskStraySession:     "another session is rooted in the review instance",
	retaskConcurrency:      "another niwa command is acting on the review instance",
	retaskAgentUnsupported: "the review session's agent can't be woken in place",
	retaskBuildForks: fmt.Sprintf("the installed Claude Code is older than %d.%d.%d, whose wake starts a copy of the session instead of continuing it",
		retaskMinClaudeVersion[0], retaskMinClaudeVersion[1], retaskMinClaudeVersion[2]),
	retaskStopFailed:    "stopping the review session failed; a later pass tries again",
	retaskSettleTimeout: "the review session's stop didn't settle in time; a later pass tries again",
}

// watchContinuation is one continuation of a staged review: what continueReview
// read, and the hooks it hands the retask core. The hooks run under the
// instance lock the core holds.
type watchContinuation struct {
	root, token string
	pr          github.PRRef
	// rec is the staged record as the sweep read it, until the marker write
	// replaces it with what is on disk.
	rec     watch.StagedRecord
	inst    string // rec.InstancePath, cleaned
	head    github.PullHead
	sandbox bool
	stderr  io.Writer

	ask       bool  // the write posture the gate resolved
	marked    bool  // the pending marker is on disk
	commitErr error // a record write after PreWake that failed
}

// continueReview continues a live, idle review session at the PR's new head
// in the same session: same session id, same short id, same job entry. It
// hands the session its fixed re-review prompt through the retask core, which
// takes the instance lock, checks the build, classifies the session, stops a
// live one and waits for the stop to settle, wakes it with
// `--bg --resume <id> -- <prompt>` and nothing else, and removes a copy the
// wake started. continueReview takes no lock of its own.
//
// A flagless wake carries no containment on its command line: the session
// re-applies the options it was staged with, and reads its settings from the
// instance at start. So watch's hooks check both before the wake (see preStop
// and preWake), and a pending marker in the staged record makes the outcome
// durable. Any refusal before the wake prints one defer line and returns nil;
// a copy or an unconfirmed wake halts the record and returns an error. The
// sandbox posture is the plan runWatchOnce already resolved fail-closed, so a
// continuation never runs on a host whose sandbox a stage would refuse.
//
// Continue is cap-neutral: it reuses an already-counted live agent.
func continueReview(cmd *cobra.Command, root, _, token string, client *github.APIClient, pr github.PRRef, rec watch.StagedRecord, plan reviewPlan) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// The ids are validated again before they become arguments of a claude
	// verb; the core checks them a third time.
	if !workspace.ValidSessionID(rec.SessionID) {
		return fmt.Errorf("captured session id is not a valid UUID")
	}
	if !watch.IsSafeHandle(rec.ShortID) {
		return fmt.Errorf("captured short id is unsafe")
	}
	if rec.InstancePath == "" || !filepath.IsAbs(rec.InstancePath) {
		return fmt.Errorf("staged record has no absolute instance path")
	}
	inst := filepath.Clean(rec.InstancePath)

	// The head is looked up before anything is stopped; it is fetched into
	// the clone only after the stop settles (preWake).
	head, err := client.GetPullHead(ctx, pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return err
	}

	wc := &watchContinuation{
		root: root, token: token, pr: pr, rec: rec, inst: inst, head: head,
		sandbox: plan.sandbox, stderr: cmd.ErrOrStderr(),
	}
	_, unset := loadDispatchEnvLists(root)
	staged := rec
	v := workerView{
		SessionID:    rec.SessionID,
		ShortID:      rec.ShortID,
		InstancePath: inst,
		Staged:       &staged,
		Root:         root,
		Spec:         claudeLaunchSpec(),
		Unset:        unset,
	}
	res, err := retaskCore(ctx, v, retaskCaller{
		Kind:       retaskCallerWatch,
		Prompt:     watch.BuildResumePrompt(watch.DefaultCloneRelDir, watch.DefaultDraftRelPath),
		Target:     wc.prName(),
		PreStop:    wc.preStop,
		PreWake:    wc.preWake,
		PostCommit: wc.postCommit,
	})
	return wc.report(cmd.OutOrStdout(), res, err)
}

func (wc *watchContinuation) prName() string {
	return fmt.Sprintf("%s/%s#%d", wc.pr.Owner, wc.pr.Repo, wc.pr.Number)
}

// report maps the core's answer to watch's own output. The core's own text is
// never printed: it speaks of retask, which a watch user never ran.
func (wc *watchContinuation) report(out io.Writer, res retaskResult, err error) error {
	// A record write after PreWake that failed is a pass error whatever the
	// core answered: the record on disk may still hold the marker.
	if wc.commitErr != nil {
		return wc.commitErr
	}
	if err == nil {
		fmt.Fprintf(out, "niwa watch: continued review for %s (handle %s)%s\n",
			wc.prName(), wc.rec.Handle, reviewWritePosture(wc.sandbox, wc.ask))
		return nil
	}
	var de *watchDeferError
	if errors.As(err, &de) {
		wc.printDefer(out, de.State, de.Reason)
		return nil
	}
	var wr *workerRefusal
	if !errors.As(err, &wr) {
		// The head fetch failed, or something else that isn't a refusal.
		return err
	}
	state := res.State
	if state == "" {
		state = wr.State
	}
	if watchHaltStates[state] {
		return fmt.Errorf("%s: %s", state, wc.haltText(state, res))
	}
	reason, ok := watchCoreDeferReasons[state]
	if state == retaskConcurrency {
		// The lock's reason names the holder or the missing file locking.
		reason = wr.Detail
	}
	if !ok || reason == "" {
		reason = "the continuation was refused"
	}
	if state == "" {
		state = "refused"
	}
	wc.printDefer(out, state, reason)
	return nil
}

func (wc *watchContinuation) printDefer(out io.Writer, state, reason string) {
	fmt.Fprintf(out, "niwa watch: deferring continuation of %s: %s: %s\n", wc.prName(), state, reason)
}

// haltText is what a halted continuation tells the user: the wake ran, so the
// prompt may already have been acted on, and watch won't send it again. The
// record stays halted until the review session is dismissed, after which a
// later pass stages a fresh review.
func (wc *watchContinuation) haltText(state string, res retaskResult) string {
	var b strings.Builder
	switch state {
	case retaskCopyStarted:
		b.WriteString("the wake started a second session at the review instance instead of continuing the review session for " + wc.prName() + ", and the second session was removed")
	case retaskCopyLeft:
		b.WriteString("the wake started a second session at the review instance instead of continuing the review session for " + wc.prName() + ", and it couldn't be removed")
		if safeRetaskHandle(res.CopyID) {
			b.WriteString(" (short id " + res.CopyID + ")")
		}
		b.WriteString("; check claude agents for a second session rooted at " + wc.inst + " and remove it")
	default:
		b.WriteString("the wake ran, but the review session for " + wc.prName() + " wasn't seen running; check claude agents for a second session rooted at " + wc.inst)
	}
	fmt.Fprintf(&b, ". The staged review is halted at %s: the re-review prompt may already have been acted on, so watch won't send it again. Dismiss the review session with claude rm %s and a later pass stages a fresh review",
		shortSHA(wc.head.SHA), wc.rec.ShortID)
	return b.String()
}

// shortSHA is a head as a message prints it.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// preStop runs under the lock, after the core classified the session and
// before it stops anything. Every refusal is a *watchDeferError.
func (wc *watchContinuation) preStop(_ context.Context, _ workerSnapshot) error {
	// The core re-reads only a CLI caller's records, so the staged record is
	// read again here, under the lock: another pass may have continued,
	// halted or discarded it since this pass's sweep read it.
	cur, err := watch.LoadStagedRecord(wc.root, wc.rec.Handle)
	if err != nil {
		return watchDefer(watchDeferRecordChanged, "the staged record can't be read again under the instance lock")
	}
	if cur.SessionID != wc.rec.SessionID || cur.ShortID != wc.rec.ShortID ||
		cur.ContinuationHalted != wc.rec.ContinuationHalted || cur.SettingsDigest != wc.rec.SettingsDigest ||
		cur.DispatchedSHA != wc.rec.DispatchedSHA || cur.InstancePath != wc.rec.InstancePath {
		return watchDefer(watchDeferRecordChanged, "the staged record changed since this pass read it")
	}

	// The two-way liveness cross-check, read fresh under the lock.
	jobsDir := defaultJobsDir()
	sid := wc.rec.SessionID
	if !sessionLive(jobsDir, sid, timeNow()) || !instanceHasLiveJob(jobsDir, wc.inst) {
		return watchDefer(watchDeferLiveness, "the review session's job entry and the review instance's live job don't agree")
	}
	// sessionLive and instanceHasLiveJob only see that the entry is there. A
	// fresh checked read, classified again, catches a turn that started after
	// the core's listing.
	js, err := readJobStateChecked(jobsDir, sid)
	if err != nil || watch.ClassifySessionActivity(jobActivityFromState(js)) != watch.ActivityDetachedIdle {
		return watchDefer(watchDeferLiveness, "the review session's job state doesn't read as detached and idle")
	}

	opts, err := readSavedOptionsFunc(jobsDir, sid)
	if err != nil {
		return watchDefer(watchDeferSavedOptions, "the options saved with the review session can't be read")
	}
	if err := watch.CheckReviewSavedOptions(opts, wc.sandbox); err != nil {
		return watchDefer(watchDeferSavedOptions, "%s", savedOptionsReason(err))
	}
	return nil
}

// savedOptionsReason is a saved-options refusal as a defer line prints it.
// CheckReviewSavedOptions' error names a flag and never a value.
func savedOptionsReason(err error) string {
	var se *watch.SavedOptionsError
	if errors.As(err, &se) {
		return se.Error()
	}
	return "the saved options were refused"
}

// preWake runs under the lock, after the stop settled and before the core's
// final re-read and wake. Its order is fixed: the instance as it stood before
// anything is written, the head fetch, the gate, and last the pending marker.
// Every refusal comes before the marker.
func (wc *watchContinuation) preWake(ctx context.Context, snap workerSnapshot) error {
	// Taken first, so the digest comparison sees what the review session left
	// behind and not what the gate re-applies.
	pre := snapshotReviewSettings(wc.inst)

	// The fetch replaces the clone's contents, so it runs only now that no
	// session process is running in the instance. Its failure is a plain
	// error: the pass fails, the worker stays stopped with its job entry, and
	// a later pass continues it.
	cloneURL := wc.head.CloneURL
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://github.com/%s/%s.git", wc.pr.Owner, wc.pr.Repo)
	}
	if err := fetchPRHeadFunc(ctx, cloneURL, wc.head.SHA, filepath.Join(wc.inst, watch.DefaultCloneRelDir), wc.token); err != nil {
		return fmt.Errorf("fetching PR head: %w", err)
	}

	ask, post, err := runReviewWakeGate(wc.stderr, wc.inst, wc.rec, wc.sandbox, snap.Options, pre)
	if err != nil {
		return err
	}
	wc.ask = ask

	// The marker names the head the wake is about to send. It goes with the
	// digest as it stands after the re-apply, so a change only niwa made (a
	// new guard binary path after an upgrade) doesn't refuse the next wake,
	// while an edit by the session still shows up in the next pre-apply
	// digest. The gate has already refused a post digest that differs from
	// pre anywhere but .claude/settings.json.
	nr := wc.rec
	nr.ContinuationHalted = wc.head.SHA
	nr.SettingsDigest = post
	if err := saveStagedRecordFunc(wc.root, nr); err != nil {
		return watchDefer(watchDeferMarkerWrite, "the staged record can't be written, so the wake isn't sent")
	}
	wc.rec = nr
	wc.marked = true
	return nil
}

// postCommit settles the staged record by the core's outcome.
func (wc *watchContinuation) postCommit(_ context.Context, _ workerSnapshot, r retaskResult) error {
	o, repo, n := wc.pr.Owner, wc.pr.Repo, wc.pr.Number
	switch r.Outcome {
	case retaskDelivered:
		// The handled set first: if either write fails, the marker is still on
		// disk, the record stays halted, and no second prompt goes out.
		if err := watch.AppendHandled(wc.root, o, repo, n, wc.head.SHA); err != nil {
			wc.commitErr = fmt.Errorf("the review session for %s took the re-review prompt, but recording head %s failed (%w); the staged review stays halted, so dismiss it with claude rm %s",
				wc.prName(), shortSHA(wc.head.SHA), err, wc.rec.ShortID)
			return wc.commitErr
		}
		nr := wc.rec
		nr.ContinuationHalted = ""
		nr.DispatchedSHA = wc.head.SHA
		if err := saveStagedRecordFunc(wc.root, nr); err != nil {
			wc.commitErr = fmt.Errorf("the review session for %s took the re-review prompt, but its staged record couldn't be written (%w); the staged review stays halted, so dismiss it with claude rm %s",
				wc.prName(), err, wc.rec.ShortID)
			return wc.commitErr
		}
		wc.rec = nr
	case retaskRefused:
		// Reached only when the final re-read refused after preWake wrote the
		// marker: nothing was sent, so the marker alone is cleared. The
		// post-apply digest stays, since it matches the instance on disk.
		if !wc.marked {
			return nil
		}
		nr := wc.rec
		nr.ContinuationHalted = ""
		if err := saveStagedRecordFunc(wc.root, nr); err != nil {
			// The core ignores PostCommit's error on this path, so it is kept
			// for report.
			wc.commitErr = fmt.Errorf("clearing the pending continuation of %s failed (%w); the staged review stays halted, so dismiss it with claude rm %s",
				wc.prName(), err, wc.rec.ShortID)
			return wc.commitErr
		}
		wc.rec = nr
		wc.marked = false
	default:
		// A copy, or a wake that couldn't be confirmed: the prompt may have
		// gone out, so the marker, the ids and the handled set stay as they are.
	}
	return nil
}

// reviewSettingsSnapshot is the digest of what the review session loads from
// the instance root, as it stood at one moment.
type reviewSettingsSnapshot struct {
	digest string
	err    error
}

// snapshotReviewSettings takes the instance's settings digest.
func snapshotReviewSettings(inst string) reviewSettingsSnapshot {
	d, err := watch.ReviewSettingsDigest(inst)
	return reviewSettingsSnapshot{digest: d, err: err}
}

// runReviewWakeGate is the containment gate a review session passes before
// every wake, in this order: resolve the write posture; re-apply the file
// layer (watch.ApplyReviewSettings) and verify it from disk; refuse when
// .claude/settings.local.json or .mcp.json exists; compare the recorded
// settings digest with pre, the instance as it stood before the re-apply; and
// check the saved option argv opts. It returns the write posture and the
// digest after the re-apply, which the caller records with the pending marker,
// once that digest is checked to differ from pre only in .claude/settings.json.
//
// Every refusal is a *watchDeferError whose reason names paths, flags and
// state words only. It writes the instance's settings (the re-apply) and may
// seed the instance's Claude trust, but never the staged record.
func runReviewWakeGate(stderr io.Writer, inst string, rec watch.StagedRecord, sandbox bool, opts []string, pre reviewSettingsSnapshot) (bool, string, error) {
	ask := resolveAskPosture(stderr, inst, sandbox)

	// The errors of the re-apply and the verify can quote a settings value,
	// so the defer line names the file and the kinds of cause instead.
	if err := watch.ApplyReviewSettings(inst, sandbox, ask); err != nil {
		return false, "", watchDefer(watchDeferReviewSettings,
			"the review settings in .claude/settings.json couldn't be re-applied; a settings file that doesn't parse, or one that sets disableAllHooks, refuses the wake")
	}
	merged, err := readReviewSettings(inst)
	if err != nil || watch.VerifyReviewSettings(merged, inst, sandbox, ask) != nil {
		return false, "", watchDefer(watchDeferReviewSettings,
			"the review settings in .claude/settings.json don't verify after re-applying them")
	}

	// Claude Code loads these from the session's working directory, and
	// neither is ever part of a review: their presence means something wrote
	// into the instance.
	for _, rel := range []string{".claude/settings.local.json", ".mcp.json"} {
		if _, err := os.Lstat(filepath.Join(inst, filepath.FromSlash(rel))); err == nil {
			return false, "", watchDefer(watchDeferSettingsFile, "%s exists in the review instance, and the session would load it", rel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, "", watchDefer(watchDeferSettingsFile, "%s in the review instance can't be checked", rel)
		}
	}

	// A record without a digest is never woken: there is nothing to compare
	// the instance against, so the comparison is refused, not skipped.
	if rec.SettingsDigest == "" {
		return false, "", watchDefer(watchDeferMissingDigest,
			"the staged record holds no settings digest, so the review instance can't be checked; dismiss the review session with claude rm %s and a later pass stages a fresh review", rec.ShortID)
	}
	if pre.err != nil {
		return false, "", watchDefer(watchDeferSettingsDigest, "the review instance's settings digest can't be taken")
	}
	changed, err := watch.ReviewSettingsDigestChanges(rec.SettingsDigest, pre.digest)
	if err != nil {
		return false, "", watchDefer(watchDeferSettingsDigest,
			"the staged record's settings digest can't be read; dismiss the review session with claude rm %s and a later pass stages a fresh review", rec.ShortID)
	}
	if len(changed) > 0 {
		return false, "", watchDefer(watchDeferSettingsDigest, "%s", digestRefusalText(changed, rec.ShortID))
	}

	if err := watch.CheckReviewSavedOptions(opts, sandbox); err != nil {
		return false, "", watchDefer(watchDeferSavedOptions, "%s", savedOptionsReason(err))
	}

	post, err := watch.ReviewSettingsDigest(inst)
	if err != nil {
		return false, "", watchDefer(watchDeferSettingsDigest, "the review instance's settings digest can't be taken after re-applying its settings")
	}
	// post becomes the record's baseline, so it must differ from pre only by
	// the re-apply. Anything else changed between the pre-apply digest and now
	// (during the fetch or the gate, by a process the stop didn't end) would
	// otherwise be recorded as the starting point and never refuse a wake.
	// The re-apply rewrites .claude/settings.json and nothing else.
	window, err := watch.ReviewSettingsDigestChanges(pre.digest, post)
	if err != nil {
		return false, "", watchDefer(watchDeferSettingsDigest, "the review instance's settings digests can't be compared")
	}
	window = slices.DeleteFunc(window, func(p string) bool { return p == ".claude/settings.json" })
	if len(window) > 0 {
		return false, "", watchDefer(watchDeferSettingsDigest, "%s", digestWindowRefusalText(window, rec.ShortID))
	}
	return ask, post, nil
}

// digestMaxNamed caps how many changed paths a digest refusal names.
const digestMaxNamed = 10

// digestRefusalText is the settings-digest refusal: the changed paths,
// relative to the instance and quoted, what that means and what to do. It
// names no content and no value.
func digestRefusalText(changed []string, short string) string {
	return fmt.Sprintf("the review instance changed since the review was staged (%s); a niwa apply in the instance since then also causes this, not only an edit by the review agent; dismiss the review session with claude rm %s and a later pass stages a fresh review",
		digestPathList(changed), short)
}

// digestWindowRefusalText is the settings-digest refusal for a change made
// while the continuation fetched the head and ran the gate. Like
// digestRefusalText it names paths only.
func digestWindowRefusalText(changed []string, short string) string {
	return fmt.Sprintf("the review instance changed while the wake was being prepared (%s); something is still writing into the instance; dismiss the review session with claude rm %s and a later pass stages a fresh review",
		digestPathList(changed), short)
}

// digestPathList quotes up to digestMaxNamed changed paths and counts the
// rest.
func digestPathList(changed []string) string {
	named := changed
	if len(named) > digestMaxNamed {
		named = named[:digestMaxNamed]
	}
	quoted := make([]string, len(named))
	for i, p := range named {
		quoted[i] = strconv.Quote(p)
	}
	list := strings.Join(quoted, ", ")
	if extra := len(changed) - len(named); extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	return list
}

// readReviewSettings reads the instance's .claude/settings.json.
func readReviewSettings(inst string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
	if err != nil {
		return nil, err
	}
	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		return nil, err
	}
	return merged, nil
}
