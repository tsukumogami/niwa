package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// destroyWorktrees is what niwa destroy knows about the linked worktrees
// inside one instance: every one niwa's walk found there, and for an
// unforced destroy, what each one's status listed and which ones the walk
// won't remove, which go with the instance directory once the instance name
// is confirmed.
type destroyWorktrees struct {
	instance string
	force    bool
	env      *removal.Env
	found    []removal.Worktree
	listed   map[string][]string
	leave    map[string]bool
}

// newDestroyWorktrees finds the worktrees inside the instance. The removal
// environment is made only for an unforced destroy of an instance that has
// any, since only that destroy runs git in them.
func newDestroyWorktrees(ctx context.Context, workspaceRoot, instance string, force bool) *destroyWorktrees {
	d := &destroyWorktrees{
		instance: instance,
		force:    force,
		found:    removal.FindWorktrees(ctx, instance),
		listed:   map[string][]string{},
		leave:    map[string]bool{},
	}
	if !force && len(d.found) > 0 {
		d.env = newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
	}
	return d
}

// close releases the removal environment.
func (d *destroyWorktrees) close() {
	if d.env != nil {
		d.env.Close()
	}
}

// destroyScanWalk is the walk an unforced destroy's scan runs before its
// first git call: the removal walk over the instance and the configuration
// check on every repository it finds, from env, handed to the scan in its
// own terms.
func destroyScanWalk(env *removal.Env) workspace.RepositoryWalkFunc {
	return func(ctx context.Context, instanceDir string) []workspace.WalkedRepository {
		repos := env.ScanRepositories(ctx, instanceDir)
		out := make([]workspace.WalkedRepository, 0, len(repos))
		for _, r := range repos {
			out = append(out, workspace.WalkedRepository{Name: r.Rel, WorkTree: r.WorkTree, Reason: r.Reason})
		}
		return out
	}
}

// scan is the worktrees' part of an unforced destroy's scan. For each
// worktree it reads the repository's configuration, which refuses the
// destroy on any reason, then reads the worktree the way its removal will
// and runs its git status. A change the status reports, a status that
// can't be read, or a worktree the walk won't remove (locked, holding a
// repository, a .git file that doesn't name its admin directory) comes back
// as a finding with the path named, to join the scan's; a worktree the walk
// won't remove, or whose status can't be read, then goes with the instance
// directory. Something that changed under the walk refuses the destroy.
func (d *destroyWorktrees) scan(ctx context.Context) ([]workspace.RepoScan, error) {
	var findings []workspace.RepoScan
	for _, w := range d.found {
		if _, reason := d.env.RecheckConfig(ctx, w.AdminDir, w.WorkTree); reason != "" {
			return nil, fmt.Errorf("nothing was destroyed: worktree %s: %s", w.Rel, reason)
		}
		if err := removal.CheckWorktree(d.instance, w.WorkTree, w.AdminDir); err != nil {
			if removal.WalkChanged(err) {
				return nil, fmt.Errorf("nothing was destroyed: worktree %s: %s", w.Rel, err)
			}
			d.leave[w.Rel] = true
			findings = append(findings, workspace.RepoScan{Name: w.Rel, Skipped: "a worktree niwa won't remove on its own (" + err.Error() + "); it goes with the instance directory"})
			continue
		}
		entries, err := d.env.WorktreeStatus(ctx, w)
		if err != nil {
			d.leave[w.Rel] = true
			findings = append(findings, workspace.RepoScan{Name: w.Rel, Skipped: "the worktree's status can't be read (" + err.Error() + "); it goes with the instance directory"})
			continue
		}
		d.listed[w.Rel] = entries
		if len(entries) > 0 {
			findings = append(findings, workspace.RepoScan{Name: w.Rel, Skipped: "the worktree has changes: " + strings.Join(entries, ", ")})
		}
	}
	return findings, nil
}

// remove removes each worktree through the no-follow walk, before the
// sessions and the instance directory. Unforced, every worktree's
// repository configuration is read again and a fresh status taken, all of
// them before the first removal: a reason, a status that can't be read, or
// a change the scan's listing didn't show stops the destroy before any
// worktree goes, with the path named. Then each is removed; a stop the walk
// makes stops the destroy there. A worktree left to go with the instance
// directory is skipped. Under --force nothing is read, and a worktree the
// walk won't remove is left to go with the instance directory, with a line
// on out naming it and the walk's reason. No git call removes or prunes a
// worktree here, and none runs between the removals.
func (d *destroyWorktrees) remove(ctx context.Context, out io.Writer) error {
	if err := d.recheck(ctx); err != nil {
		return err
	}
	return d.removeChecked(out)
}

// recheck is remove's reading half: unforced, every worktree the walk will
// remove has its repository configuration read again and a fresh status
// taken, and a reason, a status that can't be read, or a change the scan's
// listing didn't show stops the destroy with the path named. It removes
// nothing, so a caller destroying several instances runs it on every one
// before the first removal anywhere. Under --force it reads nothing.
func (d *destroyWorktrees) recheck(ctx context.Context) error {
	if d.force {
		return nil
	}
	for _, w := range d.toRemove() {
		if _, reason := d.env.RecheckConfig(ctx, w.AdminDir, w.WorkTree); reason != "" {
			return destroyWorktreeStop(w, reason)
		}
		entries, err := d.env.WorktreeStatus(ctx, w)
		if err != nil {
			return destroyWorktreeStop(w, err.Error())
		}
		if fresh := unlisted(d.listed[w.Rel], entries); len(fresh) > 0 {
			return destroyWorktreeStop(w, changedReason(prefixed(w.Rel, fresh)))
		}
	}
	return nil
}

// removeChecked is remove's removing half, run only after recheck passed:
// each worktree goes through the no-follow walk, with no git call in
// between, and a stop the walk makes stops the destroy there. Under --force
// a worktree the walk won't remove is left to go with the instance
// directory, with a line on out naming it.
func (d *destroyWorktrees) removeChecked(out io.Writer) error {
	if d.force {
		for _, w := range d.found {
			if err := worktreeRemover.RemoveWorktree(d.instance, w.WorkTree, w.AdminDir); err != nil {
				fmt.Fprintf(out, "worktree %s goes with the instance directory: %s\n", w.Rel, err)
			}
		}
		return nil
	}
	for _, w := range d.toRemove() {
		if err := worktreeRemover.RemoveWorktree(d.instance, w.WorkTree, w.AdminDir); err != nil {
			return destroyWorktreeStop(w, err.Error())
		}
	}
	return nil
}

// toRemove is every worktree found that isn't left to go with the instance
// directory.
func (d *destroyWorktrees) toRemove() []removal.Worktree {
	var out []removal.Worktree
	for _, w := range d.found {
		if !d.leave[w.Rel] {
			out = append(out, w)
		}
	}
	return out
}

// destroyWorktreeStop is the error that stops a destroy before worktree w
// goes.
func destroyWorktreeStop(w removal.Worktree, why string) error {
	return fmt.Errorf("destroy stopped before worktree %s was removed: %s; the instance and its sessions were kept", w.Rel, why)
}

// unlisted is every entry of now that listed doesn't hold.
func unlisted(listed, now []string) []string {
	seen := map[string]bool{}
	for _, e := range listed {
		seen[e] = true
	}
	var out []string
	for _, e := range now {
		if !seen[e] {
			out = append(out, e)
		}
	}
	return out
}

// prefixed names each status entry by its path under the worktree.
func prefixed(rel string, entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		_, p, _ := strings.Cut(e, " ")
		out = append(out, rel+"/"+p)
	}
	return out
}
