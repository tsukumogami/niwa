package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// inheritFixture builds a clone repo dir and a (git) worktree dir, writing the
// given clone target files. Returns (cloneRepoDir, worktreeDir).
func inheritFixture(t *testing.T, cloneFiles map[string]string, gitWorktree bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	cloneRepoDir := filepath.Join(root, "clone")
	worktreeDir := filepath.Join(root, "wt")
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if gitWorktree {
		gitInit(t, worktreeDir)
	}
	for rel, content := range cloneFiles {
		abs := filepath.Join(cloneRepoDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cloneRepoDir, worktreeDir
}

// envConfiguredCfg returns a cfg + effective env that the "configured" predicate
// treats as having env (a workspace env file). The per-repo env_output targets
// are set on the repo override.
func envConfiguredCfg(repo string, targets config.OutputTargets) (*config.WorkspaceConfig, config.EnvConfig) {
	cfg := &config.WorkspaceConfig{
		Env: config.EnvConfig{Files: []string{"workspace.env"}},
		Repos: map[string]config.RepoOverride{
			repo: {EnvOutput: targets},
		},
	}
	return cfg, config.EnvConfig{Files: []string{"workspace.env"}}
}

func TestInheritEnvOutputs_ByteEquivalenceAcrossFormats(t *testing.T) {
	const (
		dotenv = "FOO=bar\nBAZ=qux\n"
		jsonC  = "{\n  \"FOO\": \"bar\"\n}\n"
		shellC = "export FOO='bar'\n"
	)
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		".env.local":          dotenv,
		"config/secrets.json": jsonC,
		"env.sh":              shellC,
	}, true)

	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{
		{Path: ".env.local"},
		{Path: "config/secrets.json"},
		{Path: "env.sh"},
	})

	written, custom, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err != nil {
		t.Fatalf("inheritEnvOutputs: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("expected 3 written, got %d: %v", len(written), written)
	}

	for rel, want := range map[string]string{
		".env.local":          dotenv,
		"config/secrets.json": jsonC,
		"env.sh":              shellC,
	} {
		got, err := os.ReadFile(filepath.Join(worktreeDir, rel))
		if err != nil {
			t.Fatalf("reading worktree %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s not byte-identical:\n got: %q\nwant: %q", rel, got, want)
		}
	}

	// All three target names are custom (none contain ".local" via base pattern
	// except .env.local). .env.local DOES contain ".local", so only the json and
	// sh names are custom.
	if len(custom) != 2 {
		t.Errorf("expected 2 custom names (json, sh), got %v", custom)
	}
}

func TestInheritEnvOutputs_Perms0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm bits not meaningful on windows")
	}
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		".local.env": "FOO=bar\n",
	}, true)
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{{Path: ".local.env"}})

	written, _, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err != nil {
		t.Fatalf("inheritEnvOutputs: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 written, got %v", written)
	}
	info, err := os.Stat(written[0])
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("written env perm = %o, want 0600", perm)
	}
}

func TestInheritEnvOutputs_CustomNameGitExcludeCoverage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		"secrets.json": "{\"K\":\"v\"}\n",
	}, true)
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{{Path: "secrets.json"}})

	written, custom, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err != nil {
		t.Fatalf("inheritEnvOutputs: %v", err)
	}
	if len(written) != 1 || len(custom) != 1 || custom[0] != "secrets.json" {
		t.Fatalf("unexpected written/custom: %v / %v", written, custom)
	}

	// The custom-named secret file must be invisible to git status (coverage
	// recorded before the write).
	cmd := exec.Command("git", "-C", worktreeDir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean git status (custom secret excluded), got:\n%s", out)
	}
}

