package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// BranchHeld judges one session branch of a workspace clone against the
// proof remotes, for a forced destroy of a session whose worktree directory
// is already gone. It walks the instance the way the removal pass does and
// takes the clone whose work tree is repoDir; runs the identity check and the
// configuration read on it, with the program-naming refusal and the check of
// the work tree the configuration names; reads refs/heads/<branch>, and the
// HEAD of the gone worktree at workTree when git still lists one; and judges
// those refs with the proof remotes the pass would pick for that clone (its
// clone URL and the proof_remotes prefixes), every git call through
// gitCommand.
//
// keep is empty only when every ref it read is held. Otherwise it says why:
// a commit no proof remote holds, a proof remote that can't be read or a
// clone with none, a key that names a program, a walk keep on the clone, a
// clone the walk didn't find, a read that fails, a credential-helper read
// that failed, or a pass with no scratch directory.
//
// A branch the listing doesn't show is judged only on git's positive answer
// that it doesn't exist (see branchAbsent): then absent is true, with keep
// empty, and there is no branch to delete. Any other answer keeps, since a
// ref git can't read is left out of the listing without a word.
func (e *Env) BranchHeld(ctx context.Context, instance, repoDir, branch, workTree string, p Policy) (absent bool, keep string) {
	switch {
	case e == nil:
		return false, "git can't be run for this pass"
	case e.credErr != nil:
		return false, "the credential-helper read failed: " + e.credErr.Error()
	case e.Scratch == nil:
		why := "no scratch directory"
		if e.ScratchErr != nil {
			why = e.ScratchErr.Error()
		}
		return false, "scratch store unusable: " + why
	case p.ConfigError != nil:
		return false, "configuration unreadable: " + p.ConfigError.Error()
	case branch == "":
		return false, "the session record names no branch"
	}
	keep = e.branchHeld(ctx, instance, repoDir, branch, workTree, p, &absent)
	if keep != "" {
		return false, keep
	}
	return absent, ""
}

// branchHeld is BranchHeld's walk, reads and judgement. It sets *absent only
// on git's positive answer that the branch doesn't exist.
func (e *Env) branchHeld(ctx context.Context, instance, repoDir, branch, workTree string, p Policy, absent *bool) (keep string) {

	f := walkUnit(ctx, Unit{InstancePath: instance}, Policy{CloneURL: p.CloneURL})
	var own *foundRepo
	for i := range f.repos {
		r := &f.repos[i]
		if !r.Linked && r.WorkTree != "" && sameDir(r.WorkTree, repoDir) {
			own = r
			break
		}
	}
	if own == nil {
		return "the walk found no repository at " + repoDir + " inside the instance"
	}
	var keeps []string
	for _, c := range append(append([]Check(nil), f.errs...), f.keeps...) {
		mine := len(c.Paths) == 0
		for _, path := range c.Paths {
			if path == own.Rel || strings.HasPrefix(path, own.Rel+"/") {
				mine = true
			}
		}
		if mine {
			keeps = append(keeps, c.Evidence)
		}
	}
	if len(keeps) > 0 {
		return strings.Join(keeps, "; ")
	}

	t := repoTarget{Rel: own.Rel, Base: own.Base, Dirs: own.dirs(), URL: own.URL, wtInfo: own.wtInfo, gitInfo: own.gitInfo, walk: &f, repo: *own}
	rc, res, ok := e.openRepo(ctx, t)
	if !ok {
		var why []string
		for _, k := range res.keeps {
			why = append(why, k.Evidence)
		}
		if len(why) == 0 {
			why = append(why, own.Rel+": the configuration check kept it")
		}
		return strings.Join(why, "; ")
	}
	// As readRepo does: the identity check again, and on a git older than
	// 2.45 the configuration refusal too, before any call that reads an
	// object.
	if err := t.checkIdentity(); err != nil {
		return own.Rel + ": changed since the walk: " + err.Error()
	}
	if !e.gitVersion.atLeast(lazyFetchOffVersion.major, lazyFetchOffVersion.minor) {
		again, err := e.readConfig(ctx, t.Dirs)
		if err != nil {
			return own.Rel + ": " + err.Error()
		}
		if _, why := e.refusal(again); why != "" {
			return own.Rel + ": " + why
		}
	}

	opts := callOpts{excludesUnset: rc.excludesUnset}
	all, warn, err := listRefs(ctx, e, t.Dirs, opts)
	switch {
	case err != nil:
		return own.Rel + ": " + err.Error()
	case warn != "":
		return own.Rel + ": the ref listing reported a problem: " + warn
	}
	var refs []ref
	for _, rf := range all {
		if rf.Name == "refs/heads/"+branch {
			refs = append(refs, rf)
		}
	}
	if len(refs) == 0 {
		if why := e.branchAbsent(ctx, t.Dirs, opts, branch); why != "" {
			return own.Rel + ": " + why
		}
		*absent = true
	}
	entries, err := listWorktrees(ctx, e, t.Dirs, opts)
	if err != nil {
		return own.Rel + ": " + err.Error()
	}
	for _, h := range headsOf(entries) {
		if !h.Main && goneSamePath(h.Path, workTree) {
			refs = append(refs, ref{OID: h.OID, Name: "HEAD of worktree " + f.label(workTree)})
		}
	}

	proof := proofPolicy{cloneURLs: p.CloneURLs, cloneURLOf: p.CloneURL, prefixes: p.ProofRemotes}
	ps := selectProofs(rc.remotes, own.URL, proof, e.allowFile)
	held := e.checkHeld(ctx, t, opts, refs, ps)
	why := append(append([]string(nil), held.errs...), held.keeps...)
	if len(why) > 0 {
		return own.Rel + ": " + strings.Join(why, "; ")
	}
	return ""
}

