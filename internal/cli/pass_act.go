package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/testhook"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// The acting half of a pass: what happens to a unit the rule lets go, and to
// a tombstone an interrupted pass left.
//
// A unit the rule lets go is still in use until it is gone, so the pass
// decides first and removes after: every read that can keep the unit runs
// before the first thing is removed, and once removal starts nothing but
// the destructive steps' own guards can stop it. In order:
//
//  1. the gate: the workspace's [session.before_remove] command;
//  2. the re-read: liveness again, a fresh process listing, and every
//     repository the walk found read again and compared with what the rule
//     read, then a snapshot of each one's refs, HEADs and stashes and a
//     hash of every record under .niwa;
//  3. the sessions the pass will pass to claude rm: the unit's mapped
//     Claude sessions that the pass's shared claude agents listing (taken
//     once, for every unit) holds, each with its job directory vetted. A
//     listing that failed, a listed session that is still running, or a
//     vet that fails keeps the unit. A unit with a mapped session is
//     listed again later, by step 9b's fresh listing and by claude rm's
//     guard before each call in step 11;
//  4. the worktrees inside the instance, each linked worktree niwa's walk
//     found there, read on the instance's own path with nothing removed:
//     its repository's configuration (Env.RecheckConfig), its HEAD and
//     refs matched with the re-read's snapshot, a git status that reports
//     no entry, and the no-follow walk's dry run (removal.CheckWorktree);
//  5. the destroy prepared on the instance's own path;
//  6. the tombstone lock, taken shared without blocking;
//  7. the rename of the instance to its tombstone;
//  8. the check on the tombstone: the process listing and the snapshot
//     again, compared with the re-read's, each worktree step 4 read now
//     read at its tombstone paths by an explicit --git-dir and --work-tree
//     (HEAD, refs, status), and the finding the rename itself makes in a
//     worktree listing accepted only for those worktrees;
//  9. each worktree's repository configuration read again at its tombstone
//     path (Env.RecheckConfig): the last git call before the removal;
//  9b. the last full read before the first removal, which starts no git:
//     the session store read again, a fresh claude agents listing, and the
//     job-directory vet of every session the pass will pass to claude rm.
//     A session newly listed, now running or no longer listed, a listing
//     or store that can't be read, or a vet that fails keeps the unit;
//  10. each worktree removed inside the tombstone by the no-follow walk
//     alone, with no git call: the tree with its .git file last (matched
//     against the admin directory under the instance's original path,
//     mapped onto the tombstone), then the admin directory, then worktrees/
//     when it is empty;
//  11. the sessions: claude rm on each, through removeClaudeSessionFunc,
//     which lists the sessions again and vets the session's job directory
//     again right before the call; those two reads are the step's own
//     guard. A session absent from the listing gets no call;
//  12. the removal of the tombstone;
//  13. the release of the tombstone lock;
//  14. the on-reap command, for a mapped unit, given the instance's
//     original path as the record of where it was, its result held;
//  15. the mapping's settlement.
//
// A refusal at any step keeps the unit with one "niwa: kept" line naming
// the step and why, and the tombstone, once there is one, is renamed back.
// A keep from step 4 to step 9b removes nothing at all. A failure at steps
// 10 to 12 (a walk that stops, a claude rm that fails, a tombstone removal
// that fails) keeps the unit and renames back what is left; what is gone by
// then are worktrees every read found clean and pushed. No Claude session
// is removed and no on-reap command runs for a unit any of these keeps, and
// a kept unit's mapping is left exactly as it was.

// worktreeRemover removes a linked worktree inside an instance for niwa
// destroy: the removal package's no-follow walk.
var worktreeRemover worktree.WorktreeRemover = removal.NoFollowRemover

// removeMovedWorktreeFunc removes a linked worktree inside a unit's
// tombstone for the pass, its .git file matched against the admin directory
// under the instance's original paths: the removal package's no-follow walk.
// A variable so a test can watch the removals.
var removeMovedWorktreeFunc = removal.RemoveMovedWorktree

