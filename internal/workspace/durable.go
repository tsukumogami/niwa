// Package workspace, file durable.go: decides whether the commits a local
// branch (or a detached HEAD) carries would survive deleting the clone.
//
// Local bookkeeping can't answer that. A branch's upstream setting says
// nothing about where its content went: a squash merge lands the content on
// the default branch as a new commit, so the branch never becomes an ancestor
// of it, and a remote-tracking ref can outlive the remote branch it mirrors.
// The test here asks the remote instead, at scan time, and treats a commit as
// durable only when one of these holds:
//
//  1. It is an ancestor of a remote's default branch (after refreshing that
//     branch with a bounded fetch).
//  2. Its changes are already on the default branch by content: merging the
//     branch into some first-parent default-branch commit since the fork
//     changes nothing (`git merge-tree --write-tree`). This is what recognises
//     a squash merge, and it keeps recognising it after the default branch
//     moves on.
//  3. It is contained in a remote branch that exists right now (`git
//     ls-remote`), and the forge says no merged or closed pull request has that
//     branch as its head. A merged PR's head branch is about to be deleted, so
//     only test 2 can pass its commits; that is what catches commits pushed
//     after the merge.
//
// Anything else is at risk, and so is every case where a check couldn't run
// (remote unreachable, forge unavailable, git too old for merge-tree): the
// finding names the branch and the reason, and never passes silently.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HeadPRState is a forge's answer about the pull requests whose head is a
// given branch.
type HeadPRState int

const (
	// HeadPRNone: no pull request has ever used this branch as its head.
	HeadPRNone HeadPRState = iota + 1
	// HeadPROpen: an open pull request uses this branch as its head, so the
	// forge keeps the branch at least until that PR is resolved.
	HeadPROpen
	// HeadPRMerged: a merged pull request used this branch as its head and no
	// open one does. The branch is due for deletion.
	HeadPRMerged
	// HeadPRClosed: only closed, unmerged pull requests used this branch. An
	// abandoned branch; nothing keeps it.
	HeadPRClosed
)

// HeadPR is a forge's answer for one head branch. Number is the pull request
// the state refers to (0 for HeadPRNone).
type HeadPR struct {
	State  HeadPRState
	Number int
}

// Forge looks up pull requests by head branch. Implementations talk to a
// forge's API; ScanInstance only asks for repositories on github.com.
type Forge interface {
	HeadPR(ctx context.Context, owner, repo, branch string) (HeadPR, error)
}

// ScanOption configures ScanInstance and ScanInstancesParallel.
type ScanOption func(*scanConfig)

type scanConfig struct {
	forge          Forge
	networkTimeout time.Duration

	// git builds every git call the scan makes, the git version read
	// included: nothing in the scan starts git any other way. It is the only
	// field of its kind here.
	git GitCommandFunc
	// walk, when set (WithRepositoryWalk), names the repositories the scan
	// reads in place of its own search for .git directories. It is not a
	// builder: it returns no command.
	walk RepositoryWalkFunc
	// liveDefaults is set when the builder came from WithGitCommand: a
	// remote's default branch is then judged only at the commit ls-remote
	// read from the remote itself, never at the local remote-tracking ref,
	// and a commit the repository lacks comes in through fetch, never by a
	// fetch into the repository.
	liveDefaults bool
	// fetch brings commits from a remote into a scratch store and returns
	// the store's objects directory (WithRemoteFetch). It is not a builder:
	// it starts its own git and returns no command.
	fetch RemoteFetchFunc
	// objectEnv is what a repository's later calls add to their environment
	// once fetch filled stores for it: GIT_ALTERNATE_OBJECT_DIRECTORIES
	// naming those stores.
	objectEnv []string
	// mergeTree is this scan's own read of whether git has merge-tree
	// --write-tree, made once through git.
	mergeTree *mergeTreeProbe
}

// RemoteFetchFunc brings the history of ids from the remote named remote of
// the repository at dir into a scratch store outside that repository, and
// returns the store's objects directory. An error means nothing was brought
// in, and the scan judges nothing landed on that remote's default branch.
type RemoteFetchFunc func(ctx context.Context, dir, remote string, ids []string) (string, error)

