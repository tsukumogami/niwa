// Package watch implements niwa's `watch --once` PR-review dispatch: the
// deterministic poll/select pipeline, the hardened PR fetch, the contained
// dispatch surface, and the durable state (handled-set + staged-review
// records) the verb reads and writes. It carries no model and no resident
// process -- `watch --once` is a stateless single-shot verb.
package watch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handledSetRelPath is the workspace-relative location of the flat handled-set.
const handledSetRelPath = ".niwa/watch-handled"

// stagedRecordsRelDir is the workspace-relative directory holding one
// staged-review record per dispatched PR, keyed by handle.
const stagedRecordsRelDir = ".niwa/watch"

// TriggerSemantics declares how a source's dispatch state coalesces. The PR
// source is level-triggered: a PR has one live review that re-fires on a new
// head, coalescing intermediate pushes. A future edge-triggered source records
// SemanticsEdge so it is not forced into that PR coalescing.
type TriggerSemantics string

const (
	// SemanticsLevel is the PR source's declaration: one live review per PR,
	// re-fired on head advancement, intermediate pushes coalesced.
	SemanticsLevel TriggerSemantics = "level"
	// SemanticsEdge is reserved for a source that must react to every event
	// rather than to the latest state. No source declares it yet.
	SemanticsEdge TriggerSemantics = "edge"
)

// stateHeaderPrefix is the leading comment the handled-set carries so the
// declared trigger semantics round-trip. LoadHandledSet tolerates it (and any
// other comment line) as a non-data line.
const stateHeaderPrefix = "# niwa-watch-state v2"

// HandledKey is the SHA-aware handled-set line for a PR:
// "owner/repo#number@<sha>". An empty sha yields the bare identity
// ("owner/repo#number"), the legacy "handled at unknown SHA" shape.
func HandledKey(owner, repo string, number int, sha string) string {
	if sha == "" {
		return HandledIdentity(owner, repo, number)
	}
	return HandledIdentity(owner, repo, number) + "@" + sha
}

// HandledIdentity is the stable per-PR identity used to key the handled-set,
// independent of the last-dispatched SHA: "owner/repo#number".
func HandledIdentity(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// LoadHandledSet reads the handled-set for a workspace and returns a map from
// each PR identity ("owner/repo#number") to its last-dispatched head SHA. An
// empty SHA means "handled at unknown SHA" -- a legacy SHA-less line loaded in
// place, which the decision layer adopts to the current head without re-staging.
// A missing file is an empty set, not an error (the first run has none).
// Malformed lines are skipped, never fatal. When an identity appears more than
// once, the last occurrence wins.
func LoadHandledSet(workspaceRoot string) (map[string]string, error) {
	m, _, err := loadHandledState(workspaceRoot)
	return m, err
}

// LoadTriggerSemantics returns the trigger semantics the handled-set declares.
// A file without a declaration -- including a legacy file with no header --
// defaults to SemanticsLevel, the PR source's behavior.
func LoadTriggerSemantics(workspaceRoot string) (TriggerSemantics, error) {
	_, sem, err := loadHandledState(workspaceRoot)
	return sem, err
}

// HandledMembership projects the SHA-aware handled map onto a set-membership
// view for callers that only need "is this PR handled?" and not the SHA. It is a
// thin compatibility path over LoadHandledSet's primary SHA map.
func HandledMembership(handled map[string]string) map[string]bool {
	set := make(map[string]bool, len(handled))
	for id := range handled {
		set[id] = true
	}
	return set
}

// loadHandledState is the single-scan core behind LoadHandledSet and
// LoadTriggerSemantics: it returns the identity->last-SHA map and the declared
// trigger semantics in one pass.
func loadHandledState(workspaceRoot string) (map[string]string, TriggerSemantics, error) {
	m := map[string]string{}
	sem := SemanticsLevel
	f, err := os.Open(filepath.Join(workspaceRoot, handledSetRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return m, sem, nil
		}
		return nil, sem, fmt.Errorf("opening handled-set: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if s, ok := parseSemanticsHeader(line); ok {
				sem = s
			}
			continue // comment/header line: not data
		}
		id, sha, ok := parseHandledLine(line)
		if !ok {
			continue // malformed lines are ignored, not fatal
		}
		m[id] = sha // last occurrence per identity wins
	}
	if err := sc.Err(); err != nil {
		return nil, sem, fmt.Errorf("reading handled-set: %w", err)
	}
	return m, sem, nil
}

