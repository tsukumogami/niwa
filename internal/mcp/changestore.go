// changestore.go is the on-disk substrate for the F5 change primitive. One
// directory per change at `.niwa/changes/<id>/` carries:
//
//	state.json        — ChangeState v=1 (this file owns writes)
//	diff.patch        — captured by handlers_change.go (owns those writes)
//	transitions.log   — per-change NDJSON event log (changelog.go appends)
//	.lock             — the per-change flock target
//
// Mirrors `taskstore.go` byte-for-byte where the discipline transfers:
// O_NOFOLLOW on every open, 30 s bounded flock, atomic tmp+rename with
// parent-dir fsync, mode 0o600 for files and 0o700 for the change
// directory. The DESIGN doc's "Cross-cutting interfaces" section calls out
// this mirror explicitly so future readers do not invent a new
// write-order.
//
// The reservation primitive differs from the session-lifecycle case: a
// change occupies a directory, not a single file, so atomicity comes from
// `os.Mkdir` (kernel-atomic, returns EEXIST on collision) rather than
// `O_CREATE|O_EXCL` on a placeholder file. The retry/birthday-loop shape
// matches ReserveID (atomicid.go); see reserveChangeIDLoop's doc comment.

package mcp

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ChangeState is the on-disk schema for `.niwa/changes/<id>/state.json`.
// Field order and JSON tags match PRD R1 verbatim. The schema is v=1 at
// F5; F10 will compose verdict-cast mutations without bumping v (the
// `verdict` pointer carries the schema extension).
//
// Privacy: this file never carries LLM-supplied tool argument values. The
// Metadata map is reserved for structured fields the change creator owns
// (e.g. originating-task summary keys, never bodies).
type ChangeState struct {
	V                   int            `json:"v"`
	ID                  string         `json:"id"`
	State               string         `json:"state"`
	OriginatingSessions []string       `json:"originating_sessions"`
	OriginatingTasks    []string       `json:"originating_tasks"`
	CreatedAt           string         `json:"created_at"`
	UpdatedAt           string         `json:"updated_at"`
	BaseRef             string         `json:"base_ref"`
	HeadRef             string         `json:"head_ref"`
	Branch              string         `json:"branch"`
	WorktreePath        string         `json:"worktree_path"`
	DiffPath            string         `json:"diff_path"`
	// Verdict is a typed nil at F5 (always serializes as `null`); F10
	// populates fields on the same pointer. Using a pointer-to-struct
	// instead of `any` lets the compiler check the verdict shape when
	// F10 lands.
	Verdict  *Verdict       `json:"verdict"`
	Metadata map[string]any `json:"metadata"`
}

// Verdict is reserved for F10 — at F5 it serializes as a JSON `null`. The
// empty struct keeps the on-disk shape stable: today `"verdict": null`,
// tomorrow `"verdict": {…}` with no schema bump required.
type Verdict struct{}

// Change states tracked in ChangeState.State. The F5 transition graph is
// pending → in-review → cleaned (GC) or pending → cleaned (GC).
// verdict-cast is reserved for F10 and never written by F5 code.
const (
	ChangeStatePending     = "pending"
	ChangeStateInReview    = "in-review"
	ChangeStateVerdictCast = "verdict-cast"
	ChangeStateCleaned     = "cleaned"
)

// validChangeStates is the read-time enum check. Unknown states from a
// future writer's schema bump are rejected as corruption rather than
// silently accepted; cross-version compatibility is bought via explicit
// `v` bumps, not enum laxity.
var validChangeStates = map[string]bool{
	ChangeStatePending:     true,
	ChangeStateInReview:    true,
	ChangeStateVerdictCast: true,
	ChangeStateCleaned:     true,
}

// changesDirName is the directory under `<instanceRoot>/.niwa/` that
// holds one subdirectory per change. Centralized here so callers never
// hard-code the path.
const changesDirName = "changes"

// changeStateFileName is the per-change state file. Distinct from
// taskstore's stateFileName at the constant level even though both
// resolve to "state.json"; the duplicated constant prevents an editor
// rename in taskstore.go from silently retargeting changes.
const changeStateFileName = "state.json"

// ChangesDir returns `<instanceRoot>/.niwa/changes`. Callers that need
// the raw path (handlers, GC sweep) use this helper rather than
// recomputing the join.
func ChangesDir(instanceRoot string) string {
	return filepath.Join(instanceRoot, ".niwa", changesDirName)
}

