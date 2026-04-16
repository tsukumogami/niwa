package guardrail

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

// initGitRepo runs `git init` in dir and returns the path. The helper
// scrubs global/system git config so host-level configuration (e.g., a
// default `init.defaultBranch = trunk`) cannot change the tested
// behavior. We don't care about the branch name here — the guardrail
// only enumerates remotes — but the scrub keeps these tests hermetic.
func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	runGit(t, dir, "init")
	return dir
}

// addRemote adds a named remote with the given URL to a git repo under
// test. It's a thin wrapper that fails the test loudly if git rejects
// the URL shape (e.g., a typo in a test fixture).
func addRemote(t *testing.T, dir, name, url string) {
	t.Helper()
	runGit(t, dir, "remote", "add", name, url)
}

// runGit invokes git with a scrubbed environment so host git config
// cannot affect the result. `GIT_CONFIG_GLOBAL=/dev/null` is the
// portable way to tell git to ignore ~/.gitconfig; the SYSTEM variant
// handles /etc/gitconfig for paranoid CI environments.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newCfgWithPlaintextSecret builds a minimal WorkspaceConfig where
// [env.secrets].key is a plaintext value. Enough surface to drive the
// guardrail without touching any unrelated parser or resolver paths.
func newCfgWithPlaintextSecret(key, val string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					key: {Plain: val},
				},
			},
		},
	}
}

// newCfgWithResolvedSecret returns a config where the single
// [env.secrets] entry has already been promoted through the resolver
// (IsSecret() == true). The guardrail must skip these entries — they
// live in a *.secrets table but are backed by a vault reference, which
// is the desired end state per PRD R14.
func newCfgWithResolvedSecret(key, val string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					key: {
						Plain:  "vault://" + key,
						Secret: secret.New([]byte(val), secret.Origin{Key: key}),
					},
				},
			},
		},
	}
}

func TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic(t *testing.T) {
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "git@gitlab.com:foo/bar.git")
	addRemote(t, dir, "upstream", "https://github.com/acme/tools.git")

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer

	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err == nil {
		t.Fatalf("expected guardrail error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "https://github.com/acme/tools.git") {
		t.Errorf("error did not name upstream remote; got: %s", msg)
	}
	if strings.Contains(msg, "gitlab.com") {
		t.Errorf("error should not name non-GitHub remote; got: %s", msg)
	}
	if !strings.Contains(msg, "API_KEY") {
		t.Errorf("error did not name offending key; got: %s", msg)
	}
	if strings.Contains(msg, "abcd1234") {
		t.Errorf("error leaked secret value; got: %s", msg)
	}
	if !strings.Contains(msg, "vault://") {
		t.Errorf("error should point at vault migration; got: %s", msg)
	}
	if !strings.Contains(msg, "--allow-plaintext-secrets") {
		t.Errorf("error should mention escape hatch flag; got: %s", msg)
	}
}

func TestCheckGitHubPublicRemoteSecretsAllowsPlaintextOneShot(t *testing.T) {
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer

	err := CheckGitHubPublicRemoteSecrets(dir, cfg, true, &stderr)
	if err != nil {
		t.Fatalf("expected nil error when flag is set, got: %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "warning:") {
		t.Errorf("expected loud stderr warning; got: %s", got)
	}
	if !strings.Contains(got, "API_KEY") {
		t.Errorf("warning did not name offending key; got: %s", got)
	}
	if !strings.Contains(got, "https://github.com/acme/tools.git") {
		t.Errorf("warning did not name public remote; got: %s", got)
	}
	if strings.Contains(got, "abcd1234") {
		t.Errorf("warning leaked secret value; got: %s", got)
	}
	if !strings.Contains(got, "one-shot") {
		t.Errorf("warning should note one-shot semantics; got: %s", got)
	}
}

func TestCheckGitHubPublicRemoteSecretsNoGitTree(t *testing.T) {
	// Scratch directory with no .git at all. The git subprocess will
	// exit non-zero; the guardrail must emit a warning and return nil.
	dir := t.TempDir()

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer

	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err != nil {
		t.Fatalf("expected nil error on missing git tree, got: %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "warning") {
		t.Errorf("expected stderr warning; got: %s", got)
	}
	if !strings.Contains(got, "guardrail skipped") {
		t.Errorf("warning should note the guardrail was skipped; got: %s", got)
	}
}

func TestCheckGitHubPublicRemoteSecretsOnlyWalksSecretsTables(t *testing.T) {
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	// Plaintext in BOTH env.vars and env.secrets. Only the secrets
	// entry should be flagged; the vars entry is non-secret class by
	// construction and must never trigger the guardrail.
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Vars: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"LOG_LEVEL": {Plain: "debug"},
				},
			},
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"API_KEY": {Plain: "abcd1234"},
				},
			},
		},
	}
	var stderr bytes.Buffer

	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err == nil {
		t.Fatal("expected guardrail error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "API_KEY") {
		t.Errorf("error did not name secrets-table key; got: %s", msg)
	}
	if strings.Contains(msg, "LOG_LEVEL") {
		t.Errorf("error must not flag env.vars key; got: %s", msg)
	}
}