// showRefExistsVersion is the first git version with show-ref --exists, the
// one read that tells a missing ref from one git can't read: an older git's
// show-ref --verify, rev-parse --verify and for-each-ref answer both the same
// way.
var showRefExistsVersion = gitVersion{2, 43}

// refMissing is what show-ref --exists writes, and all it writes, for a ref
// that doesn't exist, under the fixed LANG=C environment.
const refMissing = "error: reference does not exist"

// branchAbsent returns "" only on git's positive answer that
// refs/heads/<branch> doesn't exist: show-ref --exists exiting 2 with
// refMissing as its whole standard error and nothing on standard output.
// Every other outcome is the reason to keep: a git older than
// showRefExistsVersion, which can't tell the two apart; the ref being there
// after all; a failed lookup (exit 1, as for a refs directory git can't
// read); an unreadable packed-refs file (exit 128); a timeout; or any other
// exit or text.
func (e *Env) branchAbsent(ctx context.Context, r repoDirs, o callOpts, branch string) string {
	if !e.gitVersion.atLeast(showRefExistsVersion.major, showRefExistsVersion.minor) {
		return fmt.Sprintf("the ref listing doesn't show branch %s, and git %d.%d can't tell a missing branch from one it can't read (git show-ref --exists needs %d.%d)",
			branch, e.gitVersion.major, e.gitVersion.minor, showRefExistsVersion.major, showRefExistsVersion.minor)
	}
	cmd, err := e.gitCommand(ctx, r, o, "show-ref", "--exists", "refs/heads/"+branch)
	if err != nil {
		return err.Error()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "the ref listing doesn't show branch " + branch + ", but git reads it"
	case ctx.Err() != nil:
		return "reading branch " + branch + ": " + ctx.Err().Error()
	case errors.As(err, &exit) && exit.ExitCode() == 2 && stdout.Len() == 0 && strings.TrimSpace(stderr.String()) == refMissing:
		return ""
	}
	msg := failureLine(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return "branch " + branch + " can't be read: " + msg
}

// goneSamePath reports whether a and b name the same path, one that may no
// longer exist: as text, with every link resolved, or with the links above
// the last element resolved.
func goneSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if sameDir(a, b) {
		return true
	}
	parentOf := func(p string) string {
		p = filepath.Clean(p)
		return filepath.Join(canonical(filepath.Dir(p)), filepath.Base(p))
	}
	return parentOf(a) == parentOf(b)
}
