package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestClaudeConfigArgs_SessionDaemon verifies Issue 4: for a session
// daemon (taskStoreRoot != instanceRoot), the --add-dir <repoPath> arg
// resolves to the workspace's repo dir under taskStoreRootDir(), not the
// worktree under instanceRoot. The worktree has no committed .claude/
// tree, so pointing --add-dir there would defeat the contract.
func TestClaudeConfigArgs_SessionDaemon(t *testing.T) {
	workspaceRoot := t.TempDir()
	worktreePath := t.TempDir()
	// Don't seed a repo dir under workspaceRoot — resolveRoleCWD falls back
	// to the workspace root itself when the role isn't found, which is fine
	// for this assertion.

	s := spawnContext{
		instanceRoot:  worktreePath,
		taskStoreRoot: workspaceRoot,
	}
	args := claudeConfigArgs(s, "myrepo")
	want := []string{
		"--add-dir", workspaceRoot,
		"--add-dir", workspaceRoot, // resolveRoleCWD fallback when role dir missing
		"--setting-sources", "user,project,local",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("session daemon args = %v\nwant %v", args, want)
	}
}

// TestClaudeConfigArgs_MainInstance verifies that for a main-instance
// daemon, the args resolve correctly. resolveRoleCWD scans groups under
// taskStoreRootDir for the role's repo dir; this test seeds one and
// asserts the path appears in the args.
func TestClaudeConfigArgs_MainInstance(t *testing.T) {
	workspaceRoot := t.TempDir()
	const groupName = "apps"
	const roleName = "web"
	repoPath := filepath.Join(workspaceRoot, groupName, roleName)
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}

	s := spawnContext{
		instanceRoot:  workspaceRoot,
		taskStoreRoot: "", // main-instance: falls through to instanceRoot
	}
	args := claudeConfigArgs(s, roleName)
	if len(args) != 6 {
		t.Fatalf("args len = %d, want 6: %v", len(args), args)
	}
	if args[0] != "--add-dir" || args[1] != workspaceRoot {
		t.Errorf("first --add-dir should target workspaceRoot %q; got %v", workspaceRoot, args[:2])
	}
	if args[2] != "--add-dir" {
		t.Errorf("flag 2 = %q, want --add-dir", args[2])
	}
	if !strings.Contains(args[3], groupName) || !strings.Contains(args[3], roleName) {
		t.Errorf("repo path %q should contain group %q and role %q", args[3], groupName, roleName)
	}
	if args[4] != "--setting-sources" {
		t.Errorf("flag 4 = %q, want --setting-sources", args[4])
	}
	if args[5] != "user,project,local" {
		t.Errorf("setting sources = %q, want user,project,local", args[5])
	}
}

// TestClaudeConfigArgs_FullStructure validates the full 6-element structure
// for any spawn-context shape.
func TestClaudeConfigArgs_FullStructure(t *testing.T) {
	workspaceRoot := t.TempDir()
	s := spawnContext{instanceRoot: workspaceRoot}
	args := claudeConfigArgs(s, "any-role")
	if len(args) != 6 {
		t.Fatalf("args len = %d, want 6: %v", len(args), args)
	}
	if args[0] != "--add-dir" || args[2] != "--add-dir" {
		t.Errorf("flags 0,2 must both be --add-dir; got %q, %q", args[0], args[2])
	}
	if args[4] != "--setting-sources" {
		t.Errorf("flag 4 = %q, want --setting-sources", args[4])
	}
	if args[5] != "user,project,local" {
		t.Errorf("setting sources = %q, want user,project,local", args[5])
	}
}
