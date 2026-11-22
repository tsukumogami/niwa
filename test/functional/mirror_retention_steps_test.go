package functional

// Steps for features/mirror-retention.feature: old mirror stores kept
// through a restore and recorded in .meta/<repo-id>.stores, the sweep that
// removes a retired one once no clone on disk names it, the probe's view of
// kept and half-created instances, a truncated old pack's readable half, and
// what a workspace destroy keeps. Like the steps beside them they read the
// layout the design fixes rather than importing niwa's mirror package; the
// one exception is the lock path, taken the way the race steps take it.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/mirror"
)

func registerMirrorRetentionSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a half-created instance "([^"]*)" holds a copy of the clone of "([^"]*)" in instance "([^"]*)" and no state file$`, aHalfCreatedInstanceHoldsACopy)
	ctx.Step(`^the restore line for "([^"]*)" probed the clones of "([^"]*)" in "([^"]*)" and "([^"]*)"$`, theRestoreLineProbed)
	ctx.Step(`^the store "([^"]*)" replaced is kept and recorded as retired$`, theReplacedStoreIsKeptAndRetired)
	ctx.Step(`^I age the store record of "([^"]*)" past an hour$`, iAgeTheStoreRecord)
	ctx.Step(`^no store of "([^"]*)" was removed and the record still lists the retired store$`, noStoreWasRemoved)
	ctx.Step(`^the mirror log gained a keep line for the retired store of "([^"]*)" because the scan failed$`, theMirrorLogGainedAScanFailedKeep)
	ctx.Step(`^the borrower of "([^"]*)" in instance "([^"]*)" names the linked store directly$`, theBorrowerNamesTheLinkedStoreDirectly)
	ctx.Step(`^the store the borrower names directly is still on disk$`, theDirectlyNamedStoreIsOnDisk)
	ctx.Step(`^the mirror log gained a remove line for the retired store of "([^"]*)"$`, theMirrorLogGainedARemoveLine)
	ctx.Step(`^source repo "([^"]*)" gains branch "([^"]*)" with a small commit and branch "([^"]*)" with a commit holding a 256 KiB random file$`, theSourceRepoGainsTwoBranches)
	ctx.Step(`^the borrower of "([^"]*)" in instance "([^"]*)" has local branches "([^"]*)" on "([^"]*)" and "([^"]*)" on "([^"]*)"$`, theBorrowerHasLocalBranchesOn)
	ctx.Step(`^source repo "([^"]*)" deletes branches "([^"]*)" and "([^"]*)" and prunes their commits$`, theSourceRepoDeletesTwoBranches)
	ctx.Step(`^I replace the packs of the store of "([^"]*)" with one pack holding branch "([^"]*)"'s objects first and branch "([^"]*)"'s last, truncated halfway$`, iReplaceThePacksWithATruncatedPack)
	ctx.Step(`^the branch "([^"]*)" in the borrower of "([^"]*)" in instance "([^"]*)" reads to the root$`, theBranchReadsToTheRoot)
	ctx.Step(`^the mirror log names "([^"]*)" unrecoverable and not "([^"]*)"$`, theMirrorLogNamesUnrecoverable)
	ctx.Step(`^in the mirror log every restore line carrying probed comes before every remove line$`, everyProbedRestoreComesBeforeEveryRemove)
	ctx.Step(`^the mirrors of the removed workspace root are gone, and its key directory with them unless something else is in it$`, theMirrorsOfTheRemovedRootAreGone)
	ctx.Step(`^the error output says the mirror of "([^"]*)" was kept because "([^"]*)"$`, theErrorOutputSaysTheMirrorWasKept)
	ctx.Step(`^the mirror of "([^"]*)" outlived the destroy$`, theMirrorOutlivedTheDestroy)
	ctx.Step(`^I run "([^"]*)" from workspace root while the mirror lock of "([^"]*)" is held$`, iRunWhileTheMirrorLockIsHeld)
	ctx.Step(`^nothing mirror-related exists outside the sandbox home once the workspace root is gone$`, nothingMirrorRelatedOnceTheRootIsGone)
}

