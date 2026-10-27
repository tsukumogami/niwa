package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchedLaunchRecord reads the launch record the last dispatch wrote,
// failing when there is none.
func dispatchedLaunchRecord(t *testing.T, root string, f *dispatchFakes) workspace.LaunchRecord {
	t.Helper()
	id := readRecord(t, f).DispatchID
	rec, err := workspace.ReadLaunchRecord(root, id)
	if err != nil {
		t.Fatalf("launch record for %s: %v", id, err)
	}
	return rec
}

// captureLaunchSettings makes the next dispatch remember the settings
// document it hands the agent.
func captureLaunchSettings(t *testing.T) *string {
	t.Helper()
	var doc string
	prev := dispatchLaunch
	dispatchLaunch = func(ctx context.Context, req launchRequest) error {
		for i, a := range req.Passthrough {
			if a == req.Spec.Flags.Settings && i+1 < len(req.Passthrough) {
				doc = req.Passthrough[i+1]
			}
		}
		return prev(ctx, req)
	}
	return &doc
}

func TestDispatchLaunchRecord_RecordsWhatTheWorkerWasLaunchedWith(t *testing.T) {
	t.Setenv("PASS_MARK", "carried")
	t.Setenv("GH_TOKEN", "never-recorded")
	root, f := authorityWorkspace(t, "[session.dispatch_env]\npassthrough = [\"PASS_MARK\"]\n", "")
	dispatchModel = "opus"
	dispatchPermissionMode = "acceptEdits"
	dispatchName = "fix-thing"
	doc := captureLaunchSettings(t)
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	rec := dispatchedLaunchRecord(t, root, f)
	if rec.Schema != workspace.LaunchRecordSchema {
		t.Errorf("schema = %q", rec.Schema)
	}
	if rec.SessionID != dispatchTestSessionID {
		t.Errorf("session_id = %q, want %q", rec.SessionID, dispatchTestSessionID)
	}
	if rec.Agent != "claude" || rec.InstanceName != filepath.Base(f.instancePath) {
		t.Errorf("agent %q, instance %q; want claude, %q", rec.Agent, rec.InstanceName, filepath.Base(f.instancePath))
	}
	if rec.PermissionMode != "acceptEdits" || rec.Model != "opus" || rec.SessionName == "" {
		t.Errorf("permission mode %q, model %q, session name %q", rec.PermissionMode, rec.Model, rec.SessionName)
	}
	if *doc == "" || rec.Settings != *doc {
		t.Errorf("settings = %q, want the document passed: %q", rec.Settings, *doc)
	}
	if rec.Env["PASS_MARK"] != "carried" || rec.Env["NIWA_REAP_KEY"] != rec.DispatchID {
		t.Errorf("env = %v", rec.Env)
	}
	if _, ok := rec.Env["GH_TOKEN"]; ok {
		t.Error("the record holds GH_TOKEN")
	}

	data, err := os.ReadFile(filepath.Join(workspace.LaunchDir(root), rec.DispatchID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "never-recorded") {
		t.Error("a credential value reached the record")
	}
	info, err := os.Stat(filepath.Join(workspace.LaunchDir(root), rec.DispatchID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %o", info.Mode().Perm())
	}
}

func TestDispatchLaunchRecord_FailedWriteWarnsAndGoesOn(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	prev := writeLaunchRecordFunc
	writeLaunchRecordFunc = func(string, workspace.LaunchRecord) error { return errors.New("disk full") }
	t.Cleanup(func() { writeLaunchRecordFunc = prev })
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch failed: %v\n%s", err, stderr)
	}
	path := filepath.Join(workspace.LaunchDir(root), readRecord(t, f).DispatchID+".json")
	if n := strings.Count(stderr, "could not write the launch record"); n != 1 || !strings.Contains(stderr, path) {
		t.Errorf("want one warning naming %s, got:\n%s", path, stderr)
	}
	if f.destroyCalled != 0 {
		t.Error("a failed record write rolled the dispatch back")
	}
	if mappings, _ := workspace.ListSessionMappings(root); len(mappings) != 1 {
		t.Errorf("mappings = %d, want the dispatch's one", len(mappings))
	}
}

func TestDispatchLaunchRecord_SurvivesDestroyAndOnReap(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	rec := dispatchedLaunchRecord(t, root, f)
	if err := os.RemoveAll(f.instancePath); err != nil {
		t.Fatal(err)
	}
	// The on-reap processing settles the session's mapping; the record is
	// not the mapping's to take.
	newOnReapStep(root, &bytes.Buffer{}).settle(workspace.SessionMapping{SessionID: rec.SessionID, InstancePath: f.instancePath, DispatchID: rec.DispatchID})
	if _, err := workspace.ReadLaunchRecord(root, rec.DispatchID); err != nil {
		t.Errorf("record gone after destroy and on-reap: %v", err)
	}
}

// launchRecordFixture writes a record for a Claude session in a scratch root
// and returns the root, the fixture home and the record.
func launchRecordFixture(t *testing.T, writtenAt time.Time) (string, string, workspace.LaunchRecord) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	rec := workspace.LaunchRecord{
		DispatchID:   "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d",
		SessionID:    dispatchTestSessionID,
		Agent:        "claude",
		InstanceName: "test-ws+fix-0123abcd",
		WrittenAt:    writtenAt,
	}
	if err := workspace.WriteLaunchRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	return root, home, rec
}

