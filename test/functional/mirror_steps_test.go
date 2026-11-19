package functional

// Steps for features/mirror-repair.feature: a host mirror under the sandbox
// home, borrowers made from niwa's own clones, and the faults a scenario
// inflicts on the mirror.
//
// The mirror's paths are worked out here from the layout the design fixes
// (~/.niwa/w-<32 hex of sha256 of the workspace root's real path>/mirrors/<id>),
// not by importing niwa's mirror package, so a change to the layout shows up
// as a failing scenario rather than as a test that silently follows it. Only
// niwa ever writes the mirror; these steps read it, delete it, and write the
// borrowers' alternates the way a reference clone would.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// mirrorScenario is what the mirror steps remember between steps.
type mirrorScenario struct {
	rootCommits map[string]string           // source repo -> its root commit
	borrowers   map[string][]string         // repo id -> borrower clone paths
	snapshots   map[string]borrowerSnapshot // clone path -> state before the fault
	unpushed    struct{ clone, commit, status string }
	link        string // a remembered link target

	// What the damage scenarios remember: the mirror log's line count and
	// the state file's bytes before a command.
	logLines  int
	stateFile []byte

	// What the race and failure scenarios remember: each racing command's
	// exit code and stderr, and the linked store's files before a command.
	race       []raceRun
	storeFiles map[string]string
	// Each borrower's files as bytes on disk, for a check git can't make
	// while the mirror it borrows from is damaged.
	rawBorrowers map[string]map[string]string

	// What the refresh scenario added to a source repo: the branch's commit,
	// main's new commit and the commit main pointed at before it.
	added struct{ branch, topic, tip, parent string }

	// What the lost-commit scenarios made in a borrower: a local branch with
	// one unpushed commit on the added topic commit, and that commit's blob.
	lost struct{ clone, branch, commit, blob string }
}

// borrowerSnapshot is what must survive a restore untouched in a borrower.
// The index is compared through ls-files rather than byte for byte, because
// any git status rewrites its stat cache without changing what it records.
type borrowerSnapshot struct {
	alternates                []byte
	refs, head, index, status string
	files                     map[string]string
}

func registerMirrorSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a source repo "([^"]*)" exists with (\d+) commits$`, aSourceRepoExistsWithCommits)
	ctx.Step(`^the clones of "([^"]*)" in instances "([^"]*)" and "([^"]*)" borrow from the mirror of "([^"]*)"$`, theClonesBorrowFromTheMirror)
	ctx.Step(`^the mirror of "([^"]*)" is a link into its store$`, theMirrorIsALinkIntoItsStore)
	ctx.Step(`^the mirror of "([^"]*)" has the remote URL of source repo "([^"]*)"$`, theMirrorHasTheRemoteURL)
	ctx.Step(`^I repack the borrowers of "([^"]*)" against the mirror$`, iRepackTheBorrowers)
	ctx.Step(`^each borrower of "([^"]*)" holds none of the mirror's objects$`, eachBorrowerHoldsNoObjects)
	ctx.Step(`^the borrower of "([^"]*)" in instance "([^"]*)" has an unpushed commit and an uncommitted edit$`, theBorrowerHasUnpushedWork)
	ctx.Step(`^I snapshot the borrowers of "([^"]*)"$`, iSnapshotTheBorrowers)
	ctx.Step(`^I delete the mirror of "([^"]*)" and its store$`, iDeleteTheMirrorAndItsStore)
	ctx.Step(`^git fsck reports missing objects in the borrower of "([^"]*)" in instance "([^"]*)"$`, gitFsckReportsMissingObjects)
	ctx.Step(`^each borrower of "([^"]*)" reads its default branch to the root and fsck is clean$`, eachBorrowerReadsToTheRoot)
	ctx.Step(`^the borrowers of "([^"]*)" match their snapshot$`, theBorrowersMatchTheirSnapshot)
	ctx.Step(`^the unpushed commit and uncommitted edit are intact$`, theUnpushedWorkIsIntact)
	ctx.Step(`^the error output carries one mirror warning for "([^"]*)" with fault "([^"]*)" and result "([^"]*)"$`, theErrorOutputCarriesOneMirrorWarning)
	ctx.Step(`^the error output carries no mirror warning$`, theErrorOutputCarriesNoMirrorWarning)
	ctx.Step(`^no restore leftovers remain under the mirrors$`, noRestoreLeftoversRemain)
	ctx.Step(`^the workspace key directory has mode 0700 and the mirror lock has mode 0600$`, theMirrorModesAreTight)
	ctx.Step(`^nothing mirror-related exists outside the sandbox home$`, nothingMirrorRelatedOutsideTheSandboxHome)
	ctx.Step(`^I remember the mirror link of "([^"]*)"$`, iRememberTheMirrorLink)
	ctx.Step(`^the mirror link of "([^"]*)" is unchanged$`, theMirrorLinkIsUnchanged)
	ctx.Step(`^no mirror directory exists under the sandbox home$`, noMirrorDirectoryExists)
	registerMirrorDamageSteps(ctx)
	registerMirrorCommandSteps(ctx)
	registerMirrorRaceSteps(ctx)
	registerMirrorRefreshSteps(ctx)
	registerMirrorLostSteps(ctx)
	registerMirrorHealthySteps(ctx)
	registerMirrorHostileSteps(ctx)
}

