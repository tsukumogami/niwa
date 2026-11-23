package removal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxGitFileBytes bounds a .git file and the commondir and gitdir files the
// walk reads in a worktree's admin directory.
const maxGitFileBytes = 4096

// foundRepo is one repository the walk found: its git directory, and its
// work tree or none. Every one goes through the per-repository check.
type foundRepo struct {
	// Rel names the repository in evidence: its work tree, or its git
	// directory when it has none, relative to the instance, or
	// "job:"-prefixed in the job directory.
	Rel string
	// Base is the root it was found under, the instance or the job
	// directory.
	Base     string
	GitDir   string
	WorkTree string
	// Common is the common git directory: GitDir itself, or for a linked
	// worktree the directory its commondir names.
	Common string
	// Repo and URL are set for a workspace clone at <group>/<repo> and for a
	// linked worktree of one. Every other repository has no clone URL.
	Repo, URL string
	// Linked is a linked worktree's admin directory.
	Linked bool
	// Prunable is an admin directory whose working tree is gone; PrunedAt
	// is the working tree its gitdir file names.
	Prunable bool
	PrunedAt string
	// Structural is a git directory found by its HEAD, objects and refs
	// rather than by a .git entry; InModules is one under a modules/
	// directory; ViaFile is one a .git file names.
	Structural, InModules, ViaFile bool
	// wtInfo and gitInfo are what the walk saw at the work tree and the git
	// directory, so the check can tell the directory git is about to run
	// in is still the one the walk found.
	wtInfo, gitInfo fs.FileInfo
}

// dirs is where the repository's git calls run.
func (r foundRepo) dirs() repoDirs { return repoDirs{GitDir: r.GitDir, WorkTree: r.WorkTree} }

// adminDir is a worktree admin directory under a common directory's
// worktrees/.
type adminDir struct {
	Path, Common string
	info         fs.FileInfo
	reached      bool
}

// root is a directory the walk covers, as given and with its links
// resolved, so a path git prints in either form is recognized.
type root struct {
	given, real, prefix string
}

// found is what the walk of a unit turned up.
type found struct {
	// repos is every repository found, in the instance and in the job
	// directory, each with its git directory and its work tree or none.
	repos []foundRepo
	// keeps are what the walk's own rules keep the unit on: another
	// filesystem, an external git directory, an unreadable git file, a
	// worktree the walk didn't reach.
	keeps []Check
	// errs are paths the walk couldn't read, each with its error.
	errs []Check
	// skip holds every directory the outside checks leave to the
	// repository check or keep on their own: each repository's work tree
	// and git directory, and each directory on another filesystem.
	skip   map[string]bool
	roots  []root
	admins []adminDir
}

// Repositories lists every repository the walk found, with its git
// directory and its work tree or none, prunable worktrees' admin
// directories included. The configuration read runs on each before any
// other git call in it.
func (f *found) Repositories() []repoDirs {
	out := make([]repoDirs, 0, len(f.repos))
	for _, r := range f.repos {
		out = append(out, r.dirs())
	}
	return out
}

// inside reports whether p, cleaned without following a link, is in the
// instance or the job directory, in either form.
func (f *found) inside(p string) bool {
	_, ok := f.rootOf(p)
	return ok
}

// rootOf returns the form of the root p sits under.
func (f *found) rootOf(p string) (string, bool) {
	p = filepath.Clean(p)
	for _, r := range f.roots {
		for _, base := range []string{r.given, r.real} {
			if base != "" && within(p, base) {
				return base, true
			}
		}
	}
	return "", false
}

// label names p in evidence: relative to the instance, "job:"-prefixed in
// the job directory, and as given anywhere else.
func (f *found) label(p string) string {
	p = filepath.Clean(p)
	for _, r := range f.roots {
		for _, base := range []string{r.given, r.real} {
			if base == "" || !within(p, base) {
				continue
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return p
			}
			rel = filepath.ToSlash(rel)
			if r.prefix == "" {
				return rel
			}
			if rel == "." {
				return r.prefix
			}
			return r.prefix + rel
		}
	}
	return p
}

