package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// writeWorkspaceRoot creates a workspace root: a .niwa holding workspace.toml.
// A root is what config.Discover finds, and what distinguishes a root from an
// instance in ClassifyCwd.
func writeWorkspaceRoot(t *testing.T, root string) {
	t.Helper()
	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", niwaDir, err)
	}
	toml := "[workspace]\nname = \"test-ws\"\n"
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write workspace.toml: %v", err)
	}
}

// writeInstanceState writes .niwa/instance.json under dir with the given
// instance name. An empty name is what a registered `niwa init` writes at the
// root, and is the case IsSingleInstanceLayout must not mistake for an
// instance.
func writeInstanceState(t *testing.T, dir, instanceName string) {
	t.Helper()
	niwaDir := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", niwaDir, err)
	}
	st := map[string]any{"instance_name": instanceName}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "instance.json"), data, 0o644); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
}

// TestIsSingleInstanceLayout covers the distinction that makes the root
// refusal safe: a root that merely carries instance.json is not an instance,
// because every registered init writes one with no name.
func TestIsSingleInstanceLayout(t *testing.T) {
	t.Run("named instance at the root is single-instance", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceRoot(t, root)
		writeInstanceState(t, root, "solo")
		if !workspace.IsSingleInstanceLayout(root) {
			t.Fatal("want true for a root whose instance.json names an instance")
		}
	})

	t.Run("unnamed instance at the root is not single-instance", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceRoot(t, root)
		writeInstanceState(t, root, "")
		if workspace.IsSingleInstanceLayout(root) {
			t.Fatal("a freshly initialized root writes no instance_name; want false")
		}
	})

	t.Run("root with a child instance is not single-instance", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceRoot(t, root)
		writeInstanceState(t, root, "solo")
		writeInstanceState(t, filepath.Join(root, "child"), "child")
		if workspace.IsSingleInstanceLayout(root) {
			t.Fatal("a root with child instances is multi-instance even when named")
		}
	})

	t.Run("root with no state file is not single-instance", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceRoot(t, root)
		if workspace.IsSingleInstanceLayout(root) {
			t.Fatal("want false with no instance.json")
		}
	})

	t.Run("unreadable state is not single-instance", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceRoot(t, root)
		niwaDir := filepath.Join(root, ".niwa")
		if err := os.MkdirAll(niwaDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(niwaDir, "instance.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if workspace.IsSingleInstanceLayout(root) {
			t.Fatal("want false when the state cannot be parsed")
		}
	})
}

// TestDiscoverInstanceRoot_Layouts pins resolution across every layout a
// worktree command can run in. The multi-instance root row is the #292 bug:
// before the fix the walk found the root's own instance.json and returned the
// root, so every worktree command read the root's mapping store as if it held
// worktree records.
func TestDiscoverInstanceRoot_Layouts(t *testing.T) {
	// multi-instance root with one child instance
	multiRoot := t.TempDir()
	writeWorkspaceRoot(t, multiRoot)
	writeInstanceState(t, multiRoot, "")
	instanceDir := filepath.Join(multiRoot, "inst-a")
	writeInstanceState(t, instanceDir, "inst-a")
	repoClone := filepath.Join(instanceDir, "public", "niwa")
	if err := os.MkdirAll(repoClone, 0o755); err != nil {
		t.Fatalf("mkdir repo clone: %v", err)
	}
	niwaWorktree := filepath.Join(instanceDir, ".niwa", "worktrees", "niwa-abc12345")
	if err := os.MkdirAll(niwaWorktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	claudeWorktree := filepath.Join(repoClone, ".claude", "worktrees", "some-branch")
	if err := os.MkdirAll(claudeWorktree, 0o755); err != nil {
		t.Fatalf("mkdir claude worktree: %v", err)
	}

	// freshly initialized root: no children, unnamed instance state
	freshRoot := t.TempDir()
	writeWorkspaceRoot(t, freshRoot)
	writeInstanceState(t, freshRoot, "")

	// single-instance root: no children, named instance state
	soloRoot := t.TempDir()
	writeWorkspaceRoot(t, soloRoot)
	writeInstanceState(t, soloRoot, "solo")

	outside := t.TempDir()

	tests := []struct {
		name     string
		start    string
		want     string
		wantErr  error
		wantAny  bool // any error, matched loosely
		wantDesc string
	}{
		{name: "instance root", start: instanceDir, want: instanceDir},
		{name: "repo clone inside an instance", start: repoClone, want: instanceDir},
		{name: "niwa worktree", start: niwaWorktree, want: instanceDir},
		{name: "claude worktree under a repo", start: claudeWorktree, want: instanceDir},
		{name: "multi-instance root", start: multiRoot, wantErr: errAtWorkspaceRoot},
		{name: "freshly initialized root", start: freshRoot, wantErr: errAtWorkspaceRoot},
		{name: "single-instance root", start: soloRoot, want: soloRoot},
		{name: "outside any workspace", start: outside, wantAny: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := discoverInstanceRoot(tc.start)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got (%q, %v)", tc.wantErr, got, err)
				}
			case tc.wantAny:
				if err == nil {
					t.Fatalf("want an error, got %q", got)
				}
				if errors.Is(err, errAtWorkspaceRoot) {
					t.Fatal("outside a workspace must not report the root redirect")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("want %q, got %q", tc.want, got)
				}
			}
		})
	}
}

