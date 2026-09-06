package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// The secret this suite plants. Long enough to clear the redactor's
// minimum-fragment-length guard, and distinctive enough that finding it in
// output is unambiguous.
const plantedSecret = "s3cr3t-value-not-for-logs"

// The non-secret sentinel the same script prints. Its presence is the positive
// control: it proves the script ran, its output was captured, and the plumbing
// under test is connected. Without it, "the secret is absent" is satisfied
// just as well by a script that never ran.
const setupSentinel = "SETUP-OUTPUT-REACHED-THE-REPORTER"

// plantSecretEnv writes an env output file into the CLONE directory the
// worktree inherits from, and points the config's env_output at it.
func plantSecretEnv(t *testing.T, cfg *config.WorkspaceConfig, cloneRepoDir, key, value string) {
	t.Helper()
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := key + "=" + value + "\nNODE_ENV=production\n"
	if err := os.WriteFile(filepath.Join(cloneRepoDir, ".env.local"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Workspace.EnvOutput = config.OutputTargets{{Path: ".env.local", Format: "dotenv"}}
}

// declareSecret marks key as secret-capable for the repo, by declaring it in
// the repo's env.secrets table.
func declareSecret(cfg *config.WorkspaceConfig, repo, key string) {
	if cfg.Repos == nil {
		cfg.Repos = map[string]config.RepoOverride{}
	}
	ov := cfg.Repos[repo]
	ov.Env.Secrets.Values = map[string]config.MaybeSecret{key: {Plain: "placeholder"}}
	cfg.Repos[repo] = ov
}

// TestWorktreeSetupOutput_SecretIsScrubbed is the R9 behaviour, written so it
// cannot pass for the wrong reason.
//
// An absence assertion alone is worthless here: "the secret is not in the
// output" is satisfied identically by a working redactor, by a script that
// never ran, by output that was never captured, and by a fixture that never
// planted the secret. Three of those four are bugs in the test.
//
// So the assertions are:
//   - the sentinel IS present -- the script ran and its output reached the
//     reporter;
//   - the secret is NOT present -- the thing under test;
//   - the redaction placeholder IS present -- a substitution actually happened,
//     rather than the value never having been there.
func TestWorktreeSetupOutput_SecretIsScrubbed(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")
	declareSecret(cfg, "app", "API_TOKEN")
	plantSecretEnv(t, cfg, filepath.Join(instanceRoot, "apps", "app"), "API_TOKEN", plantedSecret)

	writeWorktreeSetupScript(t, worktreePath, "01-leak.sh",
		"#!/bin/sh\necho "+setupSentinel+"\necho \"token is $API_TOKEN\"\ncat ./.env.local\n")

	var out strings.Builder
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{Stderr: &out}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	got := out.String()

	// Positive control first: if this fails, every other assertion below is
	// meaningless because nothing ran or nothing was captured.
	if !strings.Contains(got, setupSentinel) {
		t.Fatalf("the setup script's output never reached the reporter, so this test "+
			"cannot say anything about redaction; got:\n%s", got)
	}

	if strings.Contains(got, plantedSecret) {
		t.Errorf("the planted secret appears unredacted in setup output:\n%s", got)
	}

	// And prove a substitution happened rather than the value simply being
	// absent. The redactor replaces registered fragments with a placeholder;
	// finding it is the difference between "scrubbed" and "never there".
	if !strings.Contains(got, "API_TOKEN=***") {
		t.Errorf("no redaction placeholder where the secret was printed, so nothing "+
			"was substituted -- the value may simply never have been there:\n%s", got)
	}
}

// TestWorktreeSetupOutput_UndeclaredValueIsNotScrubbed pins the intersection in
// the other direction.
//
// Without this, a redactor that registered every value in the inherited env
// file would pass the test above while over-scrubbing ordinary configuration --
// the failure Decision 4 names explicitly, where a workspace with
// NODE_ENV=production sees "production" replaced wherever it appears.
//
// The two tests together are what distinguish "the intersection works" from
// "everything got registered".
func TestWorktreeSetupOutput_UndeclaredValueIsNotScrubbed(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")
	declareSecret(cfg, "app", "API_TOKEN")
	plantSecretEnv(t, cfg, filepath.Join(instanceRoot, "apps", "app"), "API_TOKEN", plantedSecret)

	// NODE_ENV is in the same env file and is NOT declared secret.
	writeWorktreeSetupScript(t, worktreePath, "01-config.sh",
		"#!/bin/sh\necho "+setupSentinel+"\necho \"env is production\"\n")

	var out strings.Builder
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{Stderr: &out}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	got := out.String()

	if !strings.Contains(got, setupSentinel) {
		t.Fatalf("the setup script's output never reached the reporter; got:\n%s", got)
	}
	if !strings.Contains(got, "production") {
		t.Errorf("an undeclared configuration value was scrubbed out of setup output. "+
			"The redactor is registering everything in the env file rather than the "+
			"declared secret keys:\n%s", got)
	}
}

// TestWorktreeHookOutput_SecretIsScrubbed is the behavioural twin for the hook
// runner.
//
// Without it, "routes through the same choke point" passes against an
// implementation that hands that choke point a nil redactor -- the routing
// happens and nothing is scrubbed. This was a live gap rather than a
// hypothetical: the hook runner piped its scripts' output straight at the
// writer with no redactor on any surface.
func TestWorktreeHookOutput_SecretIsScrubbed(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	declareSecret(cfg, "app", "API_TOKEN")
	plantSecretEnv(t, cfg, filepath.Join(instanceRoot, "apps", "app"), "API_TOKEN", plantedSecret)

	hooksDir := filepath.Join(configDir, "worktree-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho HOOK-OUTPUT-REACHED-THE-REPORTER\ncat \"$NIWA_WORKTREE_PATH/.env.local\"\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "apply.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{Stderr: &out}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	got := out.String()

	if !strings.Contains(got, "HOOK-OUTPUT-REACHED-THE-REPORTER") {
		t.Fatalf("the hook script's output never reached the reporter, so this test "+
			"cannot say anything about redaction; got:\n%s", got)
	}
	if strings.Contains(got, plantedSecret) {
		t.Errorf("the planted secret appears unredacted in worktree HOOK output:\n%s", got)
	}
}

// TestSecretCapableKeys_CoversEverySlotTheWalkFinds is the both-sides
// enumeration that pins the coupling this design deliberately took on.
//
// The registered set is derived from WalkMaybeSecretSlots, which is maintained
// for vault provider-name validation. That buys exhaustiveness for free, and
// costs a dependency: narrowing that walk for its own reasons would silently
// narrow what gets scrubbed. This test makes such a narrowing break a test
// named for redaction rather than quietly shrink coverage.
func TestSecretCapableKeys_CoversEverySlotTheWalkFinds(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"app": {Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"REPO_SECRET": {Plain: "x"}}},
				Vars:    config.EnvVarsTable{Values: map[string]config.MaybeSecret{"REPO_VAULT_REF": {Plain: "vault://p/k"}}},
			}},
		},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"WS_SECRET": {Plain: "x"}}},
		},
		Session: config.SessionConfig{Env: config.SessionEnvConfig{
			Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"SESSION_REF": {Plain: "vault://p/k"}}},
		}},
	}

	keys := config.SecretCapableKeys(cfg, "app")

	for _, want := range []string{
		"REPO_SECRET",    // declared in a secrets table
		"WS_SECRET",      // workspace-level secrets table
		"REPO_VAULT_REF", // a vault:// reference in a VARS table -- the case
		// that does not go through env.secrets at all, and so is the one most
		// likely to be silently uncovered while everything else is green
		"SESSION_REF", // an agent-neutral slot that is not an env table
	} {
		if !keys[want] {
			t.Errorf("SecretCapableKeys missed %q; the walk it derives from no longer "+
				"reaches that slot, so its value would be printed unscrubbed", want)
		}
	}
}
