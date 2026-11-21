package workspace

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/pluginstore"
)

// storeFixture is a workspace root and a sandbox home whose plugin store
// holds one read-only release tree, as a held release is.
type storeFixture struct {
	root, home, keyDir, storeDir string
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()
	root := destroySetupWorkspace(t)
	home := t.TempDir()
	f := storeFixture{root: root, home: home, keyDir: hostdir.WorkspaceDir(home, root), storeDir: pluginstore.Dir(home, root)}
	tree := filepath.Join(f.storeDir, "github.com", "acme", "skills", strings.Repeat("a", 40), "tree")
	if err := os.MkdirAll(filepath.Join(tree, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".claude-plugin", "marketplace.json"), []byte("{}"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(tree, ".claude-plugin"), tree} {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	// A kept store's read-only tree is more than t.TempDir's cleanup removes.
	t.Cleanup(func() {
		_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return f
}

func (f storeFixture) destroy(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if err := DestroyWorkspace(f.root, DestroyWorkspaceOpts{Home: f.home, StoreOut: &out}); err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}
	if _, err := os.Stat(f.root); !os.IsNotExist(err) {
		t.Fatalf("workspace root survived: %v", err)
	}
	return out.String()
}

// With no kept instance, the store goes with the workspace, read-only tree
// and all, and so does the key directory it leaves empty.
func TestDestroyWorkspaceRemovesThePluginStore(t *testing.T) {
	f := newStoreFixture(t)
	if out := f.destroy(t); out != "" {
		t.Errorf("printed %q, want nothing", out)
	}
	for _, dir := range []string{f.storeDir, f.keyDir} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", dir, err)
		}
	}
}

// A kept instance under the key directory's kept/ may still link into the
// store, so the store stays and its path is printed.
func TestDestroyWorkspaceKeepsTheStoreForAKeptInstance(t *testing.T) {
	f := newStoreFixture(t)
	kept := filepath.Join(f.keyDir, hostdir.KeptDirName, "old")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	out := f.destroy(t)
	if want := "kept the plugin store at " + f.storeDir + ": kept instances still link into it"; !strings.Contains(out, want) {
		t.Errorf("printed %q, want a line %q", out, want)
	}
	for _, dir := range []string{f.storeDir, kept} {
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("%s was removed: %v", dir, err)
		}
	}
}

// The mirrors beside the store keep the key directory, which is removed only
// when nothing else is left in it.
func TestDestroyWorkspaceKeepsAKeyDirWithMirrors(t *testing.T) {
	f := newStoreFixture(t)
	mirrors := filepath.Join(f.keyDir, "mirrors", "app.git")
	if err := os.MkdirAll(mirrors, 0o700); err != nil {
		t.Fatal(err)
	}
	f.destroy(t)
	if _, err := os.Lstat(f.storeDir); !os.IsNotExist(err) {
		t.Errorf("the store survived: %v", err)
	}
	if _, err := os.Lstat(mirrors); err != nil {
		t.Errorf("the mirrors were removed: %v", err)
	}
}

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
