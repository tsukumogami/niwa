package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
)

// runGit runs `git args...` inside dir. Fails the test on non-zero exit
// with both the command line and the combined output for diagnostics.
// dir="" runs in the test's current working directory (rare — only the
// initial `git init <target>` style commands use that path).
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Force a stable identity so commit-creating commands don't fail in
	// CI environments where the global git config has no user.email.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// revParseChange resolves ref to a full commit SHA inside dir. Helper for
// asserting the handler's returned base_ref/head_ref against expected
// SHAs. Suffixed `Change` so it doesn't collide with any future helper of
// the same name in the package.
func revParseChange(t *testing.T, dir, ref string) string {
	t.Helper()
	return runGit(t, dir, "rev-parse", "--verify", ref+"^{commit}")
}

// writeTestFile writes content to path with 0o644 permissions. Helper for
// constructing test git repos.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setupRemoteFixture builds a three-level fixture for tests that need a
// real remote-tracking ref (origin/HEAD, origin/main, etc):
//
//	<root>/seed       — seed working repo, initialized on originBranch
//	<root>/origin.git — bare clone (the "remote")
//	<root>/worktree   — clone of the bare; featureBranch checked out
//
// Returns (worktreePath, baseSHA on originBranch, headSHA on featureBranch).
// origin/HEAD is set to refs/remotes/origin/<originBranch> by the clone
// (matches default git behaviour). Tests that exercise R8 chain levels
// past origin/HEAD delete the symref explicitly via
// `git symbolic-ref --delete refs/remotes/origin/HEAD`.
func setupRemoteFixture(t *testing.T, root, baseContent, headContent, originBranch, featureBranch string) (string, string, string) {
	t.Helper()
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "init", "-q", "-b", originBranch, seed)
	writeTestFile(t, filepath.Join(seed, "content.txt"), baseContent)
	runGit(t, seed, "add", "content.txt")
	runGit(t, seed, "commit", "-qm", "base")

	bare := filepath.Join(root, "origin.git")
	runGit(t, "", "clone", "--bare", "-q", seed, bare)
	// Pin the bare repo's HEAD so origin/HEAD survives the clone with a
	// predictable target. Without this the clone may pick the first
	// branch alphabetically, which is fine for happy-path but flaky for
	// precedence tests.
	runGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+originBranch)

	wt := filepath.Join(root, "worktree")
	runGit(t, "", "clone", "-q", "file://"+bare, wt)
	baseSHA := revParseChange(t, wt, originBranch)

	headSHA := baseSHA
	if headContent != "" && headContent != baseContent {
		runGit(t, wt, "checkout", "-qb", featureBranch)
		writeTestFile(t, filepath.Join(wt, "content.txt"), headContent)
		runGit(t, wt, "add", "content.txt")
		runGit(t, wt, "commit", "-qm", "head")
		headSHA = revParseChange(t, wt, "HEAD")
	}
	return wt, baseSHA, headSHA
}

// setupLocalFixture builds a single-repo fixture with no remote, for
// tests that exercise R8 chain levels 4 (main) and 5 (master). The repo
// has baseBranch committed with baseContent, then featureBranch checked
// out with headContent (when featureBranch is non-empty).
func setupLocalFixture(t *testing.T, root, baseContent, headContent, baseBranch, featureBranch string) (string, string, string) {
	t.Helper()
	wt := filepath.Join(root, "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "init", "-q", "-b", baseBranch, wt)
	writeTestFile(t, filepath.Join(wt, "content.txt"), baseContent)
	runGit(t, wt, "add", "content.txt")
	runGit(t, wt, "commit", "-qm", "base")
	baseSHA := revParseChange(t, wt, baseBranch)

	headSHA := baseSHA
	if featureBranch != "" && headContent != baseContent {
		runGit(t, wt, "checkout", "-qb", featureBranch)
		writeTestFile(t, filepath.Join(wt, "content.txt"), headContent)
		runGit(t, wt, "add", "content.txt")
		runGit(t, wt, "commit", "-qm", "head")
		headSHA = revParseChange(t, wt, "HEAD")
	}
	return wt, baseSHA, headSHA
}

// setupChangeSession writes a SessionLifecycleState under
// <root>/.niwa/sessions/<sid>.json pointing at worktree. Companion to
// the fixture helpers above; tests that pass a sid that does not match
// sessionIDRe will hit the invalid_session_id branch before this writes
// anything (verified explicitly in TestHandleCreateChange_InvalidSessionID).
func setupChangeSession(t *testing.T, root, sid, worktree string) {
	t.Helper()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	state := SessionLifecycleState{
		V:            1,
		SessionID:    sid,
		Repo:         "test-repo",
		Purpose:      "change-handler test",
		Status:       SessionStatusActive,
		CreationTime: time.Now().UTC().Format(time.RFC3339),
		WorktreePath: worktree,
		CreatorPID:   os.Getpid(),
	}
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("WriteSessionLifecycleState: %v", err)
	}
}