func TestCheckGitHubPublicRemoteSecretsGithubEnterpriseNotFlagged(t *testing.T) {
	// github.mycorp.com is a GHE host (R-deferred). Bitbucket and
	// GitLab are also deferred. None should match.
	cases := []string{
		"https://github.mycorp.com/acme/tools.git",
		"git@github.mycorp.com:acme/tools.git",
		"https://gitlab.com/acme/tools.git",
		"git@gitlab.com:acme/tools.git",
		"https://bitbucket.org/acme/tools.git",
		"git@bitbucket.org:acme/tools.git",
	}

	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			dir := initGitRepo(t, t.TempDir())
			addRemote(t, dir, "origin", url)

			cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
			var stderr bytes.Buffer
			err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
			if err != nil {
				t.Fatalf("non-github remote %s must not trigger guardrail; got: %v", url, err)
			}
		})
	}
}

func TestCheckGitHubPublicRemoteSecretsGithubComCaseInsensitive(t *testing.T) {
	// The hostname match is case-insensitive — GitHub.com and
	// GITHUB.COM are the same host to git, and users typing one or
	// the other shouldn't accidentally escape the guardrail.
	cases := []string{
		"https://GitHub.com/acme/tools.git",
		"https://GITHUB.COM/acme/tools.git",
		"git@GitHub.com:acme/tools.git",
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			dir := initGitRepo(t, t.TempDir())
			addRemote(t, dir, "origin", url)

			cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
			var stderr bytes.Buffer
			err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
			if err == nil {
				t.Fatalf("case-variant GitHub URL %s must trigger guardrail", url)
			}
		})
	}
}

func TestCheckGitHubPublicRemoteSecretsOneShotReevaluates(t *testing.T) {
	// A successful apply with --allow-plaintext-secrets must not
	// persist anywhere. Simulate two sequential apply invocations on
	// the same config and assert both evaluate the guardrail from
	// scratch.
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")

	// First call: flag on, expect pass with warning.
	var stderr1 bytes.Buffer
	if err := CheckGitHubPublicRemoteSecrets(dir, cfg, true, &stderr1); err != nil {
		t.Fatalf("first call with flag must succeed, got: %v", err)
	}
	if !strings.Contains(stderr1.String(), "warning:") {
		t.Errorf("first call should emit warning; got: %s", stderr1.String())
	}

	// Second call: flag off, expect the same error the first call
	// would have produced. No state may have been written on disk or
	// in a process-level map.
	var stderr2 bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr2)
	if err == nil {
		t.Fatal("second call without flag must re-fire the guardrail (one-shot contract)")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("re-evaluated error must still name the key; got: %s", err.Error())
	}
}

