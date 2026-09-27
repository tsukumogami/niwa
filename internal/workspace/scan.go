// Package workspace, file scan.go: comprehensive non-pushed-work detector
// used by niwa destroy's workspace-wipe path.
//
// Walks the on-disk tree under each instance dir, finds every git working
// tree (primary repos and linked worktrees), and runs a small set of git
// plumbing commands per tree to detect work that would be lost on a
// `rm -rf <workspace>`. Output is structured (typed Loss records grouped
// per repo per instance) and renderable to human-readable text.
//
// Cost shape: ~3-5 git commands per working tree, parallelized at the
// existing cloneWorkers=8 level. Realistic 15-repo workspace finishes
// in <2 seconds.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// LossKind enumerates categories of work that would be lost when the
// containing directory is deleted.
type LossKind string

const (
	// LossWorkingTreeDirty: modified or staged files in the working tree
	// (anything `git status --porcelain` reports except untracked).
	LossWorkingTreeDirty LossKind = "dirty"

	// LossUntracked: files present on disk that git is not tracking.
	// Could be junk or new code; presented as a count to avoid noise.
	LossUntracked LossKind = "untracked"

	// LossUnpushedCommits: branch whose commits were pushed once, but the
	// remote no longer holds them all: there are local commits beyond the
	// remote branch, or the remote branch was deleted or diverged. The
	// branch's content isn't on any remote default branch either.
	LossUnpushedCommits LossKind = "unpushed"

	// LossLocalOnlyBranch: branch whose commits no remote branch holds and
	// no remote default branch contains, by ancestry or by content.
	LossLocalOnlyBranch LossKind = "local-only"

	// LossUnlanded: branch whose commits are on a live remote branch, but
	// that branch's pull request merged or was closed, so the branch is due
	// for deletion, and the commits aren't on the default branch.
	LossUnlanded LossKind = "unlanded"

	// LossUnverified: branch whose durability couldn't be established
	// because a check couldn't run: a remote was unreachable, the forge
	// couldn't be asked, or a remote branch has commits this clone lacks.
	// Treated as at risk; the detail says what couldn't be checked.
	LossUnverified LossKind = "unverified"

	// LossScanError: a git command the scan relies on failed, so this part
	// of the repo couldn't be checked at all. Treated as at risk.
	LossScanError LossKind = "scan-error"

	// LossStash: git stash entries.
	LossStash LossKind = "stash"

	// LossDetachedOrphan: detached HEAD with commits no local branch holds
	// and that aren't durable by the same test branches get.
	LossDetachedOrphan LossKind = "detached"

	// LossExternalWorktree: linked worktree whose path is outside the
	// instance directory we're about to delete. Informational — the
	// worktree's files survive, but its admin entry in the primary
	// repo's .git/worktrees/ will be removed when the instance is
	// wiped, leaving it orphaned-as-a-worktree.
	LossExternalWorktree LossKind = "external-wt"
)

// Loss is one finding for one ref-or-state in one repo.
type Loss struct {
	Kind   LossKind
	Branch string // branch name; "" for stash/dirty/untracked
	Detail string // human summary: "3 modified", "2 commits", "1 stash"
	Path   string // worktree path if not the primary working tree
}

// RepoScan groups Losses for one repo (a primary working tree plus any
// linked worktrees). When the scanner couldn't enumerate the repo at
// all (broken .git, permission error), Skipped is set and Losses is
// empty — callers should treat Skipped as "we don't know what's in
// here, treat as dirty."
type RepoScan struct {
	Name    string // path relative to the instance dir
	Losses  []Loss
	Skipped string // non-empty if the scan failed; treat as dirty
}

// HasLoss reports whether this repo has any findings (loss or unknown).
func (r RepoScan) HasLoss() bool {
	return len(r.Losses) > 0 || r.Skipped != ""
}

// InstanceScan groups RepoScans for one instance.
type InstanceScan struct {
	InstanceName string
	InstanceDir  string
	Repos        []RepoScan
}

// HasLoss reports whether any repo in this instance has a finding.
func (s InstanceScan) HasLoss() bool {
	for _, r := range s.Repos {
		if r.HasLoss() {
			return true
		}
	}
	return false
}

