package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// makeRepo creates a repo directory at <instanceRoot>/<group>/<repo> with a
// .git marker so enumerateRepoCandidates picks it up, and returns its path.
func makeRepo(t *testing.T, instanceRoot, group, repo string) string {
	t.Helper()
	repoPath := filepath.Join(instanceRoot, group, repo)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repoPath
}

func TestResolveRepoNameFromCwd(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceRoot := destroySetupInstance(t, root, "alpha")

	publicNiwa := makeRepo(t, instanceRoot, "public", "niwa")
	makeRepo(t, instanceRoot, "public", "koto")
	privateTools := makeRepo(t, instanceRoot, "private", "tools")

	// A subdir inside the public/niwa repo.
	niwaSubdir := filepath.Join(publicNiwa, "internal", "workspace")
	if err := os.MkdirAll(niwaSubdir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantErr bool
	}{
		{
			name: "cwd is repo root",
			cwd:  publicNiwa,
			want: "niwa",
		},
		{
			name: "cwd is subdir of repo",
			cwd:  niwaSubdir,
			want: "niwa",
		},
		{
			name: "cwd is another repo root",
			cwd:  privateTools,
			want: "tools",
		},
		{
			name: "cwd with .. resolving into a repo",
			cwd:  filepath.Join(publicNiwa, "internal", "..", "internal", "workspace"),
			want: "niwa",
		},
		{
			name: "cwd with .. escaping to a sibling repo",
			cwd:  filepath.Join(publicNiwa, "..", "koto"),
			want: "koto",
		},
		{
			name:    "cwd at group dir (outside any repo)",
			cwd:     filepath.Join(instanceRoot, "public"),
			wantErr: true,
		},
		{
			name:    "cwd at instance root (outside any repo)",
			cwd:     instanceRoot,
			wantErr: true,
		},
		{
			name:    "cwd escaping the workspace entirely via ..",
			cwd:     filepath.Join(publicNiwa, "..", "..", ".."),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveRepoNameFromCwd(instanceRoot, tt.cwd)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got repo %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRepoNameFromCwd: %v", err)
			}
			if got != tt.want {
				t.Errorf("repo = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRepoNameFromCwd_OutsideWorkspace(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceRoot := destroySetupInstance(t, root, "alpha")
	makeRepo(t, instanceRoot, "public", "niwa")

	// A directory wholly outside the workspace must be rejected, never
	// returned as a best-effort guess.
	outside := t.TempDir()
	if _, err := ResolveRepoNameFromCwd(instanceRoot, outside); err == nil {
		t.Fatalf("expected rejection for out-of-workspace cwd %q", outside)
	}
}

func TestResolveRepoNameFromCwd_SymlinkedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := destroySetupWorkspace(t)
	instanceRoot := destroySetupInstance(t, root, "alpha")
	publicNiwa := makeRepo(t, instanceRoot, "public", "niwa")

	// A symlink that points INTO the repo must resolve to the repo.
	linkInto := filepath.Join(t.TempDir(), "link-into-niwa")
	if err := os.Symlink(publicNiwa, linkInto); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRepoNameFromCwd(instanceRoot, linkInto)
	if err != nil {
		t.Fatalf("ResolveRepoNameFromCwd via symlink into repo: %v", err)
	}
	if got != "niwa" {
		t.Errorf("repo = %q, want %q", got, "niwa")
	}

	// A symlink that points OUTSIDE every repo must be rejected even though
	// the link itself lives inside the workspace. Canonicalization is what
	// closes this spoofing gap.
	outside := t.TempDir()
	linkOut := filepath.Join(instanceRoot, "public", "spoof")
	if err := os.Symlink(outside, linkOut); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRepoNameFromCwd(instanceRoot, linkOut); err == nil {
		t.Fatalf("expected rejection for symlink pointing outside repos")
	}
}

func TestResolveRepoNameFromCwd_LongestPrefix(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceRoot := destroySetupInstance(t, root, "alpha")

	// enumerateRepoCandidates scans exactly two levels (group/repo), so the
	// candidate set cannot contain a repo nested under another repo's tree.
	// Exercise the prefix logic with two repos whose paths share a string
	// prefix ("repo" vs "repo-extended"): the component-aware guard must keep
	// "repo" from claiming a cwd under "repo-extended", and the longest-match
	// must win when a genuine prefix relationship exists.
	makeRepo(t, instanceRoot, "g", "repo")
	repoExtended := makeRepo(t, instanceRoot, "g", "repo-extended")

	sub := filepath.Join(repoExtended, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRepoNameFromCwd(instanceRoot, sub)
	if err != nil {
		t.Fatalf("ResolveRepoNameFromCwd: %v", err)
	}
	if got != "repo-extended" {
		t.Errorf("repo = %q, want %q (component-aware prefix must not match %q)", got, "repo-extended", "repo")
	}

	// And a cwd inside "repo" itself resolves to "repo", not "repo-extended".
	repoPath := filepath.Join(instanceRoot, "g", "repo")
	got2, err := ResolveRepoNameFromCwd(instanceRoot, repoPath)
	if err != nil {
		t.Fatalf("ResolveRepoNameFromCwd: %v", err)
	}
	if got2 != "repo" {
		t.Errorf("repo = %q, want %q", got2, "repo")
	}
}

func TestResolveRepoFromCwd(t *testing.T) {
	root := destroySetupWorkspace(t)
	instanceRoot := destroySetupInstance(t, root, "alpha")
	publicNiwa := makeRepo(t, instanceRoot, "public", "niwa")

	gotRoot, gotRepo, err := ResolveRepoFromCwd(publicNiwa)
	if err != nil {
		t.Fatalf("ResolveRepoFromCwd: %v", err)
	}
	if gotRepo != "niwa" {
		t.Errorf("repo = %q, want %q", gotRepo, "niwa")
	}
	// The discovered instance root should canonically match instanceRoot.
	wantCanon, err := canonicalize(instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	gotCanon, err := canonicalize(gotRoot)
	if err != nil {
		t.Fatal(err)
	}
	if gotCanon != wantCanon {
		t.Errorf("instanceRoot = %q, want %q", gotCanon, wantCanon)
	}
}

func TestResolveRepoFromCwd_OutsideInstance(t *testing.T) {
	// A cwd that is not under any niwa instance is rejected at the
	// DiscoverInstance step.
	if _, _, err := ResolveRepoFromCwd(t.TempDir()); err == nil {
		t.Fatal("expected rejection for cwd outside any niwa instance")
	}
}

// Guard against an accidentally-loose prefix check: a sibling whose name
// shares a string prefix with a repo must not be claimed by that repo.
func TestPathHasPrefix_ComponentAware(t *testing.T) {
	sep := string(filepath.Separator)
	parent := sep + "a" + sep + "b"
	cases := []struct {
		child string
		want  bool
	}{
		{parent, true},
		{parent + sep + "c", true},
		{sep + "a" + sep + "bc", false},
		{sep + "a", false},
	}
	for _, c := range cases {
		if got := pathHasPrefix(c.child, parent); got != c.want {
			t.Errorf("pathHasPrefix(%q, %q) = %v, want %v", c.child, parent, got, c.want)
		}
	}
}
