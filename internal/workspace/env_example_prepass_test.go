package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// newTestWorkspaceConfig builds a minimal WorkspaceConfig suitable for
// env-example pre-pass tests. readEnvExample controls the workspace-level
// read_env_example setting (nil means inherit default = true).
func newTestWorkspaceConfig(readEnvExample *bool) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{
			Name:           "test",
			ReadEnvExample: readEnvExample,
		},
		Repos: map[string]config.RepoOverride{},
	}
}

// makeCtx returns a MaterializeContext wired to repoDir with default workspace
// config (read_env_example unset = enabled).
func makeCtx(repoDir string, ws *config.WorkspaceConfig) *MaterializeContext {
	return &MaterializeContext{
		Config:    ws,
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: repoDir,
		Effective: EffectiveConfig{},
	}
}

// TestEnvExamplePrePassAbsentFile verifies that when there is no .env.example
// file the pre-pass is silent, produces no stderr output, and returns no error.
func TestEnvExamplePrePassAbsentFile(t *testing.T) {
	repoDir := t.TempDir()

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr output, got: %q", stderr.String())
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassSymlink verifies that when .env.example is a symlink
// the pre-pass emits a warning containing "symlink", returns no error, and
// does not set ctx.EnvExampleVars.
func TestEnvExamplePrePassSymlink(t *testing.T) {
	repoDir := t.TempDir()

	// Create a real file to symlink to.
	realFile := filepath.Join(repoDir, "real.env")
	if err := os.WriteFile(realFile, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	symlinkPath := filepath.Join(repoDir, ".env.example")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Errorf("expected stderr to contain %q, got: %q", "symlink", stderr.String())
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars after symlink skip, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassOptOut verifies that when read_env_example=false at the
// workspace level the pre-pass is skipped entirely — no stderr output even if a
// .env.example file exists.
func TestEnvExamplePrePassOptOut(t *testing.T) {
	repoDir := t.TempDir()

	// Write a .env.example with a safe key so the pre-pass would produce output
	// if it ran.
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("DB_HOST=localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(boolPtr(false)))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr when opted out, got: %q", stderr.String())
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars when opted out, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassPerRepoOptOut verifies that when the workspace-level
// setting is enabled (nil) but a per-repo override sets read_env_example=false
// the pre-pass is skipped for that repo.
func TestEnvExamplePrePassPerRepoOptOut(t *testing.T) {
	repoDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("DB_HOST=localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := newTestWorkspaceConfig(nil) // workspace-level: enabled (nil)
	// Set per-repo override to false.
	ws.Repos["myrepo"] = config.RepoOverride{ReadEnvExample: boolPtr(false)}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, ws)

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr for per-repo opt-out, got: %q", stderr.String())
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars for per-repo opt-out, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassSecretsExclusion verifies that keys present in
// env.secrets, claude.env.secrets, or per-repo env.secrets are excluded from
// EnvExampleVars silently (no diagnostic emitted).
func TestEnvExamplePrePassSecretsExclusion(t *testing.T) {
	repoDir := t.TempDir()

	// Write three high-entropy keys; all three should be excluded because they
	// are declared as secrets at different levels. Using a known blocklist prefix
	// so classification would fail if they were not excluded.
	content := "WS_SECRET=sk_live_abcdefghij\nCLAUDE_SECRET=sk_live_klmnopqrst\nREPO_SECRET=sk_live_uvwxyz1234\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := newTestWorkspaceConfig(nil)

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, ws)

	// Declare each key as a secret at a different level.
	ctx.Effective.Env.Secrets.Values = map[string]config.MaybeSecret{
		"WS_SECRET": {Plain: "ws-val"},
	}
	ctx.Effective.Claude.Env.Secrets.Values = map[string]config.MaybeSecret{
		"CLAUDE_SECRET": {Plain: "claude-val"},
	}
	// Per-repo secret via workspace config Repos map.
	ws.Repos["myrepo"] = config.RepoOverride{
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{
				Values: map[string]config.MaybeSecret{
					"REPO_SECRET": {Plain: "repo-val"},
				},
			},
		},
	}

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr for excluded secret keys, got: %q", stderr.String())
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars (all keys excluded), got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassDeclaredVar verifies that a key declared in
// env.vars.values is included in EnvExampleVars even if its value in
// .env.example is high-entropy — declared keys bypass classification.
func TestEnvExamplePrePassDeclaredVar(t *testing.T) {
	repoDir := t.TempDir()

	// High-entropy value that would fail classification if undeclared.
	content := "API_TOKEN=xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	// Declare the key in env.vars.values.
	ctx.Effective.Env.Vars.Values = map[string]config.MaybeSecret{
		"API_TOKEN": {Plain: "override-value"},
	}

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No classification warning should have been emitted.
	if strings.Contains(stderr.String(), "API_TOKEN") {
		t.Errorf("unexpected mention of API_TOKEN in stderr: %q", stderr.String())
	}
	if ctx.EnvExampleVars["API_TOKEN"] != "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f" {
		t.Errorf("EnvExampleVars[API_TOKEN] = %q, want %q", ctx.EnvExampleVars["API_TOKEN"], "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f")
	}
}

// TestEnvExamplePrePassUndeclaredSafe verifies that an undeclared key with a
// safe value emits a warning to stderr naming the key, includes the key in the
// result, and returns no error.
func TestEnvExamplePrePassUndeclaredSafe(t *testing.T) {
	repoDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("DB_HOST=localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "DB_HOST") {
		t.Errorf("expected stderr to mention key DB_HOST, got: %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "safe value") {
		t.Errorf("expected stderr to mention 'safe value', got: %q", stderrStr)
	}
	if ctx.EnvExampleVars["DB_HOST"] != "localhost" {
		t.Errorf("EnvExampleVars[DB_HOST] = %q, want %q", ctx.EnvExampleVars["DB_HOST"], "localhost")
	}
}

// TestEnvExamplePrePassUndeclaredProbableSecret verifies that an undeclared key
// with a high-entropy value causes a non-nil error that names the key (but not
// its value), and that ctx.EnvExampleVars is not set.
func TestEnvExamplePrePassUndeclaredProbableSecret(t *testing.T) {
	repoDir := t.TempDir()

	// High-entropy value with no blocklist prefix so only entropy triggers.
	highEntropy := "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f"
	content := "SECRET_TOKEN=" + highEntropy + "\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for undeclared probable secret, got nil")
	}
	if !strings.Contains(err.Error(), "SECRET_TOKEN") {
		t.Errorf("expected error to name key SECRET_TOKEN, got: %v", err)
	}
	if strings.Contains(err.Error(), highEntropy) {
		t.Errorf("error must not contain value text, got: %v", err)
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars on error path, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassLowestPriorityLayer verifies that when .env.example has
// a key that is also declared in env.vars, the env.vars value wins in the final
// resolved output (i.e., .env.example is the lowest-priority layer).
func TestEnvExamplePrePassLowestPriorityLayer(t *testing.T) {
	repoDir := t.TempDir()
	configDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("APP_URL=from-example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}

	ws := newTestWorkspaceConfig(nil)

	ctx := &MaterializeContext{
		Config:    ws,
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{
					Values: map[string]config.MaybeSecret{
						"APP_URL": {Plain: "from-workspace"},
					},
				},
			},
		},
	}

	written, err := e.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("expected at least one file written")
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "APP_URL=from-workspace") {
		t.Errorf("expected APP_URL=from-workspace in output, got:\n%s", content)
	}
	if strings.Contains(content, "from-example") {
		t.Errorf("from-example value should have been overridden, got:\n%s", content)
	}
}

// TestEnvExamplePrePassSourcesPopulated verifies that a successful pre-pass sets
// ctx.EnvExampleSources to a single entry with Kind SourceKindEnvExample.
func TestEnvExamplePrePassSourcesPopulated(t *testing.T) {
	repoDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ctx.EnvExampleSources) != 1 {
		t.Fatalf("expected 1 EnvExampleSources entry, got %d", len(ctx.EnvExampleSources))
	}
	if ctx.EnvExampleSources[0].Kind != SourceKindEnvExample {
		t.Errorf("EnvExampleSources[0].Kind = %q, want %q", ctx.EnvExampleSources[0].Kind, SourceKindEnvExample)
	}
}

// TestEnvExamplePrePassPerLineWarningForwarding verifies that per-line parse
// warnings from parseDotEnvExample are emitted to the injected stderr buffer
// and that valid lines in the same file are still parsed successfully.
func TestEnvExamplePrePassPerLineWarningForwarding(t *testing.T) {
	repoDir := t.TempDir()

	// A file with one invalid line (missing '=') and one valid safe line.
	content := "INVALID_NO_EQUALS\nDB_HOST=localhost\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "missing") {
		t.Errorf("expected per-line warning in stderr, got: %q", stderrStr)
	}
	// The valid line should still be processed.
	if ctx.EnvExampleVars["DB_HOST"] != "localhost" {
		t.Errorf("EnvExampleVars[DB_HOST] = %q, want %q", ctx.EnvExampleVars["DB_HOST"], "localhost")
	}
}

// TestEnvExamplePrePassNoValueInStderrOrError verifies that when an undeclared
// probable secret is found, neither stderr nor the returned error contains any
// substring of the secret value.
func TestEnvExamplePrePassNoValueInStderrOrError(t *testing.T) {
	repoDir := t.TempDir()

	secretValue := "sk_live_verySecretValue12345"
	content := "MY_API_KEY=" + secretValue + "\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for probable secret, got nil")
	}

	stderrStr := stderr.String()
	if strings.Contains(stderrStr, secretValue) {
		t.Errorf("stderr must not contain secret value, got: %q", stderrStr)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Errorf("error must not contain secret value, got: %v", err)
	}

	// Verify that key name appears (we name the key, not the value).
	if !strings.Contains(err.Error(), "MY_API_KEY") {
		t.Errorf("error should name the key MY_API_KEY, got: %v", err)
	}
}

