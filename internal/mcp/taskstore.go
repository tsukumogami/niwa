// Task storage primitives — per-task flock coordination, atomic state.json
// rewrites, and NDJSON transitions.log appends. All task-directory mutations
// funnel through this file so the flock scope, open flags, and fsync ordering
// stay consistent across the daemon, MCP tool handlers, and the CLI.
//
// DESIGN Decision 1 specifies the exact write-order this module implements:
//
//	flock(.lock, LOCK_EX)
//	  read state.json  (O_NOFOLLOW)
//	  validate (v==1, state in enum, UUID-shaped task_id)
//	  mutate (mutator returns new state + log entry)
//	  write state.json.tmp  (O_NOFOLLOW, O_CREATE, 0600)  — fsync
//	  rename state.json.tmp → state.json
//	  fsync parent directory
//	  append line to transitions.log (O_APPEND|O_NOFOLLOW, 0600) — fsync
//	unlock
//
// This is the only place in niwa that writes state.json or transitions.log.
// New callers add behavior through mutator functions rather than rebuilding
// the critical section.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// Task-store error values surfaced to callers. Every caller (daemon, MCP
// handler, CLI) decides how to map these onto tool-layer error codes; the
// storage layer itself does not know about PRD R50.
var (
	// ErrStateMismatch — mutator returned a "from" state that did not match
	// the on-disk state. Callers typically retry or reclassify.
	ErrStateMismatch = errors.New("taskstore: state mismatch")
	// ErrAlreadyTerminal — the on-disk state is already terminal; no
	// mutation is allowed. Maps to TASK_ALREADY_TERMINAL at the tool layer.
	ErrAlreadyTerminal = errors.New("taskstore: task already terminal")
	// ErrCorruptedState — state.json failed schema validation (wrong v,
	// unknown state, malformed task_id, unparseable JSON). Fail closed.
	ErrCorruptedState = errors.New("taskstore: corrupted state.json")
	// ErrLockTimeout — 30-second bounded flock acquisition expired. Callers
	// treat this as retryable; see lockTimeout below.
	ErrLockTimeout = errors.New("taskstore: flock acquisition timed out")
)

// lockTimeout bounds every flock acquisition to 30 seconds. The retry loop
// wakes every 20 ms and stops either when the lock is acquired or when the
// deadline passes. 30 s is long enough that a legitimate writer's critical
// section (state.json rewrite + short log append) never pushes a reader past
// the timeout, while short enough that a wedged holder surfaces as
// ErrLockTimeout instead of deadlocking the caller.
const lockTimeout = 30 * time.Second

// lockPollInterval is the retry interval inside acquireFlock. 20 ms is a
// pragmatic balance: short enough that most contention-free lock hand-offs
// complete within a single poll cycle, long enough to avoid busy-looping.
const lockPollInterval = 20 * time.Millisecond

// uuidV4Regex validates the task_id format on every read. UUIDv4 layout:
// xxxxxxxx-xxxx-4xxx-[89ab]xxx-xxxxxxxxxxxx (case-insensitive). crypto/rand
// origin is enforced by NewTaskID; this regex catches manual edits and the
// all-zero placeholder the daemon uses before backfill.
var uuidV4Regex = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// Task-directory filenames. Centralized here so callers never hard-code the
// path and the O_NOFOLLOW discipline always applies.
const (
	stateFileName       = "state.json"
	envelopeFileName    = "envelope.json"
	lockFileName        = ".lock"
	transitionsFileName = "transitions.log"
)

// NewTaskID returns a fresh UUIDv4 sourced from crypto/rand. Task IDs must be
// unpredictable so a same-UID attacker cannot pre-seed `.niwa/tasks/<id>/`
// before the daemon creates it (DESIGN Key Interfaces).
func NewTaskID() string { return newUUID() }

