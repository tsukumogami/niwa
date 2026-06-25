package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchBackstopTTL bounds how long a dispatch-marked, unmapped instance may
// sit before the reaper backstop reclaims it. It is chosen far longer than the
// worst-case dispatch wall-clock (a clone plus a bounded capture poll is
// seconds to low tens of seconds), so a healthy in-flight dispatch's marker is
// always younger than the TTL and is never reaped (DESIGN Decision 4, R38).
const dispatchBackstopTTL = 30 * time.Minute

func init() {
	rootCmd.AddCommand(reapCmd)
}

var reapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Reclaim ephemeral instances whose backing session has ended",
	Long: `Reclaim ephemeral instances whose Claude Code session ended without a
clean teardown.

reap enumerates the workspace's instances, joins each against its
session->instance mapping, and force-destroys an instance only when BOTH hold:

  - the instance is marked ephemeral (provisioned for a session), and
  - its session is dead by the liveness rule: the session's Claude Code job at
    ~/.claude/jobs/<session-id>/ is gone, its job state is terminal, or its
    job state has not been updated within the liveness window.

A non-ephemeral (developer) instance is NEVER targeted, and an instance is
NEVER reaped on the time-to-live alone without the ephemeral marker. Job state
-- not transcript mtime -- is the primary liveness signal, so a live-but-idle
worker that is still rewriting its job state is spared.

reap runs on demand and is also invoked opportunistically at the start of
niwa create so session fan-out self-bounds.`,
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
func selectReapTargets(workspaceRoot, jobsDir string, now time.Time) ([]reapTarget, error) {
	records, err := workspace.EnumerateInstanceRecords(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerating instances: %w", err)
	}

	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("listing session mappings: %w", err)
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

		if sessionLive(jobsDir, mapping.SessionID, now) {
			// The session is still live (or live-but-idle): spare it.
			continue
		}

		targets = append(targets, reapTarget{
			SessionID:    mapping.SessionID,
			InstancePath: rec.Path,
		})
	}

	return targets, nil
}

// reapWorkspace selects and reclaims orphaned ephemeral instances under
// workspaceRoot, returning the count actually destroyed. For each target it
// force-destroys the instance (via destroyInstanceFunc, the same non-interactive
// path SessionEnd teardown uses) and deletes the mapping entry. A destroy
// failure on one target is surfaced on stderr and does not abort the rest, so a
// single stuck instance never blocks reclaiming the others.
func reapWorkspace(workspaceRoot, jobsDir string, now time.Time) (int, error) {
	targets, err := selectReapTargets(workspaceRoot, jobsDir, now)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, t := range targets {
		if err := destroyInstanceFunc(t.InstancePath); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: reaping instance %s: %v\n", t.InstancePath, err)
			// Leave the mapping in place so a later reap retries this target
			// rather than orphaning the mapping for a still-present instance.
			continue
		}
		if err := workspace.DeleteSessionMapping(workspaceRoot, t.SessionID); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: deleting session mapping %s: %v\n", t.SessionID, err)
		}
		reaped++
	}

	// Second pass: the marker+TTL backstop. This is a SEPARATE scan, not a
	// branch in selectReapTargets, because EnumerateInstanceRecords derives
	// Ephemeral solely from the mapping store -- an unmapped orphan is already
	// Ephemeral:false and is dropped before any per-record branch there. The
	// backstop is the only path that may act on an UNMAPPED instance, and only
	// under its own gates (marker present, no mapping, marker timestamp past the
	// TTL). It runs after the primary reclamation so the existing sweep keeps
	// ownership of every mapped instance (DESIGN Decision 4).
	n, err := reapBackstop(workspaceRoot, now)
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
// returns those eligible for the marker+TTL backstop. An instance is eligible
// only when ALL of the following hold:
//
//   - it has NO session mapping (joined against ListSessionMappings by instance
//     path; absent means unmapped, the SIGKILL-orphan shape the backstop exists
//     for);
//   - it carries the dispatch pending-marker file; and
//   - the marker's embedded RFC3339 timestamp is older than dispatchBackstopTTL
//     relative to now.
//
// A marker that is missing, unreadable, or carries a malformed timestamp is
// treated as NOT reapable (fail safe -- never reap on a parse failure). This
// function performs no destruction, so it is unit-testable against fixture
// instances and an injectable now.
func selectBackstopTargets(workspaceRoot string, now time.Time) ([]backstopTarget, error) {
	records, err := workspace.EnumerateInstanceRecords(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("enumerating instances: %w", err)
	}

	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("listing session mappings: %w", err)
	}
	mappedPaths := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		if m.InstancePath != "" {
			mappedPaths[m.InstancePath] = true
		}
	}

	var targets []backstopTarget
	for _, rec := range records {
		// A mapped instance is owned by the primary sweep; the backstop never
		// touches it, regardless of age or whether a stale marker lingers.
		if mappedPaths[rec.Path] {
			continue
		}

		created, ok := readDispatchMarkerTime(rec.Path)
		if !ok {
			// No marker (a developer instance) or an unreadable/malformed
			// marker: skip. Failing safe here preserves the never-reap-a-
			// developer-instance guarantee and avoids reaping on a marker we
			// cannot prove is old.
			continue
		}

		if now.Sub(created) < dispatchBackstopTTL {
			// A healthy in-flight dispatch: its marker is younger than the TTL.
			// Spare it (R38).
			continue
		}

		targets = append(targets, backstopTarget{InstancePath: rec.Path})
	}

	return targets, nil
}

// readDispatchMarkerTime reads the dispatch pending-marker inside instancePath
// and parses its embedded RFC3339 timestamp. It returns (time, true) only when
// the marker exists, is readable, and its first line parses as RFC3339;
// otherwise it returns (zero, false). Reading the age from the embedded
// timestamp (not the directory mtime) keeps the gate reliable across filesystem
// mtime quirks (DESIGN Decision 4).
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

// reapBackstop selects and reclaims marked, unmapped, past-TTL orphan instances
// under workspaceRoot, returning the count actually destroyed. Each target is
// force-destroyed via destroyInstanceFunc, the same path the primary sweep and
// SessionEnd teardown use. A destroy failure on one target is surfaced on
// stderr and does not abort the rest. There is no mapping to delete (a backstop
// target is unmapped by definition).
func reapBackstop(workspaceRoot string, now time.Time) (int, error) {
	targets, err := selectBackstopTargets(workspaceRoot, now)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, t := range targets {
		if err := destroyInstanceFunc(t.InstancePath); err != nil {
			fmt.Fprintf(os.Stderr, "niwa: warning: reaping orphaned dispatch instance %s: %v\n", t.InstancePath, err)
			continue
		}
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
