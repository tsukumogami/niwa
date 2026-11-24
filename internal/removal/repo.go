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

	"github.com/tsukumogami/niwa/internal/testhook"
)

// maxNamed bounds how many paths or refs one check names in its evidence;
// the rest are counted.
const maxNamed = 5

// checkRepositories is the repository check. The walk finds every
// repository in the instance and the job directory, and each one is judged
// on its own, in path order. What the walk's own rules keep on (another
// filesystem, an external git directory, an unreadable git file, a
// worktree outside the instance) keeps the unit here too. The walk is kept
// for the outside check.
func checkRepositories(ctx context.Context, u Unit, p Policy, s *evalState) []Check {
	f := walkUnit(ctx, u, p)
	s.walk = &f
	keeps := append([]Check(nil), f.errs...)
	for _, k := range f.keeps {
		keeps = append(keeps, k)
		for _, path := range k.Paths {
			s.classes.add(path, ClassUnique, "")
		}
	}
	sort.SliceStable(f.repos, func(i, j int) bool { return f.repos[i].Rel < f.repos[j].Rel })
	gen := s.generated(u.InstancePath)
	proof := proofPolicy{cloneURLs: p.CloneURLs, cloneURLOf: p.CloneURL, prefixes: p.ProofRemotes}
	if p.Env != nil && p.Env.credErr != nil {
		// Without the person's credential helpers no network call to a
		// proof remote can be made as they would make it, so every unit
		// keeps, with the reason named.
		keeps = append(keeps, Check{Evidence: "the credential-helper read failed: " + p.Env.credErr.Error()})
	}
	seen := map[string]bool{}
	for _, k := range keeps {
		seen[k.Evidence] = true
	}
	rp := repoPolicy{
		ignoredUnique: p.IgnoredUnique,
		disposable:    append(append([]string(nil), ShippedDisposable...), p.Disposable...),
		generated:     gen,
		proof:         proof,
	}

	// The configuration read is the first git call in every repository, and
	// it runs on all of them before any other git call in any of them: a
	// repository whose configuration names a program keeps the unit before
	// anything else could start that program, wherever it sits in the
	// walk's order.
	type opened struct {
		r   foundRepo
		t   repoTarget
		rc  repoConfig
		res repoResult
		ok  bool
	}
	var repos []opened
	for _, r := range f.repos {
		if p.ConfigError != nil {
			keeps = append(keeps, Check{Paths: []string{r.Rel}, Evidence: fmt.Sprintf("%s: configuration unreadable: %v", r.Rel, p.ConfigError)})
			s.classes.add(dirPath(filepath.ToSlash(r.Rel)), ClassUnique, "")
			continue
		}
		var trees []string
		if p.BuildTrees != nil && r.Repo != "" && !r.Linked {
			trees = p.BuildTrees(r.Repo)
		}
		t := repoTarget{
			Rel:        r.Rel,
			Base:       r.Base,
			Dirs:       r.dirs(),
			URL:        r.URL,
			BuildTrees: trees,
			wtInfo:     r.wtInfo,
			gitInfo:    r.gitInfo,
			walk:       &f,
			repo:       r,
		}
		rc, res, ok := p.Env.openRepo(ctx, t)
		repos = append(repos, opened{r: r, t: t, rc: rc, res: res, ok: ok})
	}

	for _, o := range repos {
		r, res := o.r, o.res
		if o.ok {
			res = p.Env.readRepo(ctx, o.t, rp, o.rc)
		}
		repoKeeps := res.keeps
		read := RepoRead{
			Rel: r.Rel, dirs: r.dirs(), base: r.Base,
			inInstance: r.Base == f.roots[0].given || r.Base == f.roots[0].real,
			linked:     r.Linked, prunable: r.Prunable,
			wtInfo: r.wtInfo, gitInfo: r.gitInfo,
			read:   res.configRead && res.workTreeRead,
			status: res.status, index: res.index,
		}
		if res.configRead && r.Prunable {
			staged, keeps := p.Env.checkPrunedIndex(ctx, r)
			repoKeeps = append(repoKeeps, keeps...)
			read.staged = staged
			read.read = read.read && len(keeps) == 0
		}
		s.reads = append(s.reads, read)
		if res.listed && !r.Linked {
			repoKeeps = append(repoKeeps, f.reach(r, res.worktrees)...)
		}
		for _, k := range repoKeeps {
			if seen[k.Evidence] {
				continue
			}
			seen[k.Evidence] = true
			keeps = append(keeps, k)
		}
		s.classes.paths = append(s.classes.paths, res.classes...)
	}
	return keeps
}

