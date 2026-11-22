package removal

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// maxNamed bounds how many paths or refs one check names in its evidence;
// the rest are counted.
const maxNamed = 5

// clone is a repository the rule judges: a clone at <group>/<repo> in the
// instance, with the clone URL the workspace configuration gives it.
type clone struct {
	// Rel is the work tree's path relative to the instance.
	Rel      string
	WorkTree string
	GitDir   string
	URL      string
}

// found is what the walk of an instance turned up.
type found struct {
	clones []clone
	// unjudged are .git entries the rule doesn't judge yet, relative to the
	// instance. Each keeps the unit.
	unjudged []string
	// errs are paths the walk couldn't read, each with its error.
	errs []Check
}

// walkInstance walks the instance for .git entries. It reads every entry
// with Lstat and never follows a symlink, so nothing behind a link is read.
// A .git directory at <group>/<repo>/.git whose repository has a clone URL
// is a clone the rule judges; every other .git entry, a file or a
// directory, is one it doesn't judge yet and keeps the unit with its path
// named. The walk doesn't descend into a git directory, but it does walk
// each work tree, so a repository nested inside a clone is found too.
func walkInstance(ctx context.Context, instance string, cloneURL func(string) (string, bool)) found {
	var f found
	err := filepath.WalkDir(instance, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, relErr := filepath.Rel(instance, path)
		if relErr != nil {
			rel = path
		}
		if err != nil {
			f.errs = append(f.errs, Check{Paths: []string{rel}, Evidence: fmt.Sprintf("unreadable: %s", rel)})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}
		parent := filepath.Dir(rel)
		if d.IsDir() && d.Type()&fs.ModeSymlink == 0 {
			if c, ok := judgedClone(instance, parent, cloneURL); ok {
				f.clones = append(f.clones, c)
			} else {
				f.unjudged = append(f.unjudged, rel)
			}
			return filepath.SkipDir
		}
		f.unjudged = append(f.unjudged, rel)
		return nil
	})
	if err != nil {
		f.errs = append(f.errs, Check{Evidence: fmt.Sprintf("the instance walk stopped: %v", err)})
	}
	return f
}

// judgedClone reports whether the work tree at parent, relative to the
// instance, is a clone the rule judges: exactly <group>/<repo>, with a
// clone URL for <repo>.
func judgedClone(instance, parent string, cloneURL func(string) (string, bool)) (clone, bool) {
	parts := strings.Split(filepath.ToSlash(parent), "/")
	if len(parts) != 2 || strings.HasPrefix(parts[0], ".") || cloneURL == nil {
		return clone{}, false
	}
	url, ok := cloneURL(parts[1])
	if !ok || url == "" {
		return clone{}, false
	}
	work := filepath.Join(instance, parent)
	return clone{Rel: parent, WorkTree: work, GitDir: filepath.Join(work, ".git"), URL: url}, true
}

// checkRepositories is the repository check: every clone in the instance is
// judged, and every .git entry it doesn't judge keeps the unit.
func checkRepositories(ctx context.Context, u Unit, p Policy) []Check {
	f := walkInstance(ctx, u.InstancePath, p.CloneURL)
	keeps := append([]Check(nil), f.errs...)
	for _, rel := range f.unjudged {
		keeps = append(keeps, Check{Paths: []string{rel}, Evidence: fmt.Sprintf("a git entry the rule does not judge yet: %s", rel)})
	}
	sort.Slice(f.clones, func(i, j int) bool { return f.clones[i].Rel < f.clones[j].Rel })
	for _, c := range f.clones {
		if p.ConfigError != nil {
			keeps = append(keeps, Check{Paths: []string{c.Rel}, Evidence: fmt.Sprintf("%s: the workspace configuration could not be read: %v", c.Rel, p.ConfigError)})
			continue
		}
		keeps = append(keeps, checkClone(ctx, p.Env, c)...)
	}
	return keeps
}

// checkClone judges one clone. The configuration read comes first and a
// program-naming key stops everything else there. Then every ref whose tip
// the clone URL doesn't hold keeps, as does any status entry, any git error
// and an unreachable clone URL.
func checkClone(ctx context.Context, e *Env, c clone) []Check {
	keep := func(evidence string, paths ...string) []Check {
		if len(paths) == 0 {
			paths = []string{c.Rel}
		}
		return []Check{{Paths: paths, Evidence: c.Rel + ": " + evidence}}
	}
	if e == nil {
		return keep("git can't be run for this pass")
	}
	r := repoDirs{GitDir: c.GitDir, WorkTree: c.WorkTree}

	rc, err := e.readConfig(ctx, r)
	if err != nil {
		return keep(err.Error())
	}
	if rc.programKey != "" {
		return keep("repository configuration names a program: " + rc.programKey)
	}
	opts := callOpts{excludesUnset: rc.excludesUnset}

	var keeps []Check

	refs, err := listRefs(ctx, e, r, opts)
	if err != nil {
		return keep(err.Error())
	}
	if len(refs) > 0 {
		tips, err := e.liveTips(ctx, c.URL)
		if err != nil {
			keeps = append(keeps, keep("clone URL unreachable: "+err.Error())...)
		} else {
			var unheld []string
			for _, rf := range refs {
				if !tips[rf.OID] {
					unheld = append(unheld, refLabel(rf.Name))
				}
			}
			if len(unheld) > 0 {
				keeps = append(keeps, keep("commits the clone URL does not show: "+nameSome(unheld))...)
			}
		}
	}

	entries, err := repoStatus(ctx, e, r, opts)
	if err != nil {
		return append(keeps, keep(err.Error())...)
	}
	if len(entries) > 0 {
		var paths []string
		for _, en := range entries {
			paths = append(paths, filepath.Join(c.Rel, en.Path))
		}
		var shown []string
		for _, en := range entries {
			shown = append(shown, en.Path)
		}
		keeps = append(keeps, keep("uncommitted changes: "+nameSome(shown), paths...)...)
	}
	return keeps
}

// listRefs runs for-each-ref with NUL separators.
func listRefs(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]ref, error) {
	cmd, err := e.gitCommand(ctx, r, o, "for-each-ref", "--format=%(objectname)%00%(refname)%00")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing refs: %v", err)
	}
	return parseRefs(out)
}

// repoStatus runs git status in its NUL-terminated form.
func repoStatus(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]statusEntry, error) {
	cmd, err := e.gitCommand(ctx, r, o, "status", "--porcelain=v2", "-z")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading status: %v", err)
	}
	return parseStatus(out)
}

// refLabel names a ref the way a person reads it: a local branch by its
// branch name, anything else by its full name.
func refLabel(name string) string {
	if b, ok := strings.CutPrefix(name, "refs/heads/"); ok {
		return "branch " + b
	}
	return name
}

// nameSome joins the first few names and counts the rest.
func nameSome(names []string) string {
	if len(names) <= maxNamed {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNamed], ", "), len(names)-maxNamed)
}
