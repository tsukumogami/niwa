package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
	sourcepkg "github.com/tsukumogami/niwa/internal/source"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestDefaultRunBootstrap_RegistersWorkspaceInGlobalRegistry is the regression
// test for the bootstrap-registry gap: defaultRunBootstrap must write a global
// registry entry after the orchestrator returns nil, so a successfully
// bootstrapped workspace is discoverable via `niwa go` and tab completion.
//
// Before this fix, runInit's bootstrap branch returned via defaultRunBootstrap
// without reaching the registry-write block in the non-bootstrap clone path,
// leaving the workspace orphaned in the global registry even though everything
// else (instance dir, role, session worktree, scaffold commit) landed.
func TestDefaultRunBootstrap_RegistersWorkspaceInGlobalRegistry(t *testing.T) {
	// Sandbox XDG_CONFIG_HOME so global config writes land in tmp.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpHome, ".config"))

	// Stub the GitHub API base URL: defaultRunBootstrap's R17 visibility
	// lookup will hit this fake instead of api.github.com.
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/foo" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"foo","visibility":"public","private":false,"clone_url":"https://github.com/owner/foo.git","ssh_url":"git@github.com:owner/foo.git"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ghSrv.Close)
	t.Setenv("NIWA_GITHUB_API_URL", ghSrv.URL)
	// No env token; defeat the `gh auth token` fallback by stripping PATH.
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", "")

	// Stub the orchestrator so we don't need a real applier, git invoker,
	// or session worktree — only the post-orchestrator tail (registry
	// write, success block) needs to run.
	origOrch := runBootstrapOrchestrator
	runBootstrapOrchestrator = func(_ context.Context, p workspace.BootstrapParams) (workspace.BootstrapResult, error) {
		instancePath := filepath.Join(p.WorkspaceRoot, p.InstanceName)
		return workspace.BootstrapResult{
			InstancePath: instancePath,
			WorktreePath: filepath.Join(instancePath, ".niwa", "worktrees", "stub"),
			BranchName:   "niwa-bootstrap/stub",
		}, nil
	}
	t.Cleanup(func() { runBootstrapOrchestrator = origOrch })

	workspaceRoot := filepath.Join(tmpHome, "ws", "demo")
	cmd := &cobra.Command{}
	cmd.SetErr(io.Discard)
	cmd.SetOut(io.Discard)

	src := sourcepkg.Source{Owner: "owner", Repo: "foo"}

	if err := defaultRunBootstrap(context.Background(), cmd, workspaceRoot, "demo", src, "owner/foo", func() {}); err != nil {
		t.Fatalf("defaultRunBootstrap: %v", err)
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	entry := cfg.LookupWorkspace("demo")
	if entry == nil {
		t.Fatal(`expected registry entry "demo" after successful bootstrap; got none`)
	}

	absRoot, _ := filepath.Abs(workspaceRoot)
	if entry.Root != absRoot {
		t.Errorf("registry Root = %q, want %q", entry.Root, absRoot)
	}
	wantSource := filepath.Join(absRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
	if entry.Source != wantSource {
		t.Errorf("registry Source = %q, want %q", entry.Source, wantSource)
	}
	if entry.SourceURL != "owner/foo" {
		t.Errorf(`registry SourceURL = %q, want "owner/foo"`, entry.SourceURL)
	}
}
