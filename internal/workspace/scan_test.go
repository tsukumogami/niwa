package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitAvailable returns false if the git binary isn't on PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// scanInitGitRepo creates a git repo at dir with a single committed file
// and an "origin" remote pointing to a bare repo created in a sibling
// directory. The bare repo lives next to dir as `<dir>.bare`. Returns
// the path to the bare remote so tests can assert "ahead-of-upstream"
// states by pushing or not pushing.
func scanInitGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Bare remote.
	bare := dir + ".bare"
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "init", "--bare", "--initial-branch=main", bare).Run(); err != nil {
		t.Fatalf("init bare: %v", err)
	}

	// Working repo.
	run := func(args ...string) {
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
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	run("remote", "add", "origin", bare)
	run("push", "-u", "origin", "main")
	return bare
}

func gitRun(t *testing.T, dir string, args ...string) {
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

// setupInstanceWithRepo wires a destroy-style instance dir containing one
// repo at <instance>/<repoName>, pushed to origin so the baseline is clean.
func setupInstanceWithRepo(t *testing.T, instanceName, repoName string) (instanceDir, repoDir string) {
	t.Helper()
	root := destroySetupWorkspace(t)
	instanceDir = destroySetupInstance(t, root, instanceName)
	repoDir = filepath.Join(instanceDir, repoName)
	scanInitGitRepo(t, repoDir)
	return instanceDir, repoDir
}

// --- ScanInstance ---

func TestScanInstance_CleanRepo(t *testing.T) {
	instanceDir, _ := setupInstanceWithRepo(t, "alpha", "myrepo")

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	if scan.HasLoss() {
		t.Errorf("expected no losses on clean repo; got %+v", scan)
	}
	if scan.InstanceName != "alpha" {
		t.Errorf("InstanceName = %q, want alpha", scan.InstanceName)
	}
}

func TestScanInstance_ModifiedFile(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	if !scan.HasLoss() {
		t.Fatalf("expected loss on modified file; got %+v", scan)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossWorkingTreeDirty {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossWorkingTreeDirty; got %+v", scan)
	}
}

func TestScanInstance_UntrackedFile(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	if err := os.WriteFile(filepath.Join(repoDir, "newfile.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossUntracked {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossUntracked; got %+v", scan)
	}
}

func TestScanInstance_UnpushedCommits(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	if err := os.WriteFile(filepath.Join(repoDir, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", "second.txt")
	gitRun(t, repoDir, "commit", "-m", "second")

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossUnpushedCommits {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossUnpushedCommits; got %+v", scan)
	}
}

func TestScanInstance_LocalOnlyBranch(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	gitRun(t, repoDir, "checkout", "-b", "feature/local-only")

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossLocalOnlyBranch && l.Branch == "feature/local-only" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossLocalOnlyBranch; got %+v", scan)
	}
}

func TestScanInstance_Stash(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "stash")

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossStash {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossStash; got %+v", scan)
	}
}

func TestScanInstance_DetachedHead(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	// Create a commit, then detach with a new commit on top.
	if err := os.WriteFile(filepath.Join(repoDir, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", "second.txt")
	gitRun(t, repoDir, "commit", "-m", "second")
	gitRun(t, repoDir, "push", "origin", "main")

	// Move to detached HEAD on the previous commit, then add an orphan commit.
	gitRun(t, repoDir, "checkout", "HEAD~0") // detach at current HEAD
	if err := os.WriteFile(filepath.Join(repoDir, "orphan.txt"), []byte("orphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", "orphan.txt")
	gitRun(t, repoDir, "commit", "-m", "orphan")

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Kind == LossDetachedOrphan {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected LossDetachedOrphan; got %+v", scan)
	}
}

func TestScanInstance_Worktree(t *testing.T) {
	instanceDir, repoDir := setupInstanceWithRepo(t, "alpha", "myrepo")
	wtDir := filepath.Join(instanceDir, ".niwa", "worktrees", "myrepo-session1")
	if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "worktree", "add", "-b", "session/abc", wtDir)
	// Make the worktree dirty so we have something to detect inside it.
	if err := os.WriteFile(filepath.Join(wtDir, "wt.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scan, err := ScanInstance(instanceDir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	if !scan.HasLoss() {
		t.Fatalf("expected loss in worktree; got %+v", scan)
	}
	// The losses should include either local-only branch (session/abc has no upstream)
	// or untracked (wt.txt) — both qualify; we just need *something* worktree-flagged.
	found := false
	for _, r := range scan.Repos {
		for _, l := range r.Losses {
			if l.Path != "" && (l.Kind == LossUntracked || l.Kind == LossLocalOnlyBranch) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected loss with non-empty Path (worktree); got %+v", scan)
	}
}

func TestScanInstance_OrphanInstanceDir(t *testing.T) {
	root := destroySetupWorkspace(t)
	dir := filepath.Join(root, "orphan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No instance.json — should be flagged as Skipped.

	scan, err := ScanInstance(dir)
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	if !scan.HasLoss() {
		t.Errorf("expected orphan instance to be flagged as dirty; got %+v", scan)
	}
}

// --- ScanInstancesParallel ---

func TestScanInstancesParallel_PreservesOrder(t *testing.T) {
	root := destroySetupWorkspace(t)
	a := destroySetupInstance(t, root, "alpha")
	b := destroySetupInstance(t, root, "beta")
	c := destroySetupInstance(t, root, "gamma")
	scanInitGitRepo(t, filepath.Join(a, "r1"))
	scanInitGitRepo(t, filepath.Join(b, "r2"))
	scanInitGitRepo(t, filepath.Join(c, "r3"))

	scans, err := ScanInstancesParallel(root, []string{a, b, c}, 2)
	if err != nil {
		t.Fatalf("ScanInstancesParallel: %v", err)
	}
	if len(scans) != 3 {
		t.Fatalf("got %d scans, want 3", len(scans))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, w := range wantNames {
		if scans[i].InstanceName != w {
			t.Errorf("scans[%d].InstanceName = %q, want %q", i, scans[i].InstanceName, w)
		}
	}
}

// --- FormatScans ---

func TestFormatScans_NoLossSaysSo(t *testing.T) {
	var buf bytes.Buffer
	FormatScans([]InstanceScan{{InstanceName: "alpha"}}, &buf, "myws")
	if !strings.Contains(buf.String(), "No unpushed work detected") {
		t.Errorf("expected no-loss summary; got: %s", buf.String())
	}
}

func TestFormatScans_RendersLossWithConfirmPrompt(t *testing.T) {
	scan := InstanceScan{
		InstanceName: "alpha",
		Repos: []RepoScan{{
			Name: "myrepo",
			Losses: []Loss{{
				Kind:   LossUnpushedCommits,
				Branch: "feature/foo",
				Detail: "ahead 2",
			}},
		}},
	}
	var buf bytes.Buffer
	FormatScans([]InstanceScan{scan}, &buf, "myws")
	out := buf.String()
	for _, want := range []string{
		"unpushed work",
		"alpha",
		"myrepo",
		"feature/foo",
		`Type "myws" to confirm`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatScans output missing %q; full output:\n%s", want, out)
		}
	}
}

func TestFormatScans_CleanInstanceShownAsClean(t *testing.T) {
	scans := []InstanceScan{
		{
			InstanceName: "alpha",
			Repos: []RepoScan{{
				Name: "myrepo",
				Losses: []Loss{{
					Kind:   LossWorkingTreeDirty,
					Detail: "1 modified",
				}},
			}},
		},
		{InstanceName: "beta"},
	}
	var buf bytes.Buffer
	FormatScans(scans, &buf, "myws")
	out := buf.String()
	if !strings.Contains(out, "beta (clean)") {
		t.Errorf("expected 'beta (clean)' tag; got: %s", out)
	}
}