// ChangeDir returns `<instanceRoot>/.niwa/changes/<id>`. The id is
// validated against the UUIDv4 regex before any filesystem call so a
// caller-supplied `../foo` never lands on disk.
func ChangeDir(instanceRoot, id string) (string, error) {
	if !uuidV4Regex.MatchString(id) {
		return "", fmt.Errorf("invalid change ID %q: must be UUIDv4", id)
	}
	return filepath.Join(ChangesDir(instanceRoot), id), nil
}

// uuidV4Generator returns a random UUIDv4 string. Sourced from
// crypto/rand to make change IDs unpredictable; the same-UID symlink
// attacker cannot pre-seed `.niwa/changes/<predicted>/` before the
// reservation lands. Layout: xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx.
func uuidV4Generator() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ReserveChangeID atomically reserves a fresh change ID and creates the
// `<id>/` directory + `.lock` placeholder under
// `<instanceRoot>/.niwa/changes/`. Returns the reserved ID on success.
//
// TOCTOU safety: the atomic primitive here is `os.Mkdir`. On Linux the
// kernel makes mkdir's existence-check-and-create atomic, so two
// concurrent callers cannot reserve the same `<id>/`. The retry loop
// shape mirrors ReserveID (atomicid.go); the divergence is that
// ReserveID uses `O_CREATE|O_EXCL` on a placeholder file, while changes
// need to claim a whole directory (the design's "atomic
// `.niwa/changes/<id>/` directory reservation"). Five attempts cover any
// realistic UUIDv4 collision (birthday probability ~10^-29 per attempt).
//
// Side effects: creates `<instanceRoot>/.niwa/changes/` if absent,
// `<id>/` with mode 0o700, and `<id>/.lock` with mode 0o600. The
// `.lock` file is zero-byte and exists solely as the flock target; it
// is never unlinked except by GC cleanup of the entire change directory.
func ReserveChangeID(instanceRoot string) (string, error) {
	changesDir := ChangesDir(instanceRoot)
	if err := os.MkdirAll(changesDir, 0o700); err != nil {
		return "", fmt.Errorf("create changes dir: %w", err)
	}
	for range 5 {
		id, err := uuidV4Generator()
		if err != nil {
			return "", fmt.Errorf("generating change ID: %w", err)
		}
		dir := filepath.Join(changesDir, id)
		err = os.Mkdir(dir, 0o700)
		if err == nil {
			lockPath := filepath.Join(dir, lockFileName)
			lf, lerr := os.OpenFile(lockPath,
				os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
			if lerr != nil {
				// Roll back the directory so a retry can re-mkdir cleanly.
				// This path is unreachable in practice (we just created an
				// empty dir) but handles the case where a same-UID attacker
				// pre-seeded the lock file between mkdir and open.
				_ = os.Remove(dir)
				return "", fmt.Errorf("create lock %s: %w", lockPath, lerr)
			}
			_ = lf.Close()
			return id, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("reserving change ID: %w", err)
		}
		// Collision: retry with a fresh UUID.
	}
	return "", fmt.Errorf("failed to generate unique change ID after 5 attempts")
}

// openChangeLock opens `<changeDir>/.lock` with O_NOFOLLOW. The lock file
// is created on demand if absent — callers that bypass ReserveChangeID
// (e.g. tests, manual recovery) still get a working lock target.
// Mirrors OpenTaskLock's contract for the change directory.
func openChangeLock(changeDir string) (*os.File, error) {
	path := filepath.Join(changeDir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// WriteInitial atomically persists the first state.json for a freshly-
// reserved change. Assumes the caller already holds the per-change
// flock (e.g. just after ReserveChangeID, which leaves the directory
// uncontested). The mutator path in UpdateState handles all subsequent
// writes; WriteInitial exists separately because the initial write has
// no prior state to read or validate.
//
// state.ID must match the directory name (UUIDv4 regex enforced). The
// caller fills CreatedAt/UpdatedAt — WriteInitial does not generate
// timestamps because the change creator may want to align them with the
// `change_ready` event's `at` field.
func WriteInitial(instanceRoot string, state ChangeState) error {
	if err := validateChangeState(&state); err != nil {
		return err
	}
	dir, err := ChangeDir(instanceRoot, state.ID)
	if err != nil {
		return err
	}
	return writeChangeStateAtomic(dir, &state)
}

// Read returns the parsed ChangeState for id under a shared flock on
// the per-change `.lock`. UUIDv4 validation happens before any
// filesystem call so a caller-supplied `"../foo"` never reaches `os.Open`.
//
// Returns ErrCorruptedState on schema violations (wrong v, unknown
// state, malformed id). Returns the wrapped os error for not-found / IO
// problems.
func Read(instanceRoot, id string) (*ChangeState, error) {
	dir, err := ChangeDir(instanceRoot, id)
	if err != nil {
		return nil, err
	}
	lf, err := openChangeLock(dir)
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	if err := acquireFlock(lf, false); err != nil {
		return nil, err
	}
	defer func() { _ = releaseFlock(lf) }()
	return readChangeStateLocked(dir)
}

// ChangeMutator receives the current ChangeState and returns the new
// ChangeState the caller wants persisted. Returning a nil pointer skips
// the write (used for idempotent no-ops, e.g. GET /changes/<id>
// re-arriving on an already-in-review change). Returning an error
// aborts the transaction without writing anything.
type ChangeMutator func(cur *ChangeState) (*ChangeState, error)

// UpdateState atomically applies mutator to `state.json` under the
// per-change exclusive flock. Critical section:
//
//	flock(.lock, LOCK_EX)
//	  read state.json  (O_NOFOLLOW)
//	  validate (v==1, state in enum, UUIDv4 id)
//	  mutator(cur) → next  (nil ⇒ skip write)
//	  bump UpdatedAt to time.Now().UTC()
//	  write state.json.tmp  (O_NOFOLLOW, O_CREATE, 0600)  — fsync
//	  rename state.json.tmp → state.json
//	  fsync parent directory
//	unlock
//
// transitions.log appends are NOT performed here — changelog.go owns
// that side and reuses the same flock target. This separation lets the
// GC sweep (which writes state.json without a transitions.log entry on
// pending → cleaned, plus a follow-up `change_cleaned` event) compose
// the two helpers without invoking a higher-level orchestration layer.
func UpdateChangeState(instanceRoot, id string, mutator ChangeMutator) error {
	dir, err := ChangeDir(instanceRoot, id)
	if err != nil {
		return err
	}
	lf, err := openChangeLock(dir)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := acquireFlock(lf, true); err != nil {
		return err
	}
	defer func() { _ = releaseFlock(lf) }()

	cur, err := readChangeStateLocked(dir)
	if err != nil {
		return err
	}
	next, err := mutator(cur)
	if err != nil {
		return err
	}
	if next == nil {
		return nil
	}
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := validateChangeState(next); err != nil {
		return ErrCorruptedState
	}
	return writeChangeStateAtomic(dir, next)
}

// readChangeStateLocked reads and validates `<dir>/state.json`. Caller
// must already hold the flock (shared or exclusive). Mirrors
// readStateLocked in taskstore.go.
func readChangeStateLocked(dir string) (*ChangeState, error) {
	path := filepath.Join(dir, changeStateFileName)
	data, err := readFileNoFollow(path)
	if err != nil {
		return nil, err
	}
	var st ChangeState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, ErrCorruptedState
	}
	if err := validateChangeState(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// validateChangeState enforces schema invariants on every read and
// every mutator return. Returns ErrCorruptedState on any failure so
// callers can distinguish schema problems from IO problems.
func validateChangeState(st *ChangeState) error {
	if st.V != 1 {
		return ErrCorruptedState
	}
	if !validChangeStates[st.State] {
		return ErrCorruptedState
	}
	if !uuidV4Regex.MatchString(st.ID) {
		return ErrCorruptedState
	}
	return nil
}

// writeChangeStateAtomic writes state.json.tmp, fsyncs it, renames to
// state.json, and fsyncs the parent directory. Assumes the caller holds
// the exclusive flock. Mirrors writeStateAtomic in taskstore.go; the
// duplicated body avoids cross-coupling the two stores' filename
// constants.
func writeChangeStateAtomic(dir string, st *ChangeState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal change state: %w", err)
	}
	tmpPath := filepath.Join(dir, changeStateFileName+".tmp")
	f, err := os.OpenFile(tmpPath,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmpPath, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	dstPath := filepath.Join(dir, changeStateFileName)
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, dstPath, err)
	}
	if err := fsyncDir(dir); err != nil {
		return fmt.Errorf("fsync parent %s: %w", dir, err)
	}
	return nil
}

