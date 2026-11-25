package removal

import (
	"context"
	"strings"
)

// ScanRepository is one repository the walk found in an instance, for niwa
// destroy's scan: a work tree the scan reads with its own status, or one it
// runs no git call in, with the reason.
type ScanRepository struct {
	// Rel names it relative to the instance, as the walk labels it, or "."
	// for a walk finding that names no path.
	Rel string
	// WorkTree is the work tree the scan reads; empty when Reason is set.
	WorkTree string
	// Reason, when set, is why the scan runs no git call in it: a key that
	// names a program, named; a configuration that can't be read or that
	// doesn't match the walk; one of the walk's own keeps (an external git
	// directory, a .git that is a link, an unreadable git file, another
	// filesystem, a worktree outside the instance) or a path it couldn't
	// read; or a repository with no work tree for the scan to read.
	Reason string
}

// ScanRepositories walks the instance the way the removal pass does and runs
// the configuration check on every repository the walk finds, the clones,
// submodules, gitlinks and other nested repositories and linked worktrees
// alike, before any other git call in any of them. Every walk keep and
// unreadable path comes back with its evidence as the reason, and so does
// every repository whose check keeps it, with the key named; the scan runs
// no git call in those. Every other repository with a work tree comes back
// with it, to be read with its own status, except a linked worktree under
// the instance's .niwa directory, which the scan of its repository's
// worktrees and the destroy's worktree scan read. A repository with no work
// tree comes back with a reason: the scan can't read what it holds.
func (e *Env) ScanRepositories(ctx context.Context, instance string) []ScanRepository {
	if e == nil {
		return []ScanRepository{{Rel: ".", Reason: "git can't be run for this pass"}}
	}
	f := walkUnit(ctx, Unit{InstancePath: instance}, Policy{})
	var out []ScanRepository
	for _, c := range append(append([]Check(nil), f.errs...), f.keeps...) {
		rel := "."
		if len(c.Paths) > 0 && c.Paths[0] != "" {
			rel = c.Paths[0]
		}
		out = append(out, ScanRepository{Rel: rel, Reason: c.Evidence})
	}
	for _, r := range f.repos {
		t := repoTarget{Rel: r.Rel, Base: r.Base, Dirs: r.dirs(), wtInfo: r.wtInfo, gitInfo: r.gitInfo, walk: &f, repo: r}
		if _, res, ok := e.openRepo(ctx, t); !ok {
			var why []string
			for _, k := range res.keeps {
				why = append(why, k.Evidence)
			}
			if len(why) == 0 {
				why = append(why, r.Rel+": the configuration check kept it")
			}
			out = append(out, ScanRepository{Rel: r.Rel, Reason: strings.Join(why, "; ")})
			continue
		}
		switch {
		case r.Linked && r.WorkTree != "" && underNiwaDir(r.Rel):
			continue
		case r.Prunable:
			out = append(out, ScanRepository{Rel: r.Rel, Reason: r.Rel + ": a worktree whose working tree is gone (" + f.label(r.PrunedAt) + ")"})
		case r.WorkTree == "":
			out = append(out, ScanRepository{Rel: r.Rel, Reason: r.Rel + ": a repository with no work tree; the scan can't read what it holds"})
		default:
			out = append(out, ScanRepository{Rel: r.Rel, WorkTree: r.WorkTree})
		}
	}
	return out
}

// underNiwaDir reports whether rel, a walk label, is in the instance's .niwa
// directory.
func underNiwaDir(rel string) bool {
	return rel == ".niwa" || strings.HasPrefix(rel, ".niwa/")
}
