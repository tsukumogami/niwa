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

// reviewPlan is the resolved posture for a run. The dispatched review session
// always runs under the developer's real HOME, environment, and Claude daemon;
// sandbox records whether the OS no-egress sandbox is applied to it.
type reviewPlan struct {
	sandbox bool
}

// posture is the human-readable contract the run operates under, surfaced once
// per run so the operator always knows which guarantees are in force.
func (p reviewPlan) posture() string {
	if p.sandbox {
		return "sandboxed (OS no-egress boundary; agent drafts and waits, you submit)"
	}
	return "uncontained (trusted, no sandbox; agent drafts and waits, you submit)"
}

// resolveSandboxMode reads the single watch_sandbox switch from the global config
// and validates it. The default is the safe posture "required". An unrecognized
// value is a hard error, never silently coerced.
func resolveSandboxMode(gc *config.GlobalConfig) (string, error) {
	mode := "required"
	if gc != nil {
		if v := gc.Global.WatchSandbox; v != "" {
			mode = v
		}
	}
	switch mode {
	case "required", "off":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid watch_sandbox %q (want required | off)", mode)
	}
}

// resolveReviewPlan turns the resolved sandbox mode into a plan. "off" returns a
// no-sandbox plan (the trusted path). "required" runs the capability probe and
// returns a sandboxed plan on success, or a fail-closed refusal when the OS
// sandbox cannot be enforced on this host.
func resolveReviewPlan(ctx context.Context, mode string) (reviewPlan, error) {
	if mode == "off" {
		return reviewPlan{sandbox: false}, nil
	}
	if err := sandboxCapabilityCheck(ctx); err != nil {
		return reviewPlan{}, fmt.Errorf("refusing to dispatch: watch_sandbox=required but the OS sandbox cannot be enforced here: %w\n"+
			"  run `niwa setup-sandbox` once to enable it, or set watch_sandbox=off (dispatch with no sandbox)", err)
	}
	return reviewPlan{sandbox: true}, nil
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stage review agents for PRs you were directly requested on",
	Long: `watch --once performs a single poll-and-dispatch pass: it finds open PRs
on GitHub where you are the directly-requested reviewer (intersected with the
repos in your niwa workspace), and for each new one dispatches a review agent
that reads the PR in an isolated clone and drafts a review.

In every mode the agent only drafts a review and waits -- it never posts;
you read the draft and submit it yourself. The review session always runs under
your real HOME, environment, and Claude daemon, so it can read the linked issue,
CI, and threads a good review needs. Containment is governed by a single global
setting. watch_sandbox (required by default) applies the OS no-egress sandbox to
the dispatched session and refuses to dispatch when it cannot be enforced on this
host; watch_sandbox = off dispatches with no sandbox (the trusted path). Each run
reports the posture it is operating under.

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

	// (1) Preflight: resolve the sandbox posture from the single global switch
	// BEFORE creating any instance. This refuses in the required-but-unenforceable
	// case; otherwise it yields a plan the per-PR stage applies. A capability probe
	// runs here (not per PR) when the sandbox is required.
	gc, _ := config.LoadGlobalConfig()
	mode, err := resolveSandboxMode(gc)
	if err != nil {
		return fmt.Errorf("niwa watch: %w", err)
	}
	plan, err := resolveReviewPlan(ctx, mode)
	if err != nil {
		return fmt.Errorf("niwa watch: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "niwa watch: %s\n", plan.posture())
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
// launches a detached review agent under the resolved plan. The session always
// runs under the developer's real environment and Claude daemon so the agent can
// read the surrounding context; when plan.sandbox is true the OS no-egress
// sandbox stanza is applied. In both cases the post-guard ask rule is applied and
// the agent only drafts and waits -- it never posts. The handled-set and the
// staged-review record are written ONLY on success.
func stageReview(cmd *cobra.Command, root, cwd, token string, client *github.APIClient, pr github.PRRef, plan reviewPlan) error {
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
	askPosture := false
	defer func() {
		if !success {
			if askPosture {
				// Undo the trust seed for an instance we are about to destroy.
				_ = removeInstanceTrustFunc(instancePath)
			}
			_ = destroyInstanceFunc(instancePath)
		}
	}()

	// Fetch the PR head as inert data (hardened) into the clone subdir.
	cloneDir := filepath.Join(instancePath, watch.DefaultCloneRelDir)
	if err := watch.FetchPRHead(ctx, cloneURL, head.SHA, cloneDir, token); err != nil {
		return fmt.Errorf("fetching PR head: %w", err)
	}

	// Resolve the out-of-instance-write posture for a sandboxed review. The
	// operator-approval (ask) posture requires a trusted workspace and an instance
	// outside ~/.claude; resolveAskPosture seeds workspace trust when it can guarantee
	// both, and returns false (the shipped hard-deny fallback) otherwise.
	askPosture = resolveAskPosture(instancePath, plan.sandbox)

	// Apply the review-session settings: always the post-guard rule, plus the OS
	// no-egress sandbox stanza when plan.sandbox. When askPosture is on, the sandboxed
	// session runs under a non-bypass permission mode so an out-of-instance write
	// surfaces an operator approval instead of a hard deny. The session launches with a
	// nil env override, so the launcher uses os.Environ() -- the developer's real
	// environment and Claude daemon. The agent only ever drafts and waits.
	if err := watch.ApplyReviewSettings(instancePath, plan.sandbox, askPosture); err != nil {
		return err
	}

	prompt := watch.BuildReviewPrompt(pr, watch.DefaultCloneRelDir, watch.DefaultDraftRelPath)
	passthrough := buildDispatchPassthrough(slug, "")
	if plan.sandbox {
		// Belt-and-suspenders: reduce MCP server loading so the egress-deny hook
		// is not the only thing standing between an MCP tool and the network.
		passthrough = append(passthrough, "--strict-mcp-config")
	}

	// Launch detached (no terminal attach) with the real environment.
	if err := dispatchLaunch(ctx, instancePath, prompt, passthrough, nil); err != nil {
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
		"niwa watch: staged review for %s/%s#%d (handle %s)%s\n",
		pr.Owner, pr.Repo, pr.Number, slug, reviewWritePosture(plan.sandbox, askPosture))
	return nil
}

// reviewWritePosture returns a human-readable suffix naming how an out-of-instance
// write is handled, so the operator knows whether an anomalous write surfaces an
// approval or is hard-denied. It is empty for a non-sandboxed (trusted) review.
func reviewWritePosture(sandbox, ask bool) string {
	if !sandbox {
		return ""
	}
	if ask {
		return " -- out-of-instance writes surface an operator approval"
	}
	return " -- out-of-instance writes are hard-denied"
}

// ensureInstanceTrustedFunc / removeInstanceTrustFunc / reviewHomeDir are seams so a
// test can drive resolveAskPosture without touching the real ~/.claude.json or HOME.
var (
	ensureInstanceTrustedFunc = watch.EnsureInstanceTrusted
	removeInstanceTrustFunc   = watch.RemoveInstanceTrust
	reviewHomeDir             = os.UserHomeDir
)

// resolveAskPosture reports whether the operator-approval (ask) posture can be used
// for a sandboxed review instance, seeding workspace trust as a side effect when it
// can. It returns false -- the shipped hard-deny fallback -- when the sandbox is off,
// HOME cannot be resolved, the instance lives under ~/.claude (a location Claude Code
// protects independently of mode/trust/hooks), or trust cannot be seeded. The ask
// posture is never entered without a guaranteed trusted workspace, so a fallback never
// fails open relative to the shipped deny.
func resolveAskPosture(instancePath string, sandbox bool) bool {
	if !sandbox {
		return false
	}
	home, err := reviewHomeDir()
	if err != nil || home == "" {
		return false
	}
	abs, err := filepath.Abs(instancePath)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	claudeHome := filepath.Clean(filepath.Join(home, ".claude"))
	if abs == claudeHome || strings.HasPrefix(abs, claudeHome+string(os.PathSeparator)) {
		// Under ~/.claude: the sensitive-location protection would block the review's
		// own in-instance writes regardless of trust, so autonomy cannot be guaranteed.
		return false
	}
	if err := ensureInstanceTrustedFunc(instancePath); err != nil {
		return false
	}
	return true
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
