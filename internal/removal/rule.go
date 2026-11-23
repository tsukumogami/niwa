// Package removal holds the one rule every removal niwa makes on its own goes
// through: a stopped unit is removed only when nothing in it is unique.
//
// Evaluate reads and never writes. It runs every check in a fixed order, keeps
// running them after one keeps, and names the first keeping check, so a caller
// can report everything that held a unit back as well as the one reason that
// decided it.
//
// The rule fails toward keeping. Anything a check can't decide (a git error, a
// clone URL it can't reach, an output it can't parse, a path it doesn't judge
// yet) keeps the unit, so the rule never removes something an earlier, less
// careful reap would have kept.
package removal

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// CheckName names one check of the rule. The constants below are the rule's
// order, and Evaluate runs them in exactly that order.
type CheckName string

const (
	// CheckLiveness keeps a unit whose session is working, or whose liveness
	// can't be read.
	CheckLiveness CheckName = "liveness"
	// CheckHold keeps a unit something asked to be held.
	CheckHold CheckName = "hold"
	// CheckRecords keeps a unit whose records the rule needs are missing or
	// unreadable.
	CheckRecords CheckName = "records"
	// CheckRepository keeps a unit with work in a repository that no live
	// remote ref holds.
	CheckRepository CheckName = "repository"
	// CheckOutside keeps a unit with unique files outside every repository.
	CheckOutside CheckName = "outside"
	// CheckNamedFiles keeps a unit whose named files are missing or changed.
	CheckNamedFiles CheckName = "named files"
)

// checkOrder is the order Evaluate runs the checks in.
var checkOrder = []CheckName{
	CheckLiveness,
	CheckHold,
	CheckRecords,
	CheckRepository,
	CheckOutside,
	CheckNamedFiles,
}

// Kind is what the rule decided about a unit.
type Kind uint8

const (
	// Keep leaves the unit in place. It is the zero value on purpose: a
	// verdict nobody filled in keeps.
	Keep Kind = iota
	// Reap lets the unit go.
	Reap
)

func (k Kind) String() string {
	if k == Reap {
		return "reap"
	}
	return "keep"
}

// Check is one keeping check: which check kept the unit, the paths it names
// (relative to the instance), and what it saw.
type Check struct {
	Name     CheckName
	Paths    []string
	Evidence string
}

// Verdict is the rule's answer for one unit: the decision, every keeping
// check in rule order, and the class of every path the rule judged, in path
// order. A caller can render all of it without running a check again.
type Verdict struct {
	Kind   Kind
	Checks []Check
	Paths  []PathClass
}

// First returns the first keeping check, the one that decided the verdict.
func (v Verdict) First() (Check, bool) {
	if len(v.Checks) == 0 {
		return Check{}, false
	}
	return v.Checks[0], true
}

// Liveness is the caller's verdict on the session behind a unit. The rule
// doesn't read sessions itself yet; it carries the reap's own verdict, so a
// unit the reap would have spared for its liveness is still spared.
type Liveness uint8

const (
	// LivenessUnknown is the zero value and keeps: a caller that never said
	// the session was gone never gets a unit removed.
	LivenessUnknown Liveness = iota
	// LivenessGone means the session is proven finished with.
	LivenessGone
	// LivenessLive means the session is still there.
	LivenessLive
	// LivenessUnreadable means no conclusion was reached.
	LivenessUnreadable
)

// Unit is one instance the rule is asked about.
type Unit struct {
	// Name is the instance's name, for messages.
	Name string
	// InstancePath is the instance directory.
	InstancePath string
	// Liveness is the caller's session verdict; LivenessReason says why when
	// it isn't LivenessGone.
	Liveness       Liveness
	LivenessReason string
	// JobDir is the unit's job directory, empty when it has none. The rule
	// judges what is in it as it judges the instance.
	JobDir string
}

