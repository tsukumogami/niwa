// handlers_change.go ships the three F5 change-primitive MCP tools:
// niwa_create_change, niwa_list_changes, niwa_query_change. Each handler
// composes the changestore (state.json read/write under per-change flock)
// and the changelog (dual-target event emitter) primitives — both shipped
// in earlier issues of this PLAN — to expose a read-only-to-LLM view of
// the per-instance review surface.
//
// The pipeline for handleCreateChange mirrors DESIGN "Key code paths"
// step-by-step: validate the session_id, load the session, resolve the
// worktree, compute the idempotency key, scan for an existing non-cleaned
// match, resolve base_ref via R8 precedence (or the caller's hint),
// capture the diff with the 4 MiB truncate trailer, reserve a change ID,
// write state.json + diff.patch, emit change_ready. The per-session
// create lock at `.niwa/changes/.session-<sid>.create.lock` serializes
// concurrent calls for the same session so the idempotency check + write
// is an atomic critical section — a TOCTOU window between scan and
// reserve would otherwise let two callers each emit one change_ready for
// the same (session_id, head_ref) tuple, breaking R5's idempotency
// guarantee for that event.
//
// URL composition reads `.niwa/surface.port`; when the file is absent
// (surface not running) the URL substitutes the literal `<port>`
// placeholder. The change ID is durable across surface restarts, so a
// caller that obtains the URL pre-surface can reuse it once the surface
// boots — only the port part is non-durable.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// createChangeArgs holds parameters for niwa_create_change (PRD R3).
// session_id is required; base_ref_hint overrides the R8 auto-discovery
// chain; metadata is opaque to F5 (stored verbatim in state.json, never
// echoed into the change_ready audit payload — see security table in
// the design doc).
type createChangeArgs struct {
	SessionID   string         `json:"session_id"`
	BaseRefHint string         `json:"base_ref_hint,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// listChangesArgs is the input shape for niwa_list_changes. Both filters
// are optional; when both are set they AND-compose. session_id matches a
// change whose originating_session field equals the value.
type listChangesArgs struct {
	State     string `json:"state,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// queryChangeArgs is the input shape for niwa_query_change. The only
// field is change_id; UUIDv4 validation happens up-front so a malformed
// id maps to `not_found` without touching the filesystem.
type queryChangeArgs struct {
	ChangeID string `json:"change_id"`
}

// diffSizeCap matches PRD R7 verbatim: 4 MiB byte boundary on captured
// diffs. Bytes past this offset are dropped and replaced with the
// truncate trailer. The renderer treats the trailer as plain text inside
// the unified-diff <pre> block.
const diffSizeCap = 4 * 1024 * 1024

// diffTruncateTrailer is the marker appended to a truncated diff so the
// reviewer knows the snapshot is incomplete and the originating command
// is recoverable. Format matches PRD R7 verbatim.
const diffTruncateTrailer = "--- diff truncated at 4 MiB; full diff available via 'git -C <worktree> diff <base>..<head>' ---\n"

// diffPatchFileName is the per-change diff snapshot. The path is
// recorded in ChangeState.DiffPath as a relative reference so a future
// re-rooting of `.niwa/changes/` does not invalidate references.
const diffPatchFileName = "diff.patch"

// surfacePortFileName is the per-instance file written by `niwa surface
// serve` carrying the bound port (PRD R10). Absent → surface not
// running; URL composition falls back to the `<port>` placeholder.
const surfacePortFileName = "surface.port"

// sessionCreateLockPrefix is the filename prefix for the per-session
// create lock under `.niwa/changes/`. The trailing `<sid>.create.lock`
// uses the 8-hex session ID so the file is unambiguous and the
// changes-dir scan filters it via the leading dot (uuidV4Regex never
// matches a dotted filename).
const sessionCreateLockPrefix = ".session-"

// sessionCreateLockSuffix complements sessionCreateLockPrefix.
const sessionCreateLockSuffix = ".create.lock"

// handleCreateChange implements the niwa_create_change MCP tool. Pipeline
// follows DESIGN "Key code paths" exactly so the implementation is
// traceable against the spec.
func (s *Server) handleCreateChange(args createChangeArgs) toolResult {
	root := s.taskStoreRoot()
	if root == "" {
		return errResult("niwa_create_change: instance root not configured")
	}

	if !sessionIDRe.MatchString(args.SessionID) {
		return errResultCode("invalid_session_id",
			fmt.Sprintf("session_id %q must match ^[0-9a-f]{8}$", args.SessionID))
	}

	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	sessionState, err := ReadSessionLifecycleState(sessionsDir, args.SessionID)
	if err != nil {
		return errResultCode("session_not_found",
			fmt.Sprintf("session %s not found: %v", args.SessionID, err))
	}

	worktree := sessionState.WorktreePath
	if worktree == "" || !isGitWorktree(worktree) {
		return errResultCode("worktree_missing",
			fmt.Sprintf("session %s has no usable git worktree (path=%q)",
				args.SessionID, worktree))
	}

	headRef, err := gitRevParse(worktree, "HEAD")
	if err != nil {
		return errResultCode("worktree_missing",
			fmt.Sprintf("cannot resolve HEAD in %s: %v", worktree, err))
	}

	// Per-session create lock: hold this for the duration of the
	// scan-then-reserve-then-write critical section so two concurrent
	// calls for the same session_id produce one change_ready and one
	// not_modified no-op. The lock file's name carries the session_id
	// so locks for distinct sessions never block each other.
	changesDir := ChangesDir(root)
	if err := os.MkdirAll(changesDir, 0o700); err != nil {
		return errResult("create changes dir: " + err.Error())
	}
	lockPath := filepath.Join(changesDir,
		sessionCreateLockPrefix+args.SessionID+sessionCreateLockSuffix)
	clf, err := os.OpenFile(lockPath,
		os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errResult("open session create lock: " + err.Error())
	}
	defer clf.Close()
	if err := acquireFlock(clf, true); err != nil {
		return errResult("acquire session create lock: " + err.Error())
	}
	defer func() { _ = releaseFlock(clf) }()

	if existing, scanErr := findExistingChange(root, args.SessionID, headRef); scanErr == nil && existing != nil {
		// Idempotent hit — return the existing change with state="not_modified"
		// (the wire-level signal that no event was emitted on this call). The
		// state.json on disk retains its true state (pending / in-review),
		// never "not_modified" — the latter exists only in the MCP response.
		resp := map[string]any{
			"change_id": existing.ID,
			"state":     "not_modified",
			"url":       composeChangeURL(root, existing.ID),
			"base_ref":  existing.BaseRef,
			"head_ref":  existing.HeadRef,
		}
		data, _ := json.Marshal(resp)
		return textResult(string(data))
	}

	var baseRef string
	if args.BaseRefHint != "" {
		baseRef, err = gitRevParse(worktree, args.BaseRefHint)
		if err != nil {
			return errResultCode("base_ref_hint_unresolved",
				fmt.Sprintf("base_ref_hint %q did not resolve: %v", args.BaseRefHint, err))
		}
	} else {
		baseRef, err = resolveBaseRefAuto(worktree)
		if err != nil {
			return errResultCode("base_ref_unresolved", err.Error())
		}
	}

	diff, err := captureDiff(worktree, baseRef, headRef)
	if err != nil {
		return errResult("git diff: " + err.Error())
	}

	changeID, err := ReserveChangeID(root)
	if err != nil {
		return errResult("reserve change ID: " + err.Error())
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	metadata := args.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	originatingTasks := []string{}
	if s.taskID != "" {
		originatingTasks = []string{s.taskID}
	}
	state := ChangeState{
		V:                  1,
		ID:                 changeID,
		State:              ChangeStatePending,
		OriginatingSession: args.SessionID,
		OriginatingTasks:   originatingTasks,
		CreatedAt:          now,
		UpdatedAt:          now,
		BaseRef:            baseRef,
		HeadRef:            headRef,
		Branch:             currentBranch(worktree),
		WorktreePath:       worktree,
		DiffPath:           diffPatchFileName,
		Metadata:           metadata,
	}
	if err := WriteInitial(root, state); err != nil {
		return errResult("write state.json: " + err.Error())
	}

	changeDir, _ := ChangeDir(root, changeID)
	diffPath := filepath.Join(changeDir, diffPatchFileName)
	if err := os.WriteFile(diffPath, diff, 0o600); err != nil {
		return errResult("write diff.patch: " + err.Error())
	}

	url := composeChangeURL(root, changeID)
	// AppendChangeEvent returns errors.Join(transitionsErr, auditErr); per
	// PRD R4 + DESIGN D5 a failed audit emit does not fail the mutation.
	// We swallow the joined error for the same reason — partial telemetry
	// loss is preferred over a failed create that already wrote the change.
	_ = AppendChangeEvent(root, s.audit, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: changeID,
		Payload: map[string]any{
			"change_id":           changeID,
			"url":                 url,
			"originating_session": args.SessionID,
			"base_ref":            baseRef,
			"head_ref":            headRef,
		},
	})

	resp := map[string]any{
		"change_id": changeID,
		"state":     ChangeStatePending,
		"url":       url,
		"base_ref":  baseRef,
		"head_ref":  headRef,
	}
	data, _ := json.Marshal(resp)
	return textResult(string(data))
}

// handleListChanges implements the niwa_list_changes MCP tool. The result
// is the full ChangeState list filtered by the AND-composed optional
// filters, sorted by UpdatedAt descending so freshly-changed entries
// surface first in the operator's review queue.
func (s *Server) handleListChanges(args listChangesArgs) toolResult {
	root := s.taskStoreRoot()
	if root == "" {
		return textResult(`{"changes":[]}`)
	}
	changes, err := scanChanges(root, args.State, args.SessionID)
	if err != nil {
		return errResult("scanning changes: " + err.Error())
	}
	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].UpdatedAt > changes[j].UpdatedAt
	})
	summaries := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		summaries = append(summaries, map[string]any{
			"id":         c.ID,
			"state":      c.State,
			"created_at": c.CreatedAt,
			"url":        composeChangeURL(root, c.ID),
			"head_ref":   c.HeadRef,
			"branch":     c.Branch,
		})
	}
	resp := map[string]any{"changes": summaries}
	data, _ := json.Marshal(resp)
	return textResult(string(data))
}

