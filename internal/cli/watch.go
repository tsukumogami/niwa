package cli

import (
	"context"
	"fmt"
	"io"
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

	// Staged-record GC (runs EACH PASS, before the decision): prune records whose
	// session is dead, and discard records that fail freshness (PR closed/no longer
	// requested, or force-pushed off the dispatched head). This is the record-layer
	// counterpart to reapOpportunistically -- it bounds record-store growth and
	// frees capacity on dismissal, and it runs before liveStagedSessions/Decide so a
	// dead or stale record never inflates the live-count or suppresses a re-fire. A
	// per-record failure is skipped, not fatal: one bad record must not abort the
	// pass (fail-safe), matching the shipped skip-malformed contract.
	requested := requestedIdentities(prs)
	pruneStagedRecords(ctx, cmd.OutOrStdout(), root, client, requested)

	handled, err := watch.LoadHandledSet(root)
	if err != nil {
		return fmt.Errorf("niwa watch: reading handled-set: %w", err)
	}

	// Liveness anchor: for every staged record, ask whether a Claude Code job is
	// still rooted in its instance. A dismissed or crashed-and-reaped session has
	// no live job, so its PR re-fires fresh on a new head; a still-running review
	// suppresses the re-fire (Defer). Keyed by PR identity (HandledIdentity).
	live, err := liveStagedSessions(root)
	if err != nil {
		return fmt.Errorf("niwa watch: reading staged records: %w", err)
	}

	// Confirm the current head of every previously-handled, in-scope PR so the
	// decision can compare it against the last-dispatched SHA. Never-handled PRs
	// need no re-check (they stage fresh regardless of head), so this re-polls
	// only the PRs already in the handled-set. A fetch failure is fail-loud: a
	// broken head re-check must not masquerade as "nothing changed".
	heads, err := currentHeads(ctx, client, prs, scope, handled)
	if err != nil {
		return fmt.Errorf("niwa watch: confirming PR heads: %w", err)
	}

	plans := watch.Decide(prs, scope, handled, live, heads, watch.DefaultPerRunBound)

	staged := 0
	for _, p := range plans {
		id := watch.HandledIdentity(p.PR.Owner, p.PR.Repo, p.PR.Number)
		switch p.Kind {
		case watch.Fresh:
			if err := stageReview(cmd, root, cwd, token, client, p.PR, plan); err != nil {
				// Fail loud; the PR was not recorded handled (see stageReview), so a
				// later run re-attempts it.
				return fmt.Errorf("niwa watch: staging %s/%s#%d: %w", p.PR.Owner, p.PR.Repo, p.PR.Number, err)
			}
			staged++
		case watch.Noop:
			// A legacy unknown-SHA entry adopts the current head without staging, so
			// it only re-fires on genuinely new activity later. A non-legacy Noop
			// (unchanged or unconfirmed head) records nothing.
			if recorded, ok := handled[id]; ok && recorded == "" {
				if head, ok := heads[id]; ok && head != "" {
					if err := watch.AppendHandled(root, p.PR.Owner, p.PR.Repo, p.PR.Number, head); err != nil {
						return fmt.Errorf("niwa watch: adopting head for %s/%s#%d: %w", p.PR.Owner, p.PR.Repo, p.PR.Number, err)
					}
				}
			}
		default:
			// Defer (and the reserved Continue, which routes here until Issue 5):
			// nothing to stage this pass.
		}
	}
	if staged == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "niwa watch: nothing to stage")
	}
	return nil
}

