package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// writeCorruptStagedRecord drops a syntactically-invalid record file into the
// staged-record store so LoadStagedRecord fails to decode it (a partial/corrupt
// write the prune must skip, not act on).
func writeCorruptStagedRecord(t *testing.T, root, handle string) {
	t.Helper()
	dir := filepath.Join(root, ".niwa", "watch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, handle+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeFreshnessClient is a prFreshnessClient seam for the prune tests: it maps a
// PR number to its current head and a "base..head" pair to an ancestry, returning
// an inconclusive (Unknown + error) result for an unmapped compare so the
// conservative-keep path is exercised.
type fakeFreshnessClient struct {
	headByNum  map[int]github.PullHead
	ancestry   map[string]github.Ancestry
	headErrNum map[int]error
}

func (f *fakeFreshnessClient) GetPullHead(_ context.Context, _, _ string, number int) (github.PullHead, error) {
	if err := f.headErrNum[number]; err != nil {
		return github.PullHead{}, err
	}
	return f.headByNum[number], nil
}

func (f *fakeFreshnessClient) CompareCommits(_ context.Context, _, _, base, head string) (github.Ancestry, error) {
	if a, ok := f.ancestry[base+".."+head]; ok {
		return a, nil
	}
	return github.AncestryUnknown, errors.New("compare unavailable")
}

// TestPruneStagedRecords covers the full GC matrix in one pass: a dead record is
// pruned, a live+fresh record is kept, a live+not-requested and a live+diverged
// record are discarded, and a live record with an inconclusive ancestry check is
// kept (conservative). Discarded/dead records get their instance best-effort
// destroyed.
func TestPruneStagedRecords(t *testing.T) {
	root := t.TempDir()

	recs := []watch.StagedRecord{
		{Handle: "dead0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: "/inst/dead", DispatchedSHA: "aaaa111"},
		{Handle: "fresh0002", Owner: "acme", Repo: "api", Number: 2, InstancePath: "/inst/fresh", DispatchedSHA: "bbbb222"},
		{Handle: "notreq003", Owner: "acme", Repo: "api", Number: 3, InstancePath: "/inst/notreq", DispatchedSHA: "cccc333"},
		{Handle: "diverg004", Owner: "acme", Repo: "api", Number: 4, InstancePath: "/inst/div", DispatchedSHA: "dddd444"},
		{Handle: "unknwn005", Owner: "acme", Repo: "api", Number: 5, InstancePath: "/inst/unk", DispatchedSHA: "eeee555"},
	}
	for _, r := range recs {
		if err := watch.SaveStagedRecord(root, r); err != nil {
			t.Fatalf("SaveStagedRecord %s: %v", r.Handle, err)
		}
	}

	// Every record but the first is live.
	liveByInstance := map[string]bool{
		"/inst/fresh":  true,
		"/inst/notreq": true,
		"/inst/div":    true,
		"/inst/unk":    true,
	}
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_ /*jobsDir*/, instancePath string) bool { return liveByInstance[instancePath] }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })

	var destroyed []string
	origDestroy := destroyInstanceFunc
	destroyInstanceFunc = func(instancePath string) error { destroyed = append(destroyed, instancePath); return nil }
	t.Cleanup(func() { destroyInstanceFunc = origDestroy })

	// PRs #2, #4, #5 still requesting review; #3 no longer requested.
	requested := map[string]bool{
		"acme/api#2": true,
		"acme/api#4": true,
		"acme/api#5": true,
	}

	client := &fakeFreshnessClient{
		headByNum: map[int]github.PullHead{
			2: {SHA: "bbbb222"}, // == dispatched: ordinary (identical) -> Ancestor, fresh
			4: {SHA: "ffff444"}, // advanced; compare says diverged -> stale
			5: {SHA: "ffff555"}, // advanced; compare unmapped -> Unknown -> kept
		},
		ancestry: map[string]github.Ancestry{
			"dddd444..ffff444": github.AncestryDiverged,
		},
	}

	pruneStagedRecords(context.Background(), io.Discard, root, client, requested)

	gotHandles, err := watch.ListStagedHandles(root)
	if err != nil {
		t.Fatalf("ListStagedHandles: %v", err)
	}
	wantHandles := []string{"fresh0002", "unknwn005"}
	if !equalStrings(gotHandles, wantHandles) {
		t.Errorf("surviving handles = %v, want %v", gotHandles, wantHandles)
	}

	sort.Strings(destroyed)
	wantDestroyed := []string{"/inst/dead", "/inst/div", "/inst/notreq"}
	if !equalStrings(destroyed, wantDestroyed) {
		t.Errorf("destroyed instances = %v, want %v", destroyed, wantDestroyed)
	}
}