// mirrorState returns the scenario's mirror state, allocating it on first use.
func mirrorState(ctx context.Context) (*testState, *mirrorScenario, error) {
	s := getState(ctx)
	if s == nil {
		return nil, nil, fmt.Errorf("no test state")
	}
	if s.mirror == nil {
		s.mirror = &mirrorScenario{
			rootCommits: map[string]string{},
			borrowers:   map[string][]string{},
			snapshots:   map[string]borrowerSnapshot{},
		}
	}
	return s, s.mirror, nil
}

// mirrorWorkspaceKey is the workspace key niwa derives from the workspace
// root's real path.
func mirrorWorkspaceKey(s *testState) (string, error) {
	real, err := filepath.EvalSymlinks(s.workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving the workspace root: %w", err)
	}
	sum := sha256.Sum256([]byte(real))
	return "w-" + hex.EncodeToString(sum[:])[:32], nil
}

func mirrorKeyDir(s *testState) (string, error) {
	key, err := mirrorWorkspaceKey(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.homeDir, ".niwa", key), nil
}

func mirrorsDirOf(s *testState) (string, error) {
	dir, err := mirrorKeyDir(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mirrors"), nil
}

func mirrorStablePath(s *testState, repoID string) (string, error) {
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, repoID), nil
}

// mirrorStore resolves the stable path's link to the store directory, after
// checking the link is the relative .store/<id>-<stamp>-<rand> niwa writes.
func mirrorStore(s *testState, repoID string) (string, error) {
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(stable)
	if err != nil {
		return "", fmt.Errorf("the mirror's stable path %s: %w", stable, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("the mirror's stable path %s is not a symlink (mode %s)", stable, info.Mode())
	}
	target, err := os.Readlink(stable)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`^\.store/` + regexp.QuoteMeta(repoID) + `-[0-9]{8}T[0-9]{6}Z-[0-9a-f]+$`)
	if !re.MatchString(target) {
		return "", fmt.Errorf("the mirror link's target %q is not a relative .store/%s-<UTC stamp>-<rand>", target, repoID)
	}
	store := filepath.Join(filepath.Dir(stable), target)
	st, err := os.Lstat(store)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("the mirror link's target %s is not a directory (%v)", store, err)
	}
	return store, nil
}