func TestCheckGitHubPublicRemoteSecretsNoSecretsClean(t *testing.T) {
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	// All-vault-refs config: the secret field is populated (IsSecret()
	// true). This is the target state the guardrail is pushing users
	// toward, so it must not fire.
	cfg := newCfgWithResolvedSecret("API_KEY", "resolved-value")

	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err != nil {
		t.Fatalf("clean all-vault config must pass, got: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("clean config must not emit stderr noise; got: %s", stderr.String())
	}
}

func TestCheckGitHubPublicRemoteSecretsNoRemotesSkipped(t *testing.T) {
	// git-init'd repo with no remotes at all. Per spec, no remotes is
	// treated the same as no git tree — the guardrail can't make a
	// ruling and emits a skipped warning.
	dir := initGitRepo(t, t.TempDir())

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err != nil {
		t.Fatalf("no remotes must not produce an error, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "guardrail skipped") {
		t.Errorf("expected 'guardrail skipped' warning; got: %s", stderr.String())
	}
}

func TestCheckGitHubPublicRemoteSecretsNoPublicRemotesClean(t *testing.T) {
	// All remotes are private (non-github). Even with plaintext
	// secrets, the guardrail must not fire — the exposure threat is
	// specifically "a public host will index these bytes".
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "git@gitlab.com:foo/bar.git")

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err != nil {
		t.Fatalf("no public remotes must pass even with plaintext, got: %v", err)
	}
}

func TestCheckGitHubPublicRemoteSecretsWalksClaudeEnvSecrets(t *testing.T) {
	// Parallel to env.secrets: claude.env.secrets also carries
	// secret-class values and must be walked.
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"CLAUDE_KEY": {Plain: "zzz"},
					},
				},
			},
		},
	}
	var stderr bytes.Buffer

	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err == nil {
		t.Fatal("expected guardrail error for claude.env.secrets plaintext")
	}
	if !strings.Contains(err.Error(), "CLAUDE_KEY") {
		t.Errorf("error did not name claude.env.secrets key; got: %s", err.Error())
	}
}

func TestCheckGitHubPublicRemoteSecretsWalksRepoAndInstanceOverrides(t *testing.T) {
	// Issue 3 added Env.Secrets to RepoOverride and InstanceConfig.
	// Plaintext at either of those override layers must be caught by
	// the same guardrail.
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Repos: map[string]config.RepoOverride{
			"r1": {
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{
							"REPO_SECRET": {Plain: "r"},
						},
					},
				},
			},
		},
		Instance: config.InstanceConfig{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"INSTANCE_SECRET": {Plain: "i"},
					},
				},
			},
		},
	}
	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err == nil {
		t.Fatal("expected guardrail to catch repo/instance overrides")
	}
	msg := err.Error()
	if !strings.Contains(msg, "REPO_SECRET") {
		t.Errorf("error did not name repo override key; got: %s", msg)
	}
	if !strings.Contains(msg, "INSTANCE_SECRET") {
		t.Errorf("error did not name instance override key; got: %s", msg)
	}
}

func TestCheckGitHubPublicRemoteSecretsDeduplicatesRemotes(t *testing.T) {
	// A single remote appears in `git remote -v` twice (fetch + push).
	// The error message must name each URL exactly once, not duplicate
	// it. Regression guard for a trivial set-inclusion bug.
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	cfg := newCfgWithPlaintextSecret("API_KEY", "abcd1234")
	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err == nil {
		t.Fatal("expected guardrail error")
	}
	msg := err.Error()
	if strings.Count(msg, "https://github.com/acme/tools.git") != 1 {
		t.Errorf("expected remote URL named exactly once, got: %s", msg)
	}
}

// TestCheckGitHubPublicRemoteSecretsUnresolvedVaultURINotFlagged guards
// the pre-resolve state: before the resolver runs, a vault-ref config
// slot looks like {Plain: "vault://team/token", Secret: zero}. That is
// the correct way to reference a secret, and the guardrail must not
// block it. This is the regression for a bug where the old offendingKeys
// check treated any non-empty Plain with !IsSecret() as offending,
// incorrectly flagging the very vault references the error message
// directs users to adopt.
func TestCheckGitHubPublicRemoteSecretsUnresolvedVaultURINotFlagged(t *testing.T) {
	dir := initGitRepo(t, t.TempDir())
	addRemote(t, dir, "origin", "https://github.com/acme/tools.git")

	// Pre-resolve state: Plain holds the vault URI, Secret is zero.
	// !IsSecret() AND Plain != "" — the exact shape the old check
	// mistakenly flagged as plaintext.
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"TOKEN": {Plain: "vault://team/token"},
				},
			},
		},
	}
	var stderr bytes.Buffer
	err := CheckGitHubPublicRemoteSecrets(dir, cfg, false, &stderr)
	if err != nil {
		t.Fatalf("unresolved vault:// URI must pass the guardrail, got: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("clean vault-ref config must not emit stderr noise; got: %s", stderr.String())
	}
}

