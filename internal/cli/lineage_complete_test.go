package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchedEntry reads the entry the last dispatch wrote, failing when there
// is none.
func dispatchedEntry(t *testing.T, root string, f *dispatchFakes) workspace.StoredLineageEntry {
	t.Helper()
	rec := readRecord(t, f)
	stored, found, err := workspace.ReadLineageEntry(root, rec.DispatchID)
	if err != nil || !found {
		t.Fatalf("entry for %s: found %v, err %v", rec.DispatchID, found, err)
	}
	return stored
}

// breakSessionStore makes the session mapping write fail by putting a file
// where the store's directory goes.
func breakSessionStore(t *testing.T, root string) {
	t.Helper()
	store := filepath.Join(root, workspace.StateDir, "sessions")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// foregroundDispatch makes the next dispatch run its worker in the
// foreground, the turn ending before capture.
func foregroundDispatch(t *testing.T) {
	t.Helper()
	spec, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	spec.Runner = agentplan.RunnerForeground
	substituteLaunchSpec(t, spec)
	dispatchDetach = false
}

func TestDispatchLineage_CompletesWithTheSessionID(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	var atCompletion []string
	prev := completeLineageEntryFunc
	completeLineageEntryFunc = func(r, id, sid string) error {
		// The mapping is written after completion, never before.
		mappings, _ := workspace.ListSessionMappings(r)
		for _, m := range mappings {
			atCompletion = append(atCompletion, m.SessionID)
		}
		return prev(r, id, sid)
	}
	t.Cleanup(func() { completeLineageEntryFunc = prev })
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if len(atCompletion) != 0 {
		t.Errorf("mappings already written at completion: %v", atCompletion)
	}
	if got := dispatchedEntry(t, root, f).Parsed.Entry.SessionID; got != dispatchTestSessionID {
		t.Errorf("session_id = %q, want %q", got, dispatchTestSessionID)
	}
}

func TestDispatchLineage_FailedCompletionWarnsAndGoesOn(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	prev := completeLineageEntryFunc
	completeLineageEntryFunc = func(string, string, string) error { return errors.New("disk full") }
	t.Cleanup(func() { completeLineageEntryFunc = prev })
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch failed: %v\n%s", err, stderr)
	}
	stored := dispatchedEntry(t, root, f)
	path := filepath.Join(workspace.LineageDir(root), stored.Parsed.Entry.DispatchID+".json")
	if n := strings.Count(stderr, "could not record the session id in the lineage entry"); n != 1 || !strings.Contains(stderr, path) {
		t.Errorf("want one warning naming %s, got:\n%s", path, stderr)
	}
	if stored.Parsed.Entry.SessionID != "" {
		t.Errorf("session_id = %q, want none", stored.Parsed.Entry.SessionID)
	}
	if f.destroyCalled != 0 {
		t.Error("a failed completion rolled the dispatch back")
	}
}

func TestDispatchLineage_ForegroundMappingFailureKeepsTheCompletedEntry(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	foregroundDispatch(t)
	breakSessionStore(t, root)
	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "writing dispatch session mapping") {
		t.Fatalf("err = %v, want the mapping write to fail", err)
	}
	if f.destroyCalled != 0 {
		t.Fatalf("the foreground instance was rolled back")
	}
	if got := dispatchedEntry(t, root, f).Parsed.Entry.SessionID; got != dispatchTestSessionID {
		t.Errorf("session_id = %q, want %q", got, dispatchTestSessionID)
	}
}

func TestDispatchLineage_DetachedMappingFailureRemovesTheEntry(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	breakSessionStore(t, root)
	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("the dispatch succeeded")
	}
	if f.destroyCalled != 1 {
		t.Errorf("destroyed %d, want the rollback", f.destroyCalled)
	}
	if files := storeFiles(t, root); len(files) != 0 {
		t.Errorf("store files = %v, want none", files)
	}
}

func TestDispatchLineage_CaptureFailure(t *testing.T) {
	failCapture := func(agentplan.SessionRecords, string, string, time.Duration, func() time.Time, time.Duration) (string, string, error) {
		return "", "", errors.New("no session record")
	}
	t.Run("detached leaves no entry", func(t *testing.T) {
		root, f := authorityWorkspace(t, "", "")
		dispatchCapture = failCapture
		if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
			t.Fatal("the dispatch succeeded")
		}
		if f.destroyCalled != 1 {
			t.Errorf("destroyed %d", f.destroyCalled)
		}
		if files := storeFiles(t, root); len(files) != 0 {
			t.Errorf("store files = %v, want none", files)
		}
	})
	t.Run("foreground keeps an entry without a session id", func(t *testing.T) {
		root, f := authorityWorkspace(t, "", "")
		foregroundDispatch(t)
		dispatchCapture = failCapture
		if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v\n%s", err, stderr)
		}
		if f.destroyCalled != 0 {
			t.Fatal("the foreground instance was rolled back")
		}
		stored := dispatchedEntry(t, root, f)
		if stored.Parsed.Entry.SessionID != "" || strings.Contains(string(stored.Raw), "session_id") {
			t.Errorf("entry = %s, want no session_id", stored.Raw)
		}
	})
}