func aSourceRepoExistsWithCommits(ctx context.Context, name string, n int) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	url, err := s.gitServer.Repo(name)
	if err != nil {
		return ctx, err
	}
	s.repoURLs[name] = url

	work, err := os.MkdirTemp(s.gitServer.root, "commits-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", url, work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", name, err, out)
	}
	if out, err := fixtureGitWorkTree(work, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return ctx, fmt.Errorf("pointing HEAD at main: %w\n%s", err, out)
	}
	for i := 1; i <= n; i++ {
		file := fmt.Sprintf("file%d.txt", i)
		if err := os.WriteFile(filepath.Join(work, file), []byte(strings.Repeat(file+"\n", 50)), 0o644); err != nil {
			return ctx, err
		}
		if out, err := fixtureGitCommit(work, "add", file); err != nil {
			return ctx, fmt.Errorf("git add: %w\n%s", err, out)
		}
		if out, err := fixtureGitCommit(work, "commit", "--quiet", "-m", "commit "+strconv.Itoa(i)); err != nil {
			return ctx, fmt.Errorf("git commit: %w\n%s", err, out)
		}
	}
	if out, err := fixtureGitCommit(work, "push", "--quiet", "origin", "main"); err != nil {
		return ctx, fmt.Errorf("git push: %w\n%s", err, out)
	}
	root, err := fixtureGitWorkTree(work, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return ctx, fmt.Errorf("finding the root commit: %w\n%s", err, root)
	}
	m.rootCommits[name] = strings.TrimSpace(string(root))
	return ctx, nil
}

// theClonesBorrowFromTheMirror writes each clone's alternates to name the
// mirror's stable path, which is what a reference clone made by the borrowing
// create will hold. The mirror itself need not exist yet: niwa's next command
// finds borrowers naming a missing mirror and builds it.
func theClonesBorrowFromTheMirror(ctx context.Context, groupRepo, inst1, inst2, repoID string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return ctx, err
	}
	for _, inst := range []string{inst1, inst2} {
		clone := filepath.Join(s.workspaceRoot, inst, groupRepo)
		info := filepath.Join(clone, ".git", "objects", "info")
		if err := os.MkdirAll(info, 0o755); err != nil {
			return ctx, err
		}
		if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(stable+"/objects\n"), 0o644); err != nil {
			return ctx, err
		}
		m.borrowers[repoID] = append(m.borrowers[repoID], clone)
	}
	return ctx, nil
}

func theMirrorIsALinkIntoItsStore(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return err
	}
	if out, err := fixtureGitBare(store, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return fmt.Errorf("git rev-parse HEAD^{commit} in the store %s: %w\n%s", store, err, out)
	}
	return nil
}

func theMirrorHasTheRemoteURL(ctx context.Context, repoID, source string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return err
	}
	out, err := fixtureGitBare(store, "config", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("reading remote.origin.url: %w\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), s.repoURLs[source]; got != want {
		return fmt.Errorf("the mirror's remote.origin.url = %q, want %q", got, want)
	}
	return nil
}

func borrowersOf(m *mirrorScenario, repoID string) ([]string, error) {
	clones := m.borrowers[repoID]
	if len(clones) == 0 {
		return nil, fmt.Errorf("no borrowers recorded for %q", repoID)
	}
	return clones, nil
}

// iRepackTheBorrowers drops from each borrower every object the mirror holds:
// -l packs only the borrower's own objects and -d removes the old packs.
func iRepackTheBorrowers(ctx context.Context, repoID string) (context.Context, error) {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return ctx, err
	}
	for _, clone := range clones {
		if out, err := fixtureGitWorkTree(clone, "repack", "-a", "-d", "-l", "-q"); err != nil {
			return ctx, fmt.Errorf("git repack in %s: %w\n%s", clone, err, out)
		}
		if out, err := fixtureGitWorkTree(clone, "prune-packed"); err != nil {
			return ctx, fmt.Errorf("git prune-packed in %s: %w\n%s", clone, err, out)
		}
	}
	return ctx, nil
}

// countObjects parses git count-objects -v.
func countObjects(clone string) (map[string]string, error) {
	out, err := fixtureGitWorkTree(clone, "count-objects", "-v")
	if err != nil {
		return nil, fmt.Errorf("git count-objects in %s: %w\n%s", clone, err, out)
	}
	fields := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), ": "); ok {
			fields[k] = v
		}
	}
	return fields, nil
}

