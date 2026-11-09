//go:build unix

package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// runApplyAtRootCapturing drives a cascading runApply with cwd at the given
// workspace root, like runApplyAtRoot, and also returns what the command wrote
// to its stderr.
func runApplyAtRootCapturing(t *testing.T, workspaceRoot string) (error, string) {
	t.Helper()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	savedWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(savedWD) })
	if err := os.Chdir(workspaceRoot); err != nil {
		t.Fatal(err)
	}

	savedCascade := applyNoCascade
	t.Cleanup(func() { applyNoCascade = savedCascade })
	applyNoCascade = false

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetErr(&stderr)
	runErr := runApply(cmd, nil)
	return runErr, stderr.String()
}

// addFixtureInstance adds another enumerable instance to a root fixture.
func addFixtureInstance(t *testing.T, workspaceRoot, name string) string {
	t.Helper()
	dir := filepath.Join(workspaceRoot, name)
	st := &workspace.InstanceState{
		SchemaVersion: 1,
		InstanceName:  name,
		Root:          dir,
	}
	if err := workspace.SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunApply_RootScopeSkipsInstanceRemovedMidRun covers an instance that is
// removed after the root apply enumerated it but before the loop reaches it
// (#485). Nothing is left to apply to it, so the run warns and exits cleanly
// instead of failing on ENOENT for the removed instance's .gitignore.
//
// The removal is staged deterministically. inst-1's .gitignore is a FIFO, so
// inst-1's apply blocks reading it. Opening the FIFO for writing returns only
// once that read has started, which is after enumeration listed inst-2. The
// test then removes inst-2 and writes every pattern the apply wants, so inst-1
// has nothing to append and converges normally.
func TestRunApply_RootScopeSkipsInstanceRemovedMidRun(t *testing.T) {
	root, first := setupRootScopeFixture(t)
	second := addFixtureInstance(t, root, "inst-2")

	fifo := filepath.Join(first, ".gitignore")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("creating FIFO: %v", err)
	}
	patterns := strings.Join(append([]string{"*.local*"}, agentplan.InstanceExcludePatterns()...), "\n") + "\n"

	opened := make(chan struct{}, 1)
	staged := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			staged <- err
			return
		}
		opened <- struct{}{}
		rmErr := os.RemoveAll(second)
		_, wErr := f.WriteString(patterns)
		staged <- errors.Join(rmErr, wErr, f.Close())
	}()

	runErr, stderr := runApplyAtRootCapturing(t, root)

	select {
	case <-opened:
	default:
		// The apply never read inst-1's .gitignore. Release the writer so the
		// goroutine ends, then fail: the removal was never staged.
		r, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer r.Close()
		}
		<-staged
		t.Fatalf("apply never read %s, so inst-2 was not removed mid-run; runApply returned %v", fifo, runErr)
	}
	if err := <-staged; err != nil {
		t.Fatalf("staging the removal: %v", err)
	}

	if runErr != nil {
		t.Fatalf("runApply returned an error for an instance removed mid-run: %v", runErr)
	}
	if !strings.Contains(stderr, second) || !strings.Contains(stderr, "removed") {
		t.Errorf("stderr should warn that %s was removed and skipped, got:\n%s", second, stderr)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Errorf("the removed instance should stay removed, stat returned %v", err)
	}

	st, err := workspace.LoadState(first)
	if err != nil {
		t.Fatalf("loading inst-1 state: %v", err)
	}
	if st.LastApplied.IsZero() {
		t.Error("inst-1 should still converge when another instance is removed, LastApplied is zero")
	}
}

// TestRunApply_RootScopeFailsWhenExistingInstanceFails pins the other half of
// #485: a failure on an instance that still exists keeps failing the run, even
// when it is the same ENOENT on .gitignore a removed instance would produce.
// inst-1's .gitignore is a dangling symlink into a missing directory, so
// writing it fails with ENOENT while inst-1 is still there.
func TestRunApply_RootScopeFailsWhenExistingInstanceFails(t *testing.T) {
	root, first := setupRootScopeFixture(t)

	if err := os.Symlink(filepath.Join(first, "missing", "gitignore"), filepath.Join(first, ".gitignore")); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}

	runErr, stderr := runApplyAtRootCapturing(t, root)
	if runErr == nil {
		t.Fatalf("runApply should fail for an instance that still exists, stderr:\n%s", stderr)
	}
	if !strings.Contains(runErr.Error(), first) {
		t.Errorf("the error should name %s, got: %v", first, runErr)
	}
	if strings.Contains(stderr, "removed during apply") {
		t.Errorf("an instance that still exists must not be reported as removed, stderr:\n%s", stderr)
	}
}
