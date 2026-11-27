package cli

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
)

// niwa destroy's scan reads each remote the way the removal pass reads a
// proof remote: from an empty scratch directory in no repository, against
// the URL in the repository's own remote.<name>.url, so no setting in the
// repository's configuration (an insteadOf, a url-scoped proxy) decides
// where the read goes. A remote that can't be read that way keeps the
// instance, naming the remote, and the scan never falls back to the local
// remote-tracking ref it can't refresh.
//
// Every repository and remote here is under t.TempDir().

// scanFileRemotes admits local-path remotes on the destroy's git calls, as
// the pass tests do.
func scanFileRemotes(t *testing.T) {
	t.Helper()
	prev := newRemovalEnv
	newRemovalEnv = func(r, home, pathEnv string) *removal.Env {
		return removal.NewEnv(r, home, pathEnv, removal.WithFileRemotes())
	}
	t.Cleanup(func() { newRemovalEnv = prev })
}

// rewoundRemoteInstance is destroyScanInstance with one more commit made in
// the clone and pushed, after which the remote's main is set back to the
// commit before it. The clone's own refs/remotes/origin/main still names the
// new commit. It also returns a second bare repository cloned from the
// remote while it still held that commit.
func rewoundRemoteInstance(t *testing.T) (root, clone, remote, second string) {
	t.Helper()
	root, clone = destroyScanInstance(t, nil)
	remote = strings.TrimSpace(passGitOut(t, clone, "config", "--get", "remote.origin.url"))
	base := strings.TrimSpace(passGitOut(t, clone, "rev-parse", "HEAD"))
	writeTestFile(t, filepath.Join(clone, "work.txt"), "only pushed once\n")
	passGit(t, clone, "add", "work.txt")
	passGit(t, clone, "commit", "-q", "-m", "work")
	passGit(t, clone, "push", "-q", "origin", "main")
	second = filepath.Join(canonicalTempDir(t), "second.git")
	passGit(t, root, "clone", "-q", "--bare", remote, second)
	passGit(t, remote, "update-ref", "refs/heads/main", base)
	return root, clone, remote, second
}

func assertScanKept(t *testing.T, root string, err error, stderr string, want ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("an unforced destroy went ahead; want it kept with the remote named\nstderr:\n%s", stderr)
	}
	out := err.Error() + "\n" + stderr
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("destroy output does not name %q:\n%s", w, out)
		}
	}
	assertInstancesExist(t, root, "alpha")
}

// The plain case, with no redirect anywhere in the repository's
// configuration: the remote is reachable at its own URL and its main was
// rewound behind the clone's, so the clone's last commit is on no remote
// branch while its own refs/remotes/origin/main still names it. The scan
// reads the remote, finds the commit missing and keeps the instance.
func TestDestroyScanNetwork_ARewoundReachableRemoteKeeps(t *testing.T) {
	root, clone, remote, _ := rewoundRemoteInstance(t)
	scanFileRemotes(t)
	// git config --get-regexp exits 1 when nothing matches, which is the
	// answer wanted here.
	redirects := exec.Command("git", "config", "--get-regexp", `^url\.|^http\.`)
	redirects.Dir = clone
	redirects.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if got, err := redirects.Output(); err == nil || len(got) > 0 {
		t.Fatalf("the clone carries a redirect, so this is not the plain case: %q (%v)", got, err)
	}
	head := strings.TrimSpace(passGitOut(t, clone, "rev-parse", "HEAD"))
	if tracking := strings.TrimSpace(passGitOut(t, clone, "rev-parse", "refs/remotes/origin/main")); tracking != head {
		t.Fatalf("the clone's origin/main is %s, want its own HEAD %s", tracking, head)
	}
	if onRemote := strings.TrimSpace(passGitOut(t, remote, "rev-parse", "refs/heads/main")); onRemote == head {
		t.Fatalf("the remote's main still holds %s; it was not rewound", head)
	}
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertScanKept(t, root, err, stderr.String(), "origin")
}

func TestDestroyScanNetwork_TheRepositorysRedirectsDontDecideTheRead(t *testing.T) {
	t.Run("insteadOf", func(t *testing.T) {
		root, clone, remote, second := rewoundRemoteInstance(t)
		scanFileRemotes(t)
		passGit(t, clone, "config", "url."+second+".insteadOf", remote)
		stderr := quietDestroyCmd()

		err := runDestroy(destroyCmd, []string{"alpha"})
		assertScanKept(t, root, err, stderr.String(), "origin")
	})
	t.Run("url-scoped http proxy", func(t *testing.T) {
		root, clone, _, _ := rewoundRemoteInstance(t)
		scanFileRemotes(t)
		url := "http://" + closedLocalAddr(t) + "/app.git"
		passGit(t, clone, "config", "remote.origin.url", url)
		passGit(t, clone, "config", "http."+url+".proxy", "http://"+closedLocalAddr(t))
		stderr := quietDestroyCmd()

		err := runDestroy(destroyCmd, []string{"alpha"})
		assertScanKept(t, root, err, stderr.String(), "origin")
	})
}