func eachBorrowerHoldsNoObjects(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		fields, err := countObjects(clone)
		if err != nil {
			return err
		}
		if fields["count"] != "0" || fields["in-pack"] != "0" {
			return fmt.Errorf("borrower %s holds objects of its own (count %s, in-pack %s); it does not depend on the mirror",
				clone, fields["count"], fields["in-pack"])
		}
	}
	return nil
}

func theBorrowerHasUnpushedWork(ctx context.Context, repoID, inst string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clone := ""
	for _, c := range m.borrowers[repoID] {
		if strings.HasPrefix(c, filepath.Join(s.workspaceRoot, inst)+string(filepath.Separator)) {
			clone = c
		}
	}
	if clone == "" {
		return ctx, fmt.Errorf("no borrower of %q in instance %q", repoID, inst)
	}
	if err := os.WriteFile(filepath.Join(clone, "unpushed.txt"), []byte("work nobody pushed\n"), 0o644); err != nil {
		return ctx, err
	}
	if out, err := fixtureGitCommit(clone, "add", "unpushed.txt"); err != nil {
		return ctx, fmt.Errorf("git add: %w\n%s", err, out)
	}
	if out, err := fixtureGitCommit(clone, "commit", "--quiet", "-m", "unpushed work"); err != nil {
		return ctx, fmt.Errorf("git commit: %w\n%s", err, out)
	}
	commit, err := fixtureGitWorkTree(clone, "rev-parse", "HEAD")
	if err != nil {
		return ctx, fmt.Errorf("git rev-parse: %w\n%s", err, commit)
	}
	f, err := os.OpenFile(filepath.Join(clone, "file1.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return ctx, err
	}
	_, werr := f.WriteString("an edit nobody committed\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return ctx, werr
	}
	status, err := fixtureGitWorkTree(clone, "status", "--porcelain")
	if err != nil {
		return ctx, fmt.Errorf("git status: %w\n%s", err, status)
	}
	if !strings.Contains(string(status), "file1.txt") {
		return ctx, fmt.Errorf("git status does not show the edit: %q", status)
	}
	m.unpushed.clone = clone
	m.unpushed.commit = strings.TrimSpace(string(commit))
	m.unpushed.status = string(status)
	return ctx, nil
}

// takeSnapshot records what a restore must leave alone in a borrower.
func takeSnapshot(clone string) (borrowerSnapshot, error) {
	var snap borrowerSnapshot
	var err error
	if snap.alternates, err = os.ReadFile(filepath.Join(clone, ".git", "objects", "info", "alternates")); err != nil {
		return snap, err
	}
	for _, q := range []struct {
		dst  *string
		args []string
	}{
		{&snap.refs, []string{"for-each-ref", "--format=%(refname) %(objectname)"}},
		{&snap.head, []string{"symbolic-ref", "HEAD"}},
		{&snap.index, []string{"ls-files", "-s"}},
		{&snap.status, []string{"status", "--porcelain"}},
	} {
		out, err := fixtureGitWorkTree(clone, q.args...)
		if err != nil {
			return snap, fmt.Errorf("git %s in %s: %w\n%s", q.args[0], clone, err, out)
		}
		*q.dst = string(out)
	}
	tracked, err := fixtureGitWorkTree(clone, "ls-files")
	if err != nil {
		return snap, fmt.Errorf("git ls-files in %s: %w\n%s", clone, err, tracked)
	}
	snap.files = map[string]string{}
	for _, rel := range strings.Fields(string(tracked)) {
		data, err := os.ReadFile(filepath.Join(clone, rel))
		if err != nil {
			return snap, err
		}
		snap.files[rel] = string(data)
	}
	return snap, nil
}

func iSnapshotTheBorrowers(ctx context.Context, repoID string) (context.Context, error) {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return ctx, err
	}
	for _, clone := range clones {
		snap, err := takeSnapshot(clone)
		if err != nil {
			return ctx, err
		}
		m.snapshots[clone] = snap
	}
	return ctx, nil
}