// initGitRepoWithRemote initialises a git repo in dir and adds a remote named
// origin pointing to remoteURL. It skips the calling test if git is unavailable.
func initGitRepoWithRemote(t *testing.T, dir, remoteURL string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", remoteURL).Run(); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
}

// highEntropyValue is a test fixture string with Shannon entropy >3.5.
// It uses a mix of cases and digits; the "T3st" prefix makes clear it is not a
// real credential, which keeps it below secret-scanning heuristic thresholds.
// that has no blocklist prefix, so only entropy triggers classification.
const highEntropyValue = "T3stH1ghEntr0pyF1xture9ABCDef567"

// TestPrePassPublicRemoteProbableSecret verifies that when a repo has a public
// GitHub remote and AllowPlaintextSecrets is false, a probable-secret key
// causes an error that names the key and mentions "public", and that no value
// text appears in the error. EnvExampleVars is not set.
func TestPrePassPublicRemoteProbableSecret(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepoWithRemote(t, repoDir, "https://github.com/someorg/somerepo.git")

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("SECRET="+highEntropyValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))
	ctx.AllowPlaintextSecrets = false

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for probable secret with public remote, got nil")
	}
	if !strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error should name key SECRET, got: %v", err)
	}
	if !strings.Contains(err.Error(), "public") {
		t.Errorf("error should mention 'public' (public remote), got: %v", err)
	}
	if strings.Contains(err.Error(), highEntropyValue) {
		t.Errorf("error must not contain value text, got: %v", err)
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars on error path, got %v", ctx.EnvExampleVars)
	}
}

