package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/watch"
)

// A review instance as a stage leaves it: pr-clone holding the PR's commit,
// the agent's draft, and a file the agent wrote, with its staged record
// saved and no session mapping naming it.

// TestReapVerdictOnly_AReviewInstanceIsListedAndNothingIsRemoved pins what
// `niwa reap --verdict-only` lists for a review instance today, as text and
// as a niwa.verdicts/v1 document, and that the listing removes nothing: the
// instance, pr-clone, the draft, the agent's file and the staged record are
// all still there after it.
func TestReapVerdictOnly_AReviewInstanceIsListedAndNothingIsRemoved(t *testing.T) {
	f := newPassFixture(t)
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	const draft = "the review agent's draft\n"
	const notes = "written by the review agent: 7f3c\n"
	rec := stagedReviewInstance(t, f.root, draft)
	notesPath := filepath.Join(rec.InstancePath, "agent-notes.txt")
	writeTestFile(t, notesPath, notes)
	name := filepath.Base(rec.InstancePath)

	text, errOut, err := runReapCommand(t, f.root, "--verdict-only", name)
	if err != nil {
		t.Fatalf("verdict-only reap of %s: %v\n%s", name, err, errOut)
	}
	const wantText = "test-ws+watch-acme-widget-7-0a1b2c3d: keep\n" +
		"  liveness check: no session mapping names it, so there is no session whose liveness could be read\n" +
		"  repository check: pr-clone: clone URL unreachable: no clone URL for this repository\n" +
		"    pr-clone\n" +
		"  outside check: files outside every repository: agent-notes.txt, watch-review-draft.md\n" +
		"    agent-notes.txt\n" +
		"    watch-review-draft.md\n" +
		"  paths:\n" +
		"    .niwa/ generated\n" +
		"    agent-notes.txt unique\n" +
		"    pr-clone/.git/ unique\n" +
		"    pr-clone/change.go tracked\n" +
		"    watch-review-draft.md unique\n"
	if text != wantText {
		t.Errorf("verdict-only text for the review instance:\n%s\nwant:\n%s", text, wantText)
	}

	raw, errOut, err := runReapCommand(t, f.root, "--verdict-only", "--json", name)
	if err != nil {
		t.Fatalf("verdict-only --json reap of %s: %v\n%s", name, err, errOut)
	}
	doc := decodeVerdictsStrictly(t, strings.NewReader(raw))
	if doc.Schema != "niwa.verdicts/v1" || len(doc.Units) != 1 {
		t.Fatalf("document %+v; want one unit", doc)
	}
	u := doc.Units[0]
	if u.Instance != name || u.Verdict != "keep" || !u.Reached {
		t.Errorf("unit %s verdict %q reached %v; want %s, keep, reached", u.Instance, u.Verdict, u.Reached, name)
	}
	var checks []string
	for _, c := range u.Checks {
		checks = append(checks, c.Check+"="+strings.Join(c.Paths, ","))
	}
	if got, want := strings.Join(checks, " "), "liveness= repository=pr-clone outside=agent-notes.txt,watch-review-draft.md"; got != want {
		t.Errorf("checks %q, want %q", got, want)
	}
	classes := map[string]string{}
	for _, p := range u.Paths {
		classes[p.Path] = p.Class
	}
	for _, path := range []string{"agent-notes.txt", watch.DefaultDraftRelPath, watch.DefaultCloneRelDir + "/.git/"} {
		if classes[path] != "unique" {
			t.Errorf("path %s has class %q, want unique (paths %+v)", path, classes[path], u.Paths)
		}
	}

	all, errOut, err := runReapCommand(t, f.root, "--verdict-only")
	if err != nil {
		t.Fatalf("verdict-only reap: %v\n%s", err, errOut)
	}
	if !strings.Contains(all, wantText) || !strings.Contains(all, "test-ws-pass: reap\n") {
		t.Errorf("the workspace listing doesn't carry the review instance's keep and the clean unit's reap:\n%s", all)
	}

	if len(*f.destroyed) != 0 {
		t.Fatalf("destroyed %v; a verdict-only reap removes nothing", *f.destroyed)
	}
	assertInstanceListed(t, f.root, rec.InstancePath)
	if _, err := os.Stat(filepath.Join(rec.InstancePath, watch.DefaultCloneRelDir, ".git")); err != nil {
		t.Errorf("pr-clone's git directory is gone: %v", err)
	}
	assertFileUnchanged(t, filepath.Join(rec.InstancePath, watch.DefaultCloneRelDir, "change.go"), "package change\n")
	assertFileUnchanged(t, rec.DraftPath, draft)
	assertFileUnchanged(t, notesPath, notes)
	if _, err := watch.LoadStagedRecord(f.root, rec.Handle); err != nil {
		t.Errorf("the staged record is gone after a verdict-only reap: %v", err)
	}
	if strings.Contains(f.stderr.String(), "niwa: kept") {
		t.Errorf("a verdict-only reap printed the reap's kept line: %q", f.stderr.String())
	}
}

// TestWatchDiscardRule_ADiscardGivenNoLivenessReadsItAndKeepsALiveReview
// pins discardStagedRecord's default when its caller passes no liveness:
// it reads stagedInstanceLiveFunc for the instance itself, and a live
// reading reaches the removal rule, whose first keeping check is then
// liveness. The instance stays, nothing is destroyed, the record is deleted
// as the keep path deletes it, and the discard reports false so no on-reap
// command runs.
func TestWatchDiscardRule_ADiscardGivenNoLivenessReadsItAndKeepsALiveReview(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	installWatchCharFakes(t)
	claudeListingOnPath(t)
	var destroyed []string
	destroyInstanceFunc = func(p string) error { // installWatchCharFakes restores the original
		destroyed = append(destroyed, p)
		return realDestroyInstance(p)
	}
	const draft = "the review agent's draft\n"
	rec := stagedReviewInstance(t, root, draft)
	var liveCalls []string
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, instancePath string) bool {
		liveCalls = append(liveCalls, instancePath)
		return true
	}
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })

	var out bytes.Buffer
	discarded := discardStagedRecord(&out, root, rec.Handle, rec, "stale", newRemovalGate(root, removeStepWatchDiscard, &out))

	if len(liveCalls) != 1 || liveCalls[0] != rec.InstancePath {
		t.Errorf("stagedInstanceLiveFunc read for %q; want one read for %s", liveCalls, rec.InstancePath)
	}
	if discarded {
		t.Error("discardStagedRecord reported the review discarded; a kept instance reports false")
	}
	if len(destroyed) != 0 {
		t.Errorf("destroyed %v; the rule keeps a live review's instance", destroyed)
	}
	assertInstanceListed(t, root, rec.InstancePath)
	assertFileUnchanged(t, rec.DraftPath, draft)
	if _, err := os.Stat(filepath.Join(rec.InstancePath, watch.DefaultCloneRelDir, ".git")); err != nil {
		t.Errorf("pr-clone's git directory is gone: %v", err)
	}
	if _, err := watch.LoadStagedRecord(root, rec.Handle); err == nil {
		t.Error("the staged record is still there; the discard deletes it when the rule keeps the instance")
	}
	for _, want := range []string{
		"niwa: kept test-ws+watch-acme-widget-7-0a1b2c3d: liveness check: its session is still there\n",
		"niwa watch: discarding staged review for acme/widget#7 (handle watch-acme-widget-7): stale; the removal rule kept its instance\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("discard output does not carry %q:\n%s", want, out.String())
		}
	}
}
