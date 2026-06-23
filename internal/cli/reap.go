package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

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