// samePath reports whether listed, a path git printed, names walked, a
// path the walk found, compared as text in each form of the roots.
func (f *found) samePath(listed, walked string) bool {
	listed, walked = filepath.Clean(listed), filepath.Clean(walked)
	if listed == walked {
		return true
	}
	for _, r := range f.roots {
		if r.real == "" || r.real == r.given {
			continue
		}
		for _, pair := range [][2]string{{r.given, r.real}, {r.real, r.given}} {
			if rel, err := filepath.Rel(pair[0], walked); err == nil && within(walked, pair[0]) {
				if filepath.Join(pair[1], rel) == listed {
					return true
				}
			}
		}
	}
	return false
}

// keep records a walk rule's keep, naming path, once.
func (f *found) keep(path, evidence string) {
	for _, k := range f.keeps {
		if k.Evidence == evidence && len(k.Paths) == 1 && k.Paths[0] == path {
			return
		}
	}
	f.keeps = append(f.keeps, Check{Paths: []string{path}, Evidence: evidence})
}

// walkDevice is the device a walked entry is on. A variable so a test can
// put a directory on another device without mounting one.
var walkDevice = func(path string, info fs.FileInfo) (uint64, bool) { return deviceOf(info) }

// walker walks one unit.
type walker struct {
	ctx      context.Context
	f        *found
	instance string
	cloneURL func(string) (string, bool)
	// sightings are the .git files the walk read, resolved later.
	sightings []sighting
}

// sighting is a .git file that names a git directory: its work tree, the
// directory it names and, for a linked worktree, the common directory.
type sighting struct {
	label, workTree, target, common string
	linked                          bool
}

// walkUnit walks the instance and the unit's job directory for
// repositories. Every entry is read with Lstat and no link is followed.
// A directory on another device than its root's, the jobs root's for the
// job directory, keeps the unit and isn't entered. The walk records every
// .git directory and .git file and every git directory recognized by a
// HEAD file beside objects/ and refs/, whatever its name, and doesn't enter
// a git directory; under each it looks for submodule stores in modules/ at
// any depth and worktree admin directories in worktrees/. It enters build
// trees like any other directory, so a repository inside one is found.
func walkUnit(ctx context.Context, u Unit, p Policy) found {
	f := found{skip: map[string]bool{}}
	w := &walker{ctx: ctx, f: &f, instance: u.InstancePath, cloneURL: p.CloneURL}
	f.roots = append(f.roots, root{given: filepath.Clean(u.InstancePath), real: canonical(u.InstancePath)})
	if u.JobDir != "" {
		f.roots = append(f.roots, root{given: filepath.Clean(u.JobDir), real: canonical(u.JobDir), prefix: jobPrefix})
	}

	if info, err := os.Stat(u.InstancePath); err != nil {
		f.errs = append(f.errs, Check{Evidence: fmt.Sprintf("unreadable: the instance: %s", errText(err))})
	} else if dev, ok := walkDevice(u.InstancePath, info); ok {
		w.walkRoot(u.InstancePath, dev)
	} else {
		f.errs = append(f.errs, Check{Evidence: "unreadable: the instance's device can't be read"})
	}
	if u.JobDir != "" {
		w.walkJobDir(u.JobDir, p.JobsRoot)
	}
	w.resolve()
	return f
}

// walkJobDir walks the job directory, comparing devices with the jobs
// root's. A job directory that is missing is no job directory; one that
// isn't a real directory is the outside check's to keep.
func (w *walker) walkJobDir(jobDir, jobsRoot string) {
	info, err := os.Lstat(jobDir)
	if err != nil || !info.IsDir() {
		return
	}
	ref := jobsRoot
	if ref == "" {
		ref = filepath.Dir(jobDir)
	}
	refInfo, err := os.Stat(ref)
	if err != nil {
		w.f.errs = append(w.f.errs, Check{Paths: []string{jobPrefix}, Evidence: "unreadable: the jobs root: " + errText(err)})
		return
	}
	dev, ok := walkDevice(ref, refInfo)
	if !ok {
		w.f.errs = append(w.f.errs, Check{Paths: []string{jobPrefix}, Evidence: "unreadable: the jobs root's device can't be read"})
		return
	}
	if d, ok := walkDevice(jobDir, info); !ok || d != dev {
		w.f.skip[jobDir] = true
		w.f.keep(jobPrefix, "another filesystem at "+jobPrefix)
		return
	}
	w.walkRoot(jobDir, dev)
}