// retentionScenario is what the retention steps remember between steps.
type retentionScenario struct {
	// directClone is a borrower whose alternates names a .store/ directory
	// directly, and direct is that directory.
	directClone, direct string
	// branches maps each branch a source repo gained to its commit.
	branches map[string]string
}

var (
	retentionMu     sync.Mutex
	retentionStates = map[*testState]*retentionScenario{}
)

// retentionState returns the scenario's state, the mirror steps' among it.
func retentionState(ctx context.Context) (*testState, *mirrorScenario, *retentionScenario, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	retentionMu.Lock()
	defer retentionMu.Unlock()
	r := retentionStates[s]
	if r == nil {
		r = &retentionScenario{branches: map[string]string{}}
		retentionStates[s] = r
		s.cleanups = append(s.cleanups, func() {
			retentionMu.Lock()
			delete(retentionStates, s)
			retentionMu.Unlock()
		})
	}
	return s, m, r, nil
}

// retentionMirrorsDir is the mirrors directory, worked out from the
// workspace root's parent when the root itself is gone.
func retentionMirrorsDir(s *testState) (string, error) {
	key, err := keyDirOf(s, ".")
	if err != nil {
		return "", err
	}
	return filepath.Join(key, "mirrors"), nil
}

// storeRecordEntry is one entry of .meta/<repo-id>.stores.
type storeRecordEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Identity  string `json:"identity"`
	Linked    bool   `json:"linked"`
	RetiredAt string `json:"retired_at"`
}

func storeRecordPath(s *testState, repoID string) (string, error) {
	dir, err := retentionMirrorsDir(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".meta", repoID+".stores"), nil
}

