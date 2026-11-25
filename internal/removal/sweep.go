package removal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The hook worktree sweep's reads and removal. The sweep judges one linked
// worktree at a time, inside an instance that stays: the walk of the
// instance finds the worktree and every repository under it (its own, each
// submodule, gitlink and nested repository), the program-naming
// configuration check runs on all of them before any other git call in any
// of them, and the worktree's HEAD and per-worktree refs are recorded. The
// SweptWorktree that comes back is then both the git invoker and the
// remover worktree.DestroySession is called with: every git call it builds
// goes through gitCommand, every status runs after the configuration check
// again, and the removal re-reads HEAD and the per-worktree refs and runs the
// check once more before the no-follow walk removes the worktree. No call it
// builds removes or prunes a worktree.

// perWorktreeRefPrefixes are the ref namespaces git keeps per worktree.
var perWorktreeRefPrefixes = []string{"refs/worktree/", "refs/bisect/", "refs/rewritten/"}

// SweptWorktree is one worktree the sweep evaluated and found nothing to
// keep it for. It is a worktree.GitInvoker and a worktree.WorktreeRemover
// for that worktree alone.
type SweptWorktree struct {
	env      *Env
	instance string
	// workTree is the work tree as the sweep was given it, adminDir its admin
	// directory as the walk found it, and repoDir the repository's own work
	// tree, whose common git directory is common.
	workTree, adminDir string
	repoDir, common    string
	// repos is every repository the walk found under the worktree, its own
	// first; each had the configuration check.
	repos []repoDirs
	// excludesUnset is the worktree's repository configuration naming no
	// excludes file.
	excludesUnset bool
	// refs are HEAD, its symbolic name and every per-worktree ref, as the
	// evaluation read them.
	refs map[string]string
}

// EvaluateSweptWorktree walks the instance and judges the linked worktree
// at workTree. Before any other git call it runs the configuration check on
// every repository the walk finds under the worktree; then it records the
// worktree's HEAD and per-worktree refs. keep is empty and s set when nothing
// keeps the worktree. Otherwise keep says why, with the key or the walk's
// rule named: a key that names a program, a configuration that can't be
// read or doesn't match the walk, a walk rule's keep or an unreadable path
// under the worktree, a worktree the walk didn't find inside the instance,
// or a ref read that fails.
func (e *Env) EvaluateSweptWorktree(ctx context.Context, instance, workTree string) (s *SweptWorktree, keep string) {
	if e == nil {
		return nil, "git can't be run for this pass"
	}
	f := walkUnit(ctx, Unit{InstancePath: instance}, Policy{})
	var own *foundRepo
	for i := range f.repos {
		r := &f.repos[i]
		if r.Linked && !r.Prunable && r.WorkTree != "" && sameDir(r.WorkTree, workTree) {
			own = r
			break
		}
	}
	if own == nil {
		return nil, "the walk found no linked worktree at " + workTree + " inside the instance"
	}
	if !f.inside(own.GitDir) {
		return nil, "the worktree's admin directory is outside the instance: " + own.GitDir
	}
	wtRel, adminRel := f.label(own.WorkTree), f.label(own.GitDir)
	under := func(path string) bool {
		for _, base := range []string{wtRel, adminRel} {
			if path == base || strings.HasPrefix(path, base+"/") {
				return true
			}
		}
		return false
	}
	var keeps []string
	for _, c := range append(append([]Check(nil), f.errs...), f.keeps...) {
		mine := len(c.Paths) == 0
		for _, p := range c.Paths {
			if under(p) {
				mine = true
			}
		}
		if mine {
			keeps = append(keeps, c.Evidence)
		}
	}

	members := []foundRepo{*own}
	for _, r := range f.repos {
		if r.GitDir == own.GitDir && r.WorkTree == own.WorkTree {
			continue
		}
		inTree := r.WorkTree != "" && withinDir(r.WorkTree, own.WorkTree)
		inGit := withinDir(r.GitDir, own.WorkTree) || withinDir(r.GitDir, own.GitDir)
		if inTree || inGit {
			members = append(members, r)
		}
	}
	sort.SliceStable(members[1:], func(i, j int) bool { return members[1+i].Rel < members[1+j].Rel })

	// The configuration read is the first git call in every repository
	// under the worktree, and it runs on all of them before anything else.
	var ownConfig repoConfig
	for i, r := range members {
		t := repoTarget{Rel: r.Rel, Base: r.Base, Dirs: r.dirs(), wtInfo: r.wtInfo, gitInfo: r.gitInfo, walk: &f, repo: r}
		rc, res, ok := e.openRepo(ctx, t)
		if !ok {
			for _, k := range res.keeps {
				keeps = append(keeps, k.Evidence)
			}
			continue
		}
		if i == 0 {
			ownConfig = rc
		}
	}
	if len(keeps) > 0 {
		return nil, strings.Join(keeps, "; ")
	}

	s = &SweptWorktree{
		env:           e,
		instance:      instance,
		workTree:      filepath.Clean(workTree),
		adminDir:      own.GitDir,
		common:        own.Common,
		excludesUnset: ownConfig.excludesUnset,
	}
	if filepath.Base(own.Common) == ".git" {
		s.repoDir = filepath.Dir(own.Common)
	}
	for _, r := range members {
		s.repos = append(s.repos, r.dirs())
	}
	refs, err := s.readRefs(ctx)
	if err != nil {
		return nil, wtRel + ": " + err.Error()
	}
	s.refs = refs
	return s, ""
}

