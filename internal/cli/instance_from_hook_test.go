package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// testSessionID is a canonical UUID used across the from-hook tests. Its first
// 12 hex chars ("aabbccddeeff") are the instance-name suffix the SessionStart
// path derives.
const testSessionID = "aabbccdd-eeff-1122-3344-556677889900"

// setupHookWorkspace creates a workspace root with .niwa/workspace.toml and,
// when ephemeral is true, a root .niwa/instance.json carrying the
// EphemeralSessionMode flag. It returns the workspace root path. The root state
// file (instance.json at the workspace root) coexists with workspace.toml, so
// the root is NOT a destroyable instance -- mirroring how `niwa init` lands the
// mode flag.
func setupHookWorkspace(t *testing.T, ephemeral bool) string {
	t.Helper()
	root := t.TempDir()

	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := "[workspace]\nname = \"test-ws\"\n"
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if ephemeral {
		state := &workspace.InstanceState{
			SchemaVersion:        workspace.SchemaVersion,
			InstanceName:         "test-ws",
			Root:                 root,
			EphemeralSessionMode: true,
			Repos:                map[string]workspace.RepoState{},
		}
		if err := workspace.SaveState(root, state); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// writeJobState writes a fixture job-state file at <jobsDir>/<dirName>/state.json
// with the given sessionId and template. dirName is the directory the job state
// lives under (the session-id prefix in production).
func writeJobState(t *testing.T, jobsDir, dirName, sessionID, template string) {
	t.Helper()
	dir := filepath.Join(jobsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	js := jobState{SessionID: sessionID, Template: template}
	data, err := json.Marshal(js)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubProvision installs a fake provisioner that records its arguments and
// returns a result whose Path is a directory under workspaceRoot carrying a
// CLAUDE.md, without doing a real clone. It restores the original on cleanup.
func stubProvision(t *testing.T, claudeMD string) *struct {
	called       bool
	gotName      string
	gotWorkspace string
	result       provisionResult
} {
	t.Helper()
	rec := &struct {
		called       bool
		gotName      string
		gotWorkspace string
		result       provisionResult
	}{}
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(_ context.Context, workspaceRoot, _, namePrefix, sep string) (provisionResult, error) {
		rec.called = true
		rec.gotName = namePrefix
		rec.gotWorkspace = workspaceRoot
		name := "test-ws" + sep + namePrefix
		instanceDir := filepath.Join(workspaceRoot, name)
		if err := os.MkdirAll(instanceDir, 0o755); err != nil {
			return provisionResult{}, err
		}
		if claudeMD != "" {
			if err := os.WriteFile(filepath.Join(instanceDir, "CLAUDE.md"), []byte(claudeMD), 0o644); err != nil {
				return provisionResult{}, err
			}
		}
		rec.result = provisionResult{Name: name, Path: instanceDir}
		return rec.result, nil
	}
	t.Cleanup(func() { provisionInstanceFunc = prev })
	return rec
}

// runStart invokes runInstanceHookStart with the payload and jobsDir, capturing
// stdout and stderr.
func runStart(t *testing.T, payload instanceHookPayload, jobsDir string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	instanceFromHookCmd.SetOut(&outBuf)
	instanceFromHookCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		instanceFromHookCmd.SetOut(os.Stdout)
		instanceFromHookCmd.SetErr(os.Stderr)
	})
	runErr := runInstanceHookStart(instanceFromHookCmd, payload, jobsDir)
	return outBuf.String(), errBuf.String(), runErr
}

// --- Guard matrix ---

// TestSessionStart_ModeOff_NoOp: ephemeral mode is off, so even a genuine bg
// worker is a clean no-op (no provision, no output).
func TestSessionStart_ModeOff_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, false /* ephemeral */)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID, testSessionID, "bg")
	rec := stubProvision(t, "guidance")

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           root,
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if rec.called {
		t.Error("provisioner was called with ephemeral mode OFF; want no-op")
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty (no-op)", out)
	}
}

