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

// defaultReapIdleGrace bounds how long a session an agent will never delete may
// sit untouched before the instance provisioned for it is reclaimed.
//
// It is not a TTL on an instance and it is unrelated to dispatchBackstopTTL
// above, which bounds an in-flight dispatch -- seconds to tens of seconds of
// clone and capture. This bounds a person: how long after last working in a
// session they might reasonably come back to it. A day is chosen to cover
// stopping for the night, because the cost of guessing short is real. Resuming
// a session in practice means working in the instance it ran in, so reclaiming
// that instance is what ends practical resumability, and there is no undo.
//
// It applies only to an agent declaring LivenessRecordActivity. An agent whose
// records disappear when a session is deleted has an exact signal and needs no
// grace period, and one that offers neither is spared rather than timed.
const defaultReapIdleGrace = 24 * time.Hour

// reapIdleGrace resolves the grace period, honoring NIWA_REAP_IDLE_GRACE.
//
// The override exists because the default is a judgment about how people work
// rather than a fact about an agent, and because it is the only way to watch
// the rule act without waiting a day for it. A value that will not parse, or is
// not positive, is ignored in favor of the default: a typo here would otherwise
// silently shorten the window before instances are destroyed, which is the one
// direction a misreading must never go.
func reapIdleGrace() time.Duration {
	if env := strings.TrimSpace(os.Getenv("NIWA_REAP_IDLE_GRACE")); env != "" {
		if d, err := time.ParseDuration(env); err == nil && d > 0 {
			return d
		}
	}
	return defaultReapIdleGrace
}

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

What proves a session is still there is the launching agent's own business, and
there are three answers. Most agents remove a session's record when you delete
the session, so the record's presence is the proof and the rule above is the
whole story.

An agent that never removes a record cannot be read that way, because the record
outlives the session. For those, reap asks a different question: has anybody
worked in this session lately, and is anything writing to it right now. A
session nobody has touched for longer than the idle grace period, with no writer
holding its lock, is treated as finished with, and the instance is reclaimed.
This is the one path where an instance goes away without you deleting anything,
so the grace period is generous -- a day by default, and settable with
NIWA_REAP_IDLE_GRACE. A session you keep coming back to keeps its instance:
resuming one moves its record, which starts the clock again.

An agent offering neither signal leaves reap with no evidence, and an instance
whose session cannot be proven gone is never reclaimed. Those are spared, and
reap says so on stderr rather than silently, because they pile up until you
remove them yourself with niwa destroy. Sparing an instance nobody is using
costs a directory; the other mistake costs the work inside it.

The same care applies to a dispatch instance with no mapping at all, which reap
otherwise reclaims on age: if an agent's records say a worker is still writing
there, reap leaves the directory alone.

