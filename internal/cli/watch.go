package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

var watchOnce bool

func init() {
	watchCmd.Flags().BoolVar(&watchOnce, "once", false, "perform exactly one poll-and-dispatch pass and exit (the only supported mode)")
	watchCmd.AddCommand(watchPostCmd)
	watchCmd.AddCommand(watchDiscardCmd)
	rootCmd.AddCommand(watchCmd)
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stage contained review agents for PRs you were directly requested on",
	Long: `watch --once performs a single poll-and-dispatch pass: it finds open PRs
on GitHub where you are the directly-requested reviewer (intersected with the
repos in your niwa workspace), and for each new one stages a contained review
agent that reads the PR in an isolated clone, drafts a review, and halts. The
review session runs with no network egress and a credential-scrubbed
environment, so a hostile PR cannot exfiltrate or act.

It is a stateless single-shot verb -- no daemon, no resident process. Use
'niwa watch post <handle>' to post an approved draft and
'niwa watch discard <handle>' to drop one.`,
	RunE: runWatchOnce,
}

var watchPostCmd = &cobra.Command{
	Use:   "post <handle>",
	Short: "Post the approved draft review for a staged PR (trusted, outside the sandbox)",
	Args:  cobra.ExactArgs(1),
	RunE:  runWatchPost,
}

var watchDiscardCmd = &cobra.Command{
	Use:   "discard <handle>",
	Short: "Discard a staged PR review without posting",
	Args:  cobra.ExactArgs(1),
	RunE:  runWatchDiscard,
}

// runWatchOnce is the single poll-and-dispatch pass. It is fail-closed (refuses
// to dispatch where containment cannot be enforced) and fail-loud (reports and
// exits non-zero on a poll/dispatch failure, recording nothing it could not
// stage safely).
func runWatchOnce(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// (1) Fail-closed preflight: refuse where the OS sandbox cannot be enforced,
	// BEFORE creating any instance, rather than dispatch uncontained.
	if runtime.GOOS == "windows" {
		return fmt.Errorf("niwa watch: the OS sandbox is unavailable on Windows; refusing to dispatch a review uncontained")
	}
	if _, err := lookClaude(); err != nil {
		return fmt.Errorf("niwa watch: claude binary not found in PATH; install Claude Code before watching")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("niwa watch: getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("niwa watch: classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("niwa watch: not inside a niwa workspace; run watch from within a workspace")
	}
	root := class.WorkspaceRoot

	scope, err := workspaceScope(cwd)
	if err != nil {
		return fmt.Errorf("niwa watch: reading workspace repos: %w", err)
	}

	// (2) Poll GitHub. Failures here are fail-loud: a broken poll must not look
	// like "nothing to review".
	token := resolveGitHubToken()
	client := github.NewAPIClient(token)
	login, err := client.CurrentLogin(ctx)
	if err != nil {
		return fmt.Errorf("niwa watch: resolving GitHub login (check auth): %w", err)
	}
	prs, err := client.SearchReviewRequestedPRs(ctx, login)
	if err != nil {
		return fmt.Errorf("niwa watch: searching review-requested PRs: %w", err)
	}

	handled, err := watch.LoadHandledSet(root)
	if err != nil {
		return fmt.Errorf("niwa watch: reading handled-set: %w", err)
	}

	selected := watch.Select(prs, scope, handled, watch.DefaultPerRunBound)
	if len(selected) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "niwa watch: nothing to stage")
		return nil
	}

	for _, pr := range selected {
		if err := stageReview(cmd, root, cwd, token, client, pr); err != nil {
			// Fail loud; the PR was not recorded handled (see stageReview), so a
			// later run re-attempts it.
			return fmt.Errorf("niwa watch: staging %s/%s#%d: %w", pr.Owner, pr.Repo, pr.Number, err)
		}
	}
	return nil
}