// WithRemoteFetch gives a scan made WithGitCommand its way to read a
// remote's default branch when the repository lacks the commit the remote
// names: fn fetches it into a scratch store, and the scan's later calls in
// that repository read the store's objects as alternates. Without it, such a
// remote's default branch is never judged.
func WithRemoteFetch(fn RemoteFetchFunc) ScanOption {
	return func(c *scanConfig) { c.fetch = fn }
}

// GitCommandFunc builds one git call the scan makes: git run in the working
// tree at dir with args as its arguments, env added to the environment the
// builder gives it, and ctx's end stopping it. An error means the call isn't
// started, and the scan treats the check it was for as one that couldn't
// run, which lists the repository as at risk.
type GitCommandFunc func(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error)

// WithGitCommand makes the scan start every git call through fn. Without
// it the scan starts no git at all and lists every repository as at risk. fn
// adds --ignore-submodules=all and --untracked-files=all to every status and
// --no-show-signature to every log-family call itself, so the scan doesn't
// pass them. A nil fn refuses every call, as no WithGitCommand does.
func WithGitCommand(fn GitCommandFunc) ScanOption {
	return func(c *scanConfig) {
		if fn == nil {
			fn = refuseGitCommand
		}
		c.git = fn
		c.liveDefaults = true
	}
}

// WalkedRepository is one repository a walk of the instance found, before
// the scan's first git call: a work tree the scan reads with its own status,
// or one it runs no git call in.
type WalkedRepository struct {
	// Name is its path relative to the instance.
	Name string
	// WorkTree is the work tree the scan reads; empty when Reason is set.
	WorkTree string
	// Reason, when set, is why the scan runs no git call in it, a key that
	// names a program named, for one. The scan lists it as a scan-error loss.
	Reason string
}

// RepositoryWalkFunc walks the instance at instanceDir and runs the
// configuration check on every repository it finds, and returns each one:
// the clones, submodules, gitlinks and other nested repositories, and every
// finding of the walk's own as a repository with a reason.
type RepositoryWalkFunc func(ctx context.Context, instanceDir string) []WalkedRepository

// WithRepositoryWalk makes the scan run fn over the instance before its
// first git call and read exactly the work trees fn hands it, each with its
// own status, in place of its own search for .git directories. Every
// repository fn gives a reason for is listed as a scan-error loss carrying
// that reason, and the scan runs no git call in it.
func WithRepositoryWalk(fn RepositoryWalkFunc) ScanOption {
	return func(c *scanConfig) { c.walk = fn }
}

// refuseGitCommand is the builder a scan has when its caller hands it none,
// or hands WithGitCommand a nil one: it refuses every call, so git never
// runs and every repository is listed as at risk.
func refuseGitCommand(context.Context, string, []string, ...string) (*exec.Cmd, error) {
	return nil, errors.New("the scan was given no git command builder")
}

// DefaultScanNetworkTimeout bounds each network operation the scan makes: one
// ls-remote and one fetch per remote, and one forge lookup per branch.
const DefaultScanNetworkTimeout = 20 * time.Second

// WithForge lets the scan ask a forge whether a remote branch's pull request
// merged. Without one, a branch whose commits live only on a remote branch is
// reported as unverified: the scan can't tell whether that branch is about to
// be deleted.
func WithForge(f Forge) ScanOption {
	return func(c *scanConfig) { c.forge = f }
}

// WithNetworkTimeout overrides DefaultScanNetworkTimeout.
func WithNetworkTimeout(d time.Duration) ScanOption {
	return func(c *scanConfig) { c.networkTimeout = d }
}

func newScanConfig(opts []ScanOption) scanConfig {
	c := scanConfig{networkTimeout: DefaultScanNetworkTimeout, git: refuseGitCommand, mergeTree: &mergeTreeProbe{}}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// remoteInfo is what the scan learned about one remote of a repo.
type remoteInfo struct {
	name   string
	rawURL string // remote.<name>.url as configured, before insteadOf rewriting

	// defaultBranch is the remote's default branch name ("" if unknown), and
	// defaultRef the local remote-tracking ref holding it, when that ref
	// exists locally.
	defaultBranch string
	defaultRef    string

	// heads maps each branch that exists on the remote right now to its sha.
	// nil when ls-remote failed; lsErr then says why.
	heads map[string]string
	lsErr error

	// refreshErr is set when the default branch couldn't be fetched, so the
	// local copy of it may be behind the remote.
	refreshErr error

	// objects is the scratch store's objects directory the default branch's
	// commit was fetched into, under liveDefaults; "" when none was needed.
	objects string
}

// withFetchedObjects is cfg for a repository's calls after its remotes were
// read: when any remote's default branch was fetched into a scratch store,
// every later call names those stores as alternates, so the commit reads
// without its objects being written into the repository.
func (c scanConfig) withFetchedObjects(remotes []*remoteInfo) scanConfig {
	var dirs []string
	for _, r := range remotes {
		if r.objects != "" {
			dirs = append(dirs, r.objects)
		}
	}
	if len(dirs) > 0 {
		c.objectEnv = []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strings.Join(dirs, string(os.PathListSeparator))}
	}
	return c
}

