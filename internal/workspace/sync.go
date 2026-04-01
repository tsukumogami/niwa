package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// RepoSyncStatus captures the state of a repo for sync decisions.
type RepoSyncStatus struct {
	Clean         bool   // working tree is clean (no staged/unstaged changes)
	CurrentBranch string // current branch name (or "HEAD" if detached)
	OnDefault     bool   // current branch matches the configured default
	Behind        int    // commits behind remote
	Ahead         int    // commits ahead of remote
	NoTracking    bool   // no upstream tracking branch configured
}

// SyncResult describes what happened during a sync attempt.
type SyncResult struct {
	Action  string // "pulled", "skipped", "up-to-date", "fetch-failed"
	Reason  string // empty for pulled/up-to-date, explanation for skipped
	Commits int    // number of new commits pulled (0 if skipped)
}

// InspectRepo gathers the sync-relevant state of a git repository.
func InspectRepo(repoDir, defaultBranch string) (RepoSyncStatus, error) {
	var status RepoSyncStatus

	// Check if working tree is clean.
	out, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output()
	if err != nil {
		return status, fmt.Errorf("checking repo status: %w", err)
	}
	status.Clean = strings.TrimSpace(string(out)) == ""

	// Get current branch name.
	out, err = exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return status, fmt.Errorf("getting current branch: %w", err)
	}
	status.CurrentBranch = strings.TrimSpace(string(out))
	status.OnDefault = status.CurrentBranch == defaultBranch

	// Get ahead/behind counts relative to upstream.
	out, err = exec.Command("git", "-C", repoDir, "rev-list", "--count", "--left-right", "@{u}...HEAD").Output()
	if err != nil {
		// No upstream tracking branch.
		status.NoTracking = true
		return status, nil
	}

	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) == 2 {
		status.Behind, _ = strconv.Atoi(parts[0])
		status.Ahead, _ = strconv.Atoi(parts[1])
	}

	return status, nil
}

// FetchRepo runs git fetch origin for the given repository directory.
func FetchRepo(ctx context.Context, repoDir string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "fetch", "origin")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetching %s: %w", repoDir, err)
	}
	return nil
}

// PullRepo performs a fast-forward-only pull on the given branch and returns
// the number of new commits that were pulled.
func PullRepo(ctx context.Context, repoDir, branch string) (int, error) {
	// Record current HEAD before pull.
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return 0, fmt.Errorf("getting HEAD before pull: %w", err)
	}
	oldCommit := strings.TrimSpace(string(out))

	// Pull with fast-forward only.
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "pull", "--ff-only", "origin", branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("pulling %s: %w", branch, err)
	}

	// Count new commits.
	out, err = exec.Command("git", "-C", repoDir, "rev-list", "--count", oldCommit+"..HEAD").Output()
	if err != nil {
		return 0, fmt.Errorf("counting new commits: %w", err)
	}
	count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return count, nil
}

// SyncRepo fetches, inspects, and optionally pulls a repository. It returns a
// SyncResult describing what action was taken.
func SyncRepo(ctx context.Context, repoDir, defaultBranch string) (SyncResult, error) {
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	if err := FetchRepo(ctx, repoDir); err != nil {
		return SyncResult{Action: "fetch-failed", Reason: err.Error()}, nil
	}

	status, err := InspectRepo(repoDir, defaultBranch)
	if err != nil {
		return SyncResult{}, err
	}

	if !status.Clean {
		return SyncResult{Action: "skipped", Reason: "dirty working tree"}, nil
	}

	if !status.OnDefault {
		return SyncResult{
			Action: "skipped",
			Reason: fmt.Sprintf("on branch %s, not %s", status.CurrentBranch, defaultBranch),
		}, nil
	}

	if status.NoTracking {
		return SyncResult{Action: "skipped", Reason: "no upstream tracking branch"}, nil
	}

	if status.Ahead > 0 && status.Behind > 0 {
		return SyncResult{Action: "skipped", Reason: "diverged from remote"}, nil
	}

	if status.Ahead > 0 {
		return SyncResult{Action: "skipped", Reason: "ahead of remote"}, nil
	}

	if status.Behind == 0 {
		return SyncResult{Action: "up-to-date"}, nil
	}

	commits, err := PullRepo(ctx, repoDir, defaultBranch)
	if err != nil {
		return SyncResult{}, err
	}

	return SyncResult{Action: "pulled", Commits: commits}, nil
}