// ScanInstance walks instanceDir, finds every git working tree (primary
// repos and linked worktrees those repos own), and runs the loss
// detector on each. Per-repo errors are captured in RepoScan.Skipped
// rather than aborting the scan.
//
// Skips paths matching <instanceDir>/.niwa (workspace metadata) since
// that's not a git repo and contains files we expect to delete.
//
// The scan contacts each repo's remotes (ls-remote, and a fetch of the
// default branch) and, through WithForge, the forge. See durable.go for the
// test a branch has to pass.
func ScanInstance(instanceDir string, opts ...ScanOption) (InstanceScan, error) {
	cfg := newScanConfig(opts)
	state, err := LoadState(instanceDir)
	scan := InstanceScan{InstanceDir: instanceDir}
	if err != nil {
		// Orphan instance dir — treat as dirty so the user is asked
		// before wiping it.
		scan.InstanceName = filepath.Base(instanceDir)
		scan.Repos = []RepoScan{{
			Name:    filepath.Base(instanceDir),
			Skipped: fmt.Sprintf("loading instance state: %v", err),
		}}
		return scan, nil
	}
	scan.InstanceName = state.InstanceName

	primaries, err := findPrimaryRepos(instanceDir)
	if err != nil {
		return scan, fmt.Errorf("walking %s: %w", instanceDir, err)
	}

	// Repos are scanned concurrently: each one waits on its remotes, so a
	// serial scan of a many-repo instance would stack those round trips.
	scan.Repos = make([]RepoScan, len(primaries))
	sem := make(chan struct{}, repoScanWorkers)
	var wg sync.WaitGroup
	for i, primary := range primaries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			scan.Repos[i] = scanRepo(instanceDir, primary, cfg)
		}()
	}
	wg.Wait()
	sort.Slice(scan.Repos, func(i, j int) bool {
		return scan.Repos[i].Name < scan.Repos[j].Name
	})
	return scan, nil
}

// findPrimaryRepos walks instanceDir looking for primary git working
// trees (directories that contain a `.git` subdirectory). Skips niwa-
// owned metadata at <instanceDir>/.niwa to avoid re-entering session
// worktrees as primaries — those are linked worktrees of repos we'll
// also discover, and `git worktree list` handles them.
//
// Returns absolute paths to each primary's working-tree root.
func findPrimaryRepos(instanceDir string) ([]string, error) {
	var primaries []string
	skipDir := filepath.Join(instanceDir, ".niwa")
	err := filepath.Walk(instanceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Surface unreadable subtrees but don't fail the whole walk.
			return filepath.SkipDir
		}
		if !info.IsDir() {
			return nil
		}
		if path == skipDir || strings.HasPrefix(path, skipDir+string(filepath.Separator)) {
			return filepath.SkipDir
		}
		gitPath := filepath.Join(path, ".git")
		if st, err := os.Stat(gitPath); err == nil && st.IsDir() {
			primaries = append(primaries, path)
			return filepath.SkipDir
		}
		return nil
	})
	return primaries, err
}

// repoScanWorkers bounds how many repos of one instance are scanned at once.
const repoScanWorkers = 4

// worktreeEntry is one working tree from `git worktree list --porcelain`.
type worktreeEntry struct {
	path   string
	branch string // checked-out branch, short name; "" when detached
}

