package cli

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// passDeadline bounds one pass over every unit. A variable so a test can
// watch a pass run out of time without waiting for it.
var passDeadline = 30 * time.Second

// newRemovalEnv resolves the rule's environment for a pass. A variable so a
// test can admit its local file remotes.
var newRemovalEnv = func(root, home, pathEnv string) *removal.Env {
	return removal.NewEnv(root, home, pathEnv)
}

// passMode is what a pass does with the units it reaches.
type passMode uint8

const (
	// passActAll acts on every unit: a unit the rule lets go is removed.
	passActAll passMode = iota + 1
)

// passScope is what a pass covers and the readings it is given.
type passScope struct {
	// JobsDir is the jobs directory liveness is read from.
	JobsDir string
	// Now is the time selection reads liveness at.
	Now time.Time
}

// keptUnit is a unit the rule kept, with the check that decided it.
type keptUnit struct {
	Name  string
	Check removal.Check
}

// passResult is what a pass did.
type passResult struct {
	// Reaped counts the instances removed.
	Reaped int
	// Kept lists the units the rule kept for something other than their
	// liveness, in the order the pass reached them.
	Kept []keptUnit
	// Unreached counts the units the deadline left in place.
	Unreached int
}

// runPass is the one pass every removal niwa makes on its own runs through.
// It gathers the mapped units whose session is gone, takes them in a fresh
// random order, and for each, under the instance's lock, re-reads liveness
// and asks removal.Evaluate for a verdict. Only a unit the rule lets go goes
// on to the workspace's [session.before_remove] gate and DestroyInstance.
//
// The whole pass runs under one deadline. A unit the pass can't reach before
// it is left in place, and the next pass asks again; the random order keeps
// one slow unit from starving the same others every time.
//
// The pass runs only inside the niwa command that called it: it starts no
// daemon, timer or background process, and every program it starts is
// killed with its process group when the deadline passes.
func runPass(ctx context.Context, workspaceRoot string, mode passMode, scope passScope) (passResult, error) {
	var res passResult
	if mode != passActAll {
		return res, fmt.Errorf("unknown pass mode %d", mode)
	}
	ctx, cancel := context.WithTimeout(ctx, passDeadline)
	defer cancel()

	jobsDir, now := scope.JobsDir, scope.Now
	targets, unreadable, err := selectReapTargets(workspaceRoot, jobsDir, now)
	if err != nil {
		return res, err
	}
	stored, err := selectStoredMappingTargets(workspaceRoot, jobsDir, now)
	if err != nil {
		return res, err
	}
	reportSparedInstances(os.Stderr, unreadable)

	// The rule's environment is resolved once for the pass, and only when
	// there is a unit to judge, so a pass with nothing to do makes no scratch
	// directory.
	var policy removal.Policy
	if len(targets) > 0 {
		env := newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
		defer env.Close()
		policy = removalPolicy(workspaceRoot, env)
	}
	rand.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })

	// The pass's deadline bounds the commands it runs too: neither the gate
	// nor the on-reap command gets more time than the pass has left.
	deadline, _ := ctx.Deadline()
	onReap := newOnReapStep(workspaceRoot, reapStderr)
	onReap.deadline = deadline
	// The workspace's pre-removal command gets the last word on each target,
	// after the rule. Its spare lines go where the on-reap command's warnings
	// go.
	gate := newRemovalGate(workspaceRoot, removeStepReap, reapStderr)
	gate.deadline = deadline
	var sessionIDs map[string][]string
	if gate.active() {
		sessionIDs = sessionIDsByInstance(workspaceRoot)
	}
	for i, t := range targets {
		if ctx.Err() != nil {
			res.Unreached = len(targets) - i
			break
		}
		// Selection happened before the spared report was printed and before
		// every earlier destroy in this loop, so the verdict that chose the
		// target is stale by the time it is acted on: the session can have
		// come back, a resume can have relaunched into the instance, or
		// another worker can be rooted there now.
		//
		// So the instance's lock is taken first, and only then is everything
		// that justified the destroy read again: the liveness verdict, for
		// every agent and not only one whose liveness is read from activity,
		// and the two guards that spare an instance a worker is rooted in.
		// The rule runs under the same lock, and the lock is held through the
		// removal gate, the destroy and the mapping's settlement, so no other
		// niwa command can act on the instance between the rule's verdict and
		// the instance being gone.
		//
		// A held lock spares the instance for this sweep, with one "niwa:
		// kept" line naming the holder, and the next sweep asks again. A kept
		// target is left exactly as it was, mapping and on-reap failure count
		// included.
		reclaimed, kept := func() (bool, *keptUnit) {
			release, err := takeInstanceLock(t.InstancePath, "reap")
			if err != nil {
				reportInstanceLockKept(reapStderr, t.InstancePath, err)
				return false, nil
			}
			defer release()

			unit := removal.Unit{
				Name:         reapTargetName(t),
				InstancePath: t.InstancePath,
			}
			unit.Liveness, unit.LivenessReason = rereadLiveness(t, jobsDir, time.Now())
			verdict := removal.Evaluate(ctx, unit, policy)
			if verdict.Kind != removal.Reap {
				first, _ := verdict.First()
				if first.Name == removal.CheckLiveness {
					// A session that came back between selection and this
					// re-read is a live session, not a kept unit: it is left
					// alone without a word, as selection leaves it.
					return false, nil
				}
				return false, &keptUnit{Name: unit.Name, Check: first}
			}
			if !gate.ask(t.InstancePath, sessionIDs[t.InstancePath]) {
				return false, nil
			}
			if err := destroyInstanceFunc(t.InstancePath); err != nil {
				fmt.Fprintf(os.Stderr, "niwa: warning: reaping instance %s: %v\n", t.InstancePath, err)
				// Leave the mapping in place so a later reap retries this
				// target rather than orphaning the mapping for a
				// still-present instance.
				return false, nil
			}
			// The session is gone and so is its instance. The mapping goes
			// once the on-reap command has run for it; a failed run keeps it
			// for the next reap. Both happen before the lock is released, so
			// the next command to take it finds neither.
			onReap.settle(t.Mapping)
			// Printed after the destroy rather than before it, so the line is
			// a report of something that happened rather than of something
			// intended: a destroy that fails above says nothing here.
			if t.Notice.Reason != "" {
				fmt.Fprintf(os.Stderr, "niwa: reclaimed %s: %s\n", t.Notice.Name, t.Notice.Reason)
			}
			return true, nil
		}()
		if reclaimed {
			res.Reaped++
		}
		if kept != nil {
			res.Kept = append(res.Kept, *kept)
			reportKeptUnit(reapStderr, *kept)
		}
	}

	// Everything after the mapped units shares the same deadline. What it
	// cuts short is left as it is and waits for the next pass, which finds it
	// the same way. A stored mapping whose on-reap run the deadline leaves no
	// time for is kept with the usual skip warning, so every session still
	// gets its line.
	for _, m := range stored {
		settleStoredMapping(workspaceRoot, jobsDir, m, onReap)
	}
	if ctx.Err() != nil {
		return res, nil
	}

	// The launch records go only once their transcripts have, which is
	// independent of whether any instance went above: a record outlives its
	// instance on purpose.
	sweepLaunchRecords(workspaceRoot, userHomeDir(), reapStderr, now)
	if ctx.Err() != nil {
		return res, nil
	}

	// Second pass: the name+TTL backstop. This is a SEPARATE scan, not a
	// branch in selectReapTargets, because EnumerateInstanceRecords derives
	// Ephemeral solely from the mapping store -- an unmapped orphan is already
	// Ephemeral:false and is dropped before any per-record branch there. The
	// backstop is the only path that may act on an UNMAPPED instance, and only
	// under its own gates (dispatch instance name, no mapping, age past the TTL).
	// It runs after the primary reclamation so the existing sweep keeps ownership
	// of every mapped instance (DESIGN Decision 4). It starts only while the
	// pass has time left.
	n, err := reapBackstop(workspaceRoot, jobsDir, now)
	if err != nil {
		return res, err
	}
	res.Reaped += n
	return res, nil
}

