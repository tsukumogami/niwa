package removal

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// clone is a repository the rule judges: a clone at <group>/<repo> in the
// instance, with the clone URL the workspace configuration gives it.
type clone struct {
	// Rel is the work tree's path relative to the instance.
	Rel      string
	Repo     string
	WorkTree string
	GitDir   string
	URL      string
	// wtInfo and gitInfo are what the walk saw at the work tree and the git
	// directory, so the check can tell the directory git is about to run
	// in is still the one the walk found.
	wtInfo, gitInfo fs.FileInfo
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
			f.errs = append(f.errs, Check{Paths: []string{rel}, Evidence: fmt.Sprintf("unreadable: %s: %s", rel, errText(err))})
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
				gitInfo, gerr := d.Info()
				wtInfo, werr := os.Lstat(c.WorkTree)
				if gerr != nil || werr != nil {
					f.errs = append(f.errs, Check{Paths: []string{parent}, Evidence: fmt.Sprintf("unreadable: %s", parent)})
					return filepath.SkipDir
				}
				c.gitInfo, c.wtInfo = gitInfo, wtInfo
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
	return clone{Rel: parent, Repo: parts[1], WorkTree: work, GitDir: filepath.Join(work, ".git"), URL: url}, true
}
