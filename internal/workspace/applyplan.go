package workspace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
// implement. Every member of the declared vocabulary now has an arm, so what it
// guards is a value from outside it -- a producer bug that must stop the apply
// rather than quietly deliver nothing.
var errNoExecutorArm = errors.New("plan entry operation has no executor arm")

// errForeignDeliveryTarget reports a tree delivery whose target is occupied by
// something niwa did not deliver. Refreshing a delivered tree means replacing it
// wholesale, so the executor has to be certain the tree is its own before it
// removes anything; when it cannot be, it stops rather than guessing.
var errForeignDeliveryTarget = errors.New("delivery target is not a niwa-delivered tree")

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
		// Well-formedness is checked before the gate, not after it: an entry
		// the executor could not act on is a producer bug whether or not its
		// precondition happens to skip it today, and the gates themselves read
		// fields the check validates.
		if err := checkPlanEntry(e); err != nil {
			return nil, nil, err
		}

		ok, err := planEntryApplies(e)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}

		switch e.Op {
		case agentplan.OpWriteFile:
			err = writePlanFile(e.Path, e.Content, e.Mode)
		case agentplan.OpAppendLine:
			err = appendPlanLine(e.Path, e.Content, e.Mode)
		case agentplan.OpReplaceSection:
			err = replacePlanSection(e.Path, e.Marker, e.Content, e.Mode)
		case agentplan.OpDeliverTree:
			err = deliverPlanTree(e.Path, e.Source, e.Owner, e.Mode)
		default:
			return nil, nil, fmt.Errorf("%w: op %d for capability %s at %s",
				errNoExecutorArm, e.Op, e.Capability, e.Path)
		}
		if err != nil {
			// Every failure names the capability it was delivering. A
			// declaration the table calls implemented has to fail loudly and
			// legibly when its delivery cannot land, rather than surfacing as a
			// bare path the user has to trace back to a feature.
			return nil, nil, fmt.Errorf("delivering %s: %w", e.Capability, err)
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
	case agentplan.IfNotForeign:
		// The write-time half of the ownership rule. The producer already
		// declined to declare an entry for a path a pre-pass found foreign;
		// this re-check closes the window between that pass and this write, in
		// which a repository's own file can appear at the name. It is the same
		// question asked with the same helper, so the two answers cannot
		// disagree about what counts as niwa's own.
		owned, err := probeOwnership(e.Path, e.Owner)
		if err != nil {
			return false, err
		}
		return owned != pathForeign, nil
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
	if e.Op == agentplan.OpDeliverTree {
		if e.Source == "" {
			return fmt.Errorf("%w: tree delivery with no source for %s", errMalformedPlanEntry, e.Path)
		}
		if e.Owner == "" {
			// Without an owner line the copy fallback could not recognize its
			// own delivery on the next apply, so it would either refuse to
			// refresh forever or remove a directory it cannot vouch for.
			return fmt.Errorf("%w: tree delivery with no owner line for %s", errMalformedPlanEntry, e.Path)
		}
	}
	if e.Pre == agentplan.IfNotForeign && e.Owner == "" {
		// Without an owner line the gate would answer "foreign" for every file
		// that already exists, so a re-apply would silently stop refreshing its
		// own document.
		return fmt.Errorf("%w: ownership gate with no owner line for %s", errMalformedPlanEntry, e.Path)
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

// treeCopyMaxLinkHops bounds how deep the copy fallback follows symlinks it
// finds inside a delivered tree. A plugin tree's own shape needs none; the
// allowance exists for links a tree carries internally, and the bound is what
// keeps a link cycle from turning a fallback copy into an unbounded walk.
const treeCopyMaxLinkHops = 4

// treeDeliveryPrefersCopy decides, once per process, whether a tree delivery is
// a symlink or a real copy. It is the single decision point for the fallback:
// everything below it copies or links because this said so, rather than each
// delivery re-deciding.
//
// Directory symlinks need elevated privileges on Windows, so a copy is the
// default there. Everywhere else the symlink is preferred, because it leaves one
// source of truth: the delivered tree tracks its source with no second copy to
// go stale. A symlink write that fails anyway falls back to a copy at the call
// site, which covers a filesystem that rejects links for its own reasons.
//
// It is a package variable so tests can exercise the fallback on a platform
// where symlinks work.
var treeDeliveryPrefersCopy = func() bool { return runtime.GOOS == "windows" }

// deliverPlanTree is OpDeliverTree: make the tree at source readable at path,
// as a symlink where that works and as a real copy where it does not.
//
// niwa recognizes its own delivery by shape and by content. A symlink is niwa's
// -- only niwa plants one at a path it declared -- and is retargeted when its
// source has moved, so a stale link heals on the next apply rather than serving
// another instance's content. A directory is niwa's when it carries the sentinel
// holding owner, and is then re-delivered wholesale, which is what makes a file
// the source dropped leave the copy too. Anything else at the path is left
// exactly as it is and fails the delivery, because replacing a tree means
// removing it first and that is not a call to make on a guess.
func deliverPlanTree(path, source, owner string, mode fs.FileMode) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolving delivery source %s: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}

	info, err := os.Lstat(path)
	switch {
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("inspecting %s: %w", path, err)

	case err == nil && info.Mode()&os.ModeSymlink != 0:
		if treeLinkTargets(path, source) {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return fmt.Errorf("replacing stale delivery link %s: %w", path, rmErr)
		}

	case err == nil && info.IsDir():
		if !treeCopyIsNiwas(path, owner) {
			return fmt.Errorf("%w: %s", errForeignDeliveryTarget, path)
		}
		if rmErr := os.RemoveAll(path); rmErr != nil {
			return fmt.Errorf("refreshing delivered tree %s: %w", path, rmErr)
		}

	case err == nil:
		return fmt.Errorf("%w: %s", errForeignDeliveryTarget, path)
	}

	if !treeDeliveryPrefersCopy() {
		if linkErr := os.Symlink(source, path); linkErr == nil {
			return nil
		}
		// The platform said symlinks were available and the write disagreed. A
		// copy keeps the delivery usable rather than failing the apply.
	}

	if err := copyTree(source, path, mode, treeCopyMaxLinkHops); err != nil {
		return fmt.Errorf("copying %s into %s: %w", source, path, err)
	}
	return writePlanFile(filepath.Join(path, agentplan.TreeMarkerFileName()), []byte(owner+"\n"), 0o644)
}