// checkPrunedIndex reads the index of a worktree whose working tree is gone,
// through its admin directory: a staged entry that differs from its HEAD,
// or a read that fails, keeps the unit. It returns what it read with the
// keeps.
func (e *Env) checkPrunedIndex(ctx context.Context, r foundRepo) ([]string, []Check) {
	keep := func(evidence string) []Check {
		return []Check{{Paths: []string{r.Rel}, Evidence: r.Rel + ": " + evidence}}
	}
	staged, err := e.readPrunedIndex(ctx, r.GitDir)
	if err != nil {
		return nil, keep(err.Error())
	}
	if len(staged) > 0 {
		return staged, keep("staged changes in a worktree whose working tree is gone: " + nameSome(staged))
	}
	return nil, nil
}

// readPrunedIndex runs git diff-index --cached against HEAD in the admin
// directory gitDir of a worktree whose working tree is gone, and returns
// every staged path.
func (e *Env) readPrunedIndex(ctx context.Context, gitDir string) ([]string, error) {
	cmd, err := e.gitCommand(ctx, repoDirs{GitDir: gitDir}, callOpts{}, "diff-index", "--cached", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading the index of a worktree whose working tree is gone: %v", err)
	}
	var staged []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			staged = append(staged, name)
		}
	}
	return staged, nil
}

// repoTarget is one repository the rule judges, wherever the walk found it.
type repoTarget struct {
	// Rel names the repository in evidence: its path relative to the
	// instance, or "job:"-prefixed in the job directory.
	Rel string
	// Base is the root the repository was found under, the instance or the
	// job directory; the identity check looks for a link below it.
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
	// walk and repo are the walk that found the repository and what it
	// found, nil and zero for a repository judged apart from a walk.
	walk *found
	repo foundRepo
}

// display names a path in evidence: relative to the instance or
// "job:"-prefixed when the walk covers it, as given otherwise.
func (t repoTarget) display(p string) string {
	if t.walk == nil {
		return p
	}
	return t.walk.label(p)
}

// checkWorkTreeConfig checks the work tree the repository's own
// configuration names against the walk. A core.worktree outside the
// instance and the job directory keeps, and so does one inside that isn't
// the work tree the walk found; a store with no work tree may name one
// inside. A git directory found by its structure that isn't bare, isn't a
// submodule store, names no work tree and that no .git file names has its
// work tree somewhere the walk can't see (git init --separate-git-dir), and
// keeps too.
func (t repoTarget) checkWorkTreeConfig(rc repoConfig) string {
	if t.walk == nil {
		return ""
	}
	if rc.worktree != "" {
		wt := resolveFrom(t.Dirs.GitDir, rc.worktree)
		switch {
		case !t.walk.inside(wt):
			return "work tree outside the instance: " + wt
		case t.Dirs.WorkTree != "" && !t.walk.samePath(wt, t.Dirs.WorkTree):
			return "configured work tree is not the one the walk found: " + t.display(wt)
		}
		return ""
	}
	r := t.repo
	if r.Structural && !r.InModules && !r.ViaFile && !r.Linked && !rc.bare {
		return "work tree outside the instance: " + r.GitDir
	}
	return ""
}