// Policy is what the workspace and the pass give the rule.
type Policy struct {
	// Processes is the pass's one process listing. A nil listing, or one
	// that failed, keeps every unit it would have cleared.
	Processes *Listing
	// IgnoredUnique counts a git-ignored path outside a build tree as
	// unique rather than disposable ([session.removal] ignored_files).
	IgnoredUnique bool
	// JobScratchUnique counts job scratch in a unit with no handoff brief as
	// unique rather than disposable ([session.removal] job_scratch).
	JobScratchUnique bool
	// Disposable are the workspace's own disposable patterns, matched
	// beside the shipped ones.
	Disposable []string
	// BuildTrees returns the build trees the workspace declares for a
	// repository, as paths relative to its root ending in "/".
	BuildTrees func(repo string) []string
	// Env runs every git call. A nil Env can't run git, so any repository
	// found keeps its unit.
	Env *Env
	// CloneURL returns the clone URL the workspace configuration gives a
	// repository, read from the workspace root and never from anything inside
	// an instance. A repository it gives no URL for keeps its unit.
	CloneURL func(repo string) (string, bool)
	// ConfigError, when set, is why the workspace configuration couldn't be
	// read; every repository found then keeps its unit with it as evidence.
	ConfigError error
}

// evalState is what one evaluation's checks share: the walk of the instance,
// made once by the repository check and read by the outside check, the
// instance's .niwa held open for every read under it, the managed files read
// from instance.json once, and the classes every check gives.
type evalState struct {
	walk    *found
	niwa    niwaDir
	managed *managedRead
	classes classes
}

// checkFunc is one check: the keeping results it found, none when it keeps
// nothing.
type checkFunc func(ctx context.Context, u Unit, p Policy, s *evalState) []Check

// Evaluate runs the rule over one unit and returns its verdict. It reads and
// never writes. Every check runs even after one keeps, and the verdict lists
// every keeping check in rule order and every judged path with its class.
func Evaluate(ctx context.Context, u Unit, p Policy) Verdict {
	checks := map[CheckName]checkFunc{
		CheckLiveness:   checkLiveness,
		CheckHold:       checkHold,
		CheckRecords:    checkRecords,
		CheckRepository: checkRepositories,
		CheckOutside:    checkOutside,
		CheckNamedFiles: noCheckYet,
	}
	if p.ConfigError != nil {
		// Without the workspace configuration the rule can't know what the
		// workspace counts as disposable, so nothing counts as disposable
		// on its say: ignored files and job scratch act as unique, and no
		// workspace pattern or declared build tree applies.
		p.IgnoredUnique, p.JobScratchUnique = true, true
		p.Disposable, p.BuildTrees = nil, nil
	}
	s := &evalState{niwa: openNiwaDir(u.InstancePath)}
	defer s.niwa.close()
	var v Verdict
	for _, name := range checkOrder {
		for _, c := range checks[name](ctx, u, p, s) {
			c.Name = name
			v.Checks = append(v.Checks, c)
		}
	}
	v.Paths = s.classes.sorted()
	if len(v.Checks) == 0 {
		v.Kind = Reap
	}
	return v
}

// noCheckYet is a check whose records the rule doesn't read yet. It keeps
// nothing, which removes nothing an earlier reap would have kept: that reap
// read none of them either.
func noCheckYet(context.Context, Unit, Policy, *evalState) []Check { return nil }

// checkLiveness keeps a unit whose session isn't proven gone, and one with a
// live process whose working directory is inside the instance. A process
// listing that is missing or failed keeps every unit it would have cleared.
func checkLiveness(_ context.Context, u Unit, p Policy, _ *evalState) []Check {
	switch u.Liveness {
	case LivenessGone:
	case LivenessLive:
		return []Check{{Evidence: "its session is still there"}}
	default:
		reason := u.LivenessReason
		if reason == "" {
			reason = "its session's liveness could not be read"
		}
		return []Check{{Evidence: reason}}
	}
	if p.Processes == nil {
		return []Check{{Evidence: "no process listing was taken"}}
	}
	if p.Processes.Err != nil {
		return []Check{{Evidence: "the process listing failed: " + p.Processes.Err.Error()}}
	}
	var keeps []Check
	for _, pr := range p.Processes.CwdWithin(u.InstancePath) {
		keeps = append(keeps, Check{
			Paths:    []string{relOrSelf(u.InstancePath, pr.Cwd)},
			Evidence: fmt.Sprintf("a live process has its working directory in the instance: pid %d in %s", pr.PID, relOrSelf(u.InstancePath, pr.Cwd)),
		})
	}
	return keeps
}

// relOrSelf is p relative to base, or p itself when it isn't under base.
func relOrSelf(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return rel
	}
	if rb, err := filepath.EvalSymlinks(base); err == nil {
		if rel, err := filepath.Rel(rb, p); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return rel
		}
	}
	return p
}
