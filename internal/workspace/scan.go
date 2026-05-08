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

	// LossUnpushedCommits: branch with commits ahead of its upstream.
	LossUnpushedCommits LossKind = "unpushed"

	// LossLocalOnlyBranch: branch with no upstream tracking ref. The
	// branch's commits are unique to this clone.
	LossLocalOnlyBranch LossKind = "local-only"

	// LossStash: git stash entries.
	LossStash LossKind = "stash"

	// LossDetachedOrphan: detached HEAD with commits not reachable from
	// any local branch or remote-tracking ref.
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
	Name    string  // path relative to the instance dir
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
func ScanInstance(instanceDir string) (InstanceScan, error) {
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

	for _, primary := range primaries {
		scan.Repos = append(scan.Repos, scanRepo(instanceDir, primary))
	}
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

// scanRepo runs the loss detector against a single primary working tree
// and any linked worktrees the repo owns. Returns one RepoScan per
// primary repo; linked-worktree losses are folded into the same
// RepoScan with Loss.Path set to the worktree's path.
func scanRepo(instanceDir, primary string) RepoScan {
	rel, err := filepath.Rel(instanceDir, primary)
	if err != nil {
		rel = primary
	}
	scan := RepoScan{Name: rel}

	// Working trees to inspect: the primary plus any linked worktrees
	// the repo's gitdir knows about.
	trees := []string{primary}
	wts, listErr := listWorktrees(primary)
	if listErr != nil {
		// Couldn't enumerate worktrees; not fatal, but record it.
		scan.Losses = append(scan.Losses, Loss{
			Kind:   LossExternalWorktree,
			Detail: fmt.Sprintf("worktree enumeration failed: %v", listErr),
		})
	}
	for _, wt := range wts {
		if wt == primary {
			continue
		}
		// Differentiate worktrees inside the instance (will be deleted
		// outright) from those outside (only their admin entry is lost).
		if isInside(instanceDir, wt) {
			trees = append(trees, wt)
		} else {
			scan.Losses = append(scan.Losses, Loss{
				Kind:   LossExternalWorktree,
				Path:   wt,
				Detail: "linked worktree outside instance",
			})
		}
	}

	// For each working tree (primary + included linked), collect losses.
	for _, tree := range trees {
		treePath := ""
		if tree != primary {
			if r, err := filepath.Rel(instanceDir, tree); err == nil {
				treePath = r
			} else {
				treePath = tree
			}
		}
		scan.Losses = append(scan.Losses, scanWorkingTree(tree, treePath)...)
	}
	return scan
}

// scanWorkingTree runs the loss detector for a single working tree
// (primary or linked worktree). treePath is "" for the primary; for
// linked worktrees it is the path relative to the instance dir.
func scanWorkingTree(treeDir, treePath string) []Loss {
	var losses []Loss

	// 1. status --porcelain: dirty + untracked.
	if dirty, untracked, err := scanStatus(treeDir); err == nil {
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
	}

	// 2. for-each-ref refs/heads: branches ahead of upstream + local-only branches.
	branchLosses, err := scanBranches(treeDir, treePath)
	if err == nil {
		losses = append(losses, branchLosses...)
	}

	// 3. stash list.
	if n, err := scanStashes(treeDir); err == nil && n > 0 {
		losses = append(losses, Loss{
			Kind:   LossStash,
			Detail: fmt.Sprintf("%d stash entries", n),
			Path:   treePath,
		})
	}

	// 4. Detached HEAD with orphan commits.
	if orphan, err := scanDetachedOrphan(treeDir); err == nil && orphan > 0 {
		losses = append(losses, Loss{
			Kind:   LossDetachedOrphan,
			Detail: fmt.Sprintf("%d commits not on any branch or remote", orphan),
			Path:   treePath,
		})
	}

	return losses
}

// scanStatus parses `git status --porcelain=v1` output, counting modified/
// staged lines and untracked lines separately.
func scanStatus(treeDir string) (dirty, untracked int, err error) {
	out, err := gitOutput(treeDir, "status", "--porcelain=v1")
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

// scanBranches parses `git for-each-ref --format='%(refname:short) %(upstream:track) %(upstream)' refs/heads`.
// Branches with non-empty upstream-track containing "ahead" → unpushed.
// Branches with empty upstream → local-only.
func scanBranches(treeDir, treePath string) ([]Loss, error) {
	const fmtSpec = "%(refname:short)|%(upstream:track)|%(upstream)"
	out, err := gitOutput(treeDir, "for-each-ref", "--format="+fmtSpec, "refs/heads")
	if err != nil {
		return nil, err
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
		branch, track, upstream := parts[0], parts[1], parts[2]
		if upstream == "" {
			losses = append(losses, Loss{
				Kind:   LossLocalOnlyBranch,
				Branch: branch,
				Detail: "no upstream",
				Path:   treePath,
			})
			continue
		}
		if strings.Contains(track, "ahead") {
			losses = append(losses, Loss{
				Kind:   LossUnpushedCommits,
				Branch: branch,
				Detail: strings.TrimSpace(strings.Trim(track, "[]")),
				Path:   treePath,
			})
		}
	}
	return losses, nil
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
	return strings.Count(out, "\n") + 1, nil
}

// scanDetachedOrphan returns the count of commits at HEAD that are not
// reachable from any local branch or remote-tracking ref. Returns 0
// when HEAD is on a branch or when the count is zero.
func scanDetachedOrphan(treeDir string) (int, error) {
	// Is HEAD detached? `symbolic-ref -q HEAD` returns non-zero when detached.
	if _, err := gitOutput(treeDir, "symbolic-ref", "-q", "HEAD"); err == nil {
		return 0, nil // on a branch
	}
	out, err := gitOutput(treeDir, "rev-list", "--count", "HEAD", "--not", "--branches", "--remotes")
	if err != nil {
		return 0, err
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	return n, nil
}

// listWorktrees parses `git worktree list --porcelain` and returns the
// absolute paths of every worktree the repo knows about (including the
// primary). Returns nil when the command fails.
func listWorktrees(primary string) ([]string, error) {
	out, err := gitOutput(primary, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
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

// isInside reports whether candidate is at or under root. Both must be
// absolute or both relative; mixed inputs are normalized via Abs.
func isInside(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candAbs)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

// ScanInstancesParallel scans multiple instances in parallel, bounded
// by `workers` concurrent goroutines. The result preserves the input
// order. Per-instance errors are captured in InstanceScan.Repos[*].Skipped
// rather than aborting the whole batch.
//
// `workers` defaults to 8 (mirroring cloneWorkers in apply.go) when 0 is
// passed.
func ScanInstancesParallel(workspaceRoot string, instanceDirs []string, workers int) ([]InstanceScan, error) {
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
				s, err := ScanInstance(j.dir)
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