// newChangeTestServer returns a Server pre-wired to root with a working
// file-backed audit sink (so tests can assert audit-log lines). The
// minimal field set covers everything handlers_change.go touches.
//
// URL composition: the machine-level surface.port lives under
// surfaceConfigDirFn(). The test stubs that function variable to a
// per-test sub-directory of `root` so writes to "the port file" land
// somewhere the test can observe AND the function indirection makes
// it safe to alter from concurrent tests via t.Cleanup. The same
// helper stubs the workspace-instance resolver so URLs in test output
// carry stable workspace+instance placeholders rather than depending
// on the developer's real ~/.config/niwa/config.toml.
func newChangeTestServer(t testing.TB, root string) *Server {
	t.Helper()
	_ = os.MkdirAll(filepath.Join(root, ".niwa"), 0o700)

	// Per-test surface config dir keeps surface.port writes isolated.
	surfaceDir := filepath.Join(root, ".niwa", "surface-test-cfg")
	_ = os.MkdirAll(surfaceDir, 0o700)
	origDirFn := surfaceConfigDirFn
	origLoadFn := loadGlobalConfigFn
	origResolveFn := resolveWorkspaceInstanceFn
	surfaceConfigDirFn = func() (string, error) { return surfaceDir, nil }
	loadGlobalConfigFn = func() (*config.GlobalConfig, error) {
		return &config.GlobalConfig{}, nil
	}
	resolveWorkspaceInstanceFn = func(_ *config.GlobalConfig, _ string) (string, string, error) {
		// Default fallback: unregistered. Tests that want a registered
		// identity rebind this function themselves.
		return "", "", config.ErrInstanceNotUnderWorkspace
	}
	t.Cleanup(func() {
		surfaceConfigDirFn = origDirFn
		loadGlobalConfigFn = origLoadFn
		resolveWorkspaceInstanceFn = origResolveFn
	})

	return &Server{
		instanceRoot: root,
		audit:        NewFileAuditSink(root),
	}
}

