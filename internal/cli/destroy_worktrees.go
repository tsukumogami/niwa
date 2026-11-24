package cli

import (
	"context"
	"fmt"
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
// walk won't remove is left to go with the instance directory. No git call
// removes or prunes a worktree here, and none runs between the removals.
func (d *destroyWorktrees) remove(ctx context.Context) error {
	if d.force {
		for _, w := range d.found {
			_ = worktreeRemover.RemoveWorktree(d.instance, w.WorkTree, w.AdminDir)
		}
		return nil
	}
	stop := func(w removal.Worktree, why string) error {
		return fmt.Errorf("destroy stopped before worktree %s was removed: %s; the instance and its sessions were kept", w.Rel, why)
	}
	var remove []removal.Worktree
	for _, w := range d.found {
		if !d.leave[w.Rel] {
			remove = append(remove, w)
		}
	}
	for _, w := range remove {
		if _, reason := d.env.RecheckConfig(ctx, w.AdminDir, w.WorkTree); reason != "" {
			return stop(w, reason)
		}
		entries, err := d.env.WorktreeStatus(ctx, w)
		if err != nil {
			return stop(w, err.Error())
		}
		if fresh := unlisted(d.listed[w.Rel], entries); len(fresh) > 0 {
			return stop(w, changedReason(prefixed(w.Rel, fresh)))
		}
	}
	for _, w := range remove {
		if err := worktreeRemover.RemoveWorktree(d.instance, w.WorkTree, w.AdminDir); err != nil {
			return stop(w, err.Error())
		}
	}
	return nil
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
