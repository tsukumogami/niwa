package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Before ScanInstance's first git call, an unforced niwa destroy runs the
// removal walk over the instance and the program-naming configuration check
// on every repository it finds: the clone, each submodule, each gitlink and
// nested repository. A flagged repository gets no further git call and is
// reported as loss naming the key, so the destroy lists it, asks for the
// instance name on a terminal and goes ahead only on a match, and aborts
// when stdin isn't a terminal. The builder runs the check again right
// before each git status, so a driver planted after the first check is
// caught before git runs it. Under --force no git starts at all.
//
// Every repository, driver and marker file here is under t.TempDir().

// scanOrderSubcommand is the git subcommand of one logged call: the first
// argument that isn't a global option (-C and -c take a value).
func scanOrderSubcommand(line string) string {
	args := strings.Fields(line)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// scanOrderLog reads the git wrapper's log, one call per line.
func scanOrderLog(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// assertOnlyConfigReads fails on any logged git call in repo other than a
// configuration read.
func assertOnlyConfigReads(t *testing.T, log, repo string) {
	t.Helper()
	for _, line := range scanOrderLog(t, log) {
		if !strings.Contains(line, repo) {
			continue
		}
		if sub := scanOrderSubcommand(line); sub != "config" {
			t.Errorf("git ran %s in the flagged repository %s: git %s", sub, repo, line)
		}
	}
}

func TestDestroyScanOrder_AFlaggedRepositoryGetsNoGitCallAndIsListedAsLoss(t *testing.T) {
	root, clone := destroyScanInstance(t, nil)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
	log := destroyGitWrapper(t, "")
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertNoMarker(t, marker)
	assertOnlyConfigReads(t, log, clone)
	if err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Fatalf("destroy = %v; want it listed as loss and aborted because stdin isn't a terminal", err)
	}
	if !strings.Contains(stderr.String(), "core.fsmonitor") {
		t.Errorf("the scan's listing does not name core.fsmonitor:\n%s", stderr.String())
	}
	assertInstancesExist(t, root, "alpha")
}

func TestDestroyScanOrder_AFlaggedRepositoryAtATerminalGoesOnlyOnAMatch(t *testing.T) {
	t.Run("a match destroys the instance with the repository in it", func(t *testing.T) {
		root, clone := destroyScanInstance(t, nil)
		marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
		passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
		stderr := quietDestroyCmd()
		confirmDestroyAt(t, "alpha")

		err := runDestroy(destroyCmd, []string{"alpha"})
		assertNoMarker(t, marker)
		if err != nil {
			t.Fatalf("a confirmed destroy: %v\n%s", err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "core.fsmonitor") {
			t.Errorf("the destroy went ahead without listing core.fsmonitor as loss first:\n%s", stderr.String())
		}
		if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
			t.Errorf("the instance is still there after a confirmed destroy: %v", err)
		}
	})
	t.Run("a mismatch keeps it", func(t *testing.T) {
		root, clone := destroyScanInstance(t, nil)
		marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
		passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
		stderr := quietDestroyCmd()
		confirmDestroyAt(t, "not-alpha")

		err := runDestroy(destroyCmd, []string{"alpha"})
		assertNoMarker(t, marker)
		if err == nil || !strings.Contains(err.Error(), "did not match") {
			t.Fatalf("destroy = %v; want the confirmation asked and refused on a mismatch\n%s", err, stderr.String())
		}
		assertInstancesExist(t, root, "alpha")
	})
}

// plantBeforeFirstStatus puts a git on PATH that logs each call and, on the
// first call in repo that is neither a status nor a configuration read,
// plants filter.x.clean (running driver) in repo's configuration before it
// runs the call, logging "planted". The plant lands after the first check
// and before the scan's status in repo.
func plantBeforeFirstStatus(t *testing.T, repo, driver string) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	scratch := canonicalTempDir(t)
	log := filepath.Join(scratch, "git-calls.log")
	once := filepath.Join(scratch, "planted")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
if [ ! -e '%s' ]; then
	case "$*" in
	*'%s'*)
		sub=""
		skip=""
		for a in "$@"; do
			if [ -n "$skip" ]; then skip=""; continue; fi
			case "$a" in
			-C|-c) skip=1 ;;
			-*) ;;
			*) sub="$a"; break ;;
			esac
		done
		if [ "$sub" != status ] && [ "$sub" != config ]; then
			: > '%s'
			'%s' -C '%s' config filter.x.clean '%s'
			echo planted >> '%s'
		fi
		;;
	esac