// removeTombstoneFunc removes a unit's tombstone through the destroy the
// pass prepared on the instance's own path. A variable so a test can stand
// in for the removal.
var removeTombstoneFunc = func(p *workspace.DestroyPlan, tombstone string) error {
	return p.Remove(tombstone)
}

// tryTombstoneLockFunc takes the workspace's tombstone lock: shared for a
// pass about to rename an instance to its tombstone, exclusive for the
// recovery of a tombstone an interrupted pass left. Either take is a single
// try that never waits. A variable so a test can watch the takes.
var tryTombstoneLockFunc = func(workspaceRoot string, shared bool) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("the home directory can't be resolved, so the tombstone lock can't be taken")
	}
	path := workspace.TombstoneLockPath(home, workspaceRoot)
	if shared {
		return workspace.TryFileLockShared(path)
	}
	return workspace.TryFileLock(path, instanceLockHolder("reap"))
}

// tombstoneLockReason says why the tombstone lock wasn't taken.
func tombstoneLockReason(err error) string {
	if errors.Is(err, workspace.ErrLockHeld) {
		return fmt.Sprintf("the tombstone lock is held (%v); the next pass asks again", err)
	}
	return fmt.Sprintf("could not take the tombstone lock: %v", err)
}

// maxDifferencesNamed bounds how many differences one kept line names.
const maxDifferencesNamed = 5

// changedReason is the kept reason for the differences a re-read or a
// tombstone check found.
func changedReason(diffs []string) string {
	shown := diffs
	suffix := ""
	if len(shown) > maxDifferencesNamed {
		shown = shown[:maxDifferencesNamed]
		suffix = fmt.Sprintf(" and %d more", len(diffs)-maxDifferencesNamed)
	}
	return "changed during the pass: " + strings.Join(shown, "; ") + suffix
}

// passActor is what one pass's units share: the rule's environment and
// policy, the pass's first process listing, and where kept units are
// recorded.
type passActor struct {
	ctx         context.Context
	root        string
	jobsDir     string
	verdictOnly bool
	lockHolder  string
	cfg         *config.WorkspaceConfig
	cfgErr      error

	env    *removal.Env
	policy removal.Policy
	// attributed is every instance a setup call named, the ones the
	// listing is attributed to.
	attributed []string

	// claude is what the pass's claude calls run with: its environment and
	// its jobs root. sessions is the pass's one claude agents listing,
	// shared by every copy of the actor.
	claude   *claudeCalls
	sessions *passSessions
}

// passSessions is a pass's one `claude agents --json --all` listing, taken
// when the first unit with a mapped Claude session reaches its session step.
type passSessions struct {
	listed bool
	rows   []claudeSessionRow
	err    error
}

// setup prepares the units of one stage of the pass. The rule's environment
// and the process listing are made once per pass, under ctx, the first time
// a stage has a unit to judge, so a pass with nothing to judge makes no
// scratch directory and lists no processes. The attribution is redone on
// every call, over every instance any call named, so a later stage's units
// (the backstop's candidates, set up after the mapped units) are attributed
// in the same listing as the earlier ones. The listing itself isn't taken
// again: each unit's re-read takes a fresh one and compares.
func (a *passActor) setup(ctx context.Context, instances []string) {
	a.attributed = append(a.attributed, instances...)
	if a.env == nil {
		a.env = newRemovalEnv(a.root, userHomeDir(), os.Getenv("PATH"))
		a.policy = removalPolicy(a.cfg, a.env)
		a.policy.ConfigError = a.cfgErr
		a.policy.JobsRoot = a.jobsDir
		a.policy.Processes = listProcessesFunc(ctx, a.env)
		a.claude = passClaudeCalls(a.env, a.jobsDir)
		a.sessions = &passSessions{}
	}
	a.policy.Processes.Attribute(a.attributed, nil)
}

// close releases the environment.
func (a *passActor) close() {
	if a.env != nil {
		a.env.Close()
	}
}

// unitSteps is what acting on a unit takes beyond the unit: the gate it is
// asked under, and for a mapped unit the on-reap step and the session ids
// the gate is told.
type unitSteps struct {
	gate       *removalGate
	onReap     *onReapStep
	sessionIDs map[string][]string
}