// AppendHandled records a PR as handled at the given head SHA. It is called only
// after a successful contained dispatch, so a transient poll/dispatch failure
// never permanently suppresses a review. It is idempotent per identity: an entry
// already present has its recorded SHA moved forward to sha, and the file is
// rewritten rather than accumulating a duplicate line; re-recording the same SHA
// is a no-op. The declared trigger semantics and every other entry are preserved.
func AppendHandled(workspaceRoot, owner, repo string, number int, sha string) error {
	key := HandledKey(owner, repo, number, sha)
	if !isHandledKey(key) {
		return fmt.Errorf("refusing to append malformed handled key %q", key)
	}
	id := HandledIdentity(owner, repo, number)

	m, sem, err := loadHandledState(workspaceRoot)
	if err != nil {
		return err
	}
	if existing, ok := m[id]; ok && existing == sha {
		return nil // already recorded at this SHA
	}
	m[id] = sha
	return writeHandledState(workspaceRoot, m, sem)
}

// writeHandledState rewrites the handled-set atomically (temp file + rename): the
// trigger-semantics header followed by one "identity[@sha]" line per entry,
// sorted by identity for a deterministic file. A legacy unknown-SHA entry (empty
// sha) round-trips as a bare identity line, preserving its "handled at unknown
// SHA" meaning.
func writeHandledState(workspaceRoot string, m map[string]string, sem TriggerSemantics) error {
	dir := filepath.Join(workspaceRoot, ".niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating .niwa dir: %w", err)
	}

	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	fmt.Fprintf(&b, "%s semantics=%s\n", stateHeaderPrefix, sem)
	for _, id := range ids {
		if sha := m[id]; sha != "" {
			fmt.Fprintf(&b, "%s@%s\n", id, sha)
		} else {
			fmt.Fprintln(&b, id)
		}
	}

	tmp, err := os.CreateTemp(dir, "watch-handled-*.tmp")
	if err != nil {
		return fmt.Errorf("creating handled-set temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return fmt.Errorf("writing handled-set: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing handled-set temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("setting handled-set mode: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(workspaceRoot, handledSetRelPath)); err != nil {
		return fmt.Errorf("replacing handled-set: %w", err)
	}
	return nil
}

// parseHandledLine parses a handled-set data line into its PR identity and
// last-dispatched SHA. It accepts both the SHA-aware "owner/repo#number@<sha>"
// shape and the legacy SHA-less "owner/repo#number" shape (sha == ""). ok is
// false for a malformed line, which the caller skips.
func parseHandledLine(line string) (identity, sha string, ok bool) {
	if at := strings.IndexByte(line, '@'); at >= 0 {
		identity, sha = line[:at], line[at+1:]
		if !isHandledIdentity(identity) || !isHexSHA(sha) {
			return "", "", false
		}
		return identity, sha, true
	}
	if !isHandledIdentity(line) {
		return "", "", false
	}
	return line, "", true
}

// isHandledKey validates a handled-set data line -- either the SHA-aware
// "owner/repo#number@<sha>" shape or the legacy SHA-less identity. It is the
// guard AppendHandled uses to refuse writing a malformed key, and mirrors the
// tolerance parseHandledLine applies on read.
func isHandledKey(s string) bool {
	_, _, ok := parseHandledLine(s)
	return ok
}

// isHandledIdentity validates the bare "owner/repo#number" identity shape,
// preserving the shipped structural/charset checks (non-empty owner/repo, a
// numeric PR number) so a malformed or hostile line is skipped, never fatal and
// never interpolated anywhere executable.
func isHandledIdentity(s string) bool {
	slash := strings.IndexByte(s, '/')
	hash := strings.IndexByte(s, '#')
	if slash <= 0 || hash <= slash+1 {
		return false
	}
	owner := s[:slash]
	repo := s[slash+1 : hash]
	num := s[hash+1:]
	if owner == "" || repo == "" || num == "" {
		return false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isHexSHA reports whether s is a plausible git head SHA: 7 to 64 lowercase hex
// digits (an abbreviated SHA-1 through a full SHA-256). The charset check keeps a
// hostile SHA field from smuggling anything but hex into the permanent state.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// parseSemanticsHeader extracts a "semantics=<value>" declaration from a comment
// line, returning the recognized TriggerSemantics. An unrecognized or absent
// value yields ok == false, so the caller keeps its default.
func parseSemanticsHeader(line string) (TriggerSemantics, bool) {
	for _, field := range strings.Fields(line) {
		v, found := strings.CutPrefix(field, "semantics=")
		if !found {
			continue
		}
		switch TriggerSemantics(v) {
		case SemanticsLevel:
			return SemanticsLevel, true
		case SemanticsEdge:
			return SemanticsEdge, true
		}
	}
	return "", false
}

// StagedRecord is discoverability metadata niwa persists at dispatch time so a
// handle can be resolved to its PR and drafted review (e.g. to find the draft a
// contained session wrote). The handle is the dispatch session's short id (shown
// in the agent view).
type StagedRecord struct {
	Handle    string `json:"handle"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Number    int    `json:"number"`
	URL       string `json:"url"`
	DraftPath string `json:"draft_path"`
	// InstancePath is the absolute path of the niwa instance the review session
	// was launched in. It is the liveness anchor: the re-dispatch decision maps a
	// staged record to a live/dead session by asking whether any Claude Code job
	// is rooted at this path (instanceHasLiveJob). It is niwa-generated (a
	// provisioned instance directory), never author-controlled.
	InstancePath string `json:"instance_path"`
	// DispatchedSHA is the head SHA the review was staged against. It is the base
	// for the freshness ancestry check: a still-open, still-requested PR is fresh
	// only while this SHA remains an ancestor of the PR's current head (ordinary
	// advancement); a force-push/rebase that moves the head off this SHA makes the
	// staged review stale. It is platform-vouched hex from GetPullHead, never
	// author-controlled free text.
	DispatchedSHA string `json:"dispatched_sha"`
	// SessionID is the review session's full Claude Code conversation UUID,
	// captured best-effort at stage time by the jobs-dir cwd-correlation
	// (captureSessionID). It is the id `claude --resume <SessionID>` accepts (the
	// session UUID equals the resume/conversation id and names the transcript
	// file), so it is what a Continue resumes on. Empty when capture missed: such
	// a record can never be Continued (it stays Defer). It is niwa-captured from
	// ~/.claude/jobs/*/state.json and MUST pass workspace.ValidSessionID (the
	// lowercase-UUID charset) before it becomes a CLI argument.
	SessionID string `json:"session_id,omitempty"`
	// ShortID is the review session's short id -- the jobs-dir basename shown in
	// the agent view and the handle `claude stop <ShortID>` accepts (the full
	// UUID is rejected by `claude stop`). Continuation stops the prior
	// detached-idle process by this id before resuming. Empty when capture
	// missed. It MUST pass isSafeHandle before it becomes a CLI argument.
	ShortID string `json:"short_id,omitempty"`
}

// SaveStagedRecord writes a staged-review record keyed by handle.
func SaveStagedRecord(workspaceRoot string, rec StagedRecord) error {
	if !isSafeHandle(rec.Handle) {
		return fmt.Errorf("refusing to save record with unsafe handle %q", rec.Handle)
	}
	dir := filepath.Join(workspaceRoot, stagedRecordsRelDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating staged-records dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding staged record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, rec.Handle+".json"), data, 0o644); err != nil {
		return fmt.Errorf("writing staged record: %w", err)
	}
	return nil
}

// LoadStagedRecord resolves a handle to its staged-review record. The handle is
// validated against a safe charset before it becomes a path component, closing
// a traversal surface.
func LoadStagedRecord(workspaceRoot, handle string) (StagedRecord, error) {
	var rec StagedRecord
	if !isSafeHandle(handle) {
		return rec, fmt.Errorf("unsafe handle %q", handle)
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, stagedRecordsRelDir, handle+".json"))
	if err != nil {
		return rec, fmt.Errorf("reading staged record for %q: %w", handle, err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("decoding staged record for %q: %w", handle, err)
	}
	return rec, nil
}

// DeleteStagedRecord removes the staged-review record for a handle. It is the
// record-layer counterpart to the instance reaper: the watcher-pass GC calls it
// to prune a dead or stale record so the record store stops growing unbounded.
// The handle is validated against the safe charset before it becomes a path
// component (the traversal guard LoadStagedRecord applies). Removing a record
// that is already gone is not an error -- the prune is idempotent across passes.
func DeleteStagedRecord(workspaceRoot, handle string) error {
	if !isSafeHandle(handle) {
		return fmt.Errorf("refusing to delete record with unsafe handle %q", handle)
	}
	err := os.Remove(filepath.Join(workspaceRoot, stagedRecordsRelDir, handle+".json"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting staged record for %q: %w", handle, err)
	}
	return nil
}

// ListStagedHandles returns the handles of all staged records, sorted.
func ListStagedHandles(workspaceRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(workspaceRoot, stagedRecordsRelDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var handles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			handles = append(handles, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Strings(handles)
	return handles, nil
}

// IsSafeHandle reports whether h passes the conservative handle charset the
// staged-record store enforces. It is the exported form callers use to validate
// a captured short id (isSafeHandle precedent) before it becomes a CLI argument
// -- e.g. `claude stop <ShortID>` in the continuation path.
func IsSafeHandle(h string) bool { return isSafeHandle(h) }

// isSafeHandle allows only a conservative charset for a value that becomes a
// filename: lowercase/uppercase alphanumerics, dash, and underscore.
func isSafeHandle(h string) bool {
	if h == "" || len(h) > 128 {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