// treeLinkTargets reports whether the symlink at path resolves to source. A
// relative target is resolved against the link's own directory, and a textual
// mismatch is re-checked through fully resolved paths so a source reached by a
// symlinked parent -- a linked home directory, an automounted volume -- is not
// mistaken for a stale target.
func treeLinkTargets(path, source string) bool {
	target, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if filepath.Clean(target) == filepath.Clean(source) {
		return true
	}

	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return false
	}
	return resolvedTarget == resolvedSource
}

// treeCopyIsNiwas reports whether the directory at path is a copy niwa
// delivered, recognized by the owner line in the sentinel it wrote there.
func treeCopyIsNiwas(path, owner string) bool {
	data, err := os.ReadFile(filepath.Join(path, agentplan.TreeMarkerFileName()))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(first) == strings.TrimSpace(owner)
}

// copyTree copies the tree at src to dst as real files and directories,
// following symlinks rather than reproducing them: the fallback exists precisely
// where links are unavailable, so a copy full of links would deliver nothing.
// hops bounds how many symlinked directories deep the copy follows, so a link
// cycle terminates.
func copyTree(src, dst string, mode fs.FileMode, hops int) error {
	if err := os.MkdirAll(dst, mode); err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())

		info, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", srcPath, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if hops == 0 {
				// Deeper than a delivered tree's own shape needs; a cycle is
				// the likeliest cause, and stopping here bounds the copy.
				continue
			}
			resolved, err := filepath.EvalSymlinks(srcPath)
			if err != nil {
				// A dangling link inside the source is the source's own gap;
				// the copy carries it rather than failing the apply over it.
				continue
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil {
				continue
			}
			if resolvedInfo.IsDir() {
				if err := copyTree(resolved, dstPath, mode, hops-1); err != nil {
					return err
				}
				continue
			}
			if err := copyTreeFile(resolved, dstPath, resolvedInfo.Mode().Perm()); err != nil {
				return err
			}
			continue
		}

		if info.IsDir() {
			if err := copyTree(srcPath, dstPath, mode, hops); err != nil {
				return err
			}
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyTreeFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
	}

	return nil
}

// copyTreeFile copies one regular file, preserving its permission bits.
func copyTreeFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return out.Close()
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

// exempt returns the paths the applied plans refused to write because something
// niwa did not write occupies them. Cleanup consults this before deleting a
// recorded path the current apply did not produce -- which a refused path
// always is, so without the consultation the refusal would be undone by the
// step that runs right after it.
func (r *planRun) exempt() []string {
	var out []string
	for _, o := range r.outcomes {
		for _, path := range o.plan.Exempt {
			out = appendUniqueString(out, path)
		}
	}
	return out
}