// liveDefault sets r's default ref, under liveDefaults, to the commit the
// remote's default branch names right now, as ls-remote read it from the
// remote itself, and never to the local remote-tracking ref, which only says
// what the remote held at the last fetch. A remote that couldn't be read, or
// lists no default branch, gets no default ref, so nothing is judged landed
// on it. A commit the repository lacks is fetched into a scratch store
// through cfg.fetch; a fetch that can't run leaves no default ref and the
// reason on refreshErr.
func liveDefault(cfg scanConfig, dir string, r *remoteInfo) {
	if r.lsErr != nil {
		r.refreshErr = r.lsErr
		return
	}
	if r.defaultBranch == "" {
		r.defaultBranch = localDefaultBranch(cfg, dir, r.name)
	}
	sha, live := r.heads[r.defaultBranch]
	if r.defaultBranch == "" || !live {
		return
	}
	if !hasCommit(cfg, dir, sha) {
		if cfg.fetch == nil {
			r.refreshErr = errors.New("the scan has no scratch store to fetch it into")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), cfg.networkTimeout)
		defer cancel()
		objects, err := cfg.fetch(ctx, dir, r.name, []string{sha})
		if err != nil {
			r.refreshErr = err
			return
		}
		r.objects = objects
	}
	r.defaultRef = sha
}

// repoRemotes queries every remote of the repo at dir: lists its live
// branches, learns its default branch, and refreshes the local copy of that
// default branch. Per-remote failures are recorded on the remoteInfo, not
// returned; the error return is for failing to list the remotes at all.
func repoRemotes(dir string, cfg scanConfig) ([]*remoteInfo, error) {
	out, err := gitOutput(cfg, dir, "remote")
	if err != nil {
		return nil, err
	}
	var remotes []*remoteInfo
	for _, name := range strings.Fields(out) {
		r := &remoteInfo{name: name}
		if u, err := gitOutput(cfg, dir, "config", "--get", "remote."+name+".url"); err == nil {
			r.rawURL = strings.TrimSpace(u)
		}
		r.heads, r.defaultBranch, r.lsErr = lsRemote(cfg, dir, name)
		if cfg.liveDefaults {
			liveDefault(cfg, dir, r)
			remotes = append(remotes, r)
			continue
		}
		if r.defaultBranch == "" {
			r.defaultBranch = localDefaultBranch(cfg, dir, name)
		}
		if r.lsErr == nil && r.defaultBranch != "" {
			if _, live := r.heads[r.defaultBranch]; live {
				r.refreshErr = fetchDefault(cfg, dir, name, r.defaultBranch)
			}
		} else if r.lsErr != nil {
			r.refreshErr = r.lsErr
		}
		if r.defaultBranch != "" {
			ref := "refs/remotes/" + name + "/" + r.defaultBranch
			if _, err := gitOutput(cfg, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
				r.defaultRef = ref
			}
		}
		remotes = append(remotes, r)
	}
	return remotes, nil
}

// lsRemote lists the remote's branches and its HEAD symref in one call.
func lsRemote(cfg scanConfig, dir, remote string) (map[string]string, string, error) {
	out, err := gitNetwork(cfg, dir, "ls-remote", "--symref", remote, "HEAD", "refs/heads/*")
	if err != nil {
		return nil, "", err
	}
	heads := map[string]string{}
	def := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ref: ") {
			// "ref: refs/heads/main\tHEAD"
			fields := strings.Fields(strings.TrimPrefix(line, "ref: "))
			if len(fields) == 2 && fields[1] == "HEAD" {
				def = strings.TrimPrefix(fields[0], "refs/heads/")
			}
			continue
		}
		sha, ref, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(ref, "refs/heads/") {
			continue
		}
		heads[strings.TrimPrefix(ref, "refs/heads/")] = sha
	}
	return heads, def, nil
}

