package watch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv is the environment the tests' own git runs use: no global or system
// config and no ambient GIT_* variable (a GIT_SSH or GIT_SSH_COMMAND would outrank
// a planted core.sshCommand), so a developer's settings never decide whether a
// plant fires.
func gitEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// gitIn runs git in dir and returns its output; failOK lets a planted program make
// the command fail without failing the test.
func gitIn(t *testing.T, dir string, failOK bool, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil && !failOK {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoCommitRepo builds a source repo with two commits and returns it as a file://
// URL, a form url.<base>.insteadOf rewrites (a bare path isn't). The second commit
// edits README.md, deletes old.txt and adds new.txt.
func twoCommitRepo(t *testing.T) (remote, first, second string) {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, false, "init", "--quiet")
	gitIn(t, repo, false, "config", "uploadpack.allowAnySHA1InWant", "true")
	writeFile(t, repo, "README.md", "one")
	writeFile(t, repo, "old.txt", "old")
	gitIn(t, repo, false, "add", "-A")
	gitIn(t, repo, false, "commit", "--quiet", "-m", "first")
	first = gitIn(t, repo, false, "rev-parse", "HEAD")
	writeFile(t, repo, "README.md", "two")
	if err := os.Remove(filepath.Join(repo, "old.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "new.txt", "new")
	gitIn(t, repo, false, "add", "-A")
	gitIn(t, repo, false, "commit", "--quiet", "-m", "second")
	second = gitIn(t, repo, false, "rev-parse", "HEAD")
	return "file://" + repo, first, second
}

// markerScript writes an executable script that creates markerDir/name and exits 0,
// so a hook that could veto (reference-transaction) lets the git run go on.
func markerScript(t *testing.T, dir, markerDir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name+".sh")
	body := fmt.Sprintf("#!/bin/sh\n: > %s\nexit 0\n", shellQuote(filepath.Join(markerDir, name)))
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// markersPresent lists the marker files in markerDir.
func markersPresent(t *testing.T, markerDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(markerDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// clearMarkers removes every marker file.
func clearMarkers(t *testing.T, markerDir string) {
	t.Helper()
	for _, n := range markersPresent(t, markerDir) {
		if err := os.Remove(filepath.Join(markerDir, n)); err != nil {
			t.Fatal(err)
		}
	}
}

// requireMarker fails unless markerDir/name exists.
func requireMarker(t *testing.T, markerDir, name, step string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(markerDir, name)); err != nil {
		t.Fatalf("soundness: %s did not run the planted %s (present: %v); the plant is not live on this git", step, name, markersPresent(t, markerDir))
	}
}

// plantGitDir plants every program a git dir can make git run: core.fsmonitor,
// core.sshCommand with a url.<base>.insteadOf that sends remote over ssh, an
// include.path that sets a second core.fsmonitor, and post-checkout and
// reference-transaction hooks. It proves each plant live with plain git runs in
// workTree, then clears the markers. It returns the planted config keys and hook
// names.
func plantGitDir(t *testing.T, gitDir, workTree, remote, commit, scripts, markerDir string) (keys, hooks []string) {
	t.Helper()
	cfg := func(args ...string) {
		t.Helper()
		gitIn(t, workTree, false, append([]string{"--git-dir=" + gitDir, "config"}, args...)...)
	}
	run := func(failOK bool, args ...string) {
		t.Helper()
		gitIn(t, workTree, failOK, append([]string{"--git-dir=" + gitDir, "--work-tree=" + workTree}, args...)...)
	}

	// core.fsmonitor, proven by a plain git status.
	cfg("core.fsmonitor", markerScript(t, scripts, markerDir, "fsmonitor"))
	run(true, "status")
	requireMarker(t, markerDir, "fsmonitor", "git status")

	// core.sshCommand behind an insteadOf that sends the remote over ssh, proven by a
	// plain git fetch of the remote.
	cfg("core.sshCommand", markerScript(t, scripts, markerDir, "ssh"))
	cfg("url.ssh://git.example.invalid/repo.git.insteadOf", remote)
	run(true, "fetch", remote)
	requireMarker(t, markerDir, "ssh", "git fetch")

	// An include that sets a second fsmonitor, proven by git status again.
	inc := filepath.Join(scripts, "included.gitconfig")
	body := fmt.Sprintf("[core]\n\tfsmonitor = %s\n", markerScript(t, scripts, markerDir, "fsmonitor-included"))
	if err := os.WriteFile(inc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg("include.path", inc)
	run(true, "status")
	requireMarker(t, markerDir, "fsmonitor-included", "git status through include.path")

	// Hooks, proven by a plain checkout onto a new branch, which runs post-checkout
	// and creates the branch through a reference transaction.
	hooksDir := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"post-checkout", "reference-transaction"} {
		src := markerScript(t, scripts, markerDir, h)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(hooksDir, h), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run(true, "-c", "core.fsmonitor=", "checkout", "-q", "-f", "-b", "plant-probe", commit)
	requireMarker(t, markerDir, "post-checkout", "git checkout")
	requireMarker(t, markerDir, "reference-transaction", "git checkout")

	clearMarkers(t, markerDir)
	return []string{"core.fsmonitor", "core.sshcommand", "insteadof", "include.path"},
		[]string{"post-checkout", "reference-transaction"}
}

// assertTreeAtSecond checks that dir holds the second commit's tree.
func assertTreeAtSecond(t *testing.T, dir string) {
	t.Helper()
	if got := readFile(t, dir, "README.md"); got != "two" {
		t.Errorf("README.md = %q, want the second commit's content", got)
	}
	if got := readFile(t, dir, "new.txt"); got != "new" {
		t.Errorf("new.txt = %q, want the second commit's content", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "old.txt")); err == nil {
		t.Error("old.txt, deleted in the second commit, is still in the clone")
	}
}

// TestFetchPRHead_IgnoresExistingGitDir: a clone whose git dir carries every
// planted program git could run is fetched into again without running any of them,
// ends at the new commit, and keeps none of the plants.
func TestFetchPRHead_IgnoresExistingGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src, first, second := twoCommitRepo(t)
	clone := filepath.Join(t.TempDir(), "pr-clone")
	if err := FetchPRHead(context.Background(), src, first, clone, ""); err != nil {
		t.Fatalf("first FetchPRHead: %v", err)
	}
	gitDir := filepath.Join(clone, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		// The fresh git dir could not be moved in (another filesystem); give the
		// clone one, as an agent could.
		gitIn(t, clone, false, "init", "--quiet")
	}
	scripts, markerDir := t.TempDir(), t.TempDir()
	keys, hooks := plantGitDir(t, gitDir, clone, src, first, scripts, markerDir)

	if err := FetchPRHead(context.Background(), src, second, clone, ""); err != nil {
		t.Fatalf("second FetchPRHead: %v", err)
	}
	if got := markersPresent(t, markerDir); len(got) != 0 {
		t.Errorf("FetchPRHead ran planted programs: %v", got)
	}
	assertTreeAtSecond(t, clone)

	if _, err := os.Stat(gitDir); err == nil {
		cfg, err := os.ReadFile(filepath.Join(gitDir, "config"))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(cfg))
		for _, k := range keys {
			key := k[strings.LastIndex(k, ".")+1:]
			if strings.Contains(lower, key) {
				t.Errorf("the clone's .git/config still carries %s:\n%s", k, cfg)
			}
		}
		for _, h := range hooks {
			if _, err := os.Stat(filepath.Join(gitDir, "hooks", h)); err == nil {
				t.Errorf("the clone's .git/hooks still holds %s", h)
			}
		}
	}
}

// snapshotTree hashes every file under dir, keyed by relative path, so a test can
// tell whether anything changed.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "symlink:" + target
		case d.IsDir():
			out[rel] = "dir"
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = fmt.Sprintf("%o:%x", info.Mode(), sha256.Sum256(data))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestFetchPRHead_ReplacesGitDirSymlinkAndGitFile: a clone whose .git is a symlink
// to a git dir elsewhere, or a "gitdir:" file naming one, is fetched into without
// following either: the fetch succeeds, the other git dir is unchanged byte for
// byte, and nothing planted in it runs.
func TestFetchPRHead_ReplacesGitDirSymlinkAndGitFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, kind := range []string{"symlink", "gitdir file"} {
		t.Run(kind, func(t *testing.T) {
			src, first, second := twoCommitRepo(t)
			clone := filepath.Join(t.TempDir(), "pr-clone")
			if err := FetchPRHead(context.Background(), src, first, clone, ""); err != nil {
				t.Fatalf("first FetchPRHead: %v", err)
			}

			// A git dir outside the clone, with every plant live in it.
			elsewhere := t.TempDir()
			gitIn(t, elsewhere, false, "init", "--quiet")
			gitIn(t, elsewhere, false, "--git-dir="+filepath.Join(elsewhere, ".git"), "--work-tree="+clone, "fetch", "--quiet", src, first)
			outsideGitDir := filepath.Join(elsewhere, ".git")
			scripts, markerDir := t.TempDir(), t.TempDir()
			plantGitDir(t, outsideGitDir, clone, src, first, scripts, markerDir)

			dotGit := filepath.Join(clone, ".git")
			if err := os.RemoveAll(dotGit); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink(outsideGitDir, dotGit); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(dotGit, []byte("gitdir: "+outsideGitDir+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, elsewhere)

			if err := FetchPRHead(context.Background(), src, second, clone, ""); err != nil {
				t.Fatalf("second FetchPRHead: %v", err)
			}
			if got := markersPresent(t, markerDir); len(got) != 0 {
				t.Errorf("FetchPRHead ran programs planted in the redirected git dir: %v", got)
			}
			after := snapshotTree(t, elsewhere)
			if len(before) != len(after) {
				t.Errorf("the redirected git dir changed: %d entries before, %d after", len(before), len(after))
			}
			for p, h := range before {
				if after[p] != h {
					t.Errorf("the redirected git dir changed at %s", p)
				}
			}
			assertTreeAtSecond(t, clone)
			if fi, err := os.Lstat(dotGit); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				t.Error("the clone's .git is still a symlink")
			}
			if data, err := os.ReadFile(dotGit); err == nil && bytes.HasPrefix(data, []byte("gitdir:")) {
				t.Error("the clone's .git is still a gitdir file")
			}
		})
	}
}

// TestFetchPRHead_ReplacesSymlinkedCheckoutDir: a checkout dir that is itself a
// symlink is replaced, not followed, so the PR's files never land where it points.
func TestFetchPRHead_ReplacesSymlinkedCheckoutDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src, _, second := twoCommitRepo(t)
	elsewhere := t.TempDir()
	writeFile(t, elsewhere, "keep.txt", "keep")
	clone := filepath.Join(t.TempDir(), "pr-clone")
	if err := os.Symlink(elsewhere, clone); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, elsewhere)
	if err := FetchPRHead(context.Background(), src, second, clone, ""); err != nil {
		t.Fatalf("FetchPRHead: %v", err)
	}
	if fi, err := os.Lstat(clone); err != nil || !fi.IsDir() {
		t.Fatalf("checkout dir is not a real directory after the fetch (err %v)", err)
	}
	assertTreeAtSecond(t, clone)
	after := snapshotTree(t, elsewhere)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("the symlink's target changed:\nbefore %v\nafter  %v", before, after)
	}
}

