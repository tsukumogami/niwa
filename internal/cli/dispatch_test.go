package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchTestSessionID is a canonical UUID a fake capture returns.
const dispatchTestSessionID = "abcdef12-3456-7890-abcd-ef1234567890"

// setupDispatchWorkspace creates a workspace root with .niwa/workspace.toml so
// ClassifyCwd resolves it to CwdAtWorkspaceRoot. It returns the root path.
func setupDispatchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// t.TempDir can hand back a symlinked path (e.g. /var -> /private/var on
	// macOS, or a symlinked TMPDIR on Linux). Resolve it so the workspace root
	// ClassifyCwd derives matches the cwd we chdir into.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := "[workspace]\nname = \"test-ws\"\n"
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// chdir changes into dir for the duration of the test and restores the previous
// cwd on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// dispatchFakes captures the state the dispatch seams record across a run.
type dispatchFakes struct {
	provisionCalled int
	launchCalled    int
	captureCalled   int
	attachCalled    int
	destroyCalled   int
	attachedID      string
	destroyedPath   string
	instancePath    string
}

// installDispatchFakes wires every dispatch seam to a fake and resets the
// command flags to their zero values, restoring all originals on cleanup. The
// returned struct records calls. By default: lookClaude succeeds, provision
// creates a real temp instance dir under workspaceRoot, launch succeeds,
// capture returns dispatchTestSessionID, attach succeeds.
func installDispatchFakes(t *testing.T, workspaceRoot string) *dispatchFakes {
	t.Helper()
	f := &dispatchFakes{}

	prevLook := lookClaude
	prevProvision := provisionInstanceFunc
	prevLaunch := dispatchLaunch
	prevCapture := dispatchCapture
	prevAttach := dispatchAttach
	prevDestroy := destroyInstanceFunc
	prevLabel := dispatchLabel
	prevModel := dispatchModel
	prevPerm := dispatchPermissionMode
	prevAgent := dispatchAgent
	prevDetach := dispatchDetach

	dispatchLabel = ""
	dispatchModel = ""
	dispatchPermissionMode = ""
	dispatchAgent = ""
	dispatchDetach = false

	lookClaude = func() (string, error) { return "/usr/bin/claude", nil }

	provisionInstanceFunc = func(_ context.Context, root, _, namePrefix string) (provisionResult, error) {
		f.provisionCalled++
		dir := filepath.Join(root, "test-ws-"+namePrefix)
		if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		f.instancePath = dir
		return provisionResult{Name: "test-ws-" + namePrefix, Path: dir}, nil
	}

	dispatchLaunch = func(_ context.Context, _, _ string, _ []string) error {
		f.launchCalled++
		return nil
	}

	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, error) {
		f.captureCalled++
		return dispatchTestSessionID, nil
	}

	dispatchAttach = func(id string) error {
		f.attachCalled++
		f.attachedID = id
		return nil
	}

	destroyInstanceFunc = func(path string) error {
		f.destroyCalled++
		f.destroyedPath = path
		return nil
	}

	t.Cleanup(func() {
		lookClaude = prevLook
		provisionInstanceFunc = prevProvision
		dispatchLaunch = prevLaunch
		dispatchCapture = prevCapture
		dispatchAttach = prevAttach
		destroyInstanceFunc = prevDestroy
		dispatchLabel = prevLabel
		dispatchModel = prevModel
		dispatchPermissionMode = prevPerm
		dispatchAgent = prevAgent
		dispatchDetach = prevDetach
	})

	return f
}

// runDispatchCmd invokes runDispatch with the given prompt, capturing stdout
// and stderr. It uses a fresh cobra.Command so the command's output streams are
// isolated.
func runDispatchCmd(t *testing.T, prompt string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	runErr := runDispatch(cmd, []string{prompt})
	return outBuf.String(), errBuf.String(), runErr
}

func TestDispatch_OutsideWorkspace_Errors(t *testing.T) {
	outside := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(outside); err == nil {
		outside = resolved
	}
	chdir(t, outside)
	f := installDispatchFakes(t, outside)

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error outside a workspace, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision must not be called outside a workspace; called %d times", f.provisionCalled)
	}
}

func TestDispatch_ClaudeNotOnPath_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	lookClaude = func() (string, error) { return "", errors.New("not found") }

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error when claude is absent, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision must not be called when claude is absent; called %d", f.provisionCalled)
	}
}

func TestDispatch_EmptyPrompt_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	_, _, err := runDispatchCmd(t, "")
	if err == nil {
		t.Fatal("expected an error for an empty prompt, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("nothing must be created for an empty prompt; provision called %d", f.provisionCalled)
	}
}