// reapTargetName is the name a target is reported by: the mapping's
// instance name, or the directory's when the mapping records none.
func reapTargetName(t reapTarget) string {
	if t.Mapping.InstanceName != "" {
		return t.Mapping.InstanceName
	}
	return filepath.Base(t.InstancePath)
}

// rereadLiveness is the reap's own liveness verdict, read again under the
// instance's lock: the session must still be gone, and no worker may be
// rooted in the instance by either reading niwa has. The rule carries it
// unchanged.
func rereadLiveness(t reapTarget, jobsDir string, at time.Time) (removal.Liveness, string) {
	verdict, reason := sessionState(t.Agent, t.SessionID, jobsDir, t.InstancePath, at)
	switch verdict {
	case sessionIsGone:
	case sessionIsLive:
		return removal.LivenessLive, ""
	default:
		return removal.LivenessUnreadable, reason
	}
	if _, recorded := instanceHasRecordedSession(t.InstancePath, at); recorded || instanceHasLiveJob(jobsDir, t.InstancePath) {
		return removal.LivenessLive, "a worker is still rooted in the instance"
	}
	return removal.LivenessGone, ""
}

// reportKeptUnit prints one line for a unit the rule kept, naming the
// instance, the first keeping check and what it saw. The instance's
// contents are agent-written, so every part of the line is stripped of
// terminal control sequences and bounded.
func reportKeptUnit(w io.Writer, k keptUnit) {
	fmt.Fprintf(w, "niwa: kept %s: %s check: %s\n",
		sanitizeRemovalGateText(k.Name, 80),
		k.Check.Name,
		sanitizeRemovalGateText(k.Check.Evidence, 400))
}