func iDeleteTheMirrorAndItsStore(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return ctx, err
	}
	if err := os.Remove(stable); err != nil {
		return ctx, err
	}
	return ctx, os.RemoveAll(store)
}

func gitFsckReportsMissingObjects(ctx context.Context, repoID, inst string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	for _, clone := range m.borrowers[repoID] {
		if !strings.HasPrefix(clone, filepath.Join(s.workspaceRoot, inst)+string(filepath.Separator)) {
			continue
		}
		out, err := fixtureGitWorkTree(clone, "fsck", "--full")
		if err == nil {
			return fmt.Errorf("git fsck succeeded in %s with the mirror gone; the borrower does not depend on it\n%s", clone, out)
		}
		if !strings.Contains(string(out), "missing") {
			return fmt.Errorf("git fsck in %s failed without reporting missing objects:\n%s", clone, out)
		}
		return nil
	}
	return fmt.Errorf("no borrower of %q in instance %q", repoID, inst)
}

func eachBorrowerReadsToTheRoot(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		if out, err := fixtureGitWorkTree(clone, "log", "--oneline", "main"); err != nil {
			return fmt.Errorf("git log main in %s: %w\n%s", clone, err, out)
		}
		root, err := fixtureGitWorkTree(clone, "rev-list", "--max-parents=0", "main")
		if err != nil {
			return fmt.Errorf("git rev-list in %s: %w\n%s", clone, err, root)
		}
		if got, want := strings.TrimSpace(string(root)), m.rootCommits[repoID]; got != want {
			return fmt.Errorf("main in %s reaches root %q, want %q", clone, got, want)
		}
		out, err := fixtureGitWorkTree(clone, "fsck", "--full")
		if err != nil {
			return fmt.Errorf("git fsck --full in %s: %w\n%s", clone, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			for _, bad := range []string{"missing", "broken", "dangling"} {
				if strings.Contains(line, bad) {
					return fmt.Errorf("git fsck --full in %s reports %q", clone, line)
				}
			}
		}
	}
	return nil
}

func theBorrowersMatchTheirSnapshot(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		before, ok := m.snapshots[clone]
		if !ok {
			return fmt.Errorf("no snapshot of %s", clone)
		}
		after, err := takeSnapshot(clone)
		if err != nil {
			return err
		}
		if string(after.alternates) != string(before.alternates) {
			return fmt.Errorf("alternates in %s changed from %q to %q", clone, before.alternates, after.alternates)
		}
		for _, c := range []struct{ what, before, after string }{
			{"refs", before.refs, after.refs},
			{"HEAD", before.head, after.head},
			{"index", before.index, after.index},
			{"status", before.status, after.status},
		} {
			if c.before != c.after {
				return fmt.Errorf("%s in %s changed:\nbefore: %s\nafter:  %s", c.what, clone, c.before, c.after)
			}
		}
		if len(after.files) != len(before.files) {
			return fmt.Errorf("tracked files in %s changed from %d to %d", clone, len(before.files), len(after.files))
		}
		for rel, data := range before.files {
			if after.files[rel] != data {
				return fmt.Errorf("tracked file %s in %s changed", rel, clone)
			}
		}
	}
	return nil
}

func theUnpushedWorkIsIntact(ctx context.Context) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	u := m.unpushed
	if u.clone == "" {
		return fmt.Errorf("no unpushed work was recorded")
	}
	if out, err := fixtureGitWorkTree(u.clone, "merge-base", "--is-ancestor", u.commit, "main"); err != nil {
		return fmt.Errorf("the unpushed commit %s is not reachable from main in %s: %w\n%s", u.commit, u.clone, err, out)
	}
	status, err := fixtureGitWorkTree(u.clone, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w\n%s", err, status)
	}
	if string(status) != u.status {
		return fmt.Errorf("git status in %s = %q, want %q", u.clone, status, u.status)
	}
	return nil
}

