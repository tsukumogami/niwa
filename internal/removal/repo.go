package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// checkRepositories is the repository check: every clone in the instance is
// judged, in path order, and every .git entry it doesn't judge keeps the
// unit. The walk it makes is kept for the outside check.
func checkRepositories(ctx context.Context, u Unit, p Policy, s *evalState) []Check {
	f := walkInstance(ctx, u.InstancePath, p.CloneURL)
	s.walk = &f
	keeps := append([]Check(nil), f.errs...)
	for _, rel := range f.unjudged {
		keeps = append(keeps, Check{Paths: []string{rel}, Evidence: fmt.Sprintf("a git entry the rule does not judge yet: %s", rel)})
		s.classes.add(filepath.ToSlash(rel), ClassUnique, "")
	}
	sort.Slice(f.clones, func(i, j int) bool { return f.clones[i].Rel < f.clones[j].Rel })
	gen := s.generated(u.InstancePath)
	for _, c := range f.clones {
		if p.ConfigError != nil {
			keeps = append(keeps, Check{Paths: []string{c.Rel}, Evidence: fmt.Sprintf("%s: the workspace configuration could not be read: %v", c.Rel, p.ConfigError)})
			s.classes.add(dirPath(filepath.ToSlash(c.Rel)), ClassUnique, "")
			continue
		}
		var trees []string
		if p.BuildTrees != nil {
			trees = p.BuildTrees(c.Repo)
		}
		res := p.Env.checkRepo(ctx, repoTarget{
			Rel:        c.Rel,
			Base:       u.InstancePath,
			Dirs:       repoDirs{GitDir: c.GitDir, WorkTree: c.WorkTree},
			URL:        c.URL,
			BuildTrees: trees,
			wtInfo:     c.wtInfo,
			gitInfo:    c.gitInfo,
		}, repoPolicy{
			ignoredUnique: p.IgnoredUnique,
			disposable:    append(append([]string(nil), ShippedDisposable...), p.Disposable...),
			generated:     gen,
		})
		keeps = append(keeps, res.keeps...)
		s.classes.paths = append(s.classes.paths, res.classes...)
	}
	return keeps
}

// repoTarget is one repository the rule judges, wherever the walk found it.
type repoTarget struct {
	// Rel is the repository's path relative to Base, the instance.
	Rel  string
	Base string
	// Dirs is where its git calls run. An empty WorkTree judges a
	// repository with no work tree, with --git-dir alone.
	Dirs repoDirs
	// URL is the clone URL its refs are compared with; empty when the
	// workspace gives it none, which keeps any ref it holds.
	URL string
	// BuildTrees are the build trees the workspace declares for it.
	BuildTrees []string
	// wtInfo and gitInfo are the walk's view of its directories; nil skips
	// the identity check for that directory.
	wtInfo, gitInfo fs.FileInfo
}

// repoPolicy is what the classes of a repository's paths depend on.
type repoPolicy struct {
	ignoredUnique bool
	disposable    []string
	// generated reports whether a path, relative to the instance, is a
	// file niwa wrote and still holds what niwa wrote.
	generated func(rel string) bool
}

// repoResult is one repository's keeping results, in the order the rule
// names them, and the class of every path in it.
type repoResult struct {
	keeps   []Check
	classes []PathClass
}

