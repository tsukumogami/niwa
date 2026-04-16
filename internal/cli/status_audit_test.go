package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestClassifyMaybeSecret locks in the four-way classification that
// drives the audit output. vault:// prefix wins over resolved state
// only when Secret is empty, matching the parser contract in
// internal/config/maybesecret.go.
func TestClassifyMaybeSecret(t *testing.T) {
	tests := []struct {
		name string
		in   config.MaybeSecret
		want string
	}{
		{
			name: "empty",
			in:   config.MaybeSecret{},
			want: classEmpty,
		},
		{
			name: "plaintext",
			in:   config.MaybeSecret{Plain: "secret-literal"},
			want: classPlaintext,
		},
		{
			name: "vault-ref",
			in:   config.MaybeSecret{Plain: "vault://team/KEY"},
			want: classVaultRef,
		},
		{
			name: "vault-ref-anonymous",
			in:   config.MaybeSecret{Plain: "vault://KEY"},
			want: classVaultRef,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyMaybeSecret(tt.in)
			if got != tt.want {
				t.Errorf("classifyMaybeSecret(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestCollectAuditEntries_WalksAllSecretsTables verifies that every
// *.secrets location reachable from a WorkspaceConfig is enumerated,
// including per-repo and per-instance overrides. *.vars tables MUST
// NOT appear in the output.
func TestCollectAuditEntries_WalksAllSecretsTables(t *testing.T) {
	trueP := true
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
				"TOP_SECRET": {Plain: "vault://TOP_SECRET"},
			}},
			// Vars MUST NOT be walked.
			Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
				"PUBLIC_VAR": {Plain: "not-a-secret"},
			}},
		},
		Claude: config.ClaudeConfig{
			Env: config.ClaudeEnvConfig{
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"CLAUDE_SECRET": {Plain: "plaintext-leak"},
				}},
			},
		},
		Repos: map[string]config.RepoOverride{
			"app": {
				Env: config.EnvConfig{
					Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
						"REPO_SECRET": {Plain: ""},
					}},
				},
				Claude: &config.ClaudeOverride{
					Enabled: &trueP,
					Env: config.ClaudeEnvConfig{
						Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
							"REPO_CLAUDE_SECRET": {Plain: "vault://repo/CLAUDE_SECRET"},
						}},
					},
				},
			},
		},
		Instance: config.InstanceConfig{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"INSTANCE_SECRET": {Plain: "another-plaintext"},
				}},
			},
			Claude: &config.ClaudeOverride{
				Env: config.ClaudeEnvConfig{
					Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
						"INSTANCE_CLAUDE_SECRET": {Plain: "vault://i/CLAUDE_SECRET"},
					}},
				},
			},
		},
	}

	entries := collectAuditEntries(cfg, nil)

	// Build a set of KEY@TABLE pairs so assertions don't depend on
	// map iteration order.
	got := map[string]string{}
	for _, e := range entries {
		got[e.Key+"@"+e.Table] = e.Classification
	}

	want := map[string]string{
		"TOP_SECRET@env.secrets":                             classVaultRef,
		"CLAUDE_SECRET@claude.env.secrets":                   classPlaintext,
		"REPO_SECRET@repos.app.env.secrets":                  classEmpty,
		"REPO_CLAUDE_SECRET@repos.app.claude.env.secrets":    classVaultRef,
		"INSTANCE_SECRET@instance.env.secrets":               classPlaintext,
		"INSTANCE_CLAUDE_SECRET@instance.claude.env.secrets": classVaultRef,
	}

	for key, wantClass := range want {
		if got[key] != wantClass {
			t.Errorf("entry %q: classification = %q, want %q", key, got[key], wantClass)
		}
	}

	// *.vars MUST NOT be walked.
	for k := range got {
		if strings.Contains(k, "env.vars") || strings.Contains(k, "env.vars.") ||
			strings.HasSuffix(k, "PUBLIC_VAR@env.vars") {
			t.Errorf("unexpected vars entry %q", k)
		}
	}
	// Specifically assert the vars key is absent (belt-and-braces).
	if _, present := got["PUBLIC_VAR@env.vars"]; present {
		t.Errorf("PUBLIC_VAR should not appear: audit must skip *.vars tables")
	}
}

// TestCollectAuditEntries_ShadowedColumnReadsState checks that the
// SHADOWED column is derived from the Shadows slice passed in, not
// from any live overlay walk. The source-of-truth contract is
// documented in internal/workspace/shadows.go.
func TestCollectAuditEntries_ShadowedColumnReadsState(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{
			Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
				"SHADOWED_KEY":  {Plain: "vault://team/SHADOWED_KEY"},
				"CLEAR_KEY":     {Plain: "vault://team/CLEAR_KEY"},
				"UNRELATED_VAR": {Plain: "vault://team/UNRELATED_VAR"},
			}},
		},
	}
	shadows := []workspace.Shadow{
		{Kind: "env-secret", Name: "SHADOWED_KEY", Layer: "personal-overlay"},
		// A same-named env-var shadow MUST NOT flip an env-secret row.
		{Kind: "env-var", Name: "UNRELATED_VAR", Layer: "personal-overlay"},
	}

	entries := collectAuditEntries(cfg, shadows)

	got := map[string]string{}
	for _, e := range entries {
		got[e.Key] = e.Shadowed
	}

	if !strings.Contains(got["SHADOWED_KEY"], "yes") {
		t.Errorf("SHADOWED_KEY: expected yes, got %q", got["SHADOWED_KEY"])
	}
	if got["CLEAR_KEY"] != "no" {
		t.Errorf("CLEAR_KEY: expected no, got %q", got["CLEAR_KEY"])
	}
	// An env-var shadow must NOT flip an env-secret row.
	if got["UNRELATED_VAR"] != "no" {
		t.Errorf("UNRELATED_VAR: env-var shadow should not mark env-secret row, got %q", got["UNRELATED_VAR"])
	}
}

