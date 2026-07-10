package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

var watchOnce bool

func init() {
	watchCmd.Flags().BoolVar(&watchOnce, "once", false, "perform exactly one poll-and-dispatch pass and exit (the only supported mode)")
	rootCmd.AddCommand(watchCmd)
}

// containmentPlan is the resolved posture for a run, derived from the two global
// switches (design Decision 7). contained is watch_containment == "on";
// applySandbox is true only when the OS no-egress sandbox will actually be
// enabled for the dispatched session (containment on, and sandbox required, or
// optional and available on this host).
type containmentPlan struct {
	contained    bool
	applySandbox bool
}

// resolveContainmentSwitches reads the two switches from the global config and
// validates them. Defaults are the safe posture: containment "on", sandbox
// "required". An unrecognized value is a hard error, never silently coerced.
func resolveContainmentSwitches(gc *config.GlobalConfig) (containment, sandbox string, err error) {
	containment, sandbox = "on", "required"
	if gc != nil {
		if v := gc.Global.WatchContainment; v != "" {
			containment = v
		}
		if v := gc.Global.WatchSandbox; v != "" {
			sandbox = v
		}
	}
	switch containment {
	case "on", "off":
	default:
		return "", "", fmt.Errorf("invalid watch_containment %q (want on | off)", containment)
	}
	switch sandbox {
	case "required", "optional", "disabled":
	default:
		return "", "", fmt.Errorf("invalid watch_sandbox %q (want required | optional | disabled)", sandbox)
	}
	return containment, sandbox, nil
}

// resolveContainmentPlan walks the containment matrix for the resolved switch
// values and, where the sandbox is in play, runs the capability probe. It
// returns an error only in the on+required+unenforceable cell (fail-closed
// refusal); the on+optional-unavailable cell logs a notice and proceeds
// contained without the OS sandbox.
func resolveContainmentPlan(ctx context.Context, cmd *cobra.Command, containment, sandbox string) (containmentPlan, error) {
	if containment == "off" {
		return containmentPlan{contained: false, applySandbox: false}, nil
	}
	switch sandbox {
	case "disabled":
		return containmentPlan{contained: true, applySandbox: false}, nil
	case "required":
		if err := sandboxCapabilityCheck(ctx); err != nil {
			return containmentPlan{}, fmt.Errorf("refusing to dispatch: watch_sandbox=required but the OS sandbox cannot be enforced here: %w\n"+
				"  run `niwa setup-sandbox` once to enable it, or set watch_sandbox to optional (proceed contained without it) or disabled", err)
		}
		return containmentPlan{contained: true, applySandbox: true}, nil
	default: // "optional"
		if err := sandboxCapabilityCheck(ctx); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"niwa watch: notice -- OS sandbox unavailable (%v); proceeding contained without it (watch_sandbox=optional).\n", err)
			return containmentPlan{contained: true, applySandbox: false}, nil
		}
		return containmentPlan{contained: true, applySandbox: true}, nil
	}
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stage review agents for PRs you were directly requested on",
	Long: `watch --once performs a single poll-and-dispatch pass: it finds open PRs
on GitHub where you are the directly-requested reviewer (intersected with the
repos in your niwa workspace), and for each new one dispatches a review agent
that reads the PR in an isolated clone and drafts a review.

Containment is governed by two global settings. watch_containment (on by
default) applies a credential-scrubbed environment, a synthetic HOME, and a
fail-closed permission mode -- so a hostile PR cannot exfiltrate or act -- and
the agent only drafts; you post the draft from your own session. watch_sandbox
(required by default; optional | disabled) governs the OS no-egress sandbox
under containment: required refuses if it cannot be enforced, optional proceeds
without it, disabled never attempts it. With watch_containment = off the agent
runs as an ordinary dispatch with your real credentials and posts the review
itself.

It is a stateless single-shot verb -- no daemon, no resident process. A staged
session you no longer want is dismissed from the Claude Code agents view.`,
	RunE: runWatchOnce,
}

