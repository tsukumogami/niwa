package cli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestCaptureWindowSignalRemovesTheFile: a SIGTERM while the window is open
// removes the capture-pending file and then ends niwa by that signal.
func TestCaptureWindowSignalRemovesTheFile(t *testing.T) {
	inst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(inst, workspace.StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	fired := make(chan *dispatchInterruptedError, 1)
	prevExit := dispatchInterruptExit
	dispatchInterruptExit = func(e *dispatchInterruptedError) { fired <- e }
	t.Cleanup(func() { dispatchInterruptExit = prevExit })

	closeWindow, err := openCaptureWindow(inst, showDispatchA, agent.AgentClaude)
	if err != nil {
		t.Fatal(err)
	}
	// The handler stays registered until the close, so the signal below is
	// caught and the test binary survives it.
	t.Cleanup(closeWindow)
	if _, err := os.Stat(workspace.CapturePendingPath(inst)); err != nil {
		t.Fatalf("no capture-pending file once the window opened: %v", err)
	}
	signalSelf(t, syscall.SIGTERM)
	select {
	case e := <-fired:
		if e.sig != syscall.SIGTERM {
			t.Errorf("ended by %v, want SIGTERM", e.sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("niwa did not end within 5s of the signal")
	}
	if _, err := os.Stat(workspace.CapturePendingPath(inst)); !os.IsNotExist(err) {
		t.Errorf("the capture-pending file survived the signal: %v", err)
	}
}

// TestDispatchCaptureWindow pins the capture-pending file's lifetime: it
// exists while capture polls, naming this dispatch process, and is gone once
// dispatch returns, whether capture found the session or not.
func TestDispatchCaptureWindow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		detach     bool
		captureErr error
	}{
		{"detached, captured", true, nil},
		{"foreground, captured", false, nil},
		{"foreground, capture failed and the instance kept", false, errors.New("capture timed out")},
		{"detached, capture failed and the instance rolled back", true, errors.New("capture timed out")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := foregroundRunnerAgent(t)
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)

			var instance string
			var during workspace.CapturePending
			var duringFound bool
			prev := dispatchCapture
			dispatchCapture = func(_ agentplan.SessionRecords, _, instanceDir string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
				instance = instanceDir
				var err error
				during, duringFound, err = workspace.ReadCapturePending(instanceDir)
				if err != nil {
					t.Errorf("reading the capture-pending file during capture: %v", err)
				}
				if tc.captureErr != nil {
					return "", "", tc.captureErr
				}
				return dispatchTestSessionID, dispatchTestShortID, nil
			}
			t.Cleanup(func() { dispatchCapture = prev })

			dispatchHarness = string(ag)
			dispatchDetach = tc.detach
			t.Setenv("NIWA_DISPATCH_HARNESS", "")

			start := time.Now()
			_, _, err := runDispatchCmd(t, "do a thing")
			if tc.captureErr == nil && err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if !duringFound {
				t.Fatal("no capture-pending file while capture ran")
			}
			if during.PID != os.Getpid() || during.Agent != string(ag) {
				t.Errorf("the file names pid %d agent %q, want this process %d and %q", during.PID, during.Agent, os.Getpid(), ag)
			}
			if during.LaunchedAt.Before(start.Add(-time.Second)) || during.LaunchedAt.After(time.Now()) {
				t.Errorf("launch time %v is outside the dispatch", during.LaunchedAt)
			}
			if _, err := os.Stat(workspace.CapturePendingPath(instance)); !os.IsNotExist(err) {
				t.Errorf("the capture-pending file outlived dispatch: %v", err)
			}
		})
	}
}
