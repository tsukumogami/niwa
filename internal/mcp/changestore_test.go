package mcp

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// helper to round-trip a freshly reserved change. Returns the change ID
// and the change directory path for follow-up assertions.
func reserveAndWriteInitial(t *testing.T, root string) (string, string) {
	t.Helper()
	id, err := ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	st := ChangeState{
		V:                  1,
		ID:                 id,
		State:              ChangeStatePending,
		OriginatingSession: "sess0001",
		OriginatingTasks:   []string{},
		CreatedAt:          now,
		UpdatedAt:          now,
		BaseRef:            "main",
		HeadRef:            "feature/x",
		Branch:             "feature/x",
		WorktreePath:       "/tmp/worktree",
		DiffPath:           "diff.patch",
		Metadata:           map[string]any{},
	}
	if err := WriteInitial(root, st); err != nil {
		t.Fatalf("WriteInitial: %v", err)
	}
	dir := filepath.Join(root, ".niwa", changesDirName, id)
	return id, dir
}

func TestReserveChangeID_CreatesDirAndLock(t *testing.T) {
	root := t.TempDir()
	id, err := ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	if !uuidV4Regex.MatchString(id) {
		t.Errorf("id %q does not match UUIDv4 regex", id)
	}
	dir := filepath.Join(root, ".niwa", changesDirName, id)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("change dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("change path is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0o700", info.Mode().Perm())
	}
	lockInfo, err := os.Stat(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Errorf("lock mode = %v, want 0o600", lockInfo.Mode().Perm())
	}
}

func TestReserveAndReadRoundtrip(t *testing.T) {
	root := t.TempDir()
	id, _ := reserveAndWriteInitial(t, root)
	got, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != id {
		t.Errorf("got.ID = %q, want %q", got.ID, id)
	}
	if got.State != ChangeStatePending {
		t.Errorf("got.State = %q, want %q", got.State, ChangeStatePending)
	}
	if got.BaseRef != "main" || got.HeadRef != "feature/x" {
		t.Errorf("base/head mismatch: %q / %q", got.BaseRef, got.HeadRef)
	}
	if got.Verdict != nil {
		t.Errorf("Verdict should be nil at F5, got %v", got.Verdict)
	}
}

func TestRead_UnknownStateRejected(t *testing.T) {
	root := t.TempDir()
	id, dir := reserveAndWriteInitial(t, root)
	// Corrupt state.json with an unknown state value.
	bad := []byte(`{
  "v": 1,
  "id": "` + id + `",
  "state": "totally-bogus",
  "originating_session": "",
  "originating_tasks": [],
  "created_at": "2026-05-12T00:00:00Z",
  "updated_at": "2026-05-12T00:00:00Z",
  "base_ref": "main",
  "head_ref": "feature/x",
  "branch": "feature/x",
  "worktree_path": "/tmp/wt",
  "diff_path": "diff.patch",
  "verdict": null,
  "metadata": {}
}`)
	if err := os.WriteFile(filepath.Join(dir, changeStateFileName), bad, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Read(root, id)
	if !errors.Is(err, ErrCorruptedState) {
		t.Errorf("Read with unknown state: err = %v, want ErrCorruptedState", err)
	}
}

func TestRead_WrongSchemaVersionRejected(t *testing.T) {
	root := t.TempDir()
	id, dir := reserveAndWriteInitial(t, root)
	bad := []byte(`{
  "v": 99,
  "id": "` + id + `",
  "state": "pending",
  "originating_session": "",
  "originating_tasks": [],
  "created_at": "2026-05-12T00:00:00Z",
  "updated_at": "2026-05-12T00:00:00Z",
  "base_ref": "main",
  "head_ref": "feature/x",
  "branch": "feature/x",
  "worktree_path": "/tmp/wt",
  "diff_path": "diff.patch",
  "verdict": null,
  "metadata": {}
}`)
	if err := os.WriteFile(filepath.Join(dir, changeStateFileName), bad, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Read(root, id)
	if !errors.Is(err, ErrCorruptedState) {
		t.Errorf("Read with v=99: err = %v, want ErrCorruptedState", err)
	}
}

func TestRead_PathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	// Try path-traversal payloads. Every form must fail before any
	// filesystem call lands.
	for _, evil := range []string{
		"../etc",
		"../../passwd",
		"..",
		"./foo",
		"/etc/passwd",
		"a/b",
		"not-a-uuid",
		"00000000-0000-0000-0000-000000000000", // valid UUID layout but version != 4
	} {
		_, err := Read(root, evil)
		if err == nil {
			t.Errorf("Read(%q): err = nil, want UUIDv4 rejection", evil)
		}
	}
}

func TestReserveChangeID_ConcurrentDistinctIDs(t *testing.T) {
	root := t.TempDir()
	const n = 32
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			id, err := ReserveChangeID(root)
			ids[i] = id
			errs[i] = err
		}(i)
	}
	wg.Wait()
	seen := make(map[string]bool, n)
	for i := range n {
		if errs[i] != nil {
			t.Errorf("goroutine %d: %v", i, errs[i])
			continue
		}
		if seen[ids[i]] {
			t.Errorf("duplicate ID across goroutines: %q", ids[i])
		}
		seen[ids[i]] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct IDs, want %d", len(seen), n)
	}
}