// readStoreRecord reads the store record, which must be a 0600 regular file.
func readStoreRecord(s *testState, repoID string) ([]storeRecordEntry, error) {
	path, err := storeRecordPath(s, repoID)
	if err != nil {
		return nil, err
	}
	if err := requireMode(path, false, 0o600); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec struct {
		Schema int                `json:"schema"`
		Stores []storeRecordEntry `json:"stores"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decoding the store record: %w\n%s", err, data)
	}
	if rec.Schema != 1 {
		return nil, fmt.Errorf("the store record has schema %d, want 1:\n%s", rec.Schema, data)
	}
	return rec.Stores, nil
}

// canonical is path with its symlinks resolved when it exists, so two
// spellings of one directory (macOS's /var and /private/var) compare equal.
func canonical(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func aHalfCreatedInstanceHoldsACopy(ctx context.Context, inst, groupRepo, from string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	src := filepath.Join(s.workspaceRoot, from, groupRepo)
	dst := filepath.Join(s.workspaceRoot, inst, groupRepo)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ctx, err
	}
	if out, err := exec.Command("cp", "-Rp", src, dst).CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("cp -Rp %s %s: %w\n%s", src, dst, err, out)
	}
	if _, err := os.Lstat(filepath.Join(s.workspaceRoot, inst, ".niwa")); !errors.Is(err, fs.ErrNotExist) {
		return ctx, fmt.Errorf("the half-created instance %s has a .niwa (%v)", inst, err)
	}
	return ctx, nil
}

// theRestoreLineProbed finds the restore line the log gained for repoID and
// requires its probed list to name the clone of groupRepo in each instance:
// "kept/<name>" names one under the sandbox home's kept/, anything else one
// under the workspace root.
func theRestoreLineProbed(ctx context.Context, repoID, groupRepo, inst1, inst2 string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	var restore map[string]any
	for _, l := range lines {
		if l["event"] == "restore" && l["mirror"] == repoID {
			restore = l
		}
	}
	if restore == nil {
		return fmt.Errorf("the mirror log gained no restore line for %s: %v", repoID, lines)
	}
	raw, _ := restore["probed"].([]any)
	var probed []string
	for _, v := range raw {
		probed = append(probed, canonical(fmt.Sprint(v)))
	}
	for _, inst := range []string{inst1, inst2} {
		var want string
		if name, ok := strings.CutPrefix(inst, "kept/"); ok {
			kept, err := keptDirOf(s)
			if err != nil {
				return err
			}
			want = filepath.Join(kept, name, groupRepo)
		} else {
			want = filepath.Join(s.workspaceRoot, inst, groupRepo)
		}
		if !slices.Contains(probed, canonical(want)) {
			return fmt.Errorf("the restore line probed %v, not %s", probed, want)
		}
	}
	return nil
}

// theReplacedStoreIsKeptAndRetired checks the store the remembered link
// named: still on disk, and listed in the record as linked once and retired.
func theReplacedStoreIsKeptAndRetired(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if m.link == "" {
		return fmt.Errorf("no mirror link was remembered")
	}
	return requireRetiredStore(s, repoID, filepath.Base(m.link))
}

func requireRetiredStore(s *testState, repoID, name string) error {
	dir, err := retentionMirrorsDir(s)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(filepath.Join(dir, ".store", name, "objects")); err != nil || !info.IsDir() {
		return fmt.Errorf("the replaced store %s is gone (%v)", name, err)
	}
	stores, err := readStoreRecord(s, repoID)
	if err != nil {
		return err
	}
	for _, e := range stores {
		if e.Name == name {
			if !e.Linked || e.RetiredAt == "" {
				return fmt.Errorf("the record's entry for %s = %+v, want linked and retired", name, e)
			}
			return nil
		}
	}
	return fmt.Errorf("the store record %+v has no entry for %s", stores, name)
}

// iAgeTheStoreRecord moves every retired_at and the last_swept time of the
// record back two hours, as though an hour had passed and more.
func iAgeTheStoreRecord(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	path, err := storeRecordPath(s, repoID)
	if err != nil {
		return ctx, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		return ctx, fmt.Errorf("decoding the store record: %w", err)
	}
	back := func(v any) (any, error) {
		str, ok := v.(string)
		if !ok || str == "" {
			return v, nil
		}
		at, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return nil, fmt.Errorf("record time %q: %w", str, err)
		}
		return at.Add(-2 * time.Hour).UTC().Format(time.RFC3339), nil
	}
	if rec["last_swept"], err = back(rec["last_swept"]); err != nil {
		return ctx, err
	}
	stores, _ := rec["stores"].([]any)
	for _, raw := range stores {
		e, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := e["retired_at"]; ok {
			if e["retired_at"], err = back(v); err != nil {
				return ctx, err
			}
		}
	}
	out, err := json.Marshal(rec)
	if err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(path, append(out, '\n'), 0o600)
}

// noStoreWasRemoved checks the log lines since the last count hold no
// remove line, and every store the record lists as retired, the remembered
// replaced one among them, is still on disk.
func noStoreWasRemoved(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if l["event"] == "remove" {
			return fmt.Errorf("the mirror log gained a remove line: %v", l)
		}
	}
	if err := theReplacedStoreIsKeptAndRetired(ctx, repoID); err != nil {
		return err
	}
	stores, err := readStoreRecord(s, repoID)
	if err != nil {
		return err
	}
	for _, e := range stores {
		if e.RetiredAt != "" {
			if err := requireRetiredStore(s, repoID, e.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// theMirrorLogGainedAScanFailedKeep requires a keep line with reason
// scan-failed for the remembered replaced store since the last count: the
// sweep ran its scan, the scan failed, and the store stayed.
func theMirrorLogGainedAScanFailedKeep(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	want := filepath.Join(".store", filepath.Base(m.link))
	for _, l := range lines {
		if l["event"] == "keep" && l["mirror"] == repoID && l["store"] == want && l["reason"] == "scan-failed" {
			return nil
		}
	}
	return fmt.Errorf("the mirror log gained no keep line for %s with reason scan-failed: %v", want, lines)
}

func theBorrowerNamesTheLinkedStoreDirectly(ctx context.Context, repoID, inst string) (context.Context, error) {
	s, m, r, err := retentionState(ctx)
	if err != nil {
		return ctx, err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	line := filepath.Join(store, "objects") + "\n"
	if err := os.WriteFile(filepath.Join(clone, ".git", "objects", "info", "alternates"), []byte(line), 0o644); err != nil {
		return ctx, err
	}
	r.directClone, r.direct = clone, store
	return ctx, nil
}

// theDirectlyNamedStoreIsOnDisk checks the store a borrower names directly
// still holds its packs and the borrower still reads its default branch.
func theDirectlyNamedStoreIsOnDisk(ctx context.Context) error {
	_, _, r, err := retentionState(ctx)
	if err != nil {
		return err
	}
	if r.direct == "" {
		return fmt.Errorf("no borrower names a store directly")
	}
	packs, err := filepath.Glob(filepath.Join(r.direct, "objects", "pack", "pack-*.pack"))
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		return fmt.Errorf("the store %s the borrower names directly holds no pack", r.direct)
	}
	if out, err := fixtureGitWorkTree(r.directClone, "rev-list", "--objects", "main"); err != nil {
		return fmt.Errorf("the borrower %s can't read main through the store it names: %w\n%s", r.directClone, err, out)
	}
	return nil
}

// theMirrorLogGainedARemoveLine requires exactly one remove line since the
// last count, for the remembered replaced store and reason retired, that
// store gone and the linked store in place.
func theMirrorLogGainedARemoveLine(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	var removes []map[string]any
	for _, l := range lines {
		if l["event"] == "remove" {
			removes = append(removes, l)
		}
	}
	want := filepath.Join(".store", filepath.Base(m.link))
	if len(removes) != 1 || removes[0]["store"] != want || removes[0]["reason"] != "retired" || removes[0]["mirror"] != repoID {
		return fmt.Errorf("the mirror log gained remove lines %v, want one for %s with reason retired", removes, want)
	}
	dir, err := retentionMirrorsDir(s)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, want)); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the removed store %s is still there (%v)", want, err)
	}
	_, err = mirrorStore(s, repoID)
	return err
}

func theSourceRepoGainsTwoBranches(ctx context.Context, name, small, large string) (context.Context, error) {
	s, _, r, err := retentionState(ctx)
	if err != nil {
		return ctx, err
	}
	work, err := os.MkdirTemp(s.gitServer.root, "branches-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", s.repoURLs[name], work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", name, err, out)
	}
	for _, b := range []string{small, large} {
		if out, err := fixtureGitWorkTree(work, "checkout", "--quiet", "-B", b, "refs/remotes/origin/main"); err != nil {
			return ctx, fmt.Errorf("git checkout %s: %w\n%s", b, err, out)
		}
		file := b + ".txt"
		body := []byte("a small change on " + b + "\n")
		if b == large {
			file = b + ".bin"
			body = make([]byte, 256<<10)
			if _, err := rand.Read(body); err != nil {
				return ctx, err
			}
		}
		if err := os.WriteFile(filepath.Join(work, file), body, 0o644); err != nil {
			return ctx, err
		}
		for _, args := range [][]string{
			{"add", file},
			{"commit", "--quiet", "-m", "on " + b},
			{"push", "--quiet", "origin", "HEAD:refs/heads/" + b},
		} {
			if out, err := fixtureGitCommit(work, args...); err != nil {
				return ctx, fmt.Errorf("git %s: %w\n%s", args[0], err, out)
			}
		}
		if r.branches[b], err = revParse(work, "HEAD"); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func theBorrowerHasLocalBranchesOn(ctx context.Context, repoID, inst, keep1, branch1, keep2, branch2 string) (context.Context, error) {
	s, m, r, err := retentionState(ctx)
	if err != nil {
		return ctx, err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return ctx, err
	}
	for _, p := range [][2]string{{keep1, branch1}, {keep2, branch2}} {
		if out, err := fixtureGitWorkTree(clone, "branch", p[0], "refs/remotes/origin/"+p[1]); err != nil {
			return ctx, fmt.Errorf("git branch %s in %s: %w\n%s", p[0], clone, err, out)
		}
		if got, err := revParse(clone, "refs/heads/"+p[0]); err != nil || got != r.branches[p[1]] {
			return ctx, fmt.Errorf("%s in %s = %q (%v), want branch %s's commit %s", p[0], clone, got, err, p[1], r.branches[p[1]])
		}
	}
	return ctx, nil
}

func theSourceRepoDeletesTwoBranches(ctx context.Context, name, branch1, branch2 string) (context.Context, error) {
	s, _, r, err := retentionState(ctx)
	if err != nil {
		return ctx, err
	}
	repo := sourceRepoPath(s, name)
	for _, args := range [][]string{
		{"update-ref", "-d", "refs/heads/" + branch1},
		{"update-ref", "-d", "refs/heads/" + branch2},
		{"reflog", "expire", "--expire=now", "--all"},
		{"gc", "--quiet", "--prune=now"},
	} {
		if out, err := fixtureGitBare(repo, args...); err != nil {
			return ctx, fmt.Errorf("git %s in %s: %w\n%s", args[0], repo, err, out)
		}
	}
	for _, b := range []string{branch1, branch2} {
		if _, err := fixtureGitBare(repo, "cat-file", "-e", r.branches[b]); err == nil {
			return ctx, fmt.Errorf("the source repo still holds branch %s's commit after the prune", b)
		}
	}
	return ctx, nil
}

// objectsOnly lists, in rev-list --objects order, the objects reachable from
// branch in the store and not from main: its commit first.
func objectsOnly(store, branch string) ([]string, error) {
	out, err := fixtureGitBare(store, "rev-list", "--objects", "refs/heads/"+branch, "--not", "refs/heads/main")
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s in the store: %w\n%s", branch, err, out)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			ids = append(ids, fields[0])
		}
	}
	return ids, nil
}

// iReplaceThePacksWithATruncatedPack builds, from the linked store, one pack
// holding first branch first's objects and then branch second's in reverse
// (its large blob, its tree, then its commit), and replaces every pack of
// the store with it, cut to its first half. With no delta computed
// (--window=0) or reused from the store's own packs (--no-reuse-delta, since
// a reused delta's base is written ahead of it) and no tag in the store, git
// pack-objects writes the objects in the order given, which the step checks
// from the pack's own offsets: every object of first ends before the cut and
// second's commit starts after it. Second's commit is past the cut on
// purpose: a ref whose commits all read but whose blob doesn't is one the
// probe doesn't report. The manifest's packs are then missing, so the next
// check finds the fault pack-missing.
func iReplaceThePacksWithATruncatedPack(ctx context.Context, repoID, first, second string) (context.Context, error) {
	s, _, _, err := retentionState(ctx)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	firstIDs, err := objectsOnly(store, first)
	if err != nil {
		return ctx, err
	}
	secondIDs, err := objectsOnly(store, second)
	if err != nil {
		return ctx, err
	}
	if len(firstIDs) == 0 || len(secondIDs) == 0 {
		return ctx, fmt.Errorf("branch %s or %s holds no objects of its own in the store", first, second)
	}
	secondCommit := secondIDs[0]
	slices.Reverse(secondIDs)
	order := strings.Join(append(append([]string{}, firstIDs...), secondIDs...), "\n") + "\n"

	work, err := os.MkdirTemp(s.gitServer.root, "pack-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	out, err := fixtureGitBareStdin(store, order, "pack-objects", "--window=0", "--no-reuse-delta", "-q", filepath.Join(work, "p"))
	if err != nil {
		return ctx, fmt.Errorf("git pack-objects: %w\n%s", err, out)
	}
	hash := strings.TrimSpace(string(out))
	full := filepath.Join(work, "p-"+hash+".pack")
	idx := filepath.Join(work, "p-"+hash+".idx")
	data, err := os.ReadFile(full)
	if err != nil {
		return ctx, err
	}
	half := int64(len(data) / 2)

	verify, err := fixtureGitBare(store, "verify-pack", "-v", idx)
	if err != nil {
		return ctx, fmt.Errorf("git verify-pack -v: %w\n%s", err, verify)
	}
	ends := map[string][2]int64{}
	for _, line := range strings.Split(string(verify), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || len(f[0]) != 40 {
			continue
		}
		inPack, err1 := strconv.ParseInt(f[3], 10, 64)
		offset, err2 := strconv.ParseInt(f[4], 10, 64)
		if err1 == nil && err2 == nil {
			ends[f[0]] = [2]int64{offset, offset + inPack}
		}
	}
	for _, id := range firstIDs {
		if e, ok := ends[id]; !ok || e[1] > half {
			return ctx, fmt.Errorf("object %s of branch %s isn't wholly before the cut at %d: %v", id, first, half, e)
		}
	}
	if e, ok := ends[secondCommit]; !ok || e[0] < half {
		return ctx, fmt.Errorf("branch %s's commit %s isn't after the cut at %d: %v", second, secondCommit, half, e)
	}

	packDir := filepath.Join(store, "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return ctx, err
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(packDir, e.Name())); err != nil {
			return ctx, err
		}
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack-"+hash+".pack"), data[:half], 0o444); err != nil {
		return ctx, err
	}
	idxData, err := os.ReadFile(idx)
	if err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(filepath.Join(packDir, "pack-"+hash+".idx"), idxData, 0o444)
}

func theBranchReadsToTheRoot(ctx context.Context, branch, repoID, inst string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return err
	}
	if out, err := fixtureGitWorkTree(clone, "log", "--oneline", branch); err != nil {
		return fmt.Errorf("git log %s in %s: %w\n%s", branch, clone, err, out)
	}
	if out, err := fixtureGitWorkTree(clone, "rev-list", "--objects", branch); err != nil {
		return fmt.Errorf("git rev-list --objects %s in %s: %w\n%s", branch, clone, err, out)
	}
	root, err := fixtureGitWorkTree(clone, "rev-list", "--max-parents=0", branch)
	if err != nil {
		return fmt.Errorf("git rev-list %s in %s: %w\n%s", branch, clone, err, root)
	}
	if got, want := strings.TrimSpace(string(root)), m.rootCommits[repoID]; got != want {
		return fmt.Errorf("%s in %s reaches root %q, want %q", branch, clone, got, want)
	}
	return nil
}

// theMirrorLogNamesUnrecoverable requires an unrecoverable line for ref
// among the lines since the last count and none for other.
func theMirrorLogNamesUnrecoverable(ctx context.Context, ref, other string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	found := false
	for _, l := range lines {
		if l["event"] != "unrecoverable" {
			continue
		}
		switch l["ref"] {
		case ref:
			found = true
		case other:
			return fmt.Errorf("the mirror log names %s unrecoverable: %v", other, l)
		}
	}
	if !found {
		return fmt.Errorf("the mirror log gained no unrecoverable line for %s: %v", ref, lines)
	}
	return nil
}

// everyProbedRestoreComesBeforeEveryRemove reads the whole mirror log and
// requires a restore line carrying probed and a remove line, with every one
// of the first before every one of the second.
func everyProbedRestoreComesBeforeEveryRemove(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := readMirrorLog(s)
	if err != nil {
		return err
	}
	lastProbe, firstRemove := -1, -1
	for i, l := range lines {
		if probed, _ := l["probed"].([]any); l["event"] == "restore" && len(probed) > 0 {
			lastProbe = i
		}
		if l["event"] == "remove" && firstRemove < 0 {
			firstRemove = i
		}
	}
	if lastProbe < 0 || firstRemove < 0 {
		return fmt.Errorf("the mirror log has no restore line carrying probed (%d) or no remove line (%d): %v", lastProbe, firstRemove, lines)
	}
	if lastProbe > firstRemove {
		return fmt.Errorf("a remove line (line %d) comes before a restore line carrying probed (line %d)", firstRemove+1, lastProbe+1)
	}
	return nil
}

func theMirrorsOfTheRemovedRootAreGone(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(s.workspaceRoot); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("the workspace root is still there (%v)", err)
	}
	mirrors, err := retentionMirrorsDir(s)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(mirrors); !errors.Is(err, fs.ErrNotExist) {
		entries, _ := os.ReadDir(mirrors)
		return fmt.Errorf("the mirrors directory %s survived the destroy with %v (%v)", mirrors, entries, err)
	}
	keyDir := filepath.Dir(mirrors)
	entries, err := os.ReadDir(keyDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("the key directory %s is left empty", keyDir)
	}
	return nil
}

func theErrorOutputSaysTheMirrorWasKept(ctx context.Context, repoID, reason string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := retentionMirrorsDir(s)
	if err != nil {
		return err
	}
	stable := filepath.Join(dir, repoID)
	for _, spelling := range []string{stable, canonical(stable)} {
		if strings.Contains(s.stderr, "kept the mirror at "+spelling+": "+reason) {
			return nil
		}
	}
	return fmt.Errorf("the error output doesn't say the mirror at %s was kept because %q:\n%s", stable, reason, s.stderr)
}

func theMirrorOutlivedTheDestroy(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := retentionMirrorsDir(s)
	if err != nil {
		return err
	}
	stable := filepath.Join(dir, repoID)
	target, err := os.Readlink(stable)
	if err != nil {
		return fmt.Errorf("the mirror's link %s is gone: %w", stable, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, target, "objects")); err != nil || !info.IsDir() {
		return fmt.Errorf("the mirror's store %s is gone (%v)", target, err)
	}
	for _, path := range []string{filepath.Join(dir, ".meta", repoID+".json"), filepath.Join(dir, ".meta", repoID+".stores")} {
		if _, err := os.Lstat(path); err != nil {
			return fmt.Errorf("%s is gone: %w", path, err)
		}
	}
	return nil
}

// iRunWhileTheMirrorLockIsHeld runs command from the workspace root with
// the mirror's lock held the way another niwa command would hold it.
func iRunWhileTheMirrorLockIsHeld(ctx context.Context, command, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	release, err := filelock.TryFileLock(mirror.LockPath(s.homeDir, s.workspaceRoot, repoID), "functional test holder")
	if err != nil {
		return ctx, fmt.Errorf("taking the mirror lock: %w", err)
	}
	defer release()
	return ctx, runNiwa(s, s.workspaceRoot, command)
}

// nothingMirrorRelatedOnceTheRootIsGone is the sandbox check of a scenario
// that destroyed the workspace root: no key directory or mirror lock of the
// workspace under the real home, and no workspace root left to hold a
// mirror entry.
func nothingMirrorRelatedOnceTheRootIsGone(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(s.workspaceRoot); !errors.Is(err, fs.ErrNotExist) {
		return nothingMirrorRelatedOutsideTheSandboxHome(ctx)
	}
	keyDir, err := keyDirOf(s, ".")
	if err != nil {
		return err
	}
	realHome, err := os.UserHomeDir()
	if err != nil || realHome == s.homeDir {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(realHome, ".niwa", filepath.Base(keyDir))); err == nil {
		return fmt.Errorf("the workspace key directory exists under the real home %s", realHome)
	}
	locks, _ := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "locks", "m-*.lock"))
	for _, lock := range locks {
		if _, err := os.Lstat(filepath.Join(realHome, ".niwa", "locks", filepath.Base(lock))); err == nil {
			return fmt.Errorf("the mirror lock %s exists under the real home", filepath.Base(lock))
		}
	}
	return nil
}
