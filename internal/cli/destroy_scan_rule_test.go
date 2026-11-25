package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// niwa destroy's scan starts git only through a builder made from
// gitCommand, runs the program-naming configuration check on every
// repository before its first git call there, and reads each repository's
// status with --ignore-submodules=all and --untracked-files=all, in each
// submodule and nested repository as in the parent.
//
// Every repository, remote, driver and marker file here is under
// t.TempDir(); nothing touches this checkout's own configuration.

// destroyScanInstance makes a workspace with one instance, alpha, holding a
// clone of a pushed repository at alpha/app, and returns the root and the
// clone. seed, when set, runs in the seed repository before its push.
func destroyScanInstance(t *testing.T, seed func(repo string)) (root, clone string) {
	t.Helper()
	root = destroyTestSetup(t, []string{"alpha"})
	remote := filepath.Join(canonicalTempDir(t), "app.git")
	passGit(t, root, "init", "-q", "--bare", "-b", "main", remote)
	src := filepath.Join(canonicalTempDir(t), "seed")
	passGit(t, root, "init", "-q", "-b", "main", src)
	writeTestFile(t, filepath.Join(src, "README"), "hi\n")
	passGit(t, src, "add", "README")
	if seed != nil {
		seed(src)
	}
	passGit(t, src, "commit", "-q", "-m", "init")
	passGit(t, src, "push", "-q", remote, "main")
	clone = filepath.Join(root, "alpha", "app")
	passGit(t, root, "clone", "-q", remote, clone)
	chdirTo(t, root)
	setDestroyFlags(t, false, false, "")
	return root, clone
}

func TestDestroyScanRule_FsmonitorInAnInstanceRepositoryNeverRuns(t *testing.T) {
	root, clone := destroyScanInstance(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with core.fsmonitor set; want it refused with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "core.fsmonitor") {
		t.Errorf("destroy output does not name core.fsmonitor:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

func TestDestroyScanRule_ACleanFilterDriverRefusesAnUnforcedDestroy(t *testing.T) {
	root, clone := destroyScanInstance(t, func(src string) {
		writeTestFile(t, filepath.Join(src, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(src, "a.txt"), "data\n")
		passGit(t, src, "add", ".gitattributes", "a.txt")
	})
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, clone, "config", "filter.x.clean", markerScript(t, marker))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(clone, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with filter.x.clean set; want it to exit non-zero with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "filter.x.clean") {
		t.Errorf("destroy output does not name filter.x.clean:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

func TestDestroyScanRule_AModifiedFileInASubmoduleIsLossNamingTheSubmodule(t *testing.T) {
	subRemote := filepath.Join(canonicalTempDir(t), "sub.git")
	subSeed := filepath.Join(canonicalTempDir(t), "sub-seed")
	root, clone := destroyScanInstance(t, func(src string) {
		passGit(t, src, "init", "-q", "--bare", "-b", "main", subRemote)
		passGit(t, src, "init", "-q", "-b", "main", subSeed)
		writeTestFile(t, filepath.Join(subSeed, "lib.txt"), "v1\n")
		passGit(t, subSeed, "add", "lib.txt")
		passGit(t, subSeed, "commit", "-q", "-m", "lib")
		passGit(t, subSeed, "push", "-q", subRemote, "main")
		passGit(t, src, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "file://"+subRemote, "sub")
	})
	passGit(t, clone, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
	writeTestFile(t, filepath.Join(clone, "sub", "lib.txt"), "v2, never committed\n")
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	if err == nil {
		t.Fatal("an unforced destroy went ahead with a modified file in a submodule; want it refused as loss")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, filepath.Join("app", "sub")) {
		t.Errorf("destroy output does not name the submodule app/sub:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

func TestDestroyScanRule_TheScansStatusAndStashCallsCarryTheHardenedFlags(t *testing.T) {
	destroyScanInstance(t, nil)
	log := destroyGitWrapper(t, "")
	stderr := quietDestroyCmd()

	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy of a clean, pushed instance: %v\n%s", err, stderr.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	statuses := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		args := strings.Fields(line)
		for i, a := range args {
			switch {
			case a == "status":
				statuses++
				if !strings.Contains(line, "--ignore-submodules=all") || !strings.Contains(line, "--untracked-files=all") {
					t.Errorf("git %s: a status without --ignore-submodules=all and --untracked-files=all", line)
				}
			case a == "stash" && i+1 < len(args) && args[i+1] == "list", a == "log":
				if !strings.Contains(line, "--no-show-signature") {
					t.Errorf("git %s: a log-family call without --no-show-signature", line)
				}
			}
		}
	}
	if statuses == 0 {
		t.Errorf("the destroy ran no git status at all:\n%s", raw)
	}
}