// TestUpdateState_Sequenced runs two goroutines mutating the same change
// under the per-change flock and verifies neither mutation is lost.
// Both increment a counter in Metadata; if the flock works, the final
// counter is exactly 2.
func TestUpdateChangeState_Sequenced(t *testing.T) {
	root := t.TempDir()
	id, _ := reserveAndWriteInitial(t, root)

	restore := setLockTimeoutForTest(2 * time.Second)
	defer restore()

	bump := func(cur *ChangeState) (*ChangeState, error) {
		// Read-modify-write of a Metadata counter; under the flock the
		// two goroutines must serialize here.
		next := *cur
		// Allocate a fresh map so we don't share the underlying storage
		// with the read result.
		next.Metadata = map[string]any{}
		maps.Copy(next.Metadata, cur.Metadata)
		c, _ := next.Metadata["counter"].(float64)
		next.Metadata["counter"] = c + 1
		return &next, nil
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var err1, err2 error
	go func() {
		defer wg.Done()
		err1 = UpdateChangeState(root, id, bump)
	}()
	go func() {
		defer wg.Done()
		err2 = UpdateChangeState(root, id, bump)
	}()
	wg.Wait()
	if err1 != nil || err2 != nil {
		t.Fatalf("UpdateChangeState errors: %v / %v", err1, err2)
	}
	got, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read after concurrent updates: %v", err)
	}
	c, _ := got.Metadata["counter"].(float64)
	if c != 2 {
		t.Errorf("counter = %v, want 2 (concurrent mutations lost a write)", c)
	}
}

func TestUpdateChangeState_MutatorSkipsWriteOnNil(t *testing.T) {
	root := t.TempDir()
	id, _ := reserveAndWriteInitial(t, root)
	before, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read before: %v", err)
	}
	beforeUpdated := before.UpdatedAt

	// Wait a tick so a write would visibly bump UpdatedAt.
	time.Sleep(2 * time.Millisecond)

	noop := func(_ *ChangeState) (*ChangeState, error) { return nil, nil }
	if err := UpdateChangeState(root, id, noop); err != nil {
		t.Fatalf("UpdateChangeState noop: %v", err)
	}
	after, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read after: %v", err)
	}
	if after.UpdatedAt != beforeUpdated {
		t.Errorf("UpdatedAt changed on nil-mutator path: %q → %q", beforeUpdated, after.UpdatedAt)
	}
}

func TestUpdateChangeState_AdvancesUpdatedAt(t *testing.T) {
	root := t.TempDir()
	id, _ := reserveAndWriteInitial(t, root)
	before, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read before: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	advance := func(cur *ChangeState) (*ChangeState, error) {
		next := *cur
		next.State = ChangeStateInReview
		return &next, nil
	}
	if err := UpdateChangeState(root, id, advance); err != nil {
		t.Fatalf("UpdateChangeState: %v", err)
	}
	after, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read after: %v", err)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Errorf("UpdatedAt did not advance: still %q", after.UpdatedAt)
	}
	if after.State != ChangeStateInReview {
		t.Errorf("State = %q, want %q", after.State, ChangeStateInReview)
	}
}

func TestWriteInitial_RejectsInvalidState(t *testing.T) {
	root := t.TempDir()
	id, err := ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	st := ChangeState{
		V:                  1,
		ID:                 id,
		State:              "completely-made-up",
		OriginatingSession: "",
		OriginatingTasks:   []string{},
		CreatedAt:          "2026-05-12T00:00:00Z",
		UpdatedAt:          "2026-05-12T00:00:00Z",
		Metadata:           map[string]any{},
	}
	err = WriteInitial(root, st)
	if !errors.Is(err, ErrCorruptedState) {
		t.Errorf("WriteInitial with bogus state: err = %v, want ErrCorruptedState", err)
	}
}

// TestChangesDir_HelperReturnsExpectedPath documents the placement
// convention so future readers don't recompute it from scratch.
func TestChangesDir_HelperReturnsExpectedPath(t *testing.T) {
	got := ChangesDir("/tmp/inst")
	want := filepath.Join("/tmp/inst", ".niwa", "changes")
	if got != want {
		t.Errorf("ChangesDir = %q, want %q", got, want)
	}
}
