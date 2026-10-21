package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// authorityWorkspace builds a workspace root whose config carries the given
// [workspace] lines and returns the root.
func authorityWorkspace(t *testing.T, workspaceLines string) string {
	t.Helper()
	root := t.TempDir()
	niwaDir := filepath.Join(root, workspace.StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[workspace]\nname = \"authority-ws\"\n" + workspaceLines
	if err := os.WriteFile(filepath.Join(niwaDir, workspace.WorkspaceConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// chdirForTest moves into dir for the rest of the test.
func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	saved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(saved) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

// runAuthorityShowIn runs `niwa authority show` with cwd at dir and the given
// raw personal config ("" for none), returning stdout and the error.
func runAuthorityShowIn(t *testing.T, dir, hostConfig string) (string, error) {
	t.Helper()
	path := permissionModeConfigPath(t)
	if hostConfig != "" {
		writeHostConfig(t, path, hostConfig)
	}
	chdirForTest(t, dir)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runAuthorityShow(cmd, nil)
	return out.String(), err
}

func TestAuthorityShow(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		host      string
		want      string
	}{
		{
			name:      "workspace supervised, machine strict",
			workspace: "authority_level = \"supervised\"\n",
			host:      "[global]\nauthority_level = \"strict\"\n",
			want:      "workspace: supervised\nmachine: strict\ndefault dispatch: strict from machine\n",
		},
		{
			name: "neither set",
			want: "workspace: not set\nmachine: not set\ndefault dispatch: autonomous from default\n",
		},
		{
			name:      "workspace only",
			workspace: "authority_level = \"strict\"\n",
			want:      "workspace: strict\nmachine: not set\ndefault dispatch: strict from workspace\n",
		},
		{
			name:      "machine wider than workspace",
			workspace: "authority_level = \"strict\"\n",
			host:      "[global]\nauthority_level = \"autonomous\"\n",
			want:      "workspace: strict\nmachine: autonomous\ndefault dispatch: strict from workspace\n",
		},
		{
			name:      "machine ties workspace",
			workspace: "authority_level = \"supervised\"\n",
			host:      "[global]\nauthority_level = \"supervised\"\n",
			want:      "workspace: supervised\nmachine: supervised\ndefault dispatch: supervised from machine\n",
		},
		{
			name:      "blank values read as not set",
			workspace: "authority_level = \"   \"\n",
			host:      "[global]\nauthority_level = \"\"\n",
			want:      "workspace: not set\nmachine: not set\ndefault dispatch: autonomous from default\n",
		},
		{
			name:      "whitespace around a value is trimmed",
			workspace: "authority_level = \" strict \"\n",
			want:      "workspace: strict\nmachine: not set\ndefault dispatch: strict from workspace\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := authorityWorkspace(t, tc.workspace)
			out, err := runAuthorityShowIn(t, root, tc.host)
			if err != nil {
				t.Fatalf("niwa authority show: %v", err)
			}
			if out != tc.want {
				t.Fatalf("stdout =\n%s\nwant\n%s", out, tc.want)
			}
		})
	}
}

// TestAuthorityShow_InsideAnInstance resolves the same workspace from inside
// one of its instances, the place a dispatched worker would run it.
func TestAuthorityShow_InsideAnInstance(t *testing.T) {
	root := authorityWorkspace(t, "authority_level = \"supervised\"\n")
	instanceDir := filepath.Join(root, "inst-1")
	if err := workspace.SaveState(instanceDir, &workspace.InstanceState{SchemaVersion: 1, InstanceName: "inst-1", Root: instanceDir}); err != nil {
		t.Fatal(err)
	}
	out, err := runAuthorityShowIn(t, instanceDir, "")
	if err != nil {
		t.Fatalf("niwa authority show inside an instance: %v", err)
	}
	if want := "workspace: supervised\nmachine: not set\ndefault dispatch: supervised from workspace\n"; out != want {
		t.Fatalf("stdout =\n%s\nwant\n%s", out, want)
	}
}

func TestAuthorityShow_OutsideAWorkspace(t *testing.T) {
	out, err := runAuthorityShowIn(t, t.TempDir(), "")
	if err == nil {
		t.Fatalf("niwa authority show outside a workspace succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not inside a niwa workspace") {
		t.Errorf("error %q does not name the missing workspace", err)
	}
	if out != "" {
		t.Errorf("printed to stdout on refusal: %q", out)
	}
}

// TestAuthorityShow_InvalidValues fails on either setting holding a bad value,
// naming it, rather than reporting it as not set.
func TestAuthorityShow_InvalidValues(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		host      string
		wantTable string
		wantValue string
	}{
		{"invalid workspace value", "authority_level = \"Strict\"\n", "", "workspace", "Strict"},
		{"invalid machine value", "authority_level = \"strict\"\n", "[global]\nauthority_level = \"STRICT\"\n", "global", "STRICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := authorityWorkspace(t, tc.workspace)
			out, err := runAuthorityShowIn(t, root, tc.host)
			if err == nil {
				t.Fatalf("niwa authority show succeeded:\n%s", out)
			}
			if out != "" {
				t.Errorf("printed to stdout on refusal: %q", out)
			}
			var ie *config.InvalidAuthorityLevelError
			if !errors.As(err, &ie) || ie.Table != tc.wantTable || ie.Value != tc.wantValue {
				t.Fatalf("error = %v, want an *InvalidAuthorityLevelError for [%s] %q", err, tc.wantTable, tc.wantValue)
			}
			msg := err.Error()
			for _, part := range append([]string{"authority_level", `"` + tc.wantValue + `"`}, authority.Names()...) {
				if !strings.Contains(msg, part) {
					t.Errorf("error %q does not mention %q", msg, part)
				}
			}
		})
	}
}

// TestAuthorityShow_UnreadableWorkspaceConfig refuses rather than showing the
// default, because a dispatch would not get the default from that file either.
func TestAuthorityShow_UnreadableWorkspaceConfig(t *testing.T) {
	root := authorityWorkspace(t, "authority_level = \"strict\n")
	if _, err := runAuthorityShowIn(t, root, ""); err == nil {
		t.Fatal("niwa authority show succeeded on a workspace config with a TOML syntax error")
	}
}

// TestRunApply_RefusesAnInvalidWorkspaceAuthorityLevel holds that apply stops
// on an invalid [workspace] authority_level before converging anything,
// since the config load it starts with fails on the value.
func TestRunApply_RefusesAnInvalidWorkspaceAuthorityLevel(t *testing.T) {
	root, instanceDir := setupRootScopeFixture(t)
	cfgPath := filepath.Join(root, workspace.StateDir, workspace.WorkspaceConfigFile)
	if err := os.WriteFile(cfgPath, []byte("[workspace]\nname = \"cascade-ws\"\nauthority_level = \"Strict\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runApplyAtRoot(t, root, false)
	var ie *config.InvalidAuthorityLevelError
	if !errors.As(err, &ie) {
		t.Fatalf("runApply error = %v, want an *InvalidAuthorityLevelError", err)
	}

	st, err := workspace.LoadState(instanceDir)
	if err != nil {
		t.Fatalf("loading instance state: %v", err)
	}
	if !st.LastApplied.IsZero() {
		t.Errorf("the instance was converged despite the invalid config (LastApplied %v)", st.LastApplied)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("root-managed settings were written despite the invalid config (err=%v)", err)
	}
}

func TestAuthorityCommandIsRegistered(t *testing.T) {
	var found *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == "authority" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("niwa has no authority command")
	}
	var show bool
	for _, c := range found.Commands() {
		if c.Name() == "show" {
			show = true
		}
	}
	if !show {
		t.Fatal("niwa authority has no show subcommand")
	}
}