// parseCreateResp unmarshals a handleCreateChange success result into a
// typed struct. Fails the test on parse error so callers don't need to
// handle the error inline.
func parseCreateResp(t *testing.T, res toolResult) struct {
	ChangeID string `json:"change_id"`
	State    string `json:"state"`
	URL      string `json:"url"`
	BaseRef  string `json:"base_ref"`
	HeadRef  string `json:"head_ref"`
} {
	t.Helper()
	if res.IsError {
		t.Fatalf("handleCreateChange returned error: %s", res.Content[0].Text)
	}
	var resp struct {
		ChangeID string `json:"change_id"`
		State    string `json:"state"`
		URL      string `json:"url"`
		BaseRef  string `json:"base_ref"`
		HeadRef  string `json:"head_ref"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &resp); err != nil {
		t.Fatalf("parse create response: %v\nraw=%s", err, res.Content[0].Text)
	}
	return resp
}

// TestHandleCreateChange_HappyPath is the canonical end-to-end exercise:
// remote fixture with origin/HEAD set → first niwa_create_change writes
// state.json, diff.patch, and a transitions.log line, returns
// {change_id, state=pending, base_ref, head_ref, url with <port>}.
func TestHandleCreateChange_HappyPath(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, headSHA := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	res := s.handleCreateChange(createChangeArgs{SessionID: sid})
	resp := parseCreateResp(t, res)

	if !uuidV4Regex.MatchString(resp.ChangeID) {
		t.Errorf("change_id %q is not UUIDv4", resp.ChangeID)
	}
	if resp.State != ChangeStatePending {
		t.Errorf("state = %q, want %q", resp.State, ChangeStatePending)
	}
	if !strings.Contains(resp.URL, "<port>") {
		t.Errorf("URL %q missing <port> placeholder (surface.port not yet written)", resp.URL)
	}
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (origin/HEAD)", resp.BaseRef, baseSHA)
	}
	if resp.HeadRef != headSHA {
		t.Errorf("head_ref = %q, want %q", resp.HeadRef, headSHA)
	}

	// diff.patch must exist, be non-empty, and contain the +head line.
	diffPath := filepath.Join(root, ".niwa", "changes", resp.ChangeID, "diff.patch")
	diff, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	if !strings.Contains(string(diff), "+head") {
		t.Errorf("diff.patch missing +head: %q", diff)
	}

	// transitions.log must have exactly one change_ready entry.
	transitions := readTransitionsLog(t, root, resp.ChangeID)
	if got := strings.Count(transitions, `"event":"change_ready"`); got != 1 {
		t.Errorf("transitions.log has %d change_ready entries, want 1: %s", got, transitions)
	}

	// state.json must reflect what the handler wrote.
	st, err := Read(root, resp.ChangeID)
	if err != nil {
		t.Fatalf("Read change state: %v", err)
	}
	if st.State != ChangeStatePending {
		t.Errorf("state.State = %q, want pending", st.State)
	}
	if st.OriginatingSession != sid {
		t.Errorf("originating_session = %q, want %q", st.OriginatingSession, sid)
	}
}

// TestHandleListChanges_HappyPath: after one create, list returns one
// summary with the same id, sorted in updated_at desc order across two
// changes.
func TestHandleListChanges_HappyPath(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	res1 := s.handleCreateChange(createChangeArgs{SessionID: sid})
	resp1 := parseCreateResp(t, res1)

	// Create a second change after another commit (different head_ref so
	// the idempotency key doesn't collapse them).
	time.Sleep(10 * time.Millisecond) // ensure distinct updated_at
	writeTestFile(t, filepath.Join(wt, "content.txt"), "base\nhead\nmore\n")
	runGit(t, wt, "add", "content.txt")
	runGit(t, wt, "commit", "-qm", "another head")
	res2 := s.handleCreateChange(createChangeArgs{SessionID: sid})
	resp2 := parseCreateResp(t, res2)

	if resp1.ChangeID == resp2.ChangeID {
		t.Fatalf("expected two distinct change IDs, got same: %s", resp1.ChangeID)
	}

	listRes := s.handleListChanges(listChangesArgs{})
	if listRes.IsError {
		t.Fatalf("handleListChanges: %s", listRes.Content[0].Text)
	}
	var listResp struct {
		Changes []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(listRes.Content[0].Text), &listResp); err != nil {
		t.Fatalf("parse list: %v\nraw=%s", err, listRes.Content[0].Text)
	}
	if len(listResp.Changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(listResp.Changes))
	}
	// Most recently updated first → resp2.ChangeID at index 0.
	if listResp.Changes[0].ID != resp2.ChangeID {
		t.Errorf("first listed change = %q, want %q (most recent)",
			listResp.Changes[0].ID, resp2.ChangeID)
	}

	// state filter narrows the result set.
	filtered := s.handleListChanges(listChangesArgs{State: ChangeStatePending})
	var fr struct {
		Changes []struct{ ID string } `json:"changes"`
	}
	json.Unmarshal([]byte(filtered.Content[0].Text), &fr)
	if len(fr.Changes) != 2 {
		t.Errorf("filtered (state=pending) got %d, want 2", len(fr.Changes))
	}

	// session_id filter on a non-matching sid produces an empty list.
	other := s.handleListChanges(listChangesArgs{SessionID: "00000000"})
	var or struct {
		Changes []struct{ ID string } `json:"changes"`
	}
	json.Unmarshal([]byte(other.Content[0].Text), &or)
	if len(or.Changes) != 0 {
		t.Errorf("filtered (session_id=00000000) got %d, want 0", len(or.Changes))
	}
}

// TestHandleQueryChange_HappyPath: after one create, query returns the
// full ChangeState plus the change_ready transition entry.
func TestHandleQueryChange_HappyPath(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	createRes := s.handleCreateChange(createChangeArgs{SessionID: sid})
	createResp := parseCreateResp(t, createRes)

	queryRes := s.handleQueryChange(queryChangeArgs{ChangeID: createResp.ChangeID})
	if queryRes.IsError {
		t.Fatalf("handleQueryChange: %s", queryRes.Content[0].Text)
	}
	var qr struct {
		State       ChangeState      `json:"state"`
		Transitions []map[string]any `json:"transitions"`
	}
	if err := json.Unmarshal([]byte(queryRes.Content[0].Text), &qr); err != nil {
		t.Fatalf("parse query: %v\nraw=%s", err, queryRes.Content[0].Text)
	}
	if qr.State.ID != createResp.ChangeID {
		t.Errorf("queried ID = %q, want %q", qr.State.ID, createResp.ChangeID)
	}
	if qr.State.State != ChangeStatePending {
		t.Errorf("queried state = %q, want pending", qr.State.State)
	}
	if len(qr.Transitions) != 1 {
		t.Errorf("got %d transitions, want 1: %+v", len(qr.Transitions), qr.Transitions)
	}
	if event, _ := qr.Transitions[0]["event"].(string); event != ChangeEventReady {
		t.Errorf("transition event = %q, want %q", event, ChangeEventReady)
	}
}

// TestHandleQueryChange_NotFound_BadID: malformed change_id maps to
// not_found before any filesystem access (security: traversal payloads
// like "../foo" never reach os.Open).
func TestHandleQueryChange_NotFound_BadID(t *testing.T) {
	s := newChangeTestServer(t, t.TempDir())
	res := s.handleQueryChange(queryChangeArgs{ChangeID: "../foo"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "not_found" {
		t.Errorf("error code = %q, want not_found", code)
	}
}

// TestHandleQueryChange_NotFound_Cleaned: a cleaned change returns
// not_found per PRD R3 (state.json is preserved for forensics, but
// programmatic consumers should treat cleaned as removed).
func TestHandleQueryChange_NotFound_Cleaned(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)
	createResp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))

	// Flip the change to cleaned state.
	err := UpdateChangeState(root, createResp.ChangeID, func(cur *ChangeState) (*ChangeState, error) {
		next := *cur
		next.State = ChangeStateCleaned
		return &next, nil
	})
	if err != nil {
		t.Fatalf("UpdateChangeState: %v", err)
	}

	res := s.handleQueryChange(queryChangeArgs{ChangeID: createResp.ChangeID})
	if !res.IsError {
		t.Fatalf("expected not_found error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "not_found" {
		t.Errorf("error code = %q, want not_found", code)
	}
}

// TestHandleCreateChange_Idempotent: a re-issued create for the same
// (session_id, head_ref) returns the existing change with
// state="not_modified" and emits no second change_ready event.
func TestHandleCreateChange_Idempotent(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp1 := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	resp2 := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))

	if resp1.ChangeID != resp2.ChangeID {
		t.Fatalf("second create returned different change_id: %s vs %s",
			resp1.ChangeID, resp2.ChangeID)
	}
	if resp2.State != "not_modified" {
		t.Errorf("second create state = %q, want not_modified", resp2.State)
	}

	transitions := readTransitionsLog(t, root, resp1.ChangeID)
	if got := strings.Count(transitions, `"event":"change_ready"`); got != 1 {
		t.Errorf("transitions.log has %d change_ready entries, want 1: %s", got, transitions)
	}
}

// TestHandleCreateChange_Race: N concurrent niwa_create_change for the
// same session_id yield exactly one pending result and N-1 not_modified
// results, with exactly one change_ready event on disk. The per-session
// create lock at .niwa/changes/.session-<sid>.create.lock makes the
// scan-then-reserve-then-write critical section atomic; without that
// lock two callers could each race past the idempotency check and emit
// duplicate events.
func TestHandleCreateChange_Race(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	const N = 4
	type result struct {
		ChangeID string
		State    string
		Err      string
	}
	out := make(chan result, N)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for range N {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			res := s.handleCreateChange(createChangeArgs{SessionID: sid})
			if res.IsError {
				out <- result{Err: res.Content[0].Text}
				return
			}
			var r struct {
				ChangeID string `json:"change_id"`
				State    string `json:"state"`
			}
			json.Unmarshal([]byte(res.Content[0].Text), &r)
			out <- result{ChangeID: r.ChangeID, State: r.State}
		}()
	}
	start.Done()
	done.Wait()
	close(out)

	var firstID string
	var pendingCount, notModifiedCount int
	for r := range out {
		if r.Err != "" {
			t.Fatalf("concurrent create error: %s", r.Err)
		}
		if firstID == "" {
			firstID = r.ChangeID
		} else if r.ChangeID != firstID {
			t.Errorf("got two distinct change_ids: %s vs %s", firstID, r.ChangeID)
		}
		switch r.State {
		case ChangeStatePending:
			pendingCount++
		case "not_modified":
			notModifiedCount++
		default:
			t.Errorf("unexpected state %q", r.State)
		}
	}
	if pendingCount != 1 {
		t.Errorf("got %d pending, want 1", pendingCount)
	}
	if notModifiedCount != N-1 {
		t.Errorf("got %d not_modified, want %d", notModifiedCount, N-1)
	}

	transitions := readTransitionsLog(t, root, firstID)
	if got := strings.Count(transitions, `"event":"change_ready"`); got != 1 {
		t.Errorf("transitions.log has %d change_ready entries, want 1", got)
	}
}

// TestHandleCreateChange_InvalidSessionID: bad session_id maps to
// invalid_session_id before any filesystem access.
func TestHandleCreateChange_InvalidSessionID(t *testing.T) {
	s := newChangeTestServer(t, t.TempDir())
	cases := []string{"", "../foo", "DEADBEEF", "deadbee", "deadbeef1"}
	for _, sid := range cases {
		res := s.handleCreateChange(createChangeArgs{SessionID: sid})
		if !res.IsError {
			t.Errorf("session_id=%q: expected error", sid)
			continue
		}
		if code := errorCode(&res); code != "invalid_session_id" {
			t.Errorf("session_id=%q: error code = %q, want invalid_session_id", sid, code)
		}
	}
}

// TestHandleCreateChange_SessionNotFound: a well-formed session_id with
// no on-disk record produces session_not_found.
func TestHandleCreateChange_SessionNotFound(t *testing.T) {
	s := newChangeTestServer(t, t.TempDir())
	res := s.handleCreateChange(createChangeArgs{SessionID: "deadbeef"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "session_not_found" {
		t.Errorf("error code = %q, want session_not_found", code)
	}
}

// TestHandleCreateChange_WorktreeMissing: session exists but its
// worktree_path is empty or not a git repo.
func TestHandleCreateChange_WorktreeMissing(t *testing.T) {
	root := t.TempDir()
	sid := "deadbeef"
	setupChangeSession(t, root, sid, "/nonexistent/worktree/path")
	s := newChangeTestServer(t, root)
	res := s.handleCreateChange(createChangeArgs{SessionID: sid})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "worktree_missing" {
		t.Errorf("error code = %q, want worktree_missing", code)
	}
}

// TestHandleCreateChange_BaseRefHintUnresolved: a caller-supplied
// base_ref_hint that does not resolve via git rev-parse maps to
// base_ref_hint_unresolved (does NOT fall back to the R8 discovery chain).
func TestHandleCreateChange_BaseRefHintUnresolved(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	res := s.handleCreateChange(createChangeArgs{
		SessionID:   sid,
		BaseRefHint: "refs/heads/nonexistent",
	})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "base_ref_hint_unresolved" {
		t.Errorf("error code = %q, want base_ref_hint_unresolved", code)
	}
}

// TestHandleCreateChange_BaseRef_OriginHEAD: R8 chain level 1.
// The remote fixture with origin/HEAD set (default git clone behaviour)
// resolves via the symbolic-ref path. Verifies the chain's preferred
// level fires when available.
func TestHandleCreateChange_BaseRef_OriginHEAD(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (origin/HEAD)", resp.BaseRef, baseSHA)
	}
}

// TestHandleCreateChange_BaseRef_OriginMain: R8 chain level 2.
// Delete origin/HEAD so the resolver falls through to origin/main.
func TestHandleCreateChange_BaseRef_OriginMain(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	runGit(t, wt, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (origin/main)", resp.BaseRef, baseSHA)
	}
}

// TestHandleCreateChange_BaseRef_OriginMaster: R8 chain level 3.
// origin/master exists; origin/HEAD removed; origin/main does not exist.
func TestHandleCreateChange_BaseRef_OriginMaster(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "master", "feature")
	runGit(t, wt, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (origin/master)", resp.BaseRef, baseSHA)
	}
}

// TestHandleCreateChange_BaseRef_Main: R8 chain level 4.
// Local-only repo with main + feature; no remote at all.
func TestHandleCreateChange_BaseRef_Main(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, _ := setupLocalFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (main)", resp.BaseRef, baseSHA)
	}
}

// TestHandleCreateChange_BaseRef_Master: R8 chain level 5.
// Local-only repo with master + feature.
func TestHandleCreateChange_BaseRef_Master(t *testing.T) {
	root := t.TempDir()
	wt, baseSHA, _ := setupLocalFixture(t, root, "base\n", "base\nhead\n", "master", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.BaseRef != baseSHA {
		t.Errorf("base_ref = %q, want %q (master)", resp.BaseRef, baseSHA)
	}
}

// TestHandleCreateChange_BaseRef_Unresolved: every level of the R8 chain
// fails (no remote and the only branch is named "trunk"). Resolver
// returns the documented error_code.
func TestHandleCreateChange_BaseRef_Unresolved(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupLocalFixture(t, root, "base\n", "base\n", "trunk", "")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	res := s.handleCreateChange(createChangeArgs{SessionID: sid})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.Content[0].Text)
	}
	if code := errorCode(&res); code != "base_ref_unresolved" {
		t.Errorf("error code = %q, want base_ref_unresolved", code)
	}
}

// TestHandleCreateChange_DiffTruncation: a >4 MiB diff has its body
// truncated at the byte boundary with the documented trailer appended.
// Constructs a single file whose head version is 5 MiB of identical
// bytes (each line replaced) so `git diff` emits a large add+remove
// hunk.
func TestHandleCreateChange_DiffTruncation(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "worktree")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "init", "-q", "-b", "main", wt)
	// Base content: empty file. Head content: 5 MiB of 'A' bytes split
	// into 64-char lines so git produces hunk output.
	writeTestFile(t, filepath.Join(wt, "content.txt"), "")
	runGit(t, wt, "add", "content.txt")
	runGit(t, wt, "commit", "-qm", "base")
	runGit(t, wt, "checkout", "-qb", "feature")
	const headSize = 5 * 1024 * 1024
	// 64-byte line (63 alphanumeric chars + '\n') so bytes.Repeat math is
	// exact when we trim to headSize. Slightly over-allocates the repeat
	// count to make the final trim a no-op-and-then-some.
	line := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ABCDEFGHIJKLMNOPQRSTU012345\n")
	if len(line) != 64 {
		t.Fatalf("test bug: line must be 64 bytes, got %d", len(line))
	}
	headBuf := bytes.Repeat(line, headSize/64+1)
	headBuf = headBuf[:headSize]
	writeTestFile(t, filepath.Join(wt, "content.txt"), string(headBuf))
	runGit(t, wt, "add", "content.txt")
	runGit(t, wt, "commit", "-qm", "head")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	diffPath := filepath.Join(root, ".niwa", "changes", resp.ChangeID, "diff.patch")
	diff, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	wantLen := diffSizeCap + len(diffTruncateTrailer)
	if len(diff) != wantLen {
		t.Errorf("truncated diff length = %d, want %d", len(diff), wantLen)
	}
	if !bytes.HasSuffix(diff, []byte(diffTruncateTrailer)) {
		t.Errorf("diff missing truncate trailer; last bytes: %q",
			string(diff[len(diff)-len(diffTruncateTrailer):]))
	}
}

// TestHandleCreateChange_EmptyDiff: when the captured diff is empty
// (HEAD identical to the base ref), the change is still created and
// change_ready still fires. diff.patch is written as an empty file.
func TestHandleCreateChange_EmptyDiff(t *testing.T) {
	root := t.TempDir()
	// setupRemoteFixture with same baseContent / headContent → no
	// second commit, HEAD == origin/main.
	wt, baseSHA, headSHA := setupRemoteFixture(t, root, "base\n", "base\n", "main", "feature")
	if baseSHA != headSHA {
		t.Fatalf("fixture invariant: empty-diff case expects base == head, got %s vs %s", baseSHA, headSHA)
	}
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if resp.State != ChangeStatePending {
		t.Errorf("state = %q, want pending", resp.State)
	}

	diffPath := filepath.Join(root, ".niwa", "changes", resp.ChangeID, "diff.patch")
	diff, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("read diff.patch: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("empty-diff case wrote %d bytes; want 0", len(diff))
	}

	transitions := readTransitionsLog(t, root, resp.ChangeID)
	if !strings.Contains(transitions, `"event":"change_ready"`) {
		t.Errorf("transitions.log missing change_ready: %s", transitions)
	}
}

// TestHandleCreateChange_PayloadTooLargeDowngrade exercises the audit
// path's 2 KB budget on a synthetic large change_ready payload. The
// handler's own payload is bounded, so this test reaches into
// AppendChangeEvent directly with a fabricated 3 KB payload — the same
// emission path the handler uses — and verifies that the audit-log line
// is downgraded (Payload:{}, error_code=payload_too_large) while the
// per-change transitions.log line carries the full payload.
func TestHandleCreateChange_PayloadTooLargeDowngrade(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	// Land a regular change first so we have a change directory to
	// append into.
	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))

	// Emit a synthetic over-budget change_ready event onto the same
	// change. 3 KB payload guarantees the audit downgrade fires.
	largeValue := strings.Repeat("x", 3072)
	err := AppendChangeEvent(root, s.audit, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: resp.ChangeID,
		Payload: map[string]any{
			"change_id": resp.ChangeID,
			"large":     largeValue,
		},
	})
	if err != nil {
		t.Fatalf("AppendChangeEvent (large): %v", err)
	}

	// Audit log: find a change_ready line carrying the downgrade.
	auditPath := filepath.Join(root, ".niwa", "mcp-audit.log")
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var downgraded bool
	for _, line := range strings.Split(strings.TrimRight(string(auditData), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Event != ChangeEventReady {
			continue
		}
		if entry.ErrorCode == "payload_too_large" && len(entry.Payload) == 0 {
			downgraded = true
			break
		}
	}
	if !downgraded {
		t.Errorf("no downgraded change_ready audit entry found in: %s", auditData)
	}

	// transitions.log: full payload survives.
	transitions := readTransitionsLog(t, root, resp.ChangeID)
	if !strings.Contains(transitions, largeValue) {
		t.Errorf("transitions.log lost the full payload (large value missing)")
	}
}

// TestHandleCreateChange_URLPlaceholderSubstitution: when
// .niwa/surface.port is absent, the URL carries the literal <port>
// placeholder. When the port file is later written, the substitution
// reflects the new value on the next read (proves the read is per-call,
// not memoized).
func TestHandleCreateChange_URLPlaceholderSubstitution(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))
	if !strings.Contains(resp.URL, "<port>") {
		t.Fatalf("first create URL = %q, expected <port> placeholder", resp.URL)
	}

	// Write surface.port and verify the next URL composition picks it
	// up. We bypass handleCreateChange here (the same change is now
	// idempotent), but the list handler builds URLs the same way. The
	// machine-level surface.port path comes from surfaceConfigDirFn,
	// which newChangeTestServer stubs to a per-test sub-directory.
	surfaceDir, _ := surfaceConfigDirFn()
	portFile := filepath.Join(surfaceDir, surfacePortFileName)
	if err := os.WriteFile(portFile, []byte("8765"), 0o600); err != nil {
		t.Fatalf("write surface.port: %v", err)
	}
	listRes := s.handleListChanges(listChangesArgs{})
	if !strings.Contains(listRes.Content[0].Text, ":8765/workspaces/") {
		t.Errorf("list URL did not pick up port 8765: %s", listRes.Content[0].Text)
	}
}

// TestHandleCancelChange_AuthorizedBySession confirms a worker holding
// the originating session_id can cancel a pending change it created.
// The pipeline: create the change as session "deadbeef", then attach
// the same session to a fresh Server (simulating a worker re-entering
// or asking from the same session), and call handleCancelChange. Post-
// state must be cleaned with diff.patch removed and a change_cleaned
// event in transitions.log carrying the supplied reason.
func TestHandleCancelChange_AuthorizedBySession(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))

	// Re-stamp the server with the session identity so the authz check
	// in handleCancelChange has a non-empty s.sessionID to match.
	s.sessionID = sid

	cancelRes := s.handleCancelChange(cancelChangeArgs{
		ChangeID: resp.ChangeID,
		Reason:   "worker_decided_not_to_ship",
	})
	if cancelRes.IsError {
		t.Fatalf("handleCancelChange unexpected error: %s", cancelRes.Content[0].Text)
	}
	got, err := Read(root, resp.ChangeID)
	if err != nil {
		t.Fatalf("Read after cancel: %v", err)
	}
	if got.State != ChangeStateCleaned {
		t.Errorf("state = %q, want cleaned", got.State)
	}
	dir, _ := ChangeDir(root, resp.ChangeID)
	if _, err := os.Stat(filepath.Join(dir, diffPatchFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("diff.patch not removed: %v", err)
	}
	transitions := readTransitionsLog(t, root, resp.ChangeID)
	if !strings.Contains(transitions, "change_cleaned") {
		t.Errorf("transitions.log missing change_cleaned: %s", transitions)
	}
	if !strings.Contains(transitions, "worker_decided_not_to_ship") {
		t.Errorf("transitions.log missing reason payload: %s", transitions)
	}
}

// TestHandleCancelChange_UnauthorizedSession confirms a server without
// the originating session_id (or originating task_id) cannot cancel
// the change. The handler returns the "forbidden" error code and the
// change state remains unchanged.
func TestHandleCancelChange_UnauthorizedSession(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sid := "deadbeef"
	setupChangeSession(t, root, sid, wt)
	s := newChangeTestServer(t, root)

	resp := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sid}))

	// Leave s.sessionID and s.taskID empty: simulating a coordinator
	// attempting direct cancellation (rejected — coordinators must go
	// through the auto-cascade from task abandonment or session
	// destruction).
	cancelRes := s.handleCancelChange(cancelChangeArgs{ChangeID: resp.ChangeID})
	if !cancelRes.IsError {
		t.Fatalf("expected unauthorized cancel to error, got: %s", cancelRes.Content[0].Text)
	}
	if !strings.Contains(cancelRes.Content[0].Text, "forbidden") {
		t.Errorf("error text missing 'forbidden': %s", cancelRes.Content[0].Text)
	}
	// State unchanged.
	got, err := Read(root, resp.ChangeID)
	if err != nil {
		t.Fatalf("Read after rejected cancel: %v", err)
	}
	if got.State != ChangeStatePending {
		t.Errorf("state = %q after rejected cancel, want pending", got.State)
	}
}

// TestCancelChangesForSession confirms the session-destruction
// auto-cascade reaps every non-cleaned change originating from the
// destroyed session. Changes from a different session are left alone.
func TestCancelChangesForSession(t *testing.T) {
	root := t.TempDir()
	wt, _, _ := setupRemoteFixture(t, root, "base\n", "base\nhead\n", "main", "feature")
	sidA := "deadbeef"
	sidB := "cafef00d"
	setupChangeSession(t, root, sidA, wt)
	setupChangeSession(t, root, sidB, wt)
	s := newChangeTestServer(t, root)

	respA := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sidA}))
	// Advance HEAD with another commit so sidB's create gets its own
	// (session_id, head_ref) tuple and a distinct change.
	writeTestFile(t, filepath.Join(wt, "content.txt"), "base\nhead\nmore\n")
	runGit(t, wt, "add", "content.txt")
	runGit(t, wt, "commit", "-qm", "more")
	respB := parseCreateResp(t, s.handleCreateChange(createChangeArgs{SessionID: sidB}))

	CancelChangesForSession(root, sidA, "destroy_cascade", s.audit)

	gotA, _ := Read(root, respA.ChangeID)
	if gotA == nil || gotA.State != ChangeStateCleaned {
		t.Errorf("session A change not cleaned: %+v", gotA)
	}
	gotB, _ := Read(root, respB.ChangeID)
	if gotB == nil || gotB.State != ChangeStatePending {
		t.Errorf("session B change disturbed by A's cascade: %+v", gotB)
	}
}

// readTransitionsLog is a small test helper that returns the full
// transitions.log content for a change, failing the test on read error.
func readTransitionsLog(t *testing.T, root, changeID string) string {
	t.Helper()
	path := filepath.Join(root, ".niwa", "changes", changeID, transitionsLogFileName)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read transitions.log: %v", err)
	}
	return string(data)
}
