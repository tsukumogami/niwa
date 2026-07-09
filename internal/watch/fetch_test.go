package watch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeSourceRepo builds a local git repo with a single commit whose tree
// exercises the fetch-hardening threat surface: a file marked export-ignore, a
// file marked filter=lfs, and a normal file. It returns the repo path and the
// head commit SHA.
func makeSourceRepo(t *testing.T) (repoDir, sha string) {
	t.Helper()
	repoDir = t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
			"GIT_LFS_SKIP_SMUDGE=1",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "--quiet")
	// Allow fetching a reachable SHA over local transport.
	run("config", "uploadpack.allowReachableSHA1InWant", "true")
	run("config", "uploadpack.allowAnySHA1InWant", "true")

	writeFile(t, repoDir, "README.md", "hello world")
	// secret.txt is marked export-ignore: git archive would hide it, but a
	// checkout must include it (so a malicious file cannot be hidden from review).
	writeFile(t, repoDir, ".gitattributes", "secret.txt export-ignore\ndata.bin filter=lfs\n")
	writeFile(t, repoDir, "secret.txt", "SHOULD-BE-VISIBLE")
	writeFile(t, repoDir, "data.bin", "RAW-COMMITTED-BYTES")

	run("add", "-A")
	run("commit", "--quiet", "-m", "fixture")
	sha = run("rev-parse", "HEAD")
	return repoDir, sha
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPRHead_CheckoutIsFaithfulAndFilterNeutered(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	src, sha := makeSourceRepo(t)

	dst := filepath.Join(t.TempDir(), "checkout")
	if err := FetchPRHead(context.Background(), src, sha, dst, ""); err != nil {
		t.Fatalf("FetchPRHead: %v", err)
	}

	// The normal file is present with its content.
	if got := readFile(t, dst, "README.md"); got != "hello world" {
		t.Errorf("README.md = %q", got)
	}
	// export-ignore did NOT hide the file: a filter-neutered checkout (not
	// git archive) includes it, so a malicious file can't be hidden from review.
	if got := readFile(t, dst, "secret.txt"); got != "SHOULD-BE-VISIBLE" {
		t.Errorf("export-ignore file missing or altered: %q (git archive would hide it)", got)
	}
	// The LFS-attributed file is present with its RAW committed bytes -- the
	// smudge did not run (which would have replaced or fetched content).
	if got := readFile(t, dst, "data.bin"); got != "RAW-COMMITTED-BYTES" {
		t.Errorf("data.bin = %q, want raw bytes (no LFS smudge)", got)
	}
}

func TestFetchPRHead_RejectsMalformedSHA(t *testing.T) {
	for _, bad := range []string{"", "HEAD", "not-hex", "../../etc", "$(rm -rf)", "abc"} {
		if err := FetchPRHead(context.Background(), "https://example.test/r.git", bad, t.TempDir(), ""); err == nil {
			t.Errorf("FetchPRHead accepted malformed SHA %q", bad)
		}
	}
}

func TestHardenedGitEnv_ExcludesCredentialsAndConfig(t *testing.T) {
	env := hardenedGitEnv("/scratch/home")
	joined := strings.Join(env, "\n")
	// Isolated config + skipped smudge.
	for _, want := range []string{"HOME=/scratch/home", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hardened env missing %q", want)
		}
	}
	// No ambient GH_/GITHUB_/SSH credential leaks (the base env is minimal).
	for _, kv := range env {
		name := kv[:strings.IndexByte(kv, '=')]
		if strings.HasPrefix(name, "GH_") || strings.HasPrefix(name, "GITHUB_") || name == "SSH_AUTH_SOCK" {
			t.Errorf("hardened git env leaked credential var %q", name)
		}
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}
