package cli

import (
	"context"
	"errors"
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
	// passVerdictOnly judges every instance of the workspace, or the one
	// named, and acts on none: it records each verdict and removes, settles
	// and sweeps nothing.
	passVerdictOnly
)

// passScope is what a pass covers and the readings it is given.
type passScope struct {
	// JobsDir is the jobs directory liveness is read from.
	JobsDir string
	// Now is the time selection reads liveness at.
	Now time.Time
	// Instance, when set, is the one instance a verdict-only pass judges,
	// by its directory's name. Empty judges every instance.
	Instance string
}

// passUnit is one unit a pass takes in turn. never, when set, says why the
// act mode would never target the instance; a verdict-only pass judges it
// anyway, with its liveness unreadable for that reason.
type passUnit struct {
	reapTarget
	never string
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
	// Verdicts is every unit a verdict-only pass covered, in the order it
	// took them, reached or not. The act mode leaves it empty.
	Verdicts []unitVerdict
	// SettingsInvalid is true when the workspace's removal settings are
	// invalid: the pass printed its removed-nothing line and judged no unit.
	SettingsInvalid bool
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
// daemon, timer or background process. Each git call runs in its own
// process group, which the deadline kills. The mapped units' gate and
// on-reap commands run in their own process groups too, killed on their own
// timeouts, which are capped at what the pass has left. The backstop's gate
// keeps its own budget.
//
// In verdict-only mode the pass covers every instance of the workspace, or
// the one scope.Instance names, and each goes through the same lock, liveness
// re-read and rule as a target of the act mode, so its verdict is the one a
// reap would reach at that moment. An instance the act mode would never
// target is judged with its liveness unreadable, so it keeps on liveness with
// every later check still listed. No unit goes on to the gate, the destroy
// or the on-reap settlement, and the stored mappings, launch records and
// backstop are left alone: the pass records each verdict in res.Verdicts and
// returns.
func runPass(ctx context.Context, workspaceRoot string, mode passMode, scope passScope) (passResult, error) {
	var res passResult
	if mode != passActAll && mode != passVerdictOnly {
		return res, fmt.Errorf("unknown pass mode %d", mode)
	}
	verdictOnly := mode == passVerdictOnly
	ctx, cancel := context.WithTimeout(ctx, passDeadline)
	defer cancel()

	// The workspace configuration says what the rule counts as disposable
	// and where build trees are. An invalid [session.removal] or build_trees
	// ends the pass before anything is removed: the rule can't say what a
	// unit holds without them. A configuration that can't be read at all is
	// handed to the rule, which keeps every unit holding a clone.
	cfg, cfgErr := loadWorkspaceConfig(workspaceRoot)
	var settingsErr *config.RemovalSettingsError
	if errors.As(cfgErr, &settingsErr) {
		fmt.Fprintf(reapStderr, "niwa: removed nothing: the workspace's removal settings are invalid: %s\n",
			removal.ScrubEvidence(cfgErr.Error(), 400))
		res.SettingsInvalid = true
		return res, nil
	}

	jobsDir, now := scope.JobsDir, scope.Now
	var targets []passUnit
	var stored []workspace.SessionMapping
	if verdictOnly {
		units, err := selectVerdictUnits(workspaceRoot, scope.Instance)
		if err != nil {
			return res, err
		}
		targets = units
	} else {
		selected, unreadable, err := selectReapTargets(workspaceRoot, jobsDir, now)
		if err != nil {
			return res, err
		}
		for _, t := range selected {
			targets = append(targets, passUnit{reapTarget: t})
		}
		stored, err = selectStoredMappingTargets(workspaceRoot, jobsDir, now)
		if err != nil {
			return res, err
		}
		reportSparedInstances(os.Stderr, unreadable)
	}

	// The rule's environment and the process listing are taken once for the
	// pass, and only when there is a unit to judge, so a pass with nothing to
	// do makes no scratch directory and lists no processes.
	var policy removal.Policy
	if len(targets) > 0 {
		env := newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
		defer env.Close()
		policy = removalPolicy(cfg, env)
		policy.ConfigError = cfgErr
		procs := listProcessesFunc(ctx, env)
		instances := make([]string, 0, len(targets))
		for _, t := range targets {
			instances = append(instances, t.InstancePath)
		}
		procs.Attribute(instances, nil)
		policy.Processes = procs
	}
	rand.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })

	// The pass's deadline bounds the commands it runs too: neither the gate
	// nor the on-reap command gets more time than the pass has left. A
	// verdict-only pass sets up neither: no unit goes on to them.
	deadline, _ := ctx.Deadline()
	var onReap *onReapStep
	var gate *removalGate
	var sessionIDs map[string][]string
	lockHolder := "reap"
	if verdictOnly {
		lockHolder = "reap --verdict-only"
	} else {
		onReap = newOnReapStep(workspaceRoot, reapStderr)
		onReap.deadline = deadline
		// The workspace's pre-removal command gets the last word on each
		// target, after the rule. Its spare lines go where the on-reap
		// command's warnings go.
		gate = newRemovalGate(workspaceRoot, removeStepReap, reapStderr)
		gate.deadline = deadline
		if gate.active() {
			sessionIDs = sessionIDsByInstance(workspaceRoot)
		}
	}
	for i, t := range targets {
		if ctx.Err() != nil {
			res.Unreached = len(targets) - i
			if verdictOnly {
				for _, left := range targets[i:] {
					res.Verdicts = append(res.Verdicts, unitVerdict{Name: reapTargetName(left.reapTarget)})
				}
			}
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
		//
		// A verdict-only pass takes the same lock and makes the same re-read
		// and evaluation, records the verdict, and stops there.
		reclaimed, kept, judged := func() (bool, *keptUnit, *unitVerdict) {
			release, err := takeInstanceLock(t.InstancePath, lockHolder)
			if err != nil {
				reportInstanceLockKept(reapStderr, t.InstancePath, err)
				if verdictOnly {
					return false, nil, &unitVerdict{Name: reapTargetName(t.reapTarget)}
				}
				return false, nil, nil
			}
			defer release()

			unit := removal.Unit{
				Name:         reapTargetName(t.reapTarget),
				InstancePath: t.InstancePath,
				JobDir:       jobDirFor(jobsDir, t.SessionID),
			}
			if t.never != "" {
				unit.Liveness, unit.LivenessReason = removal.LivenessUnreadable, t.never
			} else {
				unit.Liveness, unit.LivenessReason = rereadLiveness(t.reapTarget, jobsDir, time.Now())
			}
			verdict := removal.Evaluate(ctx, unit, policy)
			if verdictOnly {
				// A unit the deadline cut short mid-evaluation has a verdict
				// built from checks that didn't finish: it counts as not
				// reached.
				return false, nil, &unitVerdict{Name: unit.Name, Reached: ctx.Err() == nil, Verdict: verdict}
			}
			if verdict.Kind != removal.Reap {
				first, _ := verdict.First()
				if first.Name == removal.CheckLiveness && unit.Liveness != removal.LivenessGone {
					// A session that came back between selection and this
					// re-read is a live session, not a kept unit: it is left
					// alone without a word, as selection leaves it. A
					// liveness the re-read couldn't read is left the same
					// way, silently, as the reap before the rule left it.
					// A liveness keep for a session read as gone names a
					// live process, or a process listing that failed, and
					// is printed like any other keep.
					return false, nil, nil
				}
				return false, &keptUnit{Name: unit.Name, Check: first}, nil
			}
			if !gate.ask(t.InstancePath, sessionIDs[t.InstancePath]) {
				return false, nil, nil
			}
			if err := destroyInstanceFunc(t.InstancePath); err != nil {
				fmt.Fprintf(os.Stderr, "niwa: warning: reaping instance %s: %s\n",
					removal.ScrubEvidence(t.InstancePath, 400), removal.ScrubEvidence(err.Error(), 400))
				// Leave the mapping in place so a later reap retries this
				// target rather than orphaning the mapping for a
				// still-present instance.
				return false, nil, nil
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
				fmt.Fprintf(os.Stderr, "niwa: reclaimed %s: %s\n",
					removal.ScrubEvidence(t.Notice.Name, 80), removal.ScrubEvidence(t.Notice.Reason, 200))
			}
			return true, nil, nil
		}()
		if judged != nil {
			res.Verdicts = append(res.Verdicts, *judged)
		}
		if reclaimed {
			res.Reaped++
		}
		if kept != nil {
			res.Kept = append(res.Kept, *kept)
			reportKeptUnit(reapStderr, *kept)
		}
	}
	if verdictOnly {
		// Nothing past the units: the stored mappings, the launch records
		// and the backstop all act, and this mode acts on nothing.
		return res, nil
	}

	// After the mapped units, the deadline decides what starts, not how long
	// it runs. Every stored mapping is still visited, but its on-reap run gets
	// no time past the deadline: one with none left is kept with the usual
	// skip warning, so every session still gets its line. The launch-record
	// sweep and the backstop start only while time is left and take no
	// deadline once started. Whatever is left waits for the next pass, which
	// finds it the same way.
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
// contents are agent-written, so the name and the evidence pass through
// removal.ScrubEvidence: terminal control sequences and credentials removed,
// and the result bounded.
func reportKeptUnit(w io.Writer, k keptUnit) {
	fmt.Fprintf(w, "niwa: kept %s: %s check: %s\n",
		removal.ScrubEvidence(k.Name, 80),
		k.Check.Name,
		removal.ScrubEvidence(k.Check.Evidence, 400))
}

// selectVerdictUnits is every instance of the workspace a verdict-only pass
// judges, or only the one whose directory is named instance. Each is joined
// with the session mapping selectReapTargets would join it with, so one the
// act mode targets is read the same way here. One the act mode would never
// target (no mapping names it, or it isn't ephemeral) carries the reason in
// never. Like selectReapTargets it reads and writes nothing in an instance.
func selectVerdictUnits(workspaceRoot, instance string) ([]passUnit, error) {
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
	var units []passUnit
	for _, rec := range records {
		if instance != "" && filepath.Base(rec.Path) != instance {
			continue
		}
		mapping, mapped := byPath[rec.Path]
		u := passUnit{reapTarget: reapTarget{InstancePath: rec.Path}}
		switch {
		case !mapped:
			u.never = "no session mapping names it, so there is no session whose liveness could be read"
		case !rec.Ephemeral || !mapping.Ephemeral:
			u.Mapping = mapping
			u.never = "it isn't an ephemeral instance, and the mapped reap removes only those"
		default:
			u.reapTarget = reapTarget{
				SessionID:    mapping.SessionID,
				InstancePath: rec.Path,
				Agent:        mapping.Agent,
				Mapping:      mapping,
			}
		}
		units = append(units, u)
	}
	return units, nil
}

// listProcessesFunc takes the pass's process listing. A variable so a test
// can hand the pass a listing of its own.
var listProcessesFunc = func(ctx context.Context, env *removal.Env) *removal.Listing {
	return env.ListProcesses(ctx)
}

// removalPolicy builds the rule's policy for a pass from the workspace
// configuration at the root: the environment, each repository's clone URL
// and declared build trees, and the [session.removal] settings.
func removalPolicy(cfg *config.WorkspaceConfig, env *removal.Env) removal.Policy {
	p := removal.Policy{Env: env, CloneURL: cloneURLsFromConfig(cfg)}
	if cfg == nil {
		return p
	}
	r := cfg.Session.Removal
	p.IgnoredUnique = r.IgnoredUnique()
	p.JobScratchUnique = r.JobScratchUnique()
	for _, d := range r.Disposable {
		p.Disposable = append(p.Disposable, d.Pattern)
	}
	trees := map[string][]string{}
	for name, o := range cfg.Repos {
		if len(o.BuildTrees) > 0 {
			trees[name] = append([]string(nil), o.BuildTrees...)
		}
	}
	p.BuildTrees = func(repo string) []string { return trees[repo] }
	return p
}

// jobDirFor is the job directory of a session under jobsDir: the directory
// named by its full id, or else the one whose name is a leading slice of the
// id, the layout Claude Code writes. Empty when the session has none.
func jobDirFor(jobsDir, sessionID string) string {
	if jobsDir == "" || sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return ""
	}
	full := filepath.Join(jobsDir, sessionID)
	if _, err := os.Lstat(full); err == nil {
		return full
	}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) >= 8 && len(name) <= len(sessionID) && sessionID[:len(name)] == name {
			return filepath.Join(jobsDir, name)
		}
	}
	return ""
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