// liveStagedSessions maps each staged PR identity to whether a Claude Code job
// is still rooted in its recorded instance. It reads the staged-record store
// (best-effort per record: a record that fails to load is skipped, not fatal)
// and probes liveness via instanceHasLiveJob against the jobs directory. When
// several records share a PR identity, any live session marks the PR live.
func liveStagedSessions(root string) (map[string]bool, error) {
	handles, err := watch.ListStagedHandles(root)
	if err != nil {
		return nil, err
	}
	jobsDir := defaultJobsDir()
	live := map[string]bool{}
	for _, h := range handles {
		rec, err := watch.LoadStagedRecord(root, h)
		if err != nil {
			continue // a malformed/partial record is not evidence of a live session
		}
		id := watch.HandledIdentity(rec.Owner, rec.Repo, rec.Number)
		if instanceHasLiveJob(jobsDir, rec.InstancePath) {
			live[id] = true
		}
	}
	return live, nil
}

// prFreshnessClient is the subset of the GitHub client the freshness check needs:
// the current head of a PR and the ancestry of the dispatched SHA against it.
// *github.APIClient satisfies it; a test passes a fake so the prune logic is
// exercised without a live server.
type prFreshnessClient interface {
	GetPullHead(ctx context.Context, owner, repo string, number int) (github.PullHead, error)
	CompareCommits(ctx context.Context, owner, repo, base, head string) (github.Ancestry, error)
}

// stagedInstanceLiveFunc is a seam over instanceHasLiveJob so a test can drive the
// prune's liveness branch without staging real Claude Code jobs.
var stagedInstanceLiveFunc = instanceHasLiveJob

// requestedIdentities projects the poll results onto the set of PR identities that
// are still open and still requesting the developer's review. Presence in this set
// is the freshness predicate's "stillRequested" input.
func requestedIdentities(prs []github.PRRef) map[string]bool {
	set := make(map[string]bool, len(prs))
	for _, pr := range prs {
		set[watch.HandledIdentity(pr.Owner, pr.Repo, pr.Number)] = true
	}
	return set
}

// recordAncestry resolves the ancestry of a staged record's dispatched SHA against
// its PR's current head, on the trusted GitHub API. An identical head short-circuits
// to Ancestor without a compare call. Any API error yields AncestryUnknown -- an
// inconclusive result the freshness predicate treats conservatively (keeps the
// review) rather than a fatal error, so a transient hiccup never discards a valid
// staged review nor aborts the pass.
func recordAncestry(ctx context.Context, client prFreshnessClient, rec watch.StagedRecord) github.Ancestry {
	head, err := client.GetPullHead(ctx, rec.Owner, rec.Repo, rec.Number)
	if err != nil {
		return github.AncestryUnknown
	}
	if head.SHA == rec.DispatchedSHA {
		return github.AncestryAncestor // unchanged head: trivially an ancestor
	}
	ancestry, err := client.CompareCommits(ctx, rec.Owner, rec.Repo, rec.DispatchedSHA, head.SHA)
	if err != nil {
		return github.AncestryUnknown
	}
	return ancestry
}

// evalRecordFreshness runs the freshness predicate for one staged record against
// live GitHub state: stillRequested from the poll set, ancestry from the trusted
// compare (only fetched when still requested, since a not-requested record is stale
// regardless of ancestry). It is shared by the watcher-pass prune and the session
// pre-flight subcommand so both apply the identical deterministic check.
func evalRecordFreshness(ctx context.Context, client prFreshnessClient, requested map[string]bool, rec watch.StagedRecord) (ok bool, reason string) {
	id := watch.HandledIdentity(rec.Owner, rec.Repo, rec.Number)
	stillRequested := requested[id]
	ancestry := github.AncestryUnknown
	if stillRequested {
		ancestry = recordAncestry(ctx, client, rec)
	}
	return watch.Freshness(rec, stillRequested, ancestry)
}

