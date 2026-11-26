package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Failed-dispatch cleanup, the watch's discard and the watch's failed-stage
// rollback ask removal.Evaluate about their instance before
// destroyInstanceFunc. A keeping verdict leaves the instance on disk and the
// output names the first keeping check with the path it names.
//
// These run in process against the dispatch and watch seams, with the real
// destroy, a claude on PATH whose agents listing decodes and names no
// session, and every instance, repository and file under t.TempDir().

// claudeListingOnPath puts a claude on PATH whose every call prints an empty
// agents listing: no working session for any instance.
func claudeListingOnPath(t *testing.T) {
	t.Helper()
	dir := canonicalTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nprintf '[]\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakePRClone stands in for the fetch of a PR head: a repository in dir
// holding a commit no remote holds, as pr-clone holds the PR's head.
func fakePRClone(t *testing.T, dir string) {
	t.Helper()
	passGit(t, filepath.Dir(dir), "init", "-q", "-b", "pr", dir)
	writeTestFile(t, filepath.Join(dir, "change.go"), "package change\n")
	passGit(t, dir, "add", "change.go")
	passGit(t, dir, "commit", "-q", "-m", "the PR's change")
}

func assertFileUnchanged(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s is gone: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s holds %q, want %q unchanged", path, got, want)
	}
}

func assertInstanceListed(t *testing.T, root, inst string) {
	t.Helper()
	instances, err := workspace.EnumerateInstances(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range instances {
		if i == inst {
			return
		}
	}
	t.Errorf("instance %s is no longer listed (instances %q)", inst, instances)
}

func TestFailedDispatchRule_KeepsAPartialFileAndNamesIt(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	destroyInstanceFunc = realDestroyInstance // installDispatchFakes restores the original
	claudeListingOnPath(t)
	const partial = "half of the worker's output\n"
	inner := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string, n int) (provisionResult, error) {
		res, err := inner(ctx, r, cwd, namePrefix, sep, n)
		if err == nil {
			writeTestFile(t, filepath.Join(res.Path, "partial.txt"), partial)
		}
		return res, err
	}
	dispatchLaunch = func(context.Context, launchRequest) error { return errors.New("launch boom") }

	stdout, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("dispatch succeeded, want the launch error")
	}
	assertFileUnchanged(t, filepath.Join(f.instancePath, "partial.txt"), partial)
	if out := stdout + stderr + err.Error(); !strings.Contains(out, "partial.txt") {
		t.Errorf("the failed dispatch's output does not name partial.txt:\n%s", out)
	}
}

func TestFailedDispatchRule_NothingWrittenLeavesNoInstance(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	destroyInstanceFunc = realDestroyInstance // installDispatchFakes restores the original
	claudeListingOnPath(t)
	dispatchLaunch = func(context.Context, launchRequest) error { return errors.New("launch boom") }

	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("dispatch succeeded, want the launch error")
	}
	assertInstanceGone(t, f.instancePath)
}

// stagedReviewInstance makes a review instance as a stage leaves it: pr-clone
// holding the PR's commit and the agent's draft, with its staged record
// saved.
func stagedReviewInstance(t *testing.T, root, draft string) watch.StagedRecord {
	t.Helper()
	inst := makeReapInstance(t, root, "test-ws+watch-acme-widget-7-0a1b2c3d")
	fakePRClone(t, filepath.Join(inst, watch.DefaultCloneRelDir))
	writeTestFile(t, filepath.Join(inst, watch.DefaultDraftRelPath), draft)
	rec := watch.StagedRecord{
		Handle:        "watch-acme-widget-7",
		Owner:         watchCharPR.Owner,
		Repo:          watchCharPR.Repo,
		Number:        watchCharPR.Number,
		URL:           watchCharPR.URL,
		DraftPath:     filepath.Join(inst, watch.DefaultDraftRelPath),
		InstancePath:  inst,
		DispatchedSHA: watchCharHeadSHA,
	}
	if err := watch.SaveStagedRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestWatchDiscardRule_KeepsAReviewInstanceAndDeletesItsRecord(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)
	const draft = "the review agent's draft\n"
	rec := stagedReviewInstance(t, root, draft)

	var out bytes.Buffer
	discardStagedRecord(&out, root, rec.Handle, rec, "session no longer live", newRemovalGate(root, removeStepWatchDiscard, &out))

	assertFileUnchanged(t, rec.DraftPath, draft)
	assertInstanceListed(t, root, rec.InstancePath)
	if _, err := watch.LoadStagedRecord(root, rec.Handle); err == nil {
		t.Error("the staged record is still there; the discard deletes it even when the rule keeps the instance")
	}
	for _, want := range []string{"repository", watch.DefaultCloneRelDir} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("discard output does not name %q:\n%s", want, out.String())
		}
	}
}