// TestRunAuditSecrets_ExitsNonZeroWhenPlaintextAndVaultConfigured is
// the headline AC for Issue 10: plaintext in a vault-configured
// workspace must fail the command.
func TestRunAuditSecrets_ExitsNonZeroWhenPlaintextAndVaultConfigured(t *testing.T) {
	workspaceRoot, niwaDir := setupAuditWorkspace(t, `
[workspace]
name = "audit-ws"

[vault.provider]
kind = "fake"

[env.secrets]
LEAKED = "plaintext-should-fail"
`)
	_ = niwaDir

	origDir, _ := os.Getwd()
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	statusCmd.SetOut(&strings.Builder{})
	defer statusCmd.SetOut(os.Stdout)
	err := runAuditSecrets(statusCmd, workspaceRoot)
	if err == nil {
		t.Fatal("expected non-zero exit when plaintext + vault configured")
	}
	if !strings.Contains(err.Error(), "plaintext") {
		t.Errorf("error should name the problem: %v", err)
	}
}

// TestRunAuditSecrets_ExitsZeroWithoutVault confirms that a workspace
// WITHOUT [vault.*] declared never fails the audit, even when
// plaintext secrets are present — the user hasn't opted in to the
// vault workflow, so plaintext is their intentional baseline.
func TestRunAuditSecrets_ExitsZeroWithoutVault(t *testing.T) {
	workspaceRoot, _ := setupAuditWorkspace(t, `
[workspace]
name = "no-vault-ws"

[env.secrets]
STILL_OK = "this-is-plaintext-but-ok"
`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)
	if err := runAuditSecrets(statusCmd, workspaceRoot); err != nil {
		t.Fatalf("expected nil error when no vault configured, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "STILL_OK") {
		t.Errorf("audit table should list the key: %s", out)
	}
	if !strings.Contains(out, classPlaintext) {
		t.Errorf("audit table should label the classification: %s", out)
	}
}

// TestRunAuditSecrets_ExitsZeroWithOnlyVaultRefsOrEmpty is the happy
// path: vault refs and empty values are compliant even with a vault
// configured.
func TestRunAuditSecrets_ExitsZeroWithOnlyVaultRefsOrEmpty(t *testing.T) {
	workspaceRoot, _ := setupAuditWorkspace(t, `
[workspace]
name = "clean-ws"

[vault.provider]
kind = "fake"

[env.secrets]
RESOLVED = "vault://RESOLVED"
EMPTY = ""
`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)
	if err := runAuditSecrets(statusCmd, workspaceRoot); err != nil {
		t.Fatalf("expected nil error when only vault-refs + empty present, got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "vault-ref") {
		t.Errorf("expected classification column in output: %s", out)
	}
}

// TestRunAuditSecrets_SHADOWEDReadsState verifies the integration
// path from state.Shadows to the rendered table. The state is
// persisted by Issue 8; Issue 10 reads it here.
func TestRunAuditSecrets_SHADOWEDReadsState(t *testing.T) {
	workspaceRoot, _ := setupAuditWorkspace(t, `
[workspace]
name = "shadow-ws"

[vault.provider]
kind = "fake"

[env.secrets]
SHADOWED = "vault://SHADOWED"
`)

	// Drop an instance state with a matching shadow.
	instanceDir := filepath.Join(workspaceRoot, "shadow-ws")
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	configName := "shadow-ws"
	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "shadow-ws",
		InstanceNumber: 1,
		Root:           instanceDir,
		Created:        now,
		LastApplied:    now,
		Shadows: []workspace.Shadow{
			{Kind: "env-secret", Name: "SHADOWED", Layer: "personal-overlay"},
		},
	}
	if err := workspace.SaveState(instanceDir, state); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(instanceDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)
	if err := runAuditSecrets(statusCmd, instanceDir); err != nil {
		t.Fatalf("runAuditSecrets: %v", err)
	}
	out := buf.String()
	// The SHADOWED column should report "yes (personal-overlay)" for
	// the key that state.Shadows names.
	if !strings.Contains(out, "yes (personal-overlay)") {
		t.Errorf("expected SHADOWED=yes line, got:\n%s", out)
	}
}

// setupAuditWorkspace writes a minimal .niwa/workspace.toml under a
// temp directory and returns the workspace root + niwa dir. The
// fixture covers the filesystem shape runAuditSecrets expects;
// config.Discover walks up so the caller can cd into either the root
// or an instance subdir.
func setupAuditWorkspace(t *testing.T, configTOML string) (string, string) {
	t.Helper()
	root := t.TempDir()
	niwaDir := filepath.Join(root, workspace.StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(niwaDir, workspace.WorkspaceConfigFile),
		[]byte(configTOML),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	return root, niwaDir
}