// repoPolicy is what the classes of a repository's paths depend on.
type repoPolicy struct {
	ignoredUnique bool
	disposable    []string
	// generated reports whether a path, relative to the instance, is a
	// file niwa wrote and still holds what niwa wrote.
	generated func(rel string) bool
	// proof decides which of the repository's remotes are proof remotes.
	proof proofPolicy
}

// repoResult is one repository's keeping results, in the order the rule
// names them, and the class of every path in it.
type repoResult struct {
	keeps   []Check
	classes []PathClass
	// configRead is true once the configuration read passed: the identity
	// check held, no key names a program and the work tree it names is the
	// walk's.
	configRead bool
	// worktrees is git's worktree listing, and listed says it was read.
	worktrees []worktreeEntry
	listed    bool
	// status and index are what the status and index reads returned, and
	// workTreeRead says both ran without an error; a repository with no
	// work tree has neither, and workTreeRead true.
	status       []statusEntry
	index        []indexEntry
	workTreeRead bool
}

// stopRepo is a repository's whole result when a check before its reads
// keeps it: the one keep, and the repository classed unique.
func (t repoTarget) stopRepo(evidence string) repoResult {
	return repoResult{
		keeps:   []Check{{Paths: []string{t.Rel}, Evidence: t.Rel + ": " + evidence}},
		classes: []PathClass{{Path: dirPath(filepath.ToSlash(t.Rel)), Class: ClassUnique}},
	}
}

// openRepo runs the identity check and the configuration read, the first
// git call in the repository. It returns the configuration, or, with ok
// false, the repository's whole result when either keeps it: nothing more
// runs there. A key that names a program keeps, and so does a partial clone
// on a git older than 2.45, before any call that could read an object.
func (e *Env) openRepo(ctx context.Context, t repoTarget) (rc repoConfig, res repoResult, ok bool) {
	if e == nil {
		return rc, t.stopRepo("git can't be run for this pass"), false
	}
	if err := t.checkIdentity(); err != nil {
		return rc, t.stopRepo("changed since the walk: " + err.Error()), false
	}
	rc, err := e.readConfig(ctx, t.Dirs)
	if err != nil {
		return rc, t.stopRepo(err.Error()), false
	}
	if _, why := e.refusal(rc); why != "" {
		return rc, t.stopRepo(why), false
	}
	if why := t.checkWorkTreeConfig(rc); why != "" {
		return rc, t.stopRepo(why), false
	}
	return rc, repoResult{configRead: true}, true
}

// checkRepo runs the per-repository check sequence on one repository:
// openRepo, then readRepo.
func (e *Env) checkRepo(ctx context.Context, t repoTarget, rp repoPolicy) repoResult {
	rc, res, ok := e.openRepo(ctx, t)
	if !ok {
		return res
	}
	return e.readRepo(ctx, t, rp, rc)
}