fi
exec '%s' "$@"
`, log, once, repo, once, real, repo, driver, log, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestDestroyScanOrder_ADriverPlantedAfterTheFirstCheckRefusesBeforeTheStatusRunsIt(t *testing.T) {
	root, clone := destroyScanInstance(t, func(src string) {
		writeTestFile(t, filepath.Join(src, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(src, "a.txt"), "data\n")
		passGit(t, src, "add", ".gitattributes", "a.txt")
	})
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(clone, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "filter-ran")
	log := plantBeforeFirstStatus(t, clone, markerScript(t, marker))
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})

	lines := scanOrderLog(t, log)
	planted, status := -1, -1
	for i, line := range lines {
		if line == "planted" && planted < 0 {
			planted = i
		}
		if status < 0 && strings.Contains(line, clone) && scanOrderSubcommand(line) == "status" {
			status = i
		}
	}
	if planted < 0 || (status >= 0 && status < planted) {
		t.Skipf("the destroy made no git call in %s before its status, so nothing lands between the check and the status:\n%s", clone, strings.Join(lines, "\n"))
	}
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with filter.x.clean planted after the first check; want it refused with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "filter.x.clean") {
		t.Errorf("destroy output does not name filter.x.clean:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

// A submodule whose own configuration carries a clean filter, with a
// .gitattributes entry and a stat-dirty file that would trigger it, makes an
// unforced destroy refuse with the key named, and the filter never runs.
func TestDestroyScanOrder_ASubmodulesCleanFilterRefusesWithTheKeyNamed(t *testing.T) {
	subRemote := filepath.Join(canonicalTempDir(t), "sub.git")
	subSeed := filepath.Join(canonicalTempDir(t), "sub-seed")
	root, clone := destroyScanInstance(t, func(src string) {
		passGit(t, src, "init", "-q", "--bare", "-b", "main", subRemote)
		passGit(t, src, "init", "-q", "-b", "main", subSeed)
		writeTestFile(t, filepath.Join(subSeed, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(subSeed, "lib.txt"), "v1\n")
		passGit(t, subSeed, "add", ".gitattributes", "lib.txt")
		passGit(t, subSeed, "commit", "-q", "-m", "lib")
		passGit(t, subSeed, "push", "-q", subRemote, "main")
		passGit(t, src, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "file://"+subRemote, "sub")
	})
	passGit(t, clone, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
	sub := filepath.Join(clone, "sub")
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, sub, "config", "filter.x.clean", markerScript(t, marker))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(sub, "lib.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with filter.x.clean in a submodule's configuration; want it refused with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "filter.x.clean") {
		t.Errorf("destroy output does not name filter.x.clean:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

// The same with a nested repository registered as a gitlink in place of the
// submodule.
func TestDestroyScanOrder_AGitlinkRepositorysCleanFilterRefusesWithTheKeyNamed(t *testing.T) {
	root, clone := destroyScanInstance(t, nil)
	nested := filepath.Join(clone, "vendor")
	passGit(t, clone, "init", "-q", "-b", "main", nested)
	writeTestFile(t, filepath.Join(nested, ".gitattributes"), "*.txt filter=x\n")
	writeTestFile(t, filepath.Join(nested, "lib.txt"), "v1\n")
	passGit(t, nested, "add", ".gitattributes", "lib.txt")
	passGit(t, nested, "commit", "-q", "-m", "lib")
	passGit(t, clone, "add", "vendor")
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, nested, "config", "filter.x.clean", markerScript(t, marker))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(nested, "lib.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with filter.x.clean in a gitlink repository's configuration; want it refused with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "filter.x.clean") {
		t.Errorf("destroy output does not name filter.x.clean:\n%s", out)
	}
	assertInstancesExist(t, root, "alpha")
}

// A guard: niwa destroy --force starts no git process, with a worktree in
// the instance and a driver in its repository.
func TestDestroyScanOrder_ForceStartsNoGit(t *testing.T) {
	root, clone, _ := destroyWorktreeInstance(t)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	passGit(t, clone, "config", "core.fsmonitor", markerScript(t, marker))
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, true, false, "")
	log := destroyGitWrapper(t, "")

	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy --force: %v\n%s", err, stderr.String())
	}
	if lines := scanOrderLog(t, log); len(lines) > 0 {
		t.Errorf("destroy --force started git:\n%s", strings.Join(lines, "\n"))
	}
	assertNoMarker(t, marker)
	if _, err := os.Lstat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Errorf("the instance is still there after destroy --force: %v", err)
	}
}
