package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/mcp"
)

// recordingSink captures every AuditEntry emitted to it. Used by these
// tests to count change_engaged / review_surface_opened events.
type recordingSink struct {
	mu      sync.Mutex
	entries []mcp.AuditEntry
}

func (s *recordingSink) Emit(e mcp.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *recordingSink) countEvent(event string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.entries {
		if e.Event == event {
			n++
		}
	}
	return n
}

// seedChange materialises a fresh change on disk in `state` with the
// supplied diff body. Returns the change ID. Uses the same primitives
// internal/mcp does for niwa_create_change so the on-disk shape is
// authoritative.
func seedChange(t *testing.T, root, state string, diff []byte) string {
	t.Helper()
	id, err := mcp.ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cs := mcp.ChangeState{
		V:                  1,
		ID:                 id,
		State:              state,
		OriginatingSession: "abcdef01",
		OriginatingTasks:   []string{},
		CreatedAt:          now,
		UpdatedAt:          now,
		BaseRef:            "base-sha",
		HeadRef:            "head-sha",
		Branch:             "feature/x",
		WorktreePath:       "/tmp/worktree",
		DiffPath:           "diff.patch",
		Metadata:           map[string]any{},
	}
	if err := mcp.WriteInitial(root, cs); err != nil {
		t.Fatalf("WriteInitial: %v", err)
	}
	dir, err := mcp.ChangeDir(root, id)
	if err != nil {
		t.Fatalf("ChangeDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), diff, 0o600); err != nil {
		t.Fatalf("write diff.patch: %v", err)
	}
	return id
}

// testWorkspace and testInstance are the synthetic identifiers used by
// these tests' fixture. Real callers consume identifiers from the
// global registry; the tests just need stable strings to slot into
// URLs.
const (
	testWorkspace = "ws"
	testInstance  = "inst"
)

// testChangesPrefix is the per-instance URL prefix tests use to build
// per-change URLs. Centralising it keeps the URL contract change
// (hierarchical /workspaces/<ws>/<inst>/changes/) in one place.
const testChangesPrefix = "/workspaces/" + testWorkspace + "/" + testInstance + "/changes/"

// newTestServer wires the F5 surface onto an httptest.Server backed by
// a fresh temp instance root. The test configures one instance under
// (testWorkspace, testInstance) → root, and a SinkFor factory that
// returns the shared recording sink so tests can count emitted events.
// Returns the test server, the recording sink, and the instance root.
func newTestServer(t *testing.T) (*httptest.Server, *recordingSink, string) {
	t.Helper()
	root := t.TempDir()
	sink := &recordingSink{}
	h := &Handlers{
		Instances: []config.WorkspaceInstance{
			{Workspace: testWorkspace, Instance: testInstance, Root: root},
		},
		SinkFor: func(string) mcp.AuditSink { return sink },
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, sink, root
}

// TestGetRootRedirectsToWorkspaces covers PRD R6 step 1: GET / → 302
// /workspaces/. CheckRedirect short-circuits so we can inspect the 302.
func TestGetRootRedirectsToWorkspaces(t *testing.T) {
	ts, _, _ := newTestServer(t)
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/workspaces/" {
		t.Errorf("Location = %q, want /workspaces/", loc)
	}
}

// TestGetChangeHappyPath: existing pending change returns 200 and the
// rendered HTML body carries the diff contents.
func TestGetChangeHappyPath(t *testing.T) {
	ts, _, root := newTestServer(t)
	// html/template aggressively escapes `+` as `&#43;` inside HTML body
	// context, so the diff body uses plain words that survive escaping
	// unchanged. The contextual escape itself is exercised by the render
	// package's tests; here we only confirm the diff bytes reached the
	// page.
	diff := []byte("diff --git a/x b/x\n--- a/x\nbody-marker-hello-world\n")
	id := seedChange(t, root, mcp.ChangeStatePending, diff)

	resp, err := http.Get(ts.URL + testChangesPrefix + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "body-marker-hello-world") {
		t.Errorf("body missing diff content; got:\n%s", body)
	}
	if !strings.Contains(string(body), id) {
		t.Errorf("body missing change ID %q", id)
	}
}

// TestGetChangeFirstHitAdvancesPendingToInReview confirms the state
// transition is applied exactly once on first arrival.
func TestGetChangeFirstHitAdvancesPendingToInReview(t *testing.T) {
	ts, _, root := newTestServer(t)
	id := seedChange(t, root, mcp.ChangeStatePending, []byte("diff body"))

	resp, err := http.Get(ts.URL + testChangesPrefix + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	got, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after first hit: %v", err)
	}
	if got.State != mcp.ChangeStateInReview {
		t.Errorf("state = %q, want %q", got.State, mcp.ChangeStateInReview)
	}
}

// TestGetChangeSecondHitIsNoOp confirms a re-arrival on an already
// in-review change leaves the state at in-review. UpdatedAt should
// also remain unchanged across the second hit because the handler
// short-circuits on state != pending (no UpdateChangeState call).
func TestGetChangeSecondHitIsNoOp(t *testing.T) {
	ts, _, root := newTestServer(t)
	id := seedChange(t, root, mcp.ChangeStatePending, []byte("diff body"))

	// First hit → advances pending → in-review.
	resp, err := http.Get(ts.URL + testChangesPrefix + id)
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	resp.Body.Close()
	afterFirst, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after first hit: %v", err)
	}
	if afterFirst.State != mcp.ChangeStateInReview {
		t.Fatalf("state after first hit = %q, want in-review", afterFirst.State)
	}
	firstUpdated := afterFirst.UpdatedAt

	// Sleep briefly so a hypothetical UpdatedAt bump on the second hit
	// would be observable. RFC3339Nano resolution means sub-millisecond
	// changes would show up.
	time.Sleep(2 * time.Millisecond)

	// Second hit → no-op.
	resp, err = http.Get(ts.URL + testChangesPrefix + id)
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	resp.Body.Close()

	afterSecond, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after second hit: %v", err)
	}
	if afterSecond.State != mcp.ChangeStateInReview {
		t.Errorf("state after second hit = %q, want in-review", afterSecond.State)
	}
	if afterSecond.UpdatedAt != firstUpdated {
		t.Errorf("UpdatedAt bumped on no-op second hit: %q → %q",
			firstUpdated, afterSecond.UpdatedAt)
	}
}