// OpenTaskLock opens `<taskDir>/.lock` with O_NOFOLLOW, creating it with mode
// 0600 if needed. Callers flock this descriptor for the duration of their
// critical section and close it on completion.
//
// The lock target is a dedicated zero-byte file, not state.json. Holding a
// lock on a file you are about to atomically rename produces a stale fd after
// the rename (DESIGN Decision 1 alternative "flock on state.json itself"
// rejected for this reason).
func OpenTaskLock(taskDir string) (*os.File, error) {
	path := filepath.Join(taskDir, lockFileName)
	// O_NOFOLLOW fails closed if an attacker symlinks .lock to some other
	// file under the same UID. O_CREATE keeps first-time setup simple.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// acquireFlock acquires a bounded-timeout flock (shared or exclusive) on lf.
// Exclusive=true → LOCK_EX; false → LOCK_SH. Returns ErrLockTimeout after
// lockTimeout has elapsed without success.
//
// The non-blocking LOCK_NB loop is preferred over a blocking LOCK_EX call
// because Go's runtime would otherwise have no way to surface the timeout —
// syscall.Flock with LOCK_EX blocks indefinitely in the kernel.
func acquireFlock(lf *os.File, exclusive bool) error {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	how |= syscall.LOCK_NB

	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(lf.Fd()), how)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("flock: %w", err)
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		time.Sleep(lockPollInterval)
	}
}

// releaseFlock unlocks lf. Errors are returned; callers typically log and
// continue because releasing always happens in a defer path.
func releaseFlock(lf *os.File) error {
	return syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
}

// ReadState acquires a shared flock on `<taskDir>/.lock`, reads envelope.json
// and state.json, validates them both, and returns the parsed values.
//
// Validation is schema-only (v==1, state in enum, task_id UUIDv4-shaped); it
// does not cross-check envelope fields because niwa_update_task may mutate
// envelope.body independently of state.json.
//
// Returns ErrCorruptedState on any JSON or schema failure. Auth helpers map
// this onto a fail-closed NOT_TASK_PARTY at the tool layer.
func ReadState(taskDir string) (*TaskEnvelope, *TaskState, error) {
	lf, err := OpenTaskLock(taskDir)
	if err != nil {
		return nil, nil, err
	}
	defer lf.Close()

	if err := acquireFlock(lf, false); err != nil {
		return nil, nil, err
	}
	defer func() { _ = releaseFlock(lf) }()

	env, err := readEnvelope(taskDir)
	if err != nil {
		return nil, nil, err
	}
	st, err := readStateLocked(taskDir)
	if err != nil {
		return nil, nil, err
	}
	return env, st, nil
}

// readEnvelope reads `<taskDir>/envelope.json` under the caller's flock.
// The O_NOFOLLOW open flag protects against a symlink attacker targeting a
// different file via the known pathname (see DESIGN Threat Model).
func readEnvelope(taskDir string) (*TaskEnvelope, error) {
	path := filepath.Join(taskDir, envelopeFileName)
	data, err := readFileNoFollow(path)
	if err != nil {
		return nil, err
	}
	var env TaskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, ErrCorruptedState
	}
	if env.V != 1 || !uuidV4Regex.MatchString(env.ID) {
		return nil, ErrCorruptedState
	}
	return &env, nil
}