// TestSessionStart_NotBgTemplate_NoOp: ephemeral mode is on but the job state
// is an interactive session (template "claude"), so it is a no-op.
func TestSessionStart_NotBgTemplate_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID, testSessionID, "claude")
	rec := stubProvision(t, "guidance")

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           root,
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if rec.called {
		t.Error("provisioner was called for a non-bg session; want no-op")
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestSessionStart_NoJobState_NoOp: ephemeral mode is on but there is no job
// state for the session, so it is a no-op.
func TestSessionStart_NoJobState_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty
	rec := stubProvision(t, "guidance")

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           root,
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if rec.called {
		t.Error("provisioner was called with no job state; want no-op")
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestSessionStart_AlreadyInsideInstance_NoOp: the launch cwd already resolves
// inside a genuine niwa instance, so re-entrancy blocks provisioning.
func TestSessionStart_AlreadyInsideInstance_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID, testSessionID, "bg")
	rec := stubProvision(t, "guidance")

	// Create a genuine instance under the root and use it as the cwd.
	instanceDir := filepath.Join(root, "test-ws")
	if err := os.MkdirAll(instanceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := &workspace.InstanceState{
		SchemaVersion: workspace.SchemaVersion,
		InstanceName:  "test-ws",
		Root:          instanceDir,
		Repos:         map[string]workspace.RepoState{},
	}
	if err := workspace.SaveState(instanceDir, st); err != nil {
		t.Fatal(err)
	}

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           instanceDir,
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if rec.called {
		t.Error("provisioner was called from inside an instance; want re-entrancy no-op")
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

// TestSessionStart_PassingWorker_Provisions: all three guard conditions hold,
// so the session provisions, writes a mapping, and emits the injection JSON.
func TestSessionStart_PassingWorker_Provisions(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	// Use the session-id PREFIX as the job dir name to exercise prefix matching.
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	rec := stubProvision(t, "# instance guidance\n")

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName:  hookEventSessionStart,
		SessionID:      testSessionID,
		Cwd:            root,
		TranscriptPath: "/tmp/transcript.jsonl",
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if !rec.called {
		t.Fatal("provisioner was NOT called for a passing worker")
	}
	if rec.gotName != testSessionID[:sessionNamePrefixLen] {
		t.Errorf("name prefix = %q, want %q", rec.gotName, testSessionID[:sessionNamePrefixLen])
	}

	// Mapping written, ephemeral, pointing at the provisioned instance.
	m, err := workspace.ReadSessionMapping(root, testSessionID)
	if err != nil {
		t.Fatalf("ReadSessionMapping: %v", err)
	}
	if !m.Ephemeral {
		t.Error("mapping.Ephemeral = false, want true")
	}
	if m.InstancePath != rec.result.Path {
		t.Errorf("mapping.InstancePath = %q, want %q", m.InstancePath, rec.result.Path)
	}
	if m.TranscriptPath != "/tmp/transcript.jsonl" {
		t.Errorf("mapping.TranscriptPath = %q, want the hook transcript", m.TranscriptPath)
	}

	// Injection JSON shape.
	var inj sessionStartInjection
	if err := json.Unmarshal([]byte(out), &inj); err != nil {
		t.Fatalf("unmarshal injection JSON %q: %v", out, err)
	}
	if inj.HookSpecificOutput.HookEventName != hookEventSessionStart {
		t.Errorf("hookEventName = %q, want SessionStart", inj.HookSpecificOutput.HookEventName)
	}
	ctx := inj.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, rec.result.Path) {
		t.Errorf("additionalContext missing instance path %q", rec.result.Path)
	}
	if !strings.Contains(ctx, "cd "+rec.result.Path) {
		t.Errorf("additionalContext missing cd instruction for %q", rec.result.Path)
	}
	if !strings.Contains(ctx, "# instance guidance") {
		t.Errorf("additionalContext missing instance CLAUDE.md content")
	}
}

// TestBuildSessionStartInjection_NoClaudeMD: a missing instance CLAUDE.md is
// tolerated; the path + cd instruction still inject.
func TestBuildSessionStartInjection_NoClaudeMD(t *testing.T) {
	dir := t.TempDir() // no CLAUDE.md
	out, err := buildSessionStartInjection(dir)
	if err != nil {
		t.Fatalf("buildSessionStartInjection: %v", err)
	}
	var inj sessionStartInjection
	if err := json.Unmarshal(out, &inj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ctx := inj.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, dir) {
		t.Errorf("additionalContext missing instance path %q", dir)
	}
	if !strings.Contains(ctx, "cd "+dir) {
		t.Errorf("additionalContext missing cd instruction")
	}
}

// TestIsBackgroundWorker_PrefixMismatch: a job dir whose name is a prefix of the
// session id but whose inner sessionId does NOT match is rejected, so a
// colliding prefix is never mistaken for this session.
func TestIsBackgroundWorker_PrefixMismatch(t *testing.T) {
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], "ffffffff-0000-0000-0000-000000000000", "bg")
	if isBackgroundWorker(jobsDir, testSessionID) {
		t.Error("isBackgroundWorker = true for a sessionId mismatch; want false")
	}
}

