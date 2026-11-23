package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// builtMirrorFixture is a mirrorFixture whose mirror the pipeline's check
// has built.
func builtMirrorFixture(t *testing.T) mirrorFixture {
	t.Helper()
	f := newMirrorFixture(t)
	var stderr bytes.Buffer
	f.applier(&stderr).ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")
	if _, err := os.Readlink(mirror.StablePath(f.home, f.root, "app")); err != nil {
		t.Fatalf("the mirror wasn't built: %v\n%s", err, stderr.String())
	}
	return f
}

func (f mirrorFixture) destroyRoot(t *testing.T) string {
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

// With no kept instance, the mirrors go with the workspace, and so does the
// key directory they leave empty.
func TestDestroyWorkspaceRemovesUnborrowedMirrors(t *testing.T) {
	f := builtMirrorFixture(t)
	keyDir := hostdir.WorkspaceDir(f.home, f.root)
	if out := f.destroyRoot(t); out != "" {
		t.Errorf("printed %q, want nothing", out)
	}
	for _, dir := range []string{filepath.Join(keyDir, "mirrors"), keyDir} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("%s survived: %v", dir, err)
		}
	}
}

// A kept instance whose clone borrows from the mirror keeps it, and the
// destroy says where and why.
func TestDestroyWorkspaceKeepsAMirrorAKeptInstanceBorrows(t *testing.T) {
	f := builtMirrorFixture(t)
	stable := mirror.StablePath(f.home, f.root, "app")
	target, err := os.Readlink(stable)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(hostdir.WorkspaceDir(f.home, f.root), hostdir.KeptDirName, "old", "tools", "app")
	info := filepath.Join(kept, ".git", "objects", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(stable+"/objects\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := f.destroyRoot(t)
	if want := "kept the mirror at " + stable + ": a kept instance borrows from it"; !strings.Contains(out, want) {
		t.Errorf("printed %q, want a line %q", out, want)
	}
	if got, err := os.Readlink(stable); err != nil || got != target {
		t.Fatalf("the stable link = %q (%v), want it left naming %q", got, err, target)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(stable), target, "objects")); err != nil {
		t.Fatalf("the store was removed: %v", err)
	}
}