// TestPruneStagedRecords_OnReap: discarding a staged review whose session is
// gone runs the on-reap command once, with the reap key the review was
// launched with; a stale review whose session is still live runs nothing; a
// failing command prints one warning line and the pass goes on.
func TestPruneStagedRecords_OnReap(t *testing.T) {
	for _, failing := range []bool{false, true} {
		t.Run(map[bool]string{false: "succeeds", true: "fails"}[failing], func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			command, logPath := onReapLogCommand(t)
			if failing {
				command += "; exit 1"
			}
			appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
			recs := []watch.StagedRecord{
				{Handle: "gone0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: "/inst/gone", DispatchedSHA: "aaaa111", SessionID: reapDeadSessionID, ReapKey: onReapDispatchID},
				{Handle: "stale002", Owner: "acme", Repo: "api", Number: 2, InstancePath: "/inst/stale", DispatchedSHA: "bbbb222", SessionID: onReapSID2, ReapKey: "review-key-2"},
				{Handle: "noid0003", Owner: "acme", Repo: "api", Number: 3, InstancePath: "/inst/noid", DispatchedSHA: "cccc333"},
			}
			for _, r := range recs {
				if err := watch.SaveStagedRecord(root, r); err != nil {
					t.Fatal(err)
				}
			}
			origLive := stagedInstanceLiveFunc
			stagedInstanceLiveFunc = func(_, instancePath string) bool { return instancePath == "/inst/stale" }
			t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
			stubDestroyAll(t)

			var out bytes.Buffer
			// PR #2 is no longer requested, so its live review is discarded as stale.
			pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})

			runs := onReapRuns(t, logPath)
			if len(runs) != 1 || runs[0] != reapDeadSessionID+" "+onReapDispatchID+" /inst/gone" {
				t.Errorf("runs = %q, want one for the gone session with its review key", runs)
			}
			warnings := onReapWarnings(out.String())
			if failing && len(warnings) != 1 {
				t.Errorf("warnings = %q, want one", warnings)
			}
			if !failing && len(warnings) != 0 {
				t.Errorf("warnings = %q, want none", warnings)
			}
			if handles, _ := watch.ListStagedHandles(root); len(handles) != 0 {
				t.Errorf("records left = %v, want every one discarded", handles)
			}
		})
	}
}

// TestPruneStagedRecords_OnReapUsesMapping: a gone review that does have a
// session mapping is settled like a reap: the key comes from the mapping and
// a failed run keeps it.
func TestPruneStagedRecords_OnReapUsesMapping(t *testing.T) {
	root := setupDispatchWorkspace(t)
	appendOnReapConfig(t, root, `command = "exit 1"`)
	writeMapping(t, root, workspace.SessionMapping{SessionID: reapDeadSessionID, InstancePath: "/inst/gone", DispatchID: onReapDispatchID, Ephemeral: true})
	if err := watch.SaveStagedRecord(root, watch.StagedRecord{Handle: "gone0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: "/inst/gone", SessionID: reapDeadSessionID}); err != nil {
		t.Fatal(err)
	}
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, _ string) bool { return false }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
	stubDestroyAll(t)

	var out bytes.Buffer
	pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})
	m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
	if err != nil || m.OnReapFailures != 1 {
		t.Errorf("mapping = %+v, %v; want it kept with one failed run", m, err)
	}
	if w := onReapWarnings(out.String()); len(w) != 1 {
		t.Errorf("warnings = %q, want one", w)
	}
}

// TestPruneStagedRecords_BadRecordSkipped proves a record that fails to load does
// not abort the sweep: a good record alongside it is still evaluated and kept.
func TestPruneStagedRecords_BadRecordSkipped(t *testing.T) {
	root := t.TempDir()
	good := watch.StagedRecord{Handle: "good0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: "/inst/good", DispatchedSHA: "aaaa111"}
	if err := watch.SaveStagedRecord(root, good); err != nil {
		t.Fatalf("SaveStagedRecord: %v", err)
	}
	// A corrupt record file that LoadStagedRecord will fail to decode.
	writeCorruptStagedRecord(t, root, "bad00002")

	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, _ string) bool { return true }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
	origDestroy := destroyInstanceFunc
	destroyInstanceFunc = func(string) error { return nil }
	t.Cleanup(func() { destroyInstanceFunc = origDestroy })

	client := &fakeFreshnessClient{headByNum: map[int]github.PullHead{1: {SHA: "aaaa111"}}}
	requested := map[string]bool{"acme/api#1": true}

	pruneStagedRecords(context.Background(), io.Discard, root, client, requested)

	got, err := watch.ListStagedHandles(root)
	if err != nil {
		t.Fatalf("ListStagedHandles: %v", err)
	}
	// The good record survives; the corrupt one is left in place (skipped, not
	// deleted -- the sweep does not act on a record it could not read).
	if !containsString(got, "good0001") {
		t.Errorf("good record was not kept: %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