// checkRepo runs the per-repository check sequence. The identity check and
// the configuration read come first, and either one failing stops
// everything else there. Then every read runs, and the keeping results come
// out in the rule's order: git errors and an unreachable clone URL, then
// commits and refs the clone URL doesn't hold, then stashes, then modified
// tracked files, then index flags, then untracked files.
func (e *Env) checkRepo(ctx context.Context, t repoTarget, rp repoPolicy) repoResult {
	rel := filepath.ToSlash(t.Rel)
	var res repoResult
	keepAt := func(bucket *[]Check, evidence string, paths ...string) {
		if len(paths) == 0 {
			paths = []string{t.Rel}
		}
		*bucket = append(*bucket, Check{Paths: paths, Evidence: t.Rel + ": " + evidence})
	}
	stop := func(evidence string) repoResult {
		keepAt(&res.keeps, evidence)
		res.classes = append(res.classes, PathClass{Path: dirPath(rel), Class: ClassUnique})
		return res
	}
	if e == nil {
		return stop("git can't be run for this pass")
	}
	if err := t.checkIdentity(); err != nil {
		return stop("changed since the walk: " + err.Error())
	}
	rc, err := e.readConfig(ctx, t.Dirs)
	if err != nil {
		return stop(err.Error())
	}
	if rc.programKey != "" {
		return stop("repository configuration names a program: " + rc.programKey)
	}
	opts := callOpts{excludesUnset: rc.excludesUnset}

	var errs, refKeeps, stashKeeps, modKeeps, flagKeeps, untrackedKeeps []Check
	gitDirClass := ClassPushed

	stashes, stashErr := listStashes(ctx, e, t.Dirs, opts)
	if stashErr != nil {
		keepAt(&errs, stashErr.Error())
	} else if len(stashes) > 0 {
		keepAt(&stashKeeps, "stashes: "+nameSome(stashes))
		gitDirClass = ClassUnique
	}

	refs, warn, err := listRefs(ctx, e, t.Dirs, opts)
	switch {
	case err != nil:
		keepAt(&errs, err.Error())
		gitDirClass = ClassUnique
	default:
		if warn != "" {
			keepAt(&errs, "the ref listing reported a problem: "+warn)
			gitDirClass = ClassUnique
		}
		var unheld []string
		if len(refs) > 0 {
			tips, err := e.liveTips(ctx, t.URL)
			if t.URL == "" {
				err = errors.New("no clone URL for this repository")
			}
			if err != nil {
				keepAt(&errs, "clone URL unreachable: "+err.Error())
				gitDirClass = ClassUnique
			} else {
				for _, rf := range refs {
					if rf.Name == "refs/stash" && stashErr == nil && len(stashes) > 0 {
						// Named as stashes above, from the stash list.
						continue
					}
					if !tips[rf.OID] {
						unheld = append(unheld, refLabel(rf.Name))
					}
				}
			}
		}
		if len(unheld) > 0 {
			keepAt(&refKeeps, "commits the clone URL does not show: "+nameSome(unheld))
			gitDirClass = ClassUnique
		}
	}
	gitRel := ".git/"
	if rel != "." {
		gitRel = rel + "/.git/"
	}
	if t.Dirs.WorkTree == "" {
		gitRel = dirPath(rel)
	}
	res.classes = append(res.classes, PathClass{Path: gitRel, Class: gitDirClass})

	if t.Dirs.WorkTree != "" {
		c := t.workTreeChecks(ctx, e, opts, rc, rp)
		errs = append(errs, c.errs...)
		modKeeps, flagKeeps, untrackedKeeps = c.modified, c.flags, c.untracked
		res.classes = append(res.classes, c.classes...)
	}

	for _, bucket := range [][]Check{errs, refKeeps, stashKeeps, modKeeps, flagKeeps, untrackedKeeps} {
		res.keeps = append(res.keeps, bucket...)
	}
	return res
}

// checkIdentity checks, right before git first runs in the repository,
// that its directories are still the ones the walk recorded: read without
// following a link, still directories, the same device and inode, and no
// symlink on the way down from the instance.
func (t repoTarget) checkIdentity() error {
	for _, d := range []struct {
		path     string
		recorded fs.FileInfo
	}{{t.Dirs.WorkTree, t.wtInfo}, {t.Dirs.GitDir, t.gitInfo}} {
		if d.path == "" || d.recorded == nil {
			continue
		}
		if err := noLinkBelow(t.Base, d.path); err != nil {
			return err
		}
		info, err := os.Lstat(d.path)
		if err != nil {
			return fmt.Errorf("%s: %s", t.relOf(d.path), errText(err))
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is now a symlink", t.relOf(d.path))
		case !info.IsDir():
			return fmt.Errorf("%s is no longer a directory", t.relOf(d.path))
		case !os.SameFile(info, d.recorded):
			return fmt.Errorf("%s is not the directory the walk found", t.relOf(d.path))
		}
	}
	return nil
}