// sameDir reports whether a and b name the same directory, as text or with
// their links resolved.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || canonical(a) == canonical(b)
}

// withinDir reports whether p is dir or lies under it, as text or with
// their links resolved.
func withinDir(p, dir string) bool {
	return within(filepath.Clean(p), filepath.Clean(dir)) || within(canonical(p), canonical(dir))
}

// own is where the worktree's own git calls run.
func (s *SweptWorktree) own() repoDirs {
	return repoDirs{GitDir: s.adminDir, WorkTree: s.workTree}
}

// readRefs reads the worktree's HEAD, its symbolic name, and every
// per-worktree ref.
func (s *SweptWorktree) readRefs(ctx context.Context) (map[string]string, error) {
	r := s.own()
	opts := callOpts{excludesUnset: s.excludesUnset}
	out := map[string]string{}
	for _, q := range []struct{ name, arg string }{{"HEAD", "--verify"}, {"HEAD (its name)", "--symbolic-full-name"}} {
		cmd, err := s.env.gitCommand(ctx, r, opts, "rev-parse", q.arg, "HEAD")
		if err != nil {
			return nil, err
		}
		got, err := run(cmd)
		if err != nil {
			return nil, fmt.Errorf("reading HEAD: %v", err)
		}
		out[q.name] = strings.TrimSpace(string(got))
	}
	refs, warn, err := listRefs(ctx, s.env, r, opts)
	switch {
	case err != nil:
		return nil, err
	case warn != "":
		return nil, errors.New("the ref listing reported a problem: " + warn)
	}
	for _, rf := range refs {
		for _, p := range perWorktreeRefPrefixes {
			if strings.HasPrefix(rf.Name, p) {
				out[rf.Name] = rf.OID
			}
		}
	}
	return out, nil
}

