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
	c := scanConfig{networkTimeout: DefaultScanNetworkTimeout}
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
}

// repoRemotes queries every remote of the repo at dir: lists its live
// branches, learns its default branch, and refreshes the local copy of that
// default branch. Per-remote failures are recorded on the remoteInfo, not
// returned; the error return is for failing to list the remotes at all.
func repoRemotes(dir string, cfg scanConfig) ([]*remoteInfo, error) {
	out, err := gitOutput(dir, "remote")
	if err != nil {
		return nil, err
	}
	var remotes []*remoteInfo
	for _, name := range strings.Fields(out) {
		r := &remoteInfo{name: name}
		if u, err := gitOutput(dir, "config", "--get", "remote."+name+".url"); err == nil {
			r.rawURL = strings.TrimSpace(u)
		}
		r.heads, r.defaultBranch, r.lsErr = lsRemote(dir, name, cfg.networkTimeout)
		if r.defaultBranch == "" {
			r.defaultBranch = localDefaultBranch(dir, name)
		}
		if r.lsErr == nil && r.defaultBranch != "" {
			if _, live := r.heads[r.defaultBranch]; live {
				r.refreshErr = fetchDefault(dir, name, r.defaultBranch, cfg.networkTimeout)
			}
		} else if r.lsErr != nil {
			r.refreshErr = r.lsErr
		}
		if r.defaultBranch != "" {
			ref := "refs/remotes/" + name + "/" + r.defaultBranch
			if _, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
				r.defaultRef = ref
			}
		}
		remotes = append(remotes, r)
	}
	return remotes, nil
}