// removalPolicy builds the rule's policy for a pass: the environment, and
// each repository's clone URL from the workspace configuration at the root.
// A configuration that can't be read leaves every repository without a URL,
// which keeps any unit holding one.
func removalPolicy(workspaceRoot string, env *removal.Env) removal.Policy {
	p := removal.Policy{Env: env}
	cfg, err := loadWorkspaceConfig(workspaceRoot)
	if err != nil {
		p.ConfigError = err
		return p
	}
	p.CloneURL = cloneURLsFromConfig(cfg)
	return p
}

// cloneURLsFromConfig returns the clone URL the workspace configuration
// gives each repository, read from the root and never from anything inside
// an instance, which the unit's agent can write.
//
// An explicit [repos.<name>] url is that URL. A repository a source names
// is cloned from the SSH URL apply builds for it from the source's org, as
// RepoCloneURL prefers it; one the configuration doesn't name is taken to
// come from the workspace's one auto-discovering source, when there is
// exactly one. Anything else (two sources that could have supplied it, or
// none) gives no URL, and the unit keeps.
func cloneURLsFromConfig(cfg *config.WorkspaceConfig) func(string) (string, bool) {
	explicit := map[string]string{}
	listed := map[string]string{}
	var discovering []string
	if cfg != nil {
		for name, o := range cfg.Repos {
			if o.URL != "" {
				explicit[name] = o.URL
			}
		}
		for _, s := range cfg.Sources {
			if s.Org == "" {
				continue
			}
			if len(s.Repos) == 0 {
				discovering = append(discovering, s.Org)
				continue
			}
			for _, name := range s.Repos {
				if prev, ok := listed[name]; ok && prev != s.Org {
					listed[name] = ""
					continue
				}
				listed[name] = s.Org
			}
		}
	}
	return func(name string) (string, bool) {
		if name == "" || strings.ContainsAny(name, "/\\") {
			return "", false
		}
		if u, ok := explicit[name]; ok {
			return u, true
		}
		if org, ok := listed[name]; ok {
			if org == "" {
				return "", false
			}
			return workspace.SourceSSHURL(org, name), true
		}
		if len(discovering) == 1 {
			return workspace.SourceSSHURL(discovering[0], name), true
		}
		return "", false
	}
}