// mirrorWarnings returns the stderr lines that are mirror warnings.
func mirrorWarnings(stderr string) []string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "warning: mirror ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func theErrorOutputCarriesOneMirrorWarning(ctx context.Context, repoID, fault, result string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines := mirrorWarnings(s.stderr)
	if len(lines) != 1 {
		return fmt.Errorf("stderr carries %d mirror warnings, want 1:\n%s", len(lines), s.stderr)
	}
	for _, want := range []string{"mirror " + repoID + ":", fault, result} {
		if !strings.Contains(lines[0], want) {
			return fmt.Errorf("mirror warning %q does not name %q", lines[0], want)
		}
	}
	return nil
}

func theErrorOutputCarriesNoMirrorWarning(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if lines := mirrorWarnings(s.stderr); len(lines) != 0 {
		return fmt.Errorf("stderr carries mirror warnings: %q", lines)
	}
	return nil
}

func noRestoreLeftoversRemain(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	for _, pattern := range []string{filepath.Join(dir, ".tmp", "*"), filepath.Join(dir, ".link-*")} {
		left, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(left) != 0 {
			return fmt.Errorf("restore leftovers remain: %v", left)
		}
	}
	// A state file a restore wrote under its temporary name and never
	// renamed into place.
	stateTmps, err := filepath.Glob(filepath.Join(dir, ".meta", ".*.json-*"))
	if err != nil {
		return err
	}
	if len(stateTmps) != 0 {
		return fmt.Errorf("temporary state files remain: %v", stateTmps)
	}
	return nil
}

func theMirrorModesAreTight(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	keyDir, err := mirrorKeyDir(s)
	if err != nil {
		return err
	}
	info, err := os.Lstat(keyDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("the workspace key directory %s has mode %s, want a 0700 directory", keyDir, info.Mode())
	}
	locks, err := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "locks", "m-*.lock"))
	if err != nil {
		return err
	}
	if len(locks) != 1 {
		return fmt.Errorf("found %d mirror locks, want 1: %v", len(locks), locks)
	}
	info, err = os.Lstat(locks[0])
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("the mirror lock %s has mode %s, want a 0600 regular file", locks[0], info.Mode())
	}
	return nil
}

// nothingMirrorRelatedOutsideTheSandboxHome checks the real home for this
// workspace's key directory and lock, and the workspace root (with every
// instance under it) for any mirror entry at all.
func nothingMirrorRelatedOutsideTheSandboxHome(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	key, err := mirrorWorkspaceKey(s)
	if err != nil {
		return err
	}
	if realHome, herr := os.UserHomeDir(); herr == nil && realHome != s.homeDir {
		if _, err := os.Lstat(filepath.Join(realHome, ".niwa", key)); err == nil {
			return fmt.Errorf("the workspace key directory exists under the real home %s", realHome)
		}
		locks, _ := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "locks", "m-*.lock"))
		for _, lock := range locks {
			if _, err := os.Lstat(filepath.Join(realHome, ".niwa", "locks", filepath.Base(lock))); err == nil {
				return fmt.Errorf("the mirror lock %s exists under the real home", filepath.Base(lock))
			}
		}
	}
	lockName := regexp.MustCompile(`^m-[0-9a-f]+\.lock$`)
	return filepath.WalkDir(s.workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if name == "mirrors" || name == ".store" || lockName.MatchString(name) {
			return fmt.Errorf("a mirror entry exists outside the sandbox home: %s", path)
		}
		return nil
	})
}

func iRememberTheMirrorLink(ctx context.Context, repoID string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return ctx, err
	}
	m.link, err = os.Readlink(stable)
	return ctx, err
}

func theMirrorLinkIsUnchanged(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return err
	}
	got, err := os.Readlink(stable)
	if err != nil {
		return err
	}
	if got != m.link {
		return fmt.Errorf("the mirror link moved from %q to %q", m.link, got)
	}
	return nil
}

func noMirrorDirectoryExists(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	found, err := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "w-*", "mirrors"))
	if err != nil {
		return err
	}
	if len(found) != 0 {
		return fmt.Errorf("a mirror directory exists with no borrowers: %v", found)
	}
	return nil
}
