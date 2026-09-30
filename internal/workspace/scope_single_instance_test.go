package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// singleInstanceRoot builds the single-instance layout: the workspace root is
// itself the instance, with a named instance.json at the root and its repos
// directly under it.
func singleInstanceRoot(t *testing.T) (root, repo string) {
	t.Helper()
	root = setupWorkspace(t, nil)
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "solo",
		InstanceNumber: 1,
		Root:           root,
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(root, state); err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(root, "tools", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, repo
}

// In the single-instance layout every directory under the root is inside the
// instance, so none of them is below-root, and apply from a repo resolves to
// the same scope as apply at the root, as it did before the scope rule.
func TestResolveApplyScope_SingleInstanceLayoutRepoIsNotBelowRoot(t *testing.T) {
	root, repo := singleInstanceRoot(t)

	got, err := ClassifyCwd(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got.BelowRoot {
		t.Errorf("a repo in a single-instance root must not be flagged below-root: %+v", got)
	}
	fromRepo, err := ResolveApplyScope(repo, "")
	if err != nil {
		t.Fatalf("apply from a repo in a single-instance root should resolve: %v", err)
	}
	fromRoot, err := ResolveApplyScope(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if fromRepo.Mode != fromRoot.Mode || fromRepo.WorkspaceRoot != fromRoot.WorkspaceRoot || len(fromRepo.Instances) != len(fromRoot.Instances) {
		t.Errorf("repo scope %+v differs from root scope %+v", fromRepo, fromRoot)
	}
}