// scanRepo runs the loss detector against a single primary working tree
// and any linked worktrees the repo owns. Returns one RepoScan per
// primary repo; linked-worktree losses are folded into the same
// RepoScan with Loss.Path set to the worktree's path.
//
// Working-tree state (uncommitted and untracked files, a detached HEAD) is
// checked in every working tree. Branches and stashes belong to the repo, not
// to a working tree, so they are checked once; a branch finding carries the
// path of the worktree that has the branch checked out, if any.
func scanRepo(instanceDir, primary string, cfg scanConfig) RepoScan {
	// Resolve the instance root and the primary once, up front. `git worktree
	// list` reports every path with its symlinks already resolved, while these
	// two are spelled however the caller and the directory walk produced them.
	// Left unresolved, the two spellings of the same location never compare
	// equal: the primary is not recognised among its own worktrees (so it gets
	// scanned twice and every loss in it is double-reported), every worktree
	// inside the instance is called external, and the relative paths in the
	// output degenerate into long "../.." escapes. Resolving both sides is what
	// makes the comparisons below mean what they say.
	root := resolveForCompare(instanceDir)
	primary = resolveForCompare(primary)

	rel, err := filepath.Rel(root, primary)
	if err != nil {
		rel = primary
	}
	scan := RepoScan{Name: rel}

	relPath := func(tree string) string {
		if tree == primary {
			return ""
		}
		if r, err := filepath.Rel(root, tree); err == nil {
			return r
		}
		return tree
	}

	// Working trees to inspect: the primary plus any linked worktrees
	// the repo's gitdir knows about.
	trees := []string{primary}
	branchPath := map[string]string{} // branch -> relative path of its worktree
	wts, listErr := listWorktrees(primary)
	if listErr != nil {
		// Couldn't enumerate worktrees, so any linked worktree inside the
		// instance goes unchecked.
		scan.Losses = append(scan.Losses, Loss{
			Kind:   LossScanError,
			Detail: fmt.Sprintf("worktree enumeration failed: %v", listErr),
		})
	}
	for _, wt := range wts {
		wt.path = resolveForCompare(wt.path)
		if wt.path == primary {
			if wt.branch != "" {
				branchPath[wt.branch] = ""
			}
			continue
		}
		// Differentiate worktrees inside the instance (will be deleted
		// outright) from those outside (only their admin entry is lost).
		if isInside(root, wt.path) {
			trees = append(trees, wt.path)
			if wt.branch != "" {
				branchPath[wt.branch] = relPath(wt.path)
			}
		} else {
			scan.Losses = append(scan.Losses, Loss{
				Kind:   LossExternalWorktree,
				Path:   wt.path,
				Detail: "linked worktree outside instance",
			})
		}
	}

	remotes, remoteErr := repoRemotes(primary, cfg)
	if remoteErr != nil {
		scan.Losses = append(scan.Losses, Loss{
			Kind:   LossScanError,
			Detail: fmt.Sprintf("listing remotes failed: %v", remoteErr),
		})
	}

	// For each working tree (primary + included linked), collect losses.
	for _, tree := range trees {
		scan.Losses = append(scan.Losses, scanWorkingTree(tree, relPath(tree), remotes, cfg)...)
	}

	// Branches and stashes, once per repo.
	scan.Losses = append(scan.Losses, scanBranches(primary, remotes, branchPath, cfg)...)
	if n, err := scanStashes(primary); err != nil {
		scan.Losses = append(scan.Losses, Loss{Kind: LossScanError, Detail: fmt.Sprintf("listing stashes failed: %v", err)})
	} else if n > 0 {
		scan.Losses = append(scan.Losses, Loss{
			Kind:   LossStash,
			Detail: fmt.Sprintf("%d stash entries", n),
		})
	}
	return scan
}

// scanWorkingTree runs the per-working-tree checks for a single working tree
// (primary or linked worktree). treePath is "" for the primary; for linked
// worktrees it is the path relative to the instance dir. A check that can't
// run is reported as LossScanError rather than skipped, so a broken or
// unreadable tree never reads as clean.
func scanWorkingTree(treeDir, treePath string, remotes []*remoteInfo, cfg scanConfig) []Loss {
	var losses []Loss

	// 1. status --porcelain: dirty + untracked.
	dirty, untracked, err := scanStatus(treeDir)
	if err != nil {
		losses = append(losses, Loss{
			Kind:   LossScanError,
			Detail: fmt.Sprintf("git status failed: %v", err),
			Path:   treePath,
		})
	}
	if dirty > 0 {
		losses = append(losses, Loss{
			Kind:   LossWorkingTreeDirty,
			Detail: fmt.Sprintf("%d modified or staged", dirty),
			Path:   treePath,
		})
	}
	if untracked > 0 {
		losses = append(losses, Loss{
			Kind:   LossUntracked,
			Detail: fmt.Sprintf("%d untracked", untracked),
			Path:   treePath,
		})
	}

	// 2. Detached HEAD holding commits no local branch has.
	if loss, err := scanDetachedOrphan(treeDir, remotes, cfg); err != nil {
		losses = append(losses, Loss{
			Kind:   LossScanError,
			Detail: fmt.Sprintf("checking detached HEAD failed: %v", err),
			Path:   treePath,
		})
	} else if loss != nil {
		loss.Path = treePath
		losses = append(losses, *loss)
	}

	return losses
}

