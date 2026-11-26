package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The hook worktree sweep runs the program-naming configuration check on
// every repository it finds in a worktree, a submodule's and a gitlink
// repository's included, and again right before DestroySession's status and
// right before the removal, so a clean filter in any of them, or one planted
// after the first check, keeps the worktree with the key named and never
// runs.
//
// Every repository, remote, driver and marker file here is under
// t.TempDir(); nothing touches this checkout's own configuration.

// pathSpellings returns p cleaned and, when it differs, with its symlinks
// resolved, so a check matches either spelling a caller uses.
func pathSpellings(p string) []string {
	clean := filepath.Clean(p)
	out := []string{clean}
	if resolved := resolvePathForCompare(clean); resolved != clean {
		out = append(out, resolved)
	}
	return out
}

// namesAnyPath reports whether line holds any of paths.
func namesAnyPath(line string, paths []string) bool {
	for _, p := range paths {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

// filterAttributesSeed commits a .gitattributes entry naming filter x and a
// file it applies to.
func filterAttributesSeed(t *testing.T) func(string) {
	return func(repo string) {
		writeTestFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(repo, "a.txt"), "data\n")
		passGit(t, repo, "add", ".gitattributes", "a.txt")
		passGit(t, repo, "commit", "-q", "-m", "attributes")
	}
}

// touchLater makes path stat-dirty with the same contents, so a status
// would run its clean filter to compare.
func touchLater(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func assertSweptKeptNamingKey(t *testing.T, fx sweepRuleFixture, marker string, n int, out string) {
	t.Helper()
	assertNoMarker(t, marker)
	if n != 0 {
		t.Fatalf("removed %d, want the worktree kept\nstderr:\n%s", n, out)
	}
	assertKept(t, fx.f, fx.st)
	if !strings.Contains(out, "filter.x.clean") {
		t.Errorf("sweep output does not name filter.x.clean:\n%s", out)
	}
}

// A submodule whose own configuration carries a clean filter, with a
// .gitattributes entry and a stat-dirty file that would trigger it, keeps
// the worktree, and the filter never runs. The key line is what proves the
// check ran: a removal walk stops on a repository inside the tree anyway.
func TestSweepRule_ASubmodulesCleanFilterKeepsTheWorktree(t *testing.T) {
	subRemote := filepath.Join(canonicalTempDir(t), "sub.git")
	subSeed := filepath.Join(canonicalTempDir(t), "sub-seed")
	fx := newSweepRuleFixture(t, func(repo string) {
		passGit(t, repo, "init", "-q", "--bare", "-b", "main", subRemote)
		passGit(t, repo, "init", "-q", "-b", "main", subSeed)
		writeTestFile(t, filepath.Join(subSeed, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(subSeed, "lib.txt"), "v1\n")
		passGit(t, subSeed, "add", ".gitattributes", "lib.txt")
		passGit(t, subSeed, "commit", "-q", "-m", "lib")
		passGit(t, subSeed, "push", "-q", subRemote, "main")
		passGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "file://"+subRemote, "sub")
		passGit(t, repo, "commit", "-q", "-m", "submodule")
	})
	wt := fx.st.WorktreePath
	passGit(t, wt, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
	sub := filepath.Join(wt, "sub")
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, sub, "config", "filter.x.clean", markerScript(t, marker))
	touchLater(t, filepath.Join(sub, "lib.txt"))

	n, out := sweep(t, fx.f, fx.jobs)
	assertSweptKeptNamingKey(t, fx, marker, n, out)
}

// The same with a nested repository registered as a gitlink in place of the
// submodule.
func TestSweepRule_AGitlinkRepositorysCleanFilterKeepsTheWorktree(t *testing.T) {
	fx := newSweepRuleFixture(t, func(repo string) {
		nested := filepath.Join(repo, "vendor")
		passGit(t, repo, "init", "-q", "-b", "main", nested)
		writeTestFile(t, filepath.Join(nested, ".gitattributes"), "*.txt filter=x\n")
		writeTestFile(t, filepath.Join(nested, "lib.txt"), "v1\n")
		passGit(t, nested, "add", ".gitattributes", "lib.txt")
		passGit(t, nested, "commit", "-q", "-m", "lib")
		passGit(t, repo, "add", "vendor")
		passGit(t, repo, "commit", "-q", "-m", "gitlink")
	})
	wt := fx.st.WorktreePath
	// The worktree checks the gitlink out as an empty directory; a clone of
	// the nested repository puts its recorded commit there, so the parent's
	// status stays clean.
	nested := filepath.Join(wt, "vendor")
	passGit(t, wt, "clone", "-q", filepath.Join(fx.f.repoPath, "vendor"), nested)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	passGit(t, nested, "config", "filter.x.clean", markerScript(t, marker))
	touchLater(t, filepath.Join(nested, "lib.txt"))

	n, out := sweep(t, fx.f, fx.jobs)
	assertSweptKeptNamingKey(t, fx, marker, n, out)
}

// The pre-removal command runs after the sweep's evaluation and before
// DestroySession, so a command that plants a clean filter and makes a.txt
// stat-dirty again plants it after the first check and before
// DestroySession's status.
func TestSweepRule_ADriverPlantedBeforeDestroySessionsStatusKeeps(t *testing.T) {
	fx := newSweepRuleFixture(t, filterAttributesSeed(t))
	marker := filepath.Join(t.TempDir(), "filter-ran")
	driver := markerScript(t, marker)
	wt := fx.st.WorktreePath
	setBeforeRemoveCommand(t, fx.f.root, fx.base, fmt.Sprintf(
		"'%s' -C '%s' config filter.x.clean '%s'; /usr/bin/touch -t 203001010000 '%s'; exit 0",
		realGit(t), wt, driver, filepath.Join(wt, "a.txt")))

	n, out := sweep(t, fx.f, fx.jobs)
	assertSweptKeptNamingKey(t, fx, marker, n, out)
}

// plantAfterStatusOnce puts a git on PATH that logs each call and, on the
// first status naming the worktree once sentinel exists, runs that status
// and then plants filter.x.clean (running driver) in the worktree's
// configuration and makes a.txt stat-dirty, logging "planted". With the
// pre-removal command touching sentinel, the plant lands after
// DestroySession's status and before the removal.
func plantAfterStatusOnce(t *testing.T, wt, sentinel, driver string) string {
	t.Helper()
	real := realGit(t)
	dir := canonicalTempDir(t)
	scratch := canonicalTempDir(t)
	log := filepath.Join(scratch, "git-calls.log")
	once := filepath.Join(scratch, "planted")
	spell := pathSpellings(wt)
	alt := spell[len(spell)-1]
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> '%s'
if [ -e '%s' ] && [ ! -e '%s' ]; then
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
	if [ "$sub" = status ]; then
		case "$*" in
		*'%s'*|*'%s'*)
			'%s' "$@"
			rc=$?
			: > '%s'
			'%s' -C '%s' config filter.x.clean '%s'
			/usr/bin/touch -t 203001010000 '%s'
			echo planted >> '%s'
			exit $rc
			;;
		esac
	fi
fi
exec '%s' "$@"
`, log, sentinel, once, spell[0], alt, real, once, real, wt, driver, filepath.Join(wt, "a.txt"), log, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestSweepRule_ADriverPlantedBeforeTheRemovalKeeps(t *testing.T) {
	fx := newSweepRuleFixture(t, filterAttributesSeed(t))
	marker := filepath.Join(t.TempDir(), "filter-ran")
	driver := markerScript(t, marker)
	sentinel := filepath.Join(canonicalTempDir(t), "gate-ran")
	setBeforeRemoveCommand(t, fx.f.root, fx.base, fmt.Sprintf("/usr/bin/touch '%s'; exit 0", sentinel))
	log := plantAfterStatusOnce(t, fx.st.WorktreePath, sentinel, driver)

	n, out := sweep(t, fx.f, fx.jobs)
	planted := false
	for _, line := range scanOrderLog(t, log) {
		if line == "planted" {
			planted = true
		}
	}
	if !planted {
		t.Fatalf("no status named the worktree after the pre-removal command, so nothing was planted before the removal (removed %d)\nstderr:\n%s", n, out)
	}
	assertSweptKeptNamingKey(t, fx, marker, n, out)
}
