// Package workspace, file destroy_workspace.go: workspace-wipe helper used
// by `niwa destroy --force` from the workspace root and by
// `niwa destroy` from an empty workspace root.
//
// This is a SIBLING helper to DestroyInstance — it does NOT route through
// ValidateInstanceDir (which is designed to refuse exactly the workspace
// root we want to delete here). The two helpers are intentionally
// distinct: DestroyInstance is the safe, scoped, reset-shared path;
// DestroyWorkspace is the irreversible whole-workspace path that callers
// must gate behind their own safety checks (typed confirmation, --force,
// or an empty-workspace check).
package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// DestroyWorkspaceOpts configures the workspace-wipe operation.
type DestroyWorkspaceOpts struct {
	// Reporter is optional; when set, per-instance progress is logged
	// via Reporter.Log. nil falls back to silent operation.
	Reporter *Reporter

	// ProgressOut, if set, receives a final "Destroyed workspace: <path>"
	// line on success. Defaults to nil (silent).
	ProgressOut io.Writer

	// PluginRecordBaseDir overrides the home directory used to locate Claude
	// Code's plugin registry when pruning instance-owned records. Empty means
	// the real ~/.claude. It exists for tests.
	PluginRecordBaseDir string

	// Instances, when non-nil, is the exact set of instances to remove: the
	// list the caller took locks on and checked. DestroyWorkspace then never
	// removes an instance outside it. An instance on disk that isn't in it
	// (one restored or provisioned after the caller enumerated) refuses the
	// whole operation before anything is removed, and keeps the workspace
	// root if it appears while the listed instances are being removed. A
	// non-nil empty slice means the caller saw no instances. nil keeps the
	// old behavior of removing whatever is enumerated at call time.
	Instances []string

	// BeforeRootRemoval, if set, runs once every listed instance is gone and
	// immediately before the workspace root is removed. An error keeps the
	// root and is returned.
	BeforeRootRemoval func() error
}

// UnlistedInstance returns the name of an instance under workspaceRoot whose
// base name isn't in listed, or "" when every instance on disk is listed.
func UnlistedInstance(workspaceRoot string, listed []string) (string, error) {
	present, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return "", err
	}
	known := make(map[string]bool, len(listed))
	for _, p := range listed {
		known[filepath.Base(p)] = true
	}
	sort.Strings(present)
	for _, p := range present {
		if !known[filepath.Base(p)] {
			return filepath.Base(p), nil
		}
	}
	return "", nil
}

// DestroyWorkspace wipes the entire workspace at workspaceRoot. The
// caller is responsible for any pre-flight safety checks
// (non-pushed-work scan, typed confirmation, etc.). DestroyWorkspace
// does NOT call ValidateInstanceDir on workspaceRoot — that validator
// is specifically designed to refuse this operation, and calling it
// here would be a contract violation.
//
// Sequence:
//  1. Take opts.Instances, refusing if any other instance is on disk, or
//     enumerate instances under workspaceRoot when it is nil.
//  2. Sort by instance name (alphabetical, deterministic).
//  3. For each instance: ValidateInstanceDir → prune plugin records →
//     withdraw the recorded Claude trust grant → RemoveAll(instanceDir).
//  4. After all instances are removed, refuse if an unlisted instance is
//     now on disk, run opts.BeforeRootRemoval, then RemoveAll(workspaceRoot).
//
// Per-instance synchronous ordering preserves resumability on partial
// failure: if step 3 panics or returns an error mid-loop, the
// completed instances stay destroyed and the remaining ones are
// untouched. Re-running the command picks up where it stopped.
//
// Returns the first error encountered. The workspace dir itself is
// removed only if every instance was successfully destroyed.
func DestroyWorkspace(workspaceRoot string, opts DestroyWorkspaceOpts) error {
	// Resolve and validate workspaceRoot exists before doing anything.
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolving workspace root: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}

	var instances []string
	if opts.Instances != nil {
		// Only the caller's list is removed, each resolved under this root.
		// Anything else on disk refuses before a single instance goes.
		for _, p := range opts.Instances {
			instances = append(instances, filepath.Join(abs, filepath.Base(p)))
		}
		extra, err := UnlistedInstance(abs, instances)
		if err != nil {
			return fmt.Errorf("enumerating instances: %w", err)
		}
		if extra != "" {
			return fmt.Errorf("instance %s is not one this destroy checked, so nothing was removed", extra)
		}
	} else {
		instances, err = EnumerateInstances(abs)
		if err != nil {
			return fmt.Errorf("enumerating instances: %w", err)
		}
	}

	// Deterministic alphabetical order by directory name (which equals
	// instance name when the instance was created via `niwa create`).
	sort.Strings(instances)

	for _, instanceDir := range instances {
		if opts.Reporter != nil {
			opts.Reporter.Log("destroying instance: %s", filepath.Base(instanceDir))
		}

		if err := ValidateInstanceDir(instanceDir); err != nil {
			// An entry returned by EnumerateInstances that doesn't pass
			// ValidateInstanceDir is anomalous (e.g., the workspace
			// root snuck in somehow) — abort rather than removing it.
			return fmt.Errorf("validating instance %s: %w",
				filepath.Base(instanceDir), err)
		}

		// Prune the records this instance owns and withdraw its recorded
		// Claude trust grant before removing it, so both match against a
		// path that still exists. Fail-safe: registry or config-file trouble
		// never blocks the wipe.
		prunePluginRecords(instanceDir, opts.Reporter, opts.PluginRecordBaseDir)
		withdrawClaudeTrust(instanceDir, opts.Reporter)

		if err := os.RemoveAll(instanceDir); err != nil {
			return fmt.Errorf("removing instance %s: %w",
				filepath.Base(instanceDir), err)
		}
	}

	// An instance that appeared while the listed ones were being removed
	// keeps the root: removing it would remove that instance with it.
	if opts.Instances != nil {
		extra, err := UnlistedInstance(abs, instances)
		if err != nil {
			return fmt.Errorf("enumerating instances: %w", err)
		}
		if extra != "" {
			return fmt.Errorf("kept the workspace root: instance %s appeared in it while it was being destroyed", extra)
		}
	}
	if opts.BeforeRootRemoval != nil {
		if err := opts.BeforeRootRemoval(); err != nil {
			return err
		}
	}

	// All instances destroyed; remove the workspace dir itself.
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("removing workspace root: %w", err)
	}

	if opts.ProgressOut != nil {
		fmt.Fprintf(opts.ProgressOut, "Destroyed workspace: %s\n", abs)
	}

	return nil
}