reap runs on demand and is also invoked opportunistically at the start of
niwa create, niwa dispatch, and niwa watch so session fan-out self-bounds.`,
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

// reapNotice is what a reclaimed instance says about itself, empty when the
// reclamation needs no explanation.
//
// Most reclamations follow something the developer did: they deleted the
// session, and the instance going with it is the thing they asked for. A count
// is enough for that. Reclaiming on idleness follows nothing they did -- they
// walked away, and a directory disappeared -- so that one names the instance
// and the rule. Learning it from a resume into a missing directory instead is
// the failure this line exists to prevent.
type reapNotice struct {
	Name   string
	Reason string
}

// reapTarget pairs an instance the reaper has selected for reclamation with the
// session mapping that justifies it. The session id is carried so the mapping
// entry can be deleted after the instance is destroyed.
type reapTarget struct {
	SessionID    string
	InstancePath string

	// Agent is the mapping's recorded agent, carried so the verdict can be
	// re-asked immediately before the destroy without re-reading the mapping.
	Agent string

	// Notice explains the reclamation when it followed nothing the developer
	// did, and is zero otherwise. See reapNotice.
	Notice reapNotice
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
//
// The headline speaks for this sweep and leaves what happened to the reason.
// Three different things land here -- an instance whose agent offers no
// liveness signal at all, one whose turn finished and left work niwa could not
// attach to a session, and one whose liveness could not be read this time --
// so a headline that asserted any of them would print something false about
// the others every time they came up.
//
// The last of those is why the headline stopped claiming no sweep will ever
// reclaim these. That was true while every reason here was permanent. An
// unreadable probe is not: a stat that failed once may answer next time, and a
// sweep that says the instance is yours forever would be sending the developer
// to niwa destroy for a condition that may already have cleared.
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
		fmt.Fprintf(w, "niwa: kept %d instance(s) this sweep would not reclaim (%s%s): %s\n",
			len(names), strings.Join(listed, ", "), suffix, reason)
	}
	// The name rather than the path, because that is what the command takes.
	fmt.Fprintf(w, "niwa: reclaim one with `niwa destroy <name>` when you are done with its session\n")
}

// sparedInstance is an instance the sweep left alone -- because it could not
// read whether the session was still there, or because the instance is marked
// to be kept -- with the reason in the words a user needs to understand why
// their disk is not getting emptier.
//
// Name rather than path, because `niwa destroy` takes a name and a report that
// prints one thing while telling you to type another is a report you have to
// translate before you can act on it.
type sparedInstance struct {
	Name   string
	Reason string
}

// sessionVerdict is what the sweep concluded about the session behind a
// mapping. Three answers, and the third is not a shade of the second: an
// instance whose session is gone is reclaimed, and one whose session cannot be
// read is left alone.
type sessionVerdict uint8

const (
	// sessionIsLive means the session is still there -- running, between
	// turns, or waiting to be come back to.
	sessionIsLive sessionVerdict = iota + 1

	// sessionIsGone means the session has been proven finished with.
	sessionIsGone

	// sessionIsUnreadable means no conclusion was reached.
	sessionIsUnreadable
)

// sessionState decides which of the three a mapping is in, by asking the
// launching agent's own declaration what proves a session is still there.
//
// The rule is the declaration's, not this function's, and that is the whole
// shape: a second agent arrives answered by whichever kind it declares, and an
// agent declaring a kind this build does not know is unreadable rather than
// silently reclaimed. Three shapes never get as far as a kind at all -- a
// mapping written by something this build does not recognize, an agent niwa
// launches no worker for and so keeps no records of, and an agent that declares
// no readable signal -- and all three mean the same thing here.
func sessionState(recorded, sessionID, jobsDir, instancePath string, now time.Time) (sessionVerdict, string) {
	ag, err := agent.ParseAgent(recorded)
	if err != nil {
		return sessionIsUnreadable, fmt.Sprintf("its mapping records agent %q, which this build does not recognize", recorded)
	}
	spec, ok := agentplan.For(ag).LaunchSpec()
	if !ok {
		return sessionIsUnreadable, fmt.Sprintf("niwa launches no background worker for %s, so there are no session records to read", ag)
	}

	switch spec.Records.Liveness {
	case agentplan.LivenessRecordPresence:
		// The record is removed when the session is deleted, so its presence
		// is the answer and nothing else has to be read.
		if sessionLive(jobsDir, sessionID, now) {
			return sessionIsLive, ""
		}
		return sessionIsGone, ""

	case agentplan.LivenessRecordActivity:
		// The record outlives the session, so presence says nothing. What
		// answers instead is whether anybody has worked in it lately and
		// whether a writer is attached right now.
		rec, ok := recordForInstance(spec.Records, instancePath, sessionID)
		if !ok {
			// The mapping names a session of this agent's and the store has no
			// record of it rooted here. That is a store this sweep cannot
			// reason about rather than a session it may declare gone.
			return sessionIsUnreadable, fmt.Sprintf("its mapping names a %s session that agent's own records do not describe as having run there", ag)
		}
		live, known := recordActivityLive(spec.Records, rec, userHomeDir(), os.Getenv, now, reapIdleGrace())
		switch {
		case !known:
			return sessionIsUnreadable, fmt.Sprintf("niwa could not read whether its %s session is still being worked in", ag)
		case live:
			return sessionIsLive, ""
		default:
			// The one reclamation nobody asked for, so it is the one that has
			// to explain itself. The reason travels with the verdict and is
			// printed when the instance actually goes.
			return sessionIsGone, fmt.Sprintf("its %s session had gone %s without being worked in, and nothing was writing to it", ag, reapIdleGrace())
		}

	default:
		return sessionIsUnreadable, fmt.Sprintf("%s never removes a session's record, so its presence cannot tell a live session from a deleted one", ag)
	}
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
		verdict, reason := sessionState(mapping.Agent, mapping.SessionID, jobsDir, rec.Path, now)
		name := mapping.InstanceName
		if name == "" {
			name = filepath.Base(rec.Path)
		}
		switch verdict {
		case sessionIsUnreadable:
			unreadable = append(unreadable, sparedInstance{Name: name, Reason: reason})
			continue
		case sessionIsLive:
			// Still there -- running, between turns, or waiting to be come
			// back to. Only a session proven finished with is reclaimed.
			continue
		}
		var notice reapNotice
		if reason != "" {
			notice = reapNotice{Name: name, Reason: reason}
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
		_, recorded := instanceHasRecordedSession(rec.Path, now)
		if instanceHasLiveJob(jobsDir, rec.Path) || recorded {
			continue
		}

		targets = append(targets, reapTarget{
			SessionID:    mapping.SessionID,
			InstancePath: rec.Path,
			Agent:        mapping.Agent,
			Notice:       notice,
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
		// Selection happened before the spared report was printed and before
		// every earlier destroy in this loop, so a verdict reached up there is
		// already seconds old by the time it is acted on. For a session
		// reclaimed on idleness that gap is a window somebody can resume into:
		// the probe said no writer, a resume took the lock, and the directory
		// it is now working in is about to be destroyed.
		//
		// So that verdict alone is re-asked immediately before the destroy, at
		// the cost of one stat and at most one lock probe. It does not close
		// the race -- nothing check-then-act can -- but it narrows it from the
		// length of a whole sweep to the length of one syscall pair. The notice
		// printed below is the other reason to bother: without this it could
		// tell a developer that nobody had been working in a directory
		// somebody had just opened.
		if t.Notice.Reason != "" {
			if verdict, _ := sessionState(t.Agent, t.SessionID, jobsDir, t.InstancePath, time.Now()); verdict != sessionIsGone {
				continue
			}
		}
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
		// Printed after the destroy rather than before it, so the line is a
		// report of something that happened rather than of something intended:
		// a destroy that fails above continues without saying the instance is
		// gone.
		if t.Notice.Reason != "" {
			fmt.Fprintf(os.Stderr, "niwa: reclaimed %s: %s\n", t.Notice.Name, t.Notice.Reason)
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
		if reason, recorded := instanceHasRecordedSession(rec.Path, now); recorded {
			// A permanent spare is reported; a temporary one is not, and an
			// agent answered by activity is temporary -- the sparing ends when
			// the session goes untouched for the grace period. See
			// instanceHasRecordedSession for what each kind means here.
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