// takeUnit takes one unit under its instance's lock: it reads liveness
// again, asks the rule, and for a unit the rule lets go, asks the gate and
// acts. A verdict-only pass records the verdict and stops there.
func (a *passActor) takeUnit(t passUnit, steps unitSteps) (bool, *keptUnit, *unitVerdict) {
	release, err := takeInstanceLock(t.InstancePath, a.lockHolder)
	if err != nil {
		reportInstanceLockKept(reapStderr, t.InstancePath, err)
		if a.verdictOnly {
			return false, nil, &unitVerdict{Name: reapTargetName(t.reapTarget)}
		}
		return false, nil, nil
	}
	defer release()

	if _, err := os.Lstat(t.InstancePath); errors.Is(err, fs.ErrNotExist) {
		// Another pass or command removed the instance since selection:
		// there is nothing left to judge or act on, and nothing to report.
		if a.verdictOnly {
			return false, nil, &unitVerdict{Name: reapTargetName(t.reapTarget)}
		}
		return false, nil, nil
	}
	if t.backstop && backstopMapped(a.root, t.InstancePath) {
		// A mapping that names the instance appeared since selection: the
		// mapped sweep owns it now, and the backstop leaves it alone.
		return false, nil, nil
	}

	unit := removal.Unit{
		Name:         reapTargetName(t.reapTarget),
		InstancePath: t.InstancePath,
		JobDir:       jobDirFor(a.jobsDir, t.SessionID),
	}
	switch {
	case t.never != "":
		unit.Liveness, unit.LivenessReason = removal.LivenessUnreadable, t.never
	case t.backstop:
		unit.Liveness, unit.LivenessReason = guardLiveness(a.jobsDir, t.InstancePath, time.Now())
	default:
		unit.Liveness, unit.LivenessReason = rereadLiveness(t.reapTarget, a.jobsDir, time.Now())
	}
	verdict := removal.Evaluate(a.ctx, unit, a.policy)
	if a.verdictOnly {
		// A unit the deadline cut short mid-evaluation has a verdict built
		// from checks that didn't finish: it counts as not reached.
		return false, nil, &unitVerdict{Name: unit.Name, Reached: a.ctx.Err() == nil, Verdict: verdict}
	}
	if verdict.Kind != removal.Reap {
		first, _ := verdict.First()
		if first.Name == removal.CheckLiveness && unit.Liveness != removal.LivenessGone {
			// A session that came back between selection and this re-read
			// is a live session, not a kept unit: it is left alone without
			// a word, as selection leaves it. A liveness the re-read
			// couldn't read is left the same way, silently, as the reap
			// before the rule left it. A liveness keep for a session read
			// as gone names a live process, or a process listing that
			// failed, and is printed like any other keep.
			return false, nil, nil
		}
		return false, &keptUnit{Name: unit.Name, Check: first}, nil
	}
	return a.actReap(t, unit, verdict, steps)
}