func TestDestroyScanNetwork_AURLOnlyAnInsteadOfResolvesKeeps(t *testing.T) {
	root, clone := destroyScanInstance(t, nil)
	scanFileRemotes(t)
	remote := strings.TrimSpace(passGitOut(t, clone, "config", "--get", "remote.origin.url"))
	passGit(t, clone, "config", "remote.origin.url", "niwa-alias::app")
	passGit(t, clone, "config", "url."+remote+".insteadOf", "niwa-alias::app")
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertScanKept(t, root, err, stderr.String(), "origin", "not allowed")
}

func TestDestroyScanNetwork_AFullyPushedReachableRemoteStillDestroys(t *testing.T) {
	root, _ := destroyScanInstance(t, nil)
	scanFileRemotes(t)
	stderr := quietDestroyCmd()

	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy of a fully pushed instance: %v\nstderr:\n%s", err, stderr.String())
	}
	assertInstanceGone(t, filepath.Join(root, "alpha"))
}

// plantRefHooks writes reference-transaction, post-merge and pre-push hooks
// into the repository at repo, each appending its own name to marker.
func plantRefHooks(t *testing.T, repo, marker string) {
	t.Helper()
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nbasename \"$0\" >> '" + marker + "'\n"
	for _, h := range []string{"reference-transaction", "post-merge", "pre-push"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// The remote's main moved past the clone's, so the commit the remote names
// is missing from the clone and the scan fetches it into a scratch store to
// judge the clone's main against it. No hook planted in the clone runs: the
// fetch writes no ref in the repository, and nothing the scan does merges or
// pushes. The same hooks, planted in a second clone, do run on a plain fetch
// there, so the planted hooks are live.
func TestDestroyScanNetwork_AFetchForTheScanRunsNoHookInTheRepository(t *testing.T) {
	root, clone := destroyScanInstance(t, nil)
	scanFileRemotes(t)
	remote := strings.TrimSpace(passGitOut(t, clone, "config", "--get", "remote.origin.url"))
	control := filepath.Join(canonicalTempDir(t), "control")
	passGit(t, root, "clone", "-q", remote, control)

	other := filepath.Join(canonicalTempDir(t), "other")
	passGit(t, root, "clone", "-q", remote, other)
	writeTestFile(t, filepath.Join(other, "later.txt"), "pushed from elsewhere\n")
	passGit(t, other, "add", "later.txt")
	passGit(t, other, "commit", "-q", "-m", "later")
	passGit(t, other, "push", "-q", "origin", "main")
	later := strings.TrimSpace(passGitOut(t, other, "rev-parse", "HEAD"))
	missing := exec.Command("git", "cat-file", "-e", later+"^{commit}")
	missing.Dir = clone
	if err := missing.Run(); err == nil {
		t.Fatalf("the clone already holds %s; the scan would have nothing to fetch", later)
	}

	controlMarker := filepath.Join(t.TempDir(), "control-hooks-ran")
	plantRefHooks(t, control, controlMarker)
	passGit(t, control, "fetch", "-q", "origin")
	if _, err := os.Stat(controlMarker); err != nil {
		t.Fatalf("the planted hooks didn't run on a plain fetch in the control clone: %v", err)
	}

	marker := filepath.Join(t.TempDir(), "hooks-ran")
	plantRefHooks(t, clone, marker)
	log := destroyGitWrapper(t, "")
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	if got, readErr := os.ReadFile(marker); readErr == nil {
		t.Errorf("a hook planted in the scanned repository ran: %q", got)
	} else if !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if err != nil {
		t.Fatalf("destroy of an instance whose main is behind its remote's: %v\nstderr:\n%s", err, stderr.String())
	}
	assertInstanceGone(t, filepath.Join(root, "alpha"))

	raw, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatal(readErr)
	}
	fetched := false
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !containsArg(line, "fetch") {
			continue
		}
		if strings.Contains(line, clone) {
			t.Errorf("a fetch ran in the scanned repository: git %s", line)
		}
		fetched = fetched || strings.Contains(line, "--stdin")
	}
	if !fetched {
		t.Errorf("no fetch into a scratch store ran, so the missing commit wasn't fetched; git calls:\n%s", raw)
	}
}

// containsArg reports whether one of line's space-separated fields is arg.
func containsArg(line, arg string) bool {
	for _, f := range strings.Fields(line) {
		if f == arg {
			return true
		}
	}
	return false
}

// passGitOut is passGit returning git's standard output.
func passGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}

// closedLocalAddr is a loopback address nothing listens on.
func closedLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}