// walkRoot walks one root, every directory of it on device dev.
func (w *walker) walkRoot(rootDir string, dev uint64) {
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := w.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		label := w.f.label(path)
		if err != nil {
			w.f.errs = append(w.f.errs, Check{Paths: []string{label}, Evidence: fmt.Sprintf("unreadable: %s: %s", label, errText(err))})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == rootDir {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Never followed. A .git that is a link can't be judged.
			if d.Name() == ".git" {
				w.f.keep(label, "unreadable git file: "+label+" is a symlink")
			}
			return nil
		}
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				w.f.errs = append(w.f.errs, Check{Paths: []string{label}, Evidence: fmt.Sprintf("unreadable: %s: %s", label, errText(err))})
				return filepath.SkipDir
			}
			if got, ok := walkDevice(path, info); !ok || got != dev {
				w.f.skip[path] = true
				w.f.keep(label, "another filesystem at "+label)
				return filepath.SkipDir
			}
			switch {
			case d.Name() == ".git":
				w.addGitDir(path, filepath.Dir(path), info, false)
				return filepath.SkipDir
			case isGitDir(path):
				w.addGitDir(path, "", info, true)
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".git" {
			if d.Type().IsRegular() {
				w.readGitFile(path)
			} else {
				w.f.keep(label, "unreadable git file: "+label+" is not a regular file")
			}
		}
		return nil
	})
	if err != nil {
		w.f.errs = append(w.f.errs, Check{Evidence: fmt.Sprintf("the walk stopped: %v", err)})
	}
}