// TestGitHardeningConfig_NeutersFsmonitorAndSSHCommand pins the two entries that
// keep a configured fsmonitor or ssh program from running, in the hardening list
// and so in the fetch and checkout argv.
func TestGitHardeningConfig_NeutersFsmonitorAndSSHCommand(t *testing.T) {
	want := [][2]string{
		{"-c", "core.fsmonitor="},
		{"-c", "core.sshCommand=" + pinnedSSHCommand},
		{"-c", "core.hooksPath=/dev/null"},
		{"-c", "protocol.ext.allow=never"},
	}
	hasPair := func(argv []string, pair [2]string) bool {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == pair[0] && argv[i+1] == pair[1] {
				return true
			}
		}
		return false
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	for name, argv := range map[string][]string{
		"gitHardeningConfig":   gitHardeningConfig,
		"hardenedFetchArgs":    hardenedFetchArgs("https://github.com/acme/widget.git", sha),
		"hardenedCheckoutArgs": hardenedCheckoutArgs(sha),
	} {
		for _, pair := range want {
			if !hasPair(argv, pair) {
				t.Errorf("%s lacks %s %s: %q", name, pair[0], pair[1], argv)
			}
		}
	}
	if pinnedSSHCommand == "" {
		t.Error("the pinned ssh command must not be empty")
	}
}