func TestWatchRollbackRule_AFailureAfterTheLaunchKeepsTheInstanceAndRecordsTheHead(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	fakes := installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)
	fetchPRHeadFunc = func(_ context.Context, _, _, checkoutDir, _ string) error {
		fakePRClone(t, checkoutDir)
		return nil
	}
	const written = "written by the review agent before the stage failed: 7f3c\n"
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		writeTestFile(t, filepath.Join(req.InstanceDir, "agent-notes.txt"), written)
		return nil
	}
	saveCalls := 0
	prevSave := saveStagedRecordFunc
	saveStagedRecordFunc = func(string, watch.StagedRecord) error {
		saveCalls++
		return errors.New("disk full")
	}
	t.Cleanup(func() { saveStagedRecordFunc = prevSave })
	client := newPullHeadServer(t)
	cmd, stdout, stderr := newWatchCharCmd()

	err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
	if saveCalls == 0 {
		t.Fatalf("stageReview didn't save its staged record through saveStagedRecordFunc (err %v)", err)
	}
	if err == nil {
		t.Fatal("stageReview succeeded, want the save error")
	}
	inst := fakes.provisioned.path
	assertFileUnchanged(t, filepath.Join(inst, "agent-notes.txt"), written)
	assertInstanceListed(t, root, inst)
	out := stdout.String() + stderr.String() + err.Error()
	for _, want := range []string{"repository", watch.DefaultCloneRelDir} {
		if !strings.Contains(out, want) {
			t.Errorf("rollback output does not name %q:\n%s", want, out)
		}
	}
	handled, herr := watch.LoadHandledSet(root)
	if herr != nil {
		t.Fatal(herr)
	}
	if got := handled[watch.HandledIdentity(watchCharPR.Owner, watchCharPR.Repo, watchCharPR.Number)]; got != watchCharHeadSHA {
		t.Errorf("handled set records %q for the PR, want its head %s so the next pass doesn't stage it again", got, watchCharHeadSHA)
	}
}

func TestWatchRollbackRule_ALaunchFailureKeepsTheInstanceAndSaysTheHeadWontBeStagedAgain(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	fakes := installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)
	fetchPRHeadFunc = func(_ context.Context, _, _, checkoutDir, _ string) error {
		fakePRClone(t, checkoutDir)
		return nil
	}
	dispatchLaunch = func(context.Context, launchRequest) error { return errors.New("launch boom") }
	client := newPullHeadServer(t)
	cmd, stdout, stderr := newWatchCharCmd()

	err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
	if err == nil {
		t.Fatal("stageReview succeeded, want the launch error")
	}
	inst := fakes.provisioned.path
	if _, serr := os.Stat(filepath.Join(inst, watch.DefaultCloneRelDir, ".git")); serr != nil {
		t.Fatalf("pr-clone is gone after the rollback (%v); the rule keeps a pr-clone holding a commit no remote holds", serr)
	}
	assertInstanceListed(t, root, inst)
	out := stdout.String() + stderr.String() + err.Error()
	for _, want := range []string{"repository", watch.DefaultCloneRelDir, "won't be staged again"} {
		if !strings.Contains(out, want) {
			t.Errorf("rollback output does not say %q:\n%s", want, out)
		}
	}
	handled, herr := watch.LoadHandledSet(root)
	if herr != nil {
		t.Fatal(herr)
	}
	if got := handled[watch.HandledIdentity(watchCharPR.Owner, watchCharPR.Repo, watchCharPR.Number)]; got != watchCharHeadSHA {
		t.Errorf("handled set records %q for the PR, want its head %s", got, watchCharHeadSHA)
	}
}

func TestWatchRollbackRule_AFetchThatFailsBeforeWritingLeavesNothing(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	fakes := installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)
	fakes.fetchErr = errors.New("boom")
	client := newPullHeadServer(t)
	cmd, _, _ := newWatchCharCmd()

	if err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{}); err == nil {
		t.Fatal("stageReview succeeded, want the fetch error")
	}
	assertInstanceGone(t, fakes.provisioned.path)
	handled, err := watch.LoadHandledSet(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := handled[watch.HandledIdentity(watchCharPR.Owner, watchCharPR.Repo, watchCharPR.Number)]; ok {
		t.Errorf("handled set records %q for the PR after a fetch that wrote nothing; the next pass must stage it again", got)
	}
}