// TestPrePassPublicRemoteAllowPlaintext verifies that when AllowPlaintextSecrets
// is true, a probable-secret key from a repo with a public GitHub remote is
// included in EnvExampleVars with a warning, and no error is returned.
func TestPrePassPublicRemoteAllowPlaintext(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepoWithRemote(t, repoDir, "https://github.com/someorg/somerepo.git")

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("SECRET="+highEntropyValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))
	ctx.AllowPlaintextSecrets = true

	err := e.runEnvExamplePrePass(ctx)
	if err != nil {
		t.Fatalf("expected no error with AllowPlaintextSecrets=true, got: %v", err)
	}
	if ctx.EnvExampleVars["SECRET"] != highEntropyValue {
		t.Errorf("EnvExampleVars[SECRET] = %q, want %q", ctx.EnvExampleVars["SECRET"], highEntropyValue)
	}
	if !strings.Contains(stderr.String(), "SECRET") {
		t.Errorf("expected warning mentioning SECRET in stderr, got: %q", stderr.String())
	}
}

// TestPrePassPrivateRemoteProbableSecret verifies that when a repo has a
// non-GitHub remote (e.g., GitLab), a probable-secret key still causes an
// error naming the key but NOT mentioning "public remote". EnvExampleVars is
// not set.
func TestPrePassPrivateRemoteProbableSecret(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepoWithRemote(t, repoDir, "https://gitlab.com/org/repo.git")

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("SECRET="+highEntropyValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for probable secret with private remote, got nil")
	}
	if !strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error should name key SECRET, got: %v", err)
	}
	if strings.Contains(err.Error(), "public remote") {
		t.Errorf("error must not mention 'public remote' for non-GitHub URL, got: %v", err)
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars on error path, got %v", ctx.EnvExampleVars)
	}
}

// TestPrePassNoGitProbableSecret verifies that when the directory is not a git
// repo, a probable-secret key still causes an error (guardrail skipped but
// basic error still reported), a warning about guardrail skip is emitted to
// stderr, and no value text appears in the error.
func TestPrePassNoGitProbableSecret(t *testing.T) {
	// Do NOT call initGitRepo — we want a plain directory with no .git.
	repoDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("SECRET="+highEntropyValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for probable secret with no git repo, got nil")
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "guardrail skipped") {
		t.Errorf("expected stderr warning about guardrail skip, got: %q", stderrStr)
	}
	if !strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error should name key SECRET, got: %v", err)
	}
	if strings.Contains(err.Error(), highEntropyValue) {
		t.Errorf("error must not contain value text, got: %v", err)
	}
}