// scanStatus parses `git status --porcelain=v1` output, counting modified/
// staged lines and untracked lines separately. Untracked files are asked for
// explicitly so a repo's status.showUntrackedFiles=no can't hide them.
func scanStatus(treeDir string) (dirty, untracked int, err error) {
	out, err := gitOutput(treeDir, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			dirty++
		}
	}
	return dirty, untracked, nil
}

// scanBranches judges every local branch with judgeTip and reports the ones
// whose commits aren't durable. branchPath maps a checked-out branch to the
// relative path of its worktree, so the finding points at it.
func scanBranches(dir string, remotes []*remoteInfo, branchPath map[string]string, cfg scanConfig) []Loss {
	const fmtSpec = "%(refname:short)|%(objectname)|%(upstream:short)"
	out, err := gitOutput(dir, "for-each-ref", "--format="+fmtSpec, "refs/heads")
	if err != nil {
		return []Loss{{Kind: LossScanError, Detail: fmt.Sprintf("listing branches failed: %v", err)}}
	}
	var losses []Loss
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		branch, tip, upstream := parts[0], parts[1], parts[2]
		v := judgeTip(context.Background(), dir, tip, branch, upstream, remotes, cfg)
		if v.durable {
			continue
		}
		losses = append(losses, Loss{
			Kind:   v.kind,
			Branch: branch,
			Detail: v.reason,
			Path:   branchPath[branch],
		})
	}
	return losses
}

// scanStashes returns the count of `git stash list` entries.
func scanStashes(treeDir string) (int, error) {
	out, err := gitOutput(treeDir, "stash", "list")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(out) == "" {
		return 0, nil
	}
	return strings.Count(strings.TrimRight(out, "\n"), "\n") + 1, nil
}

// scanDetachedOrphan checks a detached HEAD. Commits a local branch also
// holds are judged with that branch; otherwise HEAD is judged like a branch
// tip. Returns nil when HEAD is on a branch or its commits are durable.
func scanDetachedOrphan(treeDir string, remotes []*remoteInfo, cfg scanConfig) (*Loss, error) {
	// Is HEAD detached? `symbolic-ref -q HEAD` exits 1 when detached.
	if _, err := gitOutput(treeDir, "symbolic-ref", "-q", "HEAD"); err == nil {
		return nil, nil // on a branch
	}
	headOut, err := gitOutput(treeDir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, err
	}
	head := strings.TrimSpace(headOut)
	out, err := gitOutput(treeDir, "rev-list", "--count", head, "--not", "--branches")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "0" {
		return nil, nil
	}
	v := judgeTip(context.Background(), treeDir, head, "", "", remotes, cfg)
	if v.durable {
		return nil, nil
	}
	return &Loss{Kind: LossDetachedOrphan, Detail: v.reason}, nil
}

