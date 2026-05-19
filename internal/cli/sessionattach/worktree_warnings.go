package sessionattach

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Warnings inspects a session worktree for uncommitted changes, untracked
// files, and unpushed commits on the session branch, and writes a warning
// section to w for each kind found. Mirrors the existing branch_warning
// precedent on niwa_destroy_session: warn loudly to stderr; never auto-clean.
//
// branchName is the fully-qualified branch backing this session, resolved
// via SessionLifecycleState.EffectiveBranchName() at the caller. Pre-v1.1
// sessions resolve to `session/<sessionID>`; bootstrap-created sessions
// resolve to `niwa-bootstrap/<sessionID>`. Passing the branch through
// (rather than reconstructing from sessionID) preserves the upstream-tracking
// query semantics regardless of which prefix was used.
//
// Three kinds are detected, each with its own warning section:
//   - uncommitted changes: any line in `git status --porcelain` that does NOT
//     start with "??" (those are untracked).
//   - untracked files: lines starting with "??".
//   - unpushed commits on the branch: ahead-count > 0 from
//     `git for-each-ref --format='%(upstream:track)' refs/heads/<branchName>`.
//
// Failures inspecting any of the three kinds are reported as a single
// trailing warning ("warning: could not inspect worktree state: ...") rather
// than aborting -- the natural-detach release path must always reach the
// daemon respawn step regardless.
func Warnings(worktreePath, branchName string, w io.Writer) {
	uncommitted, untracked, statusErr := classifyStatus(worktreePath)
	if statusErr != nil {
		fmt.Fprintf(w, "warning: could not inspect worktree status: %v\n", statusErr)
	}
	if len(uncommitted) > 0 {
		fmt.Fprintln(w, "warning: worktree has uncommitted changes")
		for _, line := range uncommitted {
			fmt.Fprintln(w, "  "+line)
		}
	}
	if len(untracked) > 0 {
		fmt.Fprintln(w, "warning: worktree has untracked files")
		for _, line := range untracked {
			fmt.Fprintln(w, "  "+line)
		}
	}
	ahead, aheadErr := unpushedCount(worktreePath, branchName)
	if aheadErr != nil {
		fmt.Fprintf(w, "warning: could not inspect session branch state: %v\n", aheadErr)
	}
	if ahead > 0 {
		fmt.Fprintf(w, "warning: worktree has unpushed commits on %s\n", branchName)
		fmt.Fprintf(w, "  ahead by %d commit(s); push with `git push -u origin %s`\n", ahead, branchName)
	}
}

// classifyStatus runs `git status --porcelain` in the worktree and splits the
// output into uncommitted lines (modified/staged/etc.) and untracked lines.
func classifyStatus(worktreePath string) (uncommitted, untracked []string, err error) {
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain")
	out, runErr := cmd.Output()
	if runErr != nil {
		return nil, nil, runErr
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked = append(untracked, line)
		} else {
			uncommitted = append(uncommitted, line)
		}
	}
	return uncommitted, untracked, nil
}

// unpushedCount returns the ahead-count of the named branch versus its
// upstream. Returns 0 when the branch has no upstream configured (a
// legitimately not-yet-pushed branch is reported by counting its commits
// instead -- but for the natural-detach UX, we only warn when there's an
// upstream divergence, since branches without upstream are routine and
// would generate noise).
func unpushedCount(worktreePath, branch string) (int, error) {
	// %(upstream:track) prints e.g. "[ahead 3]" or "[gone]" or empty.
	cmd := exec.Command("git", "-C", worktreePath, "for-each-ref",
		"--format=%(upstream:track)", "refs/heads/"+branch)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	track := strings.TrimSpace(string(out))
	if track == "" {
		// No upstream or no divergence; nothing to warn about.
		return 0, nil
	}
	// Parse "[ahead N]" or "[ahead N, behind M]". Only the ahead count
	// matters for the unpushed warning.
	idx := strings.Index(track, "ahead ")
	if idx < 0 {
		return 0, nil
	}
	rest := track[idx+len("ahead "):]
	end := strings.IndexAny(rest, ",]")
	if end < 0 {
		return 0, nil
	}
	var ahead int
	if _, scanErr := fmt.Sscanf(rest[:end], "%d", &ahead); scanErr != nil {
		return 0, nil
	}
	return ahead, nil
}