// stageReview provisions an instance, fetches the PR head as inert data, applies
// the containment profile, and launches a detached, contained review agent. The
// handled-set and the staged-review record are written ONLY on success.
func stageReview(cmd *cobra.Command, root, cwd, token string, client *github.APIClient, pr github.PRRef) error {
	ctx := cmd.Context()

	head, err := client.GetPullHead(ctx, pr.Owner, pr.Repo, pr.Number)
	if err != nil {
		return err
	}
	cloneURL := head.CloneURL
	if cloneURL == "" {
		cloneURL = fmt.Sprintf("https://github.com/%s/%s.git", pr.Owner, pr.Repo)
	}

	slug := sanitizeInstanceSlug(fmt.Sprintf("watch-%s-%s-%d", pr.Owner, pr.Repo, pr.Number))
	namePrefix, err := dispatchNameSuffix(slug)
	if err != nil {
		return fmt.Errorf("generating instance name: %w", err)
	}

	reapOpportunistically(root)
	provRes, err := provisionInstanceFunc(ctx, root, cwd, namePrefix, "+")
	if err != nil {
		return fmt.Errorf("provisioning contained instance: %w", err)
	}
	instancePath := provRes.Path

	success := false
	defer func() {
		if !success {
			_ = destroyInstanceFunc(instancePath)
		}
	}()

	// Fetch the PR head as inert data (hardened) into the clone subdir.
	cloneDir := filepath.Join(instancePath, watch.DefaultCloneRelDir)
	if err := watch.FetchPRHead(ctx, cloneURL, head.SHA, cloneDir, token); err != nil {
		return fmt.Errorf("fetching PR head: %w", err)
	}

	synthHome, err := watch.SyntheticHomeDir(instancePath)
	if err != nil {
		return err
	}
	// Apply and re-verify the no-egress containment profile before launch.
	if err := watch.ApplyContainment(instancePath); err != nil {
		return err
	}

	prompt := watch.BuildReviewPrompt(pr, watch.DefaultCloneRelDir, watch.DefaultDraftRelPath)
	env := watch.BuildContainedEnv(os.Environ(), synthHome)
	passthrough := buildDispatchPassthrough(slug, "")

	// Launch detached (no terminal attach) with the contained env.
	if err := dispatchLaunch(ctx, instancePath, prompt, passthrough, env); err != nil {
		return fmt.Errorf("launching contained review agent: %w", err)
	}

	// Record ONLY on success, so a failed stage does not suppress a re-attempt.
	rec := watch.StagedRecord{
		Handle:    slug,
		Owner:     pr.Owner,
		Repo:      pr.Repo,
		Number:    pr.Number,
		URL:       pr.URL,
		DraftPath: filepath.Join(instancePath, watch.DefaultDraftRelPath),
	}
	if err := watch.SaveStagedRecord(root, rec); err != nil {
		return err
	}
	if err := watch.AppendHandled(root, watch.HandledKey(pr.Owner, pr.Repo, pr.Number)); err != nil {
		return err
	}
	success = true
	fmt.Fprintf(cmd.OutOrStdout(),
		"niwa watch: staged review for %s/%s#%d (handle %s)\n", pr.Owner, pr.Repo, pr.Number, slug)
	return nil
}

// runWatchPost posts an approved draft review. It is the trusted step: it runs
// outside the contained session, holds the GitHub token (which never entered
// that session), and fixes the review event in code so a hostile draft cannot
// force an approval.
func runWatchPost(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	handle := args[0]

	root, err := workspaceRootFromCwd()
	if err != nil {
		return err
	}
	rec, err := watch.LoadStagedRecord(root, handle)
	if err != nil {
		return fmt.Errorf("niwa watch post: %w", err)
	}
	if err := validateDraftPath(root, rec.DraftPath); err != nil {
		return fmt.Errorf("niwa watch post: %w", err)
	}
	body, err := os.ReadFile(rec.DraftPath)
	if err != nil {
		return fmt.Errorf("niwa watch post: reading draft: %w", err)
	}

	client := github.NewAPIClient(resolveGitHubToken())
	// event is fixed in trusted code -- never read from the (untrusted) draft.
	if err := client.CreateReview(ctx, rec.Owner, rec.Repo, rec.Number, string(body), "COMMENT"); err != nil {
		return fmt.Errorf("niwa watch post: posting review: %w", err)
	}
	// The PR was already recorded handled at stage time; keep it idempotent.
	_ = watch.AppendHandled(root, watch.HandledKey(rec.Owner, rec.Repo, rec.Number))
	fmt.Fprintf(cmd.OutOrStdout(), "niwa watch: posted review to %s/%s#%d\n", rec.Owner, rec.Repo, rec.Number)
	return nil
}