// listWorktrees parses `git worktree list --porcelain` and returns every
// worktree the repo knows about (including the primary) with its checked-out
// branch.
func listWorktrees(primary string) ([]worktreeEntry, error) {
	out, err := gitOutput(primary, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []worktreeEntry
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, worktreeEntry{path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch ") && len(entries) > 0:
			entries[len(entries)-1].branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return entries, nil
}

// gitOutput runs git -C treeDir <args...> and returns stdout. Combined
// stderr is dropped; non-zero exit produces an error wrapping the exit
// code.
func gitOutput(treeDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", treeDir}, args...)...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// isInside reports whether candidate is at or under root. Mixed absolute and
// relative inputs are fine: both sides go through resolveForCompare first.
func isInside(root, candidate string) bool {
	rel, err := filepath.Rel(resolveForCompare(root), resolveForCompare(candidate))
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

// resolveForCompare returns path in a form safe to compare against another
// path put through the same function: absolute, symlink-resolved, cleaned.
// Resolving matters because paths reaching this package come from two sources
// that spell the same location differently -- git resolves symlinks, callers
// and directory walks do not.
//
// It degrades rather than failing. A path that cannot be made absolute, or one
// that no longer exists on disk (a worktree git still holds an admin entry
// for), comes back in the best form reached so far, which leaves the caller's
// comparison no worse off than before resolution was attempted.
func resolveForCompare(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return filepath.Clean(resolved)
}

// ScanInstancesParallel scans multiple instances in parallel, bounded
// by `workers` concurrent goroutines. The result preserves the input
// order. Per-instance errors are captured in InstanceScan.Repos[*].Skipped
// rather than aborting the whole batch.
//
// `workers` defaults to 8 (mirroring cloneWorkers in apply.go) when 0 is
// passed.
func ScanInstancesParallel(workspaceRoot string, instanceDirs []string, workers int, opts ...ScanOption) ([]InstanceScan, error) {
	if workers <= 0 {
		workers = 8
	}
	if len(instanceDirs) < workers {
		workers = len(instanceDirs)
	}
	if workers == 0 {
		return nil, nil
	}

	type job struct {
		idx int
		dir string
	}
	type result struct {
		idx  int
		scan InstanceScan
		err  error
	}

	jobs := make(chan job, len(instanceDirs))
	results := make(chan result, len(instanceDirs))
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				s, err := ScanInstance(j.dir, opts...)
				results <- result{idx: j.idx, scan: s, err: err}
			}
		}()
	}
	for i, d := range instanceDirs {
		jobs <- job{idx: i, dir: d}
	}
	close(jobs)
	wg.Wait()
	close(results)

	scans := make([]InstanceScan, len(instanceDirs))
	var firstErr error
	for r := range results {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		scans[r.idx] = r.scan
	}
	return scans, firstErr
}

// FormatScans writes a human-readable rendering of the scans to w. The
// output groups losses per instance, then per repo, with worktrees
// nested under their primary repo. Instances with no losses appear
// with a "(clean)" tag so the user can confirm the scan saw them.
//
// workspaceName is shown in the closing prompt line; pass the
// EffectiveConfigName-derived string from the caller.
func FormatScans(scans []InstanceScan, w io.Writer, workspaceName string) {
	losing := 0
	for _, s := range scans {
		if s.HasLoss() {
			losing++
		}
	}
	if losing == 0 {
		if len(scans) == 1 {
			fmt.Fprintln(w, "No unpushed work detected.")
		} else {
			fmt.Fprintln(w, "No unpushed work detected across instances.")
		}
		return
	}

	if losing == 1 {
		fmt.Fprintln(w, "The following instance has unpushed work:")
	} else {
		fmt.Fprintln(w, "The following instances have unpushed work:")
	}
	fmt.Fprintln(w)
	for _, s := range scans {
		if !s.HasLoss() {
			fmt.Fprintf(w, "  %s (clean)\n", s.InstanceName)
			continue
		}
		fmt.Fprintf(w, "  %s:\n", s.InstanceName)
		for _, r := range s.Repos {
			if r.Skipped != "" {
				fmt.Fprintf(w, "    %s: %s\n", r.Name, r.Skipped)
				continue
			}
			if len(r.Losses) == 0 {
				continue
			}
			fmt.Fprintf(w, "    %s:\n", r.Name)
			// Group losses by Path so worktrees appear nested.
			byPath := map[string][]Loss{}
			var pathOrder []string
			for _, loss := range r.Losses {
				if _, ok := byPath[loss.Path]; !ok {
					pathOrder = append(pathOrder, loss.Path)
				}
				byPath[loss.Path] = append(byPath[loss.Path], loss)
			}
			sort.Strings(pathOrder)
			for _, p := range pathOrder {
				if p != "" {
					fmt.Fprintf(w, "      worktree at %s:\n", p)
				}
				for _, loss := range byPath[p] {
					prefix := "        "
					if p == "" {
						prefix = "      "
					}
					if loss.Branch != "" {
						fmt.Fprintf(w, "%s%s: %s — %s\n", prefix, loss.Kind, loss.Branch, loss.Detail)
					} else {
						fmt.Fprintf(w, "%s%s: %s\n", prefix, loss.Kind, loss.Detail)
					}
				}
			}
		}
	}
	fmt.Fprintln(w)
	if workspaceName != "" {
		fmt.Fprintf(w, `Type "%s" to confirm deletion (or Ctrl-C to abort):`+"\n", workspaceName)
	}
}