// pruneStagedRecords is the staged-record GC: for every staged record it either
// prunes a dead record (no live job in its instance) or evaluates freshness on a
// live one and discards it when stale. In both cases the record is deleted and the
// instance is best-effort destroyed (a dead session's instance is already gone;
// destroy is idempotent). A live+fresh record is kept untouched.
//
// It never aborts the pass: a record that fails to load is skipped (a
// malformed/partial record is not evidence of anything), and a delete/destroy error
// is reported but does not stop the sweep -- one bad record must not block pruning
// the rest, matching the shipped skip-malformed contract. It is best-effort by
// design (the next pass re-runs it), so it returns nothing.
func pruneStagedRecords(ctx context.Context, out io.Writer, root string, client prFreshnessClient, requested map[string]bool) {
	handles, err := watch.ListStagedHandles(root)
	if err != nil {
		// Listing failed (e.g. an unreadable store): nothing to prune this pass, and
		// the decision layer's own load is fail-loud, so stay quiet here.
		return
	}
	jobsDir := defaultJobsDir()
	for _, h := range handles {
		rec, err := watch.LoadStagedRecord(root, h)
		if err != nil {
			continue // skip-and-continue: a bad record is not fatal to the pass
		}

		if !stagedInstanceLiveFunc(jobsDir, rec.InstancePath) {
			discardStagedRecord(out, root, h, rec, "session no longer live")
			continue
		}

		if ok, reason := evalRecordFreshness(ctx, client, requested, rec); !ok {
			discardStagedRecord(out, root, h, rec, reason)
		}
		// live + fresh: keep the record and its session untouched.
	}
}

// discardStagedRecord tears down one staged review: best-effort destroy the
// instance (so a stale session does not linger) then delete the record. The reason
// is printed so the operator sees which condition retired the review.
func discardStagedRecord(out io.Writer, root, handle string, rec watch.StagedRecord, reason string) {
	fmt.Fprintf(out, "niwa watch: discarding staged review for %s/%s#%d (handle %s): %s\n",
		rec.Owner, rec.Repo, rec.Number, handle, reason)
	if rec.InstancePath != "" {
		_ = destroyInstanceFunc(rec.InstancePath) // best-effort: a dead instance is already gone
	}
	if err := watch.DeleteStagedRecord(root, handle); err != nil {
		fmt.Fprintf(out, "niwa watch: warning: could not delete staged record %s: %v\n", handle, err)
	}
}

// currentHeads fetches the current head SHA of every previously-handled, in-scope
// PR so Decide can compare it against the last-dispatched SHA. It re-checks only
// PRs already in the handled-set: a never-handled PR stages fresh regardless of
// its head, so re-polling it here would be wasted work. The returned map is keyed
// by PR identity (HandledIdentity). A fetch failure is returned as an error so the
// pass fails loud rather than treating an unconfirmed head as unchanged.
func currentHeads(ctx context.Context, client *github.APIClient, prs []github.PRRef, scope *watch.WorkspaceScope, handled map[string]string) (map[string]string, error) {
	heads := map[string]string{}
	for _, pr := range prs {
		if !scope.Contains(pr.Owner, pr.Repo) {
			continue
		}
		id := watch.HandledIdentity(pr.Owner, pr.Repo, pr.Number)
		if _, ok := handled[id]; !ok {
			continue // never handled: Decide stages fresh without a head comparison
		}
		if _, done := heads[id]; done {
			continue // the same PR can appear once per page; fetch its head once
		}
		head, err := client.GetPullHead(ctx, pr.Owner, pr.Repo, pr.Number)
		if err != nil {
			return nil, fmt.Errorf("%s/%s#%d: %w", pr.Owner, pr.Repo, pr.Number, err)
		}
		heads[id] = head.SHA
	}
	return heads, nil
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
		Handle:        slug,
		Owner:         pr.Owner,
		Repo:          pr.Repo,
		Number:        pr.Number,
		URL:           pr.URL,
		DraftPath:     filepath.Join(instancePath, watch.DefaultDraftRelPath),
		InstancePath:  instancePath,
		DispatchedSHA: head.SHA,
	}
	if err := watch.SaveStagedRecord(root, rec); err != nil {
		return err
	}
	if err := watch.AppendHandled(root, pr.Owner, pr.Repo, pr.Number, head.SHA); err != nil {
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
