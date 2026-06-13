package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// gitInit initializes a git repo in dir so custom-named secret outputs can have
// confirmable ignore coverage.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", "-C", dir, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func newEnvOutputCtx(t *testing.T, repoDir string, targets config.OutputTargets) *MaterializeContext {
	t.Helper()
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "workspace.env"), []byte("FOO=bar\nBAZ=qux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{
			"myrepo": {EnvOutput: targets},
		},
	}
	return &MaterializeContext{
		Config: cfg,
		Effective: EffectiveConfig{
			Env: config.EnvConfig{Files: []string{"workspace.env"}},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}
}

func TestEnvMaterializer_CustomTargetsAndFormats(t *testing.T) {
	repoDir := t.TempDir()
	gitInit(t, repoDir)

	ctx := newEnvOutputCtx(t, repoDir, config.OutputTargets{
		{Path: ".env.local"},          // inferred dotenv
		{Path: "config/secrets.json"}, // inferred json, nested dir
		{Path: "env.sh", Format: ""},  // inferred shell
	})

	written, err := (&EnvMaterializer{}).Materialize(ctx)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(written), written)
	}

	dotenv, _ := os.ReadFile(filepath.Join(repoDir, ".env.local"))
	if !strings.Contains(string(dotenv), "FOO=bar\n") {
		t.Errorf("dotenv target missing FOO=bar:\n%s", dotenv)
	}
	js, _ := os.ReadFile(filepath.Join(repoDir, "config", "secrets.json"))
	if !strings.Contains(string(js), `"FOO": "bar"`) {
		t.Errorf("json target malformed:\n%s", js)
	}
	sh, _ := os.ReadFile(filepath.Join(repoDir, "env.sh"))
	if !strings.Contains(string(sh), "export FOO='bar'") {
		t.Errorf("shell target malformed:\n%s", sh)
	}

	// All custom names must be invisible to git status (coverage recorded).
	cmd := exec.Command("git", "-C", repoDir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean git status, got:\n%s", out)
	}

	// Parent dir of a nested secret must not be world-readable.
	info, err := os.Stat(filepath.Join(repoDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("parent dir perm = %o, want no group/other access", perm)
	}
}

func TestEnvMaterializer_RejectsPathTraversal(t *testing.T) {
	repoDir := t.TempDir()
	gitInit(t, repoDir)

	for _, bad := range []string{"../escape.env", "/etc/secrets.env", "a/../../escape.env"} {
		ctx := newEnvOutputCtx(t, repoDir, config.OutputTargets{{Path: bad}})
		if _, err := (&EnvMaterializer{}).Materialize(ctx); err == nil {
			t.Errorf("expected error for traversal target %q, got nil", bad)
		}
	}
}

func TestEnvMaterializer_RejectsSymlinkedParentEscape(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repoDir)
	// A symlinked subdir inside the repo pointing outside it.
	if err := os.Symlink(outside, filepath.Join(repoDir, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	ctx := newEnvOutputCtx(t, repoDir, config.OutputTargets{{Path: "link/secrets.env"}})
	if _, err := (&EnvMaterializer{}).Materialize(ctx); err == nil {
		t.Error("expected error for symlinked-parent escape, got nil")
	}
	if _, err := os.Stat(filepath.Join(outside, "secrets.env")); err == nil {
		t.Error("secret was written outside the repo via symlink")
	}
}

func TestEnvMaterializer_CustomNameOnNonGitTreeFailsClosed(t *testing.T) {
	repoDir := t.TempDir() // deliberately NOT a git repo

	ctx := newEnvOutputCtx(t, repoDir, config.OutputTargets{{Path: ".env"}})
	if _, err := (&EnvMaterializer{}).Materialize(ctx); err == nil {
		t.Fatal("expected fail-closed error for custom name on non-git tree, got nil")
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".env")); err == nil {
		t.Error("custom secret file was written despite unconfirmable coverage")
	}
}

func TestEnvMaterializer_DefaultOnNonGitTreeStillWrites(t *testing.T) {
	repoDir := t.TempDir() // not a git repo; default .local.env carries legacy posture

	ctx := newEnvOutputCtx(t, repoDir, nil) // no targets -> default .local.env
	written, err := (&EnvMaterializer{}).Materialize(ctx)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(written) != 1 || filepath.Base(written[0]) != ".local.env" {
		t.Fatalf("expected default .local.env, got %v", written)
	}
}
