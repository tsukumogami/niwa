package workspace

import (
	"bytes"
	"os"
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

// TestEnvExamplePrePassUndeclaredProbableSecretWarnsByDefault verifies that an
// undeclared probable-secret key with NO policy configured warns (not fails) and
// is included in the result — the warn-by-default posture. The warning names the
// key and category but never the value.
func TestEnvExamplePrePassUndeclaredProbableSecretWarnsByDefault(t *testing.T) {
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

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("expected no error (warn by default), got: %v", err)
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "SECRET_TOKEN") {
		t.Errorf("expected warning to name key SECRET_TOKEN, got: %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "entropy") {
		t.Errorf("expected warning to name category entropy, got: %q", stderrStr)
	}
	if strings.Contains(stderrStr, highEntropy) {
		t.Errorf("warning must not contain value text, got: %q", stderrStr)
	}
	if ctx.EnvExampleVars["SECRET_TOKEN"] != highEntropy {
		t.Errorf("expected SECRET_TOKEN included by default, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassPerCategoryFail verifies that a workspace-level
// per-category fail policy aborts apply for an undeclared key in that category,
// naming the key and category but never the value.
func TestEnvExamplePrePassPerCategoryFail(t *testing.T) {
	repoDir := t.TempDir()

	highEntropy := "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f"
	content := "SECRET_TOKEN=" + highEntropy + "\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ws := newTestWorkspaceConfig(nil)
	ws.Workspace.EnvExamplePolicy = &config.EnvExamplePolicy{Entropy: actionPtr(config.ActionFail)}
	ctx := makeCtx(repoDir, ws)

	err := e.runEnvExamplePrePass(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for entropy=fail policy, got nil")
	}
	if !strings.Contains(err.Error(), "SECRET_TOKEN") {
		t.Errorf("expected error to name key SECRET_TOKEN, got: %v", err)
	}
	if !strings.Contains(err.Error(), "entropy") {
		t.Errorf("expected error to name category entropy, got: %v", err)
	}
	if strings.Contains(err.Error(), highEntropy) {
		t.Errorf("error must not contain value text, got: %v", err)
	}
	if ctx.EnvExampleVars != nil {
		t.Errorf("expected nil EnvExampleVars on fail path, got %v", ctx.EnvExampleVars)
	}
}

// TestEnvExamplePrePassInlineWarnOverride verifies that an inline `# niwa: warn`
// annotation on a key overrides a workspace-level per-category fail, so the key
// warns and is included instead of failing.
func TestEnvExamplePrePassInlineWarnOverride(t *testing.T) {
	repoDir := t.TempDir()

	highEntropy := "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f"
	content := "SECRET_TOKEN=" + highEntropy + " # niwa: warn\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ws := newTestWorkspaceConfig(nil)
	ws.Workspace.EnvExamplePolicy = &config.EnvExamplePolicy{Entropy: actionPtr(config.ActionFail)}
	ctx := makeCtx(repoDir, ws)

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("expected no error (inline warn overrides fail), got: %v", err)
	}
	if ctx.EnvExampleVars["SECRET_TOKEN"] != highEntropy {
		t.Errorf("expected SECRET_TOKEN included via inline warn, got %v", ctx.EnvExampleVars)
	}
	// The inline-driven downgrade of a configured fail must be greppable.
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "inline annotation lowered a configured fail") {
		t.Errorf("expected inline-downgrade diagnostic, got: %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "SECRET_TOKEN") {
		t.Errorf("expected downgrade diagnostic to name the key, got: %q", stderrStr)
	}
	if strings.Contains(stderrStr, highEntropy) {
		t.Errorf("diagnostics must not contain value text, got: %q", stderrStr)
	}
}

// TestEnvExamplePrePassInlineWarnNoDowngradeWhenNotConfigured verifies that an
// inline `# niwa: warn` annotation on a key with NO configured fail does NOT
// emit the downgrade diagnostic (nothing was lowered).
func TestEnvExamplePrePassInlineWarnNoDowngradeWhenNotConfigured(t *testing.T) {
	repoDir := t.TempDir()

	highEntropy := "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f"
	content := "SECRET_TOKEN=" + highEntropy + " # niwa: warn\n"
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
	if strings.Contains(stderr.String(), "inline annotation lowered") {
		t.Errorf("did not expect downgrade diagnostic when nothing was configured, got: %q", stderr.String())
	}
}

// TestEnvExamplePrePassAllowPlaintextDowngradesFailWithAudit verifies that
// --allow-plaintext-secrets downgrades a resolved fail to warn, emits a
// greppable per-key audit diagnostic, and includes the key. The audit line
// names the key and category but never the value.
func TestEnvExamplePrePassAllowPlaintextDowngradesFailWithAudit(t *testing.T) {
	repoDir := t.TempDir()

	highEntropy := "xJ3kP9mQ2nR7sT4uV8wY1zA6bC0dE5f"
	content := "SECRET_TOKEN=" + highEntropy + "\n"
	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ws := newTestWorkspaceConfig(nil)
	ws.Workspace.EnvExamplePolicy = &config.EnvExamplePolicy{Entropy: actionPtr(config.ActionFail)}
	ctx := makeCtx(repoDir, ws)
	ctx.AllowPlaintextSecrets = true

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("expected no error with AllowPlaintextSecrets, got: %v", err)
	}
	if ctx.EnvExampleVars["SECRET_TOKEN"] != highEntropy {
		t.Errorf("expected SECRET_TOKEN included after downgrade, got %v", ctx.EnvExampleVars)
	}
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "allow-plaintext-secrets") {
		t.Errorf("expected audit diagnostic mentioning allow-plaintext-secrets, got: %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "SECRET_TOKEN") {
		t.Errorf("expected audit diagnostic to name the key, got: %q", stderrStr)
	}
	if strings.Contains(stderrStr, highEntropy) {
		t.Errorf("audit diagnostic must not contain value text, got: %q", stderrStr)
	}
}

// TestEnvExamplePrePassNoRemoteDependency verifies that the pre-pass no longer
// consults a git remote: a plain directory with no .git and a probable-secret
// key warns by default (does not error and does not emit any guardrail-skip
// notice), proving the public-remote branch was removed.
func TestEnvExamplePrePassNoRemoteDependency(t *testing.T) {
	// Plain directory, no .git, no remote.
	repoDir := t.TempDir()

	envExample := filepath.Join(repoDir, ".env.example")
	if err := os.WriteFile(envExample, []byte("SECRET="+highEntropyValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	e := &EnvMaterializer{Stderr: &stderr}
	ctx := makeCtx(repoDir, newTestWorkspaceConfig(nil))

	if err := e.runEnvExamplePrePass(ctx); err != nil {
		t.Fatalf("expected warn-by-default with no remote, got: %v", err)
	}
	stderrStr := stderr.String()
	if strings.Contains(stderrStr, "remote") || strings.Contains(stderrStr, "guardrail") {
		t.Errorf("expected no remote/guardrail mention, got: %q", stderrStr)
	}
	if ctx.EnvExampleVars["SECRET"] != highEntropyValue {
		t.Errorf("expected SECRET included by default, got %v", ctx.EnvExampleVars)
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
	// Force the fail path with a vendor-token=fail policy (sk_live_ classifies
	// as vendor-token) so an error is produced to inspect.
	ws := newTestWorkspaceConfig(nil)
	ws.Workspace.EnvExamplePolicy = &config.EnvExamplePolicy{VendorToken: actionPtr(config.ActionFail)}
	ctx := makeCtx(repoDir, ws)

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

// highEntropyValue is a test fixture string with Shannon entropy >3.5.
// It uses a mix of cases and digits; the "T3st" prefix makes clear it is not a
// real credential, which keeps it below secret-scanning heuristic thresholds.
// that has no blocklist prefix, so only entropy triggers classification.
const highEntropyValue = "T3stH1ghEntr0pyF1xture9ABCDef567"

// actionPtr returns a pointer to the given Action, for building per-category
// EnvExamplePolicy fixtures.
func actionPtr(a config.Action) *config.Action {
	return &a
}
