package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDestroyCmd_HasForceFlag(t *testing.T) {
	flag := destroyCmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default %q, got %q", "false", flag.DefValue)
	}
}

func TestDestroyCmd_AcceptsOptionalPositionalArg(t *testing.T) {
	if err := destroyCmd.Args(destroyCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := destroyCmd.Args(destroyCmd, []string{"my-instance"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := destroyCmd.Args(destroyCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}

// destroyTestSetup creates a minimal workspace + N instances on disk and
// chdir's into the requested location. Returns the workspace root path.
func destroyTestSetup(t *testing.T, instanceNames []string) string {
	t.Helper()
	root := t.TempDir()

	// .niwa/workspace.toml at root.
	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"),
		[]byte("[workspace]\nname = \"testws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Each instance: <root>/<name>/.niwa/instance.json
	for _, name := range instanceNames {
		instDir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(instDir, ".niwa"), 0o755); err != nil {
			t.Fatal(err)
		}
		state := map[string]any{
			"schema_version":  1,
			"instance_name":   name,
			"instance_number": 1,
			"root":            instDir,
			"created":         time.Now().Format(time.RFC3339),
			"last_applied":    time.Now().Format(time.RFC3339),
			"repos":           map[string]any{},
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(instDir, ".niwa", "instance.json"),
			data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// chdirTo changes the working directory for the duration of the test.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestRunDestroy_OutsideWorkspaceErrors(t *testing.T) {
	dir := t.TempDir()
	chdirTo(t, dir)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := runDestroy(cmd, nil)
	if err == nil {
		t.Fatal("expected error when run outside any workspace")
	}
	if !strings.Contains(err.Error(), "not inside a niwa workspace or instance") {
		t.Errorf("error should mention being outside; got: %v", err)
	}
}

func TestRunDestroy_NameFromInsideInstanceIsRejected(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instDir := filepath.Join(root, "alpha")
	chdirTo(t, instDir)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := runDestroy(cmd, []string{"some-other-name"})
	if err == nil {
		t.Fatal("expected error: name from inside instance should be rejected")
	}
	if !strings.Contains(err.Error(), "instance name is only valid from the workspace root") {
		t.Errorf("error should mention root-only; got: %v", err)
	}

	// Instance dir must not have been touched.
	if _, statErr := os.Stat(instDir); statErr != nil {
		t.Errorf("instance dir should still exist; got Stat err = %v", statErr)
	}
}

func TestRunDestroy_NamedInstanceFromRootDestroys(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)

	cmd := destroyCmd
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runDestroy(cmd, []string{"alpha"}); err != nil {
		t.Fatalf("runDestroy: %v", err)
	}
	if !strings.Contains(stdout.String(), "Destroyed instance:") {
		t.Errorf("expected 'Destroyed instance:' in stdout; got: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Errorf("alpha should be removed; got Stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "beta")); err != nil {
		t.Errorf("beta should still exist; got Stat err = %v", err)
	}
}

func TestRunDestroy_NamedInstanceUnknownReportsAvailable(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := runDestroy(cmd, []string{"missing"})
	if err == nil {
		t.Fatal("expected error for unknown instance name")
	}
	if !strings.Contains(err.Error(), "instance \"missing\" not found") {
		t.Errorf("error should name the missing instance; got: %v", err)
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("error should list available instances; got: %v", err)
	}
}

func TestRunDestroy_NoArgEmptyWorkspaceRemovesWorkspace(t *testing.T) {
	root := destroyTestSetup(t, nil)
	chdirTo(t, root)

	cmd := destroyCmd
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	// chdir outside the workspace before assertion so the cleanup doesn't
	// trip over a deleted dir.
	t.Cleanup(func() {
		_ = os.Chdir(filepath.Dir(root))
	})

	if err := runDestroy(cmd, nil); err != nil {
		t.Fatalf("runDestroy: %v", err)
	}
	if !strings.Contains(stdout.String(), "Destroyed workspace:") {
		t.Errorf("expected 'Destroyed workspace:' in stdout; got: %q", stdout.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace root should be removed; got Stat err = %v", err)
	}
}

func TestRunDestroy_NoArgSingleInstanceDestroysIt(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)

	cmd := destroyCmd
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runDestroy(cmd, nil); err != nil {
		t.Fatalf("runDestroy: %v", err)
	}
	if !strings.Contains(stdout.String(), "Destroyed instance:") {
		t.Errorf("expected 'Destroyed instance:' (not 'workspace'); got: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Errorf("alpha should be removed; got: %v", err)
	}
	// Workspace root is preserved (single-instance shortcut doesn't wipe it).
	if _, err := os.Stat(root); err != nil {
		t.Errorf("workspace root should still exist; got: %v", err)
	}
}

func TestRunDestroy_NoArgMultipleInstancesNonTTYRefuses(t *testing.T) {
	// In a test environment stdin is not a TTY, so the picker path
	// should refuse with an explanatory error rather than rendering.
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	err := runDestroy(cmd, nil)
	if err == nil {
		t.Fatal("expected error for picker without a TTY")
	}
	if !strings.Contains(err.Error(), "not running in a terminal") {
		t.Errorf("error should mention non-TTY; got: %v", err)
	}
	if !strings.Contains(stderr.String(), "alpha") || !strings.Contains(stderr.String(), "beta") {
		t.Errorf("expected instance names listed on stderr; got: %q", stderr.String())
	}
}

// TestRunDestroy_FromInsideWritesLandingPath confirms that destroying from
// inside an instance writes the workspace root to NIWA_RESPONSE_FILE so
// the shell wrapper can cd the user out of the deleted directory.
func TestRunDestroy_FromInsideWritesLandingPath(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instDir := filepath.Join(root, "alpha")
	chdirTo(t, instDir)

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	respFile := filepath.Join(tmp, "niwa-response")
	withResponseFile(t, respFile)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// chdir out of the instance before cleanup since we're about to delete it.
	t.Cleanup(func() { _ = os.Chdir(root) })

	if err := runDestroy(cmd, nil); err != nil {
		t.Fatalf("runDestroy: %v", err)
	}

	data, err := os.ReadFile(respFile)
	if err != nil {
		t.Fatalf("response file: %v", err)
	}
	got := strings.TrimRight(string(data), "\n")
	if got != root {
		t.Errorf("landing path: got %q, want %q (workspace root)", got, root)
	}
}

// TestRunDestroy_PerInstanceScansForUnpushedWorkAndRefusesNonTTY confirms
// that runDestroyInstance now runs the comprehensive non-pushed-work scan
// (ScanInstance, not the narrower CheckUncommittedChanges) when --force
// is absent. With stdin not a TTY (always true in unit tests), the
// typed-confirmation path can't fire, so the command must refuse with the
// non-TTY error rather than proceeding silently.
//
// Setup uses a real git repo with an unpushed commit so the scan finds
// LossUnpushedCommits — a state the OLD CheckUncommittedChanges helper
// would have missed.
func TestRunDestroy_PerInstanceScansForUnpushedWorkAndRefusesNonTTY(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)

	repoDir := filepath.Join(root, "alpha", "myrepo")
	scanInitGitRepoForCLITest(t, repoDir)
	// Create + commit a file beyond the initial push so the branch is
	// ahead of upstream — LossUnpushedCommits.
	gitRunForCLITest(t, repoDir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "second.txt"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForCLITest(t, repoDir, "add", "second.txt")
	gitRunForCLITest(t, repoDir, "commit", "-m", "ahead")

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	err := runDestroy(cmd, nil)
	if err == nil {
		t.Fatal("expected non-TTY refusal when scan finds unpushed work")
	}
	if !strings.Contains(err.Error(), "unpushed work") {
		t.Errorf("error should mention unpushed work; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("error should mention non-TTY; got: %v", err)
	}
	// Stderr should contain the FormatScans rendering — proves the
	// broader scan ran rather than the narrow CheckUncommittedChanges.
	if !strings.Contains(stderr.String(), "unpushed") && !strings.Contains(stderr.String(), "ahead") {
		t.Errorf("stderr should contain scan output (unpushed/ahead); got: %q", stderr.String())
	}
	// And the instance must NOT be removed.
	if _, err := os.Stat(filepath.Join(root, "alpha")); err != nil {
		t.Errorf("instance should still exist after refusal; got Stat err = %v", err)
	}
}

// TestRunDestroy_PerInstanceForceSkipsScan confirms that --force still
// bypasses the broader scan, matching the documented bypass semantics.
func TestRunDestroy_PerInstanceForceSkipsScan(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)

	repoDir := filepath.Join(root, "alpha", "myrepo")
	scanInitGitRepoForCLITest(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "second.txt"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForCLITest(t, repoDir, "add", "second.txt")
	gitRunForCLITest(t, repoDir, "commit", "-m", "ahead")

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	prevForce := destroyForce
	destroyForce = true
	t.Cleanup(func() { destroyForce = prevForce })

	if err := runDestroy(cmd, []string{"alpha"}); err != nil {
		t.Fatalf("runDestroy with --force should succeed even with unpushed work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Errorf("instance should be removed; got: %v", err)
	}
}

// scanInitGitRepoForCLITest is a local copy of the workspace package's
// scanInitGitRepo — duplicated here because the cli package can't import
// internal test helpers from another package. Initializes a git repo
// with one committed file pushed to a bare remote.
func scanInitGitRepoForCLITest(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := dir + ".bare"
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "init", "--bare", "--initial-branch=main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	gitRunForCLITest(t, dir, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForCLITest(t, dir, "add", "README.md")
	gitRunForCLITest(t, dir, "commit", "-m", "initial")
	gitRunForCLITest(t, dir, "remote", "add", "origin", bare)
	gitRunForCLITest(t, dir, "push", "-u", "origin", "main")
}

func gitRunForCLITest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@example.com",
		"HOME="+t.TempDir(),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestRunDestroy_NamedInstanceDoesNotWriteLandingPath confirms that
// destroying a named instance from the workspace root does NOT write
// NIWA_RESPONSE_FILE — the user's cwd (workspace root) is still valid.
func TestRunDestroy_NamedInstanceDoesNotWriteLandingPath(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	respFile := filepath.Join(tmp, "niwa-response")
	if err := os.WriteFile(respFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	withResponseFile(t, respFile)

	cmd := destroyCmd
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runDestroy(cmd, []string{"alpha"}); err != nil {
		t.Fatalf("runDestroy: %v", err)
	}

	data, _ := os.ReadFile(respFile)
	if len(data) != 0 {
		t.Errorf("response file should be empty for named-instance destroy; got: %q", data)
	}
}