// handleQueryChange implements the niwa_query_change MCP tool. Returns the
// full ChangeState plus the last 20 transitions.log entries (oldest-first).
// `cleaned` changes are treated as not-visible per PRD R3 — the state.json
// stays on disk for forensics, but the tool returns `not_found` so
// programmatic consumers (e.g. an LLM polling on a change ID) treat the
// cleaned state as removed.
func (s *Server) handleQueryChange(args queryChangeArgs) toolResult {
	root := s.taskStoreRoot()
	if root == "" {
		return errResultCode("not_found", "instance root not configured")
	}
	if !uuidV4Regex.MatchString(args.ChangeID) {
		return errResultCode("not_found",
			fmt.Sprintf("change %q not found", args.ChangeID))
	}
	st, err := Read(root, args.ChangeID)
	if err != nil {
		return errResultCode("not_found",
			fmt.Sprintf("change %s not found: %v", args.ChangeID, err))
	}
	if st.State == ChangeStateCleaned {
		return errResultCode("not_found",
			fmt.Sprintf("change %s is cleaned", args.ChangeID))
	}
	transitions, _ := readRecentTransitions(root, args.ChangeID, 20)
	resp := map[string]any{
		"state":       st,
		"transitions": transitions,
	}
	data, _ := json.Marshal(resp)
	return textResult(string(data))
}

