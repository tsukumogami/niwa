package gitexclude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderNiwaBlock_EmptyInput(t *testing.T) {
	out := string(renderNiwaBlock(nil))
	if !strings.Contains(out, niwaExcludeBegin) || !strings.Contains(out, niwaExcludeEnd) {
		t.Fatalf("expected niwa markers in output, got:\n%s", out)
	}
	for _, p := range niwaExcludePatterns {
		if !strings.Contains(out, p) {
			t.Errorf("expected pattern %q in output, got:\n%s", p, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline, got %q", out)
	}
}

func TestRenderNiwaBlock_PreservesUserContent(t *testing.T) {
	existing := []byte("# my own ignores\nbuild/\n*.tmp\n")
	out := string(renderNiwaBlock(existing))

	for _, want := range []string{"# my own ignores", "build/", "*.tmp"} {
		if !strings.Contains(out, want) {
			t.Errorf("user content %q was not preserved, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, niwaExcludeBegin) {
		t.Errorf("niwa block missing, got:\n%s", out)
	}
	// User content must come before the niwa block (block appended at end).
	if strings.Index(out, "build/") > strings.Index(out, niwaExcludeBegin) {
		t.Errorf("expected user content before niwa block, got:\n%s", out)
	}
}

func TestRenderNiwaBlock_Idempotent(t *testing.T) {
	inputs := [][]byte{
		nil,
		[]byte(""),
		[]byte("build/\n"),
		[]byte("build/"), // no trailing newline
		[]byte("# header\n\nbuild/\n*.tmp\n"),
	}
	for _, in := range inputs {
		once := renderNiwaBlock(in)
		twice := renderNiwaBlock(once)
		if string(once) != string(twice) {
			t.Errorf("renderNiwaBlock not idempotent for %q:\nonce:\n%s\ntwice:\n%s", in, once, twice)
		}
	}
}

func TestRenderNiwaBlock_ReplacesInPlace(t *testing.T) {
	// A file whose niwa block holds a stale pattern, with user content on both
	// sides, must end with exactly the current patterns and keep both sides.
	existing := []byte("before-line\n" +
		niwaExcludeBegin + "\n" +
		"stale-pattern\n" +
		niwaExcludeEnd + "\n" +
		"after-line\n")
	out := string(renderNiwaBlock(existing))

	if strings.Contains(out, "stale-pattern") {
		t.Errorf("stale pattern was not removed, got:\n%s", out)
	}
	for _, want := range []string{"before-line", "after-line", "*.local*", ".niwa/"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	if n := strings.Count(out, niwaExcludeBegin); n != 1 {
		t.Errorf("expected exactly one niwa block, found %d, got:\n%s", n, out)
	}
}

func TestEnsureRepoExclude_PrimaryRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")

	if err := EnsureRepoExclude(repo); err != nil {
		t.Fatalf("EnsureRepoExclude: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("reading exclude: %v", err)
	}
	out := string(data)
	for _, want := range []string{niwaExcludeBegin, "*.local*", ".niwa/", niwaExcludeEnd} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in .git/info/exclude, got:\n%s", want, out)
		}
	}

	// Second call is a no-op (idempotent): content unchanged.
	if err := EnsureRepoExclude(repo); err != nil {
		t.Fatalf("second EnsureRepoExclude: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if string(data2) != out {
		t.Errorf("exclude changed on second call:\nfirst:\n%s\nsecond:\n%s", out, data2)
	}
}

func TestEnsureRepoExclude_CoversWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	runGit(t, root, "init", primary)
	// A worktree needs a commit to branch from.
	if err := os.WriteFile(filepath.Join(primary, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, primary, "add", "README")
	runGit(t, primary, "commit", "-m", "init")

	worktree := filepath.Join(root, "wt")
	runGit(t, primary, "worktree", "add", worktree, "-b", "wtbranch")

	// Record coverage from the worktree path. It must resolve to the shared
	// common dir and make niwa-authored files invisible in the worktree.
	if err := EnsureRepoExclude(worktree); err != nil {
		t.Fatalf("EnsureRepoExclude(worktree): %v", err)
	}

	// Plant niwa-style output in the worktree working tree.
	if err := os.MkdirAll(filepath.Join(worktree, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".niwa", "state"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.local.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out := gitStatusPorcelain(t, worktree); out != "" {
		t.Errorf("expected clean worktree status, got:\n%s", out)
	}

	// An uncovered file must still show, proving the exclude is scoped.
	if err := os.WriteFile(filepath.Join(worktree, "leak.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := gitStatusPorcelain(t, worktree); !strings.Contains(out, "leak.txt") {
		t.Errorf("expected uncovered leak.txt to show in status, got:\n%s", out)
	}
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status --porcelain: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
