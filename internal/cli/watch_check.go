package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

var watchCheckHandle string

func init() {
	watchCheckFreshnessCmd.Flags().StringVar(&watchCheckHandle, "handle", "",
		"the staged-review handle to re-validate")
	rootCmd.AddCommand(watchCheckFreshnessCmd)
}

// watchCheckFreshnessCmd is the deterministic session pre-flight: a staged review
// session invokes it as its first step to confirm the review is still worth
// presenting. It loads the staged record for --handle, recomputes the same
// stillRequested + ancestry inputs the watcher-pass GC uses against live GitHub,
// runs the identical pure Freshness predicate, and exits 0 (fresh) or non-zero with
// the failed-condition reason on stderr (stale). It is hidden because it is a
// machine-facing pre-flight, not an operator verb; the watcher-pass prune is the
// backstop if a session never runs it.
//
// Note: this check reaches the GitHub API, so it is only usable from a review
// session that has network egress -- i.e. an uncontained (watch_sandbox=off) or
// manual run. A required-sandbox review session has no egress and cannot run it;
// for those, the watcher-pass prune in runWatchOnce (which executes in the trusted
// watcher process, not the caged session) is the sole freshness mechanism. That
// is why the prune, not this pre-flight, is the deterministic backstop.
var watchCheckFreshnessCmd = &cobra.Command{
	Use:    "watch-check-freshness",
	Short:  "Re-validate a staged review's freshness (session pre-flight)",
	Hidden: true,
	RunE:   runWatchCheckFreshness,
}

func runWatchCheckFreshness(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	if watchCheckHandle == "" {
		return fmt.Errorf("watch-check-freshness: --handle is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("watch-check-freshness: getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("watch-check-freshness: classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("watch-check-freshness: not inside a niwa workspace")
	}
	root := class.WorkspaceRoot

	rec, err := watch.LoadStagedRecord(root, watchCheckHandle)
	if err != nil {
		return fmt.Errorf("watch-check-freshness: loading staged record: %w", err)
	}

	client := github.NewAPIClient(resolveGitHubToken())
	login, err := client.CurrentLogin(ctx)
	if err != nil {
		return fmt.Errorf("watch-check-freshness: resolving GitHub login (check auth): %w", err)
	}
	prs, err := client.SearchReviewRequestedPRs(ctx, login)
	if err != nil {
		return fmt.Errorf("watch-check-freshness: searching review-requested PRs: %w", err)
	}

	ok, reason := evalRecordFreshness(ctx, client, requestedIdentities(prs), rec)
	if !ok {
		// Stale: name the failed condition on stderr and exit non-zero so the session
		// discards its draft and posts nothing. Wrapped as a plain error -- the root
		// silences cobra's usage/error banners, so Execute prints this once on stderr.
		return errors.New(reason)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "fresh: %s/%s#%d still worth reviewing\n", rec.Owner, rec.Repo, rec.Number)
	return nil
}