// actReap takes a unit the rule let go through the acting steps, in the
// order the file's comment gives, and reports whether it was removed or why
// it was kept.
func (a *passActor) actReap(t passUnit, unit removal.Unit, v removal.Verdict, steps unitSteps) (bool, *keptUnit, *unitVerdict) {
	keep := func(reason string) (bool, *keptUnit, *unitVerdict) {
		return false, &keptUnit{Name: unit.Name, Reason: reason}, nil
	}

	// The gate: the workspace's pre-removal command gets the last word on
	// each unit, after the rule. A refusal prints its own spare line.
	gate := steps.gate
	if !gate.ask(t.InstancePath, steps.sessionIDs[t.InstancePath]) {
		return false, nil, nil
	}

	// The re-read.
	reread, diffs := a.reread(t, v)
	if len(diffs) > 0 {
		return keep(changedReason(diffs))
	}

	// The sessions the pass will pass to claude rm, each one's job directory
	// vetted. They are removed only once every keep check below has passed.
	sessions, reason := a.sessionsToRemove(t)
	if reason != "" {
		return keep(reason)
	}

	// The worktrees inside the instance, read on its own path with nothing
	// removed. They are removed inside the tombstone, after every read.
	worktrees, reason := a.checkWorktrees(t, v, reread)
	if reason != "" {
		return keep(reason)
	}

	// The destroy, checked and resolved on the instance's own path: its
	// root and symlink checks, and the trust keys that need the path to
	// exist. The prune and the withdrawal then run on this path after the
	// rename.
	warnings := newScrubLineWriter(os.Stderr, 400)
	defer warnings.Flush()
	plan, err := workspace.PrepareDestroy(a.root, t.InstancePath,
		workspace.WithDestroyReporter(workspace.NewWarningReporter(warnings)))
	if err != nil {
		return keep("the destroy was refused: " + err.Error())
	}

	// The tombstone lock, held shared from the rename until the removal or
	// the rename back returns. The instance's forms are read while it is
	// still at its own path.
	forms := instanceForms(t.InstancePath)
	testhook.RemovalAtTombstoneLock(t.InstancePath)
	releaseTomb, err := tryTombstoneLockFunc(a.root, true)
	if err != nil {
		return keep(tombstoneLockReason(err))
	}
	tomb, err := workspace.TombstoneInstance(a.root, filepath.Base(t.InstancePath))
	if err != nil {
		releaseTomb()
		return keep("renaming it to its tombstone failed: " + err.Error())
	}

	// renameBack keeps the unit after the rename: the tombstone goes back
	// under the instance's name and the lock is released.
	renameBack := func(reason string) (bool, *keptUnit, *unitVerdict) {
		if _, err := workspace.UntombstoneInstance(a.root, tomb); err != nil {
			reason += "; renaming it back failed, so it is left at " + filepath.Base(tomb) + ": " + err.Error()
		}
		releaseTomb()
		return keep(reason)
	}

	if diffs := a.tombstoneCheck(v, reread, forms, t.InstancePath, tomb, worktrees); len(diffs) > 0 {
		return renameBack(changedReason(diffs))
	}

	// The last git call: each worktree's repository configuration, read at
	// its tombstone path.
	for _, w := range worktrees {
		moved := w.Moved(t.InstancePath, tomb)
		if _, reason := a.env.RecheckConfig(a.ctx, moved.AdminDir, moved.WorkTree); reason != "" {
			return renameBack(worktreeConfigReason(w, reason))
		}
	}

	// The last full read before the first removal: the sessions listed and
	// vetted again. It starts no git.
	if reason := a.lastSessionCheck(t, sessions); reason != "" {
		return renameBack(reason)
	}

	// The worktrees, removed inside the tombstone by the no-follow walk
	// alone. No read that can keep the unit runs from here to the
	// tombstone's removal; a stop here is the walk's own.
	for _, w := range worktrees {
		moved := w.Moved(t.InstancePath, tomb)
		if err := removeMovedWorktreeFunc(tomb, forms, moved.WorkTree, moved.AdminDir); err != nil {
			return renameBack("worktree " + w.Rel + " was not removed: " + err.Error())
		}
	}

	// The sessions. Each claude rm goes through removeClaudeSessionFunc,
	// which lists the sessions and vets the session's job directory again
	// right before the call and makes none when either fails.
	for _, s := range sessions {
		if out, err := removeClaudeSessionFunc(withRemovalSession(withClaudeCalls(a.ctx, a.claude), s.SessionID), s.ID); err != nil {
			return renameBack(fmt.Sprintf("Claude session %s was not removed: %s", s.ID, firstLine(out, err)))
		}
	}

	if err := removeTombstoneFunc(plan, tomb); err != nil {
		reason := "removing it failed: " + err.Error()
		if _, lerr := os.Lstat(tomb); lerr == nil {
			// Whatever is left goes back under the instance's name, so the
			// unit is listed and judged again rather than left as a
			// tombstone.
			if _, uerr := workspace.UntombstoneInstance(a.root, tomb); uerr != nil {
				reason += "; renaming what is left back failed, so it is left at " + filepath.Base(tomb) + ": " + uerr.Error()
			}
		}
		releaseTomb()
		return keep(reason)
	}
	releaseTomb()

	// The session is gone and so is its instance. The on-reap command runs
	// now, for a mapped unit, given the instance's original path as the
	// record of where it was. A failed run isn't a refusal: it has printed
	// its warning, and the mapping settled below keeps the session for the
	// next reap to run it again. The mapping goes once the command has run
	// for it. Both happen before the instance's lock is released, so the
	// next command to take it finds neither.
	if steps.onReap != nil && t.Mapping.SessionID != "" {
		ran := steps.onReap.run(removedSession{
			SessionID:    t.Mapping.SessionID,
			ReapKey:      reapKeyFor(t.Mapping),
			InstancePath: t.Mapping.InstancePath,
		})
		steps.onReap.settleMapping(t.Mapping, ran)
	}
	return true, nil, nil
}