// readRepo runs every read in a repository whose configuration read passed,
// and the keeping results come out in the rule's order: git errors and an
// unreachable proof remote, then remotes that aren't proof remotes and the
// commits and tags no proof remote holds (checkHeld), then stashes, then
// modified tracked files, then index flags, then untracked files. The
// identity check runs again first, since other repositories' configuration
// reads can come between this one's and its reads, and on a git older than
// 2.45 so does the partial-clone refusal, before any call that reads an
// object.
func (e *Env) readRepo(ctx context.Context, t repoTarget, rp repoPolicy, rc repoConfig) repoResult {
	rel := filepath.ToSlash(t.Rel)
	if err := t.checkIdentity(); err != nil {
		return t.stopRepo("changed since the walk: " + err.Error())
	}
	if !e.gitVersion.atLeast(lazyFetchOffVersion.major, lazyFetchOffVersion.minor) {
		again, err := e.readConfig(ctx, t.Dirs)
		if err != nil {
			return t.stopRepo(err.Error())
		}
		if again.partialKey != "" {
			return t.stopRepo("partial clone needs git 2.45: " + again.partialKey)
		}
	}
	res := repoResult{configRead: true}
	keepAt := func(bucket *[]Check, evidence string, paths ...string) {
		if len(paths) == 0 {
			paths = []string{t.Rel}
		}
		*bucket = append(*bucket, Check{Paths: paths, Evidence: t.Rel + ": " + evidence})
	}
	opts := callOpts{excludesUnset: rc.excludesUnset}

	var errs, refKeeps, stashKeeps, modKeeps, flagKeeps, untrackedKeeps []Check
	gitDirClass := ClassPushed

	// git stash list refuses to run without a work tree, so a repository
	// with none (a bare one, a submodule store) has its stash reflog read
	// directly: every entry keeps, not only the newest, which is all a
	// judgment of refs/stash as a ref would see.
	var stashes []string
	var stashErr error
	if t.Dirs.WorkTree != "" {
		stashes, stashErr = listStashes(ctx, e, t.Dirs, opts)
	} else {
		stashes, stashErr = listStashReflog(ctx, e, t.Dirs, opts)
	}
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
		// Every worktree's HEAD counts like a ref, a detached one included,
		// so a commit that only HEAD reaches is judged too.
		entries, headErr := listWorktrees(ctx, e, t.Dirs, opts)
		if headErr != nil {
			keepAt(&errs, headErr.Error())
			gitDirClass = ClassUnique
		} else {
			res.worktrees, res.listed = entries, true
		}
		for _, h := range headsOf(entries) {
			refs = append(refs, ref{OID: h.OID, Name: t.headLabel(h)})
		}
		var judged []ref
		for _, rf := range refs {
			if rf.Name == "refs/stash" && stashErr == nil && len(stashes) > 0 {
				// Named as stashes above, from the stash list.
				continue
			}
			judged = append(judged, rf)
		}
		ps := selectProofs(rc.remotes, t.URL, rp.proof, e.allowFile)
		held := e.checkHeld(ctx, t, opts, judged, ps)
		for _, ev := range held.errs {
			keepAt(&errs, ev)
		}
		for _, ev := range held.keeps {
			keepAt(&refKeeps, ev)
		}
		if len(held.errs) > 0 || len(held.keeps) > 0 {
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

	res.workTreeRead = true
	if t.Dirs.WorkTree != "" {
		c := t.workTreeChecks(ctx, e, opts, rc, rp)
		errs = append(errs, c.errs...)
		modKeeps, flagKeeps, untrackedKeeps = c.modified, c.flags, c.untracked
		res.classes = append(res.classes, c.classes...)
		res.status, res.index, res.workTreeRead = c.status, c.index, c.read
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
	// status and index are what the reads returned; read says both ran
	// without an error or a warning.
	status []statusEntry
	index  []indexEntry
	read   bool
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

	testhook.RemovalBeforeStatus(testhook.StageRule, t.Dirs.GitDir, t.Dirs.WorkTree)
	entries, warn, statusErr := repoStatus(ctx, e, t.Dirs, opts)
	var refused *configRefused
	if errors.As(statusErr, &refused) {
		// The re-check right before the status found a key that keeps: the
		// status wasn't started, and nothing more runs here.
		keepAt(&w.errs, statusErr.Error(), nil)
		w.classes = append(w.classes, PathClass{Path: dirPath(rel), Class: ClassUnique})
		return w
	}
	if statusErr != nil {
		keepAt(&w.errs, statusErr.Error(), nil)
	} else if warn != "" {
		keepAt(&w.errs, "the status read reported a problem: "+warn, nil)
	}
	index, indexErr := listIndex(ctx, e, t.Dirs, opts)
	if indexErr != nil {
		keepAt(&w.errs, indexErr.Error(), nil)
	}
	w.status, w.index = entries, index
	w.read = statusErr == nil && warn == "" && indexErr == nil

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
			if isDir && t.walk != nil && t.walk.skip[filepath.Join(t.Dirs.WorkTree, filepath.FromSlash(p))] {
				// A repository the walk found here, or a directory on
				// another filesystem, is judged on its own.
				continue
			}
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
// holds a regular CACHEDIR.TAG of the user's that starts with the standard
// signature. A tag the shared reader refuses (a link, a FIFO, another
// user's file, one over its cap) makes no build tree.
func hasCacheDirTag(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	b, err := readCapped(filepath.Join(dir, "CACHEDIR.TAG"), maxCacheTagBytes)
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

// worktreeHead is one worktree's HEAD from git worktree list.
type worktreeHead struct {
	Path string
	OID  string
	// Main is the first entry git lists, the repository's own work tree.
	Main bool
}

// label names a worktree's HEAD in evidence: "HEAD" for the repository's own
// work tree, "HEAD of worktree <path>" for any other.
func (h worktreeHead) label(workTree string) string {
	if h.Main || (workTree != "" && filepath.Clean(h.Path) == filepath.Clean(workTree)) {
		return "HEAD"
	}
	return "HEAD of worktree " + h.Path
}

// headLabel names a worktree's HEAD in evidence, with a worktree inside the
// instance or job directory named by its path there. The main entry is
// "HEAD" only for the repository whose own work tree it is: a linked
// worktree's listing names the main work tree's HEAD by its path.
func (t repoTarget) headLabel(h worktreeHead) string {
	if t.repo.Linked && h.Main {
		return "HEAD of worktree " + t.display(h.Path)
	}
	if h.Main || (t.Dirs.WorkTree != "" && filepath.Clean(h.Path) == filepath.Clean(t.Dirs.WorkTree)) {
		return "HEAD"
	}
	return "HEAD of worktree " + t.display(h.Path)
}

// worktreeEntry is one record of git worktree list: its path, its HEAD
// when it names a commit, and its flags.
type worktreeEntry struct {
	Path     string
	OID      string
	Main     bool
	Bare     bool
	Prunable bool
}

// headsOf returns every entry's HEAD that names a commit. A HEAD on an
// unborn branch names none.
func headsOf(entries []worktreeEntry) []worktreeHead {
	var heads []worktreeHead
	for _, e := range entries {
		if e.OID != "" && strings.Trim(e.OID, "0") != "" {
			heads = append(heads, worktreeHead{Path: e.Path, OID: e.OID, Main: e.Main})
		}
	}
	return heads
}

// listWorktrees runs worktree list --porcelain -z and returns every
// record. A listing that fails or doesn't parse is an error, which keeps.
func listWorktrees(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]worktreeEntry, error) {
	cmd, err := e.gitCommand(ctx, r, o, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %v", err)
	}
	return parseWorktreeEntries(out)
}

// parseWorktreeList reads git worktree list --porcelain -z output and
// returns every worktree's HEAD that names a commit.
func parseWorktreeList(out []byte) ([]worktreeHead, error) {
	entries, err := parseWorktreeEntries(out)
	if err != nil {
		return nil, err
	}
	return headsOf(entries), nil
}

// parseWorktreeEntries reads git worktree list --porcelain -z output: each
// worktree is a run of NUL-terminated "<label> [<value>]" fields starting
// with "worktree <path>", and an empty field ends it. Every record but a
// bare one must carry a HEAD line: a worktree whose HEAD git didn't list is
// one the rule can't judge, so the listing fails rather than drop it.
func parseWorktreeEntries(out []byte) ([]worktreeEntry, error) {
	if len(out) == 0 {
		return nil, errors.New("worktree listing is empty")
	}
	if !bytes.HasSuffix(out, []byte("\x00\x00")) {
		return nil, errors.New("worktree listing does not end a record")
	}
	var entries []worktreeEntry
	for i, block := range strings.Split(string(out[:len(out)-2]), "\x00\x00") {
		fields := strings.Split(block, "\x00")
		path, ok := strings.CutPrefix(fields[0], "worktree ")
		if !ok || path == "" {
			return nil, fmt.Errorf("worktree listing has a record that does not parse: %q", block)
		}
		en := worktreeEntry{Path: path, Main: i == 0}
		sawHead := false
		for _, f := range fields[1:] {
			label, value, _ := strings.Cut(f, " ")
			switch label {
			case "HEAD":
				if !isObjectID(value) {
					return nil, fmt.Errorf("worktree listing has a HEAD that does not parse: %q", f)
				}
				en.OID = value
				sawHead = true
			case "bare":
				en.Bare = true
			case "prunable":
				en.Prunable = true
			case "branch", "detached", "locked":
			default:
				return nil, fmt.Errorf("worktree listing has a field that does not parse: %q", f)
			}
		}
		if !sawHead && !en.Bare {
			return nil, fmt.Errorf("worktree listing has a record with no HEAD: %q", block)
		}
		entries = append(entries, en)
	}
	return entries, nil
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

// listStashReflog lists the stashes of a repository with no work tree from
// its refs/stash reflog, through git log -g, which runs without one. A
// refs/stash whose reflog lists nothing is named by the ref itself, and a
// stash reflog with no refs/stash is an error: either way the repository
// keeps.
func listStashReflog(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]string, error) {
	cmd, err := e.gitCommand(ctx, r, o, "for-each-ref", "--format=%(refname)", "refs/stash")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %v", err)
	}
	if strings.TrimSpace(string(out)) != "refs/stash" {
		if _, err := os.Lstat(filepath.Join(r.GitDir, "logs", "refs", "stash")); err == nil {
			return nil, errors.New("listing stashes: a stash reflog with no refs/stash")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("listing stashes: %v", err)
		}
		return nil, nil
	}
	// No "--" after the ref: gitCommand appends --no-show-signature to every
	// log call, which must stay an option.
	cmd, err = e.gitCommand(ctx, r, o, "log", "-g", "-z", "--format=%gd", "refs/stash")
	if err != nil {
		return nil, err
	}
	out, err = run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing stashes: %v", err)
	}
	names, err := parseStashes([]byte(strings.ReplaceAll(string(out), "refs/stash@{", "stash@{")))
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		names = []string{"refs/stash"}
	}
	return names, nil
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
// re-read and the tombstone check repeat it and compare. Like every status,
// it runs the configuration read again first (see repoStatus).
func (e *Env) RepoStatus(ctx context.Context, gitDir, workTree string, excludesUnset bool) ([]statusEntry, string, error) {
	return repoStatus(ctx, e, repoDirs{GitDir: gitDir, WorkTree: workTree}, callOpts{excludesUnset: excludesUnset})
}

// repoStatus runs git status in its NUL-terminated form, git-ignored paths
// included, a directory an ignore pattern matches reported once. Anything git
// writes to standard error on a zero exit comes back as warn.
//
// It is the only place a status on the removal path is started, and it runs
// the configuration read again right before it, under the same bound as the
// first: status is the call that can start a filter, and a process still
// alive in the instance can write a key in the seconds since the first read.
// A key that keeps returns a *configRefused, and the status isn't started.
// The re-check and the status are separate processes, so the configuration
// is read once more right after the status too, and a key that keeps there
// returns a *configRefused in place of what the status printed.
func repoStatus(ctx context.Context, e *Env, r repoDirs, o callOpts) ([]statusEntry, string, error) {
	if e == nil {
		return nil, "", errors.New("git can't be run for this pass")
	}
	rc, err := e.recheckConfig(ctx, r)
	if err != nil {
		return nil, "", err
	}
	// The re-check's read is the fresher one: an excludes file it no longer
	// sees adds the override, and one it sees never drops an override the
	// caller asked for.
	o.excludesUnset = o.excludesUnset || rc.excludesUnset
	cmd, err := e.gitCommand(ctx, r, o, "status", "--porcelain=v2", "-z", "--ignored=matching")
	if err != nil {
		return nil, "", err
	}
	out, warn, err := runWarn(cmd)
	if err != nil {
		return nil, "", fmt.Errorf("reading status: %v", err)
	}
	if _, err := e.recheckConfig(ctx, r); err != nil {
		return nil, "", err
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