// findExistingChange scans `.niwa/changes/` for a non-cleaned change
// whose OriginatingSessions includes sessionID AND whose HeadRef matches
// headRef. Returns nil when no match exists. Errors are non-fatal — a
// corrupt change directory is skipped without aborting the scan, matching
// the discipline in ListSessionLifecycleStates.
func findExistingChange(root, sessionID, headRef string) (*ChangeState, error) {
	changesDir := ChangesDir(root)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !uuidV4Regex.MatchString(e.Name()) {
			continue
		}
		st, err := Read(root, e.Name())
		if err != nil {
			continue
		}
		if st.State == ChangeStateCleaned {
			continue
		}
		if st.HeadRef != headRef {
			continue
		}
		if st.OriginatingSession != sessionID {
			continue
		}
		return st, nil
	}
	return nil, nil
}

// scanChanges enumerates every change under `.niwa/changes/` and returns
// those matching the AND-composed filters. Corrupt change directories
// are skipped silently — a partial scan is preferred over a failure that
// hides every healthy change.
func scanChanges(root, stateFilter, sessionFilter string) ([]ChangeState, error) {
	changesDir := ChangesDir(root)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]ChangeState, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !uuidV4Regex.MatchString(e.Name()) {
			continue
		}
		st, err := Read(root, e.Name())
		if err != nil {
			continue
		}
		if stateFilter != "" && st.State != stateFilter {
			continue
		}
		if sessionFilter != "" && st.OriginatingSession != sessionFilter {
			continue
		}
		out = append(out, *st)
	}
	return out, nil
}

