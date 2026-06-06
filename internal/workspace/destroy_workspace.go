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
}

// DestroyWorkspace wipes the entire workspace at workspaceRoot. The
// caller is responsible for any pre-flight safety checks
// (non-pushed-work scan, typed confirmation, etc.). DestroyWorkspace
// does NOT call ValidateInstanceDir on workspaceRoot — that validator
// is specifically designed to refuse this operation, and calling it
// here would be a contract violation.
//
// Sequence:
//  1. Enumerate instances under workspaceRoot.
//  2. Sort by instance name (alphabetical, deterministic).
//  3. For each instance: TerminateDaemon → ValidateInstanceDir →
//     RemoveAll(instanceDir).
//  4. After all instances are removed, RemoveAll(workspaceRoot).
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

	instances, err := EnumerateInstances(abs)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
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

		if err := os.RemoveAll(instanceDir); err != nil {
			return fmt.Errorf("removing instance %s: %w",
				filepath.Base(instanceDir), err)
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