// runWatchDiscard drops a staged review without posting.
func runWatchDiscard(cmd *cobra.Command, args []string) error {
	handle := args[0]
	root, err := workspaceRootFromCwd()
	if err != nil {
		return err
	}
	rec, err := watch.LoadStagedRecord(root, handle)
	if err != nil {
		return fmt.Errorf("niwa watch discard: %w", err)
	}
	if err := watch.AppendHandled(root, watch.HandledKey(rec.Owner, rec.Repo, rec.Number)); err != nil {
		return fmt.Errorf("niwa watch discard: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "niwa watch: discarded staged review for %s/%s#%d\n", rec.Owner, rec.Repo, rec.Number)
	return nil
}

// workspaceRootFromCwd resolves the enclosing workspace root or errors.
func workspaceRootFromCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return "", fmt.Errorf("classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return "", fmt.Errorf("not inside a niwa workspace")
	}
	return class.WorkspaceRoot, nil
}

// validateDraftPath rejects a recorded draft path that does not resolve inside
// the workspace root or whose basename is not the expected draft file. The
// record is on disk and could be tampered; this closes a traversal surface
// before the trusted post step reads the file.
func validateDraftPath(root, draftPath string) error {
	clean := filepath.Clean(draftPath)
	rootClean := filepath.Clean(root)
	if !strings.HasPrefix(clean, rootClean+string(os.PathSeparator)) {
		return fmt.Errorf("draft path %q is outside the workspace", draftPath)
	}
	if filepath.Base(clean) != watch.DefaultDraftRelPath {
		return fmt.Errorf("unexpected draft file name %q", filepath.Base(clean))
	}
	return nil
}

// workspaceScope builds the workspace-membership matcher from the discovered
// workspace config: sources with an explicit repo list contribute exact
// owner/repo keys, sources without one contribute their whole org, and per-repo
// overrides with a URL contribute the parsed owner/repo.
func workspaceScope(cwd string) (*watch.WorkspaceScope, error) {
	configPath, _, err := config.Discover(cwd)
	if err != nil {
		return nil, err
	}
	res, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	cfg := res.Config

	var exact, orgs []string
	for _, s := range cfg.Sources {
		if s.Org == "" {
			continue
		}
		if len(s.Repos) == 0 {
			orgs = append(orgs, s.Org)
			continue
		}
		for _, r := range s.Repos {
			exact = append(exact, s.Org+"/"+r)
		}
	}
	for _, ov := range cfg.Repos {
		if owner, repo, ok := ownerRepoFromGitURL(ov.URL); ok {
			exact = append(exact, owner+"/"+repo)
		}
	}
	return watch.NewWorkspaceScope(exact, orgs), nil
}

// ownerRepoFromGitURL parses "owner/repo" from a git remote URL of the form
// git@host:owner/repo(.git) or https://host/owner/repo(.git).
func ownerRepoFromGitURL(u string) (owner, repo string, ok bool) {
	if u == "" {
		return "", "", false
	}
	s := strings.TrimSuffix(u, ".git")
	switch {
	case strings.Contains(s, "://"):
		// scheme://host/owner/repo -- drop scheme and host.
		s = s[strings.Index(s, "://")+3:]
		i := strings.Index(s, "/")
		if i < 0 {
			return "", "", false
		}
		s = s[i+1:]
	case strings.Contains(s, "@"):
		// scp-like: user@host:owner/repo -- drop user@host:.
		s = s[strings.Index(s, "@")+1:]
		i := strings.Index(s, ":")
		if i < 0 {
			return "", "", false
		}
		s = s[i+1:]
	case strings.Contains(s, ":"):
		// host:owner/repo (no user).
		s = s[strings.Index(s, ":")+1:]
	}
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	repo = parts[len(parts)-1]
	owner = parts[len(parts)-2]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