// checkWorktrees is the pass's read of the worktrees inside the unit, on the
// instance's own path, with nothing removed: for every linked worktree
// niwa's walk found there, its repository's configuration, its HEAD and
// refs against the re-read's snapshot and a git status that must report no
// entry, then the no-follow walk's dry run, which must find nothing it would
// stop at. It returns the worktrees, or the reason the unit keeps.
func (a *passActor) checkWorktrees(t passUnit, v removal.Verdict, snap removal.Snapshot) ([]removal.Worktree, string) {
	var worktrees []removal.Worktree
	for _, r := range v.Repos {
		if w, ok := r.Worktree(); ok {
			worktrees = append(worktrees, w)
		}
	}
	for _, w := range worktrees {
		if _, reason := a.env.RecheckConfig(a.ctx, w.AdminDir, w.WorkTree); reason != "" {
			return nil, worktreeConfigReason(w, reason)
		}
		if diffs := a.env.WorktreeChanges(a.ctx, v, snap, t.InstancePath, w); len(diffs) > 0 {
			return nil, changedReason(diffs)
		}
		if err := removal.CheckWorktree(t.InstancePath, w.WorkTree, w.AdminDir); err != nil {
			return nil, "worktree " + w.Rel + " would not be removed: " + err.Error()
		}
	}
	return worktrees, ""
}

// lastSessionCheck is the last full read before the pass's first removal:
// the session store read again, a fresh claude agents listing, and the
// job-directory vet of every session the pass will pass to claude rm. It
// keeps the unit when the sessions it would remove now differ from the ones
// sessionsToRemove chose (a session newly listed, one no longer listed, one
// now running or listed under another id), when the store or the listing
// can't be read, or when a vet fails. It starts no git. A unit with no
// mapped Claude session then or now asks for no listing. A session mapped
// after this read is harmless: claudeOwners keeps only mappings at the
// unit's original path, and nothing maps a session to a tombstone.
func (a *passActor) lastSessionCheck(t passUnit, chosen []claudeSessionRow) string {
	mappings, err := workspace.ListSessionMappings(a.root)
	if err != nil {
		return "the session store can't be read again before the first removal: " + err.Error()
	}
	owners := claudeOwners(mappings, []string{t.InstancePath})
	if len(owners) == 0 && len(chosen) == 0 {
		return ""
	}
	rows, err := listClaudeSessionsFunc(withClaudeCalls(a.ctx, a.claude))
	if err != nil {
		return "Claude Code's sessions could not be listed again before the first removal: " + err.Error()
	}
	remove, refused := listedRemovals(owners, rows)
	if len(refused) > 0 {
		return refused[0]
	}
	was := make(map[string]string, len(chosen))
	for _, s := range chosen {
		was[s.SessionID] = s.ID
	}
	now := make(map[string]bool, len(remove))
	for _, s := range remove {
		now[s.SessionID] = true
		id, ok := was[s.SessionID]
		switch {
		case !ok:
			return fmt.Sprintf("Claude session %s is newly listed since the pass listed the sessions", s.ID)
		case id != s.ID:
			return fmt.Sprintf("Claude Code lists session %s under %s now, not %s", s.SessionID, s.ID, id)
		}
	}
	for _, s := range chosen {
		if !now[s.SessionID] {
			return fmt.Sprintf("Claude Code no longer lists session %s, which the pass listed", s.ID)
		}
	}
	if err := vetBeforeRemoval(a.ctx, a.claude, remove); err != nil {
		return err.Error()
	}
	return ""
}