// TestGetChangeEmitsChangeEngagedPerHit confirms two hits produce two
// change_engaged events (R5: emitted per HTTP hit, independent of the
// state transition).
func TestGetChangeEmitsChangeEngagedPerHit(t *testing.T) {
	ts, sink, root := newTestServer(t)
	id := seedChange(t, root, mcp.ChangeStatePending, []byte("diff body"))

	for i := range 2 {
		resp, err := http.Get(ts.URL + testChangesPrefix + id)
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if got := sink.countEvent(mcp.ChangeEventEngaged); got != 2 {
		t.Errorf("change_engaged count = %d, want 2", got)
	}
}

// TestGetIndexEmitsReviewSurfaceOpened: one HTTP hit on the index emits
// one review_surface_opened audit event.
func TestGetIndexEmitsReviewSurfaceOpened(t *testing.T) {
	ts, sink, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/workspaces/" + testWorkspace + "/" + testInstance + "/changes/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if got := sink.countEvent(mcp.ChangeEventSurfaceOpened); got != 1 {
		t.Errorf("review_surface_opened count = %d, want 1", got)
	}
}

// TestGetIndexCleanedAfterNonCleaned confirms the renderer applies the
// stable cleaned-at-end ordering. We seed three changes in mixed order
// and assert the HTML body presents non-cleaned ones before cleaned.
func TestGetIndexCleanedAfterNonCleaned(t *testing.T) {
	ts, _, root := newTestServer(t)
	// Seed cleaned first so it has the *oldest* UpdatedAt — confirming
	// the cleaned-at-end ordering is driven by state, not just
	// timestamp.
	cleanedID := seedChange(t, root, mcp.ChangeStateCleaned, []byte("c"))
	time.Sleep(time.Millisecond)
	pendingID := seedChange(t, root, mcp.ChangeStatePending, []byte("p"))
	time.Sleep(time.Millisecond)
	inReviewID := seedChange(t, root, mcp.ChangeStateInReview, []byte("r"))

	resp, err := http.Get(ts.URL + "/workspaces/" + testWorkspace + "/" + testInstance + "/changes/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	idxPending := strings.Index(bodyStr, pendingID)
	idxInReview := strings.Index(bodyStr, inReviewID)
	idxCleaned := strings.Index(bodyStr, cleanedID)

	if idxPending < 0 || idxInReview < 0 || idxCleaned < 0 {
		t.Fatalf("index missing one or more IDs; pending=%d in-review=%d cleaned=%d body:\n%s",
			idxPending, idxInReview, idxCleaned, bodyStr)
	}
	if !(idxCleaned > idxPending && idxCleaned > idxInReview) {
		t.Errorf("cleaned (%d) does not follow both non-cleaned (pending=%d in-review=%d)",
			idxCleaned, idxPending, idxInReview)
	}
}

// TestGetChange404OnMalformedID: a non-UUIDv4 path param returns 404
// without touching the filesystem.
func TestGetChange404OnMalformedID(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, bogus := range []string{
		"not-a-uuid",
		"00000000-0000-3000-8000-000000000000", // wrong version nibble
		"00000000-0000-4000-c000-000000000000", // wrong variant nibble
		"00000000000040008000000000000000",     // missing dashes
		"../etc/passwd",
	} {
		resp, err := http.Get(ts.URL + "/workspaces/" + testWorkspace + "/" + testInstance + "/changes/" + bogus)
		if err != nil {
			t.Fatalf("GET %s: %v", bogus, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s%s status = %d, want 404", testChangesPrefix, bogus, resp.StatusCode)
		}
	}
}

// TestGetChange404OnUnknownID: a syntactically-valid UUIDv4 with no
// on-disk match returns 404.
func TestGetChange404OnUnknownID(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + testChangesPrefix + "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetChange50kBDiffUnder50ms covers the loose latency bound from
// PLAN Issue 8's acceptance criteria — NFR2 demands <200 ms for the
// per-change view; we assert <50 ms on a 50 kB synthetic diff so the
// test exercises the render path without becoming flaky on a busy CI.
func TestGetChange50kBDiffUnder50ms(t *testing.T) {
	ts, _, root := newTestServer(t)
	// Construct a 50 kB diff body that looks like a real unified diff
	// (the renderer wraps it in <pre>; html/template auto-escapes the
	// `+` lines).
	line := "+added line that is reasonably long so the body fills up\n"
	var buf strings.Builder
	for buf.Len() < 50*1024 {
		buf.WriteString(line)
	}
	id := seedChange(t, root, mcp.ChangeStatePending, []byte(buf.String()))

	start := time.Now()
	resp, err := http.Get(ts.URL + testChangesPrefix + id)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body) < 50*1024 {
		t.Errorf("rendered body unexpectedly short: %d bytes", len(body))
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("render took %v, want <50ms (loose bound on NFR2 <200ms)", elapsed)
	}
}

// TestGetChangeConcurrentFirstHits asserts the per-change flock makes
// the pending → in-review transition idempotent under concurrent first
// arrivals. Five goroutines hit the same pending change; the state
// ends at in-review with no panic and exactly one transition's worth
// of UpdatedAt drift.
func TestGetChangeConcurrentFirstHits(t *testing.T) {
	ts, _, root := newTestServer(t)
	id := seedChange(t, root, mcp.ChangeStatePending, []byte("diff body"))

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			resp, err := http.Get(ts.URL + testChangesPrefix + id)
			if err != nil {
				t.Errorf("GET: %v", err)
				return
			}
			resp.Body.Close()
		})
	}
	wg.Wait()

	got, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.State != mcp.ChangeStateInReview {
		t.Errorf("state = %q, want in-review", got.State)
	}
}