// TestOffendingKeysClassification exercises isPlaintextSecret directly
// across the four input shapes a MaybeSecret can take at guardrail time.
// Keeping these in one table clarifies the classification contract and
// locks it down against drift in either the resolver or the parser.
func TestOffendingKeysClassification(t *testing.T) {
	cases := []struct {
		name    string
		value   config.MaybeSecret
		flagged bool
	}{
		{
			name:    "empty slot",
			value:   config.MaybeSecret{},
			flagged: false,
		},
		{
			name:    "plaintext literal",
			value:   config.MaybeSecret{Plain: "ghp_ACTUAL_TOKEN"},
			flagged: true,
		},
		{
			name:    "unresolved vault URI",
			value:   config.MaybeSecret{Plain: "vault://team/token"},
			flagged: false,
		},
		{
			name: "resolved secret",
			value: config.MaybeSecret{
				Plain:  "vault://team/token",
				Secret: secret.New([]byte("v"), secret.Origin{Key: "TOKEN"}),
			},
			flagged: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "test"},
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{
						Values: map[string]config.MaybeSecret{
							"TOKEN": tc.value,
						},
					},
				},
			}
			got := offendingKeys(cfg)
			if tc.flagged && len(got) == 0 {
				t.Errorf("expected TOKEN to be flagged, got no offenders")
			}
			if !tc.flagged && len(got) != 0 {
				t.Errorf("expected no offenders, got: %v", got)
			}
		})
	}
}

// TestOffendingKeysIgnoresDescriptionSubtables asserts that the
// requirement-description maps (.required, .recommended, .optional)
// never show up as offenders. They are key→description strings, not
// secret values, and must be ignored by the guardrail even when the
// containing *.secrets table has descriptions declared.
func TestOffendingKeysIgnoresDescriptionSubtables(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Required: map[string]string{
					"GH_TOKEN": "GitHub token used by niwa apply",
				},
				Recommended: map[string]string{
					"SLACK_TOKEN": "Optional Slack alert token",
				},
				Optional: map[string]string{
					"DEBUG_FLAG": "Toggle verbose logs",
				},
			},
		},
	}
	got := offendingKeys(cfg)
	if len(got) != 0 {
		t.Errorf("description sub-tables must not be flagged; got: %v", got)
	}
}

// TestEnumerateGitHubRemotesRegexSamples guards the regex surface
// against accidental loosening. Each input is asserted against the
// boolean "is this a public GitHub URL?" classifier that drives the
// guardrail.
func TestEnumerateGitHubRemotesRegexSamples(t *testing.T) {
	cases := []struct {
		url      string
		isGitHub bool
	}{
		{"https://github.com/foo/bar.git", true},
		{"https://github.com/foo/bar", true},
		{"https://github.com/foo/bar/", true},
		{"http://github.com/foo/bar.git", true},
		{"git@github.com:foo/bar.git", true},
		{"git@github.com:foo/bar", true},
		{"https://GITHUB.COM/foo/bar.git", true},
		{"git@GITHUB.COM:foo/bar.git", true},

		{"https://github.mycorp.com/foo/bar.git", false},
		{"https://gitlab.com/foo/bar.git", false},
		{"git@gitlab.com:foo/bar.git", false},
		{"https://bitbucket.org/foo/bar.git", false},
		{"git@bitbucket.org:foo/bar.git", false},
		{"", false},
		// Malformed/partial — must not match.
		{"github.com/foo/bar", false},
		{"https://github.com/foo", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := isGitHubPublicRemote(tc.url); got != tc.isGitHub {
				t.Errorf("isGitHubPublicRemote(%q) = %v, want %v", tc.url, got, tc.isGitHub)
			}
		})
	}
}

// TestEnumerateGitHubRemotesRejectsUnknownPath asserts that
// enumerateGitHubRemotes reports haveGit=false when the target dir is
// not a git tree. This is the underlying branch that triggers the
// top-level "guardrail skipped" warning.
func TestEnumerateGitHubRemotesRejectsUnknownPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	_, haveGit := enumerateGitHubRemotes(dir)
	if haveGit {
		t.Error("expected haveGit=false for non-existent path")
	}
}