// --- SessionEnd teardown ---

// stubDestroy installs a fake destroyer recording the path it was asked to
// destroy. It restores the original on cleanup.
func stubDestroy(t *testing.T) *struct {
	called  bool
	gotPath string
} {
	t.Helper()
	rec := &struct {
		called  bool
		gotPath string
	}{}
	prev := destroyInstanceFunc
	destroyInstanceFunc = func(path string) error {
		rec.called = true
		rec.gotPath = path
		return nil
	}
	t.Cleanup(func() { destroyInstanceFunc = prev })
	return rec
}

func runEnd(t *testing.T, payload instanceHookPayload) (stderr string, err error) {
	t.Helper()
	var errBuf bytes.Buffer
	instanceFromHookCmd.SetErr(&errBuf)
	t.Cleanup(func() { instanceFromHookCmd.SetErr(os.Stderr) })
	runErr := runInstanceHookEnd(instanceFromHookCmd, payload)
	return errBuf.String(), runErr
}

// TestSessionEnd_NeverDestroys: SessionEnd is a no-op (DESIGN Decision 6,
// revised -- delete-only teardown). Even an ephemeral mapping whose session is
// ending is left intact: the instance is NOT destroyed and the mapping is NOT
// deleted. SessionEnd fires on idle-suspend/resume, not uniquely on delete, so
// it must not tear down; the reaper owns teardown.
func TestSessionEnd_NeverDestroys(t *testing.T) {
	root := setupHookWorkspace(t, true)
	rec := stubDestroy(t)

	instancePath := filepath.Join(root, "test-ws-aabbccddeeff")
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "test-ws-aabbccddeeff",
		InstancePath: instancePath,
		Ephemeral:    true,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := runEnd(t, instanceHookPayload{
		HookEventName: hookEventSessionEnd,
		SessionID:     testSessionID,
		Cwd:           root,
	})
	if err != nil {
		t.Fatalf("runInstanceHookEnd: %v", err)
	}
	if rec.called {
		t.Errorf("destroyer was called on SessionEnd (path %q); want no-op", rec.gotPath)
	}
	// The mapping must survive: teardown is the reaper's job, not SessionEnd's.
	if _, err := workspace.ReadSessionMapping(root, testSessionID); err != nil {
		t.Errorf("mapping deleted on SessionEnd; want preserved (reaper owns teardown): %v", err)
	}
}

// TestSessionEnd_NoMapping_NoOp: a SessionEnd for a session with no mapping is a
// clean no-op (as is every SessionEnd now).
func TestSessionEnd_NoMapping_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, true)
	rec := stubDestroy(t)

	_, err := runEnd(t, instanceHookPayload{
		HookEventName: hookEventSessionEnd,
		SessionID:     testSessionID,
		Cwd:           root,
	})
	if err != nil {
		t.Fatalf("runInstanceHookEnd: %v", err)
	}
	if rec.called {
		t.Error("destroyer was called with no mapping; want clean no-op")
	}
}

// TestSessionEnd_NonEphemeralMapping_NotDestroyed: a mapping NOT marked
// ephemeral is likewise never destroyed -- SessionEnd destroys nothing at all.
func TestSessionEnd_NonEphemeralMapping_NotDestroyed(t *testing.T) {
	root := setupHookWorkspace(t, true)
	rec := stubDestroy(t)

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "keep-me",
		InstancePath: filepath.Join(root, "keep-me"),
		Ephemeral:    false,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := runEnd(t, instanceHookPayload{
		HookEventName: hookEventSessionEnd,
		SessionID:     testSessionID,
		Cwd:           root,
	})
	if err != nil {
		t.Fatalf("runInstanceHookEnd: %v", err)
	}
	if rec.called {
		t.Error("destroyer was called for a non-ephemeral mapping; want skip")
	}
	// The mapping must survive (it is not ours to delete).
	if _, err := workspace.ReadSessionMapping(root, testSessionID); err != nil {
		t.Error("non-ephemeral mapping was deleted; want preserved")
	}
}