func writeTranscript(t *testing.T, root, home string, rec workspace.LaunchRecord) string {
	t.Helper()
	path := sessionattach.TranscriptPath(home, filepath.Join(root, rec.InstanceName), rec.SessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func launchRecordKept(t *testing.T, root string, rec workspace.LaunchRecord) bool {
	t.Helper()
	_, err := workspace.ReadLaunchRecord(root, rec.DispatchID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestReapLaunchRecords(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-2 * launchRecordGrace)

	t.Run("removes a record whose transcript is gone", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, old)
		var w bytes.Buffer
		if n := sweepLaunchRecords(root, home, &w, now); n != 1 || launchRecordKept(t, root, rec) {
			t.Errorf("removed %d, kept %v", n, launchRecordKept(t, root, rec))
		}
		if w.Len() != 0 {
			t.Errorf("output: %s", w.String())
		}
	})
	t.Run("keeps a record whose transcript exists", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, old)
		writeTranscript(t, root, home, rec)
		if n := sweepLaunchRecords(root, home, &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d", n)
		}
	})
	t.Run("keeps a record whose transcript directory can't be read", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root reads every directory")
		}
		root, home, rec := launchRecordFixture(t, old)
		dir := filepath.Dir(writeTranscript(t, root, home, rec))
		if err := os.Remove(sessionattach.TranscriptPath(home, filepath.Join(root, rec.InstanceName), rec.SessionID)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
		if n := sweepLaunchRecords(root, home, &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d", n)
		}
	})
	t.Run("keeps every record when the projects directory is missing", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, old)
		if err := os.RemoveAll(filepath.Join(home, ".claude")); err != nil {
			t.Fatal(err)
		}
		if n := sweepLaunchRecords(root, home, &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d", n)
		}
		if n := sweepLaunchRecords(root, "", &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d with no home", n)
		}
	})
	t.Run("keeps a record younger than the grace", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, now)
		if n := sweepLaunchRecords(root, home, &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d", n)
		}
	})
	t.Run("keeps a record for an agent whose transcripts it can't find", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, old)
		rec.Agent = "codex"
		if err := workspace.WriteLaunchRecord(root, rec); err != nil {
			t.Fatal(err)
		}
		if n := sweepLaunchRecords(root, home, &bytes.Buffer{}, now); n != 0 || !launchRecordKept(t, root, rec) {
			t.Errorf("removed %d", n)
		}
	})
	t.Run("warns once for each unreadable file and goes on", func(t *testing.T) {
		root, home, rec := launchRecordFixture(t, old)
		dir := workspace.LaunchDir(root)
		bad := map[string]string{
			"1b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d.json": "{not json",
			"not-an-id.json": "{}",
		}
		for name, body := range bad {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		escaping := rec
		escaping.DispatchID = "2b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d"
		escaping.InstanceName = ".."
		if err := workspace.WriteLaunchRecord(root, escaping); err != nil {
			t.Fatal(err)
		}
		var w bytes.Buffer
		if n := sweepLaunchRecords(root, home, &w, now); n != 1 || launchRecordKept(t, root, rec) {
			t.Errorf("removed %d, want the readable record removed", n)
		}
		out := w.String()
		for _, name := range []string{"1b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d.json", "not-an-id.json", escaping.DispatchID + ".json"} {
			if strings.Count(out, name) != 1 {
				t.Errorf("want one line naming %s, got:\n%s", name, out)
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("%s was not left in place: %v", name, err)
			}
		}
		if lines := strings.Count(out, "\n"); lines != 3 {
			t.Errorf("want 3 lines, got %d:\n%s", lines, out)
		}
	})
}

func TestReapWorkspace_SweepsLaunchRecords(t *testing.T) {
	root, home, rec := launchRecordFixture(t, time.Now().Add(-2*launchRecordGrace))
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })
	prevStderr := reapStderr
	reapStderr = &bytes.Buffer{}
	t.Cleanup(func() { reapStderr = prevStderr })
	if _, err := reapWorkspace(root, filepath.Join(home, ".claude", "jobs"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if launchRecordKept(t, root, rec) {
		t.Error("the reap kept a record whose transcript is gone")
	}
}