// noLinkBelow checks that no component of p below base is a symlink.
func noLinkBelow(base, p string) error {
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return fmt.Errorf("%s: %s", relOrSelf(base, cur), errText(err))
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is now a symlink", relOrSelf(base, cur))
		}
	}
	return nil
}

func (t repoTarget) relOf(p string) string {
	if t.Base == "" {
		return p
	}
	return relOrSelf(t.Base, p)
}

// workTreeResult is what the reads that need a work tree found.
type workTreeResult struct {
	errs, modified, flags, untracked []Check
	classes                          []PathClass
}

// workTreeChecks runs the status and index reads of a repository with a
// work tree and classes every path they report.
func (t repoTarget) workTreeChecks(ctx context.Context, e *Env, opts callOpts, rc repoConfig, rp repoPolicy) workTreeResult {
	var w workTreeResult
	rel := filepath.ToSlash(t.Rel)
	inst := func(p string) string {
		if rel == "." || rel == "" {
			return p
		}
		return rel + "/" + p
	}
	keepAt := func(bucket *[]Check, evidence string, paths []string) {
		if len(paths) == 0 {
			paths = []string{t.Rel}
		}
		*bucket = append(*bucket, Check{Paths: paths, Evidence: t.Rel + ": " + evidence})
	}

	entries, warn, statusErr := repoStatus(ctx, e, t.Dirs, opts)
	if statusErr != nil {
		keepAt(&w.errs, statusErr.Error(), nil)
	} else if warn != "" {
		keepAt(&w.errs, "the status read reported a problem: "+warn, nil)
	}
	index, indexErr := listIndex(ctx, e, t.Dirs, opts)
	if indexErr != nil {
		keepAt(&w.errs, indexErr.Error(), nil)
	}

	modified := map[string]bool{}
	var modShown, modPaths, untrackedShown, untrackedPaths, ignoredShown, ignoredPaths []string
	for _, en := range entries {
		p := strings.TrimSuffix(en.Path, "/")
		isDir := strings.HasSuffix(en.Path, "/")
		switch en.Kind {
		case "changed", "renamed", "unmerged":
			modified[p] = true
			modShown = append(modShown, p)
			modPaths = append(modPaths, inst(p))
			w.classes = append(w.classes, PathClass{Path: inst(p), Class: ClassUnique})
		case "untracked", "ignored":
			ignored := en.Kind == "ignored"
			class, notice, at := t.classifyLoose(p, isDir, ignored, rp)
			w.classes = append(w.classes, PathClass{Path: inst(at), Class: class, Notice: notice})
			if class != ClassUnique {
				continue
			}
			if ignored {
				ignoredShown = append(ignoredShown, en.Path)
				ignoredPaths = append(ignoredPaths, inst(p))
			} else {
				untrackedShown = append(untrackedShown, en.Path)
				untrackedPaths = append(untrackedPaths, inst(p))
			}
		}
	}
	if len(modShown) > 0 {
		keepAt(&w.modified, "modified tracked files: "+nameSome(modShown), modPaths)
	}

	var flagged, flagPaths []string
	for _, ie := range index {
		switch {
		case ie.Tag == 'S':
			flagged = append(flagged, "skip-worktree "+ie.Path)
			flagPaths = append(flagPaths, inst(ie.Path))
		case ie.Tag >= 'a' && ie.Tag <= 'z':
			flagged = append(flagged, "assume-unchanged "+ie.Path)
			flagPaths = append(flagPaths, inst(ie.Path))
		}
		if !modified[ie.Path] {
			class := ClassTracked
			if ie.Tag == 'S' || (ie.Tag >= 'a' && ie.Tag <= 'z') {
				class = ClassUnique
			}
			w.classes = append(w.classes, PathClass{Path: inst(ie.Path), Class: class})
		}
	}
	if len(flagged) > 0 {
		keepAt(&w.flags, "index flag hides changes: "+nameSome(flagged), flagPaths)
	}
	if rc.sparse || sparseFileExists(t.Dirs.GitDir) {
		keepAt(&w.flags, "sparse checkout", nil)
	}
	if len(untrackedShown) > 0 {
		keepAt(&w.untracked, "untracked files: "+nameSome(untrackedShown), untrackedPaths)
	}
	if len(ignoredShown) > 0 {
		keepAt(&w.untracked, "git-ignored files counted as unique: "+nameSome(ignoredShown), ignoredPaths)
	}
	return w
}

