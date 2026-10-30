package workspace

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// newDotNamedRepoInstance builds an instance holding a declared dot-named
// repo (public/.github), an ordinary repo (public/niwa), and dot-named
// directories that must never count as repos: an undeclared one
// (public/.hidden), the reserved names (public/.niwa, public/.claude,
// public/.git) even though the state lists them, and a declared name that sits
// only under a dot-named group (.grp/.secret). Each has a .git entry, so only
// the name rules keep them out. declared lists the repo names the instance
// state records; nil writes no state file at all.
func newDotNamedRepoInstance(t *testing.T, declared []string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"public/.github",
		"public/niwa",
		"public/.hidden",
		"public/.niwa",
		"public/.claude",
		"public/.git",
		".grp/.secret",
	} {
		mustMkdir(t, filepath.Join(root, filepath.FromSlash(rel), ".git"))
	}
	if declared != nil {
		repos := map[string]RepoState{}
		for _, name := range declared {
			repos[name] = RepoState{Cloned: true}
		}
		if err := SaveState(root, &InstanceState{SchemaVersion: SchemaVersion, InstanceName: "inst", Repos: repos}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

var dotNamedDeclared = []string{".github", "niwa", ".niwa", ".claude", ".git", ".secret"}

func candidateNames(t *testing.T, root string) []string {
	t.Helper()
	candidates, err := enumerateRepoCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		names = append(names, c.name)
	}
	sort.Strings(names)
	return names
}

func indexNames(root string) []string {
	names := []string{}
	for name := range instanceRepoIndex(root) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestRepoWalks_DeclaredDotNamedRepo(t *testing.T) {
	root := newDotNamedRepoInstance(t, dotNamedDeclared)
	want := []string{".github", "niwa"}

	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("EnumerateRepos = %v, want %v", names, want)
	}
	if got := candidateNames(t, root); !reflect.DeepEqual(got, want) {
		t.Errorf("enumerateRepoCandidates = %v, want %v", got, want)
	}
	if got := indexNames(root); !reflect.DeepEqual(got, want) {
		t.Errorf("instanceRepoIndex = %v, want %v", got, want)
	}
	if got := instanceRepoIndex(root)[".github"]; got != filepath.Join(root, "public", ".github") {
		t.Errorf("instanceRepoIndex[.github] = %q", got)
	}
}

// TestRepoWalks_DotNamedRepoNotDeclared covers a dot-named repo directory the
// state doesn't list, and an instance with no state file: both skip it.
func TestRepoWalks_DotNamedRepoNotDeclared(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared []string
	}{
		{"not in state", []string{"niwa"}},
		{"no state file", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newDotNamedRepoInstance(t, tc.declared)
			want := []string{"niwa"}
			names, err := EnumerateRepos(root)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(names, want) {
				t.Errorf("EnumerateRepos = %v, want %v", names, want)
			}
			if got := candidateNames(t, root); !reflect.DeepEqual(got, want) {
				t.Errorf("enumerateRepoCandidates = %v, want %v", got, want)
			}
			if got := indexNames(root); !reflect.DeepEqual(got, want) {
				t.Errorf("instanceRepoIndex = %v, want %v", got, want)
			}
		})
	}
}

func TestResolveHookTarget_DeclaredDotNamedRepo(t *testing.T) {
	t.Setenv(WorktreeRepoEnv, "")
	root := newDotNamedRepoInstance(t, dotNamedDeclared)
	githubDir := filepath.Join(root, "public", ".github")

	// A reserved name or a name under a dot-named group is listed in the
	// state here, so the hook may refuse it as an uncloned repo rather than
	// as no repo; either refusal keeps it out, so those cases want any error.
	cases := []struct {
		name     string
		cwd      string
		hookName string
		wantRepo string
		wantErr  string
	}{
		{"repo prefix", root, ".github/fix-x", ".github", ""},
		{"group and repo prefix", root, "public/.github/fix-x", ".github", ""},
		{"cwd in the clone", githubDir, "fix-x", ".github", ""},
		{"undeclared dot directory", root, ".hidden/fix-x", "", "no repo selected"},
		{"reserved name", root, ".claude/fix-x", "", "repo"},
		{"dot-named group", root, ".grp/.secret/fix-x", "", "repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveHookTarget(root, tc.cwd, tc.hookName)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Repo != tc.wantRepo || got.Slug != "fix-x" {
				t.Errorf("got repo %q slug %q, want repo %q slug fix-x", got.Repo, got.Slug, tc.wantRepo)
			}
		})
	}
}

// TestBuildRootContext_DeclaredDotNamedRepo covers the repo list a session
// started at the instance root gets.
func TestBuildRootContext_DeclaredDotNamedRepo(t *testing.T) {
	root := newDotNamedRepoInstance(t, dotNamedDeclared)
	got, err := BuildRootContext(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "- public/.github\n") {
		t.Errorf("root context lacks public/.github:\n%s", got)
	}
	for _, absent := range []string{".hidden", ".niwa", ".claude", "/.git\n", ".secret"} {
		if strings.Contains(got, absent) {
			t.Errorf("root context lists %q:\n%s", absent, got)
		}
	}
}

// TestResolveHookScope_DeclaredDotNamedRepo covers the repo list the worktree
// guard names.
func TestResolveHookScope_DeclaredDotNamedRepo(t *testing.T) {
	root := newDotNamedRepoInstance(t, dotNamedDeclared)
	scope, err := ResolveHookScope(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".github", "niwa"}; !reflect.DeepEqual(scope.Repos, want) {
		t.Errorf("scope.Repos = %v, want %v", scope.Repos, want)
	}
}
