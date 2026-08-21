package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchBackstopTTL bounds how long a dispatch-named, unmapped instance may
// sit before the reaper backstop reclaims it. It is chosen far longer than the
// worst-case dispatch wall-clock (a clone plus a bounded capture poll is
// seconds to low tens of seconds), so a healthy in-flight dispatch is always
// younger than the TTL and is never reaped (DESIGN Decision 4, R38).
//
// This TTL governs ONLY the name+TTL backstop for UNMAPPED orphan instances
// (the SIGKILL-before-mapping case, selectBackstopTargets). It is unrelated to
// session liveness: a MAPPED instance is reclaimed by the primary sweep on the
// entry-present rule (sessionLive, job_state.go), never on this TTL. The two
// concerns are deliberately separate -- this constant ages unmapped instances,
// not job-state.
const dispatchBackstopTTL = 30 * time.Minute

func init() {
	rootCmd.AddCommand(reapCmd)
}

var reapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Reclaim ephemeral instances whose backing session was deleted",
	Long: `Reclaim ephemeral instances whose session was deleted.

reap enumerates the workspace's instances, joins each against its
session->instance mapping, and force-destroys an instance only when BOTH hold:

  - the instance is marked ephemeral (provisioned for a session), and
  - its session is dead by the liveness rule: the session record its agent
    writes is GONE (the proxy for the developer deleting the session).

Teardown is delete-only. A session that finished its task, went idle, or was
suspended keeps its record -- and so keeps its instance, which stays resumable
-- and is reclaimed only once that record disappears. A non-ephemeral
(developer) instance is NEVER targeted, and an instance is NEVER reaped without
the ephemeral marker.

Some instances are spared rather than judged, and reap says so on stderr when
it happens. The liveness rule needs a record that disappears when a session is
deleted, and not every agent keeps one: an agent whose session records are
never removed leaves no way to tell a live session from a deleted one, and an
instance whose session cannot be proven gone is never reclaimed. The same holds
for a dispatch instance with no mapping at all, which reap otherwise reclaims on
age -- if an agent's records say a worker was started there, reap cannot tell
whether it is still writing, so it leaves the directory alone. Spared instances
pile up until you remove them yourself with niwa destroy. Sparing an instance
nobody is using costs a directory; the other mistake costs the work inside it.

reap runs on demand and is also invoked opportunistically at the start of
niwa create and niwa dispatch so session fan-out self-bounds.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runReap,
}

func runReap(cmd *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("not inside a niwa workspace")
	}

	n, err := reapWorkspace(class.WorkspaceRoot, defaultJobsDir(), time.Now())
	if err != nil {
		return err
	}
	if n > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Reaped %d orphaned ephemeral instance(s)\n", n)
	}
	return nil
}

// reapTarget pairs an instance the reaper has selected for reclamation with the
// session mapping that justifies it. The session id is carried so the mapping
// entry can be deleted after the instance is destroyed.
type reapTarget struct {
	SessionID    string
	InstancePath string
}

// maxSparedNamesListed bounds how many instance names one line carries before
// it stops naming them individually.
const maxSparedNamesListed = 3

// reportSparedInstances says what the sweep left alone and why.
//
// It goes to stderr rather than to the command's own output because the sweep
// runs at the top of a dispatch, a create, and a watch -- underneath something
// the developer actually asked for -- so this is a note about the sweep rather
// than a result of that command.
//
// It is deliberately two lines rather than one per instance. The condition is
// permanent: an instance whose liveness cannot be read is spared on every sweep
// forever, and every sweep runs under a command somebody ran for another
// reason. A report that grew with the number of spared instances would be
// training to ignore it within a day, and a warning nobody reads crowds out
// the ones they would have read.
func reportSparedInstances(w io.Writer, spared []sparedInstance) {
	if len(spared) == 0 {
		return
	}
	byReason := map[string][]string{}
	var order []string
	for _, s := range spared {
		if _, seen := byReason[s.Reason]; !seen {
			order = append(order, s.Reason)
		}
		byReason[s.Reason] = append(byReason[s.Reason], s.Name)
	}
	for _, reason := range order {
		names := byReason[reason]
		listed := names
		suffix := ""
		if len(listed) > maxSparedNamesListed {
			listed = listed[:maxSparedNamesListed]
			suffix = fmt.Sprintf(" and %d more", len(names)-maxSparedNamesListed)
		}
		fmt.Fprintf(w, "niwa: spared %d instance(s) niwa cannot tell are finished (%s%s): %s\n",
			len(names), strings.Join(listed, ", "), suffix, reason)
	}
	// The name rather than the path, because that is what the command takes.
	fmt.Fprintf(w, "niwa: reclaim one with `niwa destroy <name>` when you are done with its session\n")
}

// sparedInstance is an instance the sweep left alone because it could not read
// whether the session was still there, with the reason in the words a user
// needs to understand why their disk is not getting emptier.
//
// Name rather than path, because `niwa destroy` takes a name and a report that
// prints one thing while telling you to type another is a report you have to
// translate before you can act on it.
type sparedInstance struct {
	Name   string
	Reason string
}

// livenessUnreadable reports whether a mapping's recorded agent leaves the
// sweep with no way to tell a live session from a deleted one, and why.
//
// Three shapes reach here and all three mean the same thing to the sweep. An
// agent outside the accepted set is a mapping written by something this build
// does not understand. An agent niwa launches no background worker for has no
// record store to read at all. And an agent whose records are never removed has
// a store that says a session once existed and nothing about whether it still
// does -- reading it would answer a different question than the one being
// asked.
func livenessUnreadable(recorded string) (string, bool) {
	ag, err := agent.ParseAgent(recorded)
	if err != nil {
		return fmt.Sprintf("its mapping records agent %q, which this build does not recognize", recorded), true
	}
	spec, ok := agentplan.For(ag).LaunchSpec()
	if !ok {
		return fmt.Sprintf("niwa launches no background worker for %s, so there are no session records to read", ag), true
	}
	if spec.Records.Liveness != agentplan.LivenessRecordPresence {
		return fmt.Sprintf("%s never removes a session's record, so its presence cannot tell a live session from a deleted one", ag), true
	}
	return "", false
}

// selectReapTargets joins the workspace's instances against their session
// mappings and returns the targets eligible for reclamation. An instance is
// eligible only when it is marked ephemeral AND its session is dead by
// sessionLive (DESIGN Decision 6, R11).
//
// The join is keyed on instance_path: EnumerateInstanceRecords supplies the set
// of instances actually on disk (and whether each is ephemeral), while the
// mapping supplies the session_id liveness key. An instance with no mapping is
// never a target (no session to declare dead, and no ephemeral provenance). A
// mapping whose instance is gone from disk is skipped here; its stale mapping
// entry is pruned separately.
//
// This function performs NO destruction and touches no instance directory, so
// the selection logic is unit-testable against fixture mappings and a fixture
// jobs tree, independent of the real destroy path.
func selectReapTargets(workspaceRoot, jobsDir string, now time.Time) ([]reapTarget, []sparedInstance, error) {
	var unreadable []sparedInstance

	records, err := workspace.EnumerateInstanceRecords(workspaceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("enumerating instances: %w", err)
	}

	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("listing session mappings: %w", err)
	}
	byPath := make(map[string]workspace.SessionMapping, len(mappings))
	for _, m := range mappings {
		if m.InstancePath != "" {
			byPath[m.InstancePath] = m
		}
	}

	var targets []reapTarget
	for _, rec := range records {
		// Never target a developer instance. The ephemeral marker is the
		// load-bearing guard: without it, no TTL or dead session can justify
		// reclamation.
		if !rec.Ephemeral {
			continue
		}

		mapping, ok := byPath[rec.Path]
		if !ok {
			// Marked ephemeral by the store but no resolvable mapping to read a
			// session id from: skip rather than guess. Without a session id the
			// liveness rule cannot run, and reaping on the marker alone would
			// risk an instance whose session is still live.
			continue
		}

		// Double-check the mapping itself carries the ephemeral marker. The
		// record's Ephemeral flag is derived from the store, but reading it
		// straight off the mapping keeps the never-reap-non-ephemeral guarantee
		// local to this decision.
		if !mapping.Ephemeral {
			continue
		}

		// Which liveness rule applies is the launching agent's own
		// declaration, and the mapping records which agent that was. An agent
		// niwa launches no worker for, one whose sessions leave no signal that
		// distinguishes a live session from a deleted one, or a mapping whose
		// recorded agent will not parse at all, each give this reaper no
		// evidence -- and with no evidence it must not act. Sparing an instance
		// nobody is using costs a directory; reclaiming one a resumable session
		// still lives in costs the work in it, which is the failure this whole
		// rule exists to prevent.
		//
		// It is reported rather than skipped in silence, for the reason
		// reportSparedInstances gives: this is the runtime half of a gap the
		// capability table declares, and a declared gap nobody can observe
		// while it is happening is only half declared.
		if reason, spared := livenessUnreadable(mapping.Agent); spared {
			name := mapping.InstanceName
			if name == "" {
				name = filepath.Base(rec.Path)
			}
			unreadable = append(unreadable, sparedInstance{Name: name, Reason: reason})
			continue
		}

		if sessionLive(jobsDir, mapping.SessionID, now) {
			// The session still exists (running or idle-but-resumable): its job
			// entry is present, so spare the instance. Only a deleted session
			// (entry gone) is reclaimed.
			continue
		}

		// Defense in depth against the data-loss class this reaper fixes: even
		// if the mapping-keyed liveness rule above reads the session as dead
		// (a stale or mis-keyed mapping), never destroy an instance a live
		// Claude Code session is currently rooted in. A dispatched worker
		// launches with cmd.Dir == its instance, so its job-state cwd points at
		// the instance directory; if any present job's cwd resolves inside this
		// instance, a session is still working there and it must be spared.
		//
		// The record check is the same guard read agent-neutrally, for a worker
		// whose harness keeps no job state niwa can see. Its reason is dropped
		// here rather than reported: an instance that reaches this line already
		// passed the liveness rule above, so anything permanent about it was
		// reported there, and saying it twice about one instance would train
		// the developer to skim both.
		_, recorded := instanceHasRecordedSession(rec.Path)
		if instanceHasLiveJob(jobsDir, rec.Path) || recorded {
			continue
		}

		targets = append(targets, reapTarget{
			SessionID:    mapping.SessionID,
			InstancePath: rec.Path,
		})
	}

	return targets, unreadable, nil
}

// reapWorkspace selects and reclaims orphaned ephemeral instances under
// workspaceRoot, returning the count actually destroyed. For each target it
// force-destroys the instance (via destroyInstanceFunc, the non-interactive
// destroy path) and deletes the mapping entry. A destroy
// failure on one target is surfaced on stderr and does not abort the rest, so a
// single stuck instance never blocks reclaiming the others.
func reapWorkspace(workspaceRoot, jobsDir string, now time.Time) (int, error) {
	targets, unreadable, err := selectReapTargets(workspaceRoot, jobsDir, now)
	if err != nil {
		return 0, err
	}
	reportSparedInstances(os.Stderr, unreadable)

	reaped := 0
	for _, t := range targets {
		if err := destroyInstanceFunc(t.InstancePath); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: reaping instance %s: %v\n", t.InstancePath, err)
			// Leave the mapping in place so a later reap retries this target
			// rather than orphaning the mapping for a still-present instance.
			continue
		}
		// A watch-review instance may hold a Claude Code workspace-trust grant in
		// ~/.claude.json; remove it now that the instance is reclaimed so trust
		// entries do not accumulate for deleted paths. Best-effort and a safe no-op
		// for instances that were never granted trust.
		_ = removeInstanceTrustFunc(t.InstancePath)
		if err := workspace.DeleteSessionMapping(workspaceRoot, t.SessionID); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: deleting session mapping %s: %v\n", t.SessionID, err)
		}
		reaped++
	}

	// Second pass: the name+TTL backstop. This is a SEPARATE scan, not a
	// branch in selectReapTargets, because EnumerateInstanceRecords derives
	// Ephemeral solely from the mapping store -- an unmapped orphan is already
	// Ephemeral:false and is dropped before any per-record branch there. The
	// backstop is the only path that may act on an UNMAPPED instance, and only
	// under its own gates (dispatch instance name, no mapping, age past the TTL).
	// It runs after the primary reclamation so the existing sweep keeps ownership
	// of every mapped instance (DESIGN Decision 4).
	n, err := reapBackstop(workspaceRoot, jobsDir, now)
	if err != nil {
		return reaped, err
	}
	reaped += n

	return reaped, nil
}

// backstopTarget is an instance the marker+TTL backstop has selected. Unlike a
// reapTarget it carries no session id: a backstop target is by definition
// unmapped, so there is no mapping entry to delete after the destroy.
type backstopTarget struct {
	InstancePath string
}

// selectBackstopTargets enumerates the on-disk instances under workspaceRoot and
// returns those eligible for the name+TTL backstop. An instance is eligible only
// when ALL of the following hold:
//
//   - its base directory name is a dispatch instance name (isDispatchInstanceName
//     -- the purely structural "<config>+-<8hex>" or "<config>+<slug>-<8hex>", regex
//     "\+[a-z0-9_]*-[0-9a-f]{8}$", no "disp" literal). This NAME, not a marker file, is the
//     eligibility signal: provisionInstanceFunc creates the directory atomically,
//     so a dispatch instance is recognizable the instant it exists. That closes
//     the SIGKILL-before-marker window a marker-file-only gate left open (an
//     instance unmapped AND unmarked -> formerly an unreclaimable orphan).
//   - it has NO session mapping (joined against ListSessionMappings by instance
//     path; absent means unmapped, the SIGKILL-orphan shape the backstop exists
//     for). A MAPPED dispatch instance is a successful dispatch owned by the
//     primary sweep and is never touched here.
//   - its age exceeds dispatchBackstopTTL. Age is read from the pending-marker's
//     embedded RFC3339 timestamp when the marker exists and parses (precise);
//     otherwise it FALLS BACK to the instance directory's mtime (the
//     SIGKILL-before-marker case, and the malformed-marker case). Either source
//     must show age > TTL; a present-but-malformed marker does NOT spare the
//     instance forever -- it falls back to mtime.
//   - NO worker is rooted in it, by either of the two readings niwa has. An
//     unmapped dispatch instance is NOT necessarily an orphan: a worker that
//     outlives the TTL, or one whose mapping is missing, is still alive. A
//     dispatched worker launches with cmd.Dir == its instance, so its job-state
//     cwd points at the instance directory; instanceHasLiveJob spares any
//     instance a present job's cwd resolves inside. That reads one agent's
//     harness state, so instanceHasRecordedSession asks every launchable agent's
//     session store the same question and spares on a hit there too -- an
//     instance may hold a detached worker whose harness niwa cannot see at all.
//     Together these are the load-bearing guard that stops the backstop from
//     reaping a live instance -- including the caller's own -- on name+age alone
//     (the data-loss class this fix closes). The TTL alone was unsafe: a
//     dispatched session can live for hours, far past the 30-minute TTL.
//
// The second reading costs something, and the cost is returned rather than
// swallowed. For an agent that never removes a session's record, "a worker is
// rooted here" stays true forever, so such an instance is spared on every sweep
// until somebody destroys it by hand. Those instances come back in the spared
// list with the reason to print. See instanceHasRecordedSession for why the
// answer is not to weaken the guard.
//
// A developer instance ("<config>", "<config>-2"), a hook-created instance
// ("<config>-<sessionhex>", no "+" marker), and a create instance
// ("<config>+<slug>", no trailing "-<8hex>") never match the name predicate,
// so they are never touched regardless of age or mapping. This function performs
// no destruction, so it is unit-testable against fixture instances, a fixture
// jobs tree, and an injectable now.
func selectBackstopTargets(workspaceRoot, jobsDir string, now time.Time) ([]backstopTarget, []sparedInstance, error) {
	var spared []sparedInstance

	records, err := workspace.EnumerateInstanceRecords(workspaceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("enumerating instances: %w", err)
	}

	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("listing session mappings: %w", err)
	}
	mappedPaths := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		if m.InstancePath != "" {
			mappedPaths[m.InstancePath] = true
		}
	}

	var targets []backstopTarget
	for _, rec := range records {
		// The dispatch instance NAME is the eligibility signal. A non-dispatch
		// name (developer or hook-created instance) is never a backstop target.
		if !isDispatchInstanceName(filepath.Base(rec.Path)) {
			continue
		}

		// A mapped instance is owned by the primary sweep; the backstop never
		// touches it, regardless of age.
		if mappedPaths[rec.Path] {
			continue
		}

		// An instance explicitly marked to be kept is spared regardless of age.
		// This is the case where a worker's turn finished and produced work but
		// niwa could not identify the session, so no mapping was ever written:
		// name-and-age alone reads that as an abandoned dispatch, which is the
		// one reading that deletes exactly the directory somebody was told was
		// being kept for them. The marker is the dispatch saying so, and it is
		// reported rather than silently honored, because an instance nothing
		// will ever reclaim on its own is one the developer has to know about.
		if reason, marked := dispatchRetainReason(rec.Path); marked {
			if reason == "" {
				reason = "it is marked to be kept, though the note saying why could not be read"
			}
			spared = append(spared, sparedInstance{Name: filepath.Base(rec.Path), Reason: reason})
			continue
		}

		created, ok := dispatchInstanceAge(rec.Path)
		if !ok {
			// Neither the marker timestamp nor the directory mtime is readable:
			// fail safe and skip rather than reap on an age we cannot establish.
			continue
		}

		if now.Sub(created) < dispatchBackstopTTL {
			// A healthy in-flight dispatch: younger than the TTL. Spare it (R38).
			continue
		}

		// A worker may still be rooted in this instance even though it is
		// unmapped and past the TTL (a long-lived one, or one whose mapping is
		// absent). The backstop must never delete an instance out from under a
		// running worker -- doing so was the data-loss bug (it reaped the
		// caller's own live instance mid-dispatch).
		//
		// instanceHasLiveJob reads one agent's harness state;
		// instanceHasRecordedSession asks every launchable agent's own declared
		// session store the same question, which is what keeps a worker in an
		// agent whose sessions live somewhere else entirely from having its
		// working directory destroyed underneath it.
		//
		// The two overlap for the agent whose declared store IS the jobs
		// directory, but they are not interchangeable: instanceHasLiveJob
		// compares cleaned paths and instanceHasRecordedSession resolves
		// symlinks first, so an instance reachable only through a symlinked
		// path -- one whose recorded cwd and whose instance path are the same
		// directory under two spellings -- is matched by the second and missed
		// by the first, for BOTH agents. If these ever collapse into one call,
		// it has to be into the resolving one.
		if instanceHasLiveJob(jobsDir, rec.Path) {
			continue
		}
		if reason, recorded := instanceHasRecordedSession(rec.Path); recorded {
			// A permanent spare is reported; a temporary one is not. See
			// instanceHasRecordedSession for why this class exists at all and
			// what would actually close it.
			if reason != "" {
				spared = append(spared, sparedInstance{Name: filepath.Base(rec.Path), Reason: reason})
			}
			continue
		}

		targets = append(targets, backstopTarget{InstancePath: rec.Path})
	}

	return targets, spared, nil
}

// dispatchInstanceAge returns the creation time the backstop ages a dispatch
// instance against. It prefers the pending-marker's embedded RFC3339 timestamp
// (precise; written by dispatch at create time) and FALLS BACK to the instance
// directory's mtime when the marker is absent or its timestamp is malformed --
// the SIGKILL-before-marker case the name-keyed backstop exists to cover. It
// returns (time, true) when either source yields a time, and (zero, false) only
// when neither is readable (a missing/unstattable directory), in which case the
// caller fails safe and spares the instance.
func dispatchInstanceAge(instancePath string) (time.Time, bool) {
	if ts, ok := readDispatchMarkerTime(instancePath); ok {
		return ts, true
	}
	info, err := os.Stat(instancePath)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// readDispatchMarkerTime reads the dispatch pending-marker inside instancePath
// and parses its embedded RFC3339 timestamp. It returns (time, true) only when
// the marker exists, is readable, and its first line parses as RFC3339;
// otherwise it returns (zero, false), and the caller falls back to the directory
// mtime. The embedded timestamp is preferred when present because it is the
// exact creation instant, independent of any later mtime touch (DESIGN
// Decision 4).
func readDispatchMarkerTime(instancePath string) (time.Time, bool) {
	data, err := os.ReadFile(filepath.Join(instancePath, dispatchPendingMarker))
	if err != nil {
		return time.Time{}, false
	}
	line := strings.TrimSpace(string(data))
	ts, err := time.Parse(time.RFC3339, line)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// reapBackstop selects and reclaims dispatch-named, unmapped, past-TTL orphan
// instances under workspaceRoot, returning the count actually destroyed. Each target is
// force-destroyed via destroyInstanceFunc, the same path the primary sweep
// uses. A destroy failure on one target is surfaced on
// stderr and does not abort the rest. There is no mapping to delete (a backstop
// target is unmapped by definition). jobsDir feeds the liveness gate in
// selectBackstopTargets so a live-but-unmapped instance is never reclaimed.
func reapBackstop(workspaceRoot, jobsDir string, now time.Time) (int, error) {
	targets, spared, err := selectBackstopTargets(workspaceRoot, jobsDir, now)
	if err != nil {
		return 0, err
	}
	reportSparedInstances(os.Stderr, spared)

	reaped := 0
	for _, t := range targets {
		if err := destroyInstanceFunc(t.InstancePath); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: reaping orphaned dispatch instance %s: %v\n", t.InstancePath, err)
			continue
		}
		// Remove any Claude Code workspace-trust grant for the reclaimed instance
		// (see reapWorkspace); best-effort and a safe no-op when none was granted.
		_ = removeInstanceTrustFunc(t.InstancePath)
		reaped++
	}

	return reaped, nil
}

// reapOpportunistically runs the reaper as a best-effort side effect at the
// start of niwa create so session fan-out self-bounds (DESIGN Decision 6, R5,
// R11). It NEVER returns an error: a reap failure must not block create.
// Failures are swallowed (the on-demand `niwa reap` surfaces them); only
// successful reclamations are noted on stderr.
func reapOpportunistically(workspaceRoot string) {
	if workspaceRoot == "" {
		return
	}
	n, err := reapWorkspace(workspaceRoot, defaultJobsDir(), time.Now())
	if err != nil {
		return
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "niwa: reaped %d orphaned ephemeral instance(s)\n", n)
	}
}
