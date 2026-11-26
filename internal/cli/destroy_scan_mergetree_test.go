package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// niwa destroy's scan asks whether a branch landed on a remote's default
// branch by content with git merge-tree, which runs a merge driver the
// repository's configuration names for a path both sides changed. The
// builder runs the configuration check again right before that merge-tree,
// as it does before a status, so a driver planted after the first check and
// after the status never runs, and the destroy keeps with the key named.
//
// Every repository, remote, driver and marker file here is under
// t.TempDir().

// plantBeforeFirstDiff puts a git on PATH that logs each call and, on the
// first diff in repo, plants merge.x.driver (running driver) in repo's
// configuration before it runs the call, logging "planted". The scan's
// content check runs that diff after the repository's status and before its
// merge-tree.
func plantBeforeFirstDiff(t *testing.T, repo, driver string) string {
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
		if [ "$sub" = diff ]; then
			: > '%s'
			'%s' -C '%s' config merge.x.driver '%s'
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

func TestDestroyScanMergeTree_ADriverPlantedBeforeTheMergeTreeKeepsWithTheKeyNamed(t *testing.T) {
	root, clone := destroyScanInstance(t, func(src string) {
		writeTestFile(t, filepath.Join(src, "a.txt"), "base\n")
		passGit(t, src, "add", "a.txt")
	})
	scanFileRemotes(t)
	// A local branch and the remote's main both change a.txt from the same
	// base, so the content check's merge-tree merges that path with the
	// driver the attributes name.
	writeTestFile(t, filepath.Join(clone, ".git", "info", "attributes"), "*.txt merge=x\n")
	passGit(t, clone, "checkout", "-q", "-b", "feature")
	writeTestFile(t, filepath.Join(clone, "a.txt"), "feature\n")
	passGit(t, clone, "commit", "-q", "-am", "feature")
	passGit(t, clone, "checkout", "-q", "main")
	writeTestFile(t, filepath.Join(clone, "a.txt"), "main\n")
	passGit(t, clone, "commit", "-q", "-am", "main")
	passGit(t, clone, "push", "-q", "origin", "main")
	marker := filepath.Join(t.TempDir(), "driver-ran")
	log := plantBeforeFirstDiff(t, clone, markerScript(t, marker))
	stderr := quietDestroyCmd()

	err := runDestroy(destroyCmd, []string{"alpha"})

	lines := scanOrderLog(t, log)
	planted, status, mergeTree := -1, -1, -1
	for i, line := range lines {
		switch {
		case line == "planted" && planted < 0:
			planted = i
		case status < 0 && strings.Contains(line, clone) && scanOrderSubcommand(line) == "status":
			status = i
		case mergeTree < 0 && strings.Contains(line, clone) && scanOrderSubcommand(line) == "merge-tree":
			mergeTree = i
		}
	}
	if planted < 0 || status < 0 || status > planted {
		t.Fatalf("the driver wasn't planted after the scan's status (planted at %d, status at %d):\n%s", planted, status, strings.Join(lines, "\n"))
	}
	assertNoMarker(t, marker)
	if err == nil {
		t.Fatal("an unforced destroy went ahead with merge.x.driver planted before the scan's merge-tree; want it kept with the key named")
	}
	if out := err.Error() + stderr.String(); !strings.Contains(out, "merge.x.driver") {
		t.Errorf("destroy output does not name merge.x.driver (merge-tree logged at %d):\n%s", mergeTree, out)
	}
	assertInstancesExist(t, root, "alpha")
}