// lsRemote lists the remote's branches and its HEAD symref in one call.
func lsRemote(dir, remote string, timeout time.Duration) (map[string]string, string, error) {
	out, err := gitNetwork(dir, timeout, "ls-remote", "--symref", remote, "HEAD", "refs/heads/*")
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
func localDefaultBranch(dir, remote string) string {
	out, err := gitOutput(dir, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/"+remote+"/")
}

// fetchDefault updates the remote-tracking ref for the remote's default
// branch, and nothing else. Without it, a pull request merged on the forge but
// not yet fetched would look unmerged.
func fetchDefault(dir, remote, branch string, timeout time.Duration) error {
	refspec := "+refs/heads/" + branch + ":refs/remotes/" + remote + "/" + branch
	_, err := gitNetwork(dir, timeout, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", remote, refspec)
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
		if isAncestor(dir, tip, r.defaultRef) {
			return branchVerdict{durable: true}
		}
	}

	// 2. Landed by content.
	mergeTreeOK := supportsMergeTree()
	if mergeTreeOK {
		for _, r := range defaults {
			if landedByContent(dir, tip, r.defaultRef) {
				return branchVerdict{durable: true}
			}
		}
	}

	// 3. Held by a live remote branch the forge vouches for.
	var notes []string
	unverified := false
	homes := liveHomes(dir, tip, branch, upstream, remotes)
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
	unlanded := countUnlanded(dir, tip, defaults, mergeTreeOK)
	where := describeDefaults(defaults)
	if len(homes) > 0 {
		// On a live remote branch, but nothing vouches for it surviving.
		v.kind = LossUnlanded
		if unverified {
			v.kind = LossUnverified
		}
		v.reason = fmt.Sprintf("%s not on %s; %s", commits(unlanded), where, strings.Join(notes, "; "))
	} else {
		v.kind, v.reason = whyNoHome(dir, tip, branch, upstream, remotes, unlanded, where)
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
func liveHomes(dir, tip, branch, upstream string, remotes []*remoteInfo) []liveHome {
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
		if out, err := gitOutput(dir, "for-each-ref", "--format=%(refname)", "--contains", tip, "refs/remotes/"+r.name+"/"); err == nil {
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
			if isAncestor(dir, tip, sha) {
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
func whyNoHome(dir, tip, branch, upstream string, remotes []*remoteInfo, unlanded int, where string) (LossKind, string) {
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
			if _, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+r.name+"/"+branch); err == nil {
				names = append(names, branch)
			} else if _, live := r.heads[branch]; live {
				names = append(names, branch)
			}
		}
		for _, n := range names {
			sha, live := r.heads[n]
			if live {
				if isAncestor(dir, sha, tip) {
					ahead := countRange(dir, sha, tip)
					return LossUnpushedCommits, fmt.Sprintf("%s; %s beyond %s/%s not pushed", base, commits(ahead), r.name, n)
				}
				if !hasCommit(dir, sha) {
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
func landedByContent(dir, tip, def string) bool {
	mbOut, err := gitOutput(dir, "merge-base", def, tip)
	if err != nil {
		return false
	}
	mb := strings.TrimSpace(mbOut)
	pathsOut, err := gitOutput(dir, "diff", "--name-only", "--no-renames", "-z", mb, tip)
	if err != nil {
		return false
	}
	var paths []string
	for _, p := range strings.Split(pathsOut, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		// The branch's tree equals the merge base's, which def contains.
		return true
	}

	cands := []string{def}
	args := []string{"rev-list", "--first-parent", "--max-count=500", mb + ".." + def}
	if len(paths) <= 1000 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	if out, err := gitOutputEnv(dir, []string{"GIT_LITERAL_PATHSPECS=1"}, args...); err == nil {
		cands = append(cands, strings.Fields(out)...)
	}
	for _, c := range cands {
		treeOut, err := gitOutput(dir, "rev-parse", c+"^{tree}")
		if err != nil {
			continue
		}
		merged, err := gitOutput(dir, "merge-tree", "--write-tree", "--no-messages", c, tip)
		if err != nil {
			continue // conflict (exit 1) or error: not proven here
		}
		first, _, _ := strings.Cut(merged, "\n")
		if strings.TrimSpace(first) == strings.TrimSpace(treeOut) {
			return true
		}
	}
	return false
}

func isAncestor(dir, a, b string) bool {
	_, err := gitOutput(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func hasCommit(dir, sha string) bool {
	_, err := gitOutput(dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// countUnlanded counts the commits on tip that haven't landed on a default
// branch. After a squash merge the branch's own commits never become
// ancestors of the default branch, so counting by ancestry would include the
// ones that did land. When merge-tree is available, the newest commit on the
// branch's first-parent line whose content has landed is found, and only the
// commits after it are counted.
func countUnlanded(dir, tip string, defaults []*remoteInfo, mergeTreeOK bool) int {
	n := countNotOn(dir, tip, defaults)
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
	out, err := gitOutput(dir, args...)
	if err != nil {
		return n
	}
	for _, c := range strings.Fields(out)[1:] {
		for _, r := range defaults {
			if landedByContent(dir, c, r.defaultRef) {
				return countRange(dir, c, tip)
			}
		}
	}
	return n
}

// countNotOn counts the commits reachable from tip and from none of the
// default branches.
func countNotOn(dir, tip string, defaults []*remoteInfo) int {
	args := []string{"rev-list", "--count", tip}
	if len(defaults) > 0 {
		args = append(args, "--not")
		for _, r := range defaults {
			args = append(args, r.defaultRef)
		}
	}
	out, err := gitOutput(dir, args...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

func countRange(dir, from, to string) int {
	out, err := gitOutput(dir, "rev-list", "--count", from+".."+to)
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

// gitNetwork runs a git command that talks to a remote, bounded by timeout,
// with credential prompts disabled so it fails instead of waiting for input.
// The error carries git's stderr so a finding can say what went wrong.
func gitNetwork(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
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

// gitOutputEnv is gitOutput with extra environment variables.
func gitOutputEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

var (
	mergeTreeOnce sync.Once
	mergeTreeOK   bool

	// supportsMergeTree is swapped by tests to exercise the old-git path.
	supportsMergeTree = gitSupportsMergeTreeWriteTree
)

// gitSupportsMergeTreeWriteTree reports whether the git on PATH has `git
// merge-tree --write-tree` (git 2.38 and later).
func gitSupportsMergeTreeWriteTree() bool {
	mergeTreeOnce.Do(func() {
		out, err := exec.Command("git", "version").Output()
		if err != nil {
			return
		}
		major, minor, ok := parseGitVersion(string(out))
		mergeTreeOK = ok && (major > 2 || (major == 2 && minor >= 38))
	})
	return mergeTreeOK
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