// runWatchOnce is the single poll-and-dispatch pass. It is fail-closed (refuses
// to dispatch where containment cannot be enforced) and fail-loud (reports and
// exits non-zero on a poll/dispatch failure, recording nothing it could not
// stage safely).
func runWatchOnce(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// (1) Preflight: resolve the containment posture from the two global switches
	// (design Decision 7) BEFORE creating any instance. This refuses only in the
	// on+required+unenforceable cell; every other cell yields a plan the per-PR
	// stage applies. A capability probe runs here (not per PR) when the sandbox
	// is in play.
	gc, _ := config.LoadGlobalConfig()
	containment, sandbox, err := resolveContainmentSwitches(gc)
	if err != nil {
		return fmt.Errorf("niwa watch: %w", err)
	}
	plan, err := resolveContainmentPlan(ctx, cmd, containment, sandbox)
	if err != nil {
		return fmt.Errorf("niwa watch: %w", err)
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
		if err := stageReview(cmd, root, cwd, token, client, pr, plan); err != nil {
			// Fail loud; the PR was not recorded handled (see stageReview), so a
			// later run re-attempts it.
			return fmt.Errorf("niwa watch: staging %s/%s#%d: %w", pr.Owner, pr.Repo, pr.Number, err)
		}
	}
	return nil
}

// stageReview provisions an instance, fetches the PR head as inert data, and
// launches a detached review agent under the resolved containment plan. When
// contained it applies the containment bundle (scrubbed env, synthetic HOME,
// fail-closed mode, and the OS sandbox when plan.applySandbox) and the agent
// only drafts; when uncontained it dispatches with the developer's real
// environment and the agent posts the review itself. The handled-set and the
// staged-review record are written ONLY on success.
func stageReview(cmd *cobra.Command, root, cwd, token string, client *github.APIClient, pr github.PRRef, plan containmentPlan) error {
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

	// Build the launch posture from the plan. Contained: scrubbed env + synthetic
	// HOME + the containment settings (with the OS sandbox when applySandbox), and
	// the agent only drafts. Uncontained: nil env (the launcher uses os.Environ(),
	// the developer's real credentials) and the agent posts the review itself.
	var env []string
	if plan.contained {
		synthHome, err := watch.SyntheticHomeDir(instancePath)
		if err != nil {
			return err
		}
		if err := watch.ApplyContainment(instancePath, plan.applySandbox); err != nil {
			return err
		}
		env = watch.BuildContainedEnv(os.Environ(), synthHome)
	}

	prompt := watch.BuildReviewPrompt(pr, watch.DefaultCloneRelDir, watch.DefaultDraftRelPath, !plan.contained)
	passthrough := buildDispatchPassthrough(slug, "")

	// Launch detached (no terminal attach). env is nil for an uncontained run.
	if err := dispatchLaunch(ctx, instancePath, prompt, passthrough, env); err != nil {
		return fmt.Errorf("launching review agent: %w", err)
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

// sandboxCapabilityCheck is the preflight capability probe (a seam so tests can
// substitute it). Production wires it to checkSandboxCapability.
var sandboxCapabilityCheck = checkSandboxCapability

// checkSandboxCapability actively verifies that the Claude Code OS sandbox can
// be enforced on this host right now, and returns a descriptive error (never
// nil-on-doubt) when it cannot. This is Decision 7's active probe: it catches
// the silent-degradation cases the design refuses to tolerate -- a missing
// sandbox backend or dependency, or a kernel/container that cannot create the
// network namespace -- so `watch --once` never dispatches uncontained on a
// non-Windows-but-incapable host.
func checkSandboxCapability(ctx context.Context) error {
	switch runtime.GOOS {
	case "linux":
		// The sandbox backend and the dependency the harness itself requires
		// (and names when it disables the sandbox): bubblewrap + socat.
		if _, err := exec.LookPath("bwrap"); err != nil {
			return fmt.Errorf("the OS sandbox backend 'bwrap' (bubblewrap) is not on PATH; this host cannot enforce no-egress")
		}
		if _, err := exec.LookPath("socat"); err != nil {
			return fmt.Errorf("the OS sandbox dependency 'socat' is not on PATH; the harness cannot enable the sandbox without it")
		}
		// Functional probe: can a network-isolated namespace actually be created
		// here? On a locked-down/nested container that denies user namespaces,
		// bwrap exits non-zero and we refuse.
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(pctx, "bwrap", "--ro-bind", "/", "/", "--unshare-net", "--die-with-parent", "true")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("the OS sandbox cannot create a network namespace on this host (%v); it cannot enforce no-egress", err)
		}
		return nil
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return fmt.Errorf("the macOS sandbox tool 'sandbox-exec' is not on PATH; this host cannot enforce no-egress")
		}
		return nil
	default:
		return fmt.Errorf("the OS sandbox is unavailable on GOOS=%s (Linux, macOS, and WSL2 are supported)", runtime.GOOS)
	}
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
