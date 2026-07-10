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

// HandledKey is the stable identity of a PR in the handled-set: "owner/repo#number".
func HandledKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// LoadHandledSet reads the handled-set for a workspace. A missing file is an
// empty set, not an error (the first run has none). Malformed lines are
// skipped, never fatal.
func LoadHandledSet(workspaceRoot string) (map[string]bool, error) {
	set := map[string]bool{}
	f, err := os.Open(filepath.Join(workspaceRoot, handledSetRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, fmt.Errorf("opening handled-set: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !isHandledKey(line) {
			continue // malformed lines are ignored, not fatal
		}
		set[line] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading handled-set: %w", err)
	}
	return set, nil
}

// AppendHandled records a PR as handled. It is called only after a successful
// contained dispatch, so a transient poll/dispatch failure never permanently
// suppresses a review. Appending an already-present key is a no-op.
func AppendHandled(workspaceRoot, key string) error {
	if !isHandledKey(key) {
		return fmt.Errorf("refusing to append malformed handled key %q", key)
	}
	set, err := LoadHandledSet(workspaceRoot)
	if err != nil {
		return err
	}
	if set[key] {
		return nil
	}
	dir := filepath.Join(workspaceRoot, ".niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating .niwa dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(workspaceRoot, handledSetRelPath),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening handled-set for append: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, key); err != nil {
		return fmt.Errorf("writing handled-set: %w", err)
	}
	return nil
}

// isHandledKey validates the "owner/repo#number" shape. Used both to reject a
// malformed append and to skip a malformed line on read.
func isHandledKey(s string) bool {
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