// recheckAll runs the configuration check again on every repository under
// the worktree, the worktree's own first.
func (s *SweptWorktree) recheckAll(ctx context.Context) error {
	for _, r := range s.repos {
		if _, err := s.env.recheckConfig(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// refused is a command that never starts: running it returns err.
func refused(err error) *exec.Cmd {
	return &exec.Cmd{Err: err}
}

// CommandContext builds the git call args name, through gitCommand. It
// takes the calls worktree.DestroySession and worktree.WorktreeUnchanged
// make, each starting with -C and a directory: the worktree, which runs
// with its admin directory as the git directory, or the repository's own
// work tree. A status runs only after the configuration check on every
// repository under the worktree passes again, and gitCommand gives it
// --ignore-submodules=all and --untracked-files=all; a call in the
// repository's own work tree runs after the check on that repository. A
// check that keeps, a directory that is neither, or a call that removes or
// prunes a worktree comes back as a command that fails without starting,
// with the reason as its error.
func (s *SweptWorktree) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	if len(args) < 3 || args[0] != "-C" {
		return refused(errors.New("the sweep runs git only in the worktree or its repository"))
	}
	dir, rest := args[1], args[2:]
	if rest[0] == "worktree" {
		return refused(errors.New("the sweep removes a worktree with no git worktree call"))
	}
	var r repoDirs
	opts := callOpts{}
	switch {
	case sameDir(dir, s.workTree):
		r, opts.excludesUnset = s.own(), s.excludesUnset
		if rest[0] == "status" {
			if err := s.recheckAll(ctx); err != nil {
				return refused(err)
			}
		}
	case s.repoDir != "" && sameDir(dir, s.repoDir):
		r = repoDirs{GitDir: s.common, WorkTree: s.repoDir}
		rc, err := s.env.recheckConfig(ctx, r)
		if err != nil {
			return refused(err)
		}
		opts.excludesUnset = rc.excludesUnset
	default:
		return refused(fmt.Errorf("the sweep runs git only in the worktree or its repository, not %s", dir))
	}
	cmd, err := s.env.gitCommand(ctx, r, opts, rest...)
	if err != nil {
		return refused(err)
	}
	return cmd
}

// RemoveWorktree removes the worktree the sweep evaluated, and only that
// one. Right before the removal it reads the worktree's HEAD and
// per-worktree refs again, and a change keeps the worktree with "changed
// during the pass: ref <name>"; then the configuration check runs again on
// every repository under the worktree, as the last git read; then the
// no-follow walk (RemoveWorktree) removes the work tree and the admin
// directory, stopping with the path named on anything it won't remove. It
// makes no git call that removes or prunes a worktree.
func (s *SweptWorktree) RemoveWorktree(instance, workTree, adminDir string) error {
	if !sameDir(instance, s.instance) || !sameDir(workTree, s.workTree) || !sameDir(adminDir, s.adminDir) {
		return fmt.Errorf("the worktree to remove is not the one the sweep judged: %s", workTree)
	}
	ctx := context.Background()
	now, err := s.readRefs(ctx)
	if err != nil {
		return err
	}
	var names []string
	for name := range s.refs {
		names = append(names, name)
	}
	for name := range now {
		if _, ok := s.refs[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		was, hadIt := s.refs[name]
		is, hasIt := now[name]
		if hadIt != hasIt || was != is {
			return errors.New(changedDuringThePass + "ref " + name)
		}
	}
	if err := s.recheckAll(ctx); err != nil {
		return err
	}
	inst, wt, admin, err := instanceSpelling(instance, workTree, adminDir)
	if err != nil {
		return err
	}
	return RemoveWorktree(inst, wt, admin)
}

// instanceSpelling names the instance, the work tree and the admin
// directory in the one spelling the walk opens. The work tree and the admin
// directory can come named under different spellings of the instance, as
// given and with the links above it resolved: the hook records a worktree
// under a linked temporary directory resolved, and DestroySession names the
// admin directory from the instance as given. When the two spellings
// differ, the instance itself must be a real directory, not a link, and the
// same directory under both, so they differ only in the links above it;
// then each path is named under the resolved spelling by its place below
// whichever spelling it was given under. The paths' own links are never
// resolved, so a link swapped in under them can't move the removal, and
// the walk still opens every directory without following a link. A path
// under neither spelling is passed on as it came, for the walk to refuse.
func instanceSpelling(instance, workTree, adminDir string) (inst, wt, admin string, err error) {
	given := filepath.Clean(instance)
	real := canonical(given)
	if real == given {
		return given, workTree, adminDir, nil
	}
	gi, err := os.Lstat(given)
	if err != nil || !gi.IsDir() {
		return "", "", "", &WalkStop{Path: ".", Reason: changedDuringThePass + ".", Changed: true}
	}
	ri, err := os.Lstat(real)
	if err != nil || !ri.IsDir() || !os.SameFile(gi, ri) {
		return "", "", "", &WalkStop{Path: ".", Reason: changedDuringThePass + ".", Changed: true}
	}
	rebase := func(p string) string {
		for _, base := range []string{given, real} {
			if rel, err := relBelow(base, p); err == nil {
				return filepath.Join(real, rel)
			}
		}
		return p
	}
	return real, rebase(workTree), rebase(adminDir), nil
}