// classifyLoose classes an untracked or git-ignored path in a work tree,
// relative to the repository root, and returns the path its class is given
// at: a declared build tree's own path when the path sits inside one.
func (t repoTarget) classifyLoose(p string, isDir, ignored bool, rp repoPolicy) (Class, Notice, string) {
	if tree, ok := underDeclared(p, isDir, t.BuildTrees); ok {
		return ClassBuildTree, "", dirPath(tree)
	}
	at := p
	if isDir {
		at = dirPath(p)
	}
	if ignored && isDir && t.Dirs.WorkTree != "" && hasCacheDirTag(filepath.Join(t.Dirs.WorkTree, filepath.FromSlash(p))) {
		return ClassBuildTree, "", at
	}
	if matchAny(rp.disposable, p, isDir) {
		return ClassDisposable, "", at
	}
	if rp.generated != nil && !isDir {
		instRel := p
		if r := filepath.ToSlash(t.Rel); r != "." && r != "" {
			instRel = r + "/" + p
		}
		if rp.generated(instRel) {
			return ClassGenerated, "", at
		}
	}
	if ignored && !rp.ignoredUnique {
		return ClassDisposable, NoticeGitIgnored, at
	}
	return ClassUnique, "", at
}

// IsBuildTree reports whether the directory rel, relative to the work tree
// at workTree, is a build tree: one the workspace declares, or a git-ignored
// directory carrying a CACHEDIR.TAG with the standard signature. ignored
// says whether git ignores it.
func IsBuildTree(workTree, rel string, ignored bool, declared []string) bool {
	if _, ok := underDeclared(rel, true, declared); ok {
		return true
	}
	return ignored && hasCacheDirTag(filepath.Join(workTree, filepath.FromSlash(rel)))
}

// underDeclared reports whether p is a declared build tree or sits inside
// one, and returns that build tree.
func underDeclared(p string, isDir bool, declared []string) (string, bool) {
	p = strings.Trim(p, "/")
	for _, d := range declared {
		tree := strings.Trim(d, "/")
		if tree == "" {
			continue
		}
		if (p == tree && isDir) || strings.HasPrefix(p, tree+"/") {
			return tree, true
		}
	}
	return "", false
}

// cacheDirSignature is the line the Cache Directory Tagging convention puts
// at the start of a CACHEDIR.TAG.
const cacheDirSignature = "Signature: 8a477f597d28d172789f06886806bc55"

// hasCacheDirTag reports whether dir, a real directory and not a link,
// holds a regular CACHEDIR.TAG that starts with the standard signature.
func hasCacheDirTag(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	b, err := readRegular(filepath.Join(dir, "CACHEDIR.TAG"), 4096)
	return err == nil && bytes.HasPrefix(b, []byte(cacheDirSignature))
}

// sparseFileExists reports whether a git directory carries a sparse-checkout
// file.
func sparseFileExists(gitDir string) bool {
	if gitDir == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(gitDir, "info", "sparse-checkout"))
	return err == nil
}

// listRefs runs for-each-ref with NUL separators. Anything git writes to
// standard error on a zero exit, such as a broken ref or one with an invalid
// name it skips, comes back as warn, which keeps.
func listRefs(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]ref, string, error) {
	cmd, err := e.gitCommand(ctx, r, o, "for-each-ref", "--format=%(objectname)%00%(refname)%00")
	if err != nil {
		return nil, "", err
	}
	out, warn, err := runWarn(cmd)
	if err != nil {
		return nil, "", fmt.Errorf("listing refs: %v", err)
	}
	refs, err := parseRefs(out)
	return refs, warn, err
}

