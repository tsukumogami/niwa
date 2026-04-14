package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// sandboxHome sets HOME and XDG_CONFIG_HOME to a fresh temp dir per test so
// that config loading reads from controlled state instead of the developer's
// real machine.
func sandboxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// writeRegistry writes a global config with the given workspace names as
// registry entries. Each entry's root is created on disk so downstream
// resolvers don't choke on "stale registry" errors.
func writeRegistry(t *testing.T, home string, workspaces map[string]string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "niwa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	for name, root := range workspaces {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		body += fmt.Sprintf("[registry.%q]\nsource = %q\nroot = %q\n\n",
			name, filepath.Join(root, ".niwa", "workspace.toml"), root)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// makeInstance creates a workspace instance directory with a valid
// instance.json, and optionally a set of repos under the given group.
// Returns the instance root path.
func makeInstance(t *testing.T, workspaceRoot, instanceName string, instanceNumber int, repos map[string][]string) string {
	t.Helper()
	instanceRoot := filepath.Join(workspaceRoot, instanceName)
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		InstanceName:   instanceName,
		InstanceNumber: instanceNumber,
		Root:           instanceRoot,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]workspace.RepoState{},
	}
	if err := workspace.SaveState(instanceRoot, state); err != nil {
		t.Fatal(err)
	}
	for group, names := range repos {
		for _, n := range names {
			if err := os.MkdirAll(filepath.Join(instanceRoot, group, n), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return instanceRoot
}

// workspaceSkeleton writes a minimal .niwa/workspace.toml at workspaceRoot
// so config.Discover can find it when walking up from cwd inside an
// instance.
func workspaceSkeleton(t *testing.T, workspaceRoot, name string) {
	t.Helper()
	niwaDir := filepath.Join(workspaceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("[workspace]\nname = %q\n", name)
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteWorkspaceNames_Prefix(t *testing.T) {
	home := sandboxHome(t)
	wsRoot := filepath.Join(home, "workspaces")
	writeRegistry(t, home, map[string]string{
		"alpha":    filepath.Join(wsRoot, "alpha"),
		"alphabet": filepath.Join(wsRoot, "alphabet"),
		"beta":     filepath.Join(wsRoot, "beta"),
	})

	out, directive := completeWorkspaceNames(&cobra.Command{}, nil, "alp")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	want := []string{"alpha", "alphabet"}
	if !slices.Equal(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

func TestCompleteWorkspaceNames_EmptyRegistry(t *testing.T) {
	sandboxHome(t)
	out, directive := completeWorkspaceNames(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
}

func TestCompleteWorkspaceNames_MissingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	out, directive := completeWorkspaceNames(&cobra.Command{}, nil, "a")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
}

func TestCompleteInstanceNames_InsideWorkspace(t *testing.T) {
	sandboxHome(t)
	wsRoot := t.TempDir()
	workspaceSkeleton(t, wsRoot, "myws")
	makeInstance(t, wsRoot, "myws", 1, nil)
	makeInstance(t, wsRoot, "myws-2", 2, nil)
	makeInstance(t, wsRoot, "myws-hotfix", 3, nil)

	// cd into workspace root for completion context.
	t.Chdir(wsRoot)

	out, directive := completeInstanceNames(&cobra.Command{}, nil, "myws")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	want := []string{"myws", "myws-2", "myws-hotfix"}
	if !slices.Equal(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

func TestCompleteInstanceNames_OutsideAnyWorkspace(t *testing.T) {
	sandboxHome(t)
	// cwd is a temp dir with no workspace.toml above it.
	t.Chdir(t.TempDir())

	out, directive := completeInstanceNames(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) != 0 {
		t.Errorf("got %v, want empty", out)
	}
}

func TestCompleteRepoNames_FromCurrentInstance(t *testing.T) {
	sandboxHome(t)
	wsRoot := t.TempDir()
	workspaceSkeleton(t, wsRoot, "myws")
	instanceRoot := makeInstance(t, wsRoot, "myws", 1, map[string][]string{
		"group-a": {"api", "web"},
	})

	t.Chdir(instanceRoot)

	out, directive := completeRepoNames(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	want := []string{"api", "web"}
	if !slices.Equal(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

func TestCompleteRepoNames_UsesWorkspaceFlag(t *testing.T) {
	home := sandboxHome(t)
	wsRoot := filepath.Join(home, "wss")
	workspaceSkeleton(t, wsRoot, "alpha")
	// Sorted-first instance of "alpha" has api + web.
	makeInstance(t, wsRoot, "alpha", 1, map[string][]string{
		"group": {"api", "web"},
	})
	// A second instance with a different repo set; must NOT appear.
	makeInstance(t, wsRoot, "alpha-2", 2, map[string][]string{
		"group": {"other"},
	})
	writeRegistry(t, home, map[string]string{"alpha": wsRoot})

	// Simulate `go -w alpha -r <tab>`: build a cobra command with -w set.
	cmd := &cobra.Command{Use: "go"}
	cmd.Flags().StringP("workspace", "w", "", "")
	_ = cmd.Flags().Set("workspace", "alpha")

	// cwd is outside any workspace so cwd-based fallback isn't triggered.
	t.Chdir(t.TempDir())

	out, directive := completeRepoNames(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	want := []string{"api", "web"}
	if !slices.Equal(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
	// "other" exists only in the second instance and must NOT appear.
	for _, n := range out {
		if n == "other" {
			t.Errorf("repo from non-first instance leaked into completion: %v", out)
		}
	}
}

func TestCompleteGoTarget_UnionAndDecoration(t *testing.T) {
	home := sandboxHome(t)
	wsRoot := filepath.Join(home, "wss")
	workspaceSkeleton(t, wsRoot, "alpha")
	instanceRoot := makeInstance(t, wsRoot, "alpha", 1, map[string][]string{
		"group": {"tsuku", "api"},
	})
	writeRegistry(t, home, map[string]string{
		"alpha":    wsRoot,
		"codespar": filepath.Join(home, "codespar"),
		"tsuku":    filepath.Join(home, "tsuku"),
	})

	t.Chdir(instanceRoot)

	out, directive := completeGoTarget(&cobra.Command{}, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}

	// Repos show up with "repo in 1" description; workspaces with "workspace".
	// The "tsuku" name should appear twice (once as repo, once as workspace).
	wantContains := []string{
		"api\trepo in 1",
		"tsuku\trepo in 1",
		"tsuku\tworkspace",
		"alpha\tworkspace",
		"codespar\tworkspace",
	}
	for _, want := range wantContains {
		if !slices.Contains(out, want) {
			t.Errorf("expected %q in output, got %v", want, out)
		}
	}

	// Count "tsuku\t" prefixes: collision must produce two entries.
	count := 0
	for _, c := range out {
		if len(c) >= 6 && c[:6] == "tsuku\t" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected two entries for 'tsuku', got %d (full: %v)", count, out)
	}
}

func TestCompleteGoTarget_PrefixFilter(t *testing.T) {
	home := sandboxHome(t)
	writeRegistry(t, home, map[string]string{
		"alpha":    filepath.Join(home, "alpha"),
		"alphabet": filepath.Join(home, "alphabet"),
		"beta":     filepath.Join(home, "beta"),
	})

	// cwd is outside any instance -- only workspace half should contribute.
	t.Chdir(t.TempDir())

	out, _ := completeGoTarget(&cobra.Command{}, nil, "alp")
	for _, c := range out {
		if len(c) < 3 || c[:3] != "alp" {
			t.Errorf("prefix filter failed: %q", c)
		}
	}
	// Must include both alphabet/alpha but not beta.
	found := map[string]bool{}
	for _, c := range out {
		// strip description
		name := c
		for i := 0; i < len(c); i++ {
			if c[i] == '\t' {
				name = c[:i]
				break
			}
		}
		found[name] = true
	}
	if !found["alpha"] || !found["alphabet"] {
		t.Errorf("missing expected names: got %v", out)
	}
	if found["beta"] {
		t.Errorf("unexpected 'beta' in output: %v", out)
	}
}

func TestCompleteGoTarget_NoArgs_EmptyWhenNothingMatches(t *testing.T) {
	sandboxHome(t)
	t.Chdir(t.TempDir())
	out, directive := completeGoTarget(&cobra.Command{}, nil, "zzz")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}
