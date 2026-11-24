package removal

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// The reads a removal makes right before it removes a linked worktree inside
// the instance: the worktree's HEAD and refs read again and compared with
// the re-read's snapshot, and a git status that must report no entry. Every
// git call goes through gitCommand.

// Worktree is a linked worktree niwa's walk found with its work tree and
// its admin directory both inside the instance.
type Worktree struct {
	// Rel is the work tree relative to the instance.
	Rel string
	// WorkTree and AdminDir are where the walk found them.
	WorkTree, AdminDir string
}

// Worktree returns the repository as a linked worktree inside the instance,
// and false for any other repository: a clone, one in the job directory, or
// an admin directory whose working tree is gone.
func (r RepoRead) Worktree() (Worktree, bool) {
	if !r.linked || !r.inInstance || r.prunable || r.dirs.WorkTree == "" {
		return Worktree{}, false
	}
	return Worktree{Rel: r.Rel, WorkTree: r.dirs.WorkTree, AdminDir: r.dirs.GitDir}, true
}

// FindWorktrees walks the instance for repositories the way the rule does,
// reading no git, and returns every linked worktree whose work tree and
// admin directory are both inside it, in path order. A worktree the walk
// couldn't judge (one whose admin directory is outside the instance, say)
// isn't returned, and goes with the instance directory.
func FindWorktrees(ctx context.Context, instance string) []Worktree {
	f := walkUnit(ctx, Unit{InstancePath: instance}, Policy{})
	base := filepath.Clean(instance)
	var out []Worktree
	for _, r := range f.repos {
		if !r.Linked || r.Prunable || r.WorkTree == "" || r.Base != base {
			continue
		}
		if !within(r.GitDir, base) {
			continue
		}
		out = append(out, Worktree{Rel: r.Rel, WorkTree: r.WorkTree, AdminDir: r.GitDir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out
}

// WorktreeStatus runs git status in the worktree and returns each entry it
// reports, modified and untracked alike, as "<kind> <path>" in sorted
// order; git-ignored paths aren't asked for. Like every status on the
// removal path, the configuration is read again right before it and right
// after, and a key that keeps, or a read that fails, is the error.
func (e *Env) WorktreeStatus(ctx context.Context, w Worktree) ([]string, error) {
	if e == nil {
		return nil, errors.New("git can't be run for this pass")
	}
	r := repoDirs{GitDir: w.AdminDir, WorkTree: w.WorkTree}
	rc, err := e.recheckConfig(ctx, r)
	if err != nil {
		return nil, err
	}
	cmd, err := e.gitCommand(ctx, r, callOpts{excludesUnset: rc.excludesUnset}, "status", "--porcelain=v2", "-z")
	if err != nil {
		return nil, err
	}
	out, warn, err := runWarn(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading status: %v", err)
	}
	if warn != "" {
		return nil, fmt.Errorf("the status read reported a problem: %s", warn)
	}
	if _, err := e.recheckConfig(ctx, r); err != nil {
		return nil, err
	}
	entries, err := parseStatus(out)
	if err != nil {
		return nil, err
	}
	return statusKeys(entries), nil
}

// WorktreeChanges reads the worktree w of the unit v describes, at its own
// path instancePath, again: its refs, its own entry in git worktree list
// (its HEAD, its branch and its flags) and a git status. It returns every
// difference from the re-read's snapshot snap, and every status entry,
// each naming the worktree; a read that fails is a difference too. It is
// empty when the worktree is as the re-read saw it and its status reports
// nothing.
func (e *Env) WorktreeChanges(ctx context.Context, v Verdict, snap Snapshot, instancePath string, w Worktree) []string {
	before, ok := snap.repos[w.Rel]
	if !ok {
		return []string{w.Rel + ": the re-read has no record of it"}
	}
	if e == nil {
		return []string{w.Rel + ": git can't be run for this pass"}
	}
	r := repoDirs{GitDir: w.AdminDir, WorkTree: w.WorkTree}
	rc, err := e.recheckConfig(ctx, r)
	if err != nil {
		return []string{w.Rel + ": " + err.Error()}
	}
	opts := callOpts{excludesUnset: rc.excludesUnset}
	refs, warn, err := listRefs(ctx, e, r, opts)
	switch {
	case err != nil:
		return []string{w.Rel + ": " + err.Error()}
	case warn != "":
		return []string{w.Rel + ": the ref listing reported a problem: " + warn}
	}
	fresh := map[string]string{}
	for _, rf := range refs {
		fresh[rf.Name] = rf.OID
	}
	out := mapDiffs("ref ", w.Rel, before.refs, fresh, nil)
	lab := newSnapshotLabeler(v, instancePath, instancePath)
	wts, err := e.readWorktrees(ctx, r, opts, lab)
	if err != nil {
		return append(out, w.Rel+": "+err.Error())
	}
	if was, now := before.worktrees[w.Rel], wts[w.Rel]; was != now || now == "" {
		out = append(out, "worktree "+w.Rel+" in "+w.Rel)
	}
	status, err := e.WorktreeStatus(ctx, w)
	if err != nil {
		return append(out, w.Rel+": "+err.Error())
	}
	return append(out, pathDiffs(w.Rel, nil, status)...)
}
