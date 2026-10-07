package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setDestroyFlags sets the destroy flag globals for one test and restores
// them afterwards.
func setDestroyFlags(t *testing.T, force, wholeWorkspace bool, confirm string) {
	t.Helper()
	prevForce, prevWorkspace, prevConfirm := destroyForce, destroyWorkspace, destroyConfirm
	destroyForce, destroyWorkspace, destroyConfirm = force, wholeWorkspace, confirm
	t.Cleanup(func() {
		destroyForce, destroyWorkspace, destroyConfirm = prevForce, prevWorkspace, prevConfirm
	})
}

func quietDestroyCmd() *bytes.Buffer {
	var stderr bytes.Buffer
	destroyCmd.SetOut(&bytes.Buffer{})
	destroyCmd.SetErr(&stderr)
	return &stderr
}

func assertInstancesExist(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, n, ".niwa", "instance.json")); err != nil {
			t.Errorf("instance %s should still exist: %v", n, err)
		}
	}
}

// TestRunDestroy_BareForceAtRootRefuses is issue #342: `niwa destroy --force`
// at the root with no instance name wiped every instance. It must refuse,
// point at the explicit form, and leave everything in place, including an
// empty workspace, which it used to delete too.
func TestRunDestroy_BareForceAtRootRefuses(t *testing.T) {
	for _, names := range [][]string{{"alpha", "beta"}, {"alpha"}, nil} {
		root := destroyTestSetup(t, names)
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, true, false, "")

		err := runDestroy(destroyCmd, nil)
		if err == nil {
			t.Fatalf("instances=%v: bare --force at the root should refuse", names)
		}
		if !strings.Contains(err.Error(), "niwa destroy --workspace") || !strings.Contains(err.Error(), "niwa destroy --force <name>") {
			t.Errorf("instances=%v: error should name both explicit forms; got: %v", names, err)
		}
		assertInstancesExist(t, root, names...)
		if _, err := os.Stat(filepath.Join(root, ".niwa", "workspace.toml")); err != nil {
			t.Errorf("instances=%v: workspace should still exist: %v", names, err)
		}
	}
}

// TestRunDestroy_StrayDirRefuses is issue #344 for destroy: from a directory
// under the root that is not an instance, destroy took the root's mode.
func TestRunDestroy_StrayDirRefuses(t *testing.T) {
	cases := []struct {
		name        string
		force, wipe bool
		confirm     string
		args        []string
	}{
		{name: "no flags"},
		{name: "force", force: true},
		{name: "force and name", force: true, args: []string{"alpha"}},
		{name: "workspace confirmed", wipe: true, confirm: "testws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			stray := filepath.Join(root, "testws+-0000beef", "public")
			if err := os.MkdirAll(stray, 0o755); err != nil {
				t.Fatal(err)
			}
			chdirTo(t, stray)
			quietDestroyCmd()
			setDestroyFlags(t, tc.force, tc.wipe, tc.confirm)

			err := runDestroy(destroyCmd, tc.args)
			if err == nil || !strings.Contains(err.Error(), "not an instance, a worktree, or the workspace root") {
				t.Fatalf("expected the below-root refusal; got: %v", err)
			}
			assertInstancesExist(t, root, "alpha", "beta")
		})
	}
}

// TestRunDestroy_WorkspaceWithoutTerminalRefuses: the explicit form still
// needs the workspace name, and without a terminal only --confirm supplies it.
func TestRunDestroy_WorkspaceWithoutTerminalRefuses(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "")

	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--confirm testws") {
		t.Fatalf("expected a non-TTY refusal naming --confirm; got: %v", err)
	}
	assertInstancesExist(t, root, "alpha", "beta")
}

// --force does not stand in for the confirmation.
func TestRunDestroy_WorkspaceForceStillNeedsConfirmation(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, true, true, "")

	if err := runDestroy(destroyCmd, nil); err == nil {
		t.Fatal("--workspace --force without a terminal or --confirm should refuse")
	}
	assertInstancesExist(t, root, "alpha")
}

func TestRunDestroy_WorkspaceConfirmMismatchRefuses(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "otherws")

	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected a mismatch refusal; got: %v", err)
	}
	assertInstancesExist(t, root, "alpha")
}

// TestRunDestroy_WorkspaceConfirmNearMissRefuses: --confirm must equal the
// workspace name exactly. A prefix, an extension, a sibling's name and a case
// or whitespace variant all refuse, so a prefix or case-folding comparison
// can't creep in.
func TestRunDestroy_WorkspaceConfirmNearMissRefuses(t *testing.T) {
	for _, confirm := range []string{"testw", "testws-2", "testwsx", "TESTWS", " testws", "testws "} {
		root := destroyTestSetup(t, []string{"alpha"})
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, true, confirm)

		err := runDestroy(destroyCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("--confirm %q: expected a mismatch refusal; got: %v", confirm, err)
		}
		assertInstancesExist(t, root, "alpha")
	}
}

// TestRunDestroy_WorkspaceConfirmMismatchRefusedBeforeScan: a wrong --confirm
// can never succeed, so it is refused before the scan reaches every
// instance's remotes. The banner and listing print only after the scan.
func TestRunDestroy_WorkspaceConfirmMismatchRefusedBeforeScan(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws-2")

	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected a mismatch refusal; got: %v", err)
	}
	if strings.Contains(stderr.String(), "This destroys") {
		t.Errorf("the refusal should come before the scan and its listing; stderr: %q", stderr.String())
	}
}

func TestRunDestroy_WorkspaceConfirmedWipes(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)
	t.Cleanup(func() { _ = os.Chdir(filepath.Dir(root)) })
	stderr := quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")

	if err := runDestroy(destroyCmd, nil); err != nil {
		t.Fatalf("runDestroy --workspace --confirm testws: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace root should be removed; got: %v", err)
	}
	if !strings.Contains(stderr.String(), "This destroys 2 instance(s)") {
		t.Errorf("expected the count of instances on stderr; got: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "Type \"") {
		t.Errorf("no prompt line should print when --confirm is given; got: %q", stderr.String())
	}
}

func TestRunDestroy_WorkspaceFlagMisuseRefuses(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})

	t.Run("with a name", func(t *testing.T) {
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, true, "testws")
		if err := runDestroy(destroyCmd, []string{"alpha"}); err == nil || !strings.Contains(err.Error(), "takes no instance name") {
			t.Fatalf("got: %v", err)
		}
	})
	t.Run("inside an instance", func(t *testing.T) {
		chdirTo(t, filepath.Join(root, "alpha"))
		quietDestroyCmd()
		setDestroyFlags(t, false, true, "testws")
		if err := runDestroy(destroyCmd, nil); err == nil || !strings.Contains(err.Error(), "only valid at the workspace root") {
			t.Fatalf("got: %v", err)
		}
	})
	t.Run("confirm without workspace", func(t *testing.T) {
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "testws")
		if err := runDestroy(destroyCmd, nil); err == nil || !strings.Contains(err.Error(), "--confirm only applies to --workspace") {
			t.Fatalf("got: %v", err)
		}
	})
	assertInstancesExist(t, root, "alpha", "beta")
}
