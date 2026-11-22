package hostdir_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
	"github.com/tsukumogami/niwa/internal/pluginstore"
)

func TestWorkspaceKeyIsCanonical(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	key := hostdir.WorkspaceKey(root)
	if !regexp.MustCompile(`^w-[0-9a-f]{32}$`).MatchString(key) {
		t.Fatalf("WorkspaceKey = %q", key)
	}
	if hostdir.WorkspaceKey(link) != key || hostdir.WorkspaceKey(root+"/") != key {
		t.Fatal("a symlinked root or a trailing slash got another key")
	}
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if hostdir.WorkspaceKey(other) == key {
		t.Fatal("two roots share a key")
	}
	if got, want := hostdir.WorkspaceDir("/h", root), filepath.Join("/h", ".niwa", key); got != want {
		t.Fatalf("WorkspaceDir = %s, want %s", got, want)
	}
}

// Both host stores resolve one key directory for one root: the mirrors sit
// beside the plugin releases. A second definition of the key in either store
// fails here.
func TestBothStoresShareTheKeyDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	mirrorKeyDir := filepath.Dir(filepath.Dir(mirror.StablePath(home, root, "app")))
	pluginKeyDir := filepath.Dir(pluginstore.Dir(home, root))
	if mirrorKeyDir != pluginKeyDir {
		t.Fatalf("mirror key dir %s != plugin key dir %s", mirrorKeyDir, pluginKeyDir)
	}
	if mirrorKeyDir != hostdir.WorkspaceDir(home, root) {
		t.Fatalf("key dir %s is not hostdir's %s", mirrorKeyDir, hostdir.WorkspaceDir(home, root))
	}
	if mirror.WorkspaceKey(root) != hostdir.WorkspaceKey(root) {
		t.Fatal("the mirror's key differs from hostdir's")
	}
}

func TestCreatePrivateDirLeavesAnExistingModeAlone(t *testing.T) {
	base := t.TempDir()
	fresh := filepath.Join(base, "fresh")
	if err := hostdir.CreatePrivateDir(fresh); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(fresh); info.Mode().Perm() != 0o700 {
		t.Fatalf("new dir mode %o, want 700", info.Mode().Perm())
	}
	narrowed := filepath.Join(base, "narrowed")
	if err := os.Mkdir(narrowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(narrowed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(narrowed, 0o755) })
	if err := hostdir.CreatePrivateDir(narrowed); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(narrowed); info.Mode().Perm() != 0o555 {
		t.Fatalf("existing dir mode changed to %o", info.Mode().Perm())
	}
	if err := hostdir.EnsurePrivateDir(narrowed); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(narrowed); info.Mode().Perm() != 0o700 {
		t.Fatalf("EnsurePrivateDir left mode %o", info.Mode().Perm())
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(fresh, link); err != nil {
		t.Fatal(err)
	}
	if err := hostdir.CreatePrivateDir(link); err == nil {
		t.Fatal("CreatePrivateDir accepted a symlink")
	}
}

func TestInstanceDirs(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "ws")
	mk := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(root, ".niwa"))
	mk(filepath.Join(root, "live", ".niwa"))
	if err := os.WriteFile(filepath.Join(root, "live", ".niwa", "instance.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mk(filepath.Join(root, "half"))
	outside := filepath.Join(base, "outside")
	mk(outside)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := hostdir.InstanceDirs(home, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "half"), filepath.Join(root, "live")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InstanceDirs = %v, want %v", got, want)
	}

	kept := filepath.Join(hostdir.WorkspaceDir(home, root), hostdir.KeptDirName)
	mk(filepath.Join(kept, "old", ".niwa"))
	mk(filepath.Join(kept, "half-kept"))
	got, err = hostdir.InstanceDirs(home, root)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{filepath.Join(kept, "half-kept"), filepath.Join(kept, "old"), filepath.Join(root, "half"), filepath.Join(root, "live")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InstanceDirs with kept = %v, want %v", got, want)
	}

	if err := os.Chmod(kept, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(kept, 0o755) })
	if os.Getuid() != 0 {
		if _, err := hostdir.InstanceDirs(home, root); err == nil {
			t.Fatal("an unreadable kept directory was not an error")
		}
	}
	if _, err := hostdir.InstanceDirs(home, filepath.Join(base, "missing")); err == nil {
		t.Fatal("a missing root was not an error")
	}
}
