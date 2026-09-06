package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// TestPipeline_CloneSetupRunsBeforeWorktreeFanOut is the whole deliverable of
// the step relocation, not a check on it.
//
// The clone's setup-script step and the worktree fan-out are adjacent in
// runPipeline and their relative order was fixed by nothing: no design document
// states it, and before this test nothing in the suite observed it, because
// nothing exercised both steps in one run -- every worktree-refresh test calls
// refreshWorktreeEnvs directly, and every pipeline-level setup test runs on an
// instance with no sessions directory, so the fan-out iterates an empty list.
//
// That is why the relocation needs pinning. An ordering established by analysis
// and held in place by nothing gets reverted by the next person with a reason to
// move it, and what it reintroduces is silent: worktrees provisioned against the
// state the PREVIOUS apply's clone setup left behind.
//
// Two things about how it asserts, both learned the hard way. It needs a REAL
// git worktree, because the fan-out skips one it cannot see registered with git
// and then does no observable work. And the fan-out's observable has to be
// INLINE: an earlier version of this test keyed on the fan-out's skip warning,
// which is a DEFERRED warning flushed at the end of the run, so it landed after
// the setup marker whichever order the steps ran in. That version passed against
// the reverted order -- a co-occurrence test wearing an ordering test's clothes.
// The worktree hook below streams straight through the reporter, so its position
// in the buffer is its position in time.
func TestPipeline_CloneSetupRunsBeforeWorktreeFanOut(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Resolve symlinks in the temp root. On macOS t.TempDir() sits under
	// /var/folders, which is a symlink to /private/var/folders, and
	// `git worktree list --porcelain` reports the RESOLVED path. The fan-out's
	// registration check compares that against the path it was given, so an
	// unresolved path here makes it treat a perfectly good worktree as detached
	// and skip it -- and the test then has nothing to order.
	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving temp dir: %v", err)
	}
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := "[workspace]\nname = \"myws\"\n\n[[sources]]\norg = \"testorg\"\n\n[groups.all]\nvisibility = \"public\"\n"
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, cfgErr := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if cfgErr != nil {
		t.Fatalf("loading config: %v", cfgErr)
	}

	// The fan-out's inline observable: a worktree hook that streams a marker
	// through the reporter as it runs.
	hooksDir := filepath.Join(niwaDir, "worktree-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "apply.sh"),
		[]byte("#!/bin/sh\necho FANOUT-MARKER\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A real clone, so `git worktree add` below produces a worktree the fan-out
	// recognises and actually processes.
	const instanceName = "myws"
	repoDir := filepath.Join(tmpDir, instanceName, "all", "alpha")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, repoDir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, repoDir, "add", "-A")
	runGitWT(t, repoDir, "commit", "-qm", "init")

	// The clone's own setup script, whose marker must appear first.
	writeSetupScript(t, repoDir, "01-marker.sh", "#!/bin/sh\necho CLONE-SETUP-MARKER\n")

	wtPath := filepath.Join(tmpDir, instanceName, ".niwa", "worktrees", "alpha-0a1b2c3d")
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, repoDir, "worktree", "add", "-q", "-b", "session/0a1b2c3d", wtPath)

	sessionsDir := filepath.Join(tmpDir, instanceName, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := worktree.WriteSessionLifecycleState(sessionsDir, worktree.SessionLifecycleState{
		SessionID:    "0a1b2c3d",
		Repo:         "alpha",
		Purpose:      "ordering",
		WorktreePath: wtPath,
		Status:       worktree.SessionStatusActive,
	}); err != nil {
		t.Fatalf("writing session record: %v", err)
	}

	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{"testorg": {{
		Name:       "alpha",
		Visibility: "public",
		SSHURL:     "git@github.com:testorg/alpha.git",
	}}}})
	applier.Cloner = &Cloner{}
	var out syncBuffer
	applier.Reporter = NewReporterWithTTY(&out, false)

	if _, err := applier.Create(context.Background(), loaded.Config, niwaDir, tmpDir, instanceName); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := out.String()

	setupAt := strings.Index(got, "CLONE-SETUP-MARKER")
	if setupAt < 0 {
		t.Fatalf("the clone's setup script did not run; output was:\n%s", got)
	}
	fanOutAt := strings.Index(got, "FANOUT-MARKER")
	if fanOutAt < 0 {
		t.Fatalf("the worktree fan-out did not run its hook, so this run cannot "+
			"order the two steps; output was:\n%s", got)
	}

	if setupAt > fanOutAt {
		t.Errorf("the worktree fan-out ran BEFORE the clone's setup scripts, so a "+
			"worktree is provisioned against the previous apply's output.\n"+
			"clone setup at %d, fan-out at %d\n%s", setupAt, fanOutAt, got)
	}
}
