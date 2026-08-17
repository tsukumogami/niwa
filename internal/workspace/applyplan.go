package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// This file is the whole of niwa's plan execution: the one place a declared
// entry becomes bytes on disk. It reads an entry's Op, Pre, Path, Content,
// Mode, and Marker, and nothing else -- no agent, no filename, no capability
// branch. Everything specific to an agent lives in internal/agentplan, which
// produces the plan; what remains here is the mechanics, reviewed once instead
// of once per writer.
//
// The op and precondition vocabulary is interpreted in this file and nowhere
// else in the package; applyplan_wiring_test.go asserts that mechanically.

// errNoExecutorArm reports an entry whose operation this executor does not
// implement. It exists so an unimplemented op is a loud failure rather than a
// silent skip: OpDeliverTree is a declared member of the vocabulary whose
// implementation lands with its first consumer, and until then an entry
// carrying it must stop the apply rather than quietly deliver nothing.
var errNoExecutorArm = errors.New("plan entry operation has no executor arm")

// errUnknownPrecondition reports a gate this executor cannot evaluate. Same
// reasoning as errNoExecutorArm: a precondition nobody implements must not
// resolve to "write it anyway".
var errUnknownPrecondition = errors.New("plan entry precondition has no executor arm")

// errMalformedPlanEntry reports an entry the executor cannot act on at all: a
// relative path, a zero file mode, or a section replace with no delimiter.
// Each of those is a producer bug that would otherwise land as a file in the
// wrong place, a file nobody can read, or a truncated one.
var errMalformedPlanEntry = errors.New("malformed plan entry")

// applyPlan writes a plan's entries in order and reports what it produced.
//
// written lists the paths the plan is responsible for, one per applied entry
// and deduplicated -- an idempotent append that changed no bytes still counts,
// because the file is the plan's either way and the managed-file record must
// say so. excludes lists the git-exclude patterns the applied entries imply.
// An entry skipped by its precondition contributes to neither.
//
// The first failing entry stops the plan and returns the error: a partially
// delivered plan is a state nobody declared, and the apply path's callers
// already treat a write failure as fatal.
func applyPlan(p *agentplan.Plan) (written []string, excludes []string, err error) {
	if p == nil {
		return nil, nil, nil
	}

	for _, e := range p.Entries {
		ok, err := planEntryApplies(e)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}

		if err := checkPlanEntry(e); err != nil {
			return nil, nil, err
		}

		switch e.Op {
		case agentplan.OpWriteFile:
			err = writePlanFile(e.Path, e.Content, e.Mode)
		case agentplan.OpAppendLine:
			err = appendPlanLine(e.Path, e.Content, e.Mode)
		case agentplan.OpReplaceSection:
			err = replacePlanSection(e.Path, e.Marker, e.Content, e.Mode)
		default:
			// OpDeliverTree lands here until its consumer arrives with it.
			return nil, nil, fmt.Errorf("%w: op %d for capability %s at %s",
				errNoExecutorArm, e.Op, e.Capability, e.Path)
		}
		if err != nil {
			return nil, nil, err
		}

		written = appendUniqueString(written, e.Path)
		if e.ExcludeAs != "" {
			excludes = appendUniqueString(excludes, e.ExcludeAs)
		}
	}

	return written, excludes, nil
}