// localDefaultBranch reads refs/remotes/<remote>/HEAD, the remote's default
// branch as of the last clone or `git remote set-head`.
func localDefaultBranch(cfg scanConfig, dir, remote string) string {
	out, err := gitOutput(cfg, dir, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/"+remote+"/")
}

// fetchDefault updates the remote-tracking ref for the remote's default
// branch, and nothing else. Without it, a pull request merged on the forge but
// not yet fetched would look unmerged.
func fetchDefault(cfg scanConfig, dir, remote, branch string) error {
	refspec := "+refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch
	_, err := gitNetwork(cfg, dir, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", remote, refspec)
	return err
}

// branchVerdict is the outcome of judging one tip.
type branchVerdict struct {
	durable bool
	kind    LossKind
	reason  string
}

// judgeTip decides whether the commits reachable from tip survive deleting
// this clone. branch is the local branch name ("" for a detached HEAD) and
// upstream its configured upstream ("<remote>/<branch>" short form, or "").
func judgeTip(ctx context.Context, dir, tip, branch, upstream string, remotes []*remoteInfo, cfg scanConfig) branchVerdict {
	var defaults []*remoteInfo
	for _, r := range remotes {
		if r.defaultRef != "" {
			defaults = append(defaults, r)
		}
	}

	// 1. Landed by ancestry.
	for _, r := range defaults {
		if isAncestor(cfg, dir, tip, r.defaultRef) {
			return branchVerdict{durable: true}
		}
	}

	// 2. Landed by content. A merge-tree the builder refused lists the
	// branch with the refusal, which names the key.
	mergeTreeOK := cfg.mergeTreeSupported(dir)
	if mergeTreeOK {
		for _, r := range defaults {
			landed, err := landedByContent(cfg, dir, tip, r.defaultRef)
			if err != nil {
				return branchVerdict{kind: LossScanError, reason: fmt.Sprintf("checking whether the commits landed on %s failed: %v", r.name, err)}
			}
			if landed {
				return branchVerdict{durable: true}
			}
		}
	}

	// 3. Held by a live remote branch the forge vouches for.
	var notes []string
	unverified := false
	homes := liveHomes(cfg, dir, tip, branch, upstream, remotes)
	for _, h := range homes {
		res := vouch(ctx, h, cfg)
		if res.durable {
			return branchVerdict{durable: true}
		}
		notes = append(notes, res.note)
		unverified = unverified || res.unverified
	}

	// At risk. Say how much and why.
	v := branchVerdict{}
	unlanded := countUnlanded(cfg, dir, tip, defaults, mergeTreeOK)
	where := describeDefaults(defaults)
	if len(homes) > 0 {
		// On a live remote branch, but nothing vouches for it surviving.
		v.kind = LossUnlanded
		if unverified {
			v.kind = LossUnverified
		}
		v.reason = fmt.Sprintf("%s not on %s; %s", commits(unlanded), where, strings.Join(notes, "; "))
	} else {
		v.kind, v.reason = whyNoHome(cfg, dir, tip, branch, upstream, remotes, unlanded, where)
	}

	var caveats []string
	for _, r := range remotes {
		if r.refreshErr != nil && r.lsErr == nil {
			caveats = append(caveats, fmt.Sprintf("couldn't refresh %s/%s: %s", r.name, r.defaultBranch, oneLine(r.refreshErr)))
		}
	}
	if len(defaults) == 0 {
		caveats = append(caveats, "no remote default branch found to compare against")
	}
	if !mergeTreeOK {
		caveats = append(caveats, "git is older than 2.38, so a squash merge can't be recognised")
	}
	if len(caveats) > 0 {
		v.reason += " (" + strings.Join(caveats, "; ") + ")"
	}
	return v
}

// liveHome is a remote branch that exists right now and contains the tip.
type liveHome struct {
	remote *remoteInfo
	branch string
}

// liveHomes returns the remote branches that exist on the remote right now
// and contain tip. Candidates are the branch's own name, its upstream, and any
// remote-tracking ref already containing it; each only counts if ls-remote
// lists it and the tip is an ancestor of the sha ls-remote reported, which
// must be present locally. A tip with local commits beyond the live sha has
// unpushed work and gets no home.
func liveHomes(cfg scanConfig, dir, tip, branch, upstream string, remotes []*remoteInfo) []liveHome {
	var homes []liveHome
	for _, r := range remotes {
		if r.heads == nil {
			continue
		}
		cands := map[string]bool{}
		if branch != "" {
			cands[branch] = true
		}
		if up, ok := strings.CutPrefix(upstream, r.name+"/"); ok {
			cands[up] = true
		}
		if out, err := gitOutput(cfg, dir, "for-each-ref", "--format=%(refname)", "--contains", tip, "refs/remotes/"+r.name+"/"); err == nil {
			for _, ref := range strings.Fields(out) {
				name := strings.TrimPrefix(ref, "refs/remotes/"+r.name+"/")
				if name != "HEAD" {
					cands[name] = true
				}
			}
		}
		names := make([]string, 0, len(cands))
		for n := range cands {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sha, live := r.heads[n]
			if !live || n == r.defaultBranch {
				continue
			}
			if isAncestor(cfg, dir, tip, sha) {
				homes = append(homes, liveHome{remote: r, branch: n})
			}
		}
	}
	return homes
}

// vouchResult is the forge's verdict on one live remote branch.
type vouchResult struct {
	durable    bool
	unverified bool   // the forge couldn't be asked, or couldn't answer
	note       string // why the branch can't be relied on, when !durable
}

// vouch asks the forge whether a live remote branch will survive: it will when
// no merged or closed PR has the branch as its head.
func vouch(ctx context.Context, h liveHome, cfg scanConfig) vouchResult {
	label := h.remote.name + "/" + h.branch
	unknown := func(format string, a ...any) vouchResult {
		return vouchResult{unverified: true, note: fmt.Sprintf(format, a...)}
	}
	owner, repo, ok := githubOwnerRepo(h.remote.rawURL)
	if !ok {
		return unknown("pushed to %s, but %s isn't a GitHub remote, so whether its PR merged (leaving the branch due for deletion) couldn't be checked", label, h.remote.name)
	}
	if cfg.forge == nil {
		return unknown("pushed to %s, but no GitHub client was available to check whether its PR merged", label)
	}
	lctx, cancel := context.WithTimeout(ctx, cfg.networkTimeout)
	defer cancel()
	pr, err := cfg.forge.HeadPR(lctx, owner, repo, h.branch)
	if err != nil {
		return unknown("pushed to %s, but checking its PR state on GitHub failed: %s", label, oneLine(err))
	}
	switch pr.State {
	case HeadPRNone, HeadPROpen:
		return vouchResult{durable: true}
	case HeadPRMerged:
		return vouchResult{note: fmt.Sprintf("its PR #%d merged, so %s is due for deletion, and these commits aren't in what merged", pr.Number, label)}
	case HeadPRClosed:
		return vouchResult{note: fmt.Sprintf("its PR #%d was closed without merging, so nothing keeps %s", pr.Number, label)}
	default:
		return unknown("pushed to %s, but GitHub gave no usable PR state", label)
	}
}

// whyNoHome explains a tip that no live remote branch holds.
func whyNoHome(cfg scanConfig, dir, tip, branch, upstream string, remotes []*remoteInfo, unlanded int, where string) (LossKind, string) {
	base := fmt.Sprintf("%s not on %s", commits(unlanded), where)

	var unreachable []string
	for _, r := range remotes {
		if r.lsErr != nil {
			unreachable = append(unreachable, fmt.Sprintf("couldn't reach %s: %s", r.name, oneLine(r.lsErr)))
		}
	}

	// Pushed once, then the remote branch moved or vanished?
	for _, r := range remotes {
		if r.heads == nil {
			continue
		}
		// Names this tip was pushed under: its upstream on this remote, and
		// its own name when a remote-tracking ref for it exists locally.
		var names []string
		if up, ok := strings.CutPrefix(upstream, r.name+"/"); ok {
			names = append(names, up)
		}
		if branch != "" {
			if _, err := gitOutput(cfg, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+r.name+"/"+branch); err == nil {
				names = append(names, branch)
			} else if _, live := r.heads[branch]; live {
				names = append(names, branch)
			}
		}
		for _, n := range names {
			sha, live := r.heads[n]
			if live {
				if isAncestor(cfg, dir, sha, tip) {
					ahead := countRange(cfg, dir, sha, tip)
					return LossUnpushedCommits, fmt.Sprintf("%s; %s beyond %s/%s not pushed", base, commits(ahead), r.name, n)
				}
				if !hasCommit(cfg, dir, sha) {
					return LossUnverified, fmt.Sprintf("%s; %s/%s has commits this clone hasn't fetched, so whether it holds these couldn't be checked", base, r.name, n)
				}
				return LossUnpushedCommits, fmt.Sprintf("%s; %s/%s has diverged from this branch", base, r.name, n)
			}
			return LossUnpushedCommits, fmt.Sprintf("%s; %s/%s no longer exists on the remote", base, r.name, n)
		}
	}

	if len(unreachable) > 0 {
		return LossUnverified, base + "; " + strings.Join(unreachable, "; ")
	}
	return LossLocalOnlyBranch, base + "; no remote branch holds them"
}

// landedByContent reports whether every change tip carries relative to its
// merge base with def is already on def. For the tip of def and each
// first-parent commit of def since the merge base that touches a path the
// branch changed, it merges tip into that commit with `git merge-tree
// --write-tree`; a clean merge whose tree equals the commit's own tree means
// the branch adds nothing to a state def actually reached.
//
// Checking earlier first-parent commits, not just the tip, is what keeps a
// squash merge recognisable after def later edits or reverts the same lines.
// A conflict, an error, or a different tree all mean "not proven landed".
// A merge-tree the builder refuses to start, as when the configuration
// check right before it finds a key that names a program, ends the check
// with that refusal as the error, so the caller can name it.
func landedByContent(cfg scanConfig, dir, tip, def string) (bool, error) {
	mbOut, err := gitOutput(cfg, dir, "merge-base", def, tip)
	if err != nil {
		return false, nil
	}
	mb := strings.TrimSpace(mbOut)
	pathsOut, err := gitOutput(cfg, dir, "diff", "--name-only", "--no-renames", "-z", mb, tip)
	if err != nil {
		return false, nil
	}
	var paths []string
	for _, p := range strings.Split(pathsOut, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		// The branch's tree equals the merge base's, which def contains.
		return true, nil
	}

	cands := []string{def}
	args := []string{"rev-list", "--first-parent", "--max-count=500", mb + ".." + def}
	if len(paths) <= 1000 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	if out, err := gitOutputEnv(cfg, dir, []string{"GIT_LITERAL_PATHSPECS=1"}, args...); err == nil {
		cands = append(cands, strings.Fields(out)...)
	}
	for _, c := range cands {
		treeOut, err := gitOutput(cfg, dir, "rev-parse", c+"^{tree}")
		if err != nil {
			continue
		}
		merged, err := gitOutput(cfg, dir, "merge-tree", "--write-tree", "--no-messages", c, tip)
		var refused *notStarted
		if errors.As(err, &refused) {
			return false, err
		}
		if err != nil {
			continue // conflict (exit 1) or error: not proven here
		}
		first, _, _ := strings.Cut(merged, "\n")
		if strings.TrimSpace(first) == strings.TrimSpace(treeOut) {
			return true, nil
		}
	}
	return false, nil
}

func isAncestor(cfg scanConfig, dir, a, b string) bool {
	_, err := gitOutput(cfg, dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func hasCommit(cfg scanConfig, dir, sha string) bool {
	_, err := gitOutput(cfg, dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// countUnlanded counts the commits on tip that haven't landed on a default
// branch. After a squash merge the branch's own commits never become
// ancestors of the default branch, so counting by ancestry would include the
// ones that did land. When merge-tree is available, the newest commit on the
// branch's first-parent line whose content has landed is found, and only the
// commits after it are counted.
func countUnlanded(cfg scanConfig, dir, tip string, defaults []*remoteInfo, mergeTreeOK bool) int {
	n := countNotOn(cfg, dir, tip, defaults)
	if !mergeTreeOK || n == 0 {
		return n
	}
	args := []string{"rev-list", "--first-parent", "--max-count=50", tip}
	if len(defaults) > 0 {
		args = append(args, "--not")
		for _, r := range defaults {
			args = append(args, r.defaultRef)
		}
	}
	out, err := gitOutput(cfg, dir, args...)
	if err != nil {
		return n
	}
	for _, c := range strings.Fields(out)[1:] {
		for _, r := range defaults {
			// A refused merge-tree proves nothing landed: every commit counts.
			if landed, _ := landedByContent(cfg, dir, c, r.defaultRef); landed {
				return countRange(cfg, dir, c, tip)
			}
		}
	}
	return n
}

// countNotOn counts the commits reachable from tip and from none of the
// default branches.
func countNotOn(cfg scanConfig, dir, tip string, defaults []*remoteInfo) int {
	args := []string{"rev-list", "--count", tip}
	if len(defaults) > 0 {
		args = append(args, "--not")
		for _, r := range defaults {
			args = append(args, r.defaultRef)
		}
	}
	out, err := gitOutput(cfg, dir, args...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func countRange(cfg scanConfig, dir, from, to string) int {
	out, err := gitOutput(cfg, dir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func describeDefaults(defaults []*remoteInfo) string {
	if len(defaults) == 0 {
		return "any remote default branch"
	}
	var names []string
	for _, r := range defaults {
		names = append(names, r.name+"/"+r.defaultBranch)
	}
	return strings.Join(names, " or ")
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

func oneLine(err error) string {
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// githubOwnerRepo returns owner and repo when rawURL names a repository on
// github.com, in any of git's URL forms.
func githubOwnerRepo(rawURL string) (owner, repo string, ok bool) {
	if rawURL == "" || strings.HasPrefix(rawURL, "file://") || strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, ".") {
		return "", "", false
	}
	s, err := parseRemoteURLToSource(rawURL)
	if err != nil || s.Host != "" || s.Owner == "" || s.Repo == "" {
		return "", "", false
	}
	return s.Owner, s.Repo, true
}

// gitNetwork runs a git command that talks to a remote, built by the scan's
// builder, bounded by the scan's network timeout, with credential prompts
// disabled so it fails instead of waiting for input. The error carries git's
// stderr so a finding can say what went wrong.
func gitNetwork(cfg scanConfig, dir string, args ...string) (string, error) {
	timeout := cfg.networkTimeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd, err := cfg.git(ctx, dir, []string{"GIT_TERMINAL_PROMPT=0"}, args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git %s timed out after %s", args[0], timeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// gitOutputEnv is gitOutput with extra environment variables, which the
// scan's builder adds to the environment it gives the call, after them the
// repository's objectEnv.
func gitOutputEnv(cfg scanConfig, dir string, env []string, args ...string) (string, error) {
	env = append(append([]string(nil), env...), cfg.objectEnv...)
	cmd, err := cfg.git(context.Background(), dir, env, args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), &notStarted{err: err})
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// notStarted is the error of a git call the scan's builder refused to build,
// so git never ran: a configuration check that now names a program, for one.
type notStarted struct{ err error }

func (n *notStarted) Error() string { return n.err.Error() }
func (n *notStarted) Unwrap() error { return n.err }

// supportsMergeTree, when set, answers in place of the scan's own version
// read. Tests set it to exercise the old-git path; nothing else does.
var supportsMergeTree func() bool

// mergeTreeProbe is one scan's answer to whether its git has `git merge-tree
// --write-tree` (git 2.38 and later), read once per scan.
type mergeTreeProbe struct {
	once sync.Once
	ok   bool
}

// mergeTreeSupported reports whether the scan's git has merge-tree
// --write-tree. The first call reads `git version` through the scan's
// builder, in dir; a read that fails or doesn't parse counts as an older git,
// so a squash merge isn't recognised and the branch is listed.
func (c scanConfig) mergeTreeSupported(dir string) bool {
	if supportsMergeTree != nil {
		return supportsMergeTree()
	}
	if c.mergeTree == nil {
		return false
	}
	c.mergeTree.once.Do(func() {
		out, err := gitOutput(c, dir, "version")
		if err != nil {
			return
		}
		major, minor, ok := parseGitVersion(out)
		c.mergeTree.ok = ok && (major > 2 || (major == 2 && minor >= 38))
	})
	return c.mergeTree.ok
}

// parseGitVersion reads "git version 2.43.0" (with any vendor suffix, such as
// " (Apple Git-146)") into its major and minor numbers.
func parseGitVersion(s string) (major, minor int, ok bool) {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return 0, 0, false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	var err1, err2 error
	major, err1 = strconv.Atoi(parts[0])
	minor, err2 = strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
}