// worktreeConfigReason is the keep reason for a configuration read that
// keeps a worktree: the read's own reason, the worktree named.
func worktreeConfigReason(w removal.Worktree, reason string) string {
	return reason + " (worktree " + w.Rel + ")"
}

// reread is the re-read before removal. It reads the unit's liveness again
// with the current time (the session, its job entry and both rooted-worker
// guards; the two guards alone for a backstop unit), takes a fresh process
// listing, and reads every repository the walk found again, comparing what
// it reads with what the rule read. It returns the snapshot the tombstone
// check compares with, and every difference it found.
func (a *passActor) reread(t passUnit, v removal.Verdict) (removal.Snapshot, []string) {
	var diffs []string
	if t.backstop {
		if l, reason := guardLiveness(a.jobsDir, t.InstancePath, time.Now()); l != removal.LivenessGone {
			diffs = append(diffs, "session: "+reason)
		}
	} else {
		if l, reason := rereadLiveness(t.reapTarget, a.jobsDir, time.Now()); l != removal.LivenessGone {
			if reason == "" {
				reason = "it is no longer read as gone"
			}
			diffs = append(diffs, "session "+t.SessionID+": "+reason)
		}
	}
	diffs = append(diffs, a.newProcesses(instanceForms(t.InstancePath), t.InstancePath)...)
	snap := a.env.TakeSnapshot(a.ctx, v, t.InstancePath, t.InstancePath, nil)
	diffs = append(diffs, snap.RuleDifferences(v)...)
	return snap, diffs
}

// sessionsToRemove is the unit's session step up to its claude rm calls: the
// unit's mapped Claude sessions, read from the session store now, that the
// pass's one claude agents listing holds, each with its job directory
// vetted under the pass's deadline. It returns those sessions, or the reason
// the unit keeps: a store or listing that can't be read, a listed session
// that is still running or listed under an unusable id, or a vet that
// fails. A unit with no mapped Claude session has none to remove and asks
// for no listing.
func (a *passActor) sessionsToRemove(t passUnit) ([]claudeSessionRow, string) {
	mappings, err := workspace.ListSessionMappings(a.root)
	if err != nil {
		return nil, "the session store can't be read, so the sessions to remove are unknown: " + err.Error()
	}
	owners := claudeOwners(mappings, []string{t.InstancePath})
	if len(owners) == 0 {
		return nil, ""
	}
	rows, err := a.claudeListing()
	if err != nil {
		return nil, "Claude Code's sessions could not be listed: " + err.Error()
	}
	remove, refused := listedRemovals(owners, rows)
	if len(refused) > 0 {
		return nil, refused[0]
	}
	if err := vetBeforeRemoval(a.ctx, a.claude, remove); err != nil {
		return nil, err.Error()
	}
	return remove, ""
}

// claudeListing is the pass's one claude agents listing, taken on the first
// call and handed, error included, to every later one.
func (a *passActor) claudeListing() ([]claudeSessionRow, error) {
	s := a.sessions
	if !s.listed {
		s.listed = true
		s.rows, s.err = listClaudeSessionsFunc(withClaudeCalls(a.ctx, a.claude))
	}
	return s.rows, s.err
}

// instanceForms is the instance's path as given and, when it differs, with
// its links resolved: the forms a process listing can report it in. It is
// read while the instance is still at its path.
func instanceForms(instancePath string) []string {
	forms := []string{instancePath}
	if real, err := filepath.EvalSymlinks(instancePath); err == nil && real != filepath.Clean(instancePath) {
		forms = append(forms, real)
	}
	return forms
}

// tombstoneCheck repeats the process listing and the snapshot on the
// tombstone and compares them with the re-read's. Every git read that fails
// there counts as a difference. forms names the instance as instanceForms
// read it before the rename. worktrees are the ones checkWorktrees read on
// the instance's own path: each is read again at its tombstone paths, and
// the finding the rename makes in a worktree listing is accepted for them
// alone.
func (a *passActor) tombstoneCheck(v removal.Verdict, before removal.Snapshot, forms []string, instancePath, tomb string, worktrees []removal.Worktree) []string {
	diffs := a.newProcesses(forms, tomb)
	checked := make([]string, 0, len(worktrees))
	for _, w := range worktrees {
		checked = append(checked, w.Rel)
	}
	after := a.env.TakeMovedSnapshot(a.ctx, v, instancePath, tomb, checked)
	return append(diffs, removal.CompareSnapshots(before, after, nil)...)
}