// listStashes runs stash list and returns each entry's name.
func listStashes(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]string, error) {
	cmd, err := e.gitCommand(ctx, r, o, "stash", "list", "-z", "--format=%gd")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %v", err)
	}
	return parseStashes(out)
}

// parseStashes reads stash list -z --format=%gd output, one stash@{<n>}
// per NUL-terminated record.
func parseStashes(out []byte) ([]string, error) {
	var names []string
	for _, rec := range strings.Split(string(out), "\x00") {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		if !strings.HasPrefix(rec, "stash@{") || !strings.HasSuffix(rec, "}") || strings.ContainsAny(rec, " \n\t") {
			return nil, fmt.Errorf("stash listing has a record that does not parse: %q", rec)
		}
		names = append(names, rec)
	}
	return names, nil
}

// RepoStatus is the status read of a repository with a work tree: every
// modified, untracked and git-ignored entry, in its NUL-terminated form. The
// re-read and the tombstone check repeat it and compare.
func (e *Env) RepoStatus(ctx context.Context, gitDir, workTree string, excludesUnset bool) ([]statusEntry, string, error) {
	return repoStatus(ctx, e, repoDirs{GitDir: gitDir, WorkTree: workTree}, callOpts{excludesUnset: excludesUnset})
}

// repoStatus runs git status in its NUL-terminated form, git-ignored paths
// included, a directory an ignore pattern matches reported once. Anything git
// writes to standard error on a zero exit comes back as warn.
func repoStatus(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]statusEntry, string, error) {
	cmd, err := e.gitCommand(ctx, r, o, "status", "--porcelain=v2", "-z", "--ignored=matching")
	if err != nil {
		return nil, "", err
	}
	out, warn, err := runWarn(cmd)
	if err != nil {
		return nil, "", fmt.Errorf("reading status: %v", err)
	}
	entries, err := parseStatus(out)
	return entries, warn, err
}

// indexEntry is one git ls-files -v entry: its tag letter and path.
type indexEntry struct {
	Tag  byte
	Path string
}

// IndexFlags is the index read of a repository with a work tree: every
// tracked path with its ls-files -v tag. The re-read and the tombstone check
// repeat it and compare.
func (e *Env) IndexFlags(ctx context.Context, gitDir, workTree string) ([]indexEntry, error) {
	return listIndex(ctx, e, repoDirs{GitDir: gitDir, WorkTree: workTree}, callOpts{})
}

// listIndex runs ls-files -v -z.
func listIndex(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]indexEntry, error) {
	cmd, err := e.gitCommand(ctx, r, o, "ls-files", "-v", "-z")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading the index: %v", err)
	}
	return parseLsFiles(out)
}

// lsFilesTags are the tag letters git ls-files -v prints, in upper case;
// a lowercase one marks an assume-unchanged entry.
const lsFilesTags = "HSMRCKU?"

// parseLsFiles reads git ls-files -v -z output: "<tag> <path>" per
// NUL-terminated record. A record that doesn't parse is an error, which
// keeps.
func parseLsFiles(out []byte) ([]indexEntry, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("index listing is not NUL-terminated")
	}
	var entries []indexEntry
	for _, rec := range strings.Split(string(out[:len(out)-1]), "\x00") {
		if len(rec) < 3 || rec[1] != ' ' {
			return nil, fmt.Errorf("index listing has a record that does not parse: %q", rec)
		}
		tag := rec[0]
		upper := tag
		if tag >= 'a' && tag <= 'z' {
			upper = tag - 'a' + 'A'
		}
		if !strings.ContainsRune(lsFilesTags, rune(upper)) {
			return nil, fmt.Errorf("index listing has an unknown tag: %q", rec)
		}
		entries = append(entries, indexEntry{Tag: tag, Path: rec[2:]})
	}
	return entries, nil
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