// isGitDir reports whether dir is a git directory by its structure: a
// regular HEAD file beside objects/ and refs/ directories, none of them a
// link.
func isGitDir(dir string) bool {
	head, err := os.Lstat(filepath.Join(dir, "HEAD"))
	if err != nil || !head.Mode().IsRegular() {
		return false
	}
	for _, sub := range []string{"objects", "refs"} {
		info, err := os.Lstat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// addGitDir records a git directory and scans it for submodule stores and
// worktree admin directories. workTree is the directory holding a .git
// directory, empty for one found by its structure.
func (w *walker) addGitDir(gitDir, workTree string, gitInfo fs.FileInfo, structural bool) {
	r := foundRepo{GitDir: gitDir, WorkTree: workTree, Common: gitDir, Structural: structural, gitInfo: gitInfo}
	if workTree != "" {
		wtInfo, err := os.Lstat(workTree)
		if err != nil {
			label := w.f.label(workTree)
			w.f.errs = append(w.f.errs, Check{Paths: []string{label}, Evidence: "unreadable: " + label})
			return
		}
		r.wtInfo = wtInfo
		if url, repo, ok := w.clone(workTree); ok {
			r.URL, r.Repo = url, repo
		}
	}
	w.add(r)
	w.scanGitDir(gitDir)
}

// clone reports whether workTree is a workspace clone: exactly
// <group>/<repo> in the instance, with a clone URL for <repo>.
func (w *walker) clone(workTree string) (string, string, bool) {
	rel, err := filepath.Rel(w.instance, workTree)
	if err != nil || !within(workTree, w.instance) {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || strings.HasPrefix(parts[0], ".") || w.cloneURL == nil {
		return "", "", false
	}
	url, ok := w.cloneURL(parts[1])
	if !ok || url == "" {
		return "", "", false
	}
	return url, parts[1], true
}

// add records a repository, once per git directory.
func (w *walker) add(r foundRepo) {
	for _, have := range w.f.repos {
		if have.gitInfo != nil && r.gitInfo != nil && os.SameFile(have.gitInfo, r.gitInfo) {
			return
		}
	}
	w.f.repos = append(w.f.repos, r)
}

// scanGitDir looks under a git directory for submodule stores, at any
// depth of modules/, and for worktree admin directories in worktrees/.
// Nothing under it is followed through a link: a modules/ or worktrees/
// that is a link or not a directory, or a link inside modules/, keeps the
// unit as an unreadable git file.
func (w *walker) scanGitDir(gitDir string) {
	modules := filepath.Join(gitDir, "modules")
	if w.realDir(modules) {
		_ = filepath.WalkDir(modules, func(path string, d fs.DirEntry, err error) error {
			if ctxErr := w.ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err != nil {
				label := w.f.label(path)
				w.f.errs = append(w.f.errs, Check{Paths: []string{label}, Evidence: fmt.Sprintf("unreadable: %s: %s", label, errText(err))})
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				label := w.f.label(path)
				w.f.keep(label, "unreadable git file: "+label+" is a symlink")
				return nil
			}
			if path == modules || !d.IsDir() {
				return nil
			}
			if isGitDir(path) {
				info, err := d.Info()
				if err != nil {
					return filepath.SkipDir
				}
				w.add(foundRepo{GitDir: path, Common: path, Structural: true, InModules: true, gitInfo: info})
				w.scanGitDir(path)
				return filepath.SkipDir
			}
			return nil
		})
	}
	trees := filepath.Join(gitDir, "worktrees")
	if !w.realDir(trees) {
		return
	}
	entries, err := os.ReadDir(trees)
	if err != nil {
		label := w.f.label(trees)
		w.f.errs = append(w.f.errs, Check{Paths: []string{label}, Evidence: fmt.Sprintf("unreadable: %s: %s", label, errText(err))})
		return
	}
	for _, e := range entries {
		path := filepath.Join(trees, e.Name())
		if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
			label := w.f.label(path)
			w.f.keep(label, "unreadable git file: "+label+" is not a worktree admin directory")
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		w.f.admins = append(w.f.admins, adminDir{Path: path, Common: gitDir, info: info})
	}
}

// realDir reports whether path, a directory a git directory may hold, is
// there as a real directory to read. One that is missing isn't. One that
// is a link or not a directory keeps the unit as an unreadable git file,
// and so does one that can't be read with Lstat: nothing behind it is
// read.
func (w *walker) realDir(path string) bool {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false
	case err != nil:
		label := w.f.label(path)
		w.f.keep(label, "unreadable git file: "+label+": "+errText(err))
		return false
	case info.Mode()&fs.ModeSymlink != 0:
		label := w.f.label(path)
		w.f.keep(label, "unreadable git file: "+label+" is a symlink")
		return false
	case !info.IsDir():
		label := w.f.label(path)
		w.f.keep(label, "unreadable git file: "+label+" is not a directory")
		return false
	}
	return true
}

// readGitFile reads a .git file through the shared reader: one line,
// "gitdir: <path>". The directory it names, and the common directory a
// worktree's commondir names, must be in the instance or the job
// directory, with no link on the way; one outside is never followed.
func (w *walker) readGitFile(path string) {
	label := w.f.label(path)
	b, err := readCapped(path, maxGitFileBytes)
	if err != nil {
		w.f.keep(label, "unreadable git file: "+label+": "+err.Error())
		return
	}
	line, ok := oneLine(b)
	target, ok2 := strings.CutPrefix(line, "gitdir: ")
	if !ok || !ok2 || target == "" {
		w.f.keep(label, "unreadable git file: "+label)
		return
	}
	dir := filepath.Dir(path)
	target = resolveFrom(dir, target)
	if !w.checkInside(label, target) {
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() {
		w.f.keep(label, "unreadable git file: "+label+" names a git directory that isn't there")
		return
	}
	s := sighting{label: label, workTree: dir, target: target, common: target}
	commondir := filepath.Join(target, "commondir")
	if _, err := os.Lstat(commondir); err == nil {
		cb, err := readCapped(commondir, maxGitFileBytes)
		cline, ok := oneLine(cb)
		if err != nil || !ok || cline == "" {
			cl := w.f.label(commondir)
			w.f.keep(cl, "unreadable git file: "+cl)
			return
		}
		common := resolveFrom(target, cline)
		if !w.checkInside(label, common) {
			return
		}
		s.common, s.linked = common, true
	}
	w.sightings = append(w.sightings, s)
}

// checkInside keeps the unit on an external git directory: target outside
// the instance and the job directory, or reached through a link.
func (w *walker) checkInside(label, target string) bool {
	base, ok := w.f.rootOf(target)
	if !ok {
		w.f.keep(label, "external git directory: "+target)
		return false
	}
	if err := noLinkBelow(base, target); err != nil {
		w.f.keep(label, "external git directory: "+target+" ("+err.Error()+")")
		return false
	}
	return true
}

// oneLine returns the single line b holds, with at most one trailing
// newline and no other line break.
func oneLine(b []byte) (string, bool) {
	s := strings.TrimSuffix(string(b), "\n")
	if strings.ContainsAny(s, "\n\r\x00") {
		return "", false
	}
	return s, true
}

// resolveFrom resolves p against dir as text, without following a link.
func resolveFrom(dir, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}

// resolve matches every .git file the walk read with the git directory it
// names. A git directory a .git file names is judged with that work tree;
// a linked worktree's admin directory is judged with its work tree and
// the URL of the clone whose common directory it sits in. An admin
// directory no .git file reached is a worktree outside the instance,
// unless its working tree is gone, when it is judged on its own.
func (w *walker) resolve() {
	for _, s := range w.sightings {
		info, err := os.Lstat(s.target)
		if err != nil {
			w.f.keep(s.label, "unreadable git file: "+s.label+" names a git directory that isn't there")
			continue
		}
		wtInfo, err := os.Lstat(s.workTree)
		if err != nil {
			w.f.keep(s.label, "unreadable: "+w.f.label(s.workTree))
			continue
		}
		if s.linked {
			w.resolveLinked(s, info, wtInfo)
			continue
		}
		matched := false
		for i := range w.f.repos {
			r := &w.f.repos[i]
			if r.gitInfo == nil || !os.SameFile(r.gitInfo, info) {
				continue
			}
			matched = true
			if r.ViaFile || (r.WorkTree != "" && !r.Structural) {
				w.f.keep(s.label, "unreadable git file: "+s.label+" names a git directory another work tree uses")
				break
			}
			r.WorkTree, r.wtInfo, r.ViaFile = s.workTree, wtInfo, true
			break
		}
		if !matched {
			w.add(foundRepo{GitDir: s.target, WorkTree: s.workTree, Common: s.target, ViaFile: true, gitInfo: info, wtInfo: wtInfo})
		}
	}
	for i := range w.f.admins {
		a := &w.f.admins[i]
		if a.reached {
			continue
		}
		w.unreachedAdmin(a)
	}
	for i := range w.f.repos {
		r := &w.f.repos[i]
		named := r.GitDir
		if r.WorkTree != "" {
			named = r.WorkTree
			w.f.skip[r.WorkTree] = true
		}
		r.Rel = w.f.label(named)
		if base, ok := w.f.rootOf(named); ok {
			r.Base = base
		} else {
			r.Base = w.instance
		}
		w.f.skip[r.GitDir] = true
	}
}

// resolveLinked judges a linked worktree the walk reached: a .git file
// naming an admin directory under its common directory's worktrees/.
func (w *walker) resolveLinked(s sighting, info, wtInfo fs.FileInfo) {
	commonInfo, err := os.Lstat(s.common)
	if err != nil {
		w.f.keep(s.label, "unreadable git file: "+s.label+" names a common directory that isn't there")
		return
	}
	var admin *adminDir
	for i := range w.f.admins {
		a := &w.f.admins[i]
		if os.SameFile(a.info, info) {
			admin = a
			break
		}
	}
	if admin == nil || filepath.Base(filepath.Dir(s.target)) != "worktrees" {
		w.f.keep(s.label, "unreadable git file: "+s.label+" names "+w.f.label(s.target)+", which isn't a worktree of "+w.f.label(s.common))
		return
	}
	if ci, err := os.Lstat(admin.Common); err != nil || !os.SameFile(ci, commonInfo) {
		w.f.keep(s.label, "unreadable git file: "+s.label+" names "+w.f.label(s.target)+", which isn't a worktree of "+w.f.label(s.common))
		return
	}
	if admin.reached {
		w.f.keep(s.label, "unreadable git file: "+s.label+" names a worktree another work tree uses")
		return
	}
	admin.reached = true
	r := foundRepo{GitDir: s.target, WorkTree: s.workTree, Common: admin.Common, Linked: true, ViaFile: true, gitInfo: info, wtInfo: wtInfo}
	r.URL, r.Repo = w.cloneOf(commonInfo)
	w.f.repos = append(w.f.repos, r)
}

// cloneOf returns the clone URL and repository of the workspace clone whose
// git directory is common, empty when common is no workspace clone's.
func (w *walker) cloneOf(common fs.FileInfo) (string, string) {
	for _, c := range w.f.repos {
		if c.URL != "" && !c.Linked && c.gitInfo != nil && os.SameFile(c.gitInfo, common) {
			return c.URL, c.Repo
		}
	}
	return "", ""
}

// unreachedAdmin keeps the unit on an admin directory no .git file in the
// instance or job directory reached, naming the working tree its gitdir
// file names, or judges it on its own when that working tree is gone.
func (w *walker) unreachedAdmin(a *adminDir) {
	label := w.f.label(a.Path)
	gitdirFile := filepath.Join(a.Path, "gitdir")
	b, err := readCapped(gitdirFile, maxGitFileBytes)
	line, ok := oneLine(b)
	if err != nil || !ok || line == "" {
		gl := w.f.label(gitdirFile)
		w.f.keep(gl, "unreadable git file: "+gl)
		return
	}
	dotGit := resolveFrom(a.Path, line)
	workTree := filepath.Dir(dotGit)
	if _, err := os.Lstat(dotGit); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(workTree); errors.Is(err, fs.ErrNotExist) {
			if !w.adminCommonInside(a) {
				return
			}
			r := foundRepo{
				GitDir: a.Path, Common: a.Common, Linked: true,
				Prunable: true, PrunedAt: workTree, gitInfo: a.info,
			}
			if ci, err := os.Lstat(a.Common); err == nil {
				r.URL, r.Repo = w.cloneOf(ci)
			}
			w.f.repos = append(w.f.repos, r)
			return
		}
	}
	w.f.keep(label, "worktree outside the instance: "+workTree)
}

// adminCommonInside checks an admin directory's own commondir file before
// git is run with that admin directory as --git-dir, since git reads the
// shared refs from wherever commondir names. The file is read through the
// shared reader and must be one line naming, in the instance or the job
// directory and with no link on the way, the common directory the walk
// found the admin directory under. Anything else keeps the unit.
func (w *walker) adminCommonInside(a *adminDir) bool {
	file := filepath.Join(a.Path, "commondir")
	label := w.f.label(file)
	b, err := readCapped(file, maxGitFileBytes)
	line, ok := oneLine(b)
	if err != nil || !ok || line == "" {
		w.f.keep(label, "unreadable git file: "+label)
		return false
	}
	common := resolveFrom(a.Path, line)
	if !w.checkInside(label, common) {
		return false
	}
	ci, err := os.Lstat(common)
	ai, aerr := os.Lstat(a.Common)
	if err != nil || aerr != nil || !os.SameFile(ci, ai) {
		w.f.keep(label, "unreadable git file: "+label+" names "+w.f.label(common)+", not "+w.f.label(a.Common))
		return false
	}
	return true
}

// reach checks a repository's worktree listing against the walk: every
// entry git lists must be one the walk reached in the instance or the job
// directory. The main entry is reached only at the work tree the walk
// found for that git directory, or, for a bare repository or a store with
// no work tree, with none; any other entry only where the walk found a
// .git file naming its admin directory under this common directory, or as
// a prunable admin directory the walk judged.
func (f *found) reach(r foundRepo, entries []worktreeEntry) []Check {
	var keeps []Check
	add := func(path, evidence string) {
		keeps = append(keeps, Check{Paths: []string{path}, Evidence: evidence})
	}
	gitInfo := r.gitInfo
	for _, e := range entries {
		if e.Main {
			switch {
			case e.Bare:
				if r.WorkTree != "" {
					add(r.Rel, r.Rel+": the repository is bare but the walk found a work tree")
				}
			case r.WorkTree != "" && f.samePath(e.Path, r.WorkTree):
			case !f.inside(e.Path):
				add(r.Rel, r.Rel+": work tree outside the instance: "+e.Path)
			case r.WorkTree != "":
				add(r.Rel, r.Rel+": configured work tree is not the one the walk found: "+f.label(e.Path))
			}
			continue
		}
		reached := false
		for _, o := range f.repos {
			if !o.Linked {
				continue
			}
			ci, err := os.Lstat(o.Common)
			if err != nil || gitInfo == nil || !os.SameFile(ci, gitInfo) {
				continue
			}
			if (o.Prunable && f.samePath(e.Path, o.PrunedAt)) || (!o.Prunable && o.WorkTree != "" && f.samePath(e.Path, o.WorkTree)) {
				reached = true
				break
			}
		}
		if !reached {
			add(e.Path, "worktree outside the instance: "+e.Path)
		}
	}
	return keeps
}