func TestDispatch_HappyPath_WritesMappingAndAttaches(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchLabel = "my-task"

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mapping written with the expected provenance.
	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading mapping: %v", err)
	}
	if !m.Ephemeral {
		t.Error("mapping must be ephemeral")
	}
	if m.Origin != "dispatch" {
		t.Errorf("Origin = %q, want dispatch", m.Origin)
	}
	if m.Label != "my-task" {
		t.Errorf("Label = %q, want my-task", m.Label)
	}
	if m.InstancePath != f.instancePath {
		t.Errorf("InstancePath = %q, want %q", m.InstancePath, f.instancePath)
	}

	// Pending-marker removed after the durable mapping.
	if _, statErr := os.Stat(filepath.Join(f.instancePath, dispatchPendingMarker)); !os.IsNotExist(statErr) {
		t.Errorf("pending-marker should be removed on success; stat err = %v", statErr)
	}

	// Attach called exactly once with the captured id.
	if f.attachCalled != 1 {
		t.Errorf("attach called %d times, want 1", f.attachCalled)
	}
	if f.attachedID != dispatchTestSessionID {
		t.Errorf("attached id = %q, want %q", f.attachedID, dispatchTestSessionID)
	}
	if f.destroyCalled != 0 {
		t.Errorf("instance must not be destroyed on success; destroy called %d", f.destroyCalled)
	}
	if !bytes.Contains([]byte(stdout), []byte(dispatchTestSessionID)) {
		t.Errorf("stdout should print the session id; got %q", stdout)
	}
}

func TestDispatch_Detach_SkipsAttach(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.attachCalled != 0 {
		t.Errorf("--detach must skip attach; attach called %d", f.attachCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr != nil {
		t.Errorf("mapping must still be written under --detach: %v", mErr)
	}
}

func TestDispatch_AttachFailure_NonFatal(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchAttach = func(id string) error {
		f.attachCalled++
		f.attachedID = id
		return errors.New("session already exited")
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("attach failure must be non-fatal; got err %v", err)
	}
	if f.destroyCalled != 0 {
		t.Errorf("attach failure must not roll back; destroy called %d", f.destroyCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr != nil {
		t.Errorf("mapping must survive an attach failure: %v", mErr)
	}
	if _, statErr := os.Stat(filepath.Join(f.instancePath, dispatchPendingMarker)); !os.IsNotExist(statErr) {
		t.Errorf("marker must still be gone after attach failure; stat err = %v", statErr)
	}
	if !bytes.Contains([]byte(stderr), []byte("warning")) {
		t.Errorf("expected a non-fatal warning on stderr; got %q", stderr)
	}
}

func TestDispatch_Rollback_LaunchFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchLaunch = func(_ context.Context, _, _ string, _ []string) error {
		f.launchCalled++
		return errors.New("launch boom")
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on launch failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("launch failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
	if f.destroyedPath != f.instancePath {
		t.Errorf("destroyed path = %q, want %q", f.destroyedPath, f.instancePath)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr == nil {
		t.Error("no mapping must remain after a launch-failure rollback")
	}
}

func TestDispatch_Rollback_CaptureFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, error) {
		f.captureCalled++
		return "", errors.New("capture timeout")
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on capture failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("capture failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr == nil {
		t.Error("no mapping must remain after a capture-failure rollback")
	}
}

func TestDispatch_Rollback_MappingWriteFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	// Force a mapping-write failure by having capture return an invalid id;
	// WriteSessionMapping rejects a non-UUID session id without writing.
	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, error) {
		f.captureCalled++
		return "not-a-uuid", nil
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on mapping-write failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("mapping-write failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
}

func TestDispatch_SelfDispatch_ResolvesEnclosingWorkspaceRoot(t *testing.T) {
	root := setupDispatchWorkspace(t)
	// Create a real instance under the root and chdir inside it, so ClassifyCwd
	// returns CwdInsideInstance whose WorkspaceRoot is the enclosing root.
	instanceDir := makeReapInstance(t, root, "test-ws-existing")
	chdir(t, instanceDir)
	f := installDispatchFakes(t, root)

	var gotRoot string
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix string) (provisionResult, error) {
		gotRoot = r
		return prev(ctx, r, cwd, namePrefix)
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRoot != root {
		t.Errorf("provision invoked with root %q, want enclosing workspace root %q", gotRoot, root)
	}
	if f.provisionCalled != 1 {
		t.Errorf("provision called %d times, want 1", f.provisionCalled)
	}
}

func TestDispatch_OverLongPrompt_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	big := bytes.Repeat([]byte("a"), maxPromptBytes+1)
	_, _, err := runDispatchCmd(t, string(big))
	if err == nil {
		t.Fatal("expected an error for an over-limit prompt")
	}
	if f.provisionCalled != 0 {
		t.Errorf("nothing must be created for an over-limit prompt; provision called %d", f.provisionCalled)
	}
}

func TestDispatch_PassthroughFlags_DiscreteArgv(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	installDispatchFakes(t, root)
	dispatchModel = "sonnet"
	dispatchPermissionMode = "acceptEdits"
	dispatchAgent = "reviewer"
	dispatchDetach = true

	var gotPass []string
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string) error {
		gotPass = passthrough
		return nil
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--model", "sonnet", "--permission-mode", "acceptEdits", "--agent", "reviewer"}
	if len(gotPass) != len(want) {
		t.Fatalf("passthrough = %v, want %v", gotPass, want)
	}
	for i := range want {
		if gotPass[i] != want[i] {
			t.Fatalf("passthrough[%d] = %q, want %q (full %v)", i, gotPass[i], want[i], gotPass)
		}
	}
}