// planEntryApplies evaluates an entry's gate. Preconditions are evaluated here
// rather than by the producer so a condition about the target tree does not
// drag filesystem access into the leaf package.
func planEntryApplies(e agentplan.Entry) (bool, error) {
	switch e.Pre {
	case agentplan.Always:
		return true, nil
	case agentplan.IfSourceExists:
		if _, err := os.Stat(e.Source); err != nil {
			if os.IsNotExist(err) {
				// An absent source is a no-op, not an error: the
				// entry declares "deliver this if it is there".
				return false, nil
			}
			return false, fmt.Errorf("probing plan source %s: %w", e.Source, err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("%w: precondition %d for capability %s at %s",
			errUnknownPrecondition, e.Pre, e.Capability, e.Path)
	}
}

// checkPlanContainment verifies every path a plan targets resolves inside root.
// It lives beside the executor because it reads the same field applyPlan does,
// and a caller that wants the guarantee should not have to walk the entries
// itself: the plan is interpreted here or not at all.
//
// It is the plan-shaped form of the containment discipline the content
// installers have always applied to their targets, and it keeps that guarantee
// on the caller's side of the boundary -- the producer decides the filename,
// and the writer still refuses a path that escapes the tree it is writing into.
func checkPlanContainment(p *agentplan.Plan, root string) error {
	if p == nil {
		return nil
	}
	for _, e := range p.Entries {
		if err := checkContainment(e.Path, root); err != nil {
			return err
		}
	}
	return nil
}

// checkPlanEntry rejects entries the executor cannot act on safely.
func checkPlanEntry(e agentplan.Entry) error {
	if !filepath.IsAbs(e.Path) {
		return fmt.Errorf("%w: path %q is not absolute", errMalformedPlanEntry, e.Path)
	}
	if e.Mode == 0 {
		return fmt.Errorf("%w: no file mode for %s", errMalformedPlanEntry, e.Path)
	}
	if e.Op == agentplan.OpReplaceSection && e.Marker == "" {
		return fmt.Errorf("%w: section replace with no marker for %s", errMalformedPlanEntry, e.Path)
	}
	return nil
}

// writePlanFile is OpWriteFile: create the parent directory, then write the
// content at the entry's mode.
func writePlanFile(path string, content []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// appendPlanLine is OpAppendLine: append the line unless the file already
// contains it, which is what makes re-applying an instance idempotent. The
// match is a substring check on the whole file, matching the accumulating
// @import writer this op generalizes: the line is an absolute path, so a
// substring hit is the line itself.
func appendPlanLine(path string, content []byte, mode fs.FileMode) error {
	line := strings.TrimSuffix(string(content), "\n")

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if strings.Contains(string(existing), line) {
		return nil
	}

	merged := string(existing)
	if len(merged) > 0 && !strings.HasSuffix(merged, "\n") {
		merged += "\n"
	}
	merged += line + "\n"

	return writePlanFile(path, []byte(merged), mode)
}

// replacePlanSection is OpReplaceSection: rewrite the region of the file that
// starts at Marker, leaving whatever precedes it alone.
//
// A missing marker -- including a missing file -- appends the section rather
// than failing, because that is what "this section belongs in this file" means
// on the first apply, and because the delimited-section writer this op
// generalizes has always behaved that way. The marker is expected to open the
// section and run to the end of the file, so the region replaced is from the
// marker onward.
func replacePlanSection(path, marker string, content []byte, mode fs.FileMode) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	merged := string(existing)
	if idx := strings.Index(merged, marker); idx >= 0 {
		merged = merged[:idx]
	}
	merged = strings.TrimRight(merged, "\n")
	if len(merged) > 0 {
		// Separate prior content from the section with a blank line.
		merged += "\n\n"
	}
	merged += string(content)

	return writePlanFile(path, []byte(merged), mode)
}

// appendUniqueString appends s unless it is already present. The lists it
// builds are short (a plan's paths and exclude patterns), so the linear scan
// costs less than the map it replaces.
func appendUniqueString(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
}

// planRun accumulates the plans applied during one pipeline run so the
// bookkeeping steps that follow can read the entries rather than a bare path
// list plus a side map. Step 7 takes Managed and Sources off the entries; the
// per-repo git-exclude call takes ExcludeAs off the ones that landed in that
// repo; the reporter takes the warnings.
type planRun struct {
	outcomes []planOutcome
}

// planOutcome is one applied plan plus the set of paths its entries produced.
// Holding both is what lets the later steps tell an applied entry from one its
// precondition skipped.
type planOutcome struct {
	plan    *agentplan.Plan
	written map[string]bool
}

// apply executes a plan and records it for the bookkeeping steps.
func (r *planRun) apply(p *agentplan.Plan) (written []string, excludes []string, err error) {
	written, excludes, err = applyPlan(p)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, nil
	}

	produced := make(map[string]bool, len(written))
	for _, path := range written {
		produced[path] = true
	}
	r.outcomes = append(r.outcomes, planOutcome{plan: p, written: produced})

	return written, excludes, nil
}

// managedEntries returns the applied entries that join the instance's managed
// file record, in application order and one per path. Files niwa writes into a
// developer's own tree deliberately do not set Managed, so they are absent
// here and stay out of the record and its cleanup.
func (r *planRun) managedEntries() []agentplan.Entry {
	var out []agentplan.Entry
	seen := map[string]int{}
	for _, o := range r.outcomes {
		for _, e := range o.plan.Entries {
			if !e.Managed || !o.written[e.Path] {
				continue
			}
			// A path written by more than one entry is recorded
			// once, by its last writer: that entry's provenance
			// describes the bytes now on disk.
			if i, ok := seen[e.Path]; ok {
				out[i] = e
				continue
			}
			seen[e.Path] = len(out)
			out = append(out, e)
		}
	}
	return out
}

// excludesUnder returns the git-exclude patterns implied by applied entries
// that landed inside dir. Selecting by path rather than by an extra scope
// parameter keeps the executor agent-blind and the accumulator repo-agnostic:
// an entry knows where it went, and that is enough to decide which repository
// has to ignore it.
func (r *planRun) excludesUnder(dir string) []string {
	if dir == "" {
		return nil
	}
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)

	var out []string
	for _, o := range r.outcomes {
		for _, e := range o.plan.Entries {
			if e.ExcludeAs == "" || !o.written[e.Path] {
				continue
			}
			if !strings.HasPrefix(e.Path, prefix) {
				continue
			}
			out = appendUniqueString(out, e.ExcludeAs)
		}
	}
	return out
}

// warnings returns what the applied plans said the user needs to hear: a
// declaration that could not be honored, a refusal, an omission.
func (r *planRun) warnings() []string {
	var out []string
	for _, o := range r.outcomes {
		out = append(out, o.plan.Warnings...)
	}
	return out
}
