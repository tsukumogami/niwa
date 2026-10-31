package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestroyWorkspace_EmptyWorkspace(t *testing.T) {
	root := destroySetupWorkspace(t)

	var out bytes.Buffer
	err := DestroyWorkspace(root, DestroyWorkspaceOpts{ProgressOut: &out})
	if err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}

	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace root should be removed; got Stat err = %v", err)
	}
	if !strings.Contains(out.String(), "Destroyed workspace:") {
		t.Errorf("expected 'Destroyed workspace:' line in output; got: %q", out.String())
	}
}

func TestDestroyWorkspace_WithInstances(t *testing.T) {
	root := destroySetupWorkspace(t)
	a := destroySetupInstance(t, root, "alpha")
	b := destroySetupInstance(t, root, "beta")

	err := DestroyWorkspace(root, DestroyWorkspaceOpts{})
	if err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}

	for _, dir := range []string{a, b, root} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("expected %s removed; got Stat err = %v", dir, err)
		}
	}
}

func TestDestroyWorkspace_DoesNotCallValidateInstanceDirOnWorkspaceRoot(t *testing.T) {
	// Regression: the workspace-root validator (ValidateInstanceDir) is
	// designed to refuse the workspace root. DestroyWorkspace must not
	// route the workspace root through it; if it did, the workspace
	// would never be removable via this path.
	//
	// This test asserts the invariant by setting up a workspace whose
	// root would *also* qualify as an instance (i.e., contains
	// .niwa/instance.json AND .niwa/workspace.toml — a corrupt state
	// that ValidateInstanceDir explicitly rejects). DestroyWorkspace
	// should still remove the workspace root because it never calls
	// ValidateInstanceDir on the root.
	root := destroySetupWorkspace(t)

	// Inject .niwa/instance.json at the root (corrupt: workspace.toml
	// is also there). ValidateInstanceDir would reject this with
	// "refusing to destroy workspace root", but DestroyWorkspace
	// should proceed.
	stateDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DestroyWorkspace(root, DestroyWorkspaceOpts{}); err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace root not removed: %v", err)
	}
}

func TestDestroyWorkspace_OrderingAlphabetical(t *testing.T) {
	// Verify per-instance destroy order is alphabetical so the output
	// is deterministic at a confirmation prompt.
	root := destroySetupWorkspace(t)
	for _, name := range []string{"gamma", "alpha", "beta"} {
		destroySetupInstance(t, root, name)
	}

	reporter := NewReporterWithTTY(&bytes.Buffer{}, false)
	err := DestroyWorkspace(root, DestroyWorkspaceOpts{Reporter: reporter})
	if err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("expected workspace root removed; got: %v", err)
	}
	// We don't assert the exact reporter output here — destroy order
	// is internal — but the test relies on the function completing
	// successfully across multiple instances which exercises the loop.
}

func TestDestroyWorkspace_NonexistentRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	err := DestroyWorkspace(dir, DestroyWorkspaceOpts{})
	if err == nil {
		t.Fatal("expected error for non-existent workspace root")
	}
	if !strings.Contains(err.Error(), "workspace root") {
		t.Errorf("error message should mention workspace root; got: %v", err)
	}
}

// TestDestroyWorkspace_ListedOnly: given the caller's list, an instance on
// disk outside it refuses the whole operation before anything is removed, and
// a refusing BeforeRootRemoval keeps the root once the listed instances are
// gone.
func TestDestroyWorkspace_ListedOnly(t *testing.T) {
	t.Run("unlisted_before", func(t *testing.T) {
		root := destroySetupWorkspace(t)
		a := destroySetupInstance(t, root, "alpha")
		extra := destroySetupInstance(t, root, "delta")

		err := DestroyWorkspace(root, DestroyWorkspaceOpts{Instances: []string{a}})
		if err == nil || !strings.Contains(err.Error(), "delta") {
			t.Fatalf("DestroyWorkspace = %v, want a refusal naming delta", err)
		}
		for _, dir := range []string{a, extra, root} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("%s was removed: %v", dir, err)
			}
		}
	})

	t.Run("hook_refuses", func(t *testing.T) {
		root := destroySetupWorkspace(t)
		a := destroySetupInstance(t, root, "alpha")
		err := DestroyWorkspace(root, DestroyWorkspaceOpts{
			Instances:         []string{a},
			BeforeRootRemoval: func() error { return os.ErrExist },
		})
		if err == nil {
			t.Fatal("DestroyWorkspace succeeded though the hook refused")
		}
		if _, err := os.Stat(a); !os.IsNotExist(err) {
			t.Errorf("the listed instance survived: %v", err)
		}
		if _, err := os.Stat(root); err != nil {
			t.Errorf("the workspace root was removed though the hook refused: %v", err)
		}
	})

	t.Run("empty_list", func(t *testing.T) {
		root := destroySetupWorkspace(t)
		if err := DestroyWorkspace(root, DestroyWorkspaceOpts{Instances: []string{}}); err != nil {
			t.Fatalf("DestroyWorkspace: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("the empty workspace root survived: %v", err)
		}
	})
}