// readStateLocked reads and validates `<taskDir>/state.json`. Caller must
// already hold the flock (shared or exclusive).
func readStateLocked(taskDir string) (*TaskState, error) {
	path := filepath.Join(taskDir, stateFileName)
	data, err := readFileNoFollow(path)
	if err != nil {
		return nil, err
	}
	var st TaskState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, ErrCorruptedState
	}
	if err := validateState(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// validateState enforces schema invariants on every read: v==1, state in the
// enum, task_id UUIDv4-shaped. Returns ErrCorruptedState on any failure.
// max_restarts == 0 is accepted (legacy path where the daemon has not yet
// backfilled the field).
func validateState(st *TaskState) error {
	if st.V != 1 {
		return ErrCorruptedState
	}
	if !validTaskStates[st.State] {
		return ErrCorruptedState
	}
	if !uuidV4Regex.MatchString(st.TaskID) {
		return ErrCorruptedState
	}
	return nil
}

// Mutator receives the current TaskState and returns the new TaskState plus
// an optional transitions.log entry. Returning a nil entry skips the log
// append (used for idempotent no-op updates or special admin paths).
//
// Returning an error from the mutator aborts the transaction without writing
// anything. The error is surfaced to the caller verbatim.
type Mutator func(cur *TaskState) (*TaskState, *TransitionLogEntry, error)

// UpdateState atomically applies mutator to `<taskDir>/state.json` and
// optionally appends an entry to `<taskDir>/transitions.log`. The full
// critical section runs under exclusive flock on `.lock`:
//
//  1. Acquire exclusive flock (30-second bounded timeout).
//  2. Read + validate state.json.
//  3. Reject if the on-disk state is terminal (ErrAlreadyTerminal).
//  4. Call mutator to produce new state + optional log entry.
//  5. Write state.json.tmp with O_NOFOLLOW; fsync.
//  6. atomic rename state.json.tmp → state.json.
//  7. fsync parent directory so the rename survives a crash before the log append.
//  8. Append NDJSON line to transitions.log with O_APPEND|O_NOFOLLOW; fsync.
//  9. Release flock.
//
// The rename is the externally-visible commit point: if the log append fails
// after the rename, readers still see consistent state.json. If any step
// before the rename fails, state.json is untouched.
func UpdateState(taskDir string, mutator Mutator) error {
	lf, err := OpenTaskLock(taskDir)
	if err != nil {
		return err
	}
	defer lf.Close()

	if err := acquireFlock(lf, true); err != nil {
		return err
	}
	defer func() { _ = releaseFlock(lf) }()

	cur, err := readStateLocked(taskDir)
	if err != nil {
		return err
	}
	if isTaskStateTerminal(cur.State) {
		return ErrAlreadyTerminal
	}

	next, entry, err := mutator(cur)
	if err != nil {
		return err
	}
	if next == nil {
		// Mutator chose to skip the write (e.g. idempotent update). Release
		// the lock without touching state.json or transitions.log.
		return nil
	}
	if err := validateState(next); err != nil {
		// A programming error in a mutator must not corrupt state.json. The
		// ErrCorruptedState surface is reused here even though the in-memory
		// value is what's malformed — the symptom is the same for callers.
		return ErrCorruptedState
	}

	if err := writeStateAtomic(taskDir, next); err != nil {
		return err
	}
	if entry != nil {
		if err := appendTransitionLog(taskDir, entry); err != nil {
			// state.json is already committed; the log append failure leaves
			// a legitimate discrepancy between state_transitions (in-file)
			// and transitions.log. state.json is authoritative by design.
			return err
		}
	}
	return nil
}

// writeStateAtomic writes state.json.tmp, fsyncs it, renames to state.json,
// and fsyncs the parent directory. Assumes the caller holds the exclusive
// flock.
func writeStateAtomic(taskDir string, st *TaskState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state.json: %w", err)
	}
	tmpPath := filepath.Join(taskDir, stateFileName+".tmp")
	// O_NOFOLLOW on the tmp file protects against a symlink-substitution
	// attack where an attacker pre-creates a symlink at state.json.tmp.
	// O_TRUNC ensures we start from an empty file even if an old tmp exists.
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

	dstPath := filepath.Join(taskDir, stateFileName)
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, dstPath, err)
	}

	// fsync the parent directory so the rename survives a crash before the
	// transitions.log append completes. See DESIGN Decision 1 write-order.
	if err := fsyncDir(taskDir); err != nil {
		return fmt.Errorf("fsync parent %s: %w", taskDir, err)
	}
	return nil
}

// appendTransitionLog opens transitions.log in append mode with O_NOFOLLOW,
// writes a single NDJSON line, and fsyncs. Assumes the caller holds the
// exclusive flock so no other writer interleaves.
func appendTransitionLog(taskDir string, entry *TransitionLogEntry) error {
	if entry.V == 0 {
		entry.V = 1
	}
	if entry.At == "" {
		entry.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal log entry: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(taskDir, transitionsFileName)
	f, err := os.OpenFile(path,
		os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync %s: %w", path, err)
	}
	return f.Close()
}

// readFileNoFollow is os.ReadFile with the O_NOFOLLOW open flag. The stdlib
// function follows symlinks; the taskstore requires the caller-observed file
// to be a regular file — a symlink-substitution test would otherwise pass
// through silently.
func readFileNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		// ELOOP (symlink) is mapped to a distinct message so the
		// symlink-substitution test can assert on it.
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("symlink not permitted: %s: %w", path, err)
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// fsyncDir opens a directory read-only and fsyncs it. Required after
// atomic rename so the new directory entry survives a crash.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
