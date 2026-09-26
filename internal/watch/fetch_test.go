package watch

import (
	"context"
	"encoding/base64"
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
	// The LFS-attributed file is checked out as its RAW committed blob -- the
	// smudge did not run. We compare against the committed blob read with
	// cat-file (which never smudges), so the check is deterministic whether or
	// not git-lfs is installed on the host: if git-lfs is present, the fixture
	// commit stored an LFS pointer and FetchPRHead must reproduce that pointer
	// (not download the real content); if absent, it stored the raw bytes. A
	// smudge on our checkout would alter the content and fail this.
	committedBlob := gitCatFileBlob(t, src, "HEAD:data.bin")
	if got := readFile(t, dst, "data.bin"); got != committedBlob {
		t.Errorf("data.bin checkout %q != committed blob %q (a smudge altered it)", got, committedBlob)
	}
}

// gitCatFileBlob returns the raw committed blob at rev (e.g. "HEAD:path").
// cat-file never applies smudge filters, so it is the ground-truth committed
// content.
func gitCatFileBlob(t *testing.T, repoDir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoDir, "cat-file", "-p", rev)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git cat-file %s: %v\n%s", rev, err, out)
	}
	return string(out)
}

func TestFetchPRHead_RejectsMalformedSHA(t *testing.T) {
	for _, bad := range []string{"", "HEAD", "not-hex", "../../etc", "$(rm -rf)", "abc"} {
		if err := FetchPRHead(context.Background(), "https://example.test/r.git", bad, t.TempDir(), ""); err == nil {
			t.Errorf("FetchPRHead accepted malformed SHA %q", bad)
		}
	}
}

func TestHardenedGitEnv_ExcludesCredentialsAndConfig(t *testing.T) {
	env := hardenedGitEnv("/scratch/home", "")
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

// TestFetchToken_NeverInArgv is the Finding-A regression guard: the auth token
// must ride the environment (GIT_CONFIG_VALUE_0), never the git command line.
func TestFetchToken_NeverInArgv(t *testing.T) {
	const token = "ghp_supersecrettoken"

	// The fetch argv is a pure function of remote + sha; the token is not an
	// argument to it.
	args := hardenedFetchArgs("https://github.com/acme/api.git", "abcdef1234")
	if strings.Contains(strings.Join(args, " "), token) {
		t.Fatalf("token leaked into git fetch argv: %v", args)
	}
	for _, a := range args {
		if strings.Contains(a, "extraheader") || strings.Contains(a, "Authorization") {
			t.Errorf("auth header must not be a git argv element, found %q", a)
		}
	}

	// The token is carried in the environment instead, as an HTTP Basic auth
	// header value (username x-access-token, token as password) -- the form
	// GitHub's git transport accepts. A Bearer header is rejected by the git
	// endpoint (see hardenedGitEnv).
	env := hardenedGitEnv("/scratch/home", token)
	wantCred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	wantVal := "GIT_CONFIG_VALUE_0=Authorization: Basic " + wantCred
	var found bool
	for _, kv := range env {
		if kv == wantVal {
			found = true
		}
		if strings.Contains(kv, "Bearer") {
			t.Errorf("Bearer header is rejected by the git transport; must use Basic, got %q", kv)
		}
	}
	if !found {
		t.Errorf("expected Basic x-access-token header in env, want %q", wantVal)
	}
	// Sanity: the value decodes back to the x-access-token:token credential.
	dec, _ := base64.StdEncoding.DecodeString(wantCred)
	if string(dec) != "x-access-token:"+token {
		t.Errorf("credential encoding mismatch: %q", dec)
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