// isGitWorktree returns true when path resolves via `git -C <path>
// rev-parse --git-dir`. A missing path or a non-git directory returns
// false; the caller maps that to error_code=worktree_missing.
func isGitWorktree(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// gitRevParse resolves ref to its full commit SHA inside worktree.
// Returns the rev-parse stderr text via the wrapped error so the caller
// can surface a useful diagnostic for malformed hints.
func gitRevParse(worktree, ref string) (string, error) {
	cmd := exec.Command("git", "-C", worktree, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveBaseRefAuto walks the PRD R8 precedence chain. First match wins;
// full-chain exhaustion returns an error with the chain enumerated so the
// caller can pass base_ref_hint explicitly per PRD's documented escape
// hatch.
//
// Precedence (PRD R8 verbatim):
//
//  1. git symbolic-ref refs/remotes/origin/HEAD → resolve to SHA
//  2. origin/main
//  3. origin/master
//  4. main
//  5. master
func resolveBaseRefAuto(worktree string) (string, error) {
	out, err := exec.Command("git", "-C", worktree, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		symref := strings.TrimSpace(string(out))
		if sha, err := gitRevParse(worktree, symref); err == nil {
			return sha, nil
		}
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if sha, err := gitRevParse(worktree, ref); err == nil {
			return sha, nil
		}
	}
	return "", errors.New(
		"base_ref_unresolved: tried origin/HEAD, origin/main, origin/master, main, master. " +
			"Pass base_ref_hint explicitly.")
}

// currentBranch returns the worktree's current branch name, or "" when
// HEAD is detached. The branch is recorded on the change for surface
// rendering (D11) but the change itself is anchored by HeadRef so a
// later branch rename does not orphan it.
func currentBranch(worktree string) string {
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	br := strings.TrimSpace(string(out))
	if br == "HEAD" {
		return ""
	}
	return br
}

// captureDiff runs `git -C <worktree> diff <base>..<head>` and truncates
// the output at diffSizeCap, appending diffTruncateTrailer when the cap
// trips. The empty-diff case returns an empty slice — the change is still
// created and the renderer shows the "no changes" body per R7.
func captureDiff(worktree, base, head string) ([]byte, error) {
	cmd := exec.Command("git", "-C", worktree, "diff", base+".."+head)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(out) > diffSizeCap {
		buf := make([]byte, 0, diffSizeCap+len(diffTruncateTrailer))
		buf = append(buf, out[:diffSizeCap]...)
		buf = append(buf, []byte(diffTruncateTrailer)...)
		return buf, nil
	}
	return out, nil
}

// composeChangeURL reads `.niwa/surface.port` and renders the change URL.
// When the port file is absent or empty (surface not running), the URL
// substitutes the literal `<port>` placeholder per PRD R3 — the caller
// retains a durable URL even when the surface boots later, because the
// change ID is the durable anchor.
func composeChangeURL(root, changeID string) string {
	portFile := filepath.Join(root, ".niwa", surfacePortFileName)
	port := "<port>"
	if data, err := os.ReadFile(portFile); err == nil {
		if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
			port = trimmed
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%s/changes/%s", port, changeID)
}

// readRecentTransitions reads the last n NDJSON lines from the change's
// `transitions.log` and returns them oldest-first per PRD R3. A missing
// log file returns an empty slice without an error: a freshly-created
// change may not yet have a transitions.log when the caller queries it
// (the change_ready append is best-effort), and that should not poison
// a query that's otherwise valid.
func readRecentTransitions(root, changeID string, n int) ([]map[string]any, error) {
	dir, err := ChangeDir(root, changeID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, transitionsLogFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}
	out := make([]map[string]any, 0, len(lines)-start)
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