func TestInheritEnvOutputs_CustomNameRefusesNonGitTree(t *testing.T) {
	// Worktree is NOT a git repo: a custom (non-*.local*) target cannot have
	// confirmable exclude coverage, so the primitive must fail closed.
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		"secrets.json": "{\"K\":\"v\"}\n",
	}, false /* not a git worktree */)
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{{Path: "secrets.json"}})

	_, _, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err == nil {
		t.Fatal("expected fail-closed error for custom target on non-git worktree, got nil")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error should name the non-git refusal, got: %v", err)
	}
	// Nothing must have been written.
	if _, statErr := os.Stat(filepath.Join(worktreeDir, "secrets.json")); !os.IsNotExist(statErr) {
		t.Error("custom secret file must not be written on a non-git tree")
	}
}

func TestInheritEnvOutputs_MissingTargetIsR8Error(t *testing.T) {
	// Repo HAS env configured but the clone holds no materialized output.
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{}, true)
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{{Path: ".local.env"}})

	_, _, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err == nil {
		t.Fatal("expected R8 error for missing configured clone target, got nil")
	}
	if !strings.Contains(err.Error(), "niwa apply") {
		t.Errorf("R8 error must name `niwa apply`, got: %v", err)
	}
}

func TestInheritEnvOutputs_NoEnvNoError(t *testing.T) {
	// Repo has NO env configured at all: a missing clone target is not an error,
	// nothing is copied.
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{}, true)
	cfg := &config.WorkspaceConfig{} // no env, no repo override
	effEnv := config.EnvConfig{}

	written, custom, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err != nil {
		t.Fatalf("expected no error for unconfigured repo, got: %v", err)
	}
	if len(written) != 0 || len(custom) != 0 {
		t.Errorf("expected nothing written/custom, got %v / %v", written, custom)
	}
}

func TestInheritEnvOutputs_RejectsSourcePathTraversal(t *testing.T) {
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		".local.env": "FOO=bar\n",
	}, true)
	// A crafted traversal target must be rejected at the source guard.
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{{Path: "../escape.env"}})

	_, _, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err == nil {
		t.Fatal("expected error for traversal target, got nil")
	}
}

func TestInheritEnvOutputs_MultipleDotenvTargets(t *testing.T) {
	const a = "A=1\n"
	const b = "B=2\n"
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		".local.env":     a,
		"sub/.local.env": b,
	}, true)
	cfg, effEnv := envConfiguredCfg("app", config.OutputTargets{
		{Path: ".local.env"},
		{Path: "sub/.local.env"},
	})

	written, custom, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", nil, effEnv, nil)
	if err != nil {
		t.Fatalf("inheritEnvOutputs: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 written, got %v", written)
	}
	// Both names contain ".local", so neither is custom.
	if len(custom) != 0 {
		t.Errorf("expected no custom names, got %v", custom)
	}
	gotA, _ := os.ReadFile(filepath.Join(worktreeDir, ".local.env"))
	gotB, _ := os.ReadFile(filepath.Join(worktreeDir, "sub", ".local.env"))
	if string(gotA) != a || string(gotB) != b {
		t.Errorf("multiple-target inherit mismatch: %q / %q", gotA, gotB)
	}
}

func TestInheritEnvOutputs_GlobalEnvOutputRung(t *testing.T) {
	// No workspace/per-repo env_output, but a personal/global rung names a custom
	// target. The primitive must resolve targets from that rung.
	cloneRepoDir, worktreeDir := inheritFixture(t, map[string]string{
		"global-secrets.json": "{\"K\":\"v\"}\n",
	}, true)
	cfg := &config.WorkspaceConfig{Env: config.EnvConfig{Files: []string{"workspace.env"}}}
	effEnv := config.EnvConfig{Files: []string{"workspace.env"}}
	global := config.OutputTargets{{Path: "global-secrets.json"}}

	written, custom, err := inheritEnvOutputs(cloneRepoDir, worktreeDir, cfg, "app", global, effEnv, nil)
	if err != nil {
		t.Fatalf("inheritEnvOutputs: %v", err)
	}
	if len(written) != 1 || len(custom) != 1 {
		t.Fatalf("expected 1 written + 1 custom from global rung, got %v / %v", written, custom)
	}
}