// newProcesses takes a fresh process listing and names every process in at
// that the pass's first listing didn't find in the instance, in any of
// instanceForms. A listing that fails is a difference.
func (a *passActor) newProcesses(instanceForms []string, at string) []string {
	fresh := listProcessesFunc(a.ctx, a.env)
	procs, err := removal.NewWithin(a.policy.Processes, instanceForms, fresh, at)
	if err != nil {
		return []string{"process listing: " + err.Error()}
	}
	var out []string
	for _, p := range procs {
		where := p.Cwd
		if rel, err := filepath.Rel(at, p.Cwd); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			where = rel
		}
		out = append(out, fmt.Sprintf("process %d (working directory %s)", p.PID, where))
	}
	return out
}

// guardLiveness is a backstop unit's liveness: gone unless either of the two
// rooted-worker guards finds a worker in the instance.
func guardLiveness(jobsDir, instancePath string, now time.Time) (removal.Liveness, string) {
	if instanceHasLiveJob(jobsDir, instancePath) {
		return removal.LivenessLive, "a worker is still rooted in the instance"
	}
	if _, recorded := instanceHasRecordedSession(instancePath, now); recorded {
		return removal.LivenessLive, "a worker is still rooted in the instance"
	}
	return removal.LivenessGone, ""
}

// backstopMapped reports whether a session mapping names instancePath now.
// A mapping store that can't be read counts as naming it, so the backstop
// leaves the instance alone.
func backstopMapped(workspaceRoot, instancePath string) bool {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return true
	}
	for _, m := range mappings {
		if m.InstancePath == instancePath {
			return true
		}
	}
	return false
}

// recoverTombstones renames back each tombstone an interrupted pass left,
// so the pass lists and judges it as usual. It runs only when the tombstone
// lock can be taken exclusively without waiting: a pass that holds a
// tombstone holds the lock shared until it removes the tombstone or renames
// it back, so recovery never touches a tombstone a live pass owns, and the
// operating system drops the lock when its holder dies, which is what
// makes a tombstone left behind recoverable. When the lock is held,
// recovery waits for a later pass.
//
// Holding the lock, it Lstats each leftover tombstone and goes on only for
// a real directory directly under the root, never a symlink, so a link
// planted at a tombstone's name can't have it rename a directory
// elsewhere. A tombstone whose instance name is taken again is kept where
// it is and listed.
func recoverTombstones(workspaceRoot string, res *passResult) {
	tombs, err := workspace.ListTombstones(workspaceRoot)
	if err != nil || len(tombs) == 0 {
		return
	}
	release, err := tryTombstoneLockFunc(workspaceRoot, false)
	if err != nil {
		return
	}
	defer release()
	for _, tomb := range tombs {
		base := filepath.Base(tomb)
		keep := func(reason string) {
			k := keptUnit{Name: base, Reason: reason}
			res.Kept = append(res.Kept, k)
			reportKeptUnit(reapStderr, k)
		}
		if _, ok := workspace.ParseTombstoneName(base); !ok {
			keep("a leftover tombstone whose name doesn't name an instance; left where it is")
			continue
		}
		info, err := os.Lstat(tomb)
		switch {
		case err != nil:
			continue
		case info.Mode()&os.ModeSymlink != 0:
			keep("a leftover tombstone that is a symlink; neither it nor what it points to is touched")
			continue
		case !info.IsDir():
			keep("a leftover tombstone that isn't a directory; left where it is")
			continue
		}
		if _, err := workspace.UntombstoneInstance(workspaceRoot, tomb); err != nil {
			if errors.Is(err, workspace.ErrInstanceNameTaken) {
				keep("a tombstone an interrupted pass left, whose instance name is taken again; left where it is")
				continue
			}
			keep("a tombstone an interrupted pass left could not be renamed back: " + err.Error())
		}
	}
}
