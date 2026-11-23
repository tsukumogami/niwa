package workspace

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// offRepoConfig turns borrowing off for app, which borrowing would otherwise
// cover by default.
func offRepoConfig() *config.WorkspaceConfig {
	off := false
	return &config.WorkspaceConfig{Repos: map[string]config.RepoOverride{"app": {ShareGitHistory: &off}}}
}

func (f mirrorFixture) cloneInfo() string {
	return filepath.Join(f.instance, "tools", "app", ".git", "objects", "info")
}

// logCount is how many lines the mirror log holds; a missing log holds none.
func (f mirrorFixture) logCount(t *testing.T) int {
	t.Helper()
	path := filepath.Join(filepath.Dir(mirror.StablePath(f.home, f.root, "app")), ".meta", "mirror.log")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	return len(f.logLines(t))
}

func (f mirrorFixture) ensure(t *testing.T, cfg *config.WorkspaceConfig) {
	t.Helper()
	var stderr bytes.Buffer
	f.applier(&stderr).ensureMirrors(context.Background(), cfg, f.classified, f.instance, "apply")
}

func TestEnsureMirrors_AnOffRepoWithABorrowerIsCheckedAndRestored(t *testing.T) {
	f := newMirrorFixture(t)
	f.ensure(t, offRepoConfig())
	if n := f.logCount(t); n != 2 {
		t.Fatalf("the mirror log has %d lines, want a check and a restore", n)
	}
	if _, err := mirror.LinkedStore(f.home, f.root, "app"); err != nil {
		t.Fatalf("the mirror wasn't restored for its borrower: %v", err)
	}
}

func TestEnsureMirrors_AnOffRepoWithNoBorrowerIsLeftAlone(t *testing.T) {
	f := newMirrorFixture(t)
	f.ensure(t, offRepoConfig())
	before := f.logCount(t)
	stable := mirror.StablePath(f.home, f.root, "app")
	link, err := os.Readlink(stable)
	if err != nil {
		t.Fatal(err)
	}
	// The borrower is dissociated: its alternates file is gone.
	if err := os.Remove(filepath.Join(f.cloneInfo(), "alternates")); err != nil {
		t.Fatal(err)
	}

	f.ensure(t, offRepoConfig())
	if got := f.logCount(t); got != before {
		t.Fatalf("the mirror log went from %d to %d lines; an off repo with no borrower was checked", before, got)
	}
	if now, err := os.Readlink(stable); err != nil || now != link {
		t.Fatalf("the mirror link is %q (%v), want %q unchanged", now, err, link)
	}

	// With the mirror deleted too, nothing restores it.
	if err := os.RemoveAll(filepath.Dir(stable)); err != nil {
		t.Fatal(err)
	}
	f.ensure(t, offRepoConfig())
	if _, err := os.Lstat(filepath.Dir(stable)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("something was made under the mirrors (%v)", err)
	}
}

func TestEnsureMirrors_AnOffRepoWhoseBorrowerIsMidDissociateIsChecked(t *testing.T) {
	f := newMirrorFixture(t)
	info := f.cloneInfo()
	if err := os.Rename(filepath.Join(info, "alternates"), filepath.Join(info, hostdir.AlternatesAside)); err != nil {
		t.Fatal(err)
	}
	f.ensure(t, offRepoConfig())
	if n := f.logCount(t); n != 2 {
		t.Fatalf("the mirror log has %d lines, want a check and a restore", n)
	}
}

func TestChecksMirrorOf(t *testing.T) {
	f := newMirrorFixture(t)
	clone := filepath.Join(f.instance, "tools", "app")
	if !checksMirrorOf(true, nil, f.home, f.root, "app") {
		t.Fatal("a repo with borrowing on lost its check")
	}
	if !checksMirrorOf(false, []string{clone}, f.home, f.root, "app") {
		t.Fatal("an off repo with a stable-path borrower isn't checked")
	}
	if checksMirrorOf(false, []string{clone}, f.home, f.root, "lib") {
		t.Fatal("an off repo nothing borrows from is checked")
	}
}