// TestResolveInstanceRoot_EnvOverrideWins checks NIWA_INSTANCE_ROOT is returned
// verbatim without classifying, including when cwd is a multi-instance root.
// niwa sets it only to an instance root, and dispatch relies on it.
func TestResolveInstanceRoot_EnvOverrideWins(t *testing.T) {
	multiRoot := t.TempDir()
	writeWorkspaceRoot(t, multiRoot)
	writeInstanceState(t, multiRoot, "")
	writeInstanceState(t, filepath.Join(multiRoot, "inst-a"), "inst-a")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(multiRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	t.Setenv("NIWA_INSTANCE_ROOT", "/explicit/instance/root")
	got, err := resolveInstanceRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/explicit/instance/root" {
		t.Fatalf("want the env value verbatim, got %q", got)
	}
}

// multiInstanceRootCwd builds a multi-instance root, chdirs into it for the
// duration of the test, and returns its path.
func multiInstanceRootCwd(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeWorkspaceRoot(t, root)
	writeInstanceState(t, root, "")
	writeInstanceState(t, filepath.Join(root, "inst-a"), "inst-a")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("NIWA_INSTANCE_ROOT", "")
	return root
}

// TestWorktreeListAtMultiInstanceRoot_RedirectsExitsZero is the pre-fix failure:
// listing at the root used to read the root's session mapping store as if the
// files were worktree records. It now prints a redirect and exits 0, with no
// table.
func TestWorktreeListAtMultiInstanceRoot_RedirectsExitsZero(t *testing.T) {
	root := multiInstanceRootCwd(t)

	// A mapping in the root store is what the old code would have listed.
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	mapping := `{"session_id":"6f1f8a0e-1f1a-4a3b-9c2d-5e6f70818283","instance_path":"` +
		filepath.Join(root, "inst-a") + `"}`
	if err := os.WriteFile(filepath.Join(sessionsDir, "m.json"), []byte(mapping), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	var out, errBuf bytes.Buffer
	sessionListCmd.SetOut(&out)
	sessionListCmd.SetErr(&errBuf)
	defer func() {
		sessionListCmd.SetOut(os.Stdout)
		sessionListCmd.SetErr(os.Stderr)
	}()

	if err := runSessionLifecycleList(sessionListCmd, "", "", false, false); err != nil {
		t.Fatalf("want exit 0 at the root, got error: %v", err)
	}
	if got := errBuf.String(); !strings.Contains(got, "niwa: "+atWorkspaceRootMessage) {
		t.Fatalf("stderr missing the redirect, got %q", got)
	}
	if strings.Contains(errBuf.String(), "error:") {
		t.Fatalf("the redirect must not read as an error, got %q", errBuf.String())
	}
	if got := out.String(); got != "" {
		t.Fatalf("want no table on stdout, got %q", got)
	}
}

// TestWorktreeListJSONAtMultiInstanceRoot_EmptyArray keeps --json parseable:
// a script reading stdout gets [] rather than nothing.
func TestWorktreeListJSONAtMultiInstanceRoot_EmptyArray(t *testing.T) {
	multiInstanceRootCwd(t)

	prevJSON := sessionListJSON
	sessionListJSON = true
	defer func() { sessionListJSON = prevJSON }()

	var out, errBuf bytes.Buffer
	sessionListCmd.SetOut(&out)
	sessionListCmd.SetErr(&errBuf)
	defer func() {
		sessionListCmd.SetOut(os.Stdout)
		sessionListCmd.SetErr(os.Stderr)
	}()

	if err := runSessionLifecycleList(sessionListCmd, "", "", false, false); err != nil {
		t.Fatalf("want exit 0, got error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Fatalf("want [] on stdout, got %q", got)
	}
	if !strings.Contains(errBuf.String(), atWorkspaceRootMessage) {
		t.Fatalf("stderr missing the redirect, got %q", errBuf.String())
	}
}

// TestCompletionAtMultiInstanceRootOffersNothing: a TAB press at the root must
// not offer worktree ids read out of the mapping store.
func TestCompletionAtMultiInstanceRootOffersNothing(t *testing.T) {
	multiInstanceRootCwd(t)

	ids, _ := completeSessionIDs(sessionListCmd, nil, "")
	if len(ids) != 0 {
		t.Fatalf("want no candidates at a multi-instance root, got %v", ids)
	}
}
